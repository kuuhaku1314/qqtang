package probe

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"testing"

	"qqtang/internal/game/mapdata"
	"qqtang/internal/game/match"
	roomstate "qqtang/internal/game/room"
	"qqtang/internal/protocol/game"
)

func TestSculptureDamageCapturedRoundNeedsTwoReplacementPieces(t *testing.T) {
	server, owner, _, _ := newSculptureDamageTestServer(t)
	// 2026-10-05 20:22:32..20:24:28, map1218: two deposits,
	// own style9 hit, opposing style9 hit, then two more deposits.
	// The old mirror concluded at that fourth lifetime deposit, although the
	// native statue had only two installed fragments left.
	trace := []struct {
		schema uint16
		body   string
	}{
		{game.NotifyPlayerPutBun, "00010000a122006b009f000c"},
		{game.NotifyPlayerPutBun, "00010000c4980066009f000c"},
		{game.NotifyBombExplode, "00010000e7910100010000dbc9090302020401030000"},
		{game.NotifyBombExplode, "00010001477401000200013bac090302020401030000"},
		{game.NotifyPlayerPutBun, "0001000175a80064009f000c"},
		{game.NotifyPlayerPutBun, "00010002677a0053009e000c"},
	}
	for i, e := range trace {
		body, err := hex.DecodeString(e.body)
		if err != nil {
			t.Fatal(err)
		}
		r := sendReliableBossRuleEvent(t, server, owner, uint32(i+1), e.schema, body)
		if len(r.response) == 0 || r.completeCompetitiveAfterSend {
			t.Fatalf("captured event %d rejected or prematurely concluded: %s", i, r.result)
		}
	}
	for i := 0; i < 2; i++ {
		r := sendReliableBossRuleEvent(t, server, owner, uint32(20+i), game.NotifyPlayerPutBun, sculptureDamageDepositBody(t, owner.Profile.PlayerID, uint32(0x28000+i)))
		if len(r.response) == 0 || r.completeCompetitiveAfterSend != (i == 1) {
			t.Fatalf("replacement %d conclusion=%t result=%s", i+1, r.completeCompetitiveAfterSend, r.result)
		}
	}
}

func TestSculptureDamageMap1218StatueGeometry(t *testing.T) {
	server, _, _, _ := newSculptureDamageTestServer(t)
	selected, ok := server.mapCatalog.CompetitiveMap(1218)
	if !ok {
		t.Fatal("map1218 missing")
	}
	var positions []string
	for _, c := range selected.ObjectiveCells {
		positions = append(positions, fmt.Sprintf("%d,%d", c.Row, c.Col))
	}
	if fmt.Sprint(positions) != "[3,2 3,12]" {
		t.Fatalf("captured map statue cells=%v objectiveTable=%v", positions, selected.ObjectiveCells)
	}
}

func newSculptureDamageTestServer(t *testing.T) (*Server, *connectionSession, *connectionSession, *match.CompetitiveBattle) {
	t.Helper()
	catalog, err := mapdata.LoadCatalog(filepath.Join("..", "..", "..", "runtime", "client-patched"))
	if err != nil {
		// The public source checkout keeps the same native fixtures under
		// client/original; clean source-only clones may not have either copy.
		catalog, err = mapdata.LoadCatalog(filepath.Join("..", "..", "..", "client", "original"))
		if err != nil {
			t.Skipf("native client map fixtures unavailable: %v", err)
		}
	}
	server, owner, peer := newRoomPeerUDPTestServer(t)
	server.mapCatalog = catalog
	state, err := server.sessionRoom(owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = state.UpdateMatchSettings(owner.Profile.PlayerID, roomstate.MatchSettings{Map: roomstate.FixedMapSelection(1218), GameType: roomstate.GameTypeCompetitiveNoItem}); err != nil {
		t.Fatal(err)
	}
	if _, err = state.SetReady(peer.Profile.PlayerID, true); err != nil {
		t.Fatal(err)
	}
	if _, err = server.worldState().StartMatchWithID(owner.UIN, 1); err != nil {
		t.Fatal(err)
	}
	owner.CurrentGameID, owner.CurrentMapID = 1, 1218
	peer.CurrentGameID, peer.CurrentMapID = 1, 1218
	if err = server.replaceCompetitiveBattle(1, 1218, owner.Profile.PlayerID, []match.CompetitiveParticipant{
		{PlayerID: owner.Profile.PlayerID, RoleID: 7, TeamID: 1}, {PlayerID: peer.Profile.PlayerID, RoleID: 8, TeamID: 2},
	}, match.CompetitiveRuleConfig{ConclusionPolicy: match.CompetitiveConclusionClientRule,
		PlayerLifecycle: match.CompetitivePlayerTimedRespawn, Objective: match.CompetitiveObjectiveSculpture}); err != nil {
		t.Fatal(err)
	}
	battle, err := server.competitiveBattle(1)
	if err != nil {
		t.Fatal(err)
	}
	return server, owner, peer, battle
}

func sculptureDamageDepositBody(t *testing.T, player uint16, tick uint32) []byte {
	t.Helper()
	b, err := (game.BunActionEvent{PlayerID: player, ClientTime: tick, PosX: 100, PosY: 159, BunID: 0, BunTeamID: 12}).MarshalNetworkBinary()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sculptureDamageExplosionBody(t *testing.T, reporter, owner uint16, walls bool) []byte {
	t.Helper()
	// Exact player-1 self-hit geometry and identity from the 20:27:17 capture.
	e := game.BombExplodeEvent{PlayerID: reporter, ClientTime: 0x225ed, Bombs: []game.ExplodedBomb{
		{PlayerID: owner, ClientTime: 0x21a25, BombID: 9, Row: 4, Column: 2, RowMin: 3, RowMax: 5, ColumnMin: 1, ColumnMax: 3},
	}}
	if walls {
		e.MapElems = []game.ExplodedMapElement{{MapElementID: 12003, Row: 3, Column: 11}}
	}
	b, err := e.MarshalNetworkBinary()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSculptureDamageBothTransportsDelayVictoryUntilRebuilt(t *testing.T) {
	for _, self := range []bool{true, false} {
		for _, fastFirst := range []bool{true, false} {
			t.Run(fmt.Sprintf("self_%t_fast_first_%t", self, fastFirst), func(t *testing.T) {
				server, owner, peer, battle := newSculptureDamageTestServer(t)
				sequence := uint32(1)
				deposit := func(tick uint32) gameEventMessageResult {
					r := sendReliableBossRuleEvent(t, server, owner, sequence, game.NotifyPlayerPutBun, sculptureDamageDepositBody(t, owner.Profile.PlayerID, tick))
					sequence++
					if len(r.response) == 0 {
						t.Fatalf("deposit rejected: %+v", r)
					}
					return r
				}
				deposit(100)
				deposit(200)
				bombOwner := peer.Profile.PlayerID
				if self {
					bombOwner = owner.Profile.PlayerID
				}
				reliable := func() {
					r := sendReliableBossRuleEvent(t, server, owner, sequence, game.NotifyBombExplode, sculptureDamageExplosionBody(t, owner.Profile.PlayerID, bombOwner, false))
					sequence++
					if len(r.response) == 0 || r.afterResponse != nil || len(r.startFollowUp) != 0 {
						t.Fatalf("explosion mirror: %+v", r)
					}
				}
				fast := func() {
					packets, settlement, err := normalizeCompetitiveObjectiveFastPackages([]game.GameplayDataPackage{{GameID: 1, PlayerID: owner.Profile.PlayerID,
						MessageIndexes: []uint32{77}, Messages: []game.BattleMessageData{{DataID: game.NotifyBombExplode, Data: sculptureDamageExplosionBody(t, owner.Profile.PlayerID, bombOwner, true)}}}}, battle)
					if err != nil || settlement != nil || len(packets) != 1 || len(packets[0].Messages) != 1 {
						t.Fatalf("explosion fast relay: %v %+v %+v", err, packets, settlement)
					}
					if _, err = packets[0].MarshalNetworkBinary(); err != nil {
						t.Fatal(err)
					}
				}
				if fastFirst {
					fast()
					reliable()
				} else {
					reliable()
					fast()
				}
				for _, tick := range []uint32{0x23000, 0x24000} {
					if r := deposit(tick); r.completeCompetitiveAfterSend || len(r.startFollowUp) != 0 {
						t.Fatalf("cumulative fourth deposit incorrectly concluded after a fragment dropped: %s", r.startFollowUpResult)
					}
				}
				if r := deposit(0x25000); !r.completeCompetitiveAfterSend || r.competitiveRoomSettlement == nil {
					t.Fatalf("rebuilt four installed pieces did not win: %+v", r)
				}
			})
		}
	}
}

func TestSculptureDamageFastBatchPreservesDamageBeforeDepositOrder(t *testing.T) {
	_, owner, _, battle := newSculptureDamageTestServer(t)
	for n := uint32(1); n <= 3; n++ {
		if _, _, err := battle.RecordSculptureAction(false, owner.Profile.PlayerID, n, 100, 159, 0, 12); err != nil {
			t.Fatal(err)
		}
	}
	packets, settlement, err := normalizeCompetitiveObjectiveFastPackages([]game.GameplayDataPackage{{GameID: 1, PlayerID: owner.Profile.PlayerID, MessageIndexes: []uint32{41, 42}, Messages: []game.BattleMessageData{
		{DataID: game.NotifyBombExplode, Data: sculptureDamageExplosionBody(t, owner.Profile.PlayerID, owner.Profile.PlayerID, false)},
		{DataID: game.NotifyPlayerPutBun, Data: sculptureDamageDepositBody(t, owner.Profile.PlayerID, 0x23000)},
	}}}, battle)
	if err != nil || settlement != nil || len(packets) != 1 || len(packets[0].Messages) != 2 {
		t.Fatalf("damage/deposit ordering: packets=%+v settled=%+v err=%v", packets, settlement, err)
	}
	r, err := battle.ConcludeTimeout()
	if err != nil || r.Results[0].ObjectiveCount != 4 {
		t.Fatalf("cumulative deposit statistic changed: %+v %v", r, err)
	}
}

func TestSculptureDamageRejectsNonArbitratorReporter(t *testing.T) {
	server, owner, peer, battle := newSculptureDamageTestServer(t)
	if _, _, err := battle.RecordSculptureAction(false, owner.Profile.PlayerID, 1, 100, 159, 0, 12); err != nil {
		t.Fatal(err)
	}
	bad := sculptureDamageExplosionBody(t, peer.Profile.PlayerID, owner.Profile.PlayerID, false)
	r := sendReliableBossRuleEvent(t, server, peer, 1, game.NotifyBombExplode, bad)
	if len(r.response) != 0 {
		t.Fatal("non-arbitrator reliable damage was accepted")
	}
	if _, _, err := normalizeCompetitiveObjectiveFastPackages([]game.GameplayDataPackage{{PlayerID: peer.Profile.PlayerID, MessageIndexes: []uint32{1}, Messages: []game.BattleMessageData{{DataID: game.NotifyBombExplode, Data: bad}}}}, battle); err == nil {
		t.Fatal("non-arbitrator fast damage was accepted")
	}
	resolution, err := battle.ConcludeTimeout()
	if err != nil || resolution.Results[0].ObjectiveCount != 1 {
		t.Fatalf("unauthorized damage changed progress: %+v %v", resolution, err)
	}
}
