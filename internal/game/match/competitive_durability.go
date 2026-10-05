package match

import "fmt"

type competitiveHarmEventKey struct {
	playerID   uint16
	clientTime uint32
	posX, posY uint16
	isAvatar   bool
	lossHP     uint16
}

type competitiveDurabilityState struct {
	limit          byte
	remaining      map[uint16]byte
	deathConfirmed map[uint16]bool
	seen           map[competitiveHarmEventKey]struct{}
	seenDeaths     map[competitiveDurabilityDeathKey]struct{}
	// Some rule-7/8 damage producers precede 0x0FA7 with 0x10F4 while
	// roaming rule NPCs emit only 0x0FA7. Pair the two forms when possible.
	pendingReports map[uint16]byte
	// FUN_00601fec copies Time/PosX/PosY from harm to its death report.
	// Remember deaths which already consumed a layer before that harm's
	// reliable mirror arrives, so the native producer's send order cannot
	// turn one hit into two server-side life charges.
	deathsBeforeHarm map[competitiveDurabilityDeathKey]struct{}
}

type competitiveDurabilityDeathKey struct {
	playerID   uint16
	clientTime uint32
	posX, posY uint16
}

func newCompetitiveDurabilityState(participants []CompetitiveParticipant, limit byte) *competitiveDurabilityState {
	state := &competitiveDurabilityState{
		limit: limit, remaining: make(map[uint16]byte, len(participants)),
		deathConfirmed:   make(map[uint16]bool, len(participants)),
		seen:             make(map[competitiveHarmEventKey]struct{}),
		seenDeaths:       make(map[competitiveDurabilityDeathKey]struct{}),
		pendingReports:   make(map[uint16]byte, len(participants)),
		deathsBeforeHarm: make(map[competitiveDurabilityDeathKey]struct{}),
	}
	for _, participant := range participants {
		state.remaining[participant.PlayerID] = limit
	}
	return state
}

func (battle *CompetitiveBattle) UsesNativeDurability() bool {
	if battle == nil {
		return false
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	return battle.nativeDurability != nil
}

// AuthorizesNativeDurabilityDeathReporter reflects the producers present in
// the rule-7/8 client. The elected rule object, the victim whose local UI
// reached zero, and a dynamic rule NPC running under that elected object may
// publish the observation. An ordinary peer still cannot eliminate another
// participant.
func (battle *CompetitiveBattle) AuthorizesNativeDurabilityDeathReporter(reporterID, playerID uint16) bool {
	if battle == nil || reporterID == 0 || playerID == 0 {
		return false
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	if battle.nativeDurability == nil {
		return false
	}
	if _, ok := battle.nativeDurability.remaining[playerID]; !ok {
		return false
	}
	return reporterID == playerID || reporterID == battle.arbitratorID ||
		(battle.arbitratorID != 0 && battle.nativeNPCAlive[reporterID])
}

// RecordNativeHarm mirrors the four-drop life display used by rules 7 and 8.
// Client.exe emits LossHP=0 for this path: it is a hit marker, not a scalar.
// Each non-avatar event removes exactly one native life layer. IsAvatar
// consumes the transformed shell and leaves the underlying durability intact.
func (battle *CompetitiveBattle) RecordNativeHarm(playerID uint16, clientTime uint32, posX, posY uint16, isAvatar bool, lossHP uint16) (remaining byte, fresh bool, err error) {
	if battle == nil {
		return 0, false, fmt.Errorf("competitive battle is nil")
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	if battle.nativeDurability == nil {
		return 0, false, fmt.Errorf("competitive battle does not use native durability")
	}
	remaining, ok := battle.nativeDurability.remaining[playerID]
	if !ok {
		return 0, false, fmt.Errorf("native-durability player %d is not a participant", playerID)
	}
	key := competitiveHarmEventKey{playerID: playerID, clientTime: clientTime, posX: posX, posY: posY, isAvatar: isAvatar, lossHP: lossHP}
	if _, duplicate := battle.nativeDurability.seen[key]; duplicate {
		return remaining, false, nil
	}
	if battle.concluded || battle.departed[playerID] || battle.nativeDurability.deathConfirmed[playerID] {
		return remaining, false, nil
	}
	battle.nativeDurability.seen[key] = struct{}{}
	if !isAvatar {
		hit := competitiveDurabilityDeathKey{playerID: playerID, clientTime: clientTime, posX: posX, posY: posY}
		if _, counted := battle.nativeDurability.deathsBeforeHarm[hit]; counted {
			delete(battle.nativeDurability.deathsBeforeHarm, hit)
			// This is still the first harm record. Only its server-side life
			// charge was already applied; native Type-2 owns the peer scene.
			return remaining, true, nil
		}
	}
	// The native consumer returns before emitting 0x0FA7 for avatar damage.
	// A shell hit must not reserve a report from a later body/NPC hit.
	if !isAvatar && battle.nativeDurability.pendingReports[playerID] < ^byte(0) {
		battle.nativeDurability.pendingReports[playerID]++
	}
	if !isAvatar && remaining > 0 {
		remaining--
		battle.nativeDurability.remaining[playerID] = remaining
	}
	return remaining, true, nil
}

// RecordNativeDurabilityDeath consumes the per-layer 0x0FA7 emitted by the
// rule-7/8 client. A preceding 0x10F4 and this record describe one hit; when
// no 0x10F4 exists, 0x0FA7 itself consumes the layer. This covers roaming
// rule NPC kills without double-counting fixed-device/player damage.
func (battle *CompetitiveBattle) RecordNativeDurabilityDeath(playerID uint16, clientTime uint32, posX, posY uint16) (CompetitiveResolution, bool, error) {
	if battle == nil {
		return CompetitiveResolution{}, false, fmt.Errorf("competitive battle is nil")
	}
	battle.mu.Lock()
	defer battle.mu.Unlock()
	if battle.nativeDurability == nil {
		return CompetitiveResolution{}, false, fmt.Errorf("competitive battle does not use native durability")
	}
	remaining, ok := battle.nativeDurability.remaining[playerID]
	if !ok {
		return CompetitiveResolution{}, false, fmt.Errorf("native-durability death player %d is not a participant", playerID)
	}
	if battle.concluded || battle.nativeDurability.deathConfirmed[playerID] {
		return battle.resolution(false), false, nil
	}
	key := competitiveDurabilityDeathKey{playerID: playerID, clientTime: clientTime, posX: posX, posY: posY}
	if _, duplicate := battle.nativeDurability.seenDeaths[key]; duplicate {
		return battle.resolution(false), false, nil
	}
	battle.nativeDurability.seenDeaths[key] = struct{}{}
	if pending := battle.nativeDurability.pendingReports[playerID]; pending > 0 {
		battle.nativeDurability.pendingReports[playerID] = pending - 1
	} else if remaining > 0 {
		remaining--
		battle.nativeDurability.remaining[playerID] = remaining
		battle.nativeDurability.deathsBeforeHarm[key] = struct{}{}
	}
	if remaining != 0 {
		return battle.resolution(false), false, nil
	}
	battle.nativeDurability.deathConfirmed[playerID] = true
	battle.alive[playerID] = false
	battle.trapped[playerID] = false
	battle.pendingDeath[playerID] = false
	if len(battle.livingTeams()) > 1 {
		return battle.resolution(false), true, nil
	}
	battle.concluded = true
	return battle.resolution(true), true, nil
}
