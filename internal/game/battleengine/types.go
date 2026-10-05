// Package battleengine implements a deterministic, restricted simulation of
// QQTang's native rule-1 grid combat. It intentionally excludes Boss AI and
// every objective-specific competitive rule (kick-bomb, bun, treasure,
// sculpture, machine, box and tank).
package battleengine

import (
	"fmt"

	"qqtang/internal/game/rolecatalog"
)

const (
	MaxParticipants                 = 8
	ObservationSchemaVersion uint16 = 7
	ActionSpaceVersion       uint16 = 2
	MaxNativeSpeedRate              = 10
	CellSizePixels                  = 40
	ProtocolGameTimeMS       uint32 = 240_000
	StandardRoundTimeMS      uint32 = 240_000
	NativeBombFuseMS         uint32 = 3_000
	// NativeBombFlightMS is the duration passed to the native bomb move
	// animation. While state 3 is active the fuse continues to count down, but
	// FUN_005df7d0 only permits detonation after the object returns to state 1.
	NativeBombFlightMS    uint32 = 400
	NativeFlameDurationMS uint32 = 500
	// NativeRoundStartClockMS is the first controllable game clock after the
	// client's Ready/Go scene countdown. The deterministic engine starts here
	// directly; live projection still waits the same wall-clock interval.
	NativeRoundStartClockMS   uint32 = 3_000
	NativeHiddenPickupReachMS uint32 = 10_000
	NativeSceneFourEffectMS   uint32 = 30_000
	NativeOxygenValueInitial  uint32 = 6_000
	// NativeActorHalfSizePixels is the +/-0x13 leading-edge offset used by
	// Client.exe FUN_005b7d1b for all four movement directions.
	NativeActorHalfSizePixels uint16 = 19
	// NativeMaxMovementPixelsPerUpdate is the +/-40 clamp applied by
	// Client.exe FUN_005cfc10 after converting one elapsed-time update into a
	// directional displacement.
	NativeMaxMovementPixelsPerUpdate int32 = 40
	// NativeMovementProjectionStepMS is the fixed 0x19 millisecond step used by
	// Client.exe FUN_005cf9b8 while it walks CurrentPos toward the collision
	// endpoint advertised in one PLAYER_MOVESEQ.
	NativeMovementProjectionStepMS uint32 = 25
	// NativeCornerSlideTolerancePixels is the distinct value returned through
	// the rule virtual at +0x2c and passed to FUN_005cfc10. It controls native
	// corner correction; it is not the actor collision radius.
	NativeCornerSlideTolerancePixels uint16 = 6
	// NativeDynamicEntryBoundaryPixels is FUN_005b7f5c's +/-3-pixel test at a
	// dynamic bubble cell boundary. An actor already farther inside that cell
	// may leave it, while an actor outside cannot enter it normally.
	NativeDynamicEntryBoundaryPixels int32  = 3
	NativePassChargeMinMS            uint32 = 500
	NativePassChargeMaxMS            uint32 = 600
	NativePassCollisionFreshMS       uint32 = 100
	NativeBattleActionSlots                 = 7
	NativeMovementStatusMS           uint32 = 10_000
	NativeOxygenValueIncrement       uint32 = 1_000
	// NativeMapElementPushPulseMS is the collision-action cadence recovered
	// from repeated QBV 0xFB2 traces. Seven 2-point pulses cross the client's
	// ordinary-rule threshold of 12 after roughly 350 ms of held contact.
	NativeMapElementPushPulseMS uint32 = 50
	// NativeMapElementContactOffsetPixels is FUN_0060cac9's exact directional
	// probe. It is three pixels beyond the actor's +/-19 movement edge; unlike a
	// centre-cell neighbour lookup it cannot start charging from anywhere in the
	// current cell.
	NativeMapElementContactOffsetPixels int32 = 22
	NativeMapElementPushThreshold             = 12
	// NativePostTransformationProtectionMS is the exact flash2.eff lifetime
	// installed on the normal actor when Client.exe removes an avatar object.
	// The same active-effect predicate is the client's incoming-harm gate.
	NativePostTransformationProtectionMS uint32 = 3_000
)

var nativeSpeedScalarMilliByRate = [MaxNativeSpeedRate + 1]uint16{
	0, 1000, 1800, 3500, 4000, 4500, 5100, 5800, 6700, 8000, 13000,
}

var nativeSpeedPixelsPerSecondByRate = [MaxNativeSpeedRate + 1]uint16{
	0, 40, 72, 140, 160, 180, 204, 232, 268, 320, 520,
}

// NativeSpeedScalarMilli returns Client+0x1C450F's exact lookup value as
// fixed-point thousandths.
func NativeSpeedScalarMilli(rate byte) (uint16, bool) {
	if rate > MaxNativeSpeedRate {
		return 0, false
	}
	return nativeSpeedScalarMilliByRate[rate], true
}

// NativeSpeedPixelsPerSecond returns the exact elapsed-time movement rate used
// by the rule-1 client. The client multiplies the lookup scalar by elapsed
// milliseconds and 0.04, which is equivalent to scalar*40 pixels per second.
func NativeSpeedPixelsPerSecond(rate byte) (uint16, bool) {
	if rate > MaxNativeSpeedRate {
		return 0, false
	}
	return nativeSpeedPixelsPerSecondByRate[rate], true
}

type CellKind byte

const (
	CellOpen CellKind = iota
	CellBreakable
	CellSolid
)

type Cell struct {
	Row int16
	Col int16
}

// Tile contains only native traversal semantics needed by the restricted
// simulation. Actor and flame traversal are deliberately independent because
// the client's GridAttr stores them as separate flags.
type Tile struct {
	Kind                    CellKind
	FlamePassable           bool
	NativeGridAttr          uint32
	NativeGridAttrSet       bool
	NativePlayerExitAttr    uint32
	NativePlayerExitAttrSet bool
	MapElementOccupied      bool
	Durability              byte
	MapElementID            uint32
	NormalPushable          bool
	PandaPushable           bool
	// PushCounter is the native CMapElem +0x2c counter. Each valid contact
	// pulse adds two; rule 1 confirms the move only after it exceeds 12.
	PushCounter   byte
	ElementWidth  byte
	ElementHeight byte
	ElementAnchor Cell
}

// PlayerPassable applies the native CMapElem+0x2c directional predicate.
// Kind remains the any-direction summary for legacy/synthetic grids.
func (tile Tile) PlayerPassable(direction Direction) bool {
	if !tile.NativeGridAttrSet {
		return tile.Kind == CellOpen
	}
	return tile.NativeGridAttr&nativeTraversalBit(direction) != 0
}

// PlayerExitPassable is the source-cell query in FUN_005cfc10: the
// direction is ORed with 0x80 before calling CMapElem+0x2c.
func (tile Tile) PlayerExitPassable(direction Direction) bool {
	if tile.NativePlayerExitAttrSet {
		return tile.NativePlayerExitAttr&nativeTraversalBit(oppositeDirection(direction)) != 0
	}
	return tile.PlayerPassable(oppositeDirection(direction))
}

// FlamePassableIn applies CMapElem+0x30. A ray queries its destination with
// its travel direction and its previous cell with the reverse direction.
func (tile Tile) FlamePassableIn(direction Direction) bool {
	if !tile.NativeGridAttrSet {
		return tile.FlamePassable
	}
	return tile.NativeGridAttr&(nativeTraversalBit(direction)<<8) != 0
}

func nativeTraversalBit(direction Direction) uint32 {
	switch direction {
	case DirectionRight:
		return 8
	case DirectionUp:
		return 4
	case DirectionLeft:
		return 2
	case DirectionDown:
		return 1
	case DirectionNone:
		return 15
	default:
		return 0
	}
}

func oppositeDirection(direction Direction) Direction {
	switch direction {
	case DirectionUp:
		return DirectionDown
	case DirectionRight:
		return DirectionLeft
	case DirectionDown:
		return DirectionUp
	case DirectionLeft:
		return DirectionRight
	default:
		return DirectionNone
	}
}

type Position struct {
	X int32
	Y int32
}

func PositionAtCellCenter(cell Cell) Position {
	return Position{
		X: int32(cell.Col)*CellSizePixels + CellSizePixels/2,
		Y: int32(cell.Row)*CellSizePixels + CellSizePixels/2,
	}
}

func (position Position) Cell() Cell {
	return Cell{Row: int16(position.Y / CellSizePixels), Col: int16(position.X / CellSizePixels)}
}

type Grid struct {
	Width  uint16
	Height uint16
	Cells  []Tile
}

func (grid Grid) Clone() Grid {
	grid.Cells = append([]Tile(nil), grid.Cells...)
	return grid
}

func (grid Grid) Cell(cell Cell) (Tile, bool) {
	if cell.Row < 0 || cell.Col < 0 || int(cell.Row) >= int(grid.Height) || int(cell.Col) >= int(grid.Width) {
		return Tile{Kind: CellSolid}, false
	}
	index := int(cell.Row)*int(grid.Width) + int(cell.Col)
	if index < 0 || index >= len(grid.Cells) {
		return Tile{Kind: CellSolid}, false
	}
	return grid.Cells[index], true
}

func (grid Grid) validate() error {
	if grid.Width == 0 || grid.Height == 0 {
		return fmt.Errorf("battle grid dimensions must be non-zero")
	}
	if int(grid.Width)*int(grid.Height) != len(grid.Cells) {
		return fmt.Errorf("battle grid has %d cells, want %d for %dx%d", len(grid.Cells), int(grid.Width)*int(grid.Height), grid.Width, grid.Height)
	}
	for index, cell := range grid.Cells {
		if cell.Kind > CellSolid {
			return fmt.Errorf("battle grid cell %d has invalid kind %d", index, cell.Kind)
		}
		if cell.Kind == CellBreakable && cell.Durability == 0 {
			return fmt.Errorf("battle grid breakable cell %d has zero durability", index)
		}
		if cell.Durability != 0 && cell.Kind != CellBreakable && !(cell.Kind == CellOpen && cell.MapElementOccupied) {
			return fmt.Errorf("battle grid non-destructible cell %d has durability %d", index, cell.Durability)
		}
		if cell.NormalPushable || cell.PandaPushable {
			if cell.Kind == CellOpen || cell.MapElementID == 0 || cell.ElementWidth == 0 || cell.ElementHeight == 0 {
				return fmt.Errorf("battle grid cell %d has invalid pushable metadata %+v", index, cell)
			}
		} else if cell.MapElementID != 0 || cell.ElementWidth != 0 || cell.ElementHeight != 0 || cell.ElementAnchor != (Cell{}) || cell.PushCounter != 0 {
			if cell.Durability == 0 || !cell.MapElementOccupied || cell.MapElementID == 0 || cell.ElementWidth == 0 || cell.ElementHeight == 0 || cell.PushCounter != 0 {
				return fmt.Errorf("battle grid cell %d has orphan map-element metadata %+v", index, cell)
			}
		}
		if cell.PushCounter > NativeMapElementPushThreshold {
			return fmt.Errorf("battle grid cell %d has invalid native push counter %d", index, cell.PushCounter)
		}
	}
	return nil
}

type ParticipantSource byte

const (
	ParticipantHuman ParticipantSource = iota
	ParticipantVirtualAI
)

type Participant struct {
	PlayerID             uint16
	RoleID               uint16
	TeamID               byte
	Source               ParticipantSource
	Spawn                Cell
	SpeedPixelsPerSecond uint16
	// SpeedRate is the client's discrete movement attribute. A zero value
	// leaves movement on the caller-supplied SpeedPixelsPerSecond path. When it
	// is non-zero, Rules.SpeedPixelsPerSecondByRate is authoritative so a
	// SceneID 3/8 pickup can change movement without inventing a conversion.
	SpeedRate       byte
	MaxSpeedRate    byte
	BombCapacity    byte
	MaxBombCapacity byte
	BombPower       byte
	MaxBombPower    byte
}

// ParticipantFromNativeRole creates the exact initial combat attributes used
// by Client.exe for one complete player model. Spawn placement remains the map
// adapter's responsibility, so the returned participant intentionally has a
// zero Spawn until PlaceNativeSpawns runs.
func ParticipantFromNativeRole(playerID, roleID uint16, teamID byte, source ParticipantSource) (Participant, error) {
	profile, ok := rolecatalog.PlayableCombatProfile(roleID)
	if !ok {
		return Participant{}, fmt.Errorf("RoleID %d has no playable native combat profile", roleID)
	}
	return Participant{
		PlayerID: playerID, RoleID: roleID, TeamID: teamID, Source: source,
		BombCapacity: profile.BombCapacity, MaxBombCapacity: profile.MaxBombCapacity,
		BombPower: profile.BombPower, MaxBombPower: profile.MaxBombPower,
		SpeedRate: profile.SpeedRate, MaxSpeedRate: profile.MaxSpeedRate,
	}, nil
}

func (participant Participant) CanArbitrate() bool {
	return participant.Source == ParticipantHuman
}

type Rules struct {
	// TickMS is explicit because the original client advances gameplay from
	// elapsed time while rendering FPS may vary. The engine never reads a wall
	// clock, so callers and future vectorized training own the cadence.
	TickMS uint32
	// StartClockMS is the native game clock at the first controllable frame.
	// It is part of (and therefore consumes) the absolute RoundDurationMS scene
	// deadline, while still letting offline/RL callers skip empty Ready/Go steps.
	StartClockMS uint32
	// RoundDurationMS comes from the selected native rule and is an absolute
	// scene-clock deadline. Rule 1 is 240000 ms including Ready/Go.
	RoundDurationMS uint32
	// BombFuseMS and FlameDurationMS are overwritten with the native rule-1
	// constants by ConfigFromCompetitiveMap. Keeping them in Rules makes the
	// standalone engine useful for focused simulations and tests.
	BombFuseMS      uint32
	FlameDurationMS uint32
	// TrapDurationMS may be zero while the exact native automatic-death timeout
	// remains unproven. Rescue and opponent finish interactions still work.
	TrapDurationMS uint32
	// VirtualTrapDurationMS supplies a live-server timeout only for virtual
	// actors, which have no owning client capable of emitting the native Die
	// animation callback. Human actors continue to wait for verified native
	// death when TrapDurationMS is zero.
	VirtualTrapDurationMS uint32
	// NativeOutcomeAuthority is enabled only by the live mixed human/virtual
	// runtime. The elected original-client arbitrator owns every shared scene
	// outcome, including explosions whose root bomb belongs to a virtual actor.
	// The engine may author only the request half that an absent virtual
	// Client.exe would normally produce; ApplyVerified* commits the arbitrator's
	// notification. Offline replay and RL leave this false and simulate the
	// complete battle.
	NativeOutcomeAuthority bool
	// RecordItemEffects captures event-local before/after evidence only for
	// diagnostic matches. It does not alter physics, visibility or rewards.
	RecordItemEffects bool
	// ActorHalfSizePixels defines the axis-aligned collision footprint around
	// the actor centre. ConfigFromCompetitiveMap binds the native rule-1 value.
	ActorHalfSizePixels uint16
	// SpeedPixelsPerSecondByRate is an explicit live-server/training
	// projection for the native rate 1..10. ConfigFromCompetitiveMap binds the
	// client's exact elapsed-time conversion.
	SpeedPixelsPerSecondByRate [MaxNativeSpeedRate + 1]uint16
}

func (rules Rules) validate() error {
	if rules.TickMS == 0 || rules.RoundDurationMS == 0 || rules.BombFuseMS == 0 || rules.FlameDurationMS == 0 {
		return fmt.Errorf("tick, round, bomb fuse and flame durations must be non-zero")
	}
	if rules.ActorHalfSizePixels == 0 || rules.ActorHalfSizePixels >= CellSizePixels/2 {
		return fmt.Errorf("actor half-size %d must be within 1..%d pixels", rules.ActorHalfSizePixels, CellSizePixels/2-1)
	}
	return nil
}

type Config struct {
	Seed                  uint64
	Grid                  Grid
	Rules                 Rules
	Participants          []Participant
	Pickups               []Pickup
	ScheduledPickups      []NativeScheduledPickup
	PublicWallItemProfile PublicWallItemProfile
}

// PublicWallItemCategory is a stable effect-level grouping of the map's
// published wall-item table. It deliberately identifies useful affordances,
// not a MapID or any rolled hidden coordinate, so policies transfer when a map
// probability changes and cannot inspect the server's secret ItemSeed layout.
type PublicWallItemCategory byte

const (
	WallItemBombCapacity PublicWallItemCategory = iota
	WallItemBombPower
	WallItemMovement
	WallItemBattleAction
	WallItemTransformation
	WallItemDetector
	WallItemUtility
	WallItemReward
	PublicWallItemCategoryCount
)

type PublicWallItemCategoryProfile struct {
	PresenceProbability float32
	ExpectedDensity     float32
}

type PublicWallItemProfile struct {
	Categories [PublicWallItemCategoryCount]PublicWallItemCategoryProfile
}

type Direction byte

const (
	DirectionNone Direction = iota
	DirectionUp
	DirectionRight
	DirectionDown
	DirectionLeft
)

func (direction Direction) delta() (x, y int32, ok bool) {
	switch direction {
	case DirectionNone:
		return 0, 0, true
	case DirectionUp:
		return 0, -1, true
	case DirectionRight:
		return 1, 0, true
	case DirectionDown:
		return 0, 1, true
	case DirectionLeft:
		return -1, 0, true
	default:
		return 0, 0, false
	}
}

// Action is one fixed-tick player input. Movement is an independently held
// input in the native client, so placement and held-item key pulses do not
// introduce an artificial movement-stiffness tick.
type Action struct {
	PlayerID    uint16
	Move        Direction
	PlaceBomb   bool
	UseActionID uint8
}

type ActorState byte

const (
	ActorActive ActorState = iota
	ActorTrapped
	ActorEliminated
)

type MovementStatusKind byte

const (
	MovementStatusNone MovementStatusKind = iota
	MovementStatusSlow
	MovementStatusForcedSlide
	MovementStatusFast
)

// HeldActionSlot mirrors one of the seven native in-match action slots. Empty
// slots have ActionID and Count equal to zero. The fixed-width value enters
// Clone and Observation without maps, pointers or allocation-dependent order.
type HeldActionSlot struct {
	ActionID uint8
	Count    uint8
}

// PublicBehaviorKind identifies one visible action category accumulated over
// the current round. The memory is derived from authoritative public events,
// not private input intent, so policies stay on the original-client
// information boundary.
type PublicBehaviorKind byte

const (
	PublicBehaviorMove PublicBehaviorKind = iota
	PublicBehaviorWait
	PublicBehaviorBomb
	PublicBehaviorItem
	PublicBehaviorKindCount
)

// PublicBehaviorMemory supplies both short- and long-horizon opponent habit
// signals. Recent values are unsigned fixed-point EMAs in [0, 65535]; Totals
// and Samples retain whole-round event frequencies. Arrays make Engine.Clone
// exact and allocation-free.
type PublicBehaviorMemory struct {
	Recent  [PublicBehaviorKindCount]uint16
	Totals  [PublicBehaviorKindCount]uint32
	Samples uint32
}

func (memory PublicBehaviorMemory) RecentRate(kind PublicBehaviorKind) float32 {
	if kind >= PublicBehaviorKindCount {
		return 0
	}
	return float32(memory.Recent[kind]) / float32(^uint16(0))
}

func (memory PublicBehaviorMemory) MatchRate(kind PublicBehaviorKind) float32 {
	if kind >= PublicBehaviorKindCount || memory.Samples == 0 {
		return 0
	}
	return float32(memory.Totals[kind]) / float32(memory.Samples)
}

type Actor struct {
	Participant
	Position  Position
	State     ActorState
	Facing    Direction
	TrappedBy uint16
	// TrappedByBombID preserves the exact authoritative bubble that produced
	// the flame hit. Zero denotes a non-bomb/native-reconciled trap. Keeping
	// this beside TrappedBy lets offline training carry causal credit through
	// the several-second trapped state until the later elimination event.
	TrappedByBombID            uint32
	TrapExpiresAt              uint32
	HiddenPickupReachExpiresAt uint32
	SceneFourEffectExpiresAt   uint32
	OxygenValue                uint32
	TransformationSceneID      uint32
	AvatarRoleID               uint16
	TransformationExpiresAt    uint32
	HarmProtectionExpiresAt    uint32
	MatchSugar                 uint32
	MovementStatus             MovementStatusKind
	MovementStatusExpiresAt    uint32
	HeldActions                [NativeBattleActionSlots]HeldActionSlot
	PublicBehavior             PublicBehaviorMemory
	// nativeBasicPickupDelta mirrors the three per-match counters at actor
	// +0x124/+0x138/+0x14c. Rule 1 uses their positive, capped values to build
	// SceneID 1/2/3 death drops; they are not the actor's initial attributes.
	nativeBasicPickupDelta [3]int16
	// NativePass* mirrors the actor +0x350 state initialized by FUN_005cc7e4.
	// Continuous, aligned collision with one type-1 cell is charged by
	// FUN_005d335c. A successful local bubble placement then reaches
	// FUN_005d33ab through the actor vtable slot +0x1c and may activate the
	// one-cell traversal window. Static terrain never enters this pass branch;
	// apparent 穿墙/上墙 is a visual-map/collision-boundary composition.
	NativePassCollisionValid     bool
	NativePassCollisionCell      Cell
	NativePassCollisionStartedAt uint32
	NativePassCollisionLastAt    uint32
	NativePassActive             bool
	NativePassStartedAt          uint32
	NativePassDurationMS         uint32
	// Native 005ad322 retains the previous coordinate in actor +0x3a8/+0x3ac.
	// 005f43b1 dispatches pickup/contact handlers only across that cell boundary.
	nativePreviousPosition Position
	// A live FA5 has been produced, but its authoritative echo is pending.
	// Keep the actor's outcome uncommitted while excluding local physics.
	nativeHitPending  bool
	nativeHumanMotion *nativeHumanMotion
	moveRemainder     uint32
}

type Bomb struct {
	ID            uint32
	OwnerID       uint16
	Cell          Cell
	Power         byte
	ExplodeAtMS   uint32
	FlightUntilMS uint32
	// TriggeredByBombID is the stable identity of the explosion that shortened
	// this bomb's fuse. Zero means the bomb remained a natural fuse root. The
	// link is retained until EventBombExploded so offline training can recover
	// the real, variable-length chain instead of guessing from nearby cells.
	TriggeredByBombID uint32
	// SceneFourEffect is the exact boolean appended to the native 0x0FA3
	// placement event while SceneID 4's 30-second client effect is active.
	// Client.exe only consumes it through the bubble effect-0x40 rendering
	// path; it does not change collision, fuse, power or flame propagation.
	SceneFourEffect bool
}

// EffectiveExplodeAtMS returns the earliest engine clock at which the bomb is
// allowed to explode. A Panda throw preserves the original fuse and changes
// the registered map cell immediately, but native state 3 suppresses both
// normal and chain detonation until the 400 ms flight finishes.
func (bomb Bomb) EffectiveExplodeAtMS() uint32 {
	if bomb.FlightUntilMS > bomb.ExplodeAtMS {
		return bomb.FlightUntilMS
	}
	return bomb.ExplodeAtMS
}

type Flame struct {
	Cell    Cell
	OwnerID uint16
	BombID  uint32
	// ImpactAtMS is the instant at which this explosion arm intersects
	// actors. The native 0x0FA5 path is emitted once by the explosion state;
	// the remaining 500 ms is visual/flame-object lifetime, not a repeating
	// damage pulse. A later explosion may re-arm the same cell.
	ImpactAtMS  uint32
	ExpiresAtMS uint32
}

// FieldObject is a placed native battlefield action (41, 42 or 43). Like a
// newly placed bomb, players already overlapping its cell may leave before it
// becomes solidly triggerable for them.
type FieldObject struct {
	ID         uint32
	ActionID   uint8
	OwnerID    uint16
	Cell       Cell
	PassableBy []uint16
}

// ActionProjectile is the native action 44/46 one-second visual flight toward
// the first target object in the actor's facing ray. Both stable bomb identity
// and the original destination cell are retained: the client resolves the
// object vector at that cell after the flight and does not follow a bomb that
// was kicked elsewhere in the meantime.
type ActionProjectile struct {
	ID           uint32
	ActionID     uint8
	OwnerID      uint16
	TargetBombID uint32
	TargetCell   Cell
	ResolveAtMS  uint32
}

type PickupState byte

const (
	PickupHidden PickupState = iota + 1
	PickupAvailable
	PickupCollected
)

// Pickup is one scene item placed by the client's ItemSeed shuffle. Hidden
// pickups become available only after their containing breakable cell is
// destroyed. The restricted engine accepts only scene effects it can model
// from client code; unsupported special items fail configuration validation.
type Pickup struct {
	SceneID uint32
	Cell    Cell
	State   PickupState
}

type AttributeKind byte

const (
	AttributeNone AttributeKind = iota
	AttributeBombCapacity
	AttributeBombPower
	AttributeSpeedRate
)

type PickupEffectKind byte

const (
	PickupEffectNone PickupEffectKind = iota
	// PickupEffectHiddenReach is the SceneID 61 detector effect: while active,
	// Observation reveals still-hidden scene items in the surrounding 3x3
	// cells, but their containing walls still prevent collection.
	PickupEffectHiddenReach
	// PickupEffectSceneFour marks the native 30-second visual bubble flag.
	PickupEffectSceneFour
	// PickupEffectOxygen records the exact +1000 update to the actor field
	// initialized to 6000. Live 5.2-client comparison proved that its natural
	// trapped-death path remains about six seconds at both 6000 and 7000, so
	// this value is observable legacy state rather than a simulated timeout.
	PickupEffectOxygen
	PickupEffectTransformation
	PickupEffectMatchSugar
	PickupEffectBattleAction
	PickupEffectMovementStatus
)

type TransformationEndReason byte

const (
	TransformationEndNone TransformationEndReason = iota
	TransformationEndExpired
	TransformationEndHit
)

type EventKind byte

const (
	EventBombPlaced EventKind = iota + 1
	EventActorMoved
	EventBombExploded
	EventCellDamaged
	EventCellDestroyed
	EventActorTrapped
	EventActorRescued
	EventActorEliminated
	EventMatchEnded
	EventPickupRevealed
	EventPickupCollected
	// EventPickupDropped is one native temporary action item scattered by an
	// eliminated actor. The engine adds the same available Pickup to its world;
	// live projection groups all events from one elimination into the single
	// NOTIFY_PLAYER_DIE/BE_KILLED Items array used by the original client.
	EventPickupDropped
	EventActorTransformationEnded
	EventBattleActionGranted
	EventBattleActionUsed
	EventFieldObjectPlaced
	EventFieldObjectTriggered
	EventMovementStatusStarted
	EventMovementStatusEnded
	EventMapElementMoved
	EventBombKicked
	// EventNativePassStarted is observational only. Live projection may ignore
	// it; training uses it to measure repeated one-cell traversal tactics.
	EventNativePassStarted
	// Native contact requests exist only when a virtual actor stands in for an
	// absent original client. The engine detects the contact, but the elected
	// native arbitrator remains responsible for confirming the rescue or kill.
	EventActorRescueRequested
	EventActorEliminationRequested
	// EventPickupCollectRequested is emitted only by a virtual participant in
	// live native-authority mode. It does not consume the pickup or grant its
	// effect; the elected original-client arbitrator must confirm the request
	// with 0x0FAD before ApplyVerifiedPickupAt changes shared state.
	EventPickupCollectRequested
	// The following request events exist only in live native-authority mode.
	// PlayerID is the virtual actor whose missing Client.exe would have produced
	// the request. None of them commits the corresponding shared scene outcome.
	EventActorHitRequested
	EventBattleActionUseRequested
	EventMapElementMoveRequested
	EventBombKickRequested
	// Offline/training rule simulation owns these two events. A live mixed
	// match instead waits for the elected native arbitrator's 0x0FBF/0x0FAE.
	EventPickupDestroyed
	EventPickupDispatched
)

// EliminationCause separates finishing contact from an expiring trap. A native
// death reconciliation does not always identify its cause and remains unknown.
type EliminationCause uint8

const (
	EliminationUnknown EliminationCause = iota
	EliminationContact
	EliminationTrapDeath
)

type Event struct {
	// OpenedTraversal describes the pre-destruction terrain, not predicted
	// value. Burning already walkable grass is not a new walking entrance.
	OpenedTraversal bool
	Kind            EventKind
	TimeMS          uint32
	PlayerID        uint16
	TargetID        uint16
	BombID          uint32
	// Elimination metadata preserves the actual trapping source separately
	// from PlayerID (the final killer). A victim's own bubble is not attributed
	// to a nearby opponent or to the player who later finishes the victim.
	TrappedByPlayerID uint16
	EliminationCause  EliminationCause
	// TriggeredByBombID is populated only for EventBombExploded. It names the
	// immediately preceding bomb in the authoritative chain; following these
	// links reaches every actual chain ancestor without treating simultaneous
	// natural-fuse explosions as causal.
	TriggeredByBombID uint32
	Cell              Cell
	FromCell          Cell
	Position          Position
	TeamID            byte
	SceneID           uint32
	Attribute         AttributeKind
	Effect            PickupEffectKind
	ValueBefore       byte
	ValueAfter        byte
	AvatarRoleID      uint16
	RewardBefore      uint32
	RewardAfter       uint32
	EffectExpiresAt   uint32
	TransformationEnd TransformationEndReason
	ActionID          uint8
	ActionCount       uint8
	ItemChange        *ItemStateChange `json:",omitempty"`
	// Native 44/46 requests always carry a ray endpoint, including a miss.
	// BombID alone cannot represent an empty endpoint.
	ProjectileTargetCell Cell
	ProjectileDirection  Direction
	ObjectID             uint32
	MovementStatus       MovementStatusKind
	// Lightweight contact credit metadata, independent of full item diagnostics.
	MovementStatusBefore      MovementStatusKind `json:",omitempty"`
	MovementRemainingBeforeMS uint32             `json:",omitempty"`
	// Blast bounds are populated only for EventBombExploded. They mirror the
	// four inclusive row/column limits carried by native EXPLODED_BOMB_C and
	// let the live adapter serialize the exact engine result without
	// reconstructing propagation from a second copy of the map.
	BlastRowMin int16
	BlastRowMax int16
	BlastColMin int16
	BlastColMax int16
}

type Outcome struct {
	Ended        bool
	Draw         bool
	TimedOut     bool
	WinnerTeamID byte
	EndedAtMS    uint32
}
