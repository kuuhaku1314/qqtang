package match

import "testing"

func newSculptureDamageBattle(t *testing.T, cells ...CompetitiveSculptureCell) *CompetitiveBattle {
	t.Helper()
	b, err := NewCompetitiveBattleWithRuleConfig(1, 1218, 1, []CompetitiveParticipant{{PlayerID: 1, RoleID: 7, TeamID: 1}, {PlayerID: 2, RoleID: 8, TeamID: 2}, {PlayerID: 3, RoleID: 9, TeamID: 1}},
		CompetitiveRuleConfig{ConclusionPolicy: CompetitiveConclusionClientRule, PlayerLifecycle: CompetitivePlayerTimedRespawn, Objective: CompetitiveObjectiveSculpture, SculptureCells: cells})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func addSculpturePiece(t *testing.T, b *CompetitiveBattle, player uint16, tick uint32, row, col byte) CompetitiveResolution {
	t.Helper()
	r, _, err := b.RecordSculptureAction(false, player, tick, uint16(col)*40+20, uint16(row)*40+20, 0, 12)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSculptureDamageUsesNativeFiveCellFootprint(t *testing.T) {
	for _, tc := range []struct {
		name            string
		style, row, col byte
		want            int
	}{
		{"centre", 9, 3, 2, 1}, {"right", 9, 3, 1, 1}, {"up", 9, 4, 2, 1}, {"left", 9, 3, 3, 1}, {"down", 9, 2, 2, 1},
		{"ordinary", 1, 3, 2, 0}, {"diagonal", 9, 4, 3, 0}, {"long_flame", 9, 6, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newSculptureDamageBattle(t)
			addSculpturePiece(t, b, 1, 1, 3, 2)
			addSculpturePiece(t, b, 3, 2, 3, 2)
			damaged, err := b.RecordSculptureExplosions([]CompetitiveSculptureBomb{{PlayerID: 2, ClientTime: 3, BombID: tc.style, Row: tc.row, Col: tc.col}})
			if err != nil || damaged != tc.want || b.sculptures.progressByTeam[1] != byte(2-tc.want) {
				t.Fatalf("damage=%d err=%v progress=%v", damaged, err, b.sculptures.progressByTeam)
			}
			if b.objectives[1] != 1 || b.objectives[3] != 1 {
				t.Fatalf("cumulative player deposit statistic changed: %v", b.objectives)
			}
		})
	}
}

func TestSculptureDamageDeduplicatesEachBombAndClampsAtZero(t *testing.T) {
	b := newSculptureDamageBattle(t)
	addSculpturePiece(t, b, 1, 1, 3, 2)
	addSculpturePiece(t, b, 1, 2, 3, 2)
	bomb := CompetitiveSculptureBomb{PlayerID: 1, ClientTime: 10, BombID: 9, Row: 3, Col: 2}
	if n, err := b.RecordSculptureExplosions([]CompetitiveSculptureBomb{bomb, bomb}); err != nil || n != 1 {
		t.Fatalf("duplicate entries damage=%d err=%v", n, err)
	}
	bomb.ClientTime++
	if n, err := b.RecordSculptureExplosions([]CompetitiveSculptureBomb{bomb}); err != nil || n != 1 {
		t.Fatalf("second bomb damage=%d err=%v", n, err)
	}
	bomb.ClientTime++
	if n, err := b.RecordSculptureExplosions([]CompetitiveSculptureBomb{bomb}); err != nil || n != 0 {
		t.Fatalf("empty statue damage=%d err=%v", n, err)
	}
	addSculpturePiece(t, b, 1, 30, 3, 2)
	if n, err := b.RecordSculptureExplosions([]CompetitiveSculptureBomb{bomb}); err != nil || n != 0 || b.sculptures.progressByTeam[1] != 1 {
		t.Fatalf("late zero-effect retry damaged a new fragment: %d %v", n, err)
	}
	for i := uint32(31); i <= 33; i++ {
		addSculpturePiece(t, b, 1, i, 3, 2)
	}
	bomb.ClientTime = 40
	if n, err := b.RecordSculptureExplosions([]CompetitiveSculptureBomb{bomb}); err != nil || n != 0 || b.sculptures.progressByTeam[1] != 4 {
		t.Fatalf("completed statue changed: %d %v", n, err)
	}
}

func TestSculptureDamageEmptyFirstStatueStopsNativeSearch(t *testing.T) {
	b := newSculptureDamageBattle(t, CompetitiveSculptureCell{Row: 3, Col: 3}, CompetitiveSculptureCell{Row: 2, Col: 2})
	addSculpturePiece(t, b, 2, 1, 2, 2)
	n, err := b.RecordSculptureExplosions([]CompetitiveSculptureBomb{{PlayerID: 1, ClientTime: 2, BombID: 9, Row: 3, Col: 2}})
	if err != nil || n != 0 || b.sculptures.progressByTeam[2] != 1 {
		t.Fatalf("searched past first empty statue: %d %v", n, err)
	}
}

func TestSculptureDamageTimeoutUsesRemainingPieces(t *testing.T) {
	b := newSculptureDamageBattle(t)
	addSculpturePiece(t, b, 1, 1, 3, 2)
	addSculpturePiece(t, b, 2, 2, 3, 12)
	if _, err := b.RecordSculptureExplosions([]CompetitiveSculptureBomb{{PlayerID: 1, ClientTime: 3, BombID: 9, Row: 3, Col: 2}}); err != nil {
		t.Fatal(err)
	}
	r, err := b.ConcludeTimeout()
	if err != nil || r.WinnerTeamID != 2 {
		t.Fatalf("destroyed piece still counted at timeout: %+v %v", r, err)
	}
}
