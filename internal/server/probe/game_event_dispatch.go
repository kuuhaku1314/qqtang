package probe

import (
	"fmt"
	"time"

	"qqtang/internal/clientdata/sceneelement"
	"qqtang/internal/game/itemuse"
	"qqtang/internal/game/mapdata"
	"qqtang/internal/game/match"
	roomstate "qqtang/internal/game/room"
	"qqtang/internal/protocol/game"
)

// gameEventMessageResult contains transport work produced by one match event.
// Keeping it separate from the TCP connection loop prevents match rules from
// growing another command-by-command branch in handleTCPMessage.
type gameEventMessageResult struct {
	handled                      bool
	response                     []byte
	result                       string
	followUp                     []byte
	followUpResult               string
	startFollowUp                []byte
	startFollowUpResult          string
	startFollowUpDelay           time.Duration
	completeAdventureAfterSend   bool
	completeCompetitiveAfterSend bool
	adventureSettlement          *adventureSettlementCommit
	adventureRoomSettlement      *adventureRoomSettlementCommit
	competitiveRoomSettlement    *competitiveRoomSettlementCommit
	finalVictorySchedule         *adventureFinalVictorySchedule
	handledWithoutResponse       bool
	afterFollowUp                func()
	afterStartFollowUp           func()
	afterResponse                func()
}

// nativePeerMulticastMirrorSchema identifies REQUEST_PLAY events whose scene
// delivery is already owned by QQTPPP's Type-2 room multicast. The client also
// uploads these records through reliable TCP so that the service can validate
// identity and maintain settlement state. That reliable copy is not a second
// peer-delivery path: emitting a GAME_EVENT notification from it applies the
// same non-idempotent scene mutation again on every observer.
//
// Request-like entries in this list are resolved by the elected native
// arbitrator over that same Type-2 path. Server-owned request/notify pairs such
// as REQUEST_EAT_BOMB, REQUEST_USE_ITEM and REQUEST_PREPARED_USE_PROP are
// deliberately absent and retain their authoritative Go response.
func nativePeerMulticastMirrorSchema(schema uint16) bool {
	switch schema {
	case game.RequestLaunchMachine,
		game.RequestPushMapElement,
		game.RequestDestroyBox,
		game.RequestMoveBomb,
		game.RequestGetBun,
		game.RequestPutBun,
		game.RequestGetItem,
		game.RequestKillPlayer,
		game.RequestSavePlayer,
		game.PlayerBeExploded,
		game.PlayerThrowBomb,
		game.NotifyBombExplode,
		game.NotifyPlayerExploded,
		game.NotifyPlayerDieEvent,
		game.NotifyPlayerKilled,
		game.NotifyPlayerSaved,
		game.NotifyPlayerGetItem,
		game.NotifyDispatchItem,
		game.NotifyPlayerRelive,
		game.NotifyPlayerAffection,
		game.NotifyPlayerGetBun,
		game.NotifyPlayerPutBun,
		game.NotifyMapElementExploded,
		game.NotifyItemExploded,
		game.NotifyNPCUseSkill,
		game.NotifyNPCTalk,
		game.NotifyNPCDropItem,
		game.NotifyUseEmotion,
		game.NotifyRecoverAvatar,
		game.NotifyDispatchBomb,
		game.NotifyPlayerLeave,
		game.NotifyMachineFree,
		game.NotifyMachineAngry,
		game.NotifyMachineGrace,
		game.NotifyMachineCool,
		game.PlayerBeHarmed,
		game.NotifyPropTrigger,
		game.NotifyProduceItem,
		game.NotifyMachineMove,
		game.NotifyPushMapElement,
		game.NotifyMapElementKnock,
		game.NotifyMapElementStop,
		game.NotifyPlayerKnocked,
		game.NotifyPlayerTrapped,
		game.NotifyFire,
		game.NotifyGenerateBox,
		game.NotifyMapElement,
		game.NotifyPlayerMoveBomb,
		game.CreateNPCBossEvent:
		return true
	default:
		return false
	}
}

func (server *Server) handleGameEventMessage(config ListenerConfig, session *connectionSession, id, local, remote string, data []byte) gameEventMessageResult {
	commandInspection, commandErr := game.InspectLocalPacket(data)
	if commandErr != nil || commandInspection.Command != game.GameEventRequestCommand {
		return gameEventMessageResult{}
	}
	var response []byte
	result := ""
	var followUp []byte
	followUpResult := "qqt_game_begin"
	var startFollowUp []byte
	startFollowUpResult := "qqt_second_followup"
	startFollowUpDelay := time.Duration(0)
	completeAdventureAfterSend := false
	completeCompetitiveAfterSend := false
	var adventureSettlement *adventureSettlementCommit
	var adventureRoomSettlement *adventureRoomSettlementCommit
	var competitiveRoomSettlement *competitiveRoomSettlementCommit
	var finalVictorySchedule *adventureFinalVictorySchedule
	handledWithoutResponse := false
	handled := false
	var followUpPeerPlayers map[uint16]struct{}
	var startFollowUpPeerPlayers map[uint16]struct{}
	var afterResponse func()
	// authoritativeStateAfterResponse is deliberately separate from
	// afterResponse. Native Type-2 traffic has already delivered its scene
	// notification, so that delivery callback is cleared below. State owned by
	// the reliable audit copy must still be committed exactly once after the
	// session lock is released.
	var authoritativeStateAfterResponse func()
	var afterFollowUpAction func()

	if len(response) == 0 && config.Response.QQTGameEventRelay {
		inspection, inspectErr := game.InspectLocalPacket(data)
		if inspectErr == nil && inspection.Command == game.GameEventRequestCommand {
			handled = true
			result = ""
			var event game.GameEvent
			var relayErr error
			event, relayErr = game.ParseGameEventPayload(inspection.Payload)
			if relayErr == nil {
				switch event.Schema {
				case game.PlayerUseBomb:
					var use game.PlayerUseBombEvent
					var nativeActor bool
					use, relayErr = game.ParsePlayerUseBombEvent(event)
					var category roomstate.Category
					if relayErr == nil {
						category, relayErr = server.activeSessionMatchCategory(session)
					}
					if relayErr == nil && category == roomstate.CategoryCompetitive {
						var battle *match.CompetitiveBattle
						battle, relayErr = server.competitiveBattle(session.CurrentGameID)
						if relayErr == nil {
							if battle.HasParticipant(use.PlayerID) {
								_, relayErr = server.requireMatchParticipantActor(session, use.PlayerID, "player-use-bomb")
							} else {
								nativeActor = true
								_, relayErr = authorizeCompetitiveActor(session, battle, use.PlayerID, "player-use-bomb")
							}
						}
					} else if relayErr == nil && category == roomstate.CategoryAdventure {
						_, relayErr = server.requireMatchParticipantActor(session, use.PlayerID, "player-use-bomb")
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_player_%d_use_bomb_%d", use.PlayerID, use.BombID)
						// QQTPPP Type-2 keeps its gameplay payload opaque, so the Go
						// relay cannot rewrite the request-only 0x0FA3 contained in
						// that payload. Participant bubbles are reconstructed from the
						// observer's cached loadout, but native Boss/NPC actors have no
						// such account loadout. Send their byte-identical peer form
						// 0x138B on the reliable path so every observer consumes the
						// authoritative BombID. Do not mirror ordinary participants.
						if nativeActor {
							followUp, _, relayErr = game.BuildLocalGameEventNotification(data, session.RoomID, server.nextGameDataSequence())
							followUpResult = fmt.Sprintf("qqt_game_event_notify_native_player_%d_use_bomb_%d", use.PlayerID, use.BombID)
						}
					}
				case game.NotifyBombExplode:
					if battle, battleErr := server.competitiveBattle(session.CurrentGameID); battleErr == nil && battle.UsesSculptureObjective() {
						relayErr = recordCompetitiveSculptureExplosion(battle, session.Profile.PlayerID, event)
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_native_0x%04X", event.Schema)
					}
				case game.RequestEatBomb:
					var action game.EatBombAction
					action, relayErr = game.ParseEatBombAction(event)
					if relayErr == nil {
						_, relayErr = server.requireMatchParticipantActor(session, action.PlayerID, "eat-bomb")
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						followUp, relayErr = game.BuildLocalPlayerEatBombNotify(data, session.RoomID, server.nextGameDataSequence(), action)
						result = fmt.Sprintf("qqt_game_event_ack_eat_bomb_player_%d_owner_%d", action.PlayerID, action.BombPlayerID)
						followUpResult = fmt.Sprintf("qqt_game_event_notify_eat_bomb_player_%d_owner_%d", action.PlayerID, action.BombPlayerID)
					}
				case game.NotifyPlayerEatBomb:
					relayErr = fmt.Errorf("NOTIFY_PLAYER_EAT_BOMB is server-generated and cannot be submitted by a peer")
				case game.RequestLaunchMachine:
					var launch game.MachineLaunchEvent
					launch, relayErr = game.ParseMachineLaunchEvent(event)
					if relayErr == nil {
						_, relayErr = server.requireMatchParticipantActor(session, launch.PlayerID, "machine-launch")
					}
					var battle *match.CompetitiveBattle
					if relayErr == nil {
						battle, relayErr = server.requireCompetitiveRule(session, mapdata.CompetitiveRuleMachine, launch.PlayerID)
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_machine_launch_player_%d_arbitrator_%d", launch.PlayerID, battle.ArbitratorPlayerID())
					}
				case game.NotifyMachineFree, game.NotifyMachineAngry, game.NotifyMachineGrace, game.NotifyMachineCool, game.NotifyMachineMove:
					var state game.MachineBombState
					state, relayErr = game.ParseMachineBombState(event)
					var battle *match.CompetitiveBattle
					if relayErr == nil {
						battle, relayErr = server.requireCompetitiveRule(session, mapdata.CompetitiveRuleMachine, state.PlayerID)
					}
					if relayErr == nil && !battle.IsArbitrator(session.Profile.PlayerID) {
						relayErr = fmt.Errorf("machine state source player %d is not the match arbitrator", session.Profile.PlayerID)
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_machine_state_0x%04X_player_%d_bombs_%d", event.Schema, state.PlayerID, len(state.Bombs))
					}
				case game.RequestPushMapElement:
					var push game.PushMapElementRequest
					push, relayErr = game.ParsePushMapElementRequest(event)
					if relayErr == nil {
						_, relayErr = server.requireMatchParticipantActor(session, push.PlayerID, "push-map-element")
					}
					var battle *match.CompetitiveBattle
					if relayErr == nil {
						battle, relayErr = server.requireCompetitiveRule(session, mapdata.CompetitiveRuleBox, push.PlayerID)
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_push_map_element_player_%d_arbitrator_%d", push.PlayerID, battle.ArbitratorPlayerID())
					}
				case game.RequestDestroyBox:
					var destroy game.DestroyBoxRequest
					destroy, relayErr = game.ParseDestroyBoxRequest(event)
					if relayErr == nil {
						_, relayErr = server.requireMatchParticipantActor(session, destroy.PlayerID, "destroy-box")
					}
					var battle *match.CompetitiveBattle
					if relayErr == nil {
						battle, relayErr = server.requireCompetitiveRule(session, mapdata.CompetitiveRuleBox, destroy.PlayerID)
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_destroy_box_player_%d_arbitrator_%d", destroy.PlayerID, battle.ArbitratorPlayerID())
					}
				case game.NotifyPushMapElement, game.NotifyMapElementKnock, game.NotifyFire, game.NotifyGenerateBox:
					// The native arbitrator applies these notifications locally and
					// QQTPPP multicasts them before the reliable mirror is uploaded.
					var referencedPlayerID uint16
					switch event.Schema {
					case game.NotifyPushMapElement:
						var push game.PushMapElementNotify
						push, relayErr = game.ParsePushMapElementNotify(event)
						referencedPlayerID = push.PlayerID
					case game.NotifyMapElementKnock:
						_, relayErr = game.ParseMapElementKnockEvent(event)
					case game.NotifyFire:
						var fire game.PlayerTimedStateEvent
						fire, relayErr = game.ParsePlayerTimedStateEvent(event)
						referencedPlayerID = fire.PlayerID
					case game.NotifyGenerateBox:
						_, relayErr = game.ParseGenerateBoxesEvent(event)
					}
					var battle *match.CompetitiveBattle
					if relayErr == nil {
						battle, relayErr = server.requireCompetitiveRule(session, mapdata.CompetitiveRuleBox, referencedPlayerID)
					}
					if relayErr == nil && !battle.IsArbitrator(session.Profile.PlayerID) {
						relayErr = fmt.Errorf("box-state source player %d is not the match arbitrator", session.Profile.PlayerID)
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_box_state_0x%04X", event.Schema)
					}
				case game.NotifyMapElementStop, game.NotifyPlayerKnocked, game.NotifyPlayerTrapped:
					// These box-physics observations are not guarded by the native
					// arbitrator check in Client.exe. Validate any referenced player
					// against the active battle, but do not reject the observing peer.
					// The observer has already applied its own result and QQTPPP has
					// already multicast it, so reliable TCP only acknowledges it.
					// In contrast 0x115F/0x1160/0x1167/0x1168 are generated by the
					// elected rule object and remain arbitrator-only above.
					var referencedPlayerID uint16
					switch event.Schema {
					case game.NotifyMapElementStop:
						_, relayErr = game.ParseMapElementStopEvent(event)
					case game.NotifyPlayerKnocked:
						var knocked game.PlayerKnockedEvent
						knocked, relayErr = game.ParsePlayerKnockedEvent(event)
						referencedPlayerID = knocked.PlayerID
					case game.NotifyPlayerTrapped:
						var trapped game.PlayerTimedStateEvent
						trapped, relayErr = game.ParsePlayerTimedStateEvent(event)
						referencedPlayerID = trapped.PlayerID
					}
					if relayErr == nil {
						_, relayErr = server.requireCompetitiveRule(session, mapdata.CompetitiveRuleBox, referencedPlayerID)
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_box_observation_0x%04X", event.Schema)
					}
				case game.PlayerBeHarmed:
					// 0x10F4 targets either native-durability participants or a
					// registered rule-1/2 Boss object. In the Boss path every
					// non-lethal hit causes the arbitrator's rule object to choose
					// items and emit 0x116B; the server must relay this event before
					// that follow-up can exist.
					var harmed game.EntityHarmedEvent
					harmed, relayErr = game.ParseEntityHarmedEvent(event)
					var route competitiveHarmRoute
					if relayErr == nil {
						route, relayErr = server.requireCompetitiveHarmTarget(session, harmed.ObjectID, harmed.LossHP)
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					fresh := true
					remaining := byte(0)
					if relayErr == nil && route.Kind == competitiveHarmParticipant {
						remaining, fresh, relayErr = route.Battle.RecordNativeHarm(
							harmed.ObjectID, harmed.Time, harmed.PosX, harmed.PosY, harmed.IsAvatar, harmed.LossHP,
						)
					}
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_%s_%s_%d_harmed_%d_remaining_%d", route.Rule, route.Kind, harmed.ObjectID, harmed.LossHP, remaining)
						if !fresh {
							result += "_transport_duplicate"
						}
					}
				case game.NotifyNPCUseSkill:
					skill, parseErr := game.ParseNPCUseSkillEvent(event)
					relayErr = parseErr
					if relayErr == nil {
						relayErr = server.requireNativeNPCEventAuthority(session, skill.ObjectID, skill.SkillID, "NPC-use-skill notify")
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_npc_%d_skill_%d", skill.ObjectID, skill.SkillID)
					}
				case game.NotifyNPCTalk:
					talk, parseErr := game.ParseNPCTalkEvent(event)
					relayErr = parseErr
					if relayErr == nil {
						relayErr = server.requireNativeNPCEventAuthority(session, talk.ObjectID, 0, "NPC-talk notify")
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_npc_%d_talk", talk.ObjectID)
					}
				case game.RequestCancelUseProp:
					var cancel game.CancelUsePropEvent
					cancel, relayErr = game.ParseCancelUsePropEvent(event)
					if relayErr == nil {
						_, relayErr = server.requireMatchParticipantActor(session, cancel.PlayerID, "cancel-use-prop")
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						followUp, relayErr = game.BuildLocalCancelUsePropNotify(data, session.RoomID, server.nextGameDataSequence(), cancel)
						result = fmt.Sprintf("qqt_game_event_ack_cancel_prop_player_%d_prop_%d", cancel.PlayerID, cancel.PropID)
						followUpResult = fmt.Sprintf("qqt_game_event_notify_cancel_prop_player_%d_prop_%d", cancel.PlayerID, cancel.PropID)
					}
				case game.NotifyCancelUseProp:
					relayErr = fmt.Errorf("NOTIFY_CANCEL_USE_PROP is server-generated and cannot be submitted by a peer")
				case game.NotifyPropTrigger:
					var trigger game.PropTriggerEvent
					trigger, relayErr = game.ParsePropTriggerEvent(event)
					if relayErr == nil {
						relayErr = server.requireMatchArbitrator(session, "prop-trigger notify")
					}
					if relayErr == nil {
						category, categoryErr := server.activeSessionMatchCategory(session)
						if categoryErr != nil {
							relayErr = categoryErr
						} else if category == roomstate.CategoryAdventure {
							var battle *match.AdventureBattle
							battle, relayErr = server.adventureBattle(session.CurrentGameID)
							if relayErr == nil && !battle.HasParticipant(trigger.UserID) {
								relayErr = fmt.Errorf("prop-trigger user %d is not an adventure participant", trigger.UserID)
							}
							if relayErr == nil {
								for _, affected := range trigger.AffectedPlayers {
									if !battle.HasParticipant(affected.PlayerID) {
										relayErr = fmt.Errorf("prop-trigger affected player %d is not an adventure participant", affected.PlayerID)
										break
									}
								}
							}
						} else {
							var battle *match.CompetitiveBattle
							battle, relayErr = server.competitiveBattle(session.CurrentGameID)
							if relayErr == nil && !battle.HasParticipant(trigger.UserID) {
								relayErr = fmt.Errorf("prop-trigger user %d is not a competitive participant", trigger.UserID)
							}
							if relayErr == nil {
								for _, affected := range trigger.AffectedPlayers {
									if !battle.HasParticipant(affected.PlayerID) {
										relayErr = fmt.Errorf("prop-trigger affected player %d is not a competitive participant", affected.PlayerID)
										break
									}
								}
							}
						}
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_prop_trigger_%d_affected_%d", trigger.PropGUID, len(trigger.AffectedPlayers))
					}
				case game.PlayerKnocked, game.BoxStopped:
					// These two adjacent records are client-internal physics messages,
					// not network operations. Accepting them through REQUEST_PLAY would
					// let a peer bypass the authoritative 0x1162/0x1161 results.
					relayErr = fmt.Errorf("schema 0x%04X is an internal box-physics record", event.Schema)
				case game.RequestTankBaseHP:
					var change game.TankBaseHPChangeEvent
					var tankResult match.CompetitiveTankBaseHPResult
					var tankBattle *match.CompetitiveBattle
					change, relayErr = game.ParseTankBaseHPChangeEvent(event)
					if relayErr == nil {
						_, relayErr = server.requireMatchParticipantActor(session, change.PlayerID, "tank-base")
					}
					if relayErr == nil {
						tankBattle, relayErr = server.requireCompetitiveRule(session, mapdata.CompetitiveRuleTank, change.PlayerID)
						if relayErr == nil && change.TeamID > 0xff {
							relayErr = fmt.Errorf("tank-base team %d is outside room team range", change.TeamID)
						}
						if relayErr == nil {
							tankResult, relayErr = tankBattle.RecordTankBaseHPChange(change.PlayerID, byte(change.TeamID), change.ChangeHP)
							if relayErr == nil {
								result = fmt.Sprintf("qqt_game_event_ack_tank_base_player_%d_report_team_%d_base_team_%d_change_%d_hp_%d_destroyed_%t_applied_%t", change.PlayerID, change.TeamID, tankResult.BaseTeamID, change.ChangeHP, tankResult.HP, tankResult.Destroyed, tankResult.Applied)
							}
						}
					}
					if relayErr == nil {
						// The 5.2 table marks 0x15B3 client-to-server and the client
						// contains no downlink consumer for the same schema. It is an
						// HP report, not a notification to echo to peers.
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil && tankResult.Resolution.NewlyConcluded {
						startFollowUp, startFollowUpResult, competitiveRoomSettlement, relayErr = server.competitiveKillConclusion(session, data, change.Time, tankBattle, tankResult.Resolution)
						if relayErr == nil {
							startFollowUpDelay = competitiveNativeConclusionDelay
							completeCompetitiveAfterSend = true
						}
					}
				case game.PlayerThrowBomb:
					var action game.KickBombAction
					action, relayErr = game.ParseKickBombAction(event)
					var battle *match.CompetitiveBattle
					var rule mapdata.CompetitiveRuleKind
					if relayErr == nil {
						// Map capability is checked first. The actor check below then
						// accepts either the authenticated participant or the narrowly
						// authorized arbitrator-owned Boss proxy.
						battle, rule, relayErr = server.requireCompetitiveKickableBombRule(session, 0)
					}
					if relayErr == nil {
						_, relayErr = authorizeCompetitiveActor(session, battle, action.PlayerID, "kick-bomb")
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_kick_bomb_rule_%s_player_%d", rule, action.PlayerID)
					}
				case game.NotifyProduceItem:
					var item game.ProduceItem
					item, relayErr = game.ParseProduceItem(event)
					var battle *match.CompetitiveBattle
					if relayErr == nil {
						battle, relayErr = server.requireCompetitiveRule(session, mapdata.CompetitiveRuleKickBomb, 0)
					}
					if relayErr == nil && !battle.IsArbitrator(session.Profile.PlayerID) {
						relayErr = fmt.Errorf("produce-item player %d is not the match arbitrator", session.Profile.PlayerID)
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_kick_bomb_produce_item_%d", item.ItemID)
					}
				case game.RequestGetBun, game.RequestPutBun:
					var action game.BunActionEvent
					action, relayErr = game.ParseBunActionEvent(event)
					if relayErr == nil {
						_, relayErr = server.requireMatchParticipantActor(session, action.PlayerID, "bun action")
					}
					var battle *match.CompetitiveBattle
					var rule mapdata.CompetitiveRuleKind
					if relayErr == nil {
						battle, rule, relayErr = server.requireCompetitiveObjectiveRule(session, action.PlayerID)
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						verb := "get"
						if event.Schema == game.RequestPutBun {
							verb = "put"
						}
						result = fmt.Sprintf("qqt_game_event_ack_%s_%s_player_%d_bun_%d_team_%d_arbitrator_%d", rule, verb, action.PlayerID, action.BunID, action.BunTeamID, battle.ArbitratorPlayerID())
					}
				case game.NotifyPlayerGetBun, game.NotifyPlayerPutBun:
					var action game.BunActionEvent
					action, relayErr = game.ParseBunActionEvent(event)
					var battle *match.CompetitiveBattle
					var rule mapdata.CompetitiveRuleKind
					if relayErr == nil {
						battle, rule, relayErr = server.requireCompetitiveObjectiveRule(session, action.PlayerID)
					}
					if relayErr == nil && !battle.IsArbitrator(session.Profile.PlayerID) {
						relayErr = fmt.Errorf("%s bun-notify source player %d is not the match arbitrator", rule, session.Profile.PlayerID)
					}
					newEvent := true
					var resolution match.CompetitiveResolution
					if relayErr == nil && rule == mapdata.CompetitiveRuleBun {
						resolution, newEvent, relayErr = battle.RecordBunAction(
							event.Schema == game.NotifyPlayerGetBun, action.PlayerID, action.ClientTime,
							action.PosX, action.PosY, action.BunID, action.BunTeamID,
						)
					} else if relayErr == nil && rule == mapdata.CompetitiveRuleSculpture {
						resolution, newEvent, relayErr = battle.RecordSculptureAction(
							event.Schema == game.NotifyPlayerGetBun, action.PlayerID, action.ClientTime,
							action.PosX, action.PosY, action.BunID, action.BunTeamID,
						)
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					// 0x0FB7/0x0FB9 are already delivered in QQTPPP's native Type-2
					// peer UDP batch. The reliable copy closes server state and
					// settlement only; broadcasting it repeats one action on peers.
					if relayErr == nil && resolution.NewlyConcluded {
						startFollowUp, startFollowUpResult, competitiveRoomSettlement, relayErr = server.competitiveKillConclusion(
							session, data, action.ClientTime, battle, resolution,
						)
						// The producer and peers animate the final native deposit after
						// 0x0FB9. GAME_OVER must not freeze that last sculpture frame.
						startFollowUpDelay = competitiveNativeConclusionDelay
						completeCompetitiveAfterSend = competitiveRoomSettlement != nil
					}
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_%s_notify_0x%04X_player_%d_bun_%d_team_%d", rule, event.Schema, action.PlayerID, action.BunID, action.BunTeamID)
						if !newEvent {
							result += "_transport_duplicate"
						}
					}
				case game.RequestWrestle:
					var action game.WrestleAction
					action, relayErr = game.ParseWrestleAction(event)
					if relayErr == nil {
						_, relayErr = server.requireMatchParticipantActor(session, action.PlayerID, "wrestle")
					}
					if relayErr == nil {
						_, relayErr = server.requireCompetitiveRule(session, mapdata.CompetitiveRuleWrestle, action.PlayerID)
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						followUp, relayErr = game.BuildLocalWrestleNotify(data, session.RoomID, server.nextGameDataSequence(), action)
						result = fmt.Sprintf("qqt_game_event_ack_wrestle_player_%d_skill_%d", action.PlayerID, action.SkillType)
						followUpResult = fmt.Sprintf("qqt_game_event_notify_wrestle_player_%d_skill_%d", action.PlayerID, action.SkillType)
					}
				case game.NotifyWrestleSkill:
					relayErr = fmt.Errorf("NOTIFY_WRESTLE_SKILL is server-generated and cannot be submitted by a peer")
				case game.RequestUseItem:
					var use game.WorldUseItemEvent
					use, relayErr = game.ParseWorldUseItemEvent(event)
					if relayErr == nil {
						_, relayErr = server.requireMatchParticipantActor(session, use.PlayerID, "world-use-item")
					}
					var plan itemuse.Plan
					if relayErr == nil {
						plan, relayErr = server.resolveMatchItemUse(session, itemuse.PathBattleWorldItem, use.ItemID)
					}
					if relayErr == nil && plan.Consumption != itemuse.ConsumeNone {
						relayErr = fmt.Errorf("world-use-item %d resolved unsafe consumption %q", use.ItemID, plan.Consumption)
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						followUp, relayErr = game.BuildLocalWorldUseItemNotify(data, session.RoomID, server.nextGameDataSequence(), use)
						result = fmt.Sprintf("qqt_game_event_ack_world_item_player_%d_item_%d", use.PlayerID, use.ItemID)
						followUpResult = fmt.Sprintf("qqt_game_event_notify_world_item_player_%d_item_%d", use.PlayerID, use.ItemID)
					}
					if relayErr == nil && session.CurrentGameID != 0 {
						gameID := session.CurrentGameID
						confirmedUse := use
						afterFollowUpAction = appendPostResponse(afterFollowUpAction, func() {
							server.recordCompetitiveAINativeBattleAction(gameID, confirmedUse)
						})
					}
				case game.NotifyUseEmotion:
					var emotion game.UseEmotionEvent
					emotion, relayErr = game.ParseUseEmotionEvent(event)
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_emotion_uin_%d_id_%d", emotion.UIN, emotion.EmotionID)
					}
				case game.CreateNPCBossEvent:
					// CREATE_NPC_BOSS belongs to the native rule-8 handler set. The
					// original box-rule initializer is the only proven installer of
					// the 60-second producer; Water11/rule 1 does not install it. The
					// 60-second gate is producer-local state, not CREATE_NPC_BOSS.Time:
					// the native builder clears the whole object and never writes that
					// trailing field, so an authentic rule-8 request carries Time=0.
					// Validate the arbitrator's exact entity table rather than accepting
					// the schema on every competitive map or guessing
					// entity IDs, coordinates and HP from a historical map-name list.
					var bosses game.CreateNPCBoss
					bosses, relayErr = game.ParseCreateNPCBossNetwork(event.Body)
					if relayErr == nil && len(bosses.Bosses) != 1 {
						relayErr = fmt.Errorf("rule-8 CREATE_NPC_BOSS contains %d entities, want exactly 1", len(bosses.Bosses))
					}
					if relayErr == nil {
						for _, boss := range bosses.Bosses {
							if boss.BossID < 20_001 || boss.BossID >= 23_000 || boss.RoleID < 25 || boss.RoleID > 27 || boss.TeamID != 7 || boss.AIType != 2 ||
								boss.Row == 0 || boss.Col == 0 || boss.HP != 0 || boss.Rate != 0 || boss.Bubble != 0 || boss.Power != 0 ||
								len(boss.NormalItems) != 0 || len(boss.OutfitItems) != 2 ||
								boss.OutfitItems[0] != (game.BossItemInfo{ItemID: 500, ItemCount: 3}) ||
								boss.OutfitItems[1] != (game.BossItemInfo{ItemID: 501, ItemCount: 2}) || len(boss.Skills) != 0 || len(boss.Metadata) != 0 {
								relayErr = fmt.Errorf("rule-8 CREATE_NPC_BOSS boss %d does not match the native entity template", boss.BossID)
								break
							}
						}
					}
					var battle *match.CompetitiveBattle
					if relayErr == nil {
						battle, relayErr = server.requireCompetitiveRule(session, mapdata.CompetitiveRuleBox, 0)
						if relayErr == nil && !battle.IsArbitrator(session.Profile.PlayerID) {
							relayErr = fmt.Errorf("CREATE_NPC_BOSS player %d is not the match arbitrator", session.Profile.PlayerID)
						}
					}
					if relayErr == nil {
						objectIDs := make([]uint16, 0, len(bosses.Bosses))
						for _, boss := range bosses.Bosses {
							objectIDs = append(objectIDs, boss.BossID)
						}
						relayErr = battle.RegisterNativeNPCEntities(objectIDs)
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_create_npc_boss_count_%d_time_%d", len(bosses.Bosses), bosses.Time)
					}
				case game.RequestGameNextMap:
					nextResult, nextErr := server.handleAdventureNextMap(session, data)
					relayErr = nextErr
					response, result = nextResult.response, nextResult.result
					followUp, followUpResult = nextResult.followUp, nextResult.followUpResult
					startFollowUp, startFollowUpResult = nextResult.startFollowUp, nextResult.startFollowUpResult
					startFollowUpDelay = nextResult.startFollowUpDelay
					completeAdventureAfterSend = nextResult.completeAfterSend
					adventureSettlement, adventureRoomSettlement = nextResult.settlement, nextResult.roomSettlement
					followUpPeerPlayers, startFollowUpPeerPlayers = nextResult.followUpPeerPlayers, nextResult.startFollowUpPeerPlayers
					afterResponse = appendPostResponse(afterResponse, nextResult.afterResponse)
				case game.NotifyNPCDropItem:
					var drop game.NPCDropItemEvent
					drop, relayErr = game.ParseNPCDropItemEvent(event)
					if relayErr == nil {
						relayErr = server.requireMatchArbitrator(session, "NPC drop")
					}
					if relayErr == nil {
						category, categoryErr := server.activeSessionMatchCategory(session)
						if categoryErr != nil {
							relayErr = categoryErr
						} else if category == roomstate.CategoryCompetitive {
							battle, battleErr := server.competitiveBattle(session.CurrentGameID)
							if battleErr != nil {
								relayErr = battleErr
							} else if battle.HasBossEntity(drop.ObjectID) {
								itemIDs := make([]uint32, 0, len(drop.Items))
								for _, item := range drop.Items {
									itemIDs = append(itemIDs, item.ItemID)
								}
								relayErr = battle.RecordBossSceneDrops(drop.ObjectID, itemIDs)
							}
						}
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_npc_drop_object_%d_items_%d", drop.ObjectID, len(drop.Items))
					}
				case game.PlayerBeExploded:
					var exploded game.PlayerExplodedEvent
					exploded, relayErr = game.ParsePlayerExplodedEvent(event)
					var category roomstate.Category
					var competitiveBattle *match.CompetitiveBattle
					participantTarget := false
					virtualParticipantTarget := false
					if relayErr == nil {
						category, relayErr = server.activeSessionMatchCategory(session)
					}
					if relayErr == nil && category == roomstate.CategoryCompetitive {
						competitiveBattle, relayErr = server.competitiveBattle(session.CurrentGameID)
						if relayErr == nil {
							participantTarget = competitiveBattle.HasParticipant(exploded.PlayerID)
							if participantTarget {
								if !competitiveBattle.AuthorizesParticipantObservation(session.Profile.PlayerID, exploded.PlayerID) {
									relayErr = fmt.Errorf("player-exploded reporter %d is not authorized for participant %d", session.Profile.PlayerID, exploded.PlayerID)
								}
								virtualParticipantTarget = server.competitiveAIHasVirtualParticipant(session.CurrentGameID, exploded.PlayerID)
								if relayErr == nil && virtualParticipantTarget && !competitiveBattle.IsArbitrator(session.Profile.PlayerID) {
									relayErr = fmt.Errorf("player-exploded virtual target %d requires arbitrator %d, got %d", exploded.PlayerID, competitiveBattle.ArbitratorPlayerID(), session.Profile.PlayerID)
								}
								if relayErr == nil && virtualParticipantTarget && !server.competitiveAIHasHitRequest(session.CurrentGameID, exploded) {
									relayErr = fmt.Errorf("player-exploded virtual target %d has no exact pending request", exploded.PlayerID)
								}
							} else if competitiveBattle.AuthorizesNativeNPCProxy(session.Profile.PlayerID, exploded.PlayerID) {
								// Rule-8 AIType=2 entities use the shared collision path and
								// have no named-Boss HP contract.
							} else if competitiveBattle.AuthorizesBossProxy(session.Profile.PlayerID, exploded.PlayerID) {
								if !exploded.IsAvatar {
									relayErr = fmt.Errorf("normal-form Boss %d used player-exploded instead of Boss-harmed", exploded.PlayerID)
								}
							} else {
								relayErr = fmt.Errorf("player-exploded reporter %d is not authorized for native entity %d", session.Profile.PlayerID, exploded.PlayerID)
							}
						}
					} else if relayErr == nil && category == roomstate.CategoryAdventure {
						_, relayErr = server.requireMatchParticipantActor(session, exploded.PlayerID, "player-exploded")
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_player_%d_exploded_avatar_%t", exploded.PlayerID, exploded.IsAvatar)
					}
					if relayErr == nil && virtualParticipantTarget {
						gameID, reporterID, confirmedHit := session.CurrentGameID, session.Profile.PlayerID, exploded
						authoritativeStateAfterResponse = appendPostResponse(authoritativeStateAfterResponse, func() {
							server.commitCompetitiveAIHitConfirmation(gameID, reporterID, confirmedHit)
						})
					}
					if relayErr == nil && category == roomstate.CategoryCompetitive && participantTarget && !virtualParticipantTarget && !exploded.IsAvatar {
						// IsAvatar identifies the expendable transformed/avatar form,
						// not participant/Boss identity. A participant hit with
						// IsAvatar=1 consumes that extra form; only normal participant
						// form (0) enters syrup. Boss hits never enter player state.
						relayErr = competitiveBattle.RecordTrapped(exploded.PlayerID)
					}
				case game.RequestKillPlayer, game.RequestSavePlayer, game.NotifyPlayerSaved:
					requestSchema := event.Schema
					var interaction game.PlayerInteractionEvent
					interaction, relayErr = game.ParsePlayerInteractionEvent(event)
					var category roomstate.Category
					var battle *match.CompetitiveBattle
					bossKill := false
					nativeNPCInteraction := false
					if relayErr == nil {
						category, relayErr = server.activeSessionMatchCategory(session)
					}
					if relayErr == nil && category == roomstate.CategoryCompetitive {
						battle, relayErr = server.competitiveBattle(session.CurrentGameID)
					}
					if relayErr == nil && interaction.PlayerID != session.Profile.PlayerID {
						bossKill = requestSchema == game.RequestKillPlayer && category == roomstate.CategoryCompetitive &&
							battle.HasParticipant(interaction.DestinationPlayerID) &&
							battle.AuthorizesBossProxy(session.Profile.PlayerID, interaction.PlayerID)
						nativeNPCInteraction = requestSchema == game.RequestKillPlayer && category == roomstate.CategoryCompetitive &&
							battle.HasParticipant(interaction.DestinationPlayerID) &&
							battle.AuthorizesNativeNPCProxy(session.Profile.PlayerID, interaction.PlayerID)
						if !bossKill && !nativeNPCInteraction {
							_, relayErr = server.requireMatchParticipantActor(session, interaction.PlayerID, "player-interaction")
						}
					}
					if relayErr == nil && requestSchema == game.RequestKillPlayer && category == roomstate.CategoryCompetitive &&
						interaction.PlayerID == session.Profile.PlayerID && battle.HasNativeNPCEntity(interaction.DestinationPlayerID) {
						nativeNPCInteraction = true
					}
					if relayErr == nil && category == roomstate.CategoryCompetitive {
						if relayErr == nil {
							switch requestSchema {
							case game.RequestKillPlayer:
								if !nativeNPCInteraction {
									var resolution match.CompetitiveResolution
									if bossKill {
										resolution, relayErr = battle.RecordBossKill(interaction.PlayerID, interaction.DestinationPlayerID)
									} else {
										resolution, relayErr = battle.RecordKill(interaction.PlayerID, interaction.DestinationPlayerID)
									}
									if relayErr == nil {
										startFollowUp, startFollowUpResult, competitiveRoomSettlement, relayErr = server.competitiveKillConclusion(
											session, data, interaction.ClientTime, battle, resolution,
										)
										startFollowUpDelay = competitiveNativeConclusionDelay
										completeCompetitiveAfterSend = competitiveRoomSettlement != nil
									}
								}
							case game.RequestSavePlayer, game.NotifyPlayerSaved:
								relayErr = battle.RecordRescue(interaction.PlayerID, interaction.DestinationPlayerID)
							}
						}
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_0x%04X_player_%d_destination_%d", requestSchema, interaction.PlayerID, interaction.DestinationPlayerID)
					}
					if relayErr == nil && requestSchema == game.NotifyPlayerSaved && category == roomstate.CategoryAdventure {
						var battle *match.AdventureBattle
						battle, relayErr = server.adventureBattle(session.CurrentGameID)
						if relayErr == nil {
							_, relayErr = battle.RecordPlayerSaved(interaction.PlayerID, interaction.DestinationPlayerID, server.config.AdventureRewards.PlayerRescueScore)
						}
					}
				case game.NotifyPlayerKilled:
					var killed game.PlayerKilledEvent
					killed, relayErr = game.ParsePlayerKilledEvent(event)
					if relayErr == nil {
						relayErr = server.requireMatchArbitrator(session, "player-killed notify")
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					// The authoritative 0x0FA9 is already in QQTPPP's Type-2 peer
					// UDP batch. This reliable copy is an audit/ACK uplink; relaying it
					// again creates a second stack of death-dropped scene objects.
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_player_%d_killed_%d_items_%d", killed.PlayerID, killed.DestinationPlayerID, len(killed.Items))
					}
					if relayErr == nil {
						var category roomstate.Category
						category, relayErr = server.activeSessionMatchCategory(session)
						if relayErr == nil && category == roomstate.CategoryCompetitive {
							var battle *match.CompetitiveBattle
							battle, relayErr = server.competitiveBattle(session.CurrentGameID)
							if relayErr == nil {
								battle.RecordNativeRelay(event.Schema, event.Body)
							}
							nativeNPCInteraction := false
							if relayErr == nil {
								nativeNPCInteraction = (battle.HasNativeNPCEntity(killed.PlayerID) && battle.HasParticipant(killed.DestinationPlayerID)) ||
									(battle.HasParticipant(killed.PlayerID) && battle.HasNativeNPCEntity(killed.DestinationPlayerID))
								if (battle.HasNativeNPCEntity(killed.PlayerID) || battle.HasNativeNPCEntity(killed.DestinationPlayerID)) && !nativeNPCInteraction {
									relayErr = fmt.Errorf("native NPC killed source/target %d/%d is invalid", killed.PlayerID, killed.DestinationPlayerID)
								}
							}
							if relayErr == nil && !nativeNPCInteraction {
								itemIDs := make([]uint32, 0, len(killed.Items))
								for _, item := range killed.Items {
									itemIDs = append(itemIDs, item.ItemID)
								}
								relayErr = battle.RecordTreasureScatter(killed.DestinationPlayerID, itemIDs)
							}
							if relayErr == nil && !nativeNPCInteraction {
								var resolution match.CompetitiveResolution
								if battle.HasBossEntity(killed.PlayerID) {
									resolution, relayErr = battle.RecordBossKill(killed.PlayerID, killed.DestinationPlayerID)
								} else {
									resolution, relayErr = battle.RecordKill(killed.PlayerID, killed.DestinationPlayerID)
								}
								if relayErr == nil {
									startFollowUp, startFollowUpResult, competitiveRoomSettlement, relayErr = server.competitiveKillConclusion(
										session, data, killed.ClientTime, battle, resolution,
									)
									// The reliable ACK can arrive before the native Type-2
									// collision/death presentation has finished. Keep the
									// same grace as 0x0FA7 before GAME_OVER clears the room.
									startFollowUpDelay = competitiveNativeConclusionDelay
									completeCompetitiveAfterSend = competitiveRoomSettlement != nil
								}
							}
							if relayErr == nil {
								gameID := session.CurrentGameID
								killerID := killed.PlayerID
								targetID := killed.DestinationPlayerID
								if server.competitiveAIRuntimeForGame(gameID) != nil {
									items := append([]game.GameItem(nil), killed.Items...)
									authoritativeStateAfterResponse = appendPostResponse(authoritativeStateAfterResponse, func() {
										server.recordCompetitiveAINativeElimination(gameID, killerID, targetID, items)
									})
								}
							}
						}
					}
				case game.RequestGetItem, game.NotifyPlayerGetItem:
					var item game.PlayerItemEvent
					item, relayErr = game.ParsePlayerItemEvent(event)
					var category roomstate.Category
					var competitiveBattle *match.CompetitiveBattle
					var adventureBattle *match.AdventureBattle
					var arbitratorID uint16
					nativeActor := false
					if relayErr == nil {
						category, relayErr = server.activeSessionMatchCategory(session)
					}
					if relayErr == nil && category == roomstate.CategoryCompetitive {
						competitiveBattle, relayErr = server.competitiveBattle(session.CurrentGameID)
						if relayErr == nil && event.Schema == game.RequestGetItem {
							nativeActor, relayErr = authorizeCompetitiveActor(session, competitiveBattle, item.PlayerID, "player-item request")
						} else if relayErr == nil {
							relayErr = server.requireMatchArbitrator(session, "player-item notify")
							if relayErr == nil {
								nativeActor = competitiveBattle.AuthorizesBossProxy(session.Profile.PlayerID, item.PlayerID) ||
									competitiveBattle.AuthorizesNativeNPCProxy(session.Profile.PlayerID, item.PlayerID)
								if !competitiveBattle.HasParticipant(item.PlayerID) && !nativeActor {
									relayErr = fmt.Errorf("player-item notify actor %d is not an active participant or native entity", item.PlayerID)
								}
							}
						}
						if relayErr == nil {
							arbitratorID = competitiveBattle.ArbitratorPlayerID()
						}
					} else if relayErr == nil && category == roomstate.CategoryAdventure {
						adventureBattle, relayErr = server.adventureBattle(session.CurrentGameID)
						if relayErr == nil && event.Schema == game.RequestGetItem {
							_, relayErr = server.requireMatchParticipantActor(session, item.PlayerID, "player-item request")
						}
						if relayErr == nil && event.Schema == game.NotifyPlayerGetItem {
							relayErr = server.requireMatchArbitrator(session, "player-item notify")
						}
						if relayErr == nil && !adventureBattle.HasParticipant(item.PlayerID) {
							relayErr = fmt.Errorf("player-item actor %d is not an adventure participant", item.PlayerID)
						}
						if relayErr == nil {
							arbitratorID = adventureBattle.ArbitratorPlayerID()
						}
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil && event.Schema == game.RequestGetItem {
						// 0x0FAC is a native-rule request. QQTPPP has already routed it
						// to the elected rule object; reliable TCP only validates/ACKs it.
						result = fmt.Sprintf("qqt_game_event_ack_get_item_request_player_%d_item_%d_arbitrator_%d", item.PlayerID, item.ItemID, arbitratorID)
					}
					if relayErr == nil && event.Schema == game.NotifyPlayerGetItem {
						// The arbitrator has already applied its own 0x0FAD and QQTPPP
						// has multicast it to peers. This reliable copy is retained for
						// ACK/accounting only; a second server downlink leaves stale or
						// duplicated scene items on observers.
						relayFresh := true
						if category == roomstate.CategoryCompetitive && competitiveBattle != nil {
							relayFresh = competitiveBattle.RecordNativeRelay(event.Schema, event.Body)
						}
						result = fmt.Sprintf("qqt_game_event_ack_get_item_notify_player_%d_item_%d", item.PlayerID, item.ItemID)
						if !relayFresh {
							result += "_transport_duplicate"
						}
					}
					if relayErr == nil && event.Schema == game.NotifyPlayerGetItem {
						if category == roomstate.CategoryCompetitive {
							// Native NPC pickups, including Boss transformation items,
							// change only that object's client-side form. They are not player
							// rewards and must never enter settlement accounting.
							if nativeActor {
								break
							}
							if match.IsTreasureGem(item.ItemID) {
								var battle *match.CompetitiveBattle
								battle, relayErr = server.requireCompetitiveRule(session, mapdata.CompetitiveRuleTreasure, item.PlayerID)
								if relayErr == nil {
									var recorded bool
									var resolution match.CompetitiveResolution
									resolution, recorded, relayErr = battle.RecordTreasurePickup(item.PlayerID, item.ClientTime, item.ItemID, item.PosX, item.PosY)
									if relayErr == nil && !recorded {
										result += "_duplicate_not_counted"
									}
									if relayErr == nil && resolution.NewlyConcluded {
										startFollowUp, startFollowUpResult, competitiveRoomSettlement, relayErr = server.competitiveKillConclusion(
											session, data, item.ClientTime, battle, resolution,
										)
										startFollowUpDelay = competitiveNativeConclusionDelay
										completeCompetitiveAfterSend = competitiveRoomSettlement != nil
									}
								}
							} else if reward, proven := sceneelement.NativeReward(sceneelement.ID(item.ItemID)); proven && reward.Kind != sceneelement.RewardTreasureScore {
								if competitiveBattle != nil {
									var recorded bool
									recorded, relayErr = competitiveBattle.RecordNativeSceneReward(item.PlayerID, item.ClientTime, item.ItemID, item.PosX, item.PosY)
									if relayErr == nil && !recorded {
										result += "_duplicate_not_counted"
									}
								}
							} else if sceneelement.IsPermanentInventoryPickup(item.ItemID) && competitiveBattle != nil {
								var recorded bool
								recorded, relayErr = competitiveBattle.RecordBossPermanentItemPickup(item.PlayerID, item.ClientTime, item.ItemID, item.PosX, item.PosY)
								if relayErr == nil && !recorded {
									result += "_not_boss_inventory_or_duplicate"
								}
							}
							break
						} else if category != roomstate.CategoryAdventure {
							break
						}
						var battle *match.AdventureBattle
						battle, relayErr = server.adventureBattle(session.CurrentGameID)
						if relayErr == nil {
							var recorded bool
							recorded, relayErr = battle.RecordPlayerItemPickup(match.AdventureItemPickup{
								PlayerID: item.PlayerID, ClientTime: item.ClientTime,
								ItemID: item.ItemID, PosX: item.PosX, PosY: item.PosY,
							}, server.config.AdventureRewards.ItemPickupScore)
							if relayErr == nil && !recorded {
								result += "_duplicate_not_counted"
							}
							// Pickups are committed to the account inventory at settlement.
							// The settlement path then sends the final absolute ITEM_INFO
							// records in one or more schema-0x081D notifications.
						}
					}
				case game.RequestPreparedUseProp:
					var use game.PreparedUsePropEvent
					use, relayErr = game.ParsePreparedUsePropEvent(event)
					var plan itemuse.Plan
					if relayErr == nil {
						plan, relayErr = server.resolveMatchItemUse(session, itemuse.PathPreparedProp, use.ItemID)
					}
					if relayErr == nil && plan.Consumption != itemuse.ConsumeOnAccepted {
						relayErr = fmt.Errorf("prepared-use-prop item %d resolved unsafe consumption %q", use.ItemID, plan.Consumption)
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						followUp, relayErr = game.BuildLocalPreparedUsePropNotify(data, session.RoomID, server.nextGameDataSequence(), use)
						followUpResult = fmt.Sprintf("qqt_game_event_notify_prepared_use_prop_player_%d_item_%d", use.PlayerID, use.ItemID)
					}
					var remaining game.ItemInfo
					var preparedActor *connectionSession
					if relayErr == nil {
						remaining, preparedActor, relayErr = server.consumeSessionPreparedItem(session, use)
					}
					if relayErr == nil {
						actorUIN := preparedActor.liveUIN.Load()
						if preparedActor == session {
							actorUIN = session.UIN
						}
						template := data
						if preparedActor != session {
							template = preparedActor.packetTemplate()
							if len(template) == 0 {
								relayErr = fmt.Errorf("prepared-use-prop actor %d has no packet template", use.PlayerID)
							}
						}
						var inventoryRefresh []byte
						if relayErr == nil {
							inventoryRefresh, relayErr = game.BuildLocalPlayerItemAddNotification(template, game.PlayerItemAddNotification{
								UIN: actorUIN, Time: uint32(time.Now().Unix()), SourceUIN: actorUIN,
								Items: []game.ItemInfo{remaining},
							})
						}
						if relayErr == nil && preparedActor == session {
							startFollowUp = inventoryRefresh
						} else if relayErr == nil {
							actor := preparedActor
							packet := append([]byte(nil), inventoryRefresh...)
							afterFollowUpAction = func() {
								time.Sleep(50 * time.Millisecond)
								server.writeTCP(actor.connection, actor.connectionID, actor.localAddress, actor.remoteAddress, packet,
									fmt.Sprintf("qqt_player_item_add_profile_item_%d_absolute_%d_actor_%d", remaining.ItemID, remaining.NumOfItem, use.PlayerID))
							}
						}
						startFollowUpResult = fmt.Sprintf("qqt_player_item_add_profile_item_%d_absolute_%d", remaining.ItemID, remaining.NumOfItem)
						startFollowUpDelay = 50 * time.Millisecond
					}
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_prepared_use_prop_player_%d_item_%d", use.PlayerID, use.ItemID)
					}
				case game.NotifyDispatchItem:
					var distribution game.DispatchItemData
					distribution, relayErr = game.ParseDispatchItemEvent(event)
					if relayErr == nil {
						relayErr = server.requireMatchArbitrator(session, "item dispatch")
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_dispatch_items_%d_delayed_%d", len(distribution.Items), len(distribution.DelayedItems))
					}
				case game.NotifyRecoverAvatar:
					var recovery game.AvatarRecoveryEvent
					recovery, relayErr = game.ParseAvatarRecoveryEvent(event)
					if relayErr == nil {
						relayErr = server.requireAvatarRecoveryAuthority(session, recovery.PlayerID)
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_avatar_recovery_player_%d", recovery.PlayerID)
					}
				case game.NotifyDispatchBomb:
					var distribution game.DispatchBombData
					distribution, relayErr = game.ParseDispatchBombEvent(event)
					if relayErr == nil {
						_, relayErr = server.requireMatchParticipantActor(session, distribution.PlayerID, "dispatch-bomb")
					}
					var battle *match.CompetitiveBattle
					if relayErr == nil {
						battle, relayErr = server.requireCompetitiveBombDispatch(session, distribution.PlayerID)
					}
					if relayErr == nil && !battle.IsArbitrator(distribution.PlayerID) {
						relayErr = fmt.Errorf("dispatch-bomb player %d is not the match arbitrator", distribution.PlayerID)
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_dispatch_bomb_%d_items_%d", len(distribution.Bombs), len(distribution.Items))
					}
				case game.NotifyPlayerDieEvent:
					death, deathErr := game.ParsePlayerDeathEvent(event)
					if deathErr != nil {
						relayErr = deathErr
					} else if death.PlayerID != session.Profile.PlayerID {
						relayErr = server.requireMatchArbitrator(session, "player-death notify")
					}
					var category roomstate.Category
					if relayErr == nil {
						category, relayErr = server.activeSessionMatchCategory(session)
					}
					if relayErr == nil && category == roomstate.CategoryAdventure {
						var deathResult adventureDeathDispatchResult
						deathResult, relayErr = server.handleAdventureDeath(session, data)
						response, result = deathResult.response, deathResult.result
						followUp, followUpResult = deathResult.followUp, deathResult.followUpResult
						startFollowUp, startFollowUpResult = deathResult.startFollowUp, deathResult.startFollowUpResult
						startFollowUpDelay = deathResult.startFollowUpDelay
						completeAdventureAfterSend = deathResult.completeAfterSend
						adventureSettlement, adventureRoomSettlement = deathResult.settlement, deathResult.roomSettlement
						finalVictorySchedule = deathResult.finalVictorySchedule
					} else if relayErr == nil && category == roomstate.CategoryCompetitive {
						var deathResult competitiveDeathDispatchResult
						deathResult, relayErr = server.handleCompetitiveDeath(session, data)
						response, result = deathResult.response, deathResult.result
						followUp, followUpResult = deathResult.followUp, deathResult.followUpResult
						startFollowUp, startFollowUpResult = deathResult.startFollowUp, deathResult.startFollowUpResult
						startFollowUpDelay = deathResult.startFollowUpDelay
						completeCompetitiveAfterSend = deathResult.completeAfterSend
						competitiveRoomSettlement = deathResult.roomSettlement
						if relayErr == nil && server.competitiveAIHasVirtualParticipant(session.CurrentGameID, death.PlayerID) {
							gameID, targetID := session.CurrentGameID, death.PlayerID
							items := append([]game.GameItem(nil), death.Items...)
							authoritativeStateAfterResponse = appendPostResponse(authoritativeStateAfterResponse, func() {
								server.recordCompetitiveAINativeElimination(gameID, 0, targetID, items)
							})
						}
					}
				case game.NotifyPlayerRelive:
					var relive game.PlayerReliveEvent
					relive, relayErr = game.ParsePlayerReliveEvent(event)
					if relayErr == nil {
						relayErr = server.requireMatchArbitrator(session, "player-relive notify")
					}
					if relayErr == nil {
						var category roomstate.Category
						category, relayErr = server.activeSessionMatchCategory(session)
						if relayErr == nil {
							switch category {
							case roomstate.CategoryAdventure:
								var battle *match.AdventureBattle
								battle, relayErr = server.adventureBattle(session.CurrentGameID)
								if relayErr == nil {
									_, relayErr = battle.RecordPlayerRelive(relive.PlayerID)
								}
							case roomstate.CategoryCompetitive:
								var battle *match.CompetitiveBattle
								battle, relayErr = server.competitiveBattle(session.CurrentGameID)
								if relayErr == nil {
									relayErr = battle.RecordRespawn(relive.PlayerID)
								}
							default:
								relayErr = fmt.Errorf("player-relive is unsupported for room category %d", category)
							}
						}
					}
					if relayErr == nil {
						response, event, relayErr = game.BuildLocalGameEventRelay(data)
					}
					if relayErr == nil {
						result = fmt.Sprintf("qqt_game_event_ack_player_relive_%d", relive.PlayerID)
					}
				case game.NotifyGameOverEvent:
					var gameOverResult matchGameOverDispatchResult
					gameOverResult, relayErr = server.handleMatchGameOver(session, data, event)
					response, result = gameOverResult.response, gameOverResult.result
					followUp, followUpResult = gameOverResult.followUp, gameOverResult.followUpResult
					completeAdventureAfterSend = gameOverResult.completeAdventureAfterSend
					completeCompetitiveAfterSend = gameOverResult.completeCompetitiveAfterSend
					adventureSettlement = gameOverResult.adventureSettlement
					adventureRoomSettlement = gameOverResult.adventureRoomSettlement
					competitiveRoomSettlement = gameOverResult.competitiveRoomSettlement
					handledWithoutResponse = gameOverResult.handledWithoutResponse
				default:
					response, event, relayErr = game.BuildLocalGameEventRelay(data)
					if relayErr == nil {
						// REQUEST_PLAY is also the reliable audit mirror for native
						// scene traffic. An unclassified client event must never gain a
						// second peer-delivery path merely by falling through this switch.
						// Every genuine server-owned request-to-notification conversion is
						// handled explicitly above with its exact response schema.
						result = fmt.Sprintf("qqt_game_event_ack_native_0x%04X", event.Schema)
					}
				}
			}
			if relayErr == nil && nativePeerMulticastMirrorSchema(event.Schema) {
				// The raw Type-2 packet has already fanned this event out to every
				// current peer. Retain the reliable ACK and any state/settlement
				// work above, but never manufacture another scene notification.
				followUp = nil
				afterResponse = nil
			}
			if relayErr == nil {
				afterResponse = appendPostResponse(afterResponse, authoritativeStateAfterResponse)
			}
			if relayErr != nil {
				// A response may already have been encoded before the session-level
				// route checks run. Never leak that provisional success packet when
				// the requested continuation is invalid.
				response = nil
				followUp = nil
				startFollowUp = nil
				completeAdventureAfterSend = false
				completeCompetitiveAfterSend = false
				adventureSettlement = nil
				adventureRoomSettlement = nil
				competitiveRoomSettlement = nil
				afterResponse = nil
				authoritativeStateAfterResponse = nil
				server.log(logEvent{Level: "warn", Event: "game_event_rejected", ConnectionID: id, MessageID: fmt.Sprintf("0x%04X", inspection.Command), MessageLength: len(data), Result: "rejected", ErrorContext: relayErr.Error(), Network: "tcp", LocalAddress: local, RemoteAddress: remote})
			} else if result == "" {
				result = fmt.Sprintf("qqt_game_event_relay_0x%04X", event.Schema)
			}
		}
	}

	var afterFollowUp func()
	if len(followUp) != 0 {
		if inspection, err := game.InspectLocalPacket(followUp); err == nil && (inspection.Command == game.GameEventNotifyCommand || inspection.Command == game.GameBeginNotifyCommand) {
			notification := append([]byte(nil), followUp...)
			afterFollowUp = func() {
				server.broadcastMatchNotificationToPlayers(session.RoomID, session.UIN, followUpResult+"_peer", notification, followUpPeerPlayers)
			}
		}
	}
	if afterFollowUpAction != nil {
		broadcast := afterFollowUp
		action := afterFollowUpAction
		afterFollowUp = func() {
			if broadcast != nil {
				broadcast()
			}
			action()
		}
	}
	var afterStartFollowUp func()
	if len(startFollowUp) != 0 {
		if inspection, err := game.InspectLocalPacket(startFollowUp); err == nil && (inspection.Command == game.GameEventNotifyCommand || inspection.Command == game.GameBeginNotifyCommand) {
			notification := append([]byte(nil), startFollowUp...)
			afterStartFollowUp = func() {
				server.broadcastMatchNotificationToPlayers(session.RoomID, session.UIN, startFollowUpResult+"_peer", notification, startFollowUpPeerPlayers)
			}
		}
	}
	return gameEventMessageResult{
		handled: handled, response: response, result: result,
		followUp: followUp, followUpResult: followUpResult,
		startFollowUp: startFollowUp, startFollowUpResult: startFollowUpResult,
		startFollowUpDelay:           startFollowUpDelay,
		completeAdventureAfterSend:   completeAdventureAfterSend,
		completeCompetitiveAfterSend: completeCompetitiveAfterSend,
		adventureSettlement:          adventureSettlement, finalVictorySchedule: finalVictorySchedule,
		adventureRoomSettlement: adventureRoomSettlement, competitiveRoomSettlement: competitiveRoomSettlement,
		handledWithoutResponse: handledWithoutResponse,
		afterResponse:          afterResponse,
		afterFollowUp:          afterFollowUp, afterStartFollowUp: afterStartFollowUp,
	}
}
