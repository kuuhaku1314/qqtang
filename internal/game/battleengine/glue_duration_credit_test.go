package battleengine

import "testing"

func TestSlowGlueContactCarriesLightweightDuration(t *testing.T) {
	for _, authoritative := range []bool{false, true} {
		for _, before := range []uint32{0, 7500} {
			config := testConfig()
			config.Rules.NativeOutcomeAuthority = authoritative
			config.Rules.RecordItemEffects = false
			engine := mustEngine(t, config)
			target := &engine.actors[1]
			target.Source = ParticipantVirtualAI
			if before > 0 {
				target.MovementStatus = MovementStatusSlow
				target.MovementStatusExpiresAt = engine.elapsedMS + before
			}
			engine.fieldObjects = []FieldObject{{ID: 9, ActionID: 43, OwnerID: 1, Cell: target.Position.Cell()}}
			target.nativePreviousPosition = PositionAtCellCenter(Cell{Row: 0, Col: 0})
			var events []Event
			if authoritative {
				var err error
				events, err = engine.ApplyVerifiedFieldObjectContact(target.PlayerID, 43, target.Position)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				events = engine.resolveFieldObjectContacts()
			}
			if len(events) != 2 {
				t.Fatalf("contacts=%+v", events)
			}
			e := events[0]
			if e.Kind != EventFieldObjectTriggered || e.MovementRemainingBeforeMS != before || e.EffectExpiresAt-e.TimeMS != NativeMovementStatusMS || e.ItemChange != nil {
				t.Fatalf("lightweight duration contact=%+v", e)
			}
			if len(engine.fieldObjects) != 0 || len(engine.resolveFieldObjectContacts()) != 0 {
				t.Fatal("contact was not consumed exactly once")
			}
		}
	}
}
