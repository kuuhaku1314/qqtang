package probe

import (
	"fmt"
	"qqtang/internal/game/match"
	"qqtang/internal/protocol/game"
)

// Both transports account for the same bomb identities. Do not suppress the
// explosion relay: its wall/item arrays may differ between transport copies.
func recordCompetitiveSculptureExplosion(battle *match.CompetitiveBattle, sourceID uint16, event game.GameEvent) error {
	if !battle.IsArbitrator(sourceID) {
		return fmt.Errorf("sculpture explosion source %d is not arbitrator %d", sourceID, battle.ArbitratorPlayerID())
	}
	explosion, err := game.ParseBombExplodeEvent(event)
	if err != nil {
		return err
	}
	if explosion.PlayerID != sourceID {
		return fmt.Errorf("sculpture explosion reporter %d does not match source %d", explosion.PlayerID, sourceID)
	}
	bombs := make([]match.CompetitiveSculptureBomb, 0, len(explosion.Bombs))
	for _, bomb := range explosion.Bombs {
		bombs = append(bombs, match.CompetitiveSculptureBomb{PlayerID: bomb.PlayerID, ClientTime: bomb.ClientTime,
			BombID: bomb.BombID, Row: bomb.Row, Col: bomb.Column})
	}
	_, err = battle.RecordSculptureExplosions(bombs)
	return err
}
