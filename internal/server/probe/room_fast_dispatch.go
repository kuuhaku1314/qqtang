package probe

import (
	"encoding/binary"
	"fmt"
	"time"

	"qqtang/internal/clientdata/sceneelement"
	"qqtang/internal/game/match"
	roomstate "qqtang/internal/game/room"
	"qqtang/internal/protocol/capture"
	"qqtang/internal/protocol/game"
)

type roomFastUDPRecipient struct {
	PlayerID uint16
	UIN      uint32
	Route    game.LegacyUDPEndpoint
	Endpoint roomPeerUDPEndpoint
}

// handleRoomFastPacket converts the authenticated TCP 0x0065 upload into the
// QQTPPP Type-2 datagrams consumed by each peer's mode-1 socket. Waiting-room
// packets contain ROOM_MSG_DATA; active matches contain QQT_DATA_PACKAGE.
// The uploader envelope itself is never a valid TCP downlink.
func (server *Server) handleRoomFastPacket(session *connectionSession, connectionID string, packet []byte) bool {
	type fastResult struct {
		keepConnection bool
		afterUnlock    func()
	}
	dispatch := func() (fastResult, error) {
		session.mu.Lock()
		defer session.mu.Unlock()
		keepConnection, afterUnlock := server.handleRoomFastPacketLocked(session, connectionID, packet)
		return fastResult{keepConnection: keepConnection, afterUnlock: afterUnlock}, nil
	}
	roomID := sessionRoomActorID(session)
	var result fastResult
	var err error
	if roomID != 0 {
		result, err = callRoomActor(server, roomID, "room-fast-direct", dispatch)
	} else {
		result, err = dispatch()
	}
	if err != nil {
		server.log(logEvent{Level: "error", Event: "room_actor_command_failed", ConnectionID: connectionID, RoomID: fmt.Sprint(roomID), Result: "room_fast_direct", ErrorContext: err.Error()})
		return false
	}
	keepConnection, afterUnlock := result.keepConnection, result.afterUnlock
	if afterUnlock != nil {
		afterUnlock()
	}
	return keepConnection
}

// handleRoomFastPacketLocked performs the request-serialized half of room-fast
// handling. The caller owns session.mu and is already executing on the room
// actor. The public wrapper above establishes both boundaries for direct
// integration tests and non-socket callers.
func (server *Server) handleRoomFastPacketLocked(session *connectionSession, connectionID string, packet []byte) (bool, func()) {
	inspection, err := game.InspectRoomFastPacket(packet)
	if err != nil {
		server.log(logEvent{
			Level: "warn", Event: "room_fast_packet_rejected", ConnectionID: connectionID,
			MessageLength: len(packet), Result: "invalid_envelope", ErrorContext: err.Error(),
		})
		return true, nil
	}
	if session == nil || session.UIN == 0 || session.liveUIN.Load() != session.UIN {
		err = fmt.Errorf("room fast packet requires an authenticated primary session")
	} else if inspection.EnvelopeUIN != session.UIN {
		err = fmt.Errorf("room fast envelope UIN %d != authenticated UIN %d", inspection.EnvelopeUIN, session.UIN)
	}
	var (
		snapshot          roomstate.Snapshot
		member            roomstate.Member
		competitiveBattle *match.CompetitiveBattle
		adventureBattle   *match.AdventureBattle
		fastSettlement    *competitiveRoomSettlementCommit
	)
	if err == nil {
		var state *roomstate.State
		state, err = server.sessionRoom(session)
		if err == nil {
			snapshot = state.Snapshot()
			switch inspection.PayloadKind {
			case game.RoomFastPayloadWaitingRoom:
				if snapshot.Phase != roomstate.PhasePreparing {
					err = fmt.Errorf("room %d phase %d does not accept waiting-room events", snapshot.RoomID, snapshot.Phase)
				}
			case game.RoomFastPayloadGameplay:
				if snapshot.Phase != roomstate.PhaseInMatch || snapshot.ActiveGameID == 0 || session.CurrentGameID != snapshot.ActiveGameID {
					err = fmt.Errorf("room %d game %d is not the sender's active match", snapshot.RoomID, snapshot.ActiveGameID)
				}
			default:
				err = fmt.Errorf("room fast payload kind %d is unsupported", inspection.PayloadKind)
			}
		}
	}
	if err == nil {
		for _, candidate := range snapshot.Members {
			if candidate.PlayerID == session.Profile.PlayerID {
				member = candidate
				break
			}
		}
		if member.PlayerID == 0 {
			err = fmt.Errorf("player %d is absent from room %d", session.Profile.PlayerID, snapshot.RoomID)
		} else if inspection.PayloadKind == game.RoomFastPayloadWaitingRoom {
			err = validateRoomFastEvents(inspection.Events, member.SeatID)
		} else {
			competitiveBattle, _ = server.competitiveBattle(session.CurrentGameID)
			adventureBattle, _ = server.adventureBattle(session.CurrentGameID)
			err = validateGameplayFastPackagesForMatch(inspection.GameplayPackages, member.PlayerID, session.gameplayGameID(), competitiveBattle, adventureBattle)
			if err == nil && competitiveBattle != nil {
				inspection.GameplayPackages, err = normalizeCompetitiveNativeRequestFastPackages(inspection.GameplayPackages, competitiveBattle)
			}
			if err == nil && competitiveBattle != nil {
				inspection.GameplayPackages, fastSettlement, err = normalizeCompetitiveDurabilityFastPackages(inspection.GameplayPackages, competitiveBattle)
			}
			if err == nil && competitiveBattle != nil && fastSettlement == nil {
				inspection.GameplayPackages, fastSettlement, err = normalizeCompetitiveObjectiveFastPackages(inspection.GameplayPackages, competitiveBattle)
			}
			if err == nil && competitiveBattle != nil && fastSettlement == nil {
				inspection.GameplayPackages, fastSettlement, err = normalizeCompetitiveTreasureFastPackages(inspection.GameplayPackages, competitiveBattle)
			}
			if err == nil && competitiveBattle != nil && fastSettlement == nil {
				inspection.GameplayPackages, err = normalizeCompetitiveNativeRelayFastPackages(inspection.GameplayPackages, competitiveBattle)
			}
			if err == nil && competitiveBattle != nil && fastSettlement == nil {
				fastSettlement, err = consumeCompetitiveFastPackages(inspection.GameplayPackages, competitiveBattle)
			}
		}
	}
	if err != nil {
		server.log(logEvent{
			Level: "warn", Event: "room_fast_packet_rejected", ConnectionID: connectionID,
			AccountID: fmt.Sprint(session.UIN), RoomID: fmt.Sprint(session.RoomID),
			MessageLength: len(packet), Result: "unauthorized_event", ErrorContext: err.Error(),
		})
		return true, nil
	}
	if inspection.PayloadKind == game.RoomFastPayloadGameplay {
		// QQTPPP has already sent this same CSendPackageInRoom batch over its
		// Type-2 peer UDP socket. 0x0065 is the authenticated server mirror used
		// for validation, accounting and settlement; converting it into either a
		// second Type-2 downlink or standalone 0x0084 messages changes the native
		// channel/order contract and makes non-idempotent scene rules diverge.
		// Keep the original byte-identical Type-2 batch as the sole scene path.
		messageCount := 0
		for _, gameplay := range inspection.GameplayPackages {
			messageCount += len(gameplay.Messages)
		}
		server.log(logEvent{
			Level: "info", Event: "room_fast_gameplay_accounted", ConnectionID: connectionID,
			AccountID: fmt.Sprint(session.UIN), RoomID: fmt.Sprint(snapshot.RoomID),
			MessageLength: len(packet), Result: fmt.Sprintf("packages_%d_messages_%d", len(inspection.GameplayPackages), messageCount),
		})
		return true, server.competitiveFastSettlementAfterUnlock(session, connectionID, snapshot.RoomID, fastSettlement)
	}

	wantedRecipients := make(map[uint16]struct{}, len(inspection.RecipientPlayerIDs))
	activePlayerIDs := make(map[uint16]struct{})
	nativeRequestOnly := competitiveBattle != nil && containsOnlyCompetitiveNativeRequests(inspection.GameplayPackages)
	if inspection.PayloadKind == game.RoomFastPayloadGameplay {
		// The room state is the authoritative active-match membership. Do not
		// walk and lock peer connection sessions here: every TCP dispatcher
		// already owns its sender's session lock, so two simultaneous fast
		// packets otherwise acquire A->B and B->A and freeze the whole room.
		// Between-stage eliminations and explicit departures update this same
		// snapshot before later fast packets are authorized.
		for _, active := range snapshot.Members {
			if active.PlayerID != 0 {
				activePlayerIDs[active.PlayerID] = struct{}{}
			}
		}
	}
	for _, playerID := range inspection.RecipientPlayerIDs {
		if nativeRequestOnly && playerID != competitiveBattle.ArbitratorPlayerID() {
			continue
		}
		if playerID == member.PlayerID {
			err = fmt.Errorf("room fast packet cannot target its sender player %d", playerID)
			break
		}
		found := false
		for _, candidate := range snapshot.Members {
			if candidate.PlayerID == playerID {
				found = true
				break
			}
		}
		if inspection.PayloadKind == game.RoomFastPayloadGameplay {
			_, found = activePlayerIDs[playerID]
		}
		if !found && inspection.PayloadKind != game.RoomFastPayloadGameplay {
			err = fmt.Errorf("room fast recipient player %d is absent from room %d", playerID, snapshot.RoomID)
			break
		}
		if !found {
			// The original client may already have queued a batch when a target
			// leaves or is settled out between adventure stages. Skip that stale
			// identity while retaining every current peer in the same upload.
			continue
		}
		wantedRecipients[playerID] = struct{}{}
	}
	if err != nil {
		server.log(logEvent{
			Level: "warn", Event: "room_fast_packet_rejected", ConnectionID: connectionID,
			AccountID: fmt.Sprint(session.UIN), RoomID: fmt.Sprint(session.RoomID),
			MessageLength: len(packet), Result: "invalid_recipients", ErrorContext: err.Error(),
		})
		return true, nil
	}

	recipientPlayerIDs := make([]uint16, 0, len(wantedRecipients))
	for _, playerID := range inspection.RecipientPlayerIDs {
		if _, current := wantedRecipients[playerID]; current {
			recipientPlayerIDs = append(recipientPlayerIDs, playerID)
		}
	}
	if len(recipientPlayerIDs) == 0 {
		server.log(logEvent{Level: "info", Event: "room_fast_udp_pending", ConnectionID: connectionID, AccountID: fmt.Sprint(session.UIN), RoomID: fmt.Sprint(snapshot.RoomID), Result: "no_current_recipient"})
		return true, server.competitiveFastSettlementAfterUnlock(session, connectionID, snapshot.RoomID, fastSettlement)
	}
	recipients, err := server.resolveRoomFastUDPRecipients(snapshot.RoomID, session.UIN, recipientPlayerIDs, wantedRecipients)
	if err != nil {
		server.log(logEvent{
			Level: "info", Event: "room_fast_udp_pending", ConnectionID: connectionID,
			AccountID: fmt.Sprint(session.UIN), RoomID: fmt.Sprint(snapshot.RoomID),
			MessageLength: len(packet), Result: "mode_1_presence_unavailable", ErrorContext: err.Error(),
		})
		return true, server.competitiveFastSettlementAfterUnlock(session, connectionID, snapshot.RoomID, fastSettlement)
	}
	payloads := make([][]byte, 0, len(inspection.Events)+len(inspection.GameplayPackages))
	if inspection.PayloadKind == game.RoomFastPayloadGameplay {
		for _, gameplay := range inspection.GameplayPackages {
			payload, marshalErr := gameplay.MarshalNetworkBinary()
			if marshalErr != nil {
				err = marshalErr
				break
			}
			payloads = append(payloads, payload)
		}
	} else {
		for _, event := range inspection.Events {
			payload, marshalErr := event.MarshalRoomMessageData()
			if marshalErr != nil {
				err = marshalErr
				break
			}
			payloads = append(payloads, payload)
		}
	}
	if err != nil {
		server.log(logEvent{Level: "warn", Event: "room_fast_packet_rejected", ConnectionID: connectionID, AccountID: fmt.Sprint(session.UIN), RoomID: fmt.Sprint(snapshot.RoomID), Result: "invalid_payload", ErrorContext: err.Error()})
		return true, nil
	}
	delivered, sendErr := server.sendRoomFastUDPPayloads(session, snapshot.RoomID, payloads, recipients)
	if sendErr != nil {
		server.log(logEvent{
			Level: "warn", Event: "room_fast_udp_delivery_failed", ConnectionID: connectionID,
			AccountID: fmt.Sprint(session.UIN), RoomID: fmt.Sprint(snapshot.RoomID),
			MessageLength: len(packet), Result: fmt.Sprintf("events_%d_datagrams_%d", len(inspection.Events), delivered),
			ErrorContext: sendErr.Error(),
		})
		return true, server.competitiveFastSettlementAfterUnlock(session, connectionID, snapshot.RoomID, fastSettlement)
	}
	server.log(logEvent{
		Level: "info", Event: "room_fast_udp_relay", ConnectionID: connectionID,
		AccountID: fmt.Sprint(session.UIN), RoomID: fmt.Sprint(snapshot.RoomID),
		MessageLength: len(packet), Result: fmt.Sprintf("kind_%d_payloads_%d_peers_%d_datagrams_%d", inspection.PayloadKind, len(payloads), len(recipients), delivered),
	})
	return true, server.competitiveFastSettlementAfterUnlock(session, connectionID, snapshot.RoomID, fastSettlement)
}

func isCompetitiveNativeRequest(schema uint32) bool {
	return schema == game.RequestGetItem || schema == game.RequestGetBun || schema == game.RequestPutBun
}

func containsOnlyCompetitiveNativeRequests(packages []game.GameplayDataPackage) bool {
	found := false
	for _, packet := range packages {
		for _, message := range packet.Messages {
			if !isCompetitiveNativeRequest(message.DataID) {
				return false
			}
			found = true
		}
	}
	return found
}

// normalizeCompetitiveNativeRequestFastPackages preserves the original
// single-arbitrator topology. A request produced by that same local rule owner
// is consumed locally and peers must see only its later NOTIFY. Requests from
// other participants remain for delivery exclusively to the arbitrator.
func normalizeCompetitiveNativeRequestFastPackages(packages []game.GameplayDataPackage, battle *match.CompetitiveBattle) ([]game.GameplayDataPackage, error) {
	if battle == nil {
		return packages, nil
	}
	filtered := make([]game.GameplayDataPackage, 0, len(packages))
	for _, packet := range packages {
		if len(packet.MessageIndexes) != len(packet.Messages) {
			return nil, fmt.Errorf("native-request fast package index count %d != message count %d", len(packet.MessageIndexes), len(packet.Messages))
		}
		localRuleOwner := packet.PlayerID == battle.ArbitratorPlayerID() || battle.HasBossEntity(packet.PlayerID) || battle.HasNativeNPCEntity(packet.PlayerID)
		clone := packet
		clone.MessageIndexes = make([]uint32, 0, len(packet.MessageIndexes))
		clone.Messages = make([]game.BattleMessageData, 0, len(packet.Messages))
		for index, message := range packet.Messages {
			if localRuleOwner && isCompetitiveNativeRequest(message.DataID) {
				continue
			}
			message.Data = append([]byte(nil), message.Data...)
			clone.MessageIndexes = append(clone.MessageIndexes, packet.MessageIndexes[index])
			clone.Messages = append(clone.Messages, message)
		}
		if len(clone.Messages) != 0 {
			filtered = append(filtered, clone)
		}
	}
	return filtered, nil
}

// normalizeCompetitiveDurabilityFastPackages applies the same rule-7/8
// boundary as the reliable path before bytes are re-encoded for peers. The
// client reports 0x0FA7 after every non-avatar harm, but only applies it
// locally when its four-drop durability reached zero. Premature reports must
// therefore be removed from the outgoing package, not merely ignored by the
// server-side settlement state.
func normalizeCompetitiveDurabilityFastPackages(packages []game.GameplayDataPackage, battle *match.CompetitiveBattle) ([]game.GameplayDataPackage, *competitiveRoomSettlementCommit, error) {
	if battle == nil || !battle.UsesNativeDurability() {
		return packages, nil, nil
	}
	filtered := make([]game.GameplayDataPackage, 0, len(packages))
	var settlement *competitiveRoomSettlementCommit
	for _, packet := range packages {
		if len(packet.MessageIndexes) != len(packet.Messages) {
			return nil, nil, fmt.Errorf("native-durability fast package index count %d != message count %d", len(packet.MessageIndexes), len(packet.Messages))
		}
		clone := packet
		clone.MessageIndexes = make([]uint32, 0, len(packet.MessageIndexes))
		clone.Messages = make([]game.BattleMessageData, 0, len(packet.Messages))
		for index, message := range packet.Messages {
			keep := true
			if message.DataID <= 0xffff {
				event := game.GameEvent{Schema: uint16(message.DataID), Body: message.Data}
				switch event.Schema {
				case game.PlayerBeHarmed:
					harmed, err := game.ParseEntityHarmedEvent(event)
					if err != nil {
						return nil, nil, err
					}
					if battle.HasParticipant(harmed.ObjectID) {
						_, fresh, err := battle.RecordNativeHarm(harmed.ObjectID, harmed.Time, harmed.PosX, harmed.PosY, harmed.IsAvatar, harmed.LossHP)
						if err != nil {
							return nil, nil, err
						}
						keep = fresh
					}
				case game.NotifyPlayerDieEvent:
					death, err := game.ParsePlayerDeathEvent(event)
					if err != nil {
						return nil, nil, err
					}
					if battle.HasParticipant(death.PlayerID) {
						if !battle.AuthorizesNativeDurabilityDeathReporter(packet.PlayerID, death.PlayerID) {
							return nil, nil, fmt.Errorf("native-durability fast death source %d cannot report player %d", packet.PlayerID, death.PlayerID)
						}
						resolution, authoritative, err := battle.RecordNativeDurabilityDeath(death.PlayerID, death.ClientTime, death.PosX, death.PosY)
						if err != nil {
							return nil, nil, err
						}
						keep = authoritative
						if resolution.NewlyConcluded && settlement == nil {
							settlement = &competitiveRoomSettlementCommit{GameOver: competitiveGameOverData(death.ClientTime, resolution), Battle: battle}
						}
					}
				}
			}
			if keep {
				message.Data = append([]byte(nil), message.Data...)
				clone.MessageIndexes = append(clone.MessageIndexes, packet.MessageIndexes[index])
				clone.Messages = append(clone.Messages, message)
			}
		}
		if len(clone.Messages) != 0 {
			filtered = append(filtered, clone)
		}
	}
	return filtered, settlement, nil
}

// normalizeCompetitiveObjectiveFastPackages applies the same elected-
// arbitrator and exactly-once boundary as the reliable 0x0FB7/0x0FB9 path.
// Rule 3 (buns) and rule 6 (sculpture) share this native packet family; exact
// retries are removed before peer relay so clients cannot apply one pickup or
// deposit twice.
func normalizeCompetitiveObjectiveFastPackages(packages []game.GameplayDataPackage, battle *match.CompetitiveBattle) ([]game.GameplayDataPackage, *competitiveRoomSettlementCommit, error) {
	if battle == nil || (!battle.UsesBunObjective() && !battle.UsesSculptureObjective()) {
		return packages, nil, nil
	}
	filtered := make([]game.GameplayDataPackage, 0, len(packages))
	var settlement *competitiveRoomSettlementCommit
	for _, packet := range packages {
		if len(packet.MessageIndexes) != len(packet.Messages) {
			return nil, nil, fmt.Errorf("objective fast package index count %d != message count %d", len(packet.MessageIndexes), len(packet.Messages))
		}
		clone := packet
		clone.MessageIndexes = make([]uint32, 0, len(packet.MessageIndexes))
		clone.Messages = make([]game.BattleMessageData, 0, len(packet.Messages))
		for index, message := range packet.Messages {
			keep := true
			if message.DataID == game.NotifyBombExplode && battle.UsesSculptureObjective() {
				if err := recordCompetitiveSculptureExplosion(battle, packet.PlayerID, game.GameEvent{Schema: game.NotifyBombExplode, Body: message.Data}); err != nil {
					return nil, nil, err
				}
			}
			if message.DataID == game.NotifyPlayerGetBun || message.DataID == game.NotifyPlayerPutBun {
				if packet.PlayerID != battle.ArbitratorPlayerID() {
					return nil, nil, fmt.Errorf("objective fast-notify source %d is not arbitrator %d", packet.PlayerID, battle.ArbitratorPlayerID())
				}
				event := game.GameEvent{Schema: uint16(message.DataID), Body: message.Data}
				action, err := game.ParseBunActionEvent(event)
				if err != nil {
					return nil, nil, err
				}
				var resolution match.CompetitiveResolution
				var fresh bool
				if battle.UsesBunObjective() {
					resolution, fresh, err = battle.RecordBunAction(
						event.Schema == game.NotifyPlayerGetBun, action.PlayerID, action.ClientTime,
						action.PosX, action.PosY, action.BunID, action.BunTeamID,
					)
				} else {
					resolution, fresh, err = battle.RecordSculptureAction(
						event.Schema == game.NotifyPlayerGetBun, action.PlayerID, action.ClientTime,
						action.PosX, action.PosY, action.BunID, action.BunTeamID,
					)
				}
				if err != nil {
					return nil, nil, err
				}
				keep = fresh
				if resolution.NewlyConcluded && settlement == nil {
					settlement = &competitiveRoomSettlementCommit{
						GameOver: competitiveGameOverData(action.ClientTime, resolution), Battle: battle,
					}
				}
			}
			if keep {
				message.Data = append([]byte(nil), message.Data...)
				clone.MessageIndexes = append(clone.MessageIndexes, packet.MessageIndexes[index])
				clone.Messages = append(clone.Messages, message)
			}
		}
		if len(clone.Messages) != 0 {
			filtered = append(filtered, clone)
		}
	}
	return filtered, settlement, nil
}

// normalizeCompetitiveTreasureFastPackages reconciles the original client's
// mixed UDP/TCP delivery for rule 5. Gem pickup notifications may be fast-only,
// so they must participate in score accounting here. Death-drop notifications
// are emitted on both transports; the reliable copy is retained as the single
// peer scene mutation to avoid creating duplicate gem objects.
func normalizeCompetitiveTreasureFastPackages(packages []game.GameplayDataPackage, battle *match.CompetitiveBattle) ([]game.GameplayDataPackage, *competitiveRoomSettlementCommit, error) {
	if battle == nil || !battle.UsesTreasureObjective() {
		return packages, nil, nil
	}
	filtered := make([]game.GameplayDataPackage, 0, len(packages))
	var settlement *competitiveRoomSettlementCommit
	for _, packet := range packages {
		if len(packet.MessageIndexes) != len(packet.Messages) {
			return nil, nil, fmt.Errorf("treasure fast package index count %d != message count %d", len(packet.MessageIndexes), len(packet.Messages))
		}
		clone := packet
		clone.MessageIndexes = make([]uint32, 0, len(packet.MessageIndexes))
		clone.Messages = make([]game.BattleMessageData, 0, len(packet.Messages))
		for index, message := range packet.Messages {
			keep := true
			switch message.DataID {
			case game.NotifyPlayerGetItem:
				event := game.GameEvent{Schema: game.NotifyPlayerGetItem, Body: message.Data}
				item, err := game.ParsePlayerItemEvent(event)
				if err != nil {
					return nil, nil, err
				}
				if match.IsTreasureGem(item.ItemID) && battle.HasParticipant(item.PlayerID) {
					resolution, fresh, recordErr := battle.RecordTreasurePickup(item.PlayerID, item.ClientTime, item.ItemID, item.PosX, item.PosY)
					if recordErr != nil {
						return nil, nil, recordErr
					}
					keep = fresh
					if resolution.NewlyConcluded && settlement == nil {
						settlement = &competitiveRoomSettlementCommit{
							GameOver: competitiveGameOverData(item.ClientTime, resolution), Battle: battle,
						}
					}
				}
			}
			if keep {
				message.Data = append([]byte(nil), message.Data...)
				clone.MessageIndexes = append(clone.MessageIndexes, packet.MessageIndexes[index])
				clone.Messages = append(clone.Messages, message)
			}
		}
		if len(clone.Messages) != 0 {
			filtered = append(filtered, clone)
		}
	}
	return filtered, settlement, nil
}

// normalizeCompetitiveNativeRelayFastPackages coalesces native scene
// notifications that the original client can send over both room-fast UDP and
// reliable TCP. The first transport is relayed; a later exact copy is still
// ACKed/accounted by its own path but never applied to peers twice. Fast-only
// pickups must also be accounted here because the 0x0065 mirror is not sent
// through the reliable game-event dispatcher.
func normalizeCompetitiveNativeRelayFastPackages(packages []game.GameplayDataPackage, battle *match.CompetitiveBattle) ([]game.GameplayDataPackage, error) {
	if battle == nil {
		return packages, nil
	}
	filtered := make([]game.GameplayDataPackage, 0, len(packages))
	for _, packet := range packages {
		if len(packet.MessageIndexes) != len(packet.Messages) {
			return nil, fmt.Errorf("native-relay fast package index count %d != message count %d", len(packet.MessageIndexes), len(packet.Messages))
		}
		clone := packet
		clone.MessageIndexes = make([]uint32, 0, len(packet.MessageIndexes))
		clone.Messages = make([]game.BattleMessageData, 0, len(packet.Messages))
		for index, message := range packet.Messages {
			keep := true
			if message.DataID == game.NotifyPlayerGetItem {
				if err := recordCompetitiveNativePickupFast(message.Data, battle); err != nil {
					return nil, err
				}
			}
			if message.DataID == game.NotifyPlayerGetItem || message.DataID == game.NotifyPlayerDieEvent || message.DataID == game.NotifyPlayerKilled {
				keep = battle.RecordNativeRelay(uint16(message.DataID), message.Data)
			}
			if keep {
				message.Data = append([]byte(nil), message.Data...)
				clone.MessageIndexes = append(clone.MessageIndexes, packet.MessageIndexes[index])
				clone.Messages = append(clone.Messages, message)
			}
		}
		if len(clone.Messages) != 0 {
			filtered = append(filtered, clone)
		}
	}
	return filtered, nil
}

// recordCompetitiveNativePickupFast mirrors the reliable NotifyPlayerGetItem
// accounting path. Requests only authorize native routing and are therefore
// intentionally not handled here. Native actors collect transformation items
// for themselves; only participant pickups can affect settlement.
func recordCompetitiveNativePickupFast(body []byte, battle *match.CompetitiveBattle) error {
	event := game.GameEvent{Schema: game.NotifyPlayerGetItem, Body: body}
	item, err := game.ParsePlayerItemEvent(event)
	if err != nil {
		return err
	}
	if battle.HasBossEntity(item.PlayerID) || battle.HasNativeNPCEntity(item.PlayerID) {
		return nil
	}
	if match.IsTreasureGem(item.ItemID) {
		return nil
	}
	if reward, proven := sceneelement.NativeReward(sceneelement.ID(item.ItemID)); proven && reward.Kind != sceneelement.RewardTreasureScore {
		_, err = battle.RecordNativeSceneReward(item.PlayerID, item.ClientTime, item.ItemID, item.PosX, item.PosY)
		return err
	}
	if sceneelement.IsPermanentInventoryPickup(item.ItemID) {
		_, err = battle.RecordBossPermanentItemPickup(item.PlayerID, item.ClientTime, item.ItemID, item.PosX, item.PosY)
	}
	return err
}

func (server *Server) competitiveFastSettlementAfterUnlock(session *connectionSession, connectionID string, roomID uint16, settlement *competitiveRoomSettlementCommit) func() {
	if settlement == nil || settlement.Battle == nil || roomID == 0 {
		return nil
	}
	// The TCP dispatcher still owns the actor's session mutex here. Return all
	// cross-session work to the caller so it can run immediately after that
	// mutex is released. This prevents simultaneous fast packets from taking
	// the cyclic A-session -> B-session and B-session -> A-session paths.
	return func() {
		session.mu.Lock()
		gameID := session.CurrentGameID
		session.mu.Unlock()
		members := server.liveRoomMatchSessions(roomID, gameID)
		if len(members) == 0 {
			members = []*connectionSession{session}
		}
		for _, member := range members {
			template := member.packetTemplate()
			if len(template) == 0 {
				continue
			}
			packet, err := game.BuildLocalGameOverPushForRecipient(template, roomID, server.nextGameDataSequence(), settlement.GameOver)
			if err != nil {
				server.log(logEvent{Level: "error", Event: "competitive_fast_game_over_build_failed", ConnectionID: member.connectionID, RoomID: fmt.Sprint(roomID), Result: fmt.Sprintf("game_%d", gameID), ErrorContext: err.Error()})
				continue
			}
			server.writeTCP(member.connection, member.connectionID, member.localAddress, member.remoteAddress, packet, "qqt_competitive_game_over_fast_cooperative_loss")
		}
		if err := server.completeCompetitiveRoomMatch(session, connectionID, *settlement); err != nil {
			server.log(logEvent{Level: "error", Event: "competitive_fast_completion_failed", ConnectionID: connectionID, RoomID: fmt.Sprint(roomID), Result: fmt.Sprintf("game_%d", gameID), ErrorContext: err.Error()})
		}
	}
}

func (server *Server) resolveRoomFastUDPRecipients(roomID uint16, senderUIN uint32, playerIDs []uint16, wanted map[uint16]struct{}) ([]roomFastUDPRecipient, error) {
	uinByPlayer := make(map[uint16]uint32, len(wanted))
	server.liveMu.RLock()
	candidates := make([]*connectionSession, 0, len(server.liveSessions))
	for _, live := range server.liveSessions {
		uin := live.liveUIN.Load()
		if uin == 0 || uin == senderUIN || live.liveRoomID.Load() != uint32(roomID) {
			continue
		}
		candidates = append(candidates, live)
	}
	server.liveMu.RUnlock()
	for _, live := range candidates {
		uin := live.liveUIN.Load()
		playerID := uint16(live.livePlayerID.Load())
		matchesRoom := live.liveRoomID.Load() == uint32(roomID)
		if uin == 0 || uin == senderUIN || !matchesRoom {
			continue
		}
		if _, ok := wanted[playerID]; !ok {
			continue
		}
		if prior := uinByPlayer[playerID]; prior != 0 && prior != uin {
			return nil, fmt.Errorf("room fast player %d resolves to multiple UINs", playerID)
		}
		uinByPlayer[playerID] = uin
	}
	now := time.Now()
	server.roomPeerUDPMu.Lock()
	defer server.roomPeerUDPMu.Unlock()
	server.pruneRoomPeerUDPEndpointsLocked(now)
	recipients := make([]roomFastUDPRecipient, 0, len(playerIDs))
	for _, playerID := range playerIDs {
		uin := uinByPlayer[playerID]
		if uin == 0 {
			continue
		}
		presence, ok := server.roomPeerUDPPresence[uin]
		if !ok || presence.PlayerID != playerID || presence.Connection == nil || presence.Address == nil {
			continue
		}
		route, err := observedLegacyUDPEndpoint(presence.Address)
		if err != nil {
			continue
		}
		recipients = append(recipients, roomFastUDPRecipient{
			PlayerID: playerID,
			UIN:      uin,
			Route:    route,
			Endpoint: presence,
		})
	}
	if len(recipients) == 0 {
		return nil, fmt.Errorf("room fast packet has no current target with mode-1 presence")
	}
	return recipients, nil
}

func (server *Server) sendRoomFastUDPPayloads(session *connectionSession, roomID uint16, payloads [][]byte, recipients []roomFastUDPRecipient) (int, error) {
	if session == nil {
		return 0, fmt.Errorf("room fast sender session is absent")
	}
	if len(payloads) == 0 {
		return 0, nil
	}
	var firstPacketNumber uint32
	for range payloads {
		packetNumber, err := server.nextRoomFastUDPPacketNumber(session.UIN)
		if err != nil {
			return 0, err
		}
		if firstPacketNumber == 0 {
			firstPacketNumber = packetNumber
		}
	}
	return server.sendRoomFastUDPPayloadsFromIdentity(
		session.Profile.PlayerID, session.UIN, firstPacketNumber, roomID, payloads, recipients,
	)
}

// sendRoomFastUDPPayloadsFromIdentity is the transport-complete Type-2 relay
// used by both real sessions and server-owned virtual players. A virtual
// player has no TCP session or client-side Type-1 presence, but it is still a
// distinct room peer: using the room owner's identity here makes QQTPPP treat
// its movement as a local echo and silently discard it.
func (server *Server) sendRoomFastUDPPayloadsFromIdentity(sourcePlayerID uint16, sourceUIN, firstPacketNumber uint32, roomID uint16, payloads [][]byte, recipients []roomFastUDPRecipient) (int, error) {
	if sourcePlayerID == 0 || sourceUIN == 0 || firstPacketNumber == 0 {
		return 0, fmt.Errorf("room fast source identity is incomplete")
	}
	routes := make([]game.LegacyUDPMulticastTarget, len(recipients))
	for index, recipient := range recipients {
		routes[index] = game.LegacyUDPMulticastTarget{PlayerID: recipient.PlayerID, UIN: recipient.UIN}
	}
	delivered := 0
	for index, payload := range payloads {
		packetNumber := firstPacketNumber + uint32(index)
		packet := game.LegacyUDPControlPacket{
			Header: game.LegacyUDPControlHeader{
				PacketNumber: packetNumber,
				PlayerID:     sourcePlayerID,
				UIN:          sourceUIN,
				Type:         game.LegacyUDPMulticastType,
			},
			Multicast: &game.LegacyUDPMulticastPayload{Targets: routes, Data: payload},
		}
		data, err := packet.Encode()
		if err != nil {
			return delivered, err
		}
		for _, recipient := range recipients {
			if _, err = recipient.Endpoint.Connection.WriteToUDP(data, recipient.Endpoint.Address); err != nil {
				return delivered, fmt.Errorf("write player %d UIN %d mode-1 endpoint %s: %w", recipient.PlayerID, recipient.UIN, recipient.Endpoint.Address, err)
			}
			delivered++
			server.captureRecord(capture.NewRecord(
				time.Now(), "room-fast-udp", "server_to_client", "udp",
				recipient.Endpoint.Local, recipient.Endpoint.Address.String(), data,
			))
		}
	}
	return delivered, nil
}

// sendVirtualRoomFastUDPPayloadsFromIdentity serializes sequence allocation
// and socket writes for one virtual sender. QQTPPP's Type-2 sequence is a
// transport ordering contract, not just a unique ID: if packet N+1 is written
// first, packet N is stale when it arrives and is discarded by the client.
func (server *Server) sendVirtualRoomFastUDPPayloadsFromIdentity(sourcePlayerID uint16, sourceUIN uint32, roomID uint16, payloads [][]byte, recipients []roomFastUDPRecipient) (int, error) {
	if len(payloads) == 0 {
		return 0, nil
	}
	senderMutex := server.virtualRoomPeerSenderMutex(sourceUIN)
	senderMutex.Lock()
	defer senderMutex.Unlock()

	firstPacketNumber := server.reserveVirtualRoomPeerPacketNumbers(sourceUIN, len(payloads))
	return server.sendRoomFastUDPPayloadsFromIdentity(
		sourcePlayerID, sourceUIN, firstPacketNumber, roomID, payloads, recipients,
	)
}

func validateGameplayFastPackages(packages []game.GameplayDataPackage, playerID uint16, gameID uint32) error {
	return validateGameplayFastPackagesWithBattle(packages, playerID, gameID, nil)
}

func validateGameplayFastPackagesWithBattle(packages []game.GameplayDataPackage, playerID uint16, gameID uint32, battle *match.CompetitiveBattle) error {
	return validateGameplayFastPackagesForMatch(packages, playerID, gameID, battle, nil)
}

func validateGameplayFastPackagesForMatch(packages []game.GameplayDataPackage, playerID uint16, gameID uint32, battle *match.CompetitiveBattle, adventure *match.AdventureBattle) error {
	if len(packages) == 0 || playerID == 0 || gameID == 0 {
		return fmt.Errorf("gameplay fast package identity is incomplete")
	}
	for index, packet := range packages {
		if packet.GameID != gameID {
			return fmt.Errorf("gameplay package %d game %d != active game %d", index, packet.GameID, gameID)
		}
		if err := validateAvatarRecoveryFastMessages(packet, playerID, battle); err != nil {
			return fmt.Errorf("gameplay package %d: %w", index, err)
		}
		if packet.PlayerID == playerID {
			continue
		}
		if battle != nil && battle.AuthorizesBossProxy(playerID, packet.PlayerID) {
			if err := validateBossFastMessages(packet, playerID, battle); err != nil {
				return fmt.Errorf("gameplay package %d Boss %d: %w", index, packet.PlayerID, err)
			}
			continue
		}
		if battle != nil && battle.AuthorizesNativeNPCProxy(playerID, packet.PlayerID) {
			if err := validateNativeNPCFastMessages(packet, battle); err != nil {
				return fmt.Errorf("gameplay package %d native NPC %d: %w", index, packet.PlayerID, err)
			}
			continue
		}
		// Adventure scene NPC object IDs are allocated by the native client and
		// are not present in CREATE_PVENPC_BOSS (that table contains NPC base
		// types and counts). The reliable 0x0065 copy is validation/accounting
		// only—the Type-2 peer datagram has already applied the scene event—so
		// accept a participant or native object package only from the currently
		// elected adventure arbitrator. Human player IDs are battle-registered;
		// native runtime objects use the recovered 30000+ namespace.
		if adventure != nil && adventure.IsArbitrator(playerID) &&
			(adventure.HasParticipant(packet.PlayerID) || packet.PlayerID >= 30000) {
			continue
		}
		return fmt.Errorf("gameplay package %d player %d != authenticated player %d or an authorized native entity proxy", index, packet.PlayerID, playerID)
	}
	return nil
}

func validateAvatarRecoveryFastMessages(packet game.GameplayDataPackage, authenticatedPlayerID uint16, battle *match.CompetitiveBattle) error {
	for index, message := range packet.Messages {
		if message.DataID != game.NotifyRecoverAvatar {
			continue
		}
		recovery, err := game.ParseAvatarRecoveryEvent(game.GameEvent{Schema: game.NotifyRecoverAvatar, Body: message.Data})
		if err != nil {
			return fmt.Errorf("message %d: %w", index, err)
		}
		// Competitive transformation items and named Bosses share the same
		// native rule scanner. Adventure currently has no registered Boss
		// projection here, so its existing authenticated fast boundary remains.
		if battle == nil {
			continue
		}
		if !battle.IsArbitrator(authenticatedPlayerID) {
			return fmt.Errorf("avatar-recovery sender %d is not the match arbitrator", authenticatedPlayerID)
		}
		if !battle.HasParticipant(recovery.PlayerID) &&
			!battle.AuthorizesBossProxy(authenticatedPlayerID, recovery.PlayerID) &&
			!battle.AuthorizesNativeNPCProxy(authenticatedPlayerID, recovery.PlayerID) {
			return fmt.Errorf("avatar-recovery target %d is neither a participant nor an active native entity", recovery.PlayerID)
		}
	}
	return nil
}

// validateNativeNPCFastMessages covers only the AIType=2 entities installed
// by the native rule-8 CREATE_NPC_BOSS producer. Several messages describe a
// target (participant or the NPC itself) rather than the package author, so
// requiring every first field to equal packet.PlayerID would reject genuine
// NPC damage. The outer guard still requires the elected arbitrator and an
// exact, living registry entry.
func validateNativeNPCFastMessages(packet game.GameplayDataPackage, battle *match.CompetitiveBattle) error {
	knownTarget := func(objectID uint16) bool {
		return battle.HasParticipant(objectID) || objectID == packet.PlayerID
	}
	for index, message := range packet.Messages {
		if message.DataID > 0xFFFF {
			return fmt.Errorf("message %d schema 0x%08X exceeds the native event range", index, message.DataID)
		}
		event := game.GameEvent{Schema: uint16(message.DataID), Body: message.Data}
		switch event.Schema {
		case game.PlayerMoveSchema:
			movement, err := game.ParsePlayerMove(event.Body)
			if err != nil {
				return err
			}
			if movement.PlayerID != packet.PlayerID {
				return fmt.Errorf("message %d movement player %d != native NPC %d", index, movement.PlayerID, packet.PlayerID)
			}
			for entryIndex, entry := range movement.Entries {
				if entry.PlayerID != packet.PlayerID {
					return fmt.Errorf("message %d movement entry %d player %d != native NPC %d", index, entryIndex, entry.PlayerID, packet.PlayerID)
				}
			}
		case game.PlayerUseBomb:
			useBomb, err := game.ParsePlayerUseBombEvent(event)
			if err != nil {
				return err
			}
			if useBomb.PlayerID != packet.PlayerID {
				return fmt.Errorf("message %d bomb user %d != native NPC %d", index, useBomb.PlayerID, packet.PlayerID)
			}
		case game.NotifyBombExplode:
			explosion, err := game.ParseBombExplodeEvent(event)
			if err != nil {
				return err
			}
			if explosion.PlayerID != packet.PlayerID {
				return fmt.Errorf("message %d explosion player %d != native NPC %d", index, explosion.PlayerID, packet.PlayerID)
			}
		case game.PlayerThrowBomb:
			throw, err := game.ParseKickBombAction(event)
			if err != nil {
				return err
			}
			if throw.PlayerID != packet.PlayerID {
				return fmt.Errorf("message %d throw player %d != native NPC %d", index, throw.PlayerID, packet.PlayerID)
			}
		case game.RequestMoveBomb, game.NotifyPlayerMoveBomb:
			if len(event.Body) != 20 {
				return fmt.Errorf("message %d move-bomb body length %d, want 20", index, len(event.Body))
			}
			if sourceID := binary.BigEndian.Uint16(event.Body[:2]); sourceID != packet.PlayerID {
				return fmt.Errorf("message %d move-bomb player %d != native NPC %d", index, sourceID, packet.PlayerID)
			}
		case game.RequestKillPlayer:
			interaction, err := game.ParsePlayerInteractionEvent(event)
			if err != nil {
				return err
			}
			if interaction.PlayerID != packet.PlayerID || !battle.HasParticipant(interaction.DestinationPlayerID) {
				return fmt.Errorf("message %d native NPC kill source/target %d/%d is invalid", index, interaction.PlayerID, interaction.DestinationPlayerID)
			}
		case game.NotifyPlayerKilled:
			killed, err := game.ParsePlayerKilledEvent(event)
			if err != nil {
				return err
			}
			if killed.PlayerID != packet.PlayerID || !battle.HasParticipant(killed.DestinationPlayerID) {
				return fmt.Errorf("message %d native NPC killed source/target %d/%d is invalid", index, killed.PlayerID, killed.DestinationPlayerID)
			}
		case game.PlayerBeExploded:
			exploded, err := game.ParsePlayerExplodedEvent(event)
			if err != nil {
				return err
			}
			if !knownTarget(exploded.PlayerID) {
				return fmt.Errorf("message %d exploded target %d is unknown", index, exploded.PlayerID)
			}
		case game.PlayerBeHarmed:
			harmed, err := game.ParseEntityHarmedEvent(event)
			if err != nil {
				return err
			}
			if !knownTarget(harmed.ObjectID) || harmed.LossHP != 0 {
				return fmt.Errorf("message %d native-durability harm target/loss %d/%d is invalid", index, harmed.ObjectID, harmed.LossHP)
			}
		case game.NotifyPlayerDieEvent:
			death, err := game.ParsePlayerDeathEvent(event)
			if err != nil {
				return err
			}
			if !knownTarget(death.PlayerID) {
				return fmt.Errorf("message %d death target %d is unknown", index, death.PlayerID)
			}
		case game.NotifyMapElementExploded:
			exploded, err := game.ParseMapElementsExplodedEvent(event)
			if err != nil {
				return err
			}
			if exploded.PlayerID != packet.PlayerID {
				return fmt.Errorf("message %d map-element owner %d != native NPC %d", index, exploded.PlayerID, packet.PlayerID)
			}
		case game.NotifyPlayerKnocked:
			knocked, err := game.ParsePlayerKnockedEvent(event)
			if err != nil {
				return err
			}
			if !knownTarget(knocked.PlayerID) {
				return fmt.Errorf("message %d knocked target %d is unknown", index, knocked.PlayerID)
			}
		case game.RequestGetItem, game.NotifyPlayerGetItem:
			item, err := game.ParsePlayerItemEvent(event)
			if err != nil {
				return err
			}
			if item.PlayerID != packet.PlayerID {
				return fmt.Errorf("message %d item player %d != native NPC %d", index, item.PlayerID, packet.PlayerID)
			}
		case game.NotifyNPCDropItem:
			drop, err := game.ParseNPCDropItemEvent(event)
			if err != nil {
				return err
			}
			if drop.ObjectID != packet.PlayerID {
				return fmt.Errorf("message %d drop object %d != native NPC %d", index, drop.ObjectID, packet.PlayerID)
			}
		case game.NotifyNPCTalk:
			talk, err := game.ParseNPCTalkEvent(event)
			if err != nil {
				return err
			}
			if talk.ObjectID != packet.PlayerID {
				return fmt.Errorf("message %d talk object %d != native NPC %d", index, talk.ObjectID, packet.PlayerID)
			}
		case game.NotifyRecoverAvatar:
			recovery, err := game.ParseAvatarRecoveryEvent(event)
			if err != nil {
				return err
			}
			if recovery.PlayerID != packet.PlayerID {
				return fmt.Errorf("message %d recovery target %d != native NPC %d", index, recovery.PlayerID, packet.PlayerID)
			}
		default:
			return fmt.Errorf("message %d schema 0x%04X is not authorized for a native rule NPC", index, event.Schema)
		}
	}
	return nil
}

// validateBossFastMessages is intentionally narrower than the general battle
// schema table. It permits only messages the native arbitrator is proven to
// generate for a registered Boss and verifies the embedded object identity.
// Scene transitions, GAME_OVER, room departure and arbitrary player events
// can therefore never be smuggled through the NPC exception.
func validateBossFastMessages(packet game.GameplayDataPackage, arbitratorID uint16, battle *match.CompetitiveBattle) error {
	for index, message := range packet.Messages {
		if message.DataID > 0xFFFF {
			return fmt.Errorf("message %d schema 0x%08X exceeds the native event range", index, message.DataID)
		}
		event := game.GameEvent{Schema: uint16(message.DataID), Body: message.Data}
		var sourceID uint16
		switch event.Schema {
		case game.PlayerMoveSchema:
			movement, err := game.ParsePlayerMove(event.Body)
			if err != nil {
				return err
			}
			sourceID = movement.PlayerID
			for entryIndex, entry := range movement.Entries {
				if entry.PlayerID != packet.PlayerID {
					return fmt.Errorf("message %d movement entry %d player %d != Boss package player %d", index, entryIndex, entry.PlayerID, packet.PlayerID)
				}
			}
		case game.PlayerUseBomb:
			useBomb, err := game.ParsePlayerUseBombEvent(event)
			if err != nil {
				return err
			}
			sourceID = useBomb.PlayerID
		case game.NotifyBombExplode:
			explosion, err := game.ParseBombExplodeEvent(event)
			if err != nil {
				return err
			}
			sourceID = explosion.PlayerID
		case game.PlayerThrowBomb:
			throw, err := game.ParseKickBombAction(event)
			if err != nil {
				return err
			}
			sourceID = throw.PlayerID
		case game.RequestKillPlayer:
			interaction, err := game.ParsePlayerInteractionEvent(event)
			if err != nil {
				return err
			}
			sourceID = interaction.PlayerID
		case game.NotifyPlayerKilled:
			killed, err := game.ParsePlayerKilledEvent(event)
			if err != nil {
				return err
			}
			sourceID = killed.PlayerID
		case game.PlayerBeExploded:
			exploded, err := game.ParsePlayerExplodedEvent(event)
			if err != nil {
				return err
			}
			// A normal Boss takes the dedicated 0x10F4 HP route. The shared
			// avatar-hit schema is valid for a Boss only while a picked-up
			// transformation form is being consumed.
			if !exploded.IsAvatar {
				return fmt.Errorf("message %d normal-form Boss %d used player-exploded", index, packet.PlayerID)
			}
			sourceID = exploded.PlayerID
		case game.RequestGetItem, game.NotifyPlayerGetItem:
			item, err := game.ParsePlayerItemEvent(event)
			if err != nil {
				return err
			}
			sourceID = item.PlayerID
		case game.RequestMoveBomb, game.NotifyPlayerMoveBomb:
			if len(event.Body) != 20 {
				return fmt.Errorf("move-bomb schema 0x%04X body length %d, want 20", event.Schema, len(event.Body))
			}
			sourceID = binary.BigEndian.Uint16(event.Body[:2])
		case game.NotifyPlayerDieEvent:
			death, err := game.ParsePlayerDeathEvent(event)
			if err != nil {
				return err
			}
			sourceID = death.PlayerID
		case game.NotifyNPCDropItem:
			drop, err := game.ParseNPCDropItemEvent(event)
			if err != nil {
				return err
			}
			sourceID = drop.ObjectID
		case game.NotifyNPCUseSkill:
			skill, err := game.ParseNPCUseSkillEvent(event)
			if err != nil {
				return err
			}
			// QQT_DATA_PACKAGE.PlayerID identifies the native object that
			// authored the package.  The misleading PlayerID field inside
			// NOTIFY_NPCUSE_SKILL is target-dependent: the shipped Boss
			// controller writes a participant ID there for targeted skills and
			// the Boss ID for self/area skills.  Treating that field as the
			// author rejected valid attacks (for example Boss 30001 targeting
			// player 1 or 2) and left the Boss with movement-only behaviour.
			if !battle.AuthorizesBossSkill(arbitratorID, packet.PlayerID, skill.SkillID) {
				return fmt.Errorf("message %d Boss %d skill %d was not advertised by BOSS_INFO", index, packet.PlayerID, skill.SkillID)
			}
			if !battle.HasParticipant(skill.ObjectID) && !battle.HasBossEntity(skill.ObjectID) {
				return fmt.Errorf("message %d Boss %d skill %d targets unknown object %d", index, packet.PlayerID, skill.SkillID, skill.ObjectID)
			}
			sourceID = packet.PlayerID
		case game.NotifyNPCTalk:
			talk, err := game.ParseNPCTalkEvent(event)
			if err != nil {
				return err
			}
			sourceID = talk.ObjectID
		case game.PlayerBeHarmed:
			harmed, err := game.ParseEntityHarmedEvent(event)
			if err != nil {
				return err
			}
			// Captures contain both the historical 500 scalar and zero-valued
			// hit markers for named Bosses. Registered object ownership is the
			// stable boundary; this relay does not calculate Boss HP itself.
			sourceID = harmed.ObjectID
		case game.NotifyRecoverAvatar:
			recovery, err := game.ParseAvatarRecoveryEvent(event)
			if err != nil {
				return err
			}
			sourceID = recovery.PlayerID
		default:
			return fmt.Errorf("message %d schema 0x%04X is not authorized for a Boss proxy", index, event.Schema)
		}
		if sourceID != packet.PlayerID {
			return fmt.Errorf("message %d source %d != Boss package player %d", index, sourceID, packet.PlayerID)
		}
	}
	return nil
}

func consumeCompetitiveFastPackages(packages []game.GameplayDataPackage, battle *match.CompetitiveBattle) (*competitiveRoomSettlementCommit, error) {
	if battle == nil {
		return nil, nil
	}
	var settlement *competitiveRoomSettlementCommit
	for _, packet := range packages {
		for _, message := range packet.Messages {
			if message.DataID > 0xFFFF {
				continue
			}
			event := game.GameEvent{Schema: uint16(message.DataID), Body: message.Data}
			switch event.Schema {
			case game.PlayerBeExploded:
				exploded, err := game.ParsePlayerExplodedEvent(event)
				if err != nil {
					return nil, err
				}
				if !exploded.IsAvatar && battle.AuthorizesParticipantObservation(packet.PlayerID, exploded.PlayerID) {
					if err = battle.RecordTrapped(exploded.PlayerID); err != nil {
						return nil, err
					}
				}
			case game.NotifyPlayerKilled:
				if !battle.HasBossEntity(packet.PlayerID) {
					continue
				}
				killed, err := game.ParsePlayerKilledEvent(event)
				if err != nil {
					return nil, err
				}
				itemIDs := make([]uint32, 0, len(killed.Items))
				for _, item := range killed.Items {
					itemIDs = append(itemIDs, item.ItemID)
				}
				if err = battle.RecordTreasureScatter(killed.DestinationPlayerID, itemIDs); err != nil {
					return nil, err
				}
				resolution, err := battle.RecordBossKill(killed.PlayerID, killed.DestinationPlayerID)
				if err != nil {
					return nil, err
				}
				if resolution.NewlyConcluded && settlement == nil {
					gameOver := competitiveGameOverData(killed.ClientTime, resolution)
					settlement = &competitiveRoomSettlementCommit{GameOver: gameOver, Battle: battle}
				}
			case game.NotifyPlayerDieEvent:
				death, err := game.ParsePlayerDeathEvent(event)
				if err != nil {
					return nil, err
				}
				if death.PlayerID == packet.PlayerID && battle.HasNativeNPCEntity(death.PlayerID) {
					if _, err = battle.RecordNativeNPCDeath(death.PlayerID); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return settlement, nil
}

func validateRoomFastEvents(events []game.RoomFastEvent, seatID byte) error {
	if seatID == 0 || seatID > roomstate.RoomSeatCount {
		return fmt.Errorf("room seat ID %d is invalid", seatID)
	}
	wireSeat := seatID - 1
	for index, event := range events {
		switch event.DataID {
		case game.RoomPlayerMoveInfoEvent:
			move, err := game.DecodeRoomPlayerMoveInfo(event.Data)
			if err != nil {
				return fmt.Errorf("room fast event %d: %w", index, err)
			}
			if move.SeatIndex != wireSeat {
				return fmt.Errorf("room fast move seat %d != authenticated seat %d", move.SeatIndex, wireSeat)
			}
		case game.RoomPlayerPutBombEvent:
			bomb, err := game.DecodeRoomPlayerPutBomb(event.Data)
			if err != nil {
				return fmt.Errorf("room fast event %d: %w", index, err)
			}
			if bomb.SeatIndex != wireSeat {
				return fmt.Errorf("room fast bomb seat %d != authenticated seat %d", bomb.SeatIndex, wireSeat)
			}
		default:
			return fmt.Errorf("room fast event %d data ID 0x%04X is unsupported", index, uint16(event.DataID))
		}
	}
	return nil
}
