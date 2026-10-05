package probe

import (
	"fmt"
	"strings"
	"time"

	"qqtang/internal/game/mapdata"
	"qqtang/internal/game/match"
	roomstate "qqtang/internal/game/room"
	"qqtang/internal/protocol/game"
)

type matchStartMessageResult struct {
	handled             bool
	response            []byte
	result              string
	followUp            []byte
	followUpResult      string
	startFollowUp       []byte
	startFollowUpResult string
	startFollowUpDelay  time.Duration
	postResponse        func()
	afterFollowUp       func()
	afterStartFollowUp  func()
}

type preparedRoomMatchStart struct {
	followUp                   []byte
	followUpResult             string
	startFollowUp              []byte
	startFollowUpResult        string
	startFollowUpDelay         time.Duration
	competitiveTimeout         *competitiveTimeoutSchedule
	competitiveAirborne        *competitiveAirborneSchedule
	passiveFunctionProjections []game.PassiveFunctionProjection
	bossSummonRefreshes        []inventoryAbsoluteRefresh
	competitiveAI              *liveCompetitiveAIRuntime
}

type inventoryAbsoluteRefresh struct {
	UIN         uint32
	CommodityID uint32
	Item        game.ItemInfo
}

type startGameRejection struct {
	ResultID uint16
	Message  string
}

func classifyStartGameRejection(err error) startGameRejection {
	rejection := startGameRejection{
		ResultID: game.StartGameResultRejected,
		Message:  "当前房间暂时无法开始游戏。",
	}
	if err == nil {
		return rejection
	}
	context := err.Error()
	switch {
	case strings.Contains(context, "is not room") && strings.Contains(context, "owner"):
		rejection.Message = "只有房主可以开始游戏。"
	case strings.Contains(context, "requires at least two players"):
		rejection.ResultID = game.StartGameResultPlayersRequired
		rejection.Message = "玩家个数不足，不能开始游戏。"
	case strings.Contains(context, "not ready") && strings.Contains(context, "player"):
		rejection.ResultID = game.StartGameResultPlayersNotReady
		rejection.Message = "必须所有玩家准备才可以开始游戏。"
	case strings.Contains(context, "requires at least two teams"):
		rejection.ResultID = game.StartGameResultTeamInvalid
		rejection.Message = "需要至少两个队伍才可以开始游戏。"
	case strings.Contains(context, "every player must use a separate team"):
		rejection.ResultID = game.StartGameResultTeamInvalid
		rejection.Message = "夺宝场中每位玩家必须使用不同队伍。"
	case strings.Contains(context, "unbalanced team"):
		rejection.ResultID = game.StartGameResultTeamInvalid
		rejection.Message = "标准场各队人数必须相同。"
	case strings.Contains(context, "single-player adventure room") && strings.Contains(context, "requires item"):
		rejection.ResultID = game.StartGameResultItemRequired
		rejection.Message = "单人探险需要激活单人探险卡。"
	case strings.Contains(context, "single-player competitive Boss room") && strings.Contains(context, "requires item"):
		rejection.ResultID = game.StartGameResultItemRequired
		rejection.Message = "单人竞技需要激活单人BOSS卡。"
	case strings.Contains(context, "does not satisfy any Boss candidate"):
		rejection.ResultID = game.StartGameResultItemRequired
		rejection.Message = "当前地图或召唤道具不满足BOSS卡的开始条件。"
	case strings.Contains(context, "above capacity"):
		rejection.Message = "参与人数超过房间容量。"
	case strings.Contains(context, "chat room") && strings.Contains(context, "cannot start"):
		rejection.Message = "聊天房间不能开始对局。"
	case strings.Contains(context, "is not ready to start"):
		rejection.Message = "当前房间状态不能开始新的对局。"
	}
	return rejection
}

func buildMatchStartRejection(request []byte, session *connectionSession, cause error) (matchStartMessageResult, error) {
	rejection := classifyStartGameRejection(cause)
	response, err := game.BuildLocalStartGameResult(request, rejection.ResultID)
	if err != nil {
		return matchStartMessageResult{}, err
	}
	if session == nil {
		return matchStartMessageResult{}, fmt.Errorf("start rejection has no session")
	}
	notification, err := game.BuildLocalRoomChatNotification(request, game.RoomChatNotification{
		SourcePlayerID:      game.RoomChatSystemSourcePlayerID,
		DestinationPlayerID: session.Profile.PlayerID,
		Content:             rejection.Message,
	})
	if err != nil {
		return matchStartMessageResult{}, err
	}
	return matchStartMessageResult{
		handled: true, response: response, result: "qqt_start_game_rejected",
		followUp: notification, followUpResult: "qqt_start_game_rejection_notice",
	}, nil
}

// handleMatchStartMessage validates the long-lived room before creating a
// category-specific short-lived match. Ordinary competitive and adventure
// modes share transport framing but never share their battle state machine.
func (server *Server) handleMatchStartMessage(config ListenerConfig, session *connectionSession, id, local, remote string, data []byte) matchStartMessageResult {
	inspection, err := game.InspectLocalPacket(data)
	if err != nil || inspection.Command != game.StartGameCommand {
		return matchStartMessageResult{}
	}
	if !config.Response.QQTStartGameSuccess {
		return matchStartMessageResult{}
	}
	state, err := server.sessionRoom(session)
	if err == nil {
		err = server.validateSessionRoomStart(session, state)
	}
	if err != nil {
		server.log(logEvent{Level: "warn", Event: "room_start_rejected", ConnectionID: id, Result: "rejected", ErrorContext: err.Error()})
		rejected, buildErr := buildMatchStartRejection(data, session, err)
		if buildErr != nil {
			server.log(logEvent{Level: "error", Event: "room_start_rejection_build_failed", ConnectionID: id, Result: "rejected", ErrorContext: buildErr.Error()})
			return matchStartMessageResult{handled: true, result: "qqt_start_game_rejected"}
		}
		return rejected
	}
	response, err := game.BuildLocalStartGameSuccess(data)
	if err != nil || len(response) == 0 {
		return matchStartMessageResult{}
	}
	result := matchStartMessageResult{handled: true, response: response, result: "qqt_start_game_success"}
	if !config.Response.gameBeginEnabled() {
		return result
	}
	prepared, err := server.prepareRoomMatchStart(session, data)
	if err != nil {
		server.log(logEvent{
			Level: "error", Event: "dynamic_followup_failed", ConnectionID: id, Result: "qqt_game_begin",
			ErrorContext: err.Error(), Network: "tcp", LocalAddress: local, RemoteAddress: remote,
		})
		rejected, buildErr := buildMatchStartRejection(data, session, err)
		if buildErr != nil {
			server.log(logEvent{Level: "error", Event: "room_start_rejection_build_failed", ConnectionID: id, Result: "rejected", ErrorContext: buildErr.Error()})
			return matchStartMessageResult{handled: true, result: "qqt_start_game_rejected"}
		}
		return rejected
	}
	result.followUp = prepared.followUp
	result.followUpResult = prepared.followUpResult
	result.startFollowUp = prepared.startFollowUp
	result.startFollowUpResult = prepared.startFollowUpResult
	result.startFollowUpDelay = prepared.startFollowUpDelay
	if len(prepared.followUp) != 0 {
		notification := append([]byte(nil), prepared.followUp...)
		sectionID := session.Profile.SectionID
		roomID := session.RoomID
		afterGameBegin := func() {
			if prepared.competitiveAI != nil {
				if actorErr := runRoomActor(server, roomID, "competitive-ai-install", func() error {
					server.installCompetitiveAIRuntime(prepared.competitiveAI)
					return nil
				}); actorErr != nil {
					server.log(logEvent{Level: "error", Event: "competitive_ai_install_actor_failed", RoomID: fmt.Sprint(roomID), ErrorContext: actorErr.Error()})
				}
			}
			for _, projection := range prepared.passiveFunctionProjections {
				body, marshalErr := projection.MarshalNetworkBinary()
				if marshalErr != nil {
					server.log(logEvent{Level: "error", Event: "passive_function_projection_failed", RoomID: fmt.Sprint(roomID), ErrorContext: marshalErr.Error()})
					continue
				}
				sequence := server.nextGameDataSequence()
				server.broadcastRoomNotification(roomID, 0, "qqt_passive_function_projection", func(recipientPacket []byte) ([]byte, error) {
					return game.BuildLocalGameEventPushForRecipient(recipientPacket, roomID, sequence, game.PlayerThrowBomb, body)
				})
			}
			server.broadcastRoomLobbyMutation(sectionID, roomID, false, "qqt_room_lobby_push_match_started")
			if prepared.competitiveTimeout != nil {
				server.scheduleCompetitiveTimeout(*prepared.competitiveTimeout)
			}
			if len(prepared.startFollowUp) == 0 {
				server.sendInventoryAbsoluteRefreshes(prepared.bossSummonRefreshes)
			}
		}
		result.afterFollowUp = func() {
			server.broadcastMatchNotification(roomID, session.UIN, prepared.followUpResult+"_peer", notification)
			afterGameBegin()
		}
		if prepared.competitiveAI != nil && len(prepared.competitiveAI.roomProjections) != 0 {
			projections := append([]competitiveAIRoomProjection(nil), prepared.competitiveAI.roomProjections...)
			result.postResponse = func() {
				if actorErr := runRoomActor(server, roomID, "competitive-ai-stage", func() error {
					server.stageCompetitiveAIRuntime(prepared.competitiveAI)
					return nil
				}); actorErr != nil {
					server.log(logEvent{Level: "error", Event: "competitive_ai_stage_actor_failed", RoomID: fmt.Sprint(roomID), ErrorContext: actorErr.Error()})
					return
				}
				server.broadcastCompetitiveAIEnterProjections(roomID, projections)
			}
		}
	}
	if len(prepared.startFollowUp) != 0 {
		notification := append([]byte(nil), prepared.startFollowUp...)
		result.afterStartFollowUp = func() {
			server.broadcastMatchNotification(session.RoomID, session.UIN, prepared.startFollowUpResult+"_peer", notification)
			if prepared.competitiveAirborne != nil {
				server.scheduleCompetitiveAirborne(*prepared.competitiveAirborne)
			}
			server.sendInventoryAbsoluteRefreshes(prepared.bossSummonRefreshes)
		}
	}
	return result
}

func (server *Server) prepareRoomMatchStart(session *connectionSession, request []byte) (preparedRoomMatchStart, error) {
	state, err := server.sessionRoom(session)
	if err != nil {
		return preparedRoomMatchStart{}, err
	}
	category, err := state.Snapshot().Settings.GameType.Category()
	if err != nil {
		return preparedRoomMatchStart{}, err
	}
	switch category {
	case roomstate.CategoryAdventure:
		return server.prepareAdventureRoomMatchStart(session, request)
	case roomstate.CategoryCompetitive:
		return server.prepareCompetitiveRoomMatchStart(session, request)
	case roomstate.CategoryChat:
		return preparedRoomMatchStart{}, fmt.Errorf("chat room %d cannot start a match", session.RoomID)
	default:
		return preparedRoomMatchStart{}, fmt.Errorf("room %d has unsupported category %d", session.RoomID, category)
	}
}

func (server *Server) prepareAdventureRoomMatchStart(session *connectionSession, request []byte) (prepared preparedRoomMatchStart, err error) {
	participants, err := server.localAdventureParticipants(session)
	if err != nil {
		return prepared, err
	}
	arbitratorID, err := match.SelectAdventureArbitrator(session.Profile.PlayerID, participants)
	if err != nil {
		return prepared, err
	}
	selectedMap, err := server.resolveSessionRoomMap(session)
	if err != nil {
		return prepared, err
	}
	session.CurrentGameID = server.worldState().AllocateGameID()
	session.CurrentStageGameID = session.CurrentGameID
	defer func() {
		if err != nil {
			server.removeAdventureBattle(session.CurrentGameID)
			session.CurrentGameID = 0
			session.CurrentStageGameID = 0
			session.CurrentMapID = 0
		}
	}()
	gameData, err := server.newAdventureGameData(session, session.CurrentStageGameID, selectedMap, arbitratorID, participants)
	if err != nil {
		return prepared, err
	}
	prepared.followUp, err = game.BuildLocalGameBegin(request, gameData)
	if err != nil {
		return prepared, err
	}
	stageRule, hasStageRule := server.adventureStageRule(selectedMap.ID)
	if err = server.replaceAdventureBattleWithStage(session.CurrentGameID, selectedMap.ID, gameData.ItemSeed, stageRule.ExpectedNPCDeaths, arbitratorID, participants); err != nil {
		return prepared, err
	}
	if hasStageRule {
		prepared.startFollowUp, err = game.BuildLocalCreatePVENPCBossNotify(request, session.RoomID, server.nextGameDataSequence(), adventurePVEBossData(stageRule, gameData.ItemSeed))
		if err != nil {
			return prepared, err
		}
		prepared.startFollowUpResult = fmt.Sprintf("qqt_adventure_create_pve_npc_boss_map_%d_groups_%d", selectedMap.ID, len(stageRule.NPCDropGroups))
		prepared.startFollowUpDelay = 50 * time.Millisecond
	}
	if err = server.startSessionRoomMatch(session); err != nil {
		return prepared, err
	}
	session.CurrentMapID = selectedMap.ID
	// handleTCPMessage owns session.mu while preparing this start. Project the
	// actor without reacquiring that mutex and lock only the peer sessions.
	server.projectRoomMatchFromLockedSession(session, session.RoomID, session.CurrentGameID, selectedMap.ID, nil)
	prepared.followUpResult = fmt.Sprintf("qqt_adventure_game_begin_map_%d", selectedMap.ID)
	return prepared, nil
}

func competitiveBossInventoryRefreshes(activation competitiveMatchActivation, profiles map[uint32]game.PlayerProfile) []inventoryAbsoluteRefresh {
	if len(activation.SummonItems) == 0 || len(profiles) == 0 {
		return nil
	}
	refreshes := make([]inventoryAbsoluteRefresh, 0)
	for uin, requirements := range activation.SummonItems {
		profile, ok := profiles[uin]
		if !ok {
			continue
		}
		seen := make(map[uint16]struct{}, len(requirements))
		for _, requirement := range requirements {
			if _, duplicate := seen[requirement.ItemID]; duplicate {
				continue
			}
			seen[requirement.ItemID] = struct{}{}
			item, exists := inventoryItemByID(profile.Inventory, requirement.ItemID)
			if !exists {
				item = game.NewPermanentItemInfo(requirement.ItemID, 0)
			}
			refreshes = append(refreshes, inventoryAbsoluteRefresh{
				UIN: uin, CommodityID: uint32(requirement.ItemID), Item: item,
			})
		}
	}
	return refreshes
}

func (server *Server) sendInventoryAbsoluteRefreshes(refreshes []inventoryAbsoluteRefresh) {
	for _, refresh := range refreshes {
		server.sendPrimaryInventoryItemRefresh(refresh.UIN, refresh.CommodityID, refresh.Item)
	}
}

func (server *Server) prepareCompetitiveRoomMatchStart(session *connectionSession, request []byte) (prepared preparedRoomMatchStart, err error) {
	room, err := server.sessionRoom(session)
	if err != nil {
		return prepared, err
	}
	field, ok := room.Snapshot().Settings.GameType.CompetitiveField()
	if !ok {
		return prepared, fmt.Errorf("room %d is not a competitive field", session.RoomID)
	}
	roomSnapshot := room.Snapshot()
	participants, err := server.localCompetitiveParticipants(session)
	if err != nil {
		return prepared, err
	}
	humanCount := len(participants)
	arbitratorID, err := match.SelectCompetitiveArbitrator(session.Profile.PlayerID, participants)
	if err != nil {
		return prepared, err
	}
	aiAuthorized := server.config.CompetitiveAI.Enabled &&
		profileOwnsActiveUncollectedItem(session.Profile, game.CompetitiveAICardItemID)
	mapParticipantCount := len(participants)
	// AI fill is only defined for the no-item field. Do not let ownership of an
	// AI card alter random-map capacity selection in item/treasure rooms where
	// the fill plan will intentionally decline and normal start rules must apply.
	if aiAuthorized && field == roomstate.CompetitiveFieldNoItem && roomSnapshot.Settings.Map.Kind == roomstate.MapSelectionRandom {
		if roomSnapshot.Properties.UsesFreeRule() {
			mapParticipantCount = int(roomSnapshot.Capacity())
		} else {
			singleTeam := true
			for _, participant := range participants[1:] {
				if participant.TeamID != participants[0].TeamID {
					singleTeam = false
					break
				}
			}
			if singleTeam && len(participants)*2 <= int(roomSnapshot.Capacity()) {
				// Standard AI rooms fill both teams up to the open-seat limit.
				// Select a map for that final even roster, not just the humans
				// and an equally sized opposing team.
				mapParticipantCount = int(roomSnapshot.Capacity()) / 2 * 2
			}
		}
	}
	selectedMap, err := server.resolveSessionCompetitiveMap(session, mapParticipantCount)
	if err != nil {
		return prepared, err
	}
	if selectedMap.Rule == mapdata.CompetitiveRuleUnknown {
		return prepared, fmt.Errorf("competitive map %d family %s has unsupported native rule %d", selectedMap.ID, selectedMap.Family, selectedMap.NativeRule)
	}
	prepared.passiveFunctionProjections = server.competitivePassiveFunctionProjections(session, participants, selectedMap.Rule)
	conclusionPolicy := match.CompetitiveConclusionClientRule
	if selectedMap.UsesServerElimination() {
		conclusionPolicy = match.CompetitiveConclusionOrdinaryElimination
	}
	ruleSpec, ok := mapdata.LookupCompetitiveRule(selectedMap.Rule)
	if !ok {
		return prepared, fmt.Errorf("competitive map %d has no registered rule contract for %s", selectedMap.ID, selectedMap.Rule)
	}
	var activation competitiveMatchActivation
	// The solo card is part of Boss eligibility, not a veto after choosing a
	// Boss. Without that permission, an AI card may still authorize a normal
	// match on this map. Multiplayer Boss qualification needs no solo card.
	if humanCount > 1 || profileOwnsActiveUncollectedItem(session.Profile, game.SinglePlayerBossCardItemID) {
		activation, err = server.resolveCompetitiveMatchActivation(session, selectedMap, participants)
		if err != nil {
			return prepared, err
		}
	}
	var aiFill competitiveAIFillPlan
	if aiAuthorized && !activation.Active {
		aiFill, err = planCompetitiveAIFill(roomSnapshot, selectedMap, participants, uint64(time.Now().UnixNano()))
		if err != nil {
			return prepared, err
		}
		participants = append(participants, aiFill.Participants...)
	}
	if len(participants) == 1 && !activation.Active {
		return prepared, fmt.Errorf("single-player competitive map %d does not satisfy any Boss candidate", selectedMap.ID)
	}
	playerLifecycle, err := competitivePlayerLifecycle(ruleSpec.PlayerLifecycle)
	if err != nil {
		return prepared, fmt.Errorf("competitive map %d: %w", selectedMap.ID, err)
	}
	if activation.Active && activation.Candidate.Overlay.Kind != mapdata.CompetitiveOverlayNone {
		playerLifecycle, err = competitivePlayerLifecycle(activation.Candidate.Overlay.PlayerLifecycle)
		if err != nil {
			return prepared, fmt.Errorf("competitive map %d activation %q: %w", selectedMap.ID, activation.Candidate.ID, err)
		}
	}
	if activation.Active && activation.Candidate.Overlay.Kind != mapdata.CompetitiveOverlayNone {
		conclusionPolicy = match.CompetitiveConclusionClientRule
	}
	projectedParticipants, teamTopology, err := projectCompetitiveParticipants(activation.Candidate.Overlay, participants)
	if err != nil {
		return prepared, err
	}
	// Freeze one final participant roster after every activation projection.
	// GAME_BEGIN, settlement, the deterministic engine, room projection and
	// start validation must all consume these exact identities and team IDs.
	projectedVirtualParticipants := make([]match.CompetitiveParticipant, 0, len(aiFill.Participants))
	for _, participant := range projectedParticipants {
		if participant.Source == match.CompetitiveParticipantVirtualAI {
			projectedVirtualParticipants = append(projectedVirtualParticipants, participant)
		}
	}
	if len(projectedVirtualParticipants) != len(aiFill.Participants) {
		return prepared, fmt.Errorf("competitive map %d projected %d virtual participants from fill plan %d", selectedMap.ID, len(projectedVirtualParticipants), len(aiFill.Participants))
	}
	session.CurrentGameID = server.worldState().AllocateGameID()
	session.CurrentStageGameID = session.CurrentGameID
	defer func() {
		if err != nil {
			server.removeCompetitiveBattle(session.CurrentGameID)
			session.CurrentGameID = 0
			session.CurrentStageGameID = 0
			session.CurrentMapID = 0
		}
	}()
	gameData, err := server.newCompetitiveGameData(session, selectedMap, field, arbitratorID, projectedParticipants, activation.Active)
	if err != nil {
		return prepared, err
	}
	prepared.followUp, err = game.BuildLocalGameBegin(request, gameData)
	if err != nil {
		return prepared, err
	}
	bossData, bossEnabled, bossErr := server.competitiveBossStartData(activation, gameData.ItemSeed)
	if bossErr != nil {
		return prepared, bossErr
	}
	objective := competitiveObjective(ruleSpec.Objective)
	bossEntityIDs := make([]uint16, 0, len(bossData.Bosses))
	initialSceneItems := make(map[uint32]uint32, len(gameData.NewItems))
	for _, item := range gameData.NewItems {
		initialSceneItems[item.ItemID] += uint32(item.Quantity)
	}
	bossSceneItems := make(map[uint16]map[uint32]uint32, len(bossData.Bosses))
	bossDeathItems := make(map[uint16]map[uint32]uint32, len(bossData.Bosses))
	bossSkills := make(map[uint16][]uint16, len(bossData.Bosses))
	if bossEnabled {
		objective = match.CompetitiveObjectiveBoss
		for _, boss := range bossData.Bosses {
			bossEntityIDs = append(bossEntityIDs, boss.BossID)
			items := make(map[uint32]uint32, len(boss.NormalItems))
			for _, item := range boss.NormalItems {
				items[item.ItemID] += uint32(item.ItemCount)
			}
			bossSceneItems[boss.BossID] = items
			deathItems := make(map[uint32]uint32, len(boss.OutfitItems))
			for _, item := range boss.OutfitItems {
				deathItems[item.ItemID] += uint32(item.ItemCount)
			}
			bossDeathItems[boss.BossID] = deathItems
			skills := make([]uint16, 0, len(boss.Skills))
			for _, skillID := range boss.Skills {
				// Zero is a native positional placeholder, not a skill.  The
				// kick-bomb controller indexes its cooldown storage by the wire
				// list position, so disabling skill 10 still requires
				// {0, 11, 12}.  Preserve that slot exactly as CREATE_NPC_BOSS
				// does; the battle whitelist ignores zero below.
				if skillID > 0xffff {
					return prepared, fmt.Errorf("competitive Boss %d skill ID %d is outside the native uint16 field", boss.BossID, skillID)
				}
				skills = append(skills, uint16(skillID))
			}
			bossSkills[boss.BossID] = skills
		}
	}
	if err = server.replaceCompetitiveBattle(session.CurrentGameID, selectedMap.ID, arbitratorID, projectedParticipants, match.CompetitiveRuleConfig{
		ConclusionPolicy:  conclusionPolicy,
		PlayerLifecycle:   playerLifecycle,
		Objective:         objective,
		TeamTopology:      teamTopology,
		NativeHitLimit:    ruleSpec.NativePlayerHitLimit,
		BossEntityIDs:     bossEntityIDs,
		BossID:            activation.Candidate.ID,
		BossSkills:        bossSkills,
		InitialSceneItems: initialSceneItems,
		BossSceneItems:    bossSceneItems,
		BossDeathItems:    bossDeathItems,
	}); err != nil {
		return prepared, err
	}
	if len(projectedVirtualParticipants) != 0 {
		roomProjections, projectionErr := buildCompetitiveAIRoomProjections(roomSnapshot, projectedVirtualParticipants)
		if projectionErr != nil {
			return prepared, fmt.Errorf("competitive map %d virtual room projection: %w", selectedMap.ID, projectionErr)
		}
		prepared.competitiveAI, err = newLiveCompetitiveAIRuntime(
			session.RoomID, selectedMap, gameData, projectedParticipants,
			roomSnapshot.Properties.UsesFreeRule(), server.competitiveAIPolicy, server.config.CompetitiveAI.TickMS,
		)
		if err != nil {
			return prepared, fmt.Errorf("competitive map %d virtual runtime: %w", selectedMap.ID, err)
		}
		prepared.competitiveAI.roomProjections = roomProjections
	}
	if bossEnabled {
		prepared.startFollowUp, err = game.BuildLocalCreateNPCBossNotify(request, session.RoomID, server.nextGameDataSequence(), bossData)
		if err != nil {
			return prepared, err
		}
		prepared.startFollowUpResult = fmt.Sprintf("qqt_competitive_create_npc_boss_map_%d_candidate_%s_template_%s", selectedMap.ID, activation.Candidate.ID, activation.Candidate.Overlay.BossTemplate)
		if competitiveOverlayHasSceneCapability(activation.Candidate.Overlay, mapdata.CompetitiveBossSceneAirborneBombs) {
			if len(selectedMap.AirborneCells) == 0 {
				return prepared, fmt.Errorf("competitive Boss %q map %d has no static airborne cells", activation.Candidate.ID, selectedMap.ID)
			}
			prepared.competitiveAirborne = &competitiveAirborneSchedule{
				RoomID: session.RoomID, GameID: session.CurrentGameID, MapID: selectedMap.ID,
				ArbitratorID: arbitratorID, BossID: activation.Candidate.ID,
				Cells: append([]mapdata.CompetitiveCell(nil), selectedMap.AirborneCells...),
			}
		}
	}
	if activation.Active {
		if err = server.startSessionRoomMatchWithOptions(session, room, roomstate.StartValidationOptions{DeferCompetitiveTopology: true}); err != nil {
			return prepared, err
		}
	} else if len(projectedVirtualParticipants) != 0 {
		virtualTeamIDs := make([]byte, len(projectedVirtualParticipants))
		for index, participant := range projectedVirtualParticipants {
			virtualTeamIDs[index] = participant.TeamID
		}
		if err = server.startSessionRoomMatchWithOptions(session, room, roomstate.StartValidationOptions{VirtualCompetitiveTeamIDs: virtualTeamIDs}); err != nil {
			return prepared, err
		}
	} else if err = server.startSessionRoomMatch(session); err != nil {
		return prepared, err
	}
	profiles, consumeErr := server.consumeCompetitiveBossSummonItems(session, activation)
	if consumeErr != nil {
		_, rollbackErr := server.worldState().CompleteMatch(sessionWorldUIN(session), session.CurrentGameID)
		if rollbackErr != nil {
			return prepared, fmt.Errorf("%v; rollback competitive match %d after summon debit failure: %w", consumeErr, session.CurrentGameID, rollbackErr)
		}
		return prepared, consumeErr
	}
	prepared.bossSummonRefreshes = competitiveBossInventoryRefreshes(activation, profiles)
	session.CurrentMapID = selectedMap.ID
	server.projectRoomMatchFromLockedSession(session, session.RoomID, session.CurrentGameID, selectedMap.ID, nil)
	timeoutDuration, timeoutResultTimeMS := competitiveTimeoutTiming(ruleSpec.RoundDurationMS)
	prepared.competitiveTimeout = &competitiveTimeoutSchedule{
		RoomID: session.RoomID, GameID: session.CurrentGameID,
		// Client.exe renders its countdown from native rule-vtable slot 9.
		// GAME_BEGIN.GameTime is not a universal four-minute deadline. The
		// scene clock starts at zero and consumes its first 3000 ms in ReadyGo,
		// so the configured duration is already the absolute settlement deadline.
		Duration: timeoutDuration, ResultTimeMS: timeoutResultTimeMS,
	}
	prepared.followUpResult = fmt.Sprintf("qqt_competitive_game_begin_map_%d_family_%s", selectedMap.ID, selectedMap.Family)
	return prepared, nil
}
