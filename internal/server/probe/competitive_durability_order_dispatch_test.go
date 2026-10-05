package probe

import (
	"encoding/binary"
	"testing"

	"qqtang/internal/game/mapdata"
	"qqtang/internal/game/match"
	"qqtang/internal/protocol/game"
)

func TestMechanicalAndBoxReportOrderPreservesLastLife(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mapID uint32
		rule  mapdata.CompetitiveRuleKind
	}{
		{"machine", 1301, mapdata.CompetitiveRuleMachine},
		{"box", 1401, mapdata.CompetitiveRuleBox},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, owner, peer, _ := newSculptureDamageTestServer(t)
			// Both native map rules obtain their four-life contract from the same
			// production registry used by match start.
			selected, ok := server.mapCatalog.CompetitiveMap(tc.mapID)
			if !ok || selected.Rule != tc.rule {
				t.Fatalf("map %d does not use %s: %+v", tc.mapID, tc.rule, selected)
			}
			spec, ok := mapdata.LookupCompetitiveRule(selected.Rule)
			if !ok || spec.PlayerLifecycle != mapdata.CompetitiveLifecycleNativeDurability || spec.NativePlayerHitLimit != 4 {
				t.Fatalf("unexpected native durability contract: %+v", spec)
			}
			lifecycle, err := competitivePlayerLifecycle(spec.PlayerLifecycle)
			if err != nil {
				t.Fatal(err)
			}
			owner.CurrentMapID, peer.CurrentMapID = tc.mapID, tc.mapID
			if err := server.replaceCompetitiveBattle(1, tc.mapID, owner.Profile.PlayerID, []match.CompetitiveParticipant{
				{PlayerID: owner.Profile.PlayerID, RoleID: 7, TeamID: 1}, {PlayerID: peer.Profile.PlayerID, RoleID: 8, TeamID: 2},
			}, match.CompetitiveRuleConfig{ConclusionPolicy: match.CompetitiveConclusionClientRule, PlayerLifecycle: lifecycle, NativeHitLimit: spec.NativePlayerHitLimit}); err != nil {
				t.Fatal(err)
			}
			battle, err := server.competitiveBattle(1)
			if err != nil {
				t.Fatal(err)
			}
			// The full-side mechanical blast remains client-owned; merely seeing its
			// type14 explosion packet must not consume extra durability on the server.
			if tc.rule == mapdata.CompetitiveRuleMachine {
				explosion, err := (game.BombExplodeEvent{PlayerID: owner.Profile.PlayerID, Bombs: []game.ExplodedBomb{{PlayerID: owner.Profile.PlayerID, ClientTime: 1, BombID: 14, Row: 6, Column: 12}}}).MarshalNetworkBinary()
				if err != nil {
					t.Fatal(err)
				}
				ack := sendReliableBossRuleEvent(t, server, owner, 1, game.NotifyBombExplode, explosion)
				if len(ack.response) == 0 || len(ack.followUp) != 0 || ack.completeCompetitiveAfterSend {
					t.Fatalf("mechanical type14 relay changed: %s", ack.result)
				}
			}
			for hit := 1; hit <= 4; hit++ {
				harm := make([]byte, 13)
				binary.BigEndian.PutUint16(harm, peer.Profile.PlayerID)
				binary.BigEndian.PutUint32(harm[2:], uint32(hit*1000))
				binary.BigEndian.PutUint16(harm[6:], 100)
				binary.BigEndian.PutUint16(harm[8:], 160)
				death := append([]byte(nil), harm[:11]...)
				var r gameEventMessageResult
				doDeath := func() {
					r = sendReliableBossRuleEvent(t, server, owner, uint32(10+hit), game.NotifyPlayerDieEvent, death)
				}
				doHarm := func() {
					p, settled, e := normalizeCompetitiveDurabilityFastPackages([]game.GameplayDataPackage{{GameID: 1, PlayerID: owner.Profile.PlayerID, MessageIndexes: []uint32{uint32(hit)}, Messages: []game.BattleMessageData{{DataID: game.PlayerBeHarmed, Data: harm}}}}, battle)
					if e != nil || settled != nil || len(p) != 1 {
						t.Fatalf("hit %d harm transport: %v %+v", hit, e, settled)
					}
				}
				if hit == 1 {
					doDeath()
					doHarm()
				} else {
					doHarm()
					doDeath()
				}
				if len(r.response) == 0 || r.completeCompetitiveAfterSend != (hit == 4) {
					t.Fatalf("physical hit %d: complete=%t result=%s", hit, r.completeCompetitiveAfterSend, r.result)
				}
			}
		})
	}
}
