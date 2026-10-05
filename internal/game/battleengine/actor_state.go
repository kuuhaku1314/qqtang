package battleengine

import (
	"fmt"
	"sort"

	"qqtang/internal/clientdata/sceneelement"
)

func (engine *Engine) resolveActorContacts() []Event {
	events := make([]Event, 0)
	var pending map[nativeContactRequestKey]struct{}
	for actorIndex := range engine.actors {
		actor := &engine.actors[actorIndex]
		if actor.State == ActorEliminated || actor.nativeHitPending || actor.nativePreviousPosition.Cell() == actor.Position.Cell() {
			continue
		}
		for targetIndex := range engine.actors {
			target := &engine.actors[targetIndex]
			// Rule-1 contact producer 0060c148 tests both coordinate deltas
			// against 41.0 (strictly), independently of the actors' grid cells.
			dx, dy := actor.Position.X-target.Position.X, actor.Position.Y-target.Position.Y
			if target.State != ActorTrapped || target.nativeHitPending || actor.PlayerID == target.PlayerID || dx <= -41 || dx >= 41 || dy <= -41 || dy >= 41 {
				continue
			}
			if engine.rules.NativeOutcomeAuthority {
				// An original actor produces its own request. A virtual actor has no
				// client process, so Go publishes the request once per continuous
				// contact while leaving the outcome to the elected native arbitrator.
				if actor.Source == ParticipantHuman {
					continue
				}
				rescue := actor.TeamID == target.TeamID
				key := nativeContactRequestKey{sourceID: actor.PlayerID, targetID: target.PlayerID, rescue: rescue}
				if pending == nil {
					pending = make(map[nativeContactRequestKey]struct{})
				}
				pending[key] = struct{}{}
				if _, alreadyRequested := engine.nativeContactRequests[key]; alreadyRequested {
					continue
				}
				kind := EventActorEliminationRequested
				if rescue {
					kind = EventActorRescueRequested
				}
				events = append(events, Event{Kind: kind, TimeMS: engine.elapsedMS, PlayerID: actor.PlayerID, TargetID: target.PlayerID, Cell: actor.Position.Cell(), Position: actor.Position})
				continue
			}
			if actor.TeamID == target.TeamID {
				target.State = ActorActive
				target.TrappedBy = 0
				target.TrappedByBombID = 0
				target.TrapExpiresAt = 0
				events = append(events, Event{Kind: EventActorRescued, TimeMS: engine.elapsedMS, PlayerID: actor.PlayerID, TargetID: target.PlayerID, Cell: target.Position.Cell(), Position: target.Position})
			} else {
				events = append(events, engine.eliminateActor(targetIndex, actor.PlayerID, EliminationContact)...)
			}
		}
	}
	engine.nativeContactRequests = pending
	return events
}

func (engine *Engine) applyFlameHazards() []Event {
	// A mixed live match receives the elected native arbitrator's actor-hit
	// notification for original room participants. A virtual participant has
	// no owning Client.exe to produce its own PLAYER_BE_EXPLODED request, so
	// verified native explosions apply to that subset through
	// applyVirtualFlameHazards instead.
	if engine.rules.NativeOutcomeAuthority {
		return nil
	}
	return engine.applyFlameHazardsMatching(nil)
}

func (engine *Engine) applyFlameHazardsMatching(accept func(*Actor) bool) []Event {
	events := make([]Event, 0)
	for actorIndex := range engine.actors {
		actor := &engine.actors[actorIndex]
		if actor.State != ActorActive || engine.actorHarmProtected(actor) || (accept != nil && !accept(actor)) {
			continue
		}
		for _, flame := range engine.flames {
			// Native collision registration and FUN_005d3ec1 compare the
			// actor/bomb discrete Row/Col fields. The +/-19 movement footprint
			// is not the explosion hurt box; an actor whose visual body overlaps
			// the flame but whose centre remains in the adjacent cell is the
			// original game's 半身 safe position.
			if flame.ImpactAtMS != engine.elapsedMS || actor.Position.Cell() != flame.Cell {
				continue
			}
			engine.resetActorOnHit(actor)
			if actor.TransformationSceneID != 0 {
				events = append(events, engine.endTransformation(actor, TransformationEndHit, flame.OwnerID))
				break
			}
			actor.State = ActorTrapped
			actor.TrappedBy = flame.OwnerID
			actor.TrappedByBombID = flame.BombID
			if duration := engine.trapDuration(actor); duration != 0 {
				actor.TrapExpiresAt = saturatingAdd(engine.elapsedMS, duration)
			}
			events = append(events, Event{Kind: EventActorTrapped, TimeMS: engine.elapsedMS, PlayerID: flame.OwnerID, TargetID: actor.PlayerID, BombID: flame.BombID, Cell: actor.Position.Cell(), Position: actor.Position})
			break
		}
	}
	return events
}

func (engine *Engine) expireTransformations() []Event {
	events := make([]Event, 0)
	for index := range engine.actors {
		actor := &engine.actors[index]
		if actor.TransformationSceneID == 0 || actor.TransformationExpiresAt == 0 || actor.TransformationExpiresAt >= engine.elapsedMS {
			continue
		}
		if engine.rules.NativeOutcomeAuthority {
			continue
		}
		// The native duck avatar's recovery callback defers restoration while
		// the actor centre is over a static map object. Restoring there would
		// leave the normal actor embedded in a wall. Dynamic bombs are not part
		// of this grid test.
		if engine.actorCapabilities(actor, actor.Facing).RecoverOnlyOnOpenCell {
			tile, inside := engine.grid.Cell(actor.Position.Cell())
			if !inside || tile.Kind != CellOpen || tile.MapElementOccupied {
				continue
			}
		}
		events = append(events, engine.endTransformation(actor, TransformationEndExpired, 0))
	}
	return events
}

func (engine *Engine) endTransformation(actor *Actor, reason TransformationEndReason, sourceID uint16) Event {
	protectionExpiresAt := saturatingAdd(engine.elapsedMS, NativePostTransformationProtectionMS)
	event := Event{
		Kind: EventActorTransformationEnded, TimeMS: engine.elapsedMS,
		PlayerID: actor.PlayerID, TargetID: sourceID, Cell: actor.Position.Cell(), Position: actor.Position,
		SceneID: actor.TransformationSceneID, AvatarRoleID: actor.AvatarRoleID, TransformationEnd: reason,
		EffectExpiresAt: protectionExpiresAt,
		ItemChange:      engine.beginItemChange(actor),
	}
	if definition, ok := sceneelement.NativeTransformation(sceneelement.ID(actor.TransformationSceneID)); ok && definition.GrantedActionID != 0 {
		removeHeldAction(actor, definition.GrantedActionID)
	}
	actor.TransformationSceneID = 0
	actor.AvatarRoleID = 0
	actor.TransformationExpiresAt = 0
	actor.HarmProtectionExpiresAt = protectionExpiresAt
	engine.finishItemChange(event.ItemChange, actor)
	return event
}

// ApplyVerifiedTransformationRecovery mirrors the native arbitrator's
// NOTIFY_RECOVER_PLAYER_AVATAR decision. It is idempotent because the fixed
// engine clock may reach the same expiry one tick before the peer packet.
func (engine *Engine) ApplyVerifiedTransformationRecovery(playerID uint16, position Position) (Event, bool, error) {
	if engine == nil {
		return Event{}, false, fmt.Errorf("battle engine is nil")
	}
	index := engine.actorIndex(playerID)
	if index < 0 {
		return Event{}, false, fmt.Errorf("avatar-recovery player %d is not a participant", playerID)
	}
	actor := &engine.actors[index]
	if actor.State == ActorEliminated {
		return Event{}, false, fmt.Errorf("avatar-recovery player %d is eliminated", playerID)
	}
	if position != (Position{}) {
		if err := engine.ApplyVerifiedMovementCheckpoint(playerID, position); err != nil {
			return Event{}, false, err
		}
	}
	if actor.TransformationSceneID == 0 {
		return Event{}, false, nil
	}
	return engine.endTransformation(actor, TransformationEndExpired, 0), true, nil
}

// ApplyVerifiedActorHit mirrors the native 0x0FA5 outcome. Avatar hits
// consume only the temporary form; normal hits enter syrup. The operation is
// idempotent because the deterministic flame pass can reach the same result
// just before the authenticated peer notification arrives.
func (engine *Engine) ApplyVerifiedActorHit(playerID uint16, position Position, isAvatar bool) (Event, bool, error) {
	return engine.applyVerifiedActorHit(playerID, 0, position, isAvatar, false)
}

// BuildVirtualBlastHitRequest derives the 0x0FA5 request that an absent virtual
// Client.exe would have produced after seeing an arbitrator-confirmed blast.
// It deliberately does not change actor state; only the authority's later
// reliable echo of that exact 0x0FA5 may do so. position is an exact
// server-projected checkpoint, not a peer-supplied or widened hit box.
func (engine *Engine) BuildVirtualBlastHitRequest(playerID, sourceID uint16, position Position) (Event, bool, error) {
	if engine == nil {
		return Event{}, false, fmt.Errorf("battle engine is nil")
	}
	index := engine.actorIndex(playerID)
	if index < 0 {
		return Event{}, false, fmt.Errorf("verified virtual hit player %d is not a participant", playerID)
	}
	actor := &engine.actors[index]
	if actor.Source != ParticipantVirtualAI {
		return Event{}, false, fmt.Errorf("verified virtual hit player %d is not virtual", playerID)
	}
	if sourceID == 0 || engine.actorIndex(sourceID) < 0 {
		return Event{}, false, fmt.Errorf("verified virtual hit source %d is not a participant", sourceID)
	}
	if actor.State != ActorActive || engine.actorHarmProtected(actor) {
		return Event{}, false, nil
	}
	if position.X < 0 || position.Y < 0 || position.X >= int32(engine.grid.Width)*CellSizePixels || position.Y >= int32(engine.grid.Height)*CellSizePixels {
		return Event{}, false, fmt.Errorf("virtual hit request player %d is outside map at %d,%d", playerID, position.X, position.Y)
	}
	return Event{
		Kind: EventActorHitRequested, TimeMS: engine.elapsedMS,
		PlayerID: playerID, TargetID: sourceID,
		Cell: position.Cell(), Position: position,
		SceneID: actor.TransformationSceneID,
	}, true, nil
}

func (engine *Engine) applyVerifiedActorHit(playerID, sourceID uint16, position Position, isAvatar, reconcilePosition bool) (Event, bool, error) {
	if engine == nil {
		return Event{}, false, fmt.Errorf("battle engine is nil")
	}
	index := engine.actorIndex(playerID)
	if index < 0 {
		return Event{}, false, fmt.Errorf("verified hit player %d is not a participant", playerID)
	}
	actor := &engine.actors[index]
	if actor.State == ActorEliminated {
		return Event{}, false, nil
	}
	if (actor.Source == ParticipantHuman || reconcilePosition) && position != (Position{}) {
		if err := engine.ApplyVerifiedMovementCheckpoint(playerID, position); err != nil {
			return Event{}, false, err
		}
	}
	if isAvatar {
		if actor.TransformationSceneID == 0 {
			return Event{}, false, nil
		}
		engine.resetActorOnHit(actor)
		return engine.endTransformation(actor, TransformationEndHit, sourceID), true, nil
	}
	if actor.State == ActorTrapped {
		return Event{}, false, nil
	}
	engine.resetActorOnHit(actor)
	actor.State = ActorTrapped
	actor.TrappedBy = sourceID
	actor.TrappedByBombID = 0
	if duration := engine.trapDuration(actor); duration != 0 {
		actor.TrapExpiresAt = saturatingAdd(engine.elapsedMS, duration)
	}
	return Event{Kind: EventActorTrapped, TimeMS: engine.elapsedMS, PlayerID: sourceID, TargetID: playerID, Cell: actor.Position.Cell(), Position: actor.Position}, true, nil
}

// ApplyVerifiedRescue mirrors NOTIFY_PLAYER_BE_SAVED without repeating the
// peer visual. It accepts both human and virtual targets and is idempotent.
func (engine *Engine) ApplyVerifiedRescue(sourceID, targetID uint16, _ Position) (Event, bool, error) {
	if engine == nil {
		return Event{}, false, fmt.Errorf("battle engine is nil")
	}
	if engine.actorIndex(sourceID) < 0 {
		return Event{}, false, fmt.Errorf("verified rescue source %d is not a participant", sourceID)
	}
	index := engine.actorIndex(targetID)
	if index < 0 {
		return Event{}, false, fmt.Errorf("verified rescue target %d is not a participant", targetID)
	}
	target := &engine.actors[index]
	if target.State != ActorTrapped {
		return Event{}, false, nil
	}
	// FAB's coordinates belong to the rescuer. Native consumer 006063bd
	// restores the target through 005acc39 without changing its position.
	target.State = ActorActive
	target.TrappedBy = 0
	target.TrappedByBombID = 0
	target.TrapExpiresAt = 0
	return Event{Kind: EventActorRescued, TimeMS: engine.elapsedMS, PlayerID: sourceID, TargetID: targetID, Cell: target.Position.Cell(), Position: target.Position}, true, nil
}

func (engine *Engine) actorHarmProtected(actor *Actor) bool {
	return actor != nil && actor.HarmProtectionExpiresAt > engine.elapsedMS
}

func (engine *Engine) resetActorOnHit(actor *Actor) {
	stopNativeHumanMovement(actor)
	// Native 005acaab clears the movement modifier before both hit paths.
	// The normal branch and avatar callback 005f78bf both face down (3).
	engine.clearMovementStatus(actor)
	actor.Facing = DirectionDown
}

func (engine *Engine) trapDuration(actor *Actor) uint32 {
	if engine.rules.TrapDurationMS != 0 {
		return engine.rules.TrapDurationMS
	}
	if actor != nil && actor.Source == ParticipantVirtualAI {
		return engine.rules.VirtualTrapDurationMS
	}
	return 0
}

func (engine *Engine) expireTraps() []Event {
	if engine.rules.NativeOutcomeAuthority {
		// Keep TrapExpiresAt visible for the policy countdown, but let the
		// elected native client confirm death and its exact scatter. Committing
		// this local timer first would invent drops and make the later native
		// fact look like a duplicate death, leaving live item locations wrong.
		return nil
	}
	if engine.rules.TrapDurationMS == 0 && engine.rules.VirtualTrapDurationMS == 0 {
		return nil
	}
	// This is an explicit offline tie convention, not a recovered native
	// callback ordering. Resolve every death at the same deadline before
	// deciding the winner, so equal expirations cannot award a PlayerID win.
	// Different deadlines still settle chronologically, even within one tick.
	expiring := make([]int, 0, len(engine.actors))
	for index := range engine.actors {
		actor := &engine.actors[index]
		if actor.State == ActorTrapped && actor.TrapExpiresAt != 0 && actor.TrapExpiresAt <= engine.elapsedMS {
			expiring = append(expiring, index)
		}
	}
	sort.SliceStable(expiring, func(i, j int) bool {
		return engine.actors[expiring[i]].TrapExpiresAt < engine.actors[expiring[j]].TrapExpiresAt
	})
	events := make([]Event, 0)
	for start := 0; start < len(expiring); {
		deadline := engine.actors[expiring[start]].TrapExpiresAt
		end := start + 1
		for end < len(expiring) && engine.actors[expiring[end]].TrapExpiresAt == deadline {
			end++
		}
		for _, index := range expiring[start:end] {
			events = append(events, engine.eliminateActor(index, engine.actors[index].TrappedBy, EliminationTrapDeath)...)
		}
		if event, ended := engine.evaluateTerminal(); ended {
			events = append(events, event)
			break
		}
		start = end
	}
	return events
}

// ConfirmTrappedDeath applies the native client's Die animation callback to a
// currently trapped actor. In the live game, PLAYER_BE_EXPLODED enters the
// trapped state while the later PLAYER_DIE event confirms natural death; the
// client does not expose a trustworthy fixed timeout that the server can
// recreate. Offline simulations may instead configure TrapDurationMS, but
// both paths deliberately share eliminateActor and terminal evaluation.
func (engine *Engine) ConfirmTrappedDeath(playerID uint16) ([]Event, error) {
	if engine == nil {
		return nil, fmt.Errorf("battle engine is nil")
	}
	if engine.outcome.Ended {
		return nil, fmt.Errorf("battle has already ended")
	}
	index := engine.actorIndex(playerID)
	if index < 0 {
		return nil, fmt.Errorf("battle trapped-death player %d is not a participant", playerID)
	}
	actor := &engine.actors[index]
	if actor.State != ActorTrapped {
		return nil, fmt.Errorf("battle trapped-death player %d has state %d, want trapped", playerID, actor.State)
	}
	events := engine.eliminateActor(index, actor.TrappedBy, EliminationTrapDeath)
	if event, ended := engine.evaluateTerminal(); ended {
		events = append(events, event)
	}
	return events, nil
}

// ApplyVerifiedElimination is the live-native reconciliation
// boundary. The original client has already selected and serialized the exact
// temporary scene items and cells in its death message, so the shared engine
// consumes those authoritative drops instead of rolling a second scatter.
// Both active and trapped states are accepted here because a reliable native
// death can arrive before the server's fixed-step mirror reaches the matching
// flame tick. Offline simulation continues to require the normal trapped path.
func (engine *Engine) ApplyVerifiedElimination(playerID, killerID uint16, drops []Pickup) ([]Event, error) {
	if engine == nil {
		return nil, fmt.Errorf("battle engine is nil")
	}
	if engine.outcome.Ended {
		return nil, fmt.Errorf("battle has already ended")
	}
	index := engine.actorIndex(playerID)
	if index < 0 {
		return nil, fmt.Errorf("battle trapped-death player %d is not a participant", playerID)
	}
	if engine.actors[index].State == ActorEliminated {
		return nil, fmt.Errorf("battle verified-elimination player %d is already eliminated", playerID)
	}
	if killerID == 0 {
		killerID = engine.actors[index].TrappedBy
	}
	if err := engine.validateVerifiedDeathDrops(drops); err != nil {
		return nil, err
	}
	events := engine.eliminateActorWithDrops(index, killerID, drops, EliminationUnknown)
	if event, ended := engine.evaluateTerminal(); ended {
		events = append(events, event)
	}
	return events, nil
}

func (engine *Engine) eliminateActor(actorIndex int, killerID uint16, cause EliminationCause) []Event {
	return engine.eliminateActorWithDrops(actorIndex, killerID, nil, cause)
}

func (engine *Engine) eliminateActorWithDrops(actorIndex int, killerID uint16, verifiedDrops []Pickup, cause EliminationCause) []Event {
	actor := &engine.actors[actorIndex]
	trappingBombID := actor.TrappedByBombID
	trappingPlayerID := actor.TrappedBy
	if definition, ok := sceneelement.NativeTransformation(sceneelement.ID(actor.TransformationSceneID)); ok && definition.GrantedActionID != 0 {
		removeHeldAction(actor, definition.GrantedActionID)
	}
	drops := verifiedDrops
	if drops == nil {
		drops = engine.scatterNativeDeathDrops(actor)
	} else {
		drops = append([]Pickup(nil), drops...)
	}
	for index := range actor.HeldActions {
		actor.HeldActions[index] = HeldActionSlot{}
	}
	actor.nativeBasicPickupDelta = [3]int16{}
	events := engine.installDeathDrops(actor.PlayerID, drops)
	actor.State = ActorEliminated
	actor.TrappedBy = 0
	actor.TrappedByBombID = 0
	actor.TrapExpiresAt = 0
	actor.TransformationSceneID = 0
	actor.AvatarRoleID = 0
	actor.TransformationExpiresAt = 0
	actor.HarmProtectionExpiresAt = 0
	actor.MovementStatus = MovementStatusNone
	actor.MovementStatusExpiresAt = 0
	events = append(events, Event{Kind: EventActorEliminated, TimeMS: engine.elapsedMS, PlayerID: killerID, TargetID: actor.PlayerID, BombID: trappingBombID, TrappedByPlayerID: trappingPlayerID, EliminationCause: cause, Cell: actor.Position.Cell(), Position: actor.Position, TeamID: actor.TeamID})
	return events
}
