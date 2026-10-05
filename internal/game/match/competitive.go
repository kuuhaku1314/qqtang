package match

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// CompetitiveParticipant is the immutable player projection captured when a
// normal competitive round starts. Point is ordinary competitive experience;
// adventure ExtPoint deliberately does not enter this state machine.
type CompetitiveParticipant struct {
	PlayerID  uint16
	RoleID    byte
	TeamID    byte
	DelayTime uint32
	Point     uint32
	Source    CompetitiveParticipantSource
}

// CompetitiveParticipantSource separates a connected room member from a
// server-simulated avatar. Zero is deliberately the existing human default so
// every current room projection and persisted test fixture keeps its meaning.
type CompetitiveParticipantSource byte

const (
	CompetitiveParticipantHuman CompetitiveParticipantSource = iota
	CompetitiveParticipantVirtualAI
)

// CanArbitrate reports whether the participant may own the original client's
// shared-scene rule object. A virtual AI has no client connection and can
// therefore never become the native arbitrator, even when its PlayerID is the
// smallest remaining ID.
func (participant CompetitiveParticipant) CanArbitrate() bool {
	return participant.Source == CompetitiveParticipantHuman
}

type CompetitivePlayerResult struct {
	Participant    CompetitiveParticipant
	Won            bool
	Draw           bool
	KillCount      uint32
	RescueCount    uint32
	ObjectiveCount uint32
}

type CompetitivePlayerStatistics struct {
	KillCount   uint32
	RescueCount uint32
}

type CompetitiveResolution struct {
	NewlyConcluded bool
	WinnerTeamID   byte
	// ArbitratorPlayerID is the remaining native shared-scene owner while the
	// round continues. It is zero after conclusion, when no client should keep
	// producing rule-owned NPC, timer, bubble, or objective notifications.
	ArbitratorPlayerID uint16
	// AllPlayersLost distinguishes a cooperative encounter in which every
	// player was eliminated from an ordinary no-winner draw.  The wire result
	// table must mark every participant as a loss in this case.
	AllPlayersLost bool
	Results        []CompetitivePlayerResult
}

// CompetitiveConclusionPolicy defines who decides when a competitive round
// ends. Ordinary syrup-elimination maps can be concluded authoritatively by
// the server once one team remains. Other native map rules have distinct
// objectives (kick-bomb, buns, treasure, sculpture, machine, boxes, tanks),
// so their original client rule object supplies the final GAME_OVER while the
// server still validates participants and persists the result.
type CompetitiveConclusionPolicy byte

const (
	CompetitiveConclusionOrdinaryElimination CompetitiveConclusionPolicy = iota
	CompetitiveConclusionClientRule
)

// CompetitivePlayerLifecycle controls whether a defeated avatar remains an
// active participant. Timed-respawn modes (wrestle, bun, sculpture, treasure
// and tank) and native-durability modes must not be reduced to the permanent
// elimination semantics used by ordinary maps.
type CompetitivePlayerLifecycle byte

const (
	CompetitivePlayerPermanentElimination CompetitivePlayerLifecycle = iota
	CompetitivePlayerTimedRespawn
	CompetitivePlayerNativeDurability
)

// CompetitiveObjectiveKind selects the small piece of state that is truly
// shared by every peer for a native objective. Most native rules still own
// their full simulation and final settlement. Treasure is different only in
// that its three carried gem counters cross the common pickup/death protocol,
// so the server can validate them without reproducing the client rule object.
type CompetitiveObjectiveKind byte

const (
	CompetitiveObjectiveNone CompetitiveObjectiveKind = iota
	CompetitiveObjectiveBun
	CompetitiveObjectiveWrestle
	CompetitiveObjectiveSculpture
	CompetitiveObjectiveTreasure
	CompetitiveObjectiveTankBase
	CompetitiveObjectiveBoss
)

type CompetitiveRuleConfig struct {
	ConclusionPolicy CompetitiveConclusionPolicy
	PlayerLifecycle  CompetitivePlayerLifecycle
	Objective        CompetitiveObjectiveKind
	TeamTopology     CompetitiveTeamTopology
	// Native statue cells, including empty statues that stop a type-9 hit.
	SculptureCells []CompetitiveSculptureCell
	// NativeHitLimit is the number of non-avatar 0x10F4 harm notifications a
	// rule-7/8 participant can sustain before its early 0x0FA7 report becomes
	// an authoritative death. Zero selects the proven native default of four.
	NativeHitLimit byte
	// BossEntityIDs are the uint16 object IDs advertised by CREATE_NPC_BOSS
	// for a cooperative Boss round.  Only these explicitly registered NPCs
	// may satisfy the shared objective; arbitrary non-player death events must
	// never be able to conclude the match.
	BossEntityIDs []uint16
	// BossID identifies the activated named encounter.
	BossID string
	// BossSkills is the native skill program advertised for each BOSS_INFO
	// object. It lets both the reliable and fast proxy paths reject a skill
	// that the selected encounter never received from the server.
	BossSkills map[uint16][]uint16
	// InitialSceneItems is the server-authored GAME_BEGIN_DATA.NewItems pool.
	// It caps settlement-bearing wall pickups so a syntactically valid client
	// pickup cannot mint currency or experience by itself.
	InitialSceneItems map[uint32]uint32
	// BossSceneItems mirrors each CREATE_NPC_BOSS NormalItems inventory. The
	// native arbitrator chooses concrete drops; the server validates that those
	// drops do not exceed this source inventory.
	BossSceneItems map[uint16]map[uint32]uint32
	// BossDeathItems mirrors each CREATE_NPC_BOSS OutfitItems inventory. The
	// native rule consumes this second inventory only from its lethal callback.
	BossDeathItems map[uint16]map[uint32]uint32
}

// CompetitiveTeamTopology distinguishes opposed-team rounds from a native
// cooperative overlay. Zero remains the ordinary opposed-team default so all
// existing constructors preserve their behaviour.
type CompetitiveTeamTopology byte

const (
	CompetitiveTeamsOpposed CompetitiveTeamTopology = iota
	CompetitiveTeamsCooperative
)

type competitiveNativeRelayKey struct {
	schema uint16
	body   string
}

// CompetitiveTankBaseHPResult is the server-authoritative projection of one
// native rule-13 base report. Applied is false for a saturated repair or a late
// repair/damage report against a base that has already reached zero.
// Resolution becomes newly concluded only after that destroyed team has no
// living, connected avatar left.
type CompetitiveTankBaseHPResult struct {
	BaseTeamID byte
	HP         int16
	Destroyed  bool
	Applied    bool
	Resolution CompetitiveResolution
}

// CompetitiveBattle owns the protocol state shared by all peers. Native rule
// simulations remain in the original client; a compact objective component
// is attached only where common events expose enough information to validate
// it (currently treasure pickups and death drops).
type CompetitiveBattle struct {
	mu                sync.Mutex
	gameID            uint32
	mapID             uint32
	arbitratorID      uint16
	participants      []CompetitiveParticipant
	participantByID   map[uint16]CompetitiveParticipant
	alive             map[uint16]bool
	departed          map[uint16]bool
	trapped           map[uint16]bool
	pendingDeath      map[uint16]bool
	kills             map[uint16]uint32
	rescues           map[uint16]uint32
	objectives        map[uint16]uint32
	sceneRewards      map[uint16]CompetitiveSceneRewards
	collectedItems    map[uint16]map[uint32]uint32
	rewardPickups     map[competitiveRewardPickupKey]struct{}
	nativeRelays      map[competitiveNativeRelayKey]struct{}
	initialSceneItems map[uint32]uint32
	bossSceneItems    map[uint16]map[uint32]uint32
	bossDeathItems    map[uint16]map[uint32]uint32
	droppedSceneItems map[uint32]uint32
	// bossSceneItemCapacity is the aggregate BOSS_INFO NormalItems and
	// OutfitItems allowance.
	// Pickups consume this bounded inventory directly; they do not depend on a
	// speculative ordering relation with the native 0x116B drop notification.
	bossSceneItemCapacity map[uint32]uint32
	bossSceneItemPickups  map[uint32]uint32
	conclusionPolicy      CompetitiveConclusionPolicy
	playerLifecycle       CompetitivePlayerLifecycle
	objective             CompetitiveObjectiveKind
	teamTopology          CompetitiveTeamTopology
	bossID                string
	bossAlive             map[uint16]bool
	bossSkills            map[uint16]map[uint16]struct{}
	// nativeNPCAlive contains dynamic rule entities installed by the elected
	// client through CREATE_NPC_BOSS. Despite the legacy schema name these are
	// not cooperative Boss objectives: their death must never conclude a
	// round. Keeping a separate registry also lets the transport authorize the
	// exact proxy identity without opening arbitrary NPC/player impersonation.
	nativeNPCAlive   map[uint16]bool
	treasure         *competitiveTreasureState
	buns             *competitiveBunState
	sculptures       *competitiveSculptureState
	nativeDurability *competitiveDurabilityState
	tankBaseHP       map[byte]int16
	startedAt        time.Time
	concluded        bool
	timedOutDraw     bool
	winnerTeamID     byte
	bossVictoryGrace bool
	bossVictoryWake  chan struct{}
	bossVictoryWoken bool
}

func SelectCompetitiveArbitrator(preferredID uint16, participants []CompetitiveParticipant) (uint16, error) {
	if len(participants) == 0 {
		return 0, fmt.Errorf("cannot select competitive arbitrator without participants")
	}
	for _, participant := range participants {
		if participant.PlayerID == preferredID && participant.CanArbitrate() {
			return preferredID, nil
		}
	}
	selected := uint16(0)
	for _, participant := range participants {
		if participant.CanArbitrate() && (selected == 0 || participant.PlayerID < selected) {
			selected = participant.PlayerID
		}
	}
	if selected == 0 {
		return 0, fmt.Errorf("cannot select competitive arbitrator without a human participant")
	}
	return selected, nil
}

func NewCompetitiveBattle(gameID, mapID uint32, arbitratorID uint16, participants []CompetitiveParticipant) (*CompetitiveBattle, error) {
	return NewCompetitiveBattleWithPolicy(gameID, mapID, arbitratorID, participants, CompetitiveConclusionOrdinaryElimination)
}

func NewCompetitiveBattleWithPolicy(gameID, mapID uint32, arbitratorID uint16, participants []CompetitiveParticipant, conclusionPolicy CompetitiveConclusionPolicy) (*CompetitiveBattle, error) {
	return NewCompetitiveBattleWithRules(gameID, mapID, arbitratorID, participants, conclusionPolicy, CompetitivePlayerPermanentElimination)
}

func NewCompetitiveBattleWithRules(gameID, mapID uint32, arbitratorID uint16, participants []CompetitiveParticipant, conclusionPolicy CompetitiveConclusionPolicy, playerLifecycle CompetitivePlayerLifecycle) (*CompetitiveBattle, error) {
	return NewCompetitiveBattleWithRuleConfig(gameID, mapID, arbitratorID, participants, CompetitiveRuleConfig{
		ConclusionPolicy: conclusionPolicy,
		PlayerLifecycle:  playerLifecycle,
	})
}

func NewCompetitiveBattleWithRuleConfig(gameID, mapID uint32, arbitratorID uint16, participants []CompetitiveParticipant, config CompetitiveRuleConfig) (*CompetitiveBattle, error) {
	if gameID == 0 || mapID == 0 || arbitratorID == 0 {
		return nil, fmt.Errorf("competitive battle requires non-zero game, map, and arbitrator IDs")
	}
	minimumParticipants := 2
	if config.Objective == CompetitiveObjectiveBoss && config.TeamTopology == CompetitiveTeamsCooperative {
		minimumParticipants = 1
	}
	if len(participants) < minimumParticipants || len(participants) > 8 {
		return nil, fmt.Errorf("competitive participant count %d is outside %d..8", len(participants), minimumParticipants)
	}
	battle := &CompetitiveBattle{
		gameID: gameID, mapID: mapID, arbitratorID: arbitratorID,
		participants:    append([]CompetitiveParticipant(nil), participants...),
		participantByID: make(map[uint16]CompetitiveParticipant, len(participants)),
		alive:           make(map[uint16]bool, len(participants)),
		departed:        make(map[uint16]bool, len(participants)),
		trapped:         make(map[uint16]bool, len(participants)), pendingDeath: make(map[uint16]bool, len(participants)),
		kills: make(map[uint16]uint32), rescues: make(map[uint16]uint32), objectives: make(map[uint16]uint32),
		sceneRewards: make(map[uint16]CompetitiveSceneRewards), collectedItems: make(map[uint16]map[uint32]uint32),
		rewardPickups:     make(map[competitiveRewardPickupKey]struct{}),
		nativeRelays:      make(map[competitiveNativeRelayKey]struct{}),
		initialSceneItems: make(map[uint32]uint32), bossSceneItems: make(map[uint16]map[uint32]uint32), bossDeathItems: make(map[uint16]map[uint32]uint32), droppedSceneItems: make(map[uint32]uint32),
		bossSceneItemCapacity: make(map[uint32]uint32), bossSceneItemPickups: make(map[uint32]uint32),
		conclusionPolicy: config.ConclusionPolicy, playerLifecycle: config.PlayerLifecycle,
		objective:    config.Objective,
		teamTopology: config.TeamTopology, bossID: config.BossID,
		nativeNPCAlive: make(map[uint16]bool),
		startedAt:      time.Now(),
	}
	if config.ConclusionPolicy != CompetitiveConclusionOrdinaryElimination && config.ConclusionPolicy != CompetitiveConclusionClientRule {
		return nil, fmt.Errorf("unknown competitive conclusion policy %d", config.ConclusionPolicy)
	}
	if config.PlayerLifecycle != CompetitivePlayerPermanentElimination && config.PlayerLifecycle != CompetitivePlayerTimedRespawn && config.PlayerLifecycle != CompetitivePlayerNativeDurability {
		return nil, fmt.Errorf("unknown competitive player lifecycle %d", config.PlayerLifecycle)
	}
	if config.PlayerLifecycle != CompetitivePlayerNativeDurability && config.NativeHitLimit != 0 {
		return nil, fmt.Errorf("native hit limit requires native-durability lifecycle")
	}
	if config.Objective != CompetitiveObjectiveNone && config.Objective != CompetitiveObjectiveBun && config.Objective != CompetitiveObjectiveWrestle && config.Objective != CompetitiveObjectiveSculpture && config.Objective != CompetitiveObjectiveTreasure && config.Objective != CompetitiveObjectiveTankBase && config.Objective != CompetitiveObjectiveBoss {
		return nil, fmt.Errorf("unknown competitive objective %d", config.Objective)
	}
	if config.TeamTopology != CompetitiveTeamsOpposed && config.TeamTopology != CompetitiveTeamsCooperative {
		return nil, fmt.Errorf("unknown competitive team topology %d", config.TeamTopology)
	}
	if config.Objective == CompetitiveObjectiveTreasure {
		battle.treasure = newCompetitiveTreasureState(participants)
	}
	if config.Objective == CompetitiveObjectiveBoss {
		if config.ConclusionPolicy != CompetitiveConclusionClientRule || config.TeamTopology != CompetitiveTeamsCooperative {
			return nil, fmt.Errorf("competitive Boss objective requires cooperative client-rule topology")
		}
		if len(config.BossEntityIDs) == 0 {
			return nil, fmt.Errorf("competitive Boss objective requires at least one entity ID")
		}
		if config.BossID == "" {
			return nil, fmt.Errorf("competitive Boss objective requires a named Boss ID")
		}
		battle.bossAlive = make(map[uint16]bool, len(config.BossEntityIDs))
		battle.bossSkills = make(map[uint16]map[uint16]struct{}, len(config.BossSkills))
		for _, entityID := range config.BossEntityIDs {
			if entityID == 0 {
				return nil, fmt.Errorf("competitive Boss entity ID must be non-zero")
			}
			if _, duplicate := battle.bossAlive[entityID]; duplicate {
				return nil, fmt.Errorf("duplicate competitive Boss entity %d", entityID)
			}
			battle.bossAlive[entityID] = true
		}
		for entityID, skills := range config.BossSkills {
			if _, registered := battle.bossAlive[entityID]; !registered {
				return nil, fmt.Errorf("competitive Boss skill program references unregistered entity %d", entityID)
			}
			if len(skills) == 0 {
				return nil, fmt.Errorf("competitive Boss %d skill program is empty", entityID)
			}
			program := make(map[uint16]struct{}, len(skills))
			for _, skillID := range skills {
				if skillID == 0 {
					// BOSS_INFO preserves native positional placeholders. They keep
					// interpreter cooldown slots aligned but never authorize an event.
					continue
				}
				if _, duplicate := program[skillID]; duplicate {
					return nil, fmt.Errorf("competitive Boss %d repeats skill ID %d", entityID, skillID)
				}
				program[skillID] = struct{}{}
			}
			if len(program) == 0 {
				return nil, fmt.Errorf("competitive Boss %d skill program contains only placeholders", entityID)
			}
			battle.bossSkills[entityID] = program
		}
	} else if len(config.BossEntityIDs) != 0 || config.BossID != "" || len(config.BossSkills) != 0 || len(config.BossSceneItems) != 0 || len(config.BossDeathItems) != 0 {
		return nil, fmt.Errorf("competitive Boss metadata requires Boss objective")
	}
	for itemID, quantity := range config.InitialSceneItems {
		if itemID == 0 || quantity == 0 {
			return nil, fmt.Errorf("competitive initial scene item %d has invalid quantity %d", itemID, quantity)
		}
		battle.initialSceneItems[itemID] = quantity
	}
	for entityID, items := range config.BossSceneItems {
		if _, registered := battle.bossAlive[entityID]; !registered {
			return nil, fmt.Errorf("competitive Boss scene inventory references unregistered entity %d", entityID)
		}
		cloned := make(map[uint32]uint32, len(items))
		for itemID, quantity := range items {
			if itemID == 0 || quantity == 0 {
				return nil, fmt.Errorf("competitive Boss %d scene item %d has invalid quantity %d", entityID, itemID, quantity)
			}
			cloned[itemID] = quantity
			if ^uint32(0)-battle.bossSceneItemCapacity[itemID] < quantity {
				return nil, fmt.Errorf("competitive Boss scene item %d aggregate quantity overflows", itemID)
			}
			battle.bossSceneItemCapacity[itemID] += quantity
		}
		battle.bossSceneItems[entityID] = cloned
	}
	for entityID, items := range config.BossDeathItems {
		if _, registered := battle.bossAlive[entityID]; !registered {
			return nil, fmt.Errorf("competitive Boss death inventory references unregistered entity %d", entityID)
		}
		cloned := make(map[uint32]uint32, len(items))
		for itemID, quantity := range items {
			if itemID == 0 || quantity == 0 {
				return nil, fmt.Errorf("competitive Boss %d death item %d has invalid quantity %d", entityID, itemID, quantity)
			}
			cloned[itemID] = quantity
			if ^uint32(0)-battle.bossSceneItemCapacity[itemID] < quantity {
				return nil, fmt.Errorf("competitive Boss death item %d aggregate quantity overflows", itemID)
			}
			battle.bossSceneItemCapacity[itemID] += quantity
		}
		battle.bossDeathItems[entityID] = cloned
	}
	teams := make(map[byte]struct{})
	for _, participant := range participants {
		if participant.PlayerID == 0 || participant.RoleID == 0 || participant.TeamID == 0 {
			return nil, fmt.Errorf("competitive participant has zero player, role, or team ID")
		}
		if participant.Source != CompetitiveParticipantHuman && participant.Source != CompetitiveParticipantVirtualAI {
			return nil, fmt.Errorf("competitive participant %d has invalid source %d", participant.PlayerID, participant.Source)
		}
		if _, duplicate := battle.participantByID[participant.PlayerID]; duplicate {
			return nil, fmt.Errorf("duplicate competitive player %d", participant.PlayerID)
		}
		battle.participantByID[participant.PlayerID] = participant
		battle.alive[participant.PlayerID] = true
		teams[participant.TeamID] = struct{}{}
	}
	arbitrator, ok := battle.participantByID[arbitratorID]
	if !ok {
		return nil, fmt.Errorf("competitive arbitrator %d is not a participant", arbitratorID)
	}
	if !arbitrator.CanArbitrate() {
		return nil, fmt.Errorf("competitive arbitrator %d is not a human participant", arbitratorID)
	}
	if config.TeamTopology == CompetitiveTeamsOpposed && len(teams) < 2 {
		return nil, fmt.Errorf("competitive battle requires at least two teams")
	}
	if config.TeamTopology == CompetitiveTeamsCooperative && len(teams) != 1 {
		return nil, fmt.Errorf("cooperative competitive battle requires exactly one projected team, got %d", len(teams))
	}
	if config.Objective == CompetitiveObjectiveTankBase {
		battle.tankBaseHP = make(map[byte]int16, len(teams))
		for teamID := range teams {
			battle.tankBaseHP[teamID] = 100
		}
	}
	if config.Objective == CompetitiveObjectiveBun {
		battle.buns = newCompetitiveBunState(participants)
	}
	if config.Objective == CompetitiveObjectiveSculpture {
		battle.sculptures = newCompetitiveSculptureState(participants, config.SculptureCells)
	}
	if config.PlayerLifecycle == CompetitivePlayerNativeDurability {
		hitLimit := config.NativeHitLimit
		if hitLimit == 0 {
			hitLimit = 4
		}
		battle.nativeDurability = newCompetitiveDurabilityState(participants, hitLimit)
	}
	return battle, nil
}

// RecordNativeRelay returns true only for the first observed copy of one
// native notification. The original client can submit the same event over
// both room-fast UDP and reliable TCP; peers must apply the scene mutation
// once regardless of which transport reaches the service first.
func (battle *CompetitiveBattle) RecordNativeRelay(schema uint16, body []byte) bool {
	if battle == nil || schema == 0 || len(body) == 0 {
		return false
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	key := competitiveNativeRelayKey{schema: schema, body: string(body)}
	if _, duplicate := battle.nativeRelays[key]; duplicate {
		return false
	}
	battle.nativeRelays[key] = struct{}{}
	return true
}

// RequiresArbitratorConclusion reports whether the native rule object, rather
// than the ordinary server elimination state, owns the final result table.
func (battle *CompetitiveBattle) RequiresArbitratorConclusion() bool {
	if battle == nil {
		return false
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	return battle.conclusionPolicy == CompetitiveConclusionClientRule
}

// IsCooperative reports whether the native rule projected every participant
// onto one shared team (for example a summoned-Boss round). Cooperative total
// defeat is a real loss and must not be normalized into an ordinary timeout
// draw merely because its result table contains no winner.
func (battle *CompetitiveBattle) IsCooperative() bool {
	if battle == nil {
		return false
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	return battle.teamTopology == CompetitiveTeamsCooperative
}

// BossID returns the stable encounter identity used by diagnostics and
// capability checks. An empty value means the round has no Boss overlay.
func (battle *CompetitiveBattle) BossID() string {
	if battle == nil {
		return ""
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	return battle.bossID
}

func (battle *CompetitiveBattle) IsConcluded() bool {
	if battle == nil {
		return true
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	return battle.concluded
}

// ConcludedResolution snapshots the immutable match outcome chosen by the
// authoritative state machine. It is used during the short gap between
// conclusion and ordered GAME_OVER delivery, including disconnect handling.
func (battle *CompetitiveBattle) ConcludedResolution() (CompetitiveResolution, bool) {
	if battle == nil {
		return CompetitiveResolution{}, false
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	if !battle.concluded {
		return CompetitiveResolution{}, false
	}
	return battle.resolution(false), true
}

// ElapsedMilliseconds returns the time domain used by native GAME_OVER.Time.
// Server-authored departure settlement must not place Unix seconds in this
// field: the client compares it with the current round clock.
func (battle *CompetitiveBattle) ElapsedMilliseconds() uint32 {
	if battle == nil {
		return 0
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	if battle.startedAt.IsZero() {
		return 0
	}
	elapsed := time.Since(battle.startedAt) / time.Millisecond
	if elapsed <= 0 {
		return 0
	}
	if elapsed > time.Duration(^uint32(0)) {
		return ^uint32(0)
	}
	return uint32(elapsed)
}

// UsesBunObjective, UsesSculptureObjective, and UsesTreasureObjective expose only the objective
// discriminator needed by transport normalizers. They deliberately do not
// leak mutable objective state outside the match package.
func (battle *CompetitiveBattle) UsesBunObjective() bool {
	if battle == nil {
		return false
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	return battle.buns != nil
}

func (battle *CompetitiveBattle) UsesSculptureObjective() bool {
	if battle == nil {
		return false
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	return battle.sculptures != nil
}

func (battle *CompetitiveBattle) UsesTreasureObjective() bool {
	if battle == nil {
		return false
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	return battle.treasure != nil
}

func (battle *CompetitiveBattle) UsesTankBaseObjective() bool {
	if battle == nil {
		return false
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	return battle.objective == CompetitiveObjectiveTankBase
}

// ConcludeDraw atomically closes an opposed-team round whose authoritative
// server clock expired before any rule produced a winner. Boss/cooperative
// rounds have their own success and failure boundaries and are never turned
// into draws by this fallback.
func (battle *CompetitiveBattle) ConcludeDraw() (CompetitiveResolution, error) {
	if battle == nil {
		return CompetitiveResolution{}, fmt.Errorf("competitive battle is nil")
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	if battle.concluded {
		return battle.resolution(false), nil
	}
	if battle.teamTopology == CompetitiveTeamsCooperative {
		return CompetitiveResolution{}, fmt.Errorf("cooperative competitive battle cannot time out as a draw")
	}
	battle.concluded = true
	battle.timedOutDraw = true
	return battle.resolution(true), nil
}

// ConcludeTimeout closes a round at the authoritative server deadline.
// Opposed-team rounds are draws when no native rule selected a winner. A
// cooperative Boss round is different: leaving the objective alive until the
// deadline is a team failure, even when one or more players are still alive.
func (battle *CompetitiveBattle) ConcludeTimeout() (CompetitiveResolution, error) {
	if battle == nil {
		return CompetitiveResolution{}, fmt.Errorf("competitive battle is nil")
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	if battle.concluded {
		// Boss death locks the cooperative victory immediately, while transport
		// deliberately holds GAME_OVER for a short presentation grace. The
		// absolute round deadline is still authoritative: if it arrives during
		// that grace, wake the pending victory delivery now instead of allowing
		// the presentation delay to extend the four-minute match clock.
		if battle.bossVictoryGrace && battle.bossVictoryWake != nil && !battle.bossVictoryWoken {
			close(battle.bossVictoryWake)
			battle.bossVictoryWoken = true
		}
		return battle.resolution(false), nil
	}
	if battle.objective == CompetitiveObjectiveTankBase {
		if resolution := battle.maybeConcludeTankLocked(); resolution.NewlyConcluded {
			return resolution, nil
		}
	}
	if battle.buns != nil {
		battle.concluded = true
		battle.winnerTeamID = battle.buns.timeoutWinner()
		battle.timedOutDraw = battle.winnerTeamID == 0
		return battle.resolution(true), nil
	}
	if battle.sculptures != nil {
		battle.concluded = true
		battle.winnerTeamID = battle.sculptures.timeoutWinner()
		battle.timedOutDraw = battle.winnerTeamID == 0
		return battle.resolution(true), nil
	}
	if battle.objective == CompetitiveObjectiveWrestle {
		battle.concluded = true
		battle.winnerTeamID = battle.timeoutWinnerByPlayerValues(battle.kills)
		battle.timedOutDraw = battle.winnerTeamID == 0
		return battle.resolution(true), nil
	}
	if battle.treasure != nil {
		values := make(map[uint16]uint32, len(battle.participants))
		for _, participant := range battle.participants {
			values[participant.PlayerID] = battle.treasure.value(participant.PlayerID)
		}
		battle.concluded = true
		battle.winnerTeamID = battle.timeoutWinnerByPlayerValues(values)
		battle.timedOutDraw = battle.winnerTeamID == 0
		return battle.resolution(true), nil
	}
	battle.concluded = true
	if battle.teamTopology == CompetitiveTeamsCooperative {
		for playerID := range battle.alive {
			battle.alive[playerID] = false
			battle.trapped[playerID] = false
			battle.pendingDeath[playerID] = false
		}
		return battle.resolution(true), nil
	}
	battle.timedOutDraw = true
	return battle.resolution(true), nil
}

// timeoutWinnerByPlayerValues compares team totals and returns zero on a tie.
// It is shared by native timed-score rules such as wrestle kills and carried
// treasure value; per-player values remain available in the result table.
func (battle *CompetitiveBattle) timeoutWinnerByPlayerValues(values map[uint16]uint32) byte {
	teamTotals := make(map[byte]uint32)
	for _, participant := range battle.participants {
		teamTotals[participant.TeamID] += values[participant.PlayerID]
	}
	var winner byte
	var highest uint32
	tied := false
	for teamID, total := range teamTotals {
		switch {
		case winner == 0 || total > highest:
			winner, highest, tied = teamID, total, false
		case total == highest:
			tied = true
		}
	}
	if tied {
		return 0
	}
	return winner
}

// RecordTankBaseHPChange consumes the native tank base's signed delta. Static
// client evidence gives exactly three legal reports: +30 repairs the actor's
// own base, while -10 and the armour-reduced -5 damage an opposing base. The
// wire DestTeamID is always the target base's team: the repair producer copies
// the local player's team, while the explosion producer copies +0x39 from the
// base object hit at that cell. The client clamps HP to 0..100; zero is
// irreversible for the remainder of the round. Base destruction alone does
// not win: the destroyed team must also have no living connected avatar.
func (battle *CompetitiveBattle) RecordTankBaseHPChange(playerID uint16, reportedTeamID byte, change int16) (CompetitiveTankBaseHPResult, error) {
	if battle == nil {
		return CompetitiveTankBaseHPResult{}, fmt.Errorf("competitive battle is nil")
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	participant, ok := battle.participantByID[playerID]
	if !ok {
		return CompetitiveTankBaseHPResult{}, fmt.Errorf("tank-base player %d is not a participant", playerID)
	}
	if battle.departed[playerID] {
		return CompetitiveTankBaseHPResult{}, fmt.Errorf("tank-base player %d has departed", playerID)
	}
	targetTeamID := reportedTeamID
	if _, exists := battle.tankBaseHP[targetTeamID]; !exists {
		return CompetitiveTankBaseHPResult{}, fmt.Errorf("tank-base target team %d is not in this battle", targetTeamID)
	}
	switch change {
	case 30:
		if targetTeamID != participant.TeamID {
			return CompetitiveTankBaseHPResult{}, fmt.Errorf("tank-base player %d cannot repair opposing team %d", playerID, targetTeamID)
		}
	case -5, -10:
		if targetTeamID == participant.TeamID {
			return CompetitiveTankBaseHPResult{}, fmt.Errorf("tank-base player %d cannot damage own team %d", playerID, targetTeamID)
		}
	default:
		return CompetitiveTankBaseHPResult{}, fmt.Errorf("tank-base HP change %d is outside native +30/-5/-10 values", change)
	}
	current := battle.tankBaseHP[targetTeamID]
	result := CompetitiveTankBaseHPResult{BaseTeamID: targetTeamID, HP: current, Destroyed: current == 0, Resolution: battle.resolution(false)}
	if battle.concluded || current == 0 {
		return result, nil
	}
	next := int32(current) + int32(change)
	if next < 0 {
		next = 0
	} else if next > 100 {
		next = 100
	}
	battle.tankBaseHP[targetTeamID] = int16(next)
	result.HP = int16(next)
	result.Destroyed = next == 0
	result.Applied = int16(next) != current
	result.Resolution = battle.maybeConcludeTankLocked()
	return result, nil
}

// maybeConcludeTankLocked closes the rule-13 round only when every team but
// one has permanently lost its base/respawn path and has no living connected
// avatar. An intact base keeps a temporarily dead team viable; a destroyed
// base keeps it viable only while at least one avatar is still alive.
func (battle *CompetitiveBattle) maybeConcludeTankLocked() CompetitiveResolution {
	if battle.objective != CompetitiveObjectiveTankBase || battle.concluded || len(battle.tankBaseHP) < 2 {
		return battle.resolution(false)
	}
	viableTeams := make(map[byte]struct{}, len(battle.tankBaseHP))
	connectedTeams := make(map[byte]bool, len(battle.tankBaseHP))
	livingTeams := make(map[byte]bool, len(battle.tankBaseHP))
	for playerID, participant := range battle.participantByID {
		if battle.departed[playerID] {
			continue
		}
		connectedTeams[participant.TeamID] = true
		if battle.alive[playerID] {
			livingTeams[participant.TeamID] = true
		}
	}
	for teamID, hp := range battle.tankBaseHP {
		if connectedTeams[teamID] && (hp > 0 || livingTeams[teamID]) {
			viableTeams[teamID] = struct{}{}
		}
	}
	if len(viableTeams) > 1 {
		return battle.resolution(false)
	}
	battle.concluded = true
	battle.arbitratorID = 0
	if len(viableTeams) == 0 {
		battle.timedOutDraw = true
		return battle.resolution(true)
	}
	for teamID := range viableTeams {
		battle.winnerTeamID = teamID
	}
	return battle.resolution(true)
}

// RecordTrapped records the beginning of one syrup interaction. It is kept
// separate from permanent death: a same-colour teammate may still rescue the
// player, while a different-colour opponent may confirm the elimination.
func (battle *CompetitiveBattle) RecordTrapped(playerID uint16) error {
	if battle == nil {
		return fmt.Errorf("competitive battle is nil")
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	if _, ok := battle.participantByID[playerID]; !ok {
		return fmt.Errorf("competitive trapped player %d is not a participant", playerID)
	}
	if battle.concluded || !battle.alive[playerID] {
		return nil
	}
	battle.trapped[playerID] = true
	battle.pendingDeath[playerID] = false
	return nil
}

func (battle *CompetitiveBattle) validateInteractionLocked(actorID, targetID uint16, sameTeam bool) error {
	if actorID == targetID {
		return fmt.Errorf("competitive player %d cannot interact with itself", actorID)
	}
	actor, actorOK := battle.participantByID[actorID]
	target, targetOK := battle.participantByID[targetID]
	if !actorOK || !targetOK {
		return fmt.Errorf("competitive interaction players %d/%d must both be participants", actorID, targetID)
	}
	if !battle.alive[actorID] || !battle.alive[targetID] {
		return fmt.Errorf("competitive interaction players %d/%d must both be alive", actorID, targetID)
	}
	isSameTeam := actor.TeamID == target.TeamID
	if isSameTeam != sameTeam {
		expected := "different"
		if sameTeam {
			expected = "same"
		}
		return fmt.Errorf("competitive interaction players %d/%d must have %s team colours", actorID, targetID, expected)
	}
	return nil
}

func (battle *CompetitiveBattle) HasParticipant(playerID uint16) bool {
	if battle == nil {
		return false
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	_, ok := battle.participantByID[playerID]
	return ok
}

// AuthorizesParticipantObservation reports whether reporterID may submit one
// client-calculated scene observation for targetID. A participant may always
// report its own state. The native scene arbitrator also reports collisions
// and timeout deaths for other participants, so it may report any participant
// in the same battle. This capability is deliberately narrower than allowing
// arbitrary player actions on another player's behalf.
func (battle *CompetitiveBattle) AuthorizesParticipantObservation(reporterID, targetID uint16) bool {
	if battle == nil {
		return false
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	if _, ok := battle.participantByID[targetID]; !ok {
		return false
	}
	return reporterID == targetID || reporterID == battle.arbitratorID
}

// HasBossEntity reports whether objectID belongs to the explicit objective
// table sent for this match. NPC deaths share NOTIFY_PLAYER_DIE with avatar
// deaths, so the dispatcher must make this distinction before settlement.
func (battle *CompetitiveBattle) HasBossEntity(objectID uint16) bool {
	if battle == nil {
		return false
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	_, ok := battle.bossAlive[objectID]
	return ok
}

// RegisterNativeNPCEntities installs the dynamic rule entities described by
// one validated CREATE_NPC_BOSS table. These IDs are deliberately kept out of
// bossAlive: an ordinary AIType=2 map monster is a damage producer/target, not
// the cooperative objective whose death wins a Boss round.
func (battle *CompetitiveBattle) RegisterNativeNPCEntities(objectIDs []uint16) error {
	if battle == nil {
		return fmt.Errorf("competitive battle is nil")
	}
	if len(objectIDs) == 0 {
		return fmt.Errorf("native NPC entity table is empty")
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	if battle.nativeDurability == nil {
		return fmt.Errorf("native NPC entities require native-durability rules")
	}
	seen := make(map[uint16]struct{}, len(objectIDs))
	for _, objectID := range objectIDs {
		if objectID == 0 {
			return fmt.Errorf("native NPC entity ID must be non-zero")
		}
		if _, duplicate := seen[objectID]; duplicate {
			return fmt.Errorf("duplicate native NPC entity %d", objectID)
		}
		seen[objectID] = struct{}{}
		if _, participant := battle.participantByID[objectID]; participant {
			return fmt.Errorf("native NPC entity %d collides with a participant", objectID)
		}
		if _, boss := battle.bossAlive[objectID]; boss {
			return fmt.Errorf("native NPC entity %d collides with a Boss objective", objectID)
		}
	}
	for objectID := range seen {
		// Reliable retries of the same creation table are idempotent. A later
		// native creation using the same slot legitimately reactivates it.
		battle.nativeNPCAlive[objectID] = true
	}
	return nil
}

// HasNativeNPCEntity reports whether objectID was installed by the native
// rule for this round. It remains true after death so reliable retries can be
// ACKed without misclassifying the object as an unknown player.
func (battle *CompetitiveBattle) HasNativeNPCEntity(objectID uint16) bool {
	if battle == nil {
		return false
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	_, ok := battle.nativeNPCAlive[objectID]
	return ok
}

// AuthorizesNativeNPCProxy permits only the current arbitrator to upload a
// fast-path package carrying one living, previously registered rule NPC ID.
func (battle *CompetitiveBattle) AuthorizesNativeNPCProxy(arbitratorID, objectID uint16) bool {
	if battle == nil {
		return false
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	return battle.arbitratorID == arbitratorID && battle.nativeNPCAlive[objectID]
}

// RecordNativeNPCDeath retires a dynamic rule NPC without affecting team
// survival or match resolution. The boolean distinguishes the first report
// from reliable/fast-path duplicates.
func (battle *CompetitiveBattle) RecordNativeNPCDeath(objectID uint16) (bool, error) {
	if battle == nil {
		return false, fmt.Errorf("competitive battle is nil")
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	alive, ok := battle.nativeNPCAlive[objectID]
	if !ok {
		return false, fmt.Errorf("native NPC object %d is not registered", objectID)
	}
	if !alive {
		return false, nil
	}
	battle.nativeNPCAlive[objectID] = false
	return true, nil
}

// AuthorizesBossProxy reports whether the current arbitrator may upload one
// native fast-path package on behalf of a registered living Boss object. The
// QQTang rule object runs Boss AI on the arbitrator client, so its 0x0FBD
// package legitimately carries the NPC object ID rather than the logged-in
// player's ID. Keeping both checks under one lock prevents this narrowly
// scoped exception from becoming general player impersonation.
func (battle *CompetitiveBattle) AuthorizesBossProxy(arbitratorID, objectID uint16) bool {
	if battle == nil {
		return false
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	return battle.arbitratorID == arbitratorID && battle.bossAlive[objectID]
}

// AuthorizesBossSkill narrows the native arbitrator exception to one living
// BOSS_INFO object and one skill ID explicitly advertised for that object.
// A missing program is intentionally not treated as an open wildcard.
func (battle *CompetitiveBattle) AuthorizesBossSkill(arbitratorID, objectID, skillID uint16) bool {
	if battle == nil || skillID == 0 {
		return false
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	if battle.arbitratorID != arbitratorID || !battle.bossAlive[objectID] {
		return false
	}
	_, ok := battle.bossSkills[objectID][skillID]
	return ok
}

// AuthorizesBossSkillTarget validates the reliable REQUEST_PLAY form of a
// native Boss skill.  Unlike a QQT_DATA_PACKAGE, that wrapper has no separate
// package-level Boss identity: targeted skills put the participant target in
// BOSS_INFO.PlayerID, while self/area skills put the Boss object there.  A
// participant target is therefore accepted only when exactly one living Boss
// in this battle advertises the skill.  This keeps the inference bounded and
// refuses ambiguous multi-Boss programs instead of treating the field as an
// unrestricted wildcard.
func (battle *CompetitiveBattle) AuthorizesBossSkillTarget(arbitratorID, objectOrTargetID, skillID uint16) bool {
	if battle == nil || skillID == 0 {
		return false
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	if battle.arbitratorID != arbitratorID {
		return false
	}
	if battle.bossAlive[objectOrTargetID] {
		_, ok := battle.bossSkills[objectOrTargetID][skillID]
		return ok
	}
	if _, ok := battle.participantByID[objectOrTargetID]; !ok {
		return false
	}
	matches := 0
	for objectID, alive := range battle.bossAlive {
		if !alive {
			continue
		}
		if _, ok := battle.bossSkills[objectID][skillID]; ok {
			matches++
		}
	}
	return matches == 1
}

// IsArbitrator reports whether playerID owns client-authoritative scene
// generation for this round. The original client only lets that peer emit
// shared entity tables such as CREATE_NPC_BOSS; accepting the same table from
// every participant would duplicate entities on all recipients.
func (battle *CompetitiveBattle) IsArbitrator(playerID uint16) bool {
	if battle == nil {
		return false
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	return battle.arbitratorID == playerID
}

// ArbitratorPlayerID returns the participant that owns native shared-scene
// calculations for this round. Native request events are routed only to this
// peer; broadcasting those requests to every client would make each rule
// object independently generate a conflicting result table.
func (battle *CompetitiveBattle) ArbitratorPlayerID() uint16 {
	if battle == nil {
		return 0
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	return battle.arbitratorID
}

func (battle *CompetitiveBattle) Participants() []CompetitiveParticipant {
	if battle == nil {
		return nil
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	return append([]CompetitiveParticipant(nil), battle.participants...)
}

func (battle *CompetitiveBattle) PlayerStatistics(playerID uint16) (CompetitivePlayerStatistics, error) {
	if battle == nil {
		return CompetitivePlayerStatistics{}, fmt.Errorf("competitive battle is nil")
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	if _, ok := battle.participantByID[playerID]; !ok {
		return CompetitivePlayerStatistics{}, fmt.Errorf("competitive statistics player %d is not a participant", playerID)
	}
	return CompetitivePlayerStatistics{
		KillCount: battle.kills[playerID], RescueCount: battle.rescues[playerID],
	}, nil
}

// RecordKill confirms an opponent touch after trapping. In ordinary
// elimination maps this is the victim's permanent death; no later 0x0FA7 is
// required. Special competitive maps use dedicated state machines and never
// construct CompetitiveBattle.
func (battle *CompetitiveBattle) RecordKill(killerID, victimID uint16) (CompetitiveResolution, error) {
	if battle == nil {
		return CompetitiveResolution{}, fmt.Errorf("competitive battle is nil")
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	if battle.concluded {
		return battle.resolution(false), nil
	}
	if _, ok := battle.participantByID[killerID]; !ok {
		return CompetitiveResolution{}, fmt.Errorf("competitive kill player %d is not a participant", killerID)
	}
	if _, ok := battle.participantByID[victimID]; !ok {
		return CompetitiveResolution{}, fmt.Errorf("competitive kill target %d is not a participant", victimID)
	}
	// The client can repeat collision confirmation while the local animation
	// unwinds. Once the victim is dead, accept the duplicate without adding a
	// second kill or reopening settlement.
	if !battle.alive[victimID] {
		return battle.resolution(false), nil
	}
	if err := battle.validateInteractionLocked(killerID, victimID, false); err != nil {
		return CompetitiveResolution{}, err
	}
	if !battle.pendingDeath[victimID] {
		battle.kills[killerID]++
		battle.pendingDeath[victimID] = true
	}
	battle.dropCarriedBunLocked(victimID)
	battle.dropCarriedSculptureLocked(victimID)
	battle.trapped[victimID] = false
	if battle.playerLifecycle == CompetitivePlayerTimedRespawn {
		// Timed modes keep the participant in the round but not alive during
		// the native death animation. Only NOTIFY_PLAYER_RELIVE reopens it.
		battle.alive[victimID] = false
		if battle.objective == CompetitiveObjectiveTankBase {
			return battle.maybeConcludeTankLocked(), nil
		}
		return battle.resolution(false), nil
	}
	if battle.playerLifecycle == CompetitivePlayerNativeDurability {
		// These rule objects own hit points/durability. One collision is not a
		// permanent elimination and the next trapping event opens a new hit.
		return battle.resolution(false), nil
	}
	battle.pendingDeath[victimID] = false
	battle.alive[victimID] = false
	if battle.conclusionPolicy == CompetitiveConclusionClientRule {
		if battle.teamTopology != CompetitiveTeamsCooperative || len(battle.livingTeams()) != 0 {
			return battle.resolution(false), nil
		}
		battle.concluded = true
		return battle.resolution(true), nil
	}
	if len(battle.livingTeams()) > 1 {
		return battle.resolution(false), nil
	}
	battle.concluded = true
	return battle.resolution(true), nil
}

// RecordBossKill consumes REQUEST_KILL_PLAYER/NOTIFY_PLAYER_BE_KILLED when a
// registered cooperative Boss is the source and a room participant is the
// destination. The native client uses the same player-interaction schema for
// this NPC-to-avatar collision, so it must not be forced through RecordKill's
// player-vs-player team validation. Boss eliminations do not increment any
// player's kill counter.
func (battle *CompetitiveBattle) RecordBossKill(bossID, victimID uint16) (CompetitiveResolution, error) {
	if battle == nil {
		return CompetitiveResolution{}, fmt.Errorf("competitive battle is nil")
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	if battle.teamTopology != CompetitiveTeamsCooperative {
		return CompetitiveResolution{}, fmt.Errorf("competitive Boss kill requires cooperative topology")
	}
	if alive, ok := battle.bossAlive[bossID]; !ok || !alive {
		return CompetitiveResolution{}, fmt.Errorf("competitive Boss object %d is not an active objective", bossID)
	}
	if _, ok := battle.participantByID[victimID]; !ok {
		return CompetitiveResolution{}, fmt.Errorf("competitive Boss kill target %d is not a participant", victimID)
	}
	if battle.concluded || !battle.alive[victimID] {
		return battle.resolution(false), nil
	}
	if !battle.trapped[victimID] {
		return CompetitiveResolution{}, fmt.Errorf("competitive Boss kill target %d is not trapped", victimID)
	}
	battle.trapped[victimID] = false
	battle.pendingDeath[victimID] = false
	battle.alive[victimID] = false
	if len(battle.livingTeams()) != 0 {
		return battle.resolution(false), nil
	}
	battle.concluded = true
	return battle.resolution(true), nil
}

func (battle *CompetitiveBattle) RecordRescue(rescuerID, rescuedID uint16) error {
	if battle == nil {
		return fmt.Errorf("competitive battle is nil")
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	if battle.concluded {
		return nil
	}
	if err := battle.validateInteractionLocked(rescuerID, rescuedID, true); err != nil {
		return err
	}
	if battle.trapped[rescuedID] {
		battle.rescues[rescuerID]++
	}
	battle.trapped[rescuedID] = false
	battle.pendingDeath[rescuedID] = false
	return nil
}

// RecordObjective records one confirmed special-mode objective action, such
// as depositing a bun. The rule-specific settlement encoder decides where it
// appears; the ordinary battle does not guess a GAME_OVER field ordinal.
func (battle *CompetitiveBattle) RecordObjective(playerID uint16) error {
	if battle == nil {
		return fmt.Errorf("competitive battle is nil")
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	if battle.concluded {
		return nil
	}
	if _, ok := battle.participantByID[playerID]; !ok {
		return fmt.Errorf("competitive objective player %d is not a participant", playerID)
	}
	battle.objectives[playerID] = saturatingIncrement(battle.objectives[playerID], 1)
	return nil
}

// RecordDeath marks a permanent elimination. Bubble trapping and rescue are
// represented by their own events and must not remove a player from alive.
func (battle *CompetitiveBattle) RecordDeath(playerID uint16) (CompetitiveResolution, error) {
	if battle == nil {
		return CompetitiveResolution{}, fmt.Errorf("competitive battle is nil")
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	if _, ok := battle.participantByID[playerID]; !ok {
		return CompetitiveResolution{}, fmt.Errorf("competitive death player %d is not a participant", playerID)
	}
	if battle.concluded || !battle.alive[playerID] {
		return battle.resolution(false), nil
	}
	battle.trapped[playerID] = false
	battle.pendingDeath[playerID] = false
	battle.dropCarriedBunLocked(playerID)
	battle.dropCarriedSculptureLocked(playerID)
	if battle.playerLifecycle == CompetitivePlayerTimedRespawn {
		battle.alive[playerID] = false
		if battle.objective == CompetitiveObjectiveTankBase {
			return battle.maybeConcludeTankLocked(), nil
		}
		return battle.resolution(false), nil
	}
	if battle.playerLifecycle == CompetitivePlayerNativeDurability {
		return battle.resolution(false), nil
	}
	battle.alive[playerID] = false
	if battle.conclusionPolicy == CompetitiveConclusionClientRule {
		// A native Boss objective owns the success boundary, but it cannot
		// produce a result after every cooperative player has already died.
		// The official failure boundary is therefore server-authoritative even
		// though Boss death/success remains client-event driven.
		if battle.teamTopology != CompetitiveTeamsCooperative || len(battle.livingTeams()) != 0 {
			return battle.resolution(false), nil
		}
		battle.concluded = true
		return battle.resolution(true), nil
	}
	teams := battle.livingTeams()
	if len(teams) > 1 {
		return battle.resolution(false), nil
	}
	battle.concluded = true
	return battle.resolution(true), nil
}

// RecordBossDeath consumes the NPC half of NOTIFY_PLAYER_DIE. A Boss round
// succeeds only after every explicitly registered objective entity is dead;
// player survival and ordinary opposed-team elimination remain separate.
func (battle *CompetitiveBattle) RecordBossDeath(objectID uint16) (CompetitiveResolution, error) {
	if battle == nil {
		return CompetitiveResolution{}, fmt.Errorf("competitive battle is nil")
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	alive, ok := battle.bossAlive[objectID]
	if !ok {
		return CompetitiveResolution{}, fmt.Errorf("competitive Boss object %d is not an objective", objectID)
	}
	if battle.concluded || !alive {
		return battle.resolution(false), nil
	}
	battle.bossAlive[objectID] = false
	for _, remaining := range battle.bossAlive {
		if remaining {
			return battle.resolution(false), nil
		}
	}
	battle.concluded = true
	// Cooperative Boss success is decided by the objective death, not by
	// avatar survival during the presentation grace that follows. Persist the
	// winning team explicitly so later deaths, departures, or the absolute
	// clock cannot erase the already-earned victory.
	if len(battle.participants) != 0 {
		battle.winnerTeamID = battle.participants[0].TeamID
	}
	battle.bossVictoryGrace = true
	battle.bossVictoryWake = make(chan struct{})
	return battle.resolution(true), nil
}

// RecordBossVictoryGraceDeath keeps the already-decided cooperative Boss
// victory while tracking deaths during its short presentation delay. The last
// living participant wakes the pending GAME_OVER so the victory is shown
// immediately; it does not produce a second resolution.
func (battle *CompetitiveBattle) RecordBossVictoryGraceDeath(playerID uint16) (handled, allDead bool, err error) {
	if battle == nil {
		return false, false, fmt.Errorf("competitive battle is nil")
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	if !battle.bossVictoryGrace {
		return false, false, nil
	}
	if _, ok := battle.participantByID[playerID]; !ok {
		return false, false, fmt.Errorf("competitive Boss-victory death player %d is not a participant", playerID)
	}
	if battle.alive[playerID] {
		battle.alive[playerID] = false
		battle.trapped[playerID] = false
		battle.pendingDeath[playerID] = false
	}
	allDead = len(battle.livingTeams()) == 0
	if allDead && !battle.bossVictoryWoken {
		close(battle.bossVictoryWake)
		battle.bossVictoryWoken = true
	}
	return true, allDead, nil
}

// RecordBossVictoryGraceDeparture is the leave/disconnect counterpart of
// RecordBossVictoryGraceDeath. Departures after Boss death never turn the
// locked victory into a loss; when no participant remains, they only shorten
// the pending presentation delay.
func (battle *CompetitiveBattle) RecordBossVictoryGraceDeparture(playerID uint16) (handled, allGone bool, err error) {
	if battle == nil {
		return false, false, fmt.Errorf("competitive battle is nil")
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	if !battle.bossVictoryGrace {
		return false, false, nil
	}
	if _, ok := battle.participantByID[playerID]; !ok {
		return false, false, fmt.Errorf("competitive Boss-victory departure player %d is not a participant", playerID)
	}
	battle.departed[playerID] = true
	battle.alive[playerID] = false
	battle.trapped[playerID] = false
	battle.pendingDeath[playerID] = false
	allGone = len(battle.livingTeams()) == 0
	if allGone && !battle.bossVictoryWoken {
		close(battle.bossVictoryWake)
		battle.bossVictoryWoken = true
	}
	return true, allGone, nil
}

// BossVictoryGraceSignal exposes only the completion signal needed by the
// ordered transport delay. The battle remains concluded and its result table
// remains the Boss victory chosen before the grace period began.
func (battle *CompetitiveBattle) BossVictoryGraceSignal() (<-chan struct{}, bool) {
	if battle == nil {
		return nil, false
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	if !battle.bossVictoryGrace || battle.bossVictoryWake == nil {
		return nil, false
	}
	return battle.bossVictoryWake, true
}

func (battle *CompetitiveBattle) CompleteBossVictoryGrace() {
	if battle == nil {
		return
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	battle.bossVictoryGrace = false
}

// RecordRespawn consumes the native NOTIFY_PLAYER_RELIVE boundary. The
// client owns the visual timer and spawn cell; the server only reopens the
// interaction latch for rules whose native object explicitly supports timed
// respawn. Native-durability rules consume damage without this boundary.
func (battle *CompetitiveBattle) RecordRespawn(playerID uint16) error {
	if battle == nil {
		return fmt.Errorf("competitive battle is nil")
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	if _, ok := battle.participantByID[playerID]; !ok {
		return fmt.Errorf("competitive respawn player %d is not a participant", playerID)
	}
	if battle.concluded {
		return fmt.Errorf("competitive battle is already concluded")
	}
	if battle.playerLifecycle != CompetitivePlayerTimedRespawn {
		return fmt.Errorf("competitive player %d cannot respawn under lifecycle %d", playerID, battle.playerLifecycle)
	}
	if battle.departed[playerID] {
		return fmt.Errorf("competitive respawn player %d has departed", playerID)
	}
	if battle.objective == CompetitiveObjectiveTankBase {
		participant := battle.participantByID[playerID]
		if battle.tankBaseHP[participant.TeamID] == 0 {
			return fmt.Errorf("competitive tank player %d cannot respawn after team %d base destruction", playerID, participant.TeamID)
		}
	}
	battle.alive[playerID] = true
	battle.trapped[playerID] = false
	battle.pendingDeath[playerID] = false
	return nil
}

func (battle *CompetitiveBattle) livingTeams() map[byte]struct{} {
	teams := make(map[byte]struct{})
	for playerID, alive := range battle.alive {
		if alive {
			teams[battle.participantByID[playerID].TeamID] = struct{}{}
		}
	}
	return teams
}

func (battle *CompetitiveBattle) resolution(newlyConcluded bool) CompetitiveResolution {
	resolution := CompetitiveResolution{NewlyConcluded: newlyConcluded}
	if !battle.concluded {
		resolution.ArbitratorPlayerID = battle.arbitratorID
	}
	teams := battle.livingTeams()
	if battle.timedOutDraw {
		teams = nil
	}
	resolution.AllPlayersLost = battle.concluded && battle.teamTopology == CompetitiveTeamsCooperative && battle.winnerTeamID == 0 && len(teams) == 0
	if battle.winnerTeamID != 0 {
		resolution.WinnerTeamID = battle.winnerTeamID
	} else {
		for teamID := range teams {
			resolution.WinnerTeamID = teamID
		}
	}
	resolution.Results = make([]CompetitivePlayerResult, 0, len(battle.participants))
	for _, participant := range battle.participants {
		objectiveCount := battle.objectives[participant.PlayerID]
		if battle.treasure != nil {
			objectiveCount = battle.treasure.value(participant.PlayerID)
		}
		resolution.Results = append(resolution.Results, CompetitivePlayerResult{
			Participant: participant,
			Won:         resolution.WinnerTeamID != 0 && participant.TeamID == resolution.WinnerTeamID,
			Draw:        resolution.WinnerTeamID == 0 && !resolution.AllPlayersLost,
			KillCount:   battle.kills[participant.PlayerID], RescueCount: battle.rescues[participant.PlayerID],
			ObjectiveCount: objectiveCount,
		})
	}
	sort.SliceStable(resolution.Results, func(i, j int) bool {
		return resolution.Results[i].Participant.PlayerID < resolution.Results[j].Participant.PlayerID
	})
	return resolution
}
