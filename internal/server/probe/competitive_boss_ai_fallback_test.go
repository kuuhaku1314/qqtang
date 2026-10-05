package probe

import (
	"encoding/binary"
	"io"
	"path/filepath"
	"testing"

	"qqtang/internal/game/mapdata"
	"qqtang/internal/game/match"
	roomstate "qqtang/internal/game/room"
	"qqtang/internal/protocol/game"
)

func TestCompetitiveBossPriorityAndAIFallback(t *testing.T) {
	catalog, err := mapdata.LoadCatalog(filepath.Join("..", "..", "..", "runtime", "client-patched"))
	if err != nil {
		t.Skipf("verified runtime client is unavailable: %v", err)
	}
	for _, tc := range []struct {
		name          string
		mapID         uint32
		humans        int
		bossCard      bool
		collectedCard bool
		ownerSummon   bool
		wantBoss      bool
	}{
		{"solo summon without boss card falls back to AI", 212, 1, false, false, true, false},
		{"solo collected boss card falls back to AI", 212, 1, true, true, true, false},
		{"solo qualified boss wins over AI", 11, 1, true, false, false, true},
		{"solo missing summon falls back to AI", 212, 1, true, false, false, false},
		{"multiplayer missing peer summon falls back to AI", 212, 2, false, false, true, false},
		{"multiplayer qualified boss needs no solo card", 11, 2, false, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := &Server{logWriter: io.Discard}
			owner := &connectionSession{UIN: 1_000_001, Profile: game.DefaultPlayerProfile()}
			var peer *connectionSession
			if tc.humans == 2 {
				server, owner, peer = newRoomPeerUDPTestServer(t)
				peer.Profile.Inventory = nil
			} else if err := server.createSessionRoom(owner, 1, byte(roomstate.GameTypeCompetitiveNoItem)); err != nil {
				t.Fatal(err)
			}
			server.mapCatalog = catalog
			server.config.CompetitiveAI.Enabled = true
			server.competitiveAIPolicy = &competitiveAIActorPolicyFactoryProbe{}
			server.competitiveBattles = make(map[uint32]*match.CompetitiveBattle)
			owner.Profile.GameInfo.RoleID = 7
			owner.Profile.GameInfo.Point = ^uint32(0)
			owner.Profile.Inventory = []game.ItemInfo{game.NewPermanentItemInfo(game.CompetitiveAICardItemID, 1)}
			if tc.bossCard {
				card := game.NewPermanentItemInfo(game.SinglePlayerBossCardItemID, 1)
				if tc.collectedCard {
					card.ItemStatus = game.ItemStatusCollected
				}
				owner.Profile.Inventory = append(owner.Profile.Inventory, card)
			}
			if tc.ownerSummon {
				owner.Profile.Inventory = append(owner.Profile.Inventory, game.NewPermanentItemInfo(402, 1))
			}
			room, err := server.sessionRoom(owner)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = room.UpdateMatchSettings(owner.Profile.PlayerID, roomstate.MatchSettings{
				Map: roomstate.FixedMapSelection(tc.mapID), GameType: roomstate.GameTypeCompetitiveNoItem,
			}); err != nil {
				t.Fatal(err)
			}
			for _, member := range room.Snapshot().Members {
				member.TeamID = 1
				if _, err = room.UpdateMember(member); err != nil {
					t.Fatal(err)
				}
			}
			if peer != nil {
				if _, err = room.SetReady(peer.Profile.PlayerID, true); err != nil {
					t.Fatal(err)
				}
			}
			if err = server.validateSessionRoomStart(owner, room); err != nil {
				t.Fatal(err)
			}
			payload := make([]byte, 8)
			binary.BigEndian.PutUint32(payload[:4], owner.UIN)
			binary.BigEndian.PutUint32(payload[4:], uint32(owner.RoomID))
			request := testLocalRoutedPacketWithPayload(t, game.StartGameCommand, 3, 0xffff, 1, owner.UIN, payload)
			prepared, err := server.prepareCompetitiveRoomMatchStart(owner, request)
			if err != nil {
				t.Fatal(err)
			}
			packet, err := game.InspectLocalPacket(prepared.followUp)
			if err != nil {
				t.Fatal(err)
			}
			begin, err := game.ParseLengthPrefixedGameBeginDataNetwork(packet.Payload)
			if err != nil {
				t.Fatal(err)
			}
			battle, err := server.competitiveBattle(begin.GameID)
			if err != nil {
				t.Fatal(err)
			}
			if (battle.BossID() != "") != tc.wantBoss || (len(prepared.startFollowUp) != 0) != tc.wantBoss {
				t.Fatalf("boss=%q announcement=%t, want boss=%t", battle.BossID(), len(prepared.startFollowUp) != 0, tc.wantBoss)
			}
			if tc.wantBoss {
				if prepared.competitiveAI != nil || len(begin.Players) != tc.humans {
					t.Fatal("qualified Boss encounter also added AI players")
				}
			} else if prepared.competitiveAI == nil || len(begin.Players) <= tc.humans {
				t.Fatal("unqualified Boss encounter did not fall back to AI players")
			}
			if tc.ownerSummon && inventoryQuantity(owner.Profile.Inventory, 402) != 1 {
				t.Fatal("AI fallback consumed a Boss summon item")
			}
			if room.Snapshot().Phase != roomstate.PhaseInMatch {
				t.Fatal("room did not enter a match")
			}
		})
	}
}
