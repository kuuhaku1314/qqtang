package match

import (
	"fmt"
	"testing"
)

func TestNativeDurabilityMixedReportOrderPreservesFinalLife(t *testing.T) {
	for order := 0; order < 16; order++ {
		t.Run(fmt.Sprintf("death_first_mask_%04b", order), func(t *testing.T) {
			battle, err := NewCompetitiveBattleWithRuleConfig(1, 1301, 1, []CompetitiveParticipant{{PlayerID: 1, RoleID: 7, TeamID: 1}, {PlayerID: 2, RoleID: 8, TeamID: 2}},
				CompetitiveRuleConfig{ConclusionPolicy: CompetitiveConclusionClientRule, PlayerLifecycle: CompetitivePlayerNativeDurability, NativeHitLimit: 4})
			if err != nil {
				t.Fatal(err)
			}
			for hit := 1; hit <= 4; hit++ {
				// FUN_00601fec copies exactly Time/PosX/PosY from 0x10F4 to
				// 0x0FA7. Local harm dispatch can upload 0x0FA7 before the
				// outer producer sends the harm mirror; TCP need not reorder.
				tick := uint32(hit * 1000)
				var resolution CompetitiveResolution
				var authoritative bool
				harm := func() {
					if _, _, e := battle.RecordNativeHarm(2, tick, 100, 160, false, 0); e != nil {
						t.Fatal(e)
					}
				}
				death := func() {
					var e error
					resolution, authoritative, e = battle.RecordNativeDurabilityDeath(2, tick, 100, 160)
					if e != nil {
						t.Fatal(e)
					}
				}
				if order&(1<<(hit-1)) != 0 {
					death()
					harm()
				} else {
					harm()
					death()
				}
				if hit < 4 && (resolution.NewlyConcluded || authoritative) {
					t.Fatalf("ended after %d physical hits while native life=%d; server remaining=%d", hit, 4-hit, battle.nativeDurability.remaining[2])
				}
				if hit == 4 && (!resolution.NewlyConcluded || !authoritative || resolution.WinnerTeamID != 1) {
					t.Fatalf("fourth physical hit did not finish: %+v authoritative=%t", resolution, authoritative)
				}
				if got := battle.nativeDurability.remaining[2]; got != byte(4-hit) {
					t.Fatalf("physical hit %d: remaining=%d, want %d", hit, got, 4-hit)
				}
				// Exact reliable/fast copies must not consume another layer.
				if _, fresh, e := battle.RecordNativeHarm(2, tick, 100, 160, false, 0); e != nil || fresh {
					t.Fatalf("duplicate harm fresh=%t err=%v", fresh, e)
				}
				if r, fresh, e := battle.RecordNativeDurabilityDeath(2, tick, 100, 160); e != nil || fresh || r.NewlyConcluded {
					t.Fatalf("duplicate death changed state: %+v %t %v", r, fresh, e)
				}
			}
		})
	}
}

func TestNativeDurabilityAvatarShellDoesNotAbsorbLaterLifeReport(t *testing.T) {
	for _, order := range []string{"harm_first", "death_first", "death_only"} {
		t.Run(order, func(t *testing.T) {
			battle, err := NewCompetitiveBattleWithRules(2, 1401, 1, []CompetitiveParticipant{
				{PlayerID: 1, RoleID: 7, TeamID: 1}, {PlayerID: 2, RoleID: 8, TeamID: 2},
			}, CompetitiveConclusionClientRule, CompetitivePlayerNativeDurability)
			if err != nil {
				t.Fatal(err)
			}
			// FUN_00601fec returns before creating 0x0FA7 when IsAvatar=1.
			// The next native NPC/body hit belongs to a new physical event.
			if remaining, fresh, err := battle.RecordNativeHarm(2, 100, 40, 80, true, 0); err != nil || !fresh || remaining != 4 {
				t.Fatalf("shell hit: remaining=%d fresh=%t err=%v", remaining, fresh, err)
			}
			for hit := 1; hit <= 4; hit++ {
				tick := uint32(hit * 1000)
				var resolution CompetitiveResolution
				harm := func() {
					if _, _, err := battle.RecordNativeHarm(2, tick, 40, 80, false, 0); err != nil {
						t.Fatal(err)
					}
				}
				death := func() {
					var err error
					resolution, _, err = battle.RecordNativeDurabilityDeath(2, tick, 40, 80)
					if err != nil {
						t.Fatal(err)
					}
				}
				switch order {
				case "harm_first":
					harm()
					death()
				case "death_first":
					death()
					harm()
				case "death_only":
					death()
				}
				if remaining := battle.nativeDurability.remaining[2]; remaining != byte(4-hit) || resolution.NewlyConcluded != (hit == 4) {
					t.Fatalf("body hit %d after shell: remaining=%d conclusion=%t", hit, remaining, resolution.NewlyConcluded)
				}
			}
		})
	}
}
