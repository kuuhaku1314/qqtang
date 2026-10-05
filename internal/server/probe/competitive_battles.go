package probe

import (
	"fmt"
	"time"

	"qqtang/internal/game/match"
	"qqtang/internal/protocol/game"
)

type competitiveTimeoutSchedule struct {
	RoomID       uint16
	GameID       uint32
	Duration     time.Duration
	ResultTimeMS uint32
}

func competitiveTimeoutTiming(roundDurationMS uint32) (time.Duration, uint32) {
	return time.Duration(roundDurationMS) * time.Millisecond, roundDurationMS
}

func (server *Server) replaceCompetitiveBattle(gameID, mapID uint32, arbitratorID uint16, participants []match.CompetitiveParticipant, config match.CompetitiveRuleConfig) error {
	if config.Objective == match.CompetitiveObjectiveSculpture && server.mapCatalog != nil {
		if selected, ok := server.mapCatalog.CompetitiveMap(mapID); ok {
			// Rule6 creates its statues from the fourth native coordinate
			// table; they are not static collision-layer map elements.
			for _, cell := range selected.ObjectiveCells {
				config.SculptureCells = append(config.SculptureCells, match.CompetitiveSculptureCell{Row: cell.Row, Col: cell.Col})
			}
		}
	}
	battle, err := match.NewCompetitiveBattleWithRuleConfig(gameID, mapID, arbitratorID, participants, config)
	if err != nil {
		return err
	}
	server.battleMu.Lock()
	if server.competitiveBattles == nil {
		server.competitiveBattles = make(map[uint32]*match.CompetitiveBattle)
	}
	server.competitiveBattles[gameID] = battle
	server.battleMu.Unlock()
	return nil
}

func (server *Server) competitiveBattle(gameID uint32) (*match.CompetitiveBattle, error) {
	server.battleMu.RLock()
	battle := server.competitiveBattles[gameID]
	server.battleMu.RUnlock()
	if battle == nil {
		return nil, fmt.Errorf("competitive game %d is not active", gameID)
	}
	return battle, nil
}

func (server *Server) removeCompetitiveBattle(gameID uint32) {
	server.stopCompetitiveAIRuntime(gameID)
	server.battleMu.Lock()
	delete(server.competitiveBattles, gameID)
	server.battleMu.Unlock()
}

func (server *Server) scheduleCompetitiveTimeout(schedule competitiveTimeoutSchedule) {
	if schedule.RoomID == 0 || schedule.GameID == 0 || schedule.Duration <= 0 {
		return
	}
	server.log(logEvent{Level: "info", Event: "competitive_timeout_scheduled", RoomID: fmt.Sprint(schedule.RoomID), Result: fmt.Sprintf("game_%d_duration_%s", schedule.GameID, schedule.Duration)})
	server.runAfter(schedule.Duration, func() { server.completeCompetitiveTimeout(schedule) })
}

func (server *Server) completeCompetitiveTimeout(schedule competitiveTimeoutSchedule) {
	err := runRoomActor(server, schedule.RoomID, "competitive-timeout", func() error {
		server.completeCompetitiveTimeoutOnRoomActor(schedule)
		return nil
	})
	if err != nil {
		server.log(logEvent{Level: "error", Event: "competitive_timeout_actor_failed", RoomID: fmt.Sprint(schedule.RoomID), Result: fmt.Sprintf("game_%d", schedule.GameID), ErrorContext: err.Error()})
	}
}

func (server *Server) completeCompetitiveTimeoutOnRoomActor(schedule competitiveTimeoutSchedule) {
	battle, err := server.competitiveBattle(schedule.GameID)
	if err != nil {
		return
	}
	resolution, err := battle.ConcludeTimeout()
	if err != nil {
		server.log(logEvent{Level: "info", Event: "competitive_timeout_ignored", RoomID: fmt.Sprint(schedule.RoomID), Result: fmt.Sprintf("game_%d", schedule.GameID), ErrorContext: err.Error()})
		return
	}
	if !resolution.NewlyConcluded {
		if len(server.liveRoomMatchSessions(schedule.RoomID, schedule.GameID)) == 0 {
			server.removeCompetitiveBattle(schedule.GameID)
		}
		return
	}
	members := server.liveRoomMatchSessions(schedule.RoomID, schedule.GameID)
	if len(members) == 0 {
		server.removeCompetitiveBattle(schedule.GameID)
		return
	}
	resultTimeMS := schedule.ResultTimeMS
	if resultTimeMS == 0 {
		resultTimeMS = uint32(schedule.Duration / time.Millisecond)
	}
	gameOver := competitiveGameOverData(resultTimeMS, resolution)
	timeoutResult := "qqt_competitive_game_over_timeout_draw"
	if resolution.AllPlayersLost {
		timeoutResult = "qqt_competitive_game_over_timeout_cooperative_loss"
	} else if resolution.WinnerTeamID != 0 {
		timeoutResult = fmt.Sprintf("qqt_competitive_game_over_timeout_winner_team_%d", resolution.WinnerTeamID)
	}
	for _, member := range members {
		template := member.packetTemplate()
		if len(template) == 0 {
			continue
		}
		packet, buildErr := game.BuildLocalGameOverPushForRecipient(template, schedule.RoomID, server.nextGameDataSequence(), gameOver)
		if buildErr != nil {
			server.log(logEvent{Level: "error", Event: "competitive_timeout_notify_failed", ConnectionID: member.connectionID, RoomID: fmt.Sprint(schedule.RoomID), Result: fmt.Sprintf("game_%d", schedule.GameID), ErrorContext: buildErr.Error()})
			continue
		}
		server.writeTCP(member.connection, member.connectionID, member.localAddress, member.remoteAddress, packet, timeoutResult)
	}
	actor := members[0]
	if err = server.completeCompetitiveRoomMatchOnRoomActor(actor, actor.connectionID, competitiveRoomSettlementCommit{GameOver: gameOver, Battle: battle}); err != nil {
		server.log(logEvent{Level: "error", Event: "competitive_timeout_completion_failed", ConnectionID: actor.connectionID, RoomID: fmt.Sprint(schedule.RoomID), Result: fmt.Sprintf("game_%d", schedule.GameID), ErrorContext: err.Error()})
	}
}
