package probe

import (
	"testing"

	"qqtang/internal/game/battleengine"
	"qqtang/internal/game/mapdata"
	"qqtang/internal/game/match"
	"qqtang/internal/protocol/game"
)

func TestCompetitiveAILiveTimeoutAcceptsRecordedNativeDropCells(t *testing.T) {
	for _, free := range []bool{false, true} {
		name := "team"
		if free {
			name = "free"
		}
		t.Run(name, func(t *testing.T) {
			entry := testCompetitiveAIMap(2)
			entry.Battlefield.Width, entry.Battlefield.Height = 15, 13
			entry.Battlefield.Cells = make([]mapdata.CompetitiveBattleCell, 15*13)
			for i := range entry.Battlefield.Cells {
				entry.Battlefield.Cells[i] = mapdata.CompetitiveBattleCell{Collision: mapdata.CompetitiveCellOpen, FlamePassable: true}
			}
			entry.SpawnGroupA = []mapdata.CompetitiveCell{{Row: 0, Col: 0}}
			entry.SpawnGroupB = []mapdata.CompetitiveCell{{Row: 12, Col: 14}}
			participants := []match.CompetitiveParticipant{
				{PlayerID: 1, RoleID: 9, TeamID: 1, Source: match.CompetitiveParticipantHuman},
				{PlayerID: 20002, RoleID: 14, TeamID: 6, Source: match.CompetitiveParticipantVirtualAI},
			}
			runtime, err := newLiveCompetitiveAIRuntime(7, entry,
				game.GameBeginData{GameID: 99, SpawnSeed: 11, ItemSeed: 22},
				participants, free, &competitiveAIActorPolicyFactoryProbe{}, 100)
			if err != nil {
				t.Fatal(err)
			}
			actor, _ := actorByID(runtime.runtime.EngineSnapshot().Actors(), 20002)
			for _, sceneID := range []uint32{1, 1, 1, 1, 1, 2, 2, 3} {
				if _, err := runtime.runtime.AcceptNativePickup(20002, sceneID, actor.Position); err != nil {
					t.Fatal(err)
				}
			}
			if _, changed, err := runtime.runtime.AcceptNativeActorHitFrom(20002, 1, actor.Position, false); err != nil || !changed {
				t.Fatalf("native hit: changed=%v err=%v", changed, err)
			}
			until := runtime.runtime.EngineSnapshot().ElapsedMS() + 7_000
			for step := 0; step < 350 && runtime.runtime.EngineSnapshot().ElapsedMS() < until; step++ {
				if _, err := runtime.runtime.StepWithTrace(nil); err != nil {
					t.Fatal(err)
				}
				if runtime.runtime.EngineSnapshot().Terminal().Ended {
					break
				}
			}
			// These are the client's eight real drops for player 20002 at 51812ms
			// in the 2026-09-30 Kungfu05 team capture. The old local timeout rolled
			// a different set (including bubble (7,5)) and then ignored this fact.
			drops := []battleengine.Pickup{
				{SceneID: 1, Cell: battleengine.Cell{Row: 0, Col: 14}, State: battleengine.PickupAvailable},
				{SceneID: 1, Cell: battleengine.Cell{Row: 1, Col: 14}, State: battleengine.PickupAvailable},
				{SceneID: 1, Cell: battleengine.Cell{Row: 5, Col: 9}, State: battleengine.PickupAvailable},
				{SceneID: 1, Cell: battleengine.Cell{Row: 7, Col: 0}, State: battleengine.PickupAvailable},
				{SceneID: 1, Cell: battleengine.Cell{Row: 7, Col: 9}, State: battleengine.PickupAvailable},
				{SceneID: 2, Cell: battleengine.Cell{Row: 8, Col: 5}, State: battleengine.PickupAvailable},
				{SceneID: 2, Cell: battleengine.Cell{Row: 9, Col: 1}, State: battleengine.PickupAvailable},
				{SceneID: 3, Cell: battleengine.Cell{Row: 10, Col: 4}, State: battleengine.PickupAvailable},
			}
			if err := runtime.acceptNativeEliminationLocked(20002, 0, drops); err != nil {
				t.Fatal(err)
			}
			actual := runtime.runtime.EngineSnapshot().Pickups()
			if len(actual) != len(drops) {
				t.Fatalf("native drop count = %d, want %d: %+v", len(actual), len(drops), actual)
			}
			for i := range drops {
				if actual[i] != drops[i] {
					t.Fatalf("native drop %d = %+v, want %+v", i, actual[i], drops[i])
				}
			}
			if _, err := runtime.runtime.AcceptNativePickup(1, drops[0].SceneID, battleengine.PositionAtCellCenter(drops[0].Cell)); err != nil {
				t.Fatal(err)
			}
			if err := runtime.acceptNativeEliminationLocked(20002, 0, drops); err != nil {
				t.Fatal(err)
			}
			for _, item := range runtime.runtime.EngineSnapshot().Pickups() {
				if item.Cell == drops[0].Cell && item.State == battleengine.PickupAvailable {
					t.Fatal("duplicate death confirmation respawned a collected item")
				}
			}
		})
	}
}
