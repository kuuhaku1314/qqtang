package probe

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"qqtang/internal/game/battleengine"
	"qqtang/internal/game/mapdata"
	"qqtang/internal/game/match"
	"qqtang/internal/protocol/game"
)

const (
	// GAME_BEGIN is followed by the original client's three-second scene
	// countdown. Native players do not produce their first movement sample until
	// roughly game time 3000. Advancing the virtual engine before that point lets
	// an AI move and place bubbles while every real client is still locked, so
	// those bubbles appear to explode immediately when control is released.
	competitiveAINativeStartDelay = time.Duration(battleengine.NativeRoundStartClockMS) * time.Millisecond
	// Native high-FPS captures advance collision physics in short render frames,
	// while straight-movement absolute network checkpoints are commonly 94-156
	// ms apart and become denser only around turns. Keep those two clocks
	// separate: 20 ms matches the deterministic training environment and
	// prevents 100 ms collision leaps from oscillating at narrow L-shaped spawn
	// corners; the live adapter emits only held-input boundaries, action
	// checkpoints and a native-like absolute-position heartbeat.
	competitiveAIWorldTickMS = uint32(20)
	// A native client never publishes forty world transitions in one render
	// burst. Keep recovery bounded so a delayed scheduler tick cannot turn into
	// 800 ms of back-to-back movement/action packets.
	competitiveAIMaxCatchUpSteps = 5
	competitiveAIPeerWait        = 10 * time.Second
	// Direction proposals are sampled every 100 ms in the normal live state.
	// Requiring 120 ms of stable scene time crosses two independent normal
	// decisions, while the separate imminent-danger path may still replan on
	// each 20 ms physics frame.
	competitiveAIDirectionConfirmMS = uint32(120)
	// Client.exe FUN_005f3695 uses an exact 0x96 (150 ms) accumulator while a
	// held segment keeps the same inner sequence. Render-frame boundaries make
	// captures land near that threshold (commonly 140-157 ms).
	competitiveAIMovementHeartbeatMS = uint32(150)
	// Original 5.2 traces use this normal bubble appearance for an actor whose
	// account equipment does not provide a decorated bubble.
	competitiveAIDefaultBombAppearance uint32 = 0xF20C7FFF
)

type liveCompetitiveAIBomb struct {
	ownerID    uint16
	placedAt   uint32
	position   battleengine.Cell
	appearance uint32
	power      byte
}

type liveCompetitiveAIHumanBombKey struct {
	playerID   uint16
	clientTime uint32
	row        byte
	column     byte
}

type liveCompetitiveAIHumanActionKey struct {
	playerID   uint16
	clientTime uint32
	actionID   uint32
}

type liveCompetitiveAIHumanMoveBombKey struct {
	playerID   uint16
	clientTime uint16
	ownerID    uint16
	bombTime   uint32
	fromRow    uint16
	fromCol    uint16
	toRow      uint16
	toCol      uint16
}

type liveCompetitiveAIInboundKey struct {
	playerID    uint16
	packetTime  uint32
	dataID      uint32
	sequence    uint32
	messageTime uint32
	gameTime    uint32
	body        string
}

type liveCompetitiveAIActorFrame struct {
	position                battleengine.Position
	state                   battleengine.ActorState
	facing                  battleengine.Direction
	speed                   byte
	transformationSceneID   uint32
	harmProtectionExpiresAt uint32
}

type liveCompetitiveAIPositionFrame struct {
	timeMS uint32
	actors map[uint16]liveCompetitiveAIActorFrame
}

type liveCompetitiveAIPeerKey struct {
	humanPlayerID   uint16
	virtualPlayerID uint16
}

type liveCompetitiveAIMovementProjection struct {
	initialized      bool
	moving           bool
	direction        battleengine.Direction
	speed            byte
	sequence         uint16
	lastSentAt       uint32
	lastSentPosition battleengine.Position
	path             battleengine.NativeMovementProjection
}

type liveCompetitiveAIHitRequestKey struct {
	playerID   uint16
	clientTime uint32
	posX       uint16
	posY       uint16
	isAvatar   bool
}

type liveCompetitiveAIHitRequest struct {
	sourceID   uint16
	state      liveCompetitiveAIHitRequestState
	payload    []byte // identical scene/message identity; outer UDP number is new
	lastSentAt time.Time
	attempts   byte
}

type liveCompetitiveAIHitRequestState byte

const (
	competitiveAIHitPending liveCompetitiveAIHitRequestState = iota + 1
	competitiveAIHitCommitted
	competitiveAIHitRecoveryRequired
)

type liveCompetitiveAIPickupRequestKey struct {
	playerID uint16
	sceneID  uint32
	row      int16
	col      int16
}

type liveCompetitiveAIPickupRequest struct {
	timeMS     uint32
	lastSentAt uint32
	position   battleengine.Position
	state      liveCompetitiveAIPickupRequestState
	attempts   byte
}

type liveCompetitiveAIPickupRequestState byte

const (
	competitiveAIPickupPending liveCompetitiveAIPickupRequestState = iota + 1
	competitiveAIPickupRejectedUntilExit
)

type liveCompetitiveAISceneRequestKey struct {
	kind     byte
	playerID uint16
	actionID uint8
	objectID uint32
	fromRow  int16
	fromCol  int16
	toRow    int16
	toCol    int16
}

const (
	competitiveAIRequestAction byte = iota + 1
	competitiveAIRequestMapElement
	competitiveAIRequestBombMove
)

// competitiveAIHeldDirectionPolicy converts tick-by-tick policy proposals
// into the held-key cadence used by the original client. A new direction must
// be proposed for two consecutive ticks unless the old direction is no longer
// legal or the actor is in imminent danger. Placement/item pulses are retained
// while the movement component is stabilized.
type competitiveAIHeldDirectionPolicy struct {
	base         battleengine.Policy
	held         battleengine.Direction
	initialized  bool
	pending      battleengine.Direction
	pendingSet   bool
	pendingSince uint32
}

// competitiveAIDecisionCadencePolicy separates policy inference from world
// integration. The cached direction remains held between decisions; key-like
// bomb/item pulses are emitted only on the decision that selected them.
type competitiveAIDecisionCadencePolicy struct {
	base           battleengine.Policy
	steps          uint32
	initialWait    uint32
	remainingSteps uint32
	cached         battleengine.Action
	initialized    bool
}

func (policy *competitiveAIDecisionCadencePolicy) ChooseAction(observation battleengine.Observation, legal []battleengine.Action) (battleengine.Action, error) {
	return policy.choose(nil, observation, legal)
}

func (policy *competitiveAIDecisionCadencePolicy) ChooseActionWithSnapshot(snapshot *battleengine.Engine, observation battleengine.Observation, legal []battleengine.Action) (battleengine.Action, error) {
	return policy.choose(snapshot, observation, legal)
}

func (policy *competitiveAIDecisionCadencePolicy) choose(snapshot *battleengine.Engine, observation battleengine.Observation, legal []battleengine.Action) (battleengine.Action, error) {
	if policy.steps == 0 {
		policy.steps = 1
	}
	if !policy.initialized && policy.initialWait > 0 {
		policy.initialWait--
		waiting := battleengine.Action{PlayerID: observation.PlayerID, Move: battleengine.DirectionNone}
		if competitiveAIActionAllowed(legal, waiting) {
			return waiting, nil
		}
		if len(legal) != 0 {
			return legal[0], nil
		}
		return waiting, nil
	}
	held := policy.cached
	held.PlaceBomb = false
	held.UseActionID = 0
	canReuse := policy.initialized && policy.remainingSteps > 0 &&
		competitiveAIActionAllowed(legal, held) &&
		(snapshot == nil || !snapshot.NeedsImmediatePolicyDecision(observation.PlayerID))
	if canReuse {
		policy.remainingSteps--
		return held, nil
	}
	var (
		chosen battleengine.Action
		err    error
	)
	if snapshotPolicy, ok := policy.base.(battleengine.SnapshotPolicy); ok {
		chosen, err = snapshotPolicy.ChooseActionWithSnapshot(snapshot, observation, legal)
	} else {
		chosen, err = policy.base.ChooseAction(observation, legal)
	}
	if err != nil {
		return battleengine.Action{}, err
	}
	policy.cached = chosen
	policy.initialized = true
	policy.remainingSteps = policy.steps - 1
	return chosen, nil
}

func (policy *competitiveAIHeldDirectionPolicy) ChooseAction(observation battleengine.Observation, legal []battleengine.Action) (battleengine.Action, error) {
	chosen, err := policy.base.ChooseAction(observation, legal)
	if err != nil {
		return battleengine.Action{}, err
	}
	return policy.stabilize(nil, observation, legal, chosen), nil
}

func (policy *competitiveAIHeldDirectionPolicy) ChooseActionWithSnapshot(snapshot *battleengine.Engine, observation battleengine.Observation, legal []battleengine.Action) (battleengine.Action, error) {
	var (
		chosen battleengine.Action
		err    error
	)
	if snapshotPolicy, ok := policy.base.(battleengine.SnapshotPolicy); ok {
		chosen, err = snapshotPolicy.ChooseActionWithSnapshot(snapshot, observation, legal)
	} else {
		chosen, err = policy.base.ChooseAction(observation, legal)
	}
	if err != nil {
		return battleengine.Action{}, err
	}
	return policy.stabilize(snapshot, observation, legal, chosen), nil
}

func (policy *competitiveAIHeldDirectionPolicy) stabilize(snapshot *battleengine.Engine, observation battleengine.Observation, legal []battleengine.Action, chosen battleengine.Action) battleengine.Action {
	if !policy.initialized {
		policy.initialized = true
		policy.held = chosen.Move
		return chosen
	}
	if chosen.Move == policy.held {
		policy.pendingSet = false
		policy.pendingSince = 0
		return chosen
	}
	// Native key-up and key-down are immediate. Direction confirmation exists
	// only to suppress direct moving-direction reversals at collision edges;
	// applying it to wait swallowed short deliberate stops, and applying it to
	// restart added synthetic input lag after a stop.
	if chosen.Move == battleengine.DirectionNone || policy.held == battleengine.DirectionNone {
		policy.acceptDirection(chosen.Move)
		return chosen
	}
	// Preserve the one-shot part of the selected action while continuing the
	// currently held key. If that exact combination is no longer legal, the
	// policy turn is necessary and must be accepted immediately. Imminent danger
	// alone is not such a boundary: the outer tactical layer has already chosen
	// the escape direction, and accepting each freshly replanned reverse on the
	// next 20 ms frame creates a left/right transport loop at bubble edges.
	heldAction := chosen
	heldAction.Move = policy.held
	if !competitiveAIActionAllowed(legal, heldAction) {
		policy.acceptDirection(chosen.Move)
		return chosen
	}
	if !policy.pendingSet || policy.pending != chosen.Move {
		policy.pending = chosen.Move
		policy.pendingSet = true
		policy.pendingSince = observation.ClockMS
		return heldAction
	}
	if observation.ClockMS-policy.pendingSince < competitiveAIDirectionConfirmMS {
		return heldAction
	}
	policy.acceptDirection(chosen.Move)
	return chosen
}

func (policy *competitiveAIHeldDirectionPolicy) acceptDirection(direction battleengine.Direction) {
	policy.held = direction
	policy.pendingSet = false
	policy.pendingSince = 0
}

func competitiveAIActionAllowed(legal []battleengine.Action, action battleengine.Action) bool {
	for _, candidate := range legal {
		if candidate == action {
			return true
		}
	}
	return false
}

func competitiveAILivePolicy(base battleengine.Policy, decisionSteps, initialWaitSteps uint32) battleengine.Policy {
	if decisionSteps == 0 {
		decisionSteps = 1
	}
	// The cadence mirrors the environment's decision interval and only holds
	// the last selected key between inferences. Do not wrap the actor in a
	// tactical controller or direction-confirmation filter: those rewrite legal
	// model decisions, make deployment differ from formal evaluation and prevent
	// learned high-risk techniques such as late escape and rapid reversals.
	return &competitiveAIDecisionCadencePolicy{
		base: base, steps: decisionSteps, initialWait: initialWaitSteps,
	}
}

func competitiveAIDecisionPhase(gameID, spawnSeed, itemSeed uint32, playerID uint16, decisionSteps uint32) uint32 {
	if decisionSteps <= 1 {
		return 0
	}
	// Live ONNX inference is deliberately greedy. Without an actor-local phase,
	// symmetric observations make every participant call the same argmax on the
	// same world frame and can lock a whole group into visibly cloned rhythms.
	// A match-seeded phase keeps inference deterministic/replayable while giving
	// each native identity its own 20 ms decision alignment.
	random := competitiveAIPRNG{state: uint64(gameID)<<32 ^ uint64(spawnSeed) ^ uint64(itemSeed)<<1 ^ uint64(playerID)*0xD6E8FEB86659FD93}
	return uint32(random.next() % uint64(decisionSteps))
}

// competitiveAISampleSeed seeds an actor's sampled action choices (when the
// deployment enables them) from the match and player, so each virtual player
// draws its own stream and a match replays exactly.
func competitiveAISampleSeed(gameID, spawnSeed, itemSeed uint32, playerID uint16) uint64 {
	random := competitiveAIPRNG{state: uint64(gameID)<<32 ^ uint64(spawnSeed)<<1 ^ uint64(itemSeed) ^ uint64(playerID)*0x9E3779B97F4A7C15}
	return random.next()
}

// liveCompetitiveAIRuntime is the transport/lifecycle adapter around the
// deterministic engine. The engine remains free of sockets and clocks; this
// wrapper owns exactly one fixed-rate goroutine for one active room match.
type liveCompetitiveAIRuntime struct {
	mu        sync.Mutex
	peerMu    sync.Mutex
	inboundMu sync.Mutex
	startOnce sync.Once

	roomID              uint16
	gameID              uint32
	mapID               uint32
	decisionTick        time.Duration
	worldTick           time.Duration
	nativeGameStartedAt time.Time
	runtime             *battleengine.Runtime
	humanIDs            map[uint16]struct{}
	virtualIDs          map[uint16]struct{}
	movement            map[uint16]liveCompetitiveAIMovementProjection
	mapElemSeq          uint16
	messageSeq          map[uint16]uint32
	bombs               map[uint32]liveCompetitiveAIBomb
	humanBombs          map[liveCompetitiveAIHumanBombKey]struct{}
	nativeExplosions    map[[3]uint32]struct{}
	nativeDispatches    map[[3]uint32]struct{}
	nativeItemExplodes  map[[3]uint32]struct{}
	humanPickups        map[[3]uint32]struct{}
	hitRequestMu        sync.Mutex
	hitRequests         map[liveCompetitiveAIHitRequestKey]liveCompetitiveAIHitRequest
	pickupRequestMu     sync.Mutex
	pickupRequests      map[liveCompetitiveAIPickupRequestKey]liveCompetitiveAIPickupRequest
	sceneRequestMu      sync.Mutex
	sceneRequests       map[liveCompetitiveAISceneRequestKey]uint32
	humanActions        map[liveCompetitiveAIHumanActionKey]struct{}
	humanKicks          map[liveCompetitiveAIHumanMoveBombKey]struct{}
	nativeInbound       map[liveCompetitiveAIInboundKey]struct{}
	nativeUDPSequence   map[uint16]uint32
	positionHistory     []liveCompetitiveAIPositionFrame
	authorityClockReady bool
	authorityClockMS    uint32
	authorityClockAt    time.Time
	nativePeers         map[liveCompetitiveAIPeerKey]bool
	nativePeerChanged   chan struct{}
	// roomProjections are the temporary NOTIFY_ENTER_ROOM_OLD identities that
	// must be removed after GAME_OVER. They never enter authoritative room state.
	roomProjections []competitiveAIRoomProjection

	stop     chan struct{}
	stopOnce sync.Once
}

func newLiveCompetitiveAIRuntime(
	roomID uint16,
	selectedMap mapdata.CompetitiveMap,
	gameData game.GameBeginData,
	participants []match.CompetitiveParticipant,
	freeRule bool,
	policy battleengine.Policy,
	tickMS uint32,
) (*liveCompetitiveAIRuntime, error) {
	if roomID == 0 || gameData.GameID == 0 || policy == nil {
		return nil, fmt.Errorf("competitive AI runtime identity or policy is incomplete")
	}
	if tickMS == 0 {
		tickMS = 100
	}
	worldTickMS := competitiveAIWorldTickMS
	if tickMS < worldTickMS {
		worldTickMS = tickMS
	}
	decisionSteps := (tickMS + worldTickMS - 1) / worldTickMS
	projectionTickMS := decisionSteps * worldTickMS
	nativeParticipants := make([]battleengine.NativeRuntimeParticipant, len(participants))
	policies := make(map[uint16]battleengine.Policy)
	humanIDs := make(map[uint16]struct{})
	virtualIDs := make(map[uint16]struct{})
	for index, participant := range participants {
		source := battleengine.ParticipantHuman
		if participant.Source == match.CompetitiveParticipantVirtualAI {
			source = battleengine.ParticipantVirtualAI
			// Model weights and the inference session are shared, but recurrent
			// memory belongs to one actor in one match. Stateless policies simply
			// use the supplied instance; learned recurrent policies fork an
			// actor-local state wrapper here.
			actorPolicy := policy
			if factory, ok := policy.(interface {
				NewSeededActorPolicy(seed uint64) battleengine.Policy
			}); ok {
				actorPolicy = factory.NewSeededActorPolicy(competitiveAISampleSeed(gameData.GameID, gameData.SpawnSeed, gameData.ItemSeed, participant.PlayerID))
			} else if factory, ok := policy.(interface {
				NewActorPolicy() battleengine.Policy
			}); ok {
				actorPolicy = factory.NewActorPolicy()
			}
			phase := competitiveAIDecisionPhase(gameData.GameID, gameData.SpawnSeed, gameData.ItemSeed, participant.PlayerID, decisionSteps)
			policies[participant.PlayerID] = competitiveAILivePolicy(actorPolicy, decisionSteps, phase)
			virtualIDs[participant.PlayerID] = struct{}{}
		} else {
			humanIDs[participant.PlayerID] = struct{}{}
		}
		nativeParticipants[index] = battleengine.NativeRuntimeParticipant{
			PlayerID: participant.PlayerID, RoleID: uint16(participant.RoleID), TeamID: participant.TeamID, Source: source,
		}
	}
	if len(humanIDs) == 0 || len(virtualIDs) == 0 {
		return nil, fmt.Errorf("competitive AI runtime requires both human and virtual participants")
	}
	spawnMode := battleengine.NativeSpawnModeForMap(selectedMap, freeRule)
	runtime, err := battleengine.NewRuntimeFromCompetitiveMap(selectedMap, battleengine.CompetitiveRuntimeOptions{
		SimulationSeed: uint64(gameData.SpawnSeed)<<32 | uint64(gameData.ItemSeed),
		SpawnSeed:      gameData.SpawnSeed, ItemSeed: gameData.ItemSeed,
		SpawnMode: spawnMode, TickMS: worldTickMS, TrapDurationMS: 0, VirtualTrapDurationMS: 6_000,
		NativeOutcomeAuthority: true,
		RecordedWallItems:      competitiveAIWallItems(gameData.NewItems),
		RecordedDelayedItems:   competitiveAIWallItems(gameData.Items),
		UseRecordedWallItems:   true,
		Participants:           nativeParticipants, Policies: policies,
	})
	if err != nil {
		return nil, err
	}
	live := &liveCompetitiveAIRuntime{
		roomID: roomID, gameID: gameData.GameID, mapID: selectedMap.ID,
		decisionTick: time.Duration(projectionTickMS) * time.Millisecond,
		worldTick:    time.Duration(worldTickMS) * time.Millisecond,
		runtime:      runtime,
		humanIDs:     humanIDs, virtualIDs: virtualIDs,
		movement:   make(map[uint16]liveCompetitiveAIMovementProjection),
		messageSeq: make(map[uint16]uint32),
		bombs:      make(map[uint32]liveCompetitiveAIBomb), humanBombs: make(map[liveCompetitiveAIHumanBombKey]struct{}),
		nativeExplosions:   make(map[[3]uint32]struct{}),
		nativeDispatches:   make(map[[3]uint32]struct{}),
		nativeItemExplodes: make(map[[3]uint32]struct{}),
		humanPickups:       make(map[[3]uint32]struct{}), pickupRequests: make(map[liveCompetitiveAIPickupRequestKey]liveCompetitiveAIPickupRequest),
		hitRequests:   make(map[liveCompetitiveAIHitRequestKey]liveCompetitiveAIHitRequest),
		sceneRequests: make(map[liveCompetitiveAISceneRequestKey]uint32),
		humanActions:  make(map[liveCompetitiveAIHumanActionKey]struct{}),
		humanKicks:    make(map[liveCompetitiveAIHumanMoveBombKey]struct{}),
		nativeInbound: make(map[liveCompetitiveAIInboundKey]struct{}),
		nativePeers:   make(map[liveCompetitiveAIPeerKey]bool), nativePeerChanged: make(chan struct{}, 1),
		stop: make(chan struct{}),
	}
	live.recordPositionFrameLocked(runtime.EngineSnapshot())
	return live, nil
}

func competitiveAIWallItems(items []game.GameItemType) []mapdata.CompetitiveWallItem {
	result := make([]mapdata.CompetitiveWallItem, 0, len(items))
	for _, item := range items {
		if item.ItemID == 0 || item.Quantity <= 0 {
			continue
		}
		result = append(result, mapdata.CompetitiveWallItem{SceneID: item.ItemID, Quantity: item.Quantity})
	}
	return result
}

func (runtime *liveCompetitiveAIRuntime) stopRuntime() {
	if runtime == nil {
		return
	}
	runtime.stopOnce.Do(func() { close(runtime.stop) })
}

func (runtime *liveCompetitiveAIRuntime) stopped() bool {
	if runtime == nil {
		return true
	}
	select {
	case <-runtime.stop:
		return true
	default:
		return false
	}
}

// filterNativeInbound collapses target fan-out and the later reliable mirror
// before either one can contend on the authoritative world lock. Client.exe
// sends the same semantic Type-2 package once per target; deduplicating only
// after runtime.mu allowed every copy to block the 20 ms scheduler and to
// clone the entire world even though only the first copy carried new state.
func (runtime *liveCompetitiveAIRuntime) filterNativeInbound(packages []game.GameplayDataPackage) []game.GameplayDataPackage {
	if runtime == nil || len(packages) == 0 {
		return nil
	}
	runtime.inboundMu.Lock()
	defer runtime.inboundMu.Unlock()
	if runtime.nativeInbound == nil {
		runtime.nativeInbound = make(map[liveCompetitiveAIInboundKey]struct{})
	}
	filtered := make([]game.GameplayDataPackage, 0, len(packages))
	for _, packet := range packages {
		unique := packet
		unique.MessageIndexes = nil
		unique.Messages = nil
		for _, message := range packet.Messages {
			key := liveCompetitiveAIInboundKey{
				playerID: packet.PlayerID, packetTime: packet.Time,
				dataID: message.DataID, sequence: message.Sequence,
				messageTime: message.Time, gameTime: message.GameTime, body: string(message.Data),
			}
			if _, duplicate := runtime.nativeInbound[key]; duplicate {
				continue
			}
			runtime.nativeInbound[key] = struct{}{}
			unique.Messages = append(unique.Messages, message)
		}
		if len(unique.Messages) != 0 {
			filtered = append(filtered, unique)
		}
	}
	return filtered
}

func (runtime *liveCompetitiveAIRuntime) nativeInboundCount() int {
	if runtime == nil {
		return 0
	}
	runtime.inboundMu.Lock()
	defer runtime.inboundMu.Unlock()
	return len(runtime.nativeInbound)
}

func (server *Server) stageCompetitiveAIRuntime(runtime *liveCompetitiveAIRuntime) {
	if runtime == nil {
		return
	}
	server.competitiveAIMu.Lock()
	prior := server.competitiveAIRuntime[runtime.gameID]
	server.competitiveAIRuntime[runtime.gameID] = runtime
	server.competitiveAIMu.Unlock()
	if prior != nil && prior != runtime {
		prior.stopRuntime()
	}
}

func (server *Server) installCompetitiveAIRuntime(runtime *liveCompetitiveAIRuntime) {
	if runtime == nil {
		return
	}
	server.stageCompetitiveAIRuntime(runtime)
	runtime.startOnce.Do(func() {
		profiles := ""
		if snapshot := runtime.runtime.EngineSnapshot(); snapshot != nil {
			for _, actor := range snapshot.Actors() {
				profiles += fmt.Sprintf("_player_%d_role_%d_team_%d_source_%d_bubbles_%d_%d_power_%d_%d_speed_%d_%d",
					actor.PlayerID, actor.RoleID, actor.TeamID, actor.Source,
					actor.BombCapacity, actor.MaxBombCapacity,
					actor.BombPower, actor.MaxBombPower,
					actor.SpeedRate, actor.MaxSpeedRate,
				)
			}
		}
		server.log(logEvent{Level: "info", Event: "competitive_ai_runtime_started", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_map_%d_world_tick_%s_decision_tick_%s%s", runtime.gameID, runtime.mapID, runtime.worldTick, runtime.decisionTick, profiles)})
		if !server.runBackground(func() { runtime.run(server) }) {
			runtime.stopRuntime()
		}
	})
}

func (runtime *liveCompetitiveAIRuntime) markNativePeerReady(humanPlayerID, virtualPlayerID uint16) {
	if runtime == nil {
		return
	}
	key := liveCompetitiveAIPeerKey{humanPlayerID: humanPlayerID, virtualPlayerID: virtualPlayerID}
	runtime.mu.Lock()
	_, human := runtime.humanIDs[humanPlayerID]
	_, virtual := runtime.virtualIDs[virtualPlayerID]
	runtime.mu.Unlock()
	if !human || !virtual {
		return
	}
	runtime.peerMu.Lock()
	changed := !runtime.nativePeers[key]
	if changed {
		runtime.nativePeers[key] = true
	}
	runtime.peerMu.Unlock()
	if changed {
		select {
		case runtime.nativePeerChanged <- struct{}{}:
		default:
		}
	}
}

func (server *Server) markCompetitiveAINativePeerReady(runtime *liveCompetitiveAIRuntime, humanPlayerID, virtualPlayerID uint16) {
	if runtime == nil {
		return
	}
	err := runRoomActor(server, runtime.roomID, "competitive-ai-peer-ready", func() error {
		runtime.markNativePeerReady(humanPlayerID, virtualPlayerID)
		return nil
	})
	if err != nil {
		server.log(logEvent{Level: "error", Event: "competitive_ai_peer_ready_actor_failed", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_human_%d_virtual_%d", runtime.gameID, humanPlayerID, virtualPlayerID), ErrorContext: err.Error()})
	}
}

func (runtime *liveCompetitiveAIRuntime) nativePeerProgress() (ready, expected int) {
	if runtime == nil {
		return 0, 0
	}
	runtime.mu.Lock()
	humans := make(map[uint16]struct{}, len(runtime.humanIDs))
	for playerID := range runtime.humanIDs {
		humans[playerID] = struct{}{}
	}
	virtuals := make(map[uint16]struct{}, len(runtime.virtualIDs))
	for playerID := range runtime.virtualIDs {
		virtuals[playerID] = struct{}{}
	}
	runtime.mu.Unlock()
	runtime.peerMu.Lock()
	defer runtime.peerMu.Unlock()
	expected = len(humans) * len(virtuals)
	for key, complete := range runtime.nativePeers {
		if !complete {
			continue
		}
		if _, human := humans[key.humanPlayerID]; !human {
			continue
		}
		if _, virtual := virtuals[key.virtualPlayerID]; virtual {
			ready++
		}
	}
	return ready, expected
}

func (runtime *liveCompetitiveAIRuntime) waitForNativePeers(server *Server, maximum time.Duration) bool {
	if runtime == nil || maximum <= 0 {
		return false
	}
	timer := time.NewTimer(maximum)
	defer timer.Stop()
	for {
		ready, expected := runtime.nativePeerProgress()
		if expected == 0 || ready >= expected {
			server.log(logEvent{Level: "info", Event: "competitive_ai_native_peers_ready", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_ready_%d_of_%d", runtime.gameID, ready, expected)})
			return true
		}
		select {
		case <-runtime.nativePeerChanged:
		case <-timer.C:
			server.log(logEvent{Level: "warn", Event: "competitive_ai_native_peer_timeout", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_ready_%d_of_%d", runtime.gameID, ready, expected)})
			return false
		case <-runtime.stop:
			return false
		case <-server.done:
			return false
		}
	}
}

func (server *Server) stopCompetitiveAIRuntime(gameID uint32) {
	if gameID == 0 {
		return
	}
	server.competitiveAIMu.Lock()
	runtime := server.competitiveAIRuntime[gameID]
	delete(server.competitiveAIRuntime, gameID)
	server.competitiveAIMu.Unlock()
	if runtime != nil {
		runtime.stopRuntime()
	}
}

func (server *Server) competitiveAIRuntimeForGame(gameID uint32) *liveCompetitiveAIRuntime {
	server.competitiveAIMu.Lock()
	defer server.competitiveAIMu.Unlock()
	return server.competitiveAIRuntime[gameID]
}

func (server *Server) competitiveAIHasVirtualParticipant(gameID uint32, playerID uint16) bool {
	runtime := server.competitiveAIRuntimeForGame(gameID)
	if runtime == nil || playerID == 0 {
		return false
	}
	// virtualIDs is fixed when the runtime is constructed and is never mutated,
	// so this membership test does not need the transition mutex. Avoiding that
	// mutex here also preserves the session -> post-response lock order.
	_, ok := runtime.virtualIDs[playerID]
	return ok
}

func (server *Server) competitiveAIHasHitRequest(gameID uint32, hit game.PlayerExplodedEvent) bool {
	runtime := server.competitiveAIRuntimeForGame(gameID)
	return runtime != nil && runtime.hasHitRequest(hit, time.Now())
}

// commitCompetitiveAIHitConfirmation applies the authority's reliable 0x0FA5
// audit only when it exactly matches a live virtual Type-2 request. One
// lifecycle-bound marker is retained per actor so a delayed audit or reliable
// retransmission is not invalidated by an arbitrary wall-clock timeout.
func (server *Server) commitCompetitiveAIHitConfirmation(gameID uint32, reporterID uint16, hit game.PlayerExplodedEvent) {
	runtime := server.competitiveAIRuntimeForGame(gameID)
	if runtime == nil {
		return
	}
	err := runRoomActor(server, runtime.roomID, "competitive-ai-hit-confirmation", func() error {
		server.commitCompetitiveAIHitConfirmationOnRoomActor(runtime, reporterID, hit)
		return nil
	})
	if err != nil {
		server.log(logEvent{Level: "error", Event: "competitive_ai_hit_confirmation_actor_failed", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d_reporter_%d", gameID, hit.PlayerID, reporterID), ErrorContext: err.Error()})
	}
}

func (server *Server) commitCompetitiveAIHitConfirmationOnRoomActor(runtime *liveCompetitiveAIRuntime, reporterID uint16, hit game.PlayerExplodedEvent) {
	gameID := runtime.gameID
	battle, err := server.competitiveBattle(gameID)
	if err != nil || !battle.IsArbitrator(reporterID) {
		return
	}

	key := competitiveAIHitKey(hit)
	runtime.mu.Lock()
	runtime.hitRequestMu.Lock()
	request, ok := runtime.hitRequests[key]
	if !ok {
		runtime.hitRequestMu.Unlock()
		runtime.mu.Unlock()
		server.log(logEvent{
			Level: "warn", Event: "competitive_ai_hit_confirmation_missing", RoomID: fmt.Sprint(runtime.roomID),
			Result: fmt.Sprintf("game_%d_player_%d_reporter_%d_time_%d_pos_%d_%d_avatar_%t", gameID, hit.PlayerID, reporterID, hit.ClientTime, hit.PosX, hit.PosY, hit.IsAvatar),
		})
		return
	}
	if request.state == competitiveAIHitCommitted {
		runtime.hitRequestMu.Unlock()
		runtime.mu.Unlock()
		return
	}
	_, changed, acceptErr := runtime.runtime.AcceptNativeActorHitFrom(
		hit.PlayerID, request.sourceID,
		battleengine.Position{X: int32(hit.PosX), Y: int32(hit.PosY)}, hit.IsAvatar,
	)
	if acceptErr == nil {
		request.state = competitiveAIHitCommitted
		runtime.hitRequests[key] = request
		_ = runtime.runtime.SuspendVirtualActor(hit.PlayerID, false)
	}
	runtime.hitRequestMu.Unlock()
	if acceptErr == nil && changed && !hit.IsAvatar {
		acceptErr = battle.RecordTrapped(hit.PlayerID)
	}
	runtime.recordPositionFrameLocked(runtime.runtime.EngineSnapshot())
	runtime.mu.Unlock()

	if acceptErr != nil {
		server.log(logEvent{
			Level: "warn", Event: "competitive_ai_hit_confirmation_rejected", RoomID: fmt.Sprint(runtime.roomID),
			Result: fmt.Sprintf("game_%d_player_%d_source_%d_reporter_%d_avatar_%t", gameID, hit.PlayerID, request.sourceID, reporterID, hit.IsAvatar), ErrorContext: acceptErr.Error(),
		})
		return
	}
	server.log(logEvent{
		Level: "debug", Event: "competitive_ai_hit_confirmation_committed", RoomID: fmt.Sprint(runtime.roomID),
		Result: fmt.Sprintf("game_%d_player_%d_source_%d_reporter_%d_avatar_%t_changed_%t", gameID, hit.PlayerID, request.sourceID, reporterID, hit.IsAvatar, changed),
	})
}

func (server *Server) recordCompetitiveAIHumanDeparture(gameID uint32, playerID uint16) {
	runtime := server.competitiveAIRuntimeForGame(gameID)
	if runtime == nil || playerID == 0 {
		return
	}
	err := runRoomActor(server, runtime.roomID, "competitive-ai-human-departure", func() error {
		server.recordCompetitiveAIHumanDepartureOnRoomActor(runtime, playerID)
		return nil
	})
	if err != nil {
		server.log(logEvent{Level: "error", Event: "competitive_ai_human_departure_actor_failed", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d", gameID, playerID), ErrorContext: err.Error()})
	}
}

func (server *Server) recordCompetitiveAIHumanDepartureOnRoomActor(runtime *liveCompetitiveAIRuntime, playerID uint16) {
	gameID := runtime.gameID
	runtime.mu.Lock()
	_, human := runtime.humanIDs[playerID]
	if human {
		delete(runtime.humanIDs, playerID)
	}
	var err error
	if human {
		err = runtime.runtime.AcceptHumanDeparture(playerID)
	}
	runtime.mu.Unlock()
	if err != nil {
		server.log(logEvent{
			Level: "warn", Event: "competitive_ai_human_departure_rejected",
			RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d", gameID, playerID),
			ErrorContext: err.Error(),
		})
	}
}

// recordCompetitiveAINativeElimination commits the real arbitrator's reliable
// death result into the virtual engine. The Type-2 copy owns client rendering;
// this method changes no network state and is intentionally idempotent because
// some clients submit both the native peer copy and its reliable audit mirror.
func (server *Server) recordCompetitiveAINativeElimination(gameID uint32, killerID, targetID uint16, items []game.GameItem) {
	runtime := server.competitiveAIRuntimeForGame(gameID)
	if runtime == nil || targetID == 0 {
		return
	}
	err := runRoomActor(server, runtime.roomID, "competitive-ai-native-elimination", func() error {
		server.recordCompetitiveAINativeEliminationOnRoomActor(runtime, killerID, targetID, items)
		return nil
	})
	if err != nil {
		server.log(logEvent{Level: "error", Event: "competitive_ai_native_elimination_actor_failed", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_source_%d_target_%d", gameID, killerID, targetID), ErrorContext: err.Error()})
	}
}

func (server *Server) recordCompetitiveAINativeEliminationOnRoomActor(runtime *liveCompetitiveAIRuntime, killerID, targetID uint16, items []game.GameItem) {
	gameID := runtime.gameID
	runtime.mu.Lock()
	if _, virtual := runtime.virtualIDs[targetID]; !virtual {
		runtime.mu.Unlock()
		return
	}
	err := runtime.acceptNativeEliminationLocked(targetID, killerID, battlePickupsFromGameItems(items))
	runtime.mu.Unlock()
	if err != nil {
		if server.logWriter != nil {
			server.log(logEvent{
				Level: "warn", Event: "competitive_ai_reliable_kill_rejected", RoomID: fmt.Sprint(runtime.roomID),
				Result: fmt.Sprintf("game_%d_source_%d_target_%d_items_%d", gameID, killerID, targetID, len(items)), ErrorContext: err.Error(),
			})
		}
		return
	}
	if server.logWriter != nil {
		server.log(logEvent{
			Level: "debug", Event: "competitive_ai_reliable_kill_accepted", RoomID: fmt.Sprint(runtime.roomID),
			Result: fmt.Sprintf("game_%d_source_%d_target_%d_items_%d", gameID, killerID, targetID, len(items)),
		})
	}
}

// acceptNativeEliminationLocked is the lifecycle barrier shared by the
// arbitrator's Type-2 fact and its reliable audit mirror. Besides changing the
// engine actor state it discards actor intents that were waiting for an
// arbitrator reply, so a death cannot be followed by an old pickup, hit or map
// interaction request. Already placed bubbles deliberately remain world
// objects and continue their native fuse.
func (runtime *liveCompetitiveAIRuntime) acceptNativeEliminationLocked(playerID, killerID uint16, drops []battleengine.Pickup) error {
	if runtime == nil || runtime.runtime == nil {
		return fmt.Errorf("competitive AI runtime is nil")
	}
	alreadyEliminated := false
	if snapshot := runtime.runtime.EngineSnapshot(); snapshot != nil {
		if actor, ok := actorByID(snapshot.Actors(), playerID); ok && actor.State == battleengine.ActorEliminated {
			alreadyEliminated = true
		}
	}
	if !alreadyEliminated {
		if _, err := runtime.runtime.AcceptNativeElimination(playerID, killerID, drops); err != nil {
			return err
		}
	}
	// Cleanup is deliberately idempotent. The fixed engine may have reached its
	// syrup timeout just before the arbitrator's death mirror arrives; that does
	// not make the queued native intents valid again.
	delete(runtime.movement, playerID)
	_ = runtime.runtime.SuspendVirtualActor(playerID, false)
	runtime.hitRequestMu.Lock()
	for key := range runtime.hitRequests {
		if key.playerID == playerID {
			delete(runtime.hitRequests, key)
		}
	}
	runtime.hitRequestMu.Unlock()

	runtime.pickupRequestMu.Lock()
	for key := range runtime.pickupRequests {
		if key.playerID == playerID {
			delete(runtime.pickupRequests, key)
		}
	}
	runtime.pickupRequestMu.Unlock()
	runtime.sceneRequestMu.Lock()
	for key := range runtime.sceneRequests {
		if key.playerID == playerID {
			delete(runtime.sceneRequests, key)
		}
	}
	runtime.sceneRequestMu.Unlock()
	return nil
}

func (runtime *liveCompetitiveAIRuntime) run(server *Server) {
	startedAt := time.Now()
	if err := runRoomActor(server, runtime.roomID, "competitive-ai-start", func() error {
		runtime.mu.Lock()
		runtime.nativeGameStartedAt = startedAt
		runtime.mu.Unlock()
		return nil
	}); err != nil {
		server.log(logEvent{Level: "error", Event: "competitive_ai_start_failed", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d", runtime.gameID), ErrorContext: err.Error()})
		return
	}
	for !runtime.waitForNativePeers(server, competitiveAIPeerWait) {
		select {
		case <-runtime.stop:
			return
		case <-server.done:
			return
		default:
		}
	}
	if remaining := competitiveAINativeStartDelay - time.Since(startedAt); remaining > 0 {
		startTimer := time.NewTimer(remaining)
		select {
		case <-startTimer.C:
		case <-runtime.stop:
			startTimer.Stop()
			return
		case <-server.done:
			startTimer.Stop()
			return
		}
	}
	server.log(logEvent{Level: "info", Event: "competitive_ai_native_round_started", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_delay_%s", runtime.gameID, time.Since(startedAt).Round(time.Millisecond))})
	ticker := time.NewTicker(runtime.worldTick)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			runtime.advance(server)
		case <-runtime.stop:
			return
		case <-server.done:
			return
		}
	}
}

func (runtime *liveCompetitiveAIRuntime) advance(server *Server) {
	err := runRoomActor(server, runtime.roomID, "competitive-ai-tick", func() error {
		return runtime.advanceOnRoomActor(server)
	})
	if err != nil {
		server.log(logEvent{Level: "error", Event: "competitive_ai_step_failed", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d", runtime.gameID), ErrorContext: err.Error()})
		runtime.stopRuntime()
	}
}

func (runtime *liveCompetitiveAIRuntime) advanceOnRoomActor(server *Server) error {
	if runtime.stopped() {
		return nil
	}
	runtime.mu.Lock()
	if runtime.stopped() {
		runtime.mu.Unlock()
		return nil
	}
	target := runtime.targetElapsedMSLocked(time.Now())
	runtime.retryHitRequests(server, time.Now())
	err := runtime.advanceToLocked(server, target, competitiveAIMaxCatchUpSteps)
	runtime.mu.Unlock()
	return err
}

// advanceToLocked performs fixed world transitions up to one already-observed
// causal scene time. A positive maxSteps bounds ordinary scheduler recovery;
// zero lets an authenticated authority event close an existing backlog before
// that event mutates the world. Callers hold runtime.mu throughout so the event
// cannot interleave with a partially advanced frame.
func (runtime *liveCompetitiveAIRuntime) advanceToLocked(server *Server, target uint32, maxSteps int) error {
	for steps := 0; maxSteps <= 0 || steps < maxSteps; steps++ {
		before := runtime.runtime.EngineSnapshot()
		if before == nil {
			break
		}
		if before.ElapsedMS() >= target {
			break
		}
		stepResult, err := runtime.runtime.StepWithTraceConcurrentPolicies(nil)
		after := runtime.runtime.EngineSnapshot()
		if err != nil {
			return err
		}
		if after == nil {
			break
		}
		runtime.recordPositionFrameLocked(after)
		runtime.refreshPickupRequests(after)
		if runtime.stopped() {
			return nil
		}
		// FUN_0087960b dispatches a gameplay message to its schema handler; it
		// is not a movement flush. Native bit 5 comes from actor+0x478 (for
		// example FUN_005acaab on a hit), not from producing an ordinary action.
		runtime.projectEvents(server, before, after, stepResult.Events)
		runtime.projectMovement(server, before, after, stepResult.Actions)
		if runtime.stopped() {
			return nil
		}
		if after.Terminal().Ended || after.ElapsedMS() >= target {
			break
		}
	}
	return nil
}

// observeAuthorityClockLocked anchors the virtual scene to the authenticated
// arbitrator's own gameplay clock. Duplicate UDP copies and the later reliable
// mirror must not move the wall anchor forward, otherwise repeated delivery
// would manufacture time that never elapsed in Client.exe.
func (runtime *liveCompetitiveAIRuntime) observeAuthorityClockLocked(sceneMS uint32, now time.Time) {
	if runtime == nil || sceneMS < battleengine.NativeRoundStartClockMS || sceneMS > battleengine.StandardRoundTimeMS {
		return
	}
	if runtime.authorityClockReady && sceneMS <= runtime.authorityClockMS {
		return
	}
	runtime.authorityClockReady = true
	runtime.authorityClockMS = sceneMS
	runtime.authorityClockAt = now
}

func (runtime *liveCompetitiveAIRuntime) targetElapsedMSLocked(now time.Time) uint32 {
	tickMS := uint32(runtime.worldTick / time.Millisecond)
	wallTarget := competitiveAITargetElapsedMS(runtime.nativeGameStartedAt, now, tickMS)
	if !runtime.authorityClockReady {
		return wallTarget
	}
	authorityTarget := runtime.authorityClockMS
	if !runtime.authorityClockAt.IsZero() && now.After(runtime.authorityClockAt) {
		delta := now.Sub(runtime.authorityClockAt).Milliseconds()
		if delta > int64(battleengine.StandardRoundTimeMS-authorityTarget) {
			authorityTarget = battleengine.StandardRoundTimeMS
		} else {
			authorityTarget += uint32(delta)
		}
	}
	if tickMS == 0 {
		tickMS = competitiveAIWorldTickMS
	}
	controlled := authorityTarget - battleengine.NativeRoundStartClockMS
	authorityTarget = battleengine.NativeRoundStartClockMS + controlled/tickMS*tickMS
	if authorityTarget < wallTarget {
		return authorityTarget
	}
	return wallTarget
}

func (runtime *liveCompetitiveAIRuntime) recordPositionFrameLocked(snapshot *battleengine.Engine) {
	if runtime == nil || snapshot == nil {
		return
	}
	actors := make(map[uint16]liveCompetitiveAIActorFrame, len(runtime.virtualIDs))
	for _, actor := range snapshot.Actors() {
		if _, virtual := runtime.virtualIDs[actor.PlayerID]; !virtual {
			continue
		}
		actors[actor.PlayerID] = liveCompetitiveAIActorFrame{
			position: actor.Position, state: actor.State, facing: actor.Facing, speed: actor.SpeedRate,
			transformationSceneID: actor.TransformationSceneID, harmProtectionExpiresAt: actor.HarmProtectionExpiresAt,
		}
	}
	frame := liveCompetitiveAIPositionFrame{timeMS: snapshot.ElapsedMS(), actors: actors}
	count := len(runtime.positionHistory)
	if count != 0 && runtime.positionHistory[count-1].timeMS == frame.timeMS {
		runtime.positionHistory[count-1] = frame
		return
	}
	if count != 0 && runtime.positionHistory[count-1].timeMS > frame.timeMS {
		return
	}
	runtime.positionHistory = append(runtime.positionHistory, frame)
}

func (runtime *liveCompetitiveAIRuntime) actorFrameAtLocked(playerID uint16, timeMS uint32) (liveCompetitiveAIActorFrame, bool) {
	if runtime == nil || len(runtime.positionHistory) == 0 {
		return liveCompetitiveAIActorFrame{}, false
	}
	requiredFrame := competitiveAICompletedSceneFrameMS(timeMS, uint32(runtime.worldTick/time.Millisecond))
	if runtime.positionHistory[len(runtime.positionHistory)-1].timeMS < requiredFrame {
		return liveCompetitiveAIActorFrame{}, false
	}
	index := sort.Search(len(runtime.positionHistory), func(index int) bool {
		return runtime.positionHistory[index].timeMS > timeMS
	}) - 1
	if index < 0 {
		return liveCompetitiveAIActorFrame{}, false
	}
	actor, ok := runtime.positionHistory[index].actors[playerID]
	return actor, ok
}

func competitiveAICompletedSceneFrameMS(sceneMS, tickMS uint32) uint32 {
	if sceneMS <= battleengine.NativeRoundStartClockMS {
		return battleengine.NativeRoundStartClockMS
	}
	if tickMS == 0 {
		tickMS = competitiveAIWorldTickMS
	}
	controlled := sceneMS - battleengine.NativeRoundStartClockMS
	return battleengine.NativeRoundStartClockMS + controlled/tickMS*tickMS
}

func competitiveAITargetElapsedMS(startedAt, now time.Time, tickMS uint32) uint32 {
	if startedAt.IsZero() || now.Before(startedAt) {
		return battleengine.NativeRoundStartClockMS
	}
	if tickMS == 0 {
		tickMS = competitiveAIWorldTickMS
	}
	elapsed := now.Sub(startedAt).Milliseconds()
	if elapsed < int64(battleengine.NativeRoundStartClockMS) {
		return battleengine.NativeRoundStartClockMS
	}
	if elapsed > int64(^uint32(0)) {
		elapsed = int64(^uint32(0))
	}
	// The deterministic engine may only project transitions whose complete
	// fixed interval has elapsed on the native scene clock. Feeding a raw wall
	// time such as 3101 ms into the catch-up loop previously advanced 3100 ->
	// 3200 and kept the remote actor almost one full tick in the future. The
	// original client then corrected that speculative coordinate on its next
	// absolute sample, producing the remaining small forward/backward drift.
	// Quantize relative to the 3000 ms ReadyGo boundary; catch-up still restores
	// genuinely missed intervals without ever predicting a future one.
	controlled := uint32(elapsed) - battleengine.NativeRoundStartClockMS
	return battleengine.NativeRoundStartClockMS + controlled/tickMS*tickMS
}

func verifiedExplosionOwnerAt(bombs []battleengine.VerifiedExplodedBomb, cell battleengine.Cell) (uint16, bool) {
	for _, bomb := range bombs {
		horizontal := cell.Row == bomb.Cell.Row && cell.Col >= bomb.BlastColMin && cell.Col <= bomb.BlastColMax
		vertical := cell.Col == bomb.Cell.Col && cell.Row >= bomb.BlastRowMin && cell.Row <= bomb.BlastRowMax
		if horizontal || vertical {
			return bomb.OwnerID, true
		}
	}
	return 0, false
}

// reconcileVirtualExplosionHits mirrors Client.exe's 0x0FA4 consumer on the
// producer's causal scene frame. A delayed receive must neither test the
// server's later wall-driven coordinate nor widen the hit box into a recent
// history window: select exactly the last completed 20 ms frame at or before
// the explosion timestamp, then apply the native cross-shaped cell test.
func (runtime *liveCompetitiveAIRuntime) reconcileVirtualExplosionHits(bombs []battleengine.VerifiedExplodedBomb, explosionTime uint32) []battleengine.Event {
	if runtime == nil || len(bombs) == 0 {
		return nil
	}
	snapshot := runtime.runtime.EngineSnapshot()
	if snapshot == nil {
		return nil
	}
	actors := snapshot.Actors()
	eventTime := competitiveAIEngineTime(explosionTime)
	playerIDs := make([]int, 0, len(runtime.virtualIDs))
	for playerID := range runtime.virtualIDs {
		playerIDs = append(playerIDs, int(playerID))
	}
	sort.Ints(playerIDs)
	events := make([]battleengine.Event, 0)
	for _, value := range playerIDs {
		playerID := uint16(value)
		current, ok := actorByID(actors, playerID)
		if !ok || current.State != battleengine.ActorActive || runtime.runtime.VirtualActorSuspended(playerID) {
			continue
		}
		actor, ok := runtime.actorFrameAtLocked(playerID, eventTime)
		if !ok || actor.state != battleengine.ActorActive || actor.harmProtectionExpiresAt > eventTime {
			continue
		}
		sourceID, hit := verifiedExplosionOwnerAt(bombs, actor.position.Cell())
		if !hit {
			continue
		}
		if _, sourcePresent := actorByID(actors, sourceID); !sourcePresent {
			continue
		}
		events = append(events, battleengine.Event{
			Kind: battleengine.EventActorHitRequested, TimeMS: eventTime,
			PlayerID: playerID, TargetID: sourceID,
			Cell: actor.position.Cell(), Position: actor.position, SceneID: actor.transformationSceneID,
		})
	}
	return events
}

func actorByID(actors []battleengine.Actor, playerID uint16) (battleengine.Actor, bool) {
	for _, actor := range actors {
		if actor.PlayerID == playerID {
			return actor, true
		}
	}
	return battleengine.Actor{}, false
}

func bombByID(bombs []battleengine.Bomb, bombID uint32) (battleengine.Bomb, bool) {
	for _, bomb := range bombs {
		if bomb.ID == bombID {
			return bomb, true
		}
	}
	return battleengine.Bomb{}, false
}

func (runtime *liveCompetitiveAIRuntime) projectMovement(
	server *Server,
	before *battleengine.Engine,
	snapshot *battleengine.Engine,
	appliedActions []battleengine.Action,
) {
	if snapshot == nil {
		return
	}
	actions := make(map[uint16]battleengine.Action, len(appliedActions))
	for _, action := range appliedActions {
		actions[action.PlayerID] = action
	}
	playerIDs := make([]int, 0, len(runtime.virtualIDs))
	for playerID := range runtime.virtualIDs {
		playerIDs = append(playerIDs, int(playerID))
	}
	sort.Ints(playerIDs)
	for _, value := range playerIDs {
		playerID := uint16(value)
		if runtime.runtime.VirtualActorSuspended(playerID) {
			continue
		}
		projection := runtime.movement[playerID]
		projection, moves, err := competitiveAIMovementFrameSamples(before, snapshot, playerID, actions[playerID], projection, false)
		runtime.movement[playerID] = projection
		if err != nil {
			server.log(logEvent{Level: "warn", Event: "competitive_ai_move_projection_failed", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d", runtime.gameID, playerID), ErrorContext: err.Error()})
			continue
		}
		for _, move := range moves {
			if err := runtime.sendMovementSample(server, playerID, move.TimeStamp, move); err != nil {
				server.log(logEvent{Level: "warn", Event: "competitive_ai_move_peer_failed", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d", runtime.gameID, playerID), ErrorContext: err.Error()})
			}
		}
	}
}

// A fixed engine step applies its selected key over [before, after]. Publish
// ordinary movement at that interval's start, where the actor actually
// turns. Publishing only its post-step position omits the first 20 ms of the
// new path: FUN_005d0b81 then snaps or backtracks from the old extrapolated path.
// Native producers can flush a key change on a zero-delta update (the QBV human
// turn at 9969 ms changes down -> right at x=60, before advancing right).
// Action-forced checkpoints keep their existing event ordering and position.
func competitiveAIMovementFrameSamples(
	before, after *battleengine.Engine, playerID uint16, action battleengine.Action,
	projection liveCompetitiveAIMovementProjection, forced bool,
) (liveCompetitiveAIMovementProjection, []game.PlayerMoveSequence, error) {
	if after == nil {
		return projection, nil, fmt.Errorf("movement snapshot is nil")
	}
	current, currentOK := actorByID(after.Actors(), playerID)
	if !currentOK {
		return projection, nil, fmt.Errorf("movement player %d is absent", playerID)
	}
	if current.State == battleengine.ActorEliminated {
		return projection, nil, nil
	}
	moving, direction := competitiveAINativeMovementIntent(action, current)
	var moves []game.PlayerMoveSequence
	if before != nil && !forced &&
		(!projection.initialized || projection.lastSentAt < before.ElapsedMS()) {
		previous, previousOK := actorByID(before.Actors(), playerID)
		beforeSpeed, _ := before.NativeEffectiveSpeedRate(playerID, direction)
		afterSpeed, _ := after.NativeEffectiveSpeedRate(playerID, direction)
		// Attribute/status transitions belong to the post-event checkpoint,
		// whereas ordinary movement has an unambiguous input boundary.
		if previousOK && previous.State == battleengine.ActorActive && current.State == battleengine.ActorActive &&
			previous.MovementStatus == current.MovementStatus && previous.TransformationSceneID == current.TransformationSceneID &&
			beforeSpeed == afterSpeed {
			var move game.PlayerMoveSequence
			var emitted bool
			var err error
			projection, move, emitted, err = competitiveAIMovementSampleWithIntent(before, previous, moving, direction, projection, false)
			if err != nil {
				return projection, nil, err
			}
			if emitted {
				moves = append(moves, move)
			}
			// Heartbeats and collision-path updates use the same frame origin.
			// Publishing another ordinary checkpoint at `after` would consume
			// the next frame's turn timestamp; native queues reject two different
			// paths at the same timestamp, recreating the 20 ms reversal jump.
			return projection, moves, nil
		}
	}
	projection, move, emitted, err := competitiveAIMovementSampleWithIntent(after, current, moving, direction, projection, forced)
	if emitted {
		moves = append(moves, move)
	}
	return projection, moves, err
}

func (runtime *liveCompetitiveAIRuntime) sendMovementSample(server *Server, playerID uint16, wireTime uint32, move game.PlayerMoveSequence) error {
	runtime.messageSeq[playerID]++
	sequence := runtime.messageSeq[playerID]
	movement := game.PlayerMove{PlayerID: playerID, SeqReply: 0, Entries: []game.PlayerMoveEntry{{
		PlayerID: playerID,
		Move:     move,
	}}}
	body, err := movement.MarshalNetworkBinary()
	if err != nil {
		return fmt.Errorf("marshal movement: %w", err)
	}
	message := game.BattleMessageData{Time: wireTime, DataID: uint32(game.PlayerMoveSchema), Sequence: sequence, Data: body, GameTime: wireTime, Flag: 0}
	packet := game.GameplayDataPackage{PlayerID: playerID, Time: wireTime, GameID: runtime.gameID, MessageIndexes: []uint32{sequence}, Messages: []game.BattleMessageData{message}}
	payload, err := game.EncodeQQTPPPGameplayBatch(game.GameplayBatch{
		GameID:  runtime.gameID,
		Entries: []game.GameplayBatchEntry{{Index: uint16(sequence), Package: packet}},
	})
	if err != nil {
		return fmt.Errorf("encode movement batch: %w", err)
	}
	return runtime.sendPeerPayload(server, playerID, payload)
}

func competitiveAIMovementSample(
	snapshot *battleengine.Engine,
	playerID uint16,
	action battleengine.Action,
	projection liveCompetitiveAIMovementProjection,
	forced bool,
) (liveCompetitiveAIMovementProjection, game.PlayerMoveSequence, bool, error) {
	if snapshot == nil {
		return projection, game.PlayerMoveSequence{}, false, fmt.Errorf("movement snapshot is nil")
	}
	current, present := actorByID(snapshot.Actors(), playerID)
	if !present {
		return projection, game.PlayerMoveSequence{}, false, fmt.Errorf("movement player %d is absent", playerID)
	}
	if current.State == battleengine.ActorEliminated {
		return projection, game.PlayerMoveSequence{}, false, nil
	}
	moving, direction := competitiveAINativeMovementIntent(action, current)
	return competitiveAIMovementSampleWithIntent(snapshot, current, moving, direction, projection, forced)
}

func competitiveAIMovementSampleWithIntent(
	snapshot *battleengine.Engine, current battleengine.Actor, moving bool, direction battleengine.Direction,
	projection liveCompetitiveAIMovementProjection, forced bool,
) (liveCompetitiveAIMovementProjection, game.PlayerMoveSequence, bool, error) {
	playerID := current.PlayerID
	effectiveSpeed, speedPresent := snapshot.NativeEffectiveSpeedRate(playerID, direction)
	if !speedPresent {
		return projection, game.PlayerMoveSequence{}, false, fmt.Errorf("movement speed player %d is absent", playerID)
	}
	boundary := !projection.initialized || moving != projection.moving ||
		(moving && direction != projection.direction) || effectiveSpeed != projection.speed
	if boundary {
		projection.sequence++
	}
	path := battleengine.NativeMovementProjection{End: current.Position}
	if moving {
		var err error
		path, err = snapshot.ProjectNativeMovement(playerID, direction)
		if err != nil {
			return projection, game.PlayerMoveSequence{}, false, err
		}
		// FUN_005f34ee retains the existing corner after the actor has passed
		// it, provided the collision endpoint and held segment are unchanged.
		if !boundary && path.End == projection.path.End && !path.HasCorner {
			path.Corner, path.HasCorner = projection.path.Corner, projection.path.HasCorner
		}
	}
	// The native producer sets its dirty flag as soon as the collision path
	// changes, even with the same held key. Waiting for the heartbeat leaves
	// peers following the old wall/corner for up to 160 ms.
	pathChanged := path != projection.path
	due := boundary || forced || pathChanged || snapshot.ElapsedMS()-projection.lastSentAt >= competitiveAIMovementHeartbeatMS
	if !due || (!forced && !boundary && !pathChanged && projection.lastSentAt == snapshot.ElapsedMS() && projection.lastSentPosition == current.Position) {
		return projection, game.PlayerMoveSequence{}, false, nil
	}
	projection.initialized = true
	projection.moving = moving
	projection.direction = direction
	projection.speed = effectiveSpeed
	projection.path = path
	walk := nativeWalkDirection(direction)
	if forced {
		// FUN_005f3695 stores its explicit flush argument in bit 5.
		walk |= 0x20
	}
	if moving {
		walk |= 0x10
	}
	endX, endY := path.End.X, path.End.Y
	cornerX, cornerY := int32(0), int32(0)
	if path.HasCorner {
		cornerX, cornerY = path.Corner.X, path.Corner.Y
	}
	grid := snapshot.Grid()
	endX = clampPosition(endX, int32(grid.Width)*battleengine.CellSizePixels-1)
	endY = clampPosition(endY, int32(grid.Height)*battleengine.CellSizePixels-1)
	wireTime := competitiveAIWireTime(snapshot.ElapsedMS())
	move := game.PlayerMoveSequence{
		Sequence: projection.sequence, TimeStamp: wireTime,
		CurrentPosX: uint16(current.Position.X), CurrentPosY: uint16(current.Position.Y),
		CornerPosX: uint16(cornerX), CornerPosY: uint16(cornerY),
		EndPosX: uint16(endX), EndPosY: uint16(endY), WalkAndDirection: walk, Speed: effectiveSpeed,
	}
	projection.lastSentAt = snapshot.ElapsedMS()
	projection.lastSentPosition = current.Position
	return projection, move, true, nil
}

func competitiveAIHitMovementSample(
	event battleengine.Event,
	frame liveCompetitiveAIActorFrame,
	projection liveCompetitiveAIMovementProjection,
	grid battleengine.Grid,
) (liveCompetitiveAIMovementProjection, game.PlayerMoveSequence) {
	// 005acaab and avatar callback 005f78bf both stop and face down before
	// 005f3810 consumes actor+478. Held input is sampled again next frame.
	direction := battleengine.DirectionDown
	moving := false
	boundary := !projection.initialized || moving != projection.moving || (moving && direction != projection.direction) || frame.speed != projection.speed
	if boundary {
		projection.sequence++
	}
	projection.initialized = true
	projection.moving = moving
	projection.direction = direction
	projection.speed = frame.speed
	projection.lastSentAt = event.TimeMS
	projection.lastSentPosition = event.Position

	walk := nativeWalkDirection(direction) | 0x20
	if moving {
		walk |= 0x10
	}
	positionX := clampPosition(event.Position.X, int32(grid.Width)*battleengine.CellSizePixels-1)
	positionY := clampPosition(event.Position.Y, int32(grid.Height)*battleengine.CellSizePixels-1)
	return projection, game.PlayerMoveSequence{
		Sequence: projection.sequence, TimeStamp: competitiveAIWireTime(event.TimeMS),
		CurrentPosX: uint16(positionX), CurrentPosY: uint16(positionY),
		EndPosX: uint16(positionX), EndPosY: uint16(positionY),
		WalkAndDirection: walk, Speed: frame.speed,
	}
}

func competitiveAINativeMovementIntent(action battleengine.Action, actor battleengine.Actor) (bool, battleengine.Direction) {
	if actor.State != battleengine.ActorActive {
		return false, actor.Facing
	}
	if actor.MovementStatus == battleengine.MovementStatusForcedSlide {
		return true, actor.Facing
	}
	if action.Move == battleengine.DirectionNone {
		return false, actor.Facing
	}
	// Facing is the world-space direction after transformation controls and the
	// engine's native movement resolver have consumed this action.
	return true, actor.Facing
}

func competitiveAIWireTime(engineTime uint32) uint32 {
	return engineTime
}

func competitiveAIEngineTime(wireTime uint32) uint32 {
	if wireTime < battleengine.NativeRoundStartClockMS {
		return battleengine.NativeRoundStartClockMS
	}
	return wireTime
}

func nativeWalkDirection(direction battleengine.Direction) byte {
	switch direction {
	case battleengine.DirectionRight:
		return 0
	case battleengine.DirectionUp:
		return 1
	case battleengine.DirectionLeft:
		return 2
	case battleengine.DirectionDown:
		return 3
	default:
		return 3
	}
}

func battleDirection(walk byte) battleengine.Direction {
	switch walk & 0x03 {
	case 0:
		return battleengine.DirectionRight
	case 1:
		return battleengine.DirectionUp
	case 2:
		return battleengine.DirectionLeft
	default:
		return battleengine.DirectionDown
	}
}

func clampPosition(value, maximum int32) int32 {
	if value < 0 {
		return 0
	}
	if value > maximum {
		return maximum
	}
	return value
}

func (runtime *liveCompetitiveAIRuntime) virtualUIN(playerID uint16) uint32 {
	for _, projection := range runtime.roomProjections {
		if projection.PlayerID == playerID {
			return projection.UIN
		}
	}
	return 0
}

func (runtime *liveCompetitiveAIRuntime) virtualProjection(playerID uint16, uin uint32) (competitiveAIRoomProjection, bool) {
	for _, projection := range runtime.roomProjections {
		if projection.PlayerID == playerID && projection.UIN == uin {
			return projection, true
		}
	}
	return competitiveAIRoomProjection{}, false
}

func (runtime *liveCompetitiveAIRuntime) sendPeerPayload(server *Server, sourcePlayerID uint16, payload []byte) error {
	// The world transition mutex is held by every caller. Use only the atomic
	// room-routing projection here: taking a connection's session mutex would
	// invert REQUEST_LEAVE_ROOM's session -> runtime lock order and deadlock the
	// departing account before its ACK and cleanup can be committed.
	members := server.liveRoomRoutingSessions(runtime.roomID)
	if len(members) == 0 {
		return fmt.Errorf("no native room peers are available")
	}
	playerIDs := make([]uint16, 0, len(members))
	wanted := make(map[uint16]struct{}, len(members))
	for _, member := range members {
		playerID := uint16(member.livePlayerID.Load())
		if playerID == 0 {
			continue
		}
		playerIDs = append(playerIDs, playerID)
		wanted[playerID] = struct{}{}
	}
	recipients, err := server.resolveRoomFastUDPRecipients(runtime.roomID, 0, playerIDs, wanted)
	if err != nil {
		return err
	}
	sourceUIN := runtime.virtualUIN(sourcePlayerID)
	if sourceUIN == 0 {
		return fmt.Errorf("virtual UIN for player %d is absent", sourcePlayerID)
	}
	if _, err = server.sendVirtualRoomFastUDPPayloadsFromIdentity(sourcePlayerID, sourceUIN, runtime.roomID, [][]byte{payload}, recipients); err != nil {
		return err
	}
	return nil
}

func (runtime *liveCompetitiveAIRuntime) sendPeerPayloadToArbitrator(server *Server, sourcePlayerID uint16, payload []byte) error {
	battle, err := server.competitiveBattle(runtime.gameID)
	if err != nil {
		return err
	}
	arbitratorID := battle.ArbitratorPlayerID()
	if arbitratorID == 0 {
		return fmt.Errorf("competitive game %d has no native arbitrator", runtime.gameID)
	}
	recipients, err := server.resolveRoomFastUDPRecipients(
		runtime.roomID, 0, []uint16{arbitratorID}, map[uint16]struct{}{arbitratorID: {}},
	)
	if err != nil {
		return fmt.Errorf("resolve native arbitrator %d: %w", arbitratorID, err)
	}
	sourceUIN := runtime.virtualUIN(sourcePlayerID)
	if sourceUIN == 0 {
		return fmt.Errorf("virtual UIN for player %d is absent", sourcePlayerID)
	}
	_, err = server.sendVirtualRoomFastUDPPayloadsFromIdentity(sourcePlayerID, sourceUIN, runtime.roomID, [][]byte{payload}, recipients)
	return err
}

func competitiveAIArbitratorRequest(schema uint16) bool {
	switch schema {
	case game.RequestKillPlayer,
		game.RequestSavePlayer,
		game.RequestGetItem,
		game.RequestMoveMapElement,
		game.RequestMoveBomb:
		return true
	default:
		return false
	}
}

func competitiveAIArbitratorNotification(schema uint16) bool {
	switch schema {
	case game.NotifyBombExplode,
		game.NotifyPlayerExploded,
		game.NotifyPlayerKilled,
		game.NotifyPlayerSaved,
		game.NotifyPlayerGetItem,
		game.NotifyDispatchItem,
		game.NotifyItemExploded,
		game.NotifyMapElementMoved,
		game.NotifyPlayerMoveBomb,
		game.NotifyRecoverAvatar:
		return true
	default:
		return false
	}
}

func competitiveAICausalAuthorityMessage(schema uint16) bool {
	return competitiveAIArbitratorNotification(schema) || schema == game.PlayerBeExploded || schema == game.NotifyPlayerDieEvent
}

// sendPeerGameplayEvent emits one virtual-participant gameplay message through
// the QQTPPP Type-2 scene channel. Request schemas go only to the elected
// original-client arbitrator. Notification schemas are already authoritative
// server conversions and therefore go to every native participant.
func (runtime *liveCompetitiveAIRuntime) sendPeerGameplayEvent(server *Server, playerID uint16, wireTime uint32, schema uint16, gameTime uint32, body []byte) error {
	runtime.messageSeq[playerID]++
	sequence := runtime.messageSeq[playerID]
	payload, err := buildCompetitiveAIPeerGameplayPayload(runtime.gameID, playerID, wireTime, sequence, schema, gameTime, body)
	if err != nil {
		return err
	}
	if competitiveAIArbitratorRequest(schema) {
		return runtime.sendPeerPayloadToArbitrator(server, playerID, payload)
	}
	return runtime.sendPeerPayload(server, playerID, payload)
}

func buildCompetitiveAIPeerGameplayPayload(gameID uint32, playerID uint16, wireTime, sequence uint32, schema uint16, gameTime uint32, body []byte) ([]byte, error) {
	message := game.BattleMessageData{
		Time: wireTime, DataID: uint32(schema), Sequence: sequence,
		Data: body, GameTime: gameTime, Flag: 0,
	}
	packet := game.GameplayDataPackage{
		PlayerID: playerID, Time: wireTime, GameID: gameID,
		MessageIndexes: []uint32{sequence}, Messages: []game.BattleMessageData{message},
	}
	return game.EncodeQQTPPPGameplayBatch(game.GameplayBatch{
		GameID:  gameID,
		Entries: []game.GameplayBatchEntry{{Index: uint16(sequence), Package: packet}},
	})
}

func (runtime *liveCompetitiveAIRuntime) projectEvents(server *Server, before, after *battleengine.Engine, events []battleengine.Event) {
	for _, event := range events {
		if runtime.runtime.VirtualActorSuspended(event.PlayerID) && competitiveAISuspendedEvent(event.Kind) {
			continue
		}
		switch event.Kind {
		case battleengine.EventBombPlaced:
			if _, virtual := runtime.virtualIDs[event.PlayerID]; virtual {
				runtime.projectBombPlaced(server, after, event)
			}
		case battleengine.EventPickupCollectRequested:
			if _, virtual := runtime.virtualIDs[event.PlayerID]; virtual {
				runtime.projectPickup(server, event)
			}
		case battleengine.EventFieldObjectTriggered:
			if (event.ActionID == 42 || event.ActionID == 43) && event.TargetID != 0 {
				if _, virtual := runtime.virtualIDs[event.TargetID]; virtual {
					request := event
					request.PlayerID = event.TargetID
					request.SceneID = uint32(event.ActionID)
					runtime.projectPickup(server, request)
				}
			}
		case battleengine.EventActorHitRequested:
			if _, virtual := runtime.virtualIDs[event.PlayerID]; virtual {
				// projectActorHit emits its own event-time forced movement sample.
				// A generic flush against the later receive-time snapshot would move
				// a syrup-stopped actor to a coordinate it never occupied on screen.
				runtime.projectActorHit(server, event)
			}
		case battleengine.EventActorEliminated:
			if _, virtual := runtime.virtualIDs[event.TargetID]; virtual {
				runtime.projectEliminated(server, event, events)
			}
		case battleengine.EventActorRescueRequested:
			runtime.projectInteractionRequest(server, event, game.RequestSavePlayer)
		case battleengine.EventActorEliminationRequested:
			runtime.projectInteractionRequest(server, event, game.RequestKillPlayer)
		case battleengine.EventMapElementMoveRequested:
			if _, virtual := runtime.virtualIDs[event.PlayerID]; virtual {
				runtime.projectMapElementMoved(server, event)
			}
		case battleengine.EventBattleActionUseRequested:
			if _, virtual := runtime.virtualIDs[event.PlayerID]; virtual {
				runtime.projectBattleAction(server, event)
			}
		case battleengine.EventBombKickRequested:
			if _, virtual := runtime.virtualIDs[event.PlayerID]; virtual {
				runtime.projectBombKicked(server, before, event)
			}
		case battleengine.EventNativePassStarted:
			if _, virtual := runtime.virtualIDs[event.PlayerID]; virtual {
				server.log(logEvent{
					Level: "debug", Event: "competitive_ai_native_pass_started", RoomID: fmt.Sprint(runtime.roomID),
					Result: fmt.Sprintf("game_%d_player_%d_time_%d_cell_%d_%d", runtime.gameID, event.PlayerID, event.TimeMS, event.Cell.Row, event.Cell.Col),
				})
			}
		case battleengine.EventMatchEnded:
			// The ordinary wall-clock scheduler remains the common fallback for
			// every competitive match. In a live AI match the deterministic clock
			// reaches the same native rule deadline; consume that event as well so
			// a delayed scheduler goroutine cannot leave the client at 00:00.
			if after != nil && after.Terminal().Draw {
				resultTimeMS := after.ElapsedMS()
				server.completeCompetitiveTimeoutOnRoomActor(competitiveTimeoutSchedule{
					RoomID: runtime.roomID, GameID: runtime.gameID,
					Duration:     time.Duration(resultTimeMS) * time.Millisecond,
					ResultTimeMS: resultTimeMS,
				})
			}
		}
	}
}

func competitiveAISuspendedEvent(kind battleengine.EventKind) bool {
	switch kind {
	case battleengine.EventBombPlaced,
		battleengine.EventPickupCollectRequested,
		battleengine.EventActorHitRequested,
		battleengine.EventActorRescueRequested,
		battleengine.EventActorEliminationRequested,
		battleengine.EventMapElementMoveRequested,
		battleengine.EventBattleActionUseRequested,
		battleengine.EventBombKickRequested,
		battleengine.EventNativePassStarted:
		return true
	default:
		return false
	}
}

func (runtime *liveCompetitiveAIRuntime) projectBombPlaced(server *Server, after *battleengine.Engine, event battleengine.Event) bool {
	bomb, ok := bombByID(after.Bombs(), event.BombID)
	if !ok {
		return false
	}
	wireTime := competitiveAIWireTime(event.TimeMS)
	wirePower := uint16(bomb.Power) + 1
	runtime.bombs[event.BombID] = liveCompetitiveAIBomb{ownerID: event.PlayerID, placedAt: wireTime, position: event.Cell, appearance: competitiveAIDefaultBombAppearance, power: byte(wirePower)}
	property := byte(0)
	if bomb.SceneFourEffect {
		property = 1
	}
	body, err := (game.PlayerUseBombEvent{PlayerID: event.PlayerID, ClientTime: wireTime, BombID: competitiveAIDefaultBombAppearance, Row: byte(event.Cell.Row), Column: byte(event.Cell.Col), Power: wirePower, Property: property}).MarshalNetworkBinary()
	if err != nil {
		server.log(logEvent{Level: "error", Event: "competitive_ai_bomb_build_failed", RoomID: fmt.Sprint(runtime.roomID), ErrorContext: err.Error()})
		return false
	}
	// A virtual AI is projected as a normal room participant. Original room
	// participants publish 0x0FA3 on Type-2; 0x138B is the reliable direction
	// conversion reserved for native BOSS/NPC actors without room loadout state.
	if err := runtime.sendPeerGameplayEvent(server, event.PlayerID, wireTime, game.PlayerUseBomb, 0xffff0065, body); err != nil {
		server.log(logEvent{Level: "warn", Event: "competitive_ai_bomb_peer_failed", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d", runtime.gameID, event.PlayerID), ErrorContext: err.Error()})
		return true
	}
	server.log(logEvent{Level: "debug", Event: "competitive_ai_bomb_placed", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d_cell_%d_%d_reach_%d_wire_power_%d", runtime.gameID, event.PlayerID, event.Cell.Row, event.Cell.Col, bomb.Power, wirePower)})
	return true
}

func (runtime *liveCompetitiveAIRuntime) projectPickup(server *Server, event battleengine.Event) bool {
	position := event.Position
	if position == (battleengine.Position{}) {
		position = battleengine.PositionAtCellCenter(event.Cell)
	}
	wireTime := competitiveAIWireTime(event.TimeMS)
	// 0x0FAC is the virtual actor's contact intent. The real arbitrator validates
	// its own scene registry and publishes 0x0FAD; only that notification removes
	// the pickup and changes attributes. A negative result is sticky until the
	// actor exits the cell (or the scene object is replaced), matching native
	// contact-edge semantics instead of retrying a rejected overlap every tick.
	key := liveCompetitiveAIPickupRequestKey{playerID: event.PlayerID, sceneID: event.SceneID, row: event.Cell.Row, col: event.Cell.Col}
	runtime.pickupRequestMu.Lock()
	request, exists := runtime.pickupRequests[key]
	if exists {
		if request.state == competitiveAIPickupRejectedUntilExit || request.attempts >= 3 || event.TimeMS-request.lastSentAt < 1_000 {
			runtime.pickupRequestMu.Unlock()
			return false
		}
		// This is transport recovery for the same contact, not a new logical
		// request. Retain the original body clock and coordinate.
		wireTime = competitiveAIWireTime(request.timeMS)
		position = request.position
		request.lastSentAt = event.TimeMS
		request.attempts++
	} else {
		request = liveCompetitiveAIPickupRequest{
			timeMS: event.TimeMS, lastSentAt: event.TimeMS, position: position,
			state: competitiveAIPickupPending, attempts: 1,
		}
	}
	runtime.pickupRequests[key] = request
	runtime.pickupRequestMu.Unlock()
	body, err := (game.PlayerItemEvent{PlayerID: event.PlayerID, ClientTime: wireTime, ItemID: event.SceneID, PosX: uint16(position.X), PosY: uint16(position.Y)}).MarshalNetworkBinary()
	if err != nil {
		server.log(logEvent{Level: "error", Event: "competitive_ai_pickup_build_failed", RoomID: fmt.Sprint(runtime.roomID), ErrorContext: err.Error()})
		return false
	}
	if err = runtime.sendPeerGameplayEvent(server, event.PlayerID, wireTime, game.RequestGetItem, wireTime, body); err != nil {
		runtime.pickupRequestMu.Lock()
		delete(runtime.pickupRequests, key)
		runtime.pickupRequestMu.Unlock()
		server.log(logEvent{Level: "warn", Event: "competitive_ai_pickup_peer_failed", RoomID: fmt.Sprint(runtime.roomID), ErrorContext: err.Error()})
		return true
	}
	server.log(logEvent{Level: "debug", Event: "competitive_ai_pickup_requested", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d_item_%d_cell_%d_%d", runtime.gameID, event.PlayerID, event.SceneID, event.Cell.Row, event.Cell.Col)})
	return true
}

// refreshPickupRequests keeps an in-flight contact until its exact ACK while a
// retry is still available. Once all three transports were unanswered, a full
// footprint exit is a new native contact edge and must release the exhausted
// transaction; otherwise that item can remain permanently uncollectable.
func (runtime *liveCompetitiveAIRuntime) refreshPickupRequests(snapshot *battleengine.Engine) {
	if runtime == nil || snapshot == nil {
		return
	}
	actors := snapshot.Actors()
	pickups := snapshot.Pickups()
	fieldObjects := snapshot.FieldObjects()
	runtime.pickupRequestMu.Lock()
	defer runtime.pickupRequestMu.Unlock()
	for key, request := range runtime.pickupRequests {
		actor, present := actorByID(actors, key.playerID)
		cell := battleengine.Cell{Row: key.row, Col: key.col}
		if !present || actor.State != battleengine.ActorActive {
			delete(runtime.pickupRequests, key)
			continue
		}
		available := false
		for _, pickup := range pickups {
			if pickup.SceneID == key.sceneID && pickup.Cell == cell && pickup.State == battleengine.PickupAvailable {
				available = true
				break
			}
		}
		if !available && (key.sceneID == 42 || key.sceneID == 43) {
			for _, object := range fieldObjects {
				if uint32(object.ActionID) == key.sceneID && object.Cell == cell {
					available = true
					break
				}
			}
		}
		if !available {
			delete(runtime.pickupRequests, key)
			continue
		}
		exhausted := request.state == competitiveAIPickupPending && request.attempts >= 3
		if (request.state == competitiveAIPickupRejectedUntilExit || exhausted) && !snapshot.PositionOverlapsCell(actor.Position, cell) {
			delete(runtime.pickupRequests, key)
		}
	}
}

func (runtime *liveCompetitiveAIRuntime) clearPickupRequest(playerID uint16, sceneID uint32, position battleengine.Position) {
	if runtime == nil {
		return
	}
	cell := position.Cell()
	runtime.pickupRequestMu.Lock()
	defer runtime.pickupRequestMu.Unlock()
	for key := range runtime.pickupRequests {
		if key.sceneID != sceneID || (playerID != 0 && key.playerID != playerID) {
			continue
		}
		// A rejected notification uses PlayerID 0xffff, so only its position can
		// identify the pending request. Successful notifications name the actor;
		// clear that actor/item even if it crossed a cell during the round trip.
		if playerID == 0 && (key.row != cell.Row || key.col != cell.Col) {
			continue
		}
		delete(runtime.pickupRequests, key)
	}
}

func (runtime *liveCompetitiveAIRuntime) clearPickupRequestsForObjects(pickups []battleengine.Pickup) {
	if runtime == nil || len(pickups) == 0 {
		return
	}
	runtime.pickupRequestMu.Lock()
	defer runtime.pickupRequestMu.Unlock()
	for key := range runtime.pickupRequests {
		for _, pickup := range pickups {
			if key.sceneID == pickup.SceneID && key.row == pickup.Cell.Row && key.col == pickup.Cell.Col {
				delete(runtime.pickupRequests, key)
				break
			}
		}
	}
}

// takeRejectedPickupRequest resolves the arbitrator's 0xffff negative ACK
// back to one exact virtual request. Client.exe preserves the original item,
// clock and world position in that ACK. Matching all three prevents two AIs
// crossing the same cell from consuming each other's intent.
func (runtime *liveCompetitiveAIRuntime) takeRejectedPickupRequest(sceneID, wireTime uint32, position battleengine.Position) (liveCompetitiveAIPickupRequestKey, bool) {
	if runtime == nil {
		return liveCompetitiveAIPickupRequestKey{}, false
	}
	runtime.pickupRequestMu.Lock()
	defer runtime.pickupRequestMu.Unlock()
	for key, request := range runtime.pickupRequests {
		if key.sceneID != sceneID || competitiveAIWireTime(request.timeMS) != wireTime || request.position != position {
			continue
		}
		request.state = competitiveAIPickupRejectedUntilExit
		runtime.pickupRequests[key] = request
		return key, true
	}
	return liveCompetitiveAIPickupRequestKey{}, false
}

func (runtime *liveCompetitiveAIRuntime) reserveSceneRequest(key liveCompetitiveAISceneRequestKey, timeMS uint32) bool {
	runtime.sceneRequestMu.Lock()
	defer runtime.sceneRequestMu.Unlock()
	if last, pending := runtime.sceneRequests[key]; pending && timeMS-last < 500 {
		return false
	}
	runtime.sceneRequests[key] = timeMS
	return true
}

func (runtime *liveCompetitiveAIRuntime) clearSceneRequest(key liveCompetitiveAISceneRequestKey) {
	runtime.sceneRequestMu.Lock()
	delete(runtime.sceneRequests, key)
	runtime.sceneRequestMu.Unlock()
}

func competitiveAIHitKey(hit game.PlayerExplodedEvent) liveCompetitiveAIHitRequestKey {
	return liveCompetitiveAIHitRequestKey{
		playerID: hit.PlayerID, clientTime: hit.ClientTime,
		posX: hit.PosX, posY: hit.PosY, isAvatar: hit.IsAvatar,
	}
}

func (runtime *liveCompetitiveAIRuntime) reserveHitRequest(hit game.PlayerExplodedEvent, sourceID uint16, now time.Time) {
	runtime.hitRequestMu.Lock()
	defer runtime.hitRequestMu.Unlock()
	// A quarantined actor cannot generate a second hit. Once an avatar hit has
	// been committed and play resumes, a later hit replaces its old retry marker.
	// This bounds storage by virtual participant count for the whole match.
	for key := range runtime.hitRequests {
		if key.playerID == hit.PlayerID {
			delete(runtime.hitRequests, key)
		}
	}
	runtime.hitRequests[competitiveAIHitKey(hit)] = liveCompetitiveAIHitRequest{
		sourceID: sourceID, state: competitiveAIHitPending, lastSentAt: now, attempts: 1,
	}
}

func (runtime *liveCompetitiveAIRuntime) clearHitRequest(hit game.PlayerExplodedEvent) {
	runtime.hitRequestMu.Lock()
	delete(runtime.hitRequests, competitiveAIHitKey(hit))
	runtime.hitRequestMu.Unlock()
}

func (runtime *liveCompetitiveAIRuntime) hasHitRequest(hit game.PlayerExplodedEvent, _ time.Time) bool {
	if runtime == nil {
		return false
	}
	runtime.hitRequestMu.Lock()
	defer runtime.hitRequestMu.Unlock()
	_, ok := runtime.hitRequests[competitiveAIHitKey(hit)]
	return ok
}

func (runtime *liveCompetitiveAIRuntime) projectActorHit(server *Server, event battleengine.Event) bool {
	isAvatar := event.SceneID != 0
	wireTime := competitiveAIWireTime(event.TimeMS)
	exploded := game.PlayerExplodedEvent{PlayerID: event.PlayerID, ClientTime: wireTime, PosX: uint16(event.Position.X), PosY: uint16(event.Position.Y), IsAvatar: isAvatar}
	body, err := exploded.MarshalNetworkBinary()
	if err != nil {
		server.log(logEvent{Level: "error", Event: "competitive_ai_trapped_build_failed", RoomID: fmt.Sprint(runtime.roomID), ErrorContext: err.Error()})
		return false
	}
	runtime.messageSeq[event.PlayerID]++
	payload, err := buildCompetitiveAIPeerGameplayPayload(runtime.gameID, event.PlayerID, wireTime, runtime.messageSeq[event.PlayerID], game.PlayerBeExploded, wireTime, body)
	if err != nil {
		return false
	}
	if err = runtime.runtime.SuspendVirtualActorAtHit(event.PlayerID, event.Position); err != nil {
		server.log(logEvent{Level: "warn", Event: "competitive_ai_hit_suspend_failed", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d", runtime.gameID, event.PlayerID), ErrorContext: err.Error()})
		return false
	}
	// The absent virtual Client.exe is the producer of this 0x0FA5 Type-2 scene
	// record. Every peer consumes it directly; the elected authority also mirrors
	// the identical body over reliable TCP. Reserve the exact request before the
	// UDP send so a very fast reliable response cannot race its transaction.
	runtime.reserveHitRequest(exploded, event.TargetID, time.Now())
	runtime.hitRequestMu.Lock()
	request := runtime.hitRequests[competitiveAIHitKey(exploded)]
	request.payload = payload
	runtime.hitRequests[competitiveAIHitKey(exploded)] = request
	runtime.hitRequestMu.Unlock()
	if err := runtime.sendPeerPayload(server, event.PlayerID, payload); err != nil {
		server.log(logEvent{Level: "warn", Event: "competitive_ai_trapped_peer_failed", RoomID: fmt.Sprint(runtime.roomID), ErrorContext: err.Error()})
	}
	runtime.projectActorHitMovement(server, event)
	server.log(logEvent{Level: "debug", Event: "competitive_ai_trapped_requested", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d_source_%d_time_%d_avatar_%t_pos_%d_%d", runtime.gameID, event.PlayerID, event.TargetID, wireTime, isAvatar, event.Position.X, event.Position.Y)})
	return true
}

// projectActorHitMovement preserves the actor+0x478 checkpoint set by
// FUN_005acaab and consumed by FUN_005f3810 after a local FA5.
// Both hit branches release movement and face down at the event coordinate;
// the authority audit resumes policy projection after the reliable outcome.
func (runtime *liveCompetitiveAIRuntime) projectActorHitMovement(server *Server, event battleengine.Event) {
	frame, ok := runtime.actorFrameAtLocked(event.PlayerID, event.TimeMS)
	if !ok {
		return
	}
	snapshot := runtime.runtime.EngineSnapshot()
	if snapshot == nil {
		return
	}
	projection := runtime.movement[event.PlayerID]
	projection, move := competitiveAIHitMovementSample(event, frame, projection, snapshot.Grid())
	runtime.movement[event.PlayerID] = projection
	if err := runtime.sendMovementSample(server, event.PlayerID, competitiveAIWireTime(event.TimeMS), move); err != nil {
		server.log(logEvent{Level: "warn", Event: "competitive_ai_hit_move_peer_failed", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d", runtime.gameID, event.PlayerID), ErrorContext: err.Error()})
	}
}

func (runtime *liveCompetitiveAIRuntime) projectInteractionRequest(server *Server, event battleengine.Event, schema uint16) bool {
	wireTime := competitiveAIWireTime(event.TimeMS)
	body, err := (game.PlayerInteractionEvent{
		PlayerID: event.PlayerID, ClientTime: wireTime, DestinationPlayerID: event.TargetID,
		PosX: uint16(event.Position.X), PosY: uint16(event.Position.Y),
	}).MarshalNetworkBinary()
	if err != nil {
		server.log(logEvent{Level: "error", Event: "competitive_ai_interaction_build_failed", RoomID: fmt.Sprint(runtime.roomID), ErrorContext: err.Error()})
		return false
	}
	if err = runtime.sendPeerGameplayEvent(server, event.PlayerID, wireTime, schema, wireTime, body); err != nil {
		server.log(logEvent{Level: "warn", Event: "competitive_ai_interaction_peer_failed", RoomID: fmt.Sprint(runtime.roomID), ErrorContext: err.Error()})
		return true
	}
	server.log(logEvent{Level: "debug", Event: "competitive_ai_interaction_requested", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_schema_0x%04X_source_%d_target_%d", runtime.gameID, schema, event.PlayerID, event.TargetID)})
	return true
}

func (runtime *liveCompetitiveAIRuntime) projectEliminated(server *Server, event battleengine.Event, events []battleengine.Event) {
	// EventActorEliminated means the actor is already eliminated in the fixed
	// engine. Still cross the common lifecycle barrier before publishing the
	// native death request, otherwise a pickup/hit request queued on the preceding
	// transition can survive long enough to be rendered after death.
	_ = runtime.acceptNativeEliminationLocked(event.TargetID, event.PlayerID, nil)
	battle, err := server.competitiveBattle(runtime.gameID)
	if err != nil {
		return
	}
	drops := droppedGameItems(events, event.TargetID, event.TimeMS)
	// Under native outcome authority every rescue/finish contact is now emitted
	// as a request for the real arbitrator. A direct engine elimination therefore
	// represents only the virtual actor's syrup timeout, whose native message is
	// PLAYER_DIE even when the trapping flame belonged to a teammate. Retaining
	// TrappedBy as a training credit must not turn that timeout into a PVP kill.
	wireTime := competitiveAIWireTime(event.TimeMS)
	body, marshalErr := (game.PlayerDeathEvent{PlayerID: event.TargetID, ClientTime: wireTime, PosX: uint16(event.Position.X), PosY: uint16(event.Position.Y), Items: drops}).MarshalNetworkBinary()
	if marshalErr != nil {
		server.log(logEvent{Level: "error", Event: "competitive_ai_death_build_failed", RoomID: fmt.Sprint(runtime.roomID), ErrorContext: marshalErr.Error()})
	} else if marshalErr = runtime.sendPeerGameplayEvent(server, event.TargetID, wireTime, game.NotifyPlayerDieEvent, wireTime, body); marshalErr != nil {
		server.log(logEvent{Level: "warn", Event: "competitive_ai_death_peer_failed", RoomID: fmt.Sprint(runtime.roomID), ErrorContext: marshalErr.Error()})
	}
	resolution, err := battle.RecordDeath(event.TargetID)
	if err != nil {
		server.log(logEvent{Level: "warn", Event: "competitive_ai_elimination_rejected", RoomID: fmt.Sprint(runtime.roomID), ErrorContext: err.Error()})
		return
	}
	if resolution.NewlyConcluded {
		server.concludeCompetitiveAI(runtime, competitiveAIWireTime(event.TimeMS), battle, resolution)
	}
}

func droppedGameItems(events []battleengine.Event, playerID uint16, timeMS uint32) []game.GameItem {
	items := make([]game.GameItem, 0)
	for _, related := range events {
		if related.Kind != battleengine.EventPickupDropped || related.PlayerID != playerID || related.TimeMS != timeMS ||
			related.Cell.Row < 0 || related.Cell.Row > 0xff || related.Cell.Col < 0 || related.Cell.Col > 0xff {
			continue
		}
		items = append(items, game.GameItem{ItemID: related.SceneID, Row: byte(related.Cell.Row), Col: byte(related.Cell.Col)})
	}
	return items
}

func nativeMapElementDirection(from, to battleengine.Cell) (byte, bool) {
	switch {
	case to.Row == from.Row && to.Col == from.Col+1:
		return 0, true
	case to.Row == from.Row-1 && to.Col == from.Col:
		return 1, true
	case to.Row == from.Row && to.Col == from.Col-1:
		return 2, true
	case to.Row == from.Row+1 && to.Col == from.Col:
		return 3, true
	default:
		return 0, false
	}
}

func nativeBombMoveDirection(from, to battleengine.Cell) (byte, bool) {
	switch {
	case to.Row == from.Row && to.Col > from.Col:
		return 0, true
	case to.Col == from.Col && to.Row < from.Row:
		return 1, true
	case to.Row == from.Row && to.Col < from.Col:
		return 2, true
	case to.Col == from.Col && to.Row > from.Row:
		return 3, true
	default:
		return 0, false
	}
}

func nativeAdjacentTarget(source battleengine.Cell, direction byte) (battleengine.Cell, bool) {
	switch direction {
	case 0:
		return battleengine.Cell{Row: source.Row, Col: source.Col + 1}, true
	case 1:
		return battleengine.Cell{Row: source.Row - 1, Col: source.Col}, true
	case 2:
		return battleengine.Cell{Row: source.Row, Col: source.Col - 1}, true
	case 3:
		return battleengine.Cell{Row: source.Row + 1, Col: source.Col}, true
	default:
		return battleengine.Cell{}, false
	}
}

func (runtime *liveCompetitiveAIRuntime) projectMapElementMoved(server *Server, event battleengine.Event) bool {
	direction, ok := nativeBombMoveDirection(event.FromCell, event.Cell)
	if !ok || event.ObjectID == 0 || event.FromCell.Row < 0 || event.FromCell.Col < 0 {
		return false
	}
	key := liveCompetitiveAISceneRequestKey{
		kind: competitiveAIRequestMapElement, playerID: event.PlayerID, objectID: event.ObjectID,
		fromRow: event.FromCell.Row, fromCol: event.FromCell.Col, toRow: event.Cell.Row, toCol: event.Cell.Col,
	}
	if !runtime.reserveSceneRequest(key, event.TimeMS) {
		return false
	}
	runtime.mapElemSeq++
	wireTime := competitiveAIWireTime(event.TimeMS)
	body, err := (game.MoveMapElementRequest{
		PlayerID: event.PlayerID, ClientTime: competitiveAIWireTime(event.TimeMS), ElementID: event.ObjectID,
		Row: byte(event.FromCell.Row), Col: byte(event.FromCell.Col), Direction: direction, Sequence: runtime.mapElemSeq,
	}).MarshalNetworkBinary()
	if err != nil {
		runtime.clearSceneRequest(key)
		server.log(logEvent{Level: "error", Event: "competitive_ai_map_element_build_failed", RoomID: fmt.Sprint(runtime.roomID), ErrorContext: err.Error()})
		return false
	}
	if err = runtime.sendPeerGameplayEvent(server, event.PlayerID, wireTime, game.RequestMoveMapElement, wireTime, body); err != nil {
		runtime.clearSceneRequest(key)
		server.log(logEvent{Level: "warn", Event: "competitive_ai_map_element_peer_failed", RoomID: fmt.Sprint(runtime.roomID), ErrorContext: err.Error()})
	}
	return true
}

func (runtime *liveCompetitiveAIRuntime) projectBattleAction(server *Server, event battleengine.Event) bool {
	key := liveCompetitiveAISceneRequestKey{kind: competitiveAIRequestAction, playerID: event.PlayerID, actionID: event.ActionID}
	if !runtime.reserveSceneRequest(key, event.TimeMS) {
		return false
	}
	wireTime := competitiveAIWireTime(event.TimeMS)
	use := runtime.battleActionSample(event)
	body, err := use.MarshalNetworkBinary()
	if err != nil {
		runtime.clearSceneRequest(key)
		server.log(logEvent{Level: "error", Event: "competitive_ai_action_build_failed", RoomID: fmt.Sprint(runtime.roomID), ErrorContext: err.Error()})
		return false
	}
	// REQUEST_USE_ITEM is a server-owned request/notify pair in the original
	// protocol. A virtual actor has no reliable Client.exe connection on which
	// to submit the request, so the live adapter performs the same authoritative
	// conversion here: publish 0x0FB0 to every native peer, then commit that
	// exact notification to the deterministic mirror. Sending 0x0FAF only to the
	// elected scene arbitrator leaves both clients and the AI world unchanged.
	if err = runtime.sendPeerGameplayEvent(server, event.PlayerID, wireTime, game.NotifyPlayerUseItem, wireTime, body); err != nil {
		runtime.clearSceneRequest(key)
		server.log(logEvent{Level: "warn", Event: "competitive_ai_action_peer_failed", RoomID: fmt.Sprint(runtime.roomID), ErrorContext: err.Error()})
		return true
	}
	if err = runtime.acceptNativeBattleActionLocked(server, use); err != nil {
		server.log(logEvent{Level: "error", Event: "competitive_ai_action_commit_failed", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d_item_%d", runtime.gameID, use.PlayerID, use.ItemID), ErrorContext: err.Error()})
	}
	return true
}

func (runtime *liveCompetitiveAIRuntime) battleActionSample(event battleengine.Event) game.WorldUseItemEvent {
	use := game.WorldUseItemEvent{
		PlayerID: event.PlayerID, ClientTime: competitiveAIWireTime(event.TimeMS), ItemID: uint32(event.ActionID),
		PosX: uint16(event.Position.X), PosY: uint16(event.Position.Y),
	}
	if event.ActionID == 44 || event.ActionID == 46 {
		// FUN_00610631 packs direction/power/row/column even on a miss;
		// FUN_006047a0 uses the low two bytes as the actual flight endpoint.
		use.Flag1 = uint32(nativeWalkDirection(event.ProjectileDirection))<<24 |
			uint32(uint16(event.ProjectileTargetCell.Row)&0xff)<<8 |
			uint32(uint16(event.ProjectileTargetCell.Col)&0xff)
		use.Flag2 = ^uint32(0)
		if placed, ok := runtime.bombs[event.BombID]; ok && event.BombID != 0 {
			use.Flag1 |= uint32(placed.power) << 16
			use.Flag2 = uint32(placed.ownerID)
			use.Flag3 = placed.placedAt
			use.Flag4 = 1
		}
	}
	return use
}

func (runtime *liveCompetitiveAIRuntime) acceptNativeBattleActionLocked(server *Server, use game.WorldUseItemEvent) error {
	if runtime == nil || runtime.runtime == nil {
		return fmt.Errorf("competitive AI runtime is nil")
	}
	if use.ItemID > 0xff {
		return fmt.Errorf("native battle action item %d exceeds one-byte action ID", use.ItemID)
	}
	key := liveCompetitiveAIHumanActionKey{playerID: use.PlayerID, clientTime: use.ClientTime, actionID: use.ItemID}
	if _, duplicate := runtime.humanActions[key]; duplicate {
		return nil
	}
	targetBombID := uint32(0)
	if (use.ItemID == 44 || use.ItemID == 46) && use.Flag4 != 0 && use.Flag2 <= 0xffff {
		power := byte((use.Flag1 >> 16) & 0xff)
		cell := battleengine.Cell{Row: int16((use.Flag1 >> 8) & 0xff), Col: int16(use.Flag1 & 0xff)}
		targetBombID = runtime.findNativeBombID(uint16(use.Flag2), use.Flag3, cell, power)
	}
	runtime.clearSceneRequest(liveCompetitiveAISceneRequestKey{kind: competitiveAIRequestAction, playerID: use.PlayerID, actionID: uint8(use.ItemID)})
	if _, err := runtime.runtime.AcceptNativeBattleAction(
		use.PlayerID, byte(use.ItemID), battleengine.Position{X: int32(use.PosX), Y: int32(use.PosY)}, targetBombID,
	); err != nil {
		return err
	}
	runtime.humanActions[key] = struct{}{}
	if server != nil && server.logWriter != nil {
		server.log(logEvent{Level: "debug", Event: "competitive_ai_native_action_accepted", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d_item_%d_pos_%d_%d", runtime.gameID, use.PlayerID, use.ItemID, use.PosX, use.PosY)})
	}
	return nil
}

// recordCompetitiveAINativeBattleAction mirrors one server-authored 0x0FB0
// after it has been delivered to the match. Human requests arrive on reliable
// TCP, whereas virtual actions call the locked helper directly after their
// native-peer notification is sent.
func (server *Server) recordCompetitiveAINativeBattleAction(gameID uint32, use game.WorldUseItemEvent) {
	runtime := server.competitiveAIRuntimeForGame(gameID)
	if runtime == nil {
		return
	}
	err := runRoomActor(server, runtime.roomID, "competitive-ai-native-battle-action", func() error {
		server.recordCompetitiveAINativeBattleActionOnRoomActor(runtime, use)
		return nil
	})
	if err != nil {
		server.log(logEvent{Level: "error", Event: "competitive_ai_native_action_actor_failed", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d_item_%d", gameID, use.PlayerID, use.ItemID), ErrorContext: err.Error()})
	}
}

func (server *Server) recordCompetitiveAINativeBattleActionOnRoomActor(runtime *liveCompetitiveAIRuntime, use game.WorldUseItemEvent) {
	gameID := runtime.gameID
	runtime.mu.Lock()
	err := runtime.acceptNativeBattleActionLocked(server, use)
	runtime.mu.Unlock()
	if err != nil && server.logWriter != nil {
		server.log(logEvent{Level: "warn", Event: "competitive_ai_native_action_rejected", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d_item_%d", gameID, use.PlayerID, use.ItemID), ErrorContext: err.Error()})
	}
}

func (runtime *liveCompetitiveAIRuntime) projectBombKicked(server *Server, before *battleengine.Engine, event battleengine.Event) bool {
	key := liveCompetitiveAISceneRequestKey{
		kind: competitiveAIRequestBombMove, playerID: event.PlayerID, objectID: event.BombID,
		fromRow: event.FromCell.Row, fromCol: event.FromCell.Col, toRow: event.Cell.Row, toCol: event.Cell.Col,
	}
	if !runtime.reserveSceneRequest(key, event.TimeMS) {
		return false
	}
	placed, ok := runtime.bombs[event.BombID]
	if !ok {
		bomb, found := bombByID(before.Bombs(), event.BombID)
		if !found {
			runtime.clearSceneRequest(key)
			return false
		}
		placed = liveCompetitiveAIBomb{
			ownerID: bomb.OwnerID, placedAt: competitiveAIWireTime(bomb.ExplodeAtMS - battleengine.NativeBombFuseMS - 1),
			position: event.FromCell, appearance: competitiveAIDefaultBombAppearance, power: bomb.Power + 1,
		}
	}
	// Panda throws may land up to eight cells away; map-element pushes
	// accept only adjacent destinations and would discard these requests.
	direction, ok := nativeBombMoveDirection(event.FromCell, event.Cell)
	if !ok {
		runtime.clearSceneRequest(key)
		return false
	}
	body, err := (game.MoveBombEvent{
		PlayerID: event.PlayerID, ClientTime: uint16(competitiveAIWireTime(event.TimeMS)),
		FromRow: uint16(event.FromCell.Row), FromCol: uint16(event.FromCell.Col),
		ToRow: uint16(event.Cell.Row), ToCol: uint16(event.Cell.Col), Direction: direction,
		BombPlayerID: placed.ownerID, BombTime: placed.placedAt, BombPower: placed.power,
	}).MarshalNetworkBinary()
	if err != nil {
		runtime.clearSceneRequest(key)
		server.log(logEvent{Level: "error", Event: "competitive_ai_bomb_kick_build_failed", RoomID: fmt.Sprint(runtime.roomID), ErrorContext: err.Error()})
		return false
	}
	wireTime := competitiveAIWireTime(event.TimeMS)
	if err = runtime.sendPeerGameplayEvent(server, event.PlayerID, wireTime, game.RequestMoveBomb, wireTime, body); err != nil {
		runtime.clearSceneRequest(key)
		server.log(logEvent{Level: "warn", Event: "competitive_ai_bomb_kick_peer_failed", RoomID: fmt.Sprint(runtime.roomID), ErrorContext: err.Error()})
	}
	return true
}

func (server *Server) concludeCompetitiveAI(runtime *liveCompetitiveAIRuntime, clientTime uint32, battle *match.CompetitiveBattle, resolution match.CompetitiveResolution) {
	if runtime == nil || battle == nil || !resolution.NewlyConcluded {
		return
	}
	runtime.stopRuntime()
	gameOver := competitiveGameOverData(clientTime, resolution)
	server.runAfter(competitiveNativeConclusionDelay, func() {
		err := runRoomActor(server, runtime.roomID, "competitive-ai-conclusion", func() error {
			server.completeCompetitiveAIConclusionOnRoomActor(runtime, gameOver, battle, resolution)
			return nil
		})
		if err != nil {
			server.log(logEvent{Level: "error", Event: "competitive_ai_completion_actor_failed", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d", runtime.gameID), ErrorContext: err.Error()})
		}
	})
}

func (server *Server) completeCompetitiveAIConclusionOnRoomActor(runtime *liveCompetitiveAIRuntime, gameOver game.GameOverData, battle *match.CompetitiveBattle, resolution match.CompetitiveResolution) {
	if runtime == nil || battle == nil {
		return
	}
	members := server.liveRoomMatchSessions(runtime.roomID, runtime.gameID)
	if len(members) == 0 {
		server.removeCompetitiveBattle(runtime.gameID)
		return
	}
	for _, member := range members {
		template := member.packetTemplate()
		if len(template) == 0 {
			continue
		}
		packet, err := game.BuildLocalGameOverPushForRecipient(template, runtime.roomID, server.nextGameDataSequence(), gameOver)
		if err != nil {
			continue
		}
		server.writeTCP(member.connection, member.connectionID, member.localAddress, member.remoteAddress, packet, fmt.Sprintf("qqt_competitive_ai_game_over_winner_team_%d", resolution.WinnerTeamID))
	}
	actor := members[0]
	if err := server.completeCompetitiveRoomMatchOnRoomActor(actor, actor.connectionID, competitiveRoomSettlementCommit{GameOver: gameOver, Battle: battle}); err != nil {
		server.log(logEvent{Level: "error", Event: "competitive_ai_completion_failed", ConnectionID: actor.connectionID, RoomID: fmt.Sprint(runtime.roomID), ErrorContext: err.Error()})
	}
}

// recordCompetitiveAIHumanPackages mirrors only authenticated absolute native
// Type-2 facts into the virtual engine. The reliable 0x0065 upload is only an
// audit/accounting mirror and must not reach this boundary first: doing so can
// advance the Go actor before peers consume the preceding Type-2 scene state.
func (server *Server) recordCompetitiveAIHumanPackages(gameID uint32, packages []game.GameplayDataPackage) {
	runtime := server.competitiveAIRuntimeForGame(gameID)
	if runtime == nil {
		return
	}
	err := runRoomActor(server, runtime.roomID, "competitive-ai-native-packages", func() error {
		server.recordCompetitiveAIHumanPackagesOnRoomActor(runtime, packages)
		return nil
	})
	if err != nil {
		server.log(logEvent{Level: "error", Event: "competitive_ai_native_packages_failed", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d", gameID), ErrorContext: err.Error()})
	}
}

func (server *Server) recordCompetitiveAIHumanPackagesOnRoomActor(runtime *liveCompetitiveAIRuntime, packages []game.GameplayDataPackage) {
	if runtime == nil {
		return
	}
	gameID := runtime.gameID
	packages = runtime.filterNativeInbound(packages)
	if len(packages) == 0 {
		return
	}
	battle, err := server.competitiveBattle(gameID)
	if err != nil {
		return
	}
	arbitratorID := battle.ArbitratorPlayerID()
	runtime.mu.Lock()
	observedAt := time.Now()
	recordVirtualFrame := false
	for _, packet := range packages {
		if _, human := runtime.humanIDs[packet.PlayerID]; !human {
			continue
		}
		for _, message := range packet.Messages {
			if message.DataID > 0xffff {
				continue
			}
			schema := uint16(message.DataID)
			if competitiveAIArbitratorNotification(schema) && packet.PlayerID != arbitratorID {
				if server.logWriter != nil {
					server.log(logEvent{
						Level: "warn", Event: "competitive_ai_native_notification_rejected", RoomID: fmt.Sprint(runtime.roomID),
						Result: fmt.Sprintf("game_%d_schema_%04X_sender_%d_arbitrator_%d", runtime.gameID, schema, packet.PlayerID, arbitratorID),
					})
				}
				continue
			}
			// Preserve the authenticated stream order. Advance only to THIS
			// event, apply it, then let the next event advance the changed world.
			// Packet.Time is a send clock, not the occurrence of every old fact
			// bundled into the packet.
			if packet.PlayerID == arbitratorID {
				runtime.observeAuthorityClockLocked(packet.Time, observedAt)
				runtime.observeAuthorityClockLocked(message.Time, observedAt)
				eventTime := competitiveAIEventClock(message)
				if competitiveAICausalAuthorityMessage(schema) && eventTime >= battleengine.NativeRoundStartClockMS && eventTime <= battleengine.StandardRoundTimeMS {
					target := competitiveAICompletedSceneFrameMS(eventTime, uint32(runtime.worldTick/time.Millisecond))
					if snapshot := runtime.runtime.EngineSnapshot(); snapshot != nil && snapshot.ElapsedMS() > target {
						server.log(logEvent{Level: "debug", Event: "competitive_ai_late_authority_fact", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_schema_%04X_event_%d_scene_%d", gameID, schema, target, snapshot.ElapsedMS())})
					}
					if err := runtime.advanceToLocked(server, target, 0); err != nil {
						runtime.mu.Unlock()
						server.log(logEvent{Level: "error", Event: "competitive_ai_authority_catchup_failed", RoomID: fmt.Sprint(runtime.roomID), ErrorContext: err.Error()})
						runtime.stopRuntime()
						return
					}
					if runtime.stopped() {
						runtime.mu.Unlock()
						return
					}
				}
			}
			if schema != game.PlayerMoveSchema {
				recordVirtualFrame = true
			}
			switch schema {
			case game.PlayerMoveSchema:
				movement, err := game.ParsePlayerMove(message.Data)
				if err != nil || movement.PlayerID != packet.PlayerID {
					continue
				}
				for _, entry := range movement.Entries {
					if entry.PlayerID != packet.PlayerID {
						continue
					}
					move := entry.Move
					if err := runtime.runtime.ReconcileHumanMovementPath(entry.PlayerID, battleengine.NativeHumanMovement{
						Sequence: move.Sequence, TimeMS: move.TimeStamp,
						Current:   battleengine.Position{X: int32(move.CurrentPosX), Y: int32(move.CurrentPosY)},
						Corner:    battleengine.Position{X: int32(move.CornerPosX), Y: int32(move.CornerPosY)},
						End:       battleengine.Position{X: int32(move.EndPosX), Y: int32(move.EndPosY)},
						Direction: battleDirection(move.WalkAndDirection), Speed: move.Speed,
						Moving: move.WalkAndDirection&0x10 != 0, Forced: move.WalkAndDirection&0x20 != 0,
					}); err != nil {
						server.log(logEvent{Level: "warn", Event: "competitive_ai_human_path_rejected", RoomID: fmt.Sprint(runtime.roomID), ErrorContext: err.Error()})
					}
				}
			case game.PlayerUseBomb:
				placed, err := game.ParsePlayerUseBombEvent(game.GameEvent{Schema: game.PlayerUseBomb, Body: message.Data})
				if err != nil {
					continue
				}
				if placed.PlayerID != packet.PlayerID {
					continue
				}
				key := liveCompetitiveAIHumanBombKey{playerID: placed.PlayerID, clientTime: placed.ClientTime, row: placed.Row, column: placed.Column}
				if _, duplicate := runtime.humanBombs[key]; duplicate {
					continue
				}
				// The deterministic engine and native ClientTime share the first
				// controllable clock (3000 ms). Clamp malformed pre-round samples to
				// that boundary while preserving the wire timestamp for identity.
				enginePlacedAt := competitiveAIEngineTime(placed.ClientTime)
				if placedEvent, acceptErr := runtime.runtime.AcceptHumanBombPlacement(placed.PlayerID, battleengine.Cell{Row: int16(placed.Row), Col: int16(placed.Column)}, placed.Power, placed.Property, enginePlacedAt); acceptErr == nil {
					runtime.humanBombs[key] = struct{}{}
					runtime.bombs[placedEvent.BombID] = liveCompetitiveAIBomb{
						ownerID: placed.PlayerID, placedAt: placed.ClientTime,
						position:   battleengine.Cell{Row: int16(placed.Row), Col: int16(placed.Column)},
						appearance: placed.BombID, power: byte(placed.Power),
					}
					server.log(logEvent{
						Level: "debug", Event: "competitive_ai_human_bomb_accepted", RoomID: fmt.Sprint(runtime.roomID),
						Result: fmt.Sprintf("game_%d_player_%d_cell_%d_%d_power_%d_property_%d_wire_%d_engine_%d", runtime.gameID, placed.PlayerID, placed.Row, placed.Column, placed.Power, placed.Property, placed.ClientTime, enginePlacedAt),
					})
				} else {
					server.log(logEvent{
						Level: "warn", Event: "competitive_ai_human_bomb_rejected", RoomID: fmt.Sprint(runtime.roomID),
						Result:       fmt.Sprintf("game_%d_player_%d_cell_%d_%d_wire_%d_engine_%d", runtime.gameID, placed.PlayerID, placed.Row, placed.Column, placed.ClientTime, enginePlacedAt),
						ErrorContext: acceptErr.Error(),
					})
				}
			case game.RequestGetItem:
				// Request is intent only. The arbitrator-confirmed 0x0FAD below is
				// the sole state transition for both human and virtual actors.
				continue
			case game.NotifyPlayerGetItem:
				item, err := game.ParsePlayerItemEvent(game.GameEvent{Schema: schema, Body: message.Data})
				if err != nil {
					continue
				}
				position := battleengine.Position{X: int32(item.PosX), Y: int32(item.PosY)}
				if item.PlayerID == 0xffff {
					request, matched := runtime.takeRejectedPickupRequest(item.ItemID, item.ClientTime, position)
					result := fmt.Sprintf("game_%d_item_%d_time_%d_pos_%d_%d", runtime.gameID, item.ItemID, item.ClientTime, item.PosX, item.PosY)
					if matched {
						result = fmt.Sprintf("game_%d_player_%d_item_%d_time_%d_pos_%d_%d", runtime.gameID, request.playerID, item.ItemID, item.ClientTime, item.PosX, item.PosY)
					}
					if server.logWriter != nil {
						server.log(logEvent{Level: "debug", Event: "competitive_ai_native_pickup_rejected", RoomID: fmt.Sprint(runtime.roomID), Result: result})
					}
					continue
				}
				key := [3]uint32{uint32(item.PlayerID), item.ClientTime, item.ItemID}
				if _, duplicate := runtime.humanPickups[key]; duplicate {
					continue
				}
				if item.ItemID == 42 || item.ItemID == 43 {
					var fieldEvents []battleengine.Event
					if fieldEvents, err = runtime.runtime.AcceptNativeFieldObjectContact(item.PlayerID, uint8(item.ItemID), position); err == nil {
						runtime.clearPickupRequest(item.PlayerID, item.ItemID, position)
						runtime.humanPickups[key] = struct{}{}
						server.log(logEvent{Level: "debug", Event: "competitive_ai_native_field_contact_accepted", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d_action_%d_pos_%d_%d_events_%d", runtime.gameID, item.PlayerID, item.ItemID, item.PosX, item.PosY, len(fieldEvents))})
					} else {
						server.log(logEvent{Level: "warn", Event: "competitive_ai_native_field_contact_rejected", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d_action_%d_pos_%d_%d", runtime.gameID, item.PlayerID, item.ItemID, item.PosX, item.PosY), ErrorContext: err.Error()})
					}
					continue
				}
				var pickupEvent battleengine.Event
				if pickupEvent, err = runtime.runtime.AcceptNativePickup(item.PlayerID, item.ItemID, position); err == nil {
					// The accepted scene object is gone for every participant, not only
					// for the winner of a same-cell pickup race.
					runtime.clearPickupRequestsForObjects([]battleengine.Pickup{{SceneID: item.ItemID, Cell: pickupEvent.Cell}})
					runtime.humanPickups[key] = struct{}{}
					server.log(logEvent{Level: "debug", Event: "competitive_ai_native_pickup_accepted", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d_item_%d_pos_%d_%d", runtime.gameID, item.PlayerID, item.ItemID, item.PosX, item.PosY)})
				} else {
					server.log(logEvent{Level: "warn", Event: "competitive_ai_native_pickup_rejected", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d_item_%d_pos_%d_%d", runtime.gameID, item.PlayerID, item.ItemID, item.PosX, item.PosY), ErrorContext: err.Error()})
				}
			case game.PlayerBeExploded:
				// A human actor's own Type-2 0x0FA5 remains an authenticated absolute
				// observation for the Go mirror. A virtual target is different: this
				// server authored its Type-2 request, and only the authority's exact
				// reliable 0x0FA5 audit may commit that pending transaction.
				hit, err := game.ParsePlayerExplodedEvent(game.GameEvent{Schema: schema, Body: message.Data})
				if err != nil {
					continue
				}
				_, virtualTarget := runtime.virtualIDs[hit.PlayerID]
				_, humanTarget := runtime.humanIDs[hit.PlayerID]
				if (!virtualTarget && !humanTarget) || (packet.PlayerID != hit.PlayerID && packet.PlayerID != arbitratorID) {
					continue
				}
				if virtualTarget {
					continue
				}
				_, changed, acceptErr := runtime.runtime.AcceptNativeActorHitFrom(
					hit.PlayerID, 0,
					battleengine.Position{X: int32(hit.PosX), Y: int32(hit.PosY)},
					hit.IsAvatar,
				)
				if acceptErr != nil {
					if server.logWriter != nil {
						server.log(logEvent{Level: "warn", Event: "competitive_ai_native_hit_request_rejected", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d_reporter_%d_avatar_%t_pos_%d_%d", runtime.gameID, hit.PlayerID, packet.PlayerID, hit.IsAvatar, hit.PosX, hit.PosY), ErrorContext: acceptErr.Error()})
					}
					continue
				}
				if changed && !hit.IsAvatar {
					if battle, battleErr := server.competitiveBattle(runtime.gameID); battleErr == nil {
						_ = battle.RecordTrapped(hit.PlayerID)
					}
				}
				if server.logWriter != nil {
					server.log(logEvent{Level: "debug", Event: "competitive_ai_native_hit_request_accepted", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d_reporter_%d_avatar_%t_changed_%t_pos_%d_%d", runtime.gameID, hit.PlayerID, packet.PlayerID, hit.IsAvatar, changed, hit.PosX, hit.PosY)})
				}
			case game.NotifyPlayerExploded:
				event := game.GameEvent{Schema: schema, Body: message.Data}
				hit, err := game.ParsePlayerExplodedNotification(event)
				if err != nil {
					if server.logWriter == nil {
						continue
					}
					server.log(logEvent{Level: "warn", Event: "competitive_ai_native_hit_decode_failed", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_schema_%04X", runtime.gameID, schema), ErrorContext: err.Error()})
					continue
				}
				if _, virtualTarget := runtime.virtualIDs[hit.PlayerID]; virtualTarget {
					// This client build has no live 0x0FA6 actor-hit consumer. Retain
					// parsing only for legacy human/NPC compatibility; virtual state is
					// owned by the exact 0x0FA5 request/audit transaction above.
					continue
				}
				_, changed, acceptErr := runtime.runtime.AcceptNativeActorHitFrom(hit.PlayerID, 0, battleengine.Position{X: int32(hit.PosX), Y: int32(hit.PosY)}, hit.IsAvatar)
				if acceptErr != nil && server.logWriter != nil {
					server.log(logEvent{Level: "warn", Event: "competitive_ai_native_hit_rejected", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_schema_%04X_player_%d_avatar_%t_pos_%d_%d", runtime.gameID, schema, hit.PlayerID, hit.IsAvatar, hit.PosX, hit.PosY), ErrorContext: acceptErr.Error()})
				} else if server.logWriter != nil {
					server.log(logEvent{Level: "debug", Event: "competitive_ai_native_hit_accepted", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_schema_%04X_player_%d_avatar_%t_changed_%t_pos_%d_%d", runtime.gameID, schema, hit.PlayerID, hit.IsAvatar, changed, hit.PosX, hit.PosY)})
				}
				if changed && !hit.IsAvatar {
					if battle, battleErr := server.competitiveBattle(runtime.gameID); battleErr == nil {
						_ = battle.RecordTrapped(hit.PlayerID)
					}
				}
			case game.NotifyPlayerSaved:
				rescued, err := game.ParsePlayerInteractionEvent(game.GameEvent{Schema: schema, Body: message.Data})
				if err == nil {
					_, changed, _ := runtime.runtime.AcceptNativeRescue(rescued.PlayerID, rescued.DestinationPlayerID, battleengine.Position{X: int32(rescued.PosX), Y: int32(rescued.PosY)})
					if changed {
						if battle, battleErr := server.competitiveBattle(runtime.gameID); battleErr == nil {
							_ = battle.RecordRescue(rescued.PlayerID, rescued.DestinationPlayerID)
						}
					}
				}
			case game.NotifyBombExplode:
				explosion, err := game.ParseBombExplodeEvent(game.GameEvent{Schema: schema, Body: message.Data})
				if err != nil {
					continue
				}
				explosionKey := [3]uint32{uint32(explosion.PlayerID), explosion.ClientTime, message.Sequence}
				if _, duplicate := runtime.nativeExplosions[explosionKey]; duplicate {
					continue
				}
				verifiedBombs := make([]battleengine.VerifiedExplodedBomb, 0, len(explosion.Bombs))
				for _, bomb := range explosion.Bombs {
					cell := battleengine.Cell{Row: int16(bomb.Row), Col: int16(bomb.Column)}
					bombID := runtime.findNativeBombID(bomb.PlayerID, bomb.ClientTime, cell, 0)
					verifiedBombs = append(verifiedBombs, battleengine.VerifiedExplodedBomb{
						BombID: bombID, OwnerID: bomb.PlayerID, Cell: cell,
						BlastRowMin: int16(bomb.RowMin), BlastRowMax: int16(bomb.RowMax),
						BlastColMin: int16(bomb.ColumnMin), BlastColMax: int16(bomb.ColumnMax),
					})
				}
				mapHits := make([]battleengine.VerifiedMapElementHit, 0, len(explosion.MapElems))
				for _, element := range explosion.MapElems {
					mapHits = append(mapHits, battleengine.VerifiedMapElementHit{MapElementID: uint32(element.MapElementID), Cell: battleengine.Cell{Row: int16(element.Row), Col: int16(element.Column)}})
				}
				destroyedItems := make([]battleengine.Pickup, 0, len(explosion.Items))
				for _, item := range explosion.Items {
					destroyedItems = append(destroyedItems, battleengine.Pickup{SceneID: item.ItemID, Cell: battleengine.Cell{Row: int16(item.Row), Col: int16(item.Column)}, State: battleengine.PickupAvailable})
				}
				beforeExplosion := runtime.runtime.EngineSnapshot()
				nativeEvents, acceptErr := runtime.runtime.AcceptNativeBombExplosion(verifiedBombs, mapHits, destroyedItems)
				if acceptErr == nil {
					nativeEvents = append(nativeEvents, runtime.reconcileVirtualExplosionHits(verifiedBombs, explosion.ClientTime)...)
				}
				afterExplosion := runtime.runtime.EngineSnapshot()
				if acceptErr != nil {
					server.log(logEvent{Level: "warn", Event: "competitive_ai_native_explosion_rejected", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d_bombs_%d_walls_%d_destroyed_items_%d", runtime.gameID, explosion.PlayerID, len(verifiedBombs), len(mapHits), len(destroyedItems)), ErrorContext: acceptErr.Error()})
					continue
				}
				runtime.clearPickupRequestsForObjects(destroyedItems)
				// The arbitrator's 0x0FA4 already rendered the explosion and map
				// changes. projectEvents ignores those duplicated facts and emits
				// only lifecycle requests that an absent virtual Client.exe would
				// normally have authored for itself.
				runtime.projectEvents(server, beforeExplosion, afterExplosion, nativeEvents)
				runtime.nativeExplosions[explosionKey] = struct{}{}
				for _, bomb := range verifiedBombs {
					if bomb.BombID != 0 {
						delete(runtime.bombs, bomb.BombID)
					}
				}
				bounds := ""
				for _, bomb := range verifiedBombs {
					bounds += fmt.Sprintf("_bomb_%d_owner_%d_cell_%d_%d_rows_%d_%d_cols_%d_%d", bomb.BombID, bomb.OwnerID, bomb.Cell.Row, bomb.Cell.Col, bomb.BlastRowMin, bomb.BlastRowMax, bomb.BlastColMin, bomb.BlastColMax)
				}
				server.log(logEvent{Level: "debug", Event: "competitive_ai_native_explosion_accepted", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_arbitrator_%d_time_%d_engine_%d_bombs_%d_walls_%d_destroyed_items_%d%s", runtime.gameID, explosion.PlayerID, explosion.ClientTime, afterExplosion.ElapsedMS(), len(verifiedBombs), len(mapHits), len(destroyedItems), bounds)})
			case game.NotifyDispatchItem:
				dispatch, err := game.ParseDispatchItemEvent(game.GameEvent{Schema: schema, Body: message.Data})
				if err != nil {
					continue
				}
				key := [3]uint32{uint32(packet.PlayerID), dispatch.Time, message.Sequence}
				if _, duplicate := runtime.nativeDispatches[key]; duplicate {
					continue
				}
				pickups := battlePickupsFromDispatch(dispatch, runtime.mapID)
				if acceptErr := runtime.runtime.AcceptNativePickupDispatch(dispatch.Time, pickups); acceptErr != nil {
					server.log(logEvent{Level: "warn", Event: "competitive_ai_native_item_dispatch_rejected", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d_time_%d_items_%d_delayed_%d", runtime.gameID, packet.PlayerID, dispatch.Time, len(dispatch.Items), len(dispatch.DelayedItems)), ErrorContext: acceptErr.Error()})
					continue
				}
				// A dispatched object starts a new scene lifetime even when it reuses
				// the same SceneID/cell as a previously rejected contact.
				runtime.clearPickupRequestsForObjects(pickups)
				runtime.nativeDispatches[key] = struct{}{}
				server.log(logEvent{Level: "debug", Event: "competitive_ai_native_item_dispatch_accepted", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d_time_%d_items_%d_delayed_%d", runtime.gameID, packet.PlayerID, dispatch.Time, len(dispatch.Items), len(dispatch.DelayedItems))})
			case game.NotifyItemExploded:
				destroyed, err := game.ParseItemsExplodedEvent(game.GameEvent{Schema: schema, Body: message.Data})
				if err != nil || destroyed.PlayerID != packet.PlayerID {
					continue
				}
				key := [3]uint32{uint32(packet.PlayerID), destroyed.Time, message.Sequence}
				if _, duplicate := runtime.nativeItemExplodes[key]; duplicate {
					continue
				}
				items := battlePickupsFromGameItems(destroyed.Items)
				if acceptErr := runtime.runtime.AcceptNativeItemDestruction(items); acceptErr != nil {
					server.log(logEvent{Level: "warn", Event: "competitive_ai_native_item_explosion_rejected", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d_time_%d_items_%d", runtime.gameID, packet.PlayerID, destroyed.Time, len(items)), ErrorContext: acceptErr.Error()})
					continue
				}
				runtime.clearPickupRequestsForObjects(items)
				runtime.nativeItemExplodes[key] = struct{}{}
				server.log(logEvent{Level: "debug", Event: "competitive_ai_native_item_explosion_accepted", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d_time_%d_items_%d", runtime.gameID, packet.PlayerID, destroyed.Time, len(items))})
			case game.NotifyPlayerDieEvent:
				death, err := game.ParsePlayerDeathEvent(game.GameEvent{Schema: schema, Body: message.Data})
				if err != nil {
					continue
				}
				_, virtualTarget := runtime.virtualIDs[death.PlayerID]
				if death.PlayerID == packet.PlayerID || (packet.PlayerID == arbitratorID && virtualTarget) {
					acceptErr := runtime.acceptNativeEliminationLocked(death.PlayerID, 0, battlePickupsFromGameItems(death.Items))
					if acceptErr != nil {
						server.log(logEvent{Level: "warn", Event: "competitive_ai_native_death_rejected", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d_items_%d", runtime.gameID, death.PlayerID, len(death.Items)), ErrorContext: acceptErr.Error()})
					} else {
						server.log(logEvent{Level: "debug", Event: "competitive_ai_native_death_accepted", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d_items_%d", runtime.gameID, death.PlayerID, len(death.Items))})
					}
				}
			case game.NotifyPlayerKilled:
				killed, err := game.ParsePlayerKilledEvent(game.GameEvent{Schema: schema, Body: message.Data})
				if err == nil {
					acceptErr := runtime.acceptNativeEliminationLocked(killed.DestinationPlayerID, killed.PlayerID, battlePickupsFromGameItems(killed.Items))
					if acceptErr != nil {
						server.log(logEvent{Level: "warn", Event: "competitive_ai_native_kill_rejected", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_source_%d_target_%d_items_%d", runtime.gameID, killed.PlayerID, killed.DestinationPlayerID, len(killed.Items)), ErrorContext: acceptErr.Error()})
					} else {
						server.log(logEvent{Level: "debug", Event: "competitive_ai_native_kill_accepted", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_source_%d_target_%d_items_%d", runtime.gameID, killed.PlayerID, killed.DestinationPlayerID, len(killed.Items))})
					}
				}
			case game.NotifyMapElementMoved:
				moved, err := game.ParseMapElementMovedEvent(game.GameEvent{Schema: schema, Body: message.Data})
				if err == nil {
					source := battleengine.Cell{Row: int16(moved.Row), Col: int16(moved.Col)}
					target, validDirection := nativeAdjacentTarget(source, moved.Direction)
					if validDirection {
						runtime.clearSceneRequest(liveCompetitiveAISceneRequestKey{
							kind: competitiveAIRequestMapElement, playerID: moved.PlayerID, objectID: moved.ElementID,
							fromRow: source.Row, fromCol: source.Col, toRow: target.Row, toCol: target.Col,
						})
					}
					_, moveErr := runtime.runtime.AcceptNativeMapElementMovement(
						moved.PlayerID, moved.ElementID, source,
						battleDirection(moved.Direction),
					)
					if moveErr != nil && server.logWriter != nil {
						server.log(logEvent{Level: "warn", Event: "competitive_ai_native_map_move_rejected", RoomID: fmt.Sprint(runtime.roomID), Result: fmt.Sprintf("game_%d_player_%d_element_%d_source_%d_%d_direction_%d", runtime.gameID, moved.PlayerID, moved.ElementID, source.Row, source.Col, moved.Direction), ErrorContext: moveErr.Error()})
					}
				}
			case game.RequestUseItem:
				continue
			case game.NotifyPlayerUseItem:
				// 0x0FB0 is authored by the centre server after reliable
				// 0x0FAF validation. The reliable dispatcher commits that exact
				// notification after broadcasting it; a peer-authored Type-2 copy
				// is neither an authority source nor a second state transition.
				continue
			case game.RequestMoveBomb:
				continue
			case game.NotifyPlayerMoveBomb:
				moved, err := game.ParseMoveBombEvent(game.GameEvent{Schema: schema, Body: message.Data})
				if err != nil {
					continue
				}
				key := liveCompetitiveAIHumanMoveBombKey{
					playerID: moved.PlayerID, clientTime: moved.ClientTime, ownerID: moved.BombPlayerID, bombTime: moved.BombTime,
					fromRow: moved.FromRow, fromCol: moved.FromCol, toRow: moved.ToRow, toCol: moved.ToCol,
				}
				if _, duplicate := runtime.humanKicks[key]; duplicate {
					continue
				}
				source := battleengine.Cell{Row: int16(moved.FromRow), Col: int16(moved.FromCol)}
				target := battleengine.Cell{Row: int16(moved.ToRow), Col: int16(moved.ToCol)}
				bombID := runtime.findNativeBombID(moved.BombPlayerID, moved.BombTime, source, moved.BombPower)
				if bombID == 0 {
					continue
				}
				runtime.clearSceneRequest(liveCompetitiveAISceneRequestKey{
					kind: competitiveAIRequestBombMove, playerID: moved.PlayerID, objectID: bombID,
					fromRow: source.Row, fromCol: source.Col, toRow: target.Row, toCol: target.Col,
				})
				if _, acceptErr := runtime.runtime.AcceptNativeBombMovement(moved.PlayerID, bombID, source, target); acceptErr == nil {
					placed := runtime.bombs[bombID]
					placed.position = target
					runtime.bombs[bombID] = placed
					runtime.humanKicks[key] = struct{}{}
				}
			case game.NotifyRecoverAvatar:
				recovery, err := game.ParseAvatarRecoveryEvent(game.GameEvent{Schema: schema, Body: message.Data})
				if err != nil {
					continue
				}
				if _, human := runtime.humanIDs[recovery.PlayerID]; human {
					_, _, _ = runtime.runtime.AcceptNativeTransformationRecovery(recovery.PlayerID, battleengine.Position{X: int32(recovery.PosX), Y: int32(recovery.PosY)})
				} else if _, virtual := runtime.virtualIDs[recovery.PlayerID]; virtual {
					_, _, _ = runtime.runtime.AcceptNativeTransformationRecovery(recovery.PlayerID, battleengine.Position{})
				}
			}
		}
	}
	if recordVirtualFrame {
		runtime.recordPositionFrameLocked(runtime.runtime.EngineSnapshot())
	}
	runtime.mu.Unlock()
}

func (runtime *liveCompetitiveAIRuntime) findNativeBombID(ownerID uint16, placedAt uint32, cell battleengine.Cell, power byte) uint32 {
	for bombID, bomb := range runtime.bombs {
		if bomb.ownerID == ownerID && bomb.placedAt == placedAt && bomb.position == cell && (power == 0 || bomb.power == power) {
			return bombID
		}
	}
	return 0
}

func battlePickupsFromGameItems(items []game.GameItem) []battleengine.Pickup {
	drops := make([]battleengine.Pickup, 0, len(items))
	for _, item := range items {
		drops = append(drops, battleengine.Pickup{
			SceneID: item.ItemID,
			Cell:    battleengine.Cell{Row: int16(item.Row), Col: int16(item.Col)},
			State:   battleengine.PickupAvailable,
		})
	}
	return drops
}

// battlePickupsFromDispatch mirrors Client+0x203F01: the receiving client
// combines both QQT_GAME_ITEM and ITEM_FROM_SERVER coordinate vectors before
// starting one bird pass. The battle engine retains them as non-observable
// dispatch targets until the bird and per-object landing state have completed.
func battlePickupsFromDispatch(dispatch game.DispatchItemData, mapIDs ...uint32) []battleengine.Pickup {
	noTransformation := false
	if len(mapIDs) != 0 {
		switch mapIDs[0] {
		case 315, 316, 1011, 1012:
			noTransformation = true
		}
	}
	drops := make([]battleengine.Pickup, 0, battleengine.NativePickupDispatchMaximum)
	for _, item := range dispatch.Items {
		// 00603f01 filters only immediate entries on these native maps.
		if noTransformation && item.ItemID >= 101 && item.ItemID <= 115 {
			continue
		}
		drops = append(drops, battleengine.Pickup{SceneID: item.ItemID,
			Cell: battleengine.Cell{Row: int16(item.Row), Col: int16(item.Col)}, State: battleengine.PickupAvailable})
	}
	for _, item := range dispatch.DelayedItems {
		drops = append(drops, battleengine.Pickup{
			SceneID: item.ItemID,
			Cell:    battleengine.Cell{Row: int16(item.Row), Col: int16(item.Col)},
			State:   battleengine.PickupAvailable,
		})
	}
	if len(drops) > battleengine.NativePickupDispatchMaximum {
		drops = drops[:battleengine.NativePickupDispatchMaximum]
	}
	return drops
}
