package battleengine

import "testing"

func TestNativeAuthorityWaitsForDeathFactAfterTrapDeadline(t *testing.T) {
	config := testConfig()
	config.Rules.NativeOutcomeAuthority = true
	config.Rules.TrapDurationMS = 0
	config.Rules.VirtualTrapDurationMS = 100
	config.Participants[1].Source = ParticipantVirtualAI
	engine := mustEngine(t, config)
	actor := &engine.actors[1]
	actor.nativeBasicPickupDelta = [3]int16{1, 1, 1}
	if _, changed, err := engine.ApplyVerifiedActorHit(actor.PlayerID, Position{}, false); err != nil || !changed {
		t.Fatalf("trap virtual actor: changed=%v err=%v", changed, err)
	}
	deadline := actor.TrapExpiresAt
	if deadline == 0 {
		t.Fatal("the visible trap countdown must be retained")
	}
	events, err := engine.Step(nil)
	if err != nil {
		t.Fatal(err)
	}
	if actor.State != ActorTrapped || actor.TrapExpiresAt != deadline || engine.Terminal().Ended {
		t.Fatalf("mirror committed unconfirmed death: actor=%+v terminal=%+v", actor, engine.Terminal())
	}
	if len(engine.Pickups()) != 0 || countEventKind(events, EventActorEliminated) != 0 {
		t.Fatalf("mirror created provisional death drops: pickups=%+v events=%+v", engine.Pickups(), events)
	}
	drops := []Pickup{{SceneID: SceneBombCapacitySmall, Cell: Cell{Row: 0, Col: 2}, State: PickupAvailable}}
	if _, err := engine.ApplyVerifiedElimination(actor.PlayerID, 0, drops); err != nil {
		t.Fatal(err)
	}
	if actor.State != ActorEliminated || len(engine.Pickups()) != 1 || engine.Pickups()[0] != drops[0] {
		t.Fatalf("native death and drop were not applied: actor=%+v pickups=%+v", actor, engine.Pickups())
	}
}
