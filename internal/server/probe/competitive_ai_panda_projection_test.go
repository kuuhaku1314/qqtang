package probe

import (
	"net"
	"testing"
	"time"

	"qqtang/internal/game/battleengine"
	"qqtang/internal/game/match"
	"qqtang/internal/protocol/game"
)

func TestCompetitiveAIPandaKickRequestProjection(t *testing.T) {
	from := battleengine.Cell{Row: 6, Col: 6}
	for _, tc := range []struct {
		name      string
		to        battleengine.Cell
		direction byte
		want      bool
	}{
		{"eight_cells_right", battleengine.Cell{Row: 6, Col: 14}, 0, true},
		{"six_cells_up", battleengine.Cell{Row: 0, Col: 6}, 1, true},
		{"six_cells_left", battleengine.Cell{Row: 6, Col: 0}, 2, true},
		{"six_cells_down", battleengine.Cell{Row: 12, Col: 6}, 3, true},
		{"adjacent_still_works", battleengine.Cell{Row: 6, Col: 7}, 0, true},
		{"diagonal_rejected", battleengine.Cell{Row: 7, Col: 7}, 0, false},
		{"same_cell_rejected", from, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const gameID uint32 = 91
			const aiID uint16 = 20001
			const aiUIN uint32 = 1_020_001
			server, actor, peer := newRoomPeerUDPTestServer(t)
			actor.CurrentGameID, peer.CurrentGameID = gameID, gameID
			serverSocket := listenRoomPeerUDPTest(t)
			t.Cleanup(func() { _ = serverSocket.Close() })
			var arbSocket, peerSocket *net.UDPConn
			for _, session := range []*connectionSession{actor, peer} {
				socket := listenRoomPeerUDPTest(t)
				t.Cleanup(func() { _ = socket.Close() })
				if session == actor {
					arbSocket = socket
				} else {
					peerSocket = socket
				}
				presence := game.LegacyUDPControlPacket{
					Header:   game.LegacyUDPControlHeader{PlayerID: session.Profile.PlayerID, UIN: session.UIN, Type: game.LegacyUDPPresenceType},
					Presence: &game.LegacyUDPEndpoint{IPv4: [4]byte{127, 0, 0, 1}, Port: uint16(socket.LocalAddr().(*net.UDPAddr).Port)},
				}
				data, err := presence.Encode()
				if err != nil {
					t.Fatal(err)
				}
				server.handleLegacyUDPControl(serverSocket, "game-udp", "presence", serverSocket.LocalAddr().String(), socket.LocalAddr().(*net.UDPAddr), data)
				expectLegacyUDPPresenceObserved(t, socket, serverSocket, presence)
			}
			participants := []match.CompetitiveParticipant{
				{PlayerID: actor.Profile.PlayerID, RoleID: 1, TeamID: 1, Source: match.CompetitiveParticipantHuman},
				{PlayerID: peer.Profile.PlayerID, RoleID: 2, TeamID: 1, Source: match.CompetitiveParticipantHuman},
				{PlayerID: aiID, RoleID: 3, TeamID: 2, Source: match.CompetitiveParticipantVirtualAI},
			}
			battle, err := match.NewCompetitiveBattle(gameID, 2, actor.Profile.PlayerID, participants)
			if err != nil {
				t.Fatal(err)
			}
			server.competitiveBattles = map[uint32]*match.CompetitiveBattle{gameID: battle}
			placed := liveCompetitiveAIBomb{ownerID: actor.Profile.PlayerID, placedAt: 68124, position: from, power: 4}
			runtime := &liveCompetitiveAIRuntime{
				roomID: 7, gameID: gameID,
				roomProjections: []competitiveAIRoomProjection{{PlayerID: aiID, UIN: aiUIN}},
				messageSeq:      make(map[uint16]uint32), bombs: map[uint32]liveCompetitiveAIBomb{17: placed},
				sceneRequests: make(map[liveCompetitiveAISceneRequestKey]uint32),
			}
			event := battleengine.Event{Kind: battleengine.EventBombKickRequested, TimeMS: 70000, PlayerID: aiID, BombID: 17, FromCell: from, Cell: tc.to}
			if got := runtime.projectBombKicked(server, nil, event); got != tc.want {
				t.Fatalf("kick request projection=%t, want %t for %v -> %v", got, tc.want, from, tc.to)
			}
			if runtime.bombs[17] != placed {
				t.Fatal("request prematurely moved the bomb before arbitrator confirmation")
			}
			if !tc.want {
				if runtime.messageSeq[aiID] != 0 || len(runtime.sceneRequests) != 0 {
					t.Fatal("invalid displacement left a request")
				}
				return
			}
			if err = arbSocket.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			data := make([]byte, game.LegacyUDPMaxDatagramSize)
			n, _, err := arbSocket.ReadFromUDP(data)
			if err != nil {
				t.Fatalf("arbitrator did not receive kick request: %v", err)
			}
			packet, err := game.DecodeLegacyUDPControlPacket(data[:n])
			if err != nil || packet.Multicast == nil || packet.Header.PlayerID != aiID || packet.Header.UIN != aiUIN {
				t.Fatalf("kick packet=%+v err=%v", packet, err)
			}
			if len(packet.Multicast.Targets) != 1 || packet.Multicast.Targets[0].PlayerID != actor.Profile.PlayerID {
				t.Fatal("kick request was not arbitrator-only")
			}
			batch, err := game.DecodeQQTPPPGameplayBatch(packet.Multicast.Data)
			if err != nil || len(batch.Entries) != 1 || len(batch.Entries[0].Package.Messages) != 1 {
				t.Fatalf("kick batch=%+v err=%v", batch, err)
			}
			message := batch.Entries[0].Package.Messages[0]
			if message.DataID != uint32(game.RequestMoveBomb) || message.Time != event.TimeMS {
				t.Fatalf("kick message=%+v", message)
			}
			got, err := game.ParseMoveBombEvent(game.GameEvent{Schema: uint16(message.DataID), Body: message.Data})
			want := game.MoveBombEvent{PlayerID: aiID, ClientTime: uint16(event.TimeMS), FromRow: uint16(from.Row), FromCol: uint16(from.Col),
				ToRow: uint16(tc.to.Row), ToCol: uint16(tc.to.Col), Direction: tc.direction, BombPlayerID: placed.ownerID, BombTime: placed.placedAt, BombPower: placed.power}
			if err != nil || got != want {
				t.Fatalf("kick body=%+v err=%v want=%+v", got, err, want)
			}
			if runtime.projectBombKicked(server, nil, event) || runtime.messageSeq[aiID] != 1 {
				t.Fatal("duplicate pending kick was not suppressed")
			}
			if err = peerSocket.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			if _, _, err = peerSocket.ReadFromUDP(data); err == nil {
				t.Fatal("ordinary peer received an unconfirmed kick request")
			}
		})
	}
}
