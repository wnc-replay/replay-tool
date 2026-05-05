package analysis

import (
	"bytes"
	"encoding/binary"
	"sort"
)

// ExtractBinaryFeedback parses kill/death/DBNO events directly from the binary.
// Kill signature: 22 D9 13 3C BA
// DBNO signature: 22 96 E2 29 7F (within ±70 bytes of kill)
func ExtractBinaryFeedback(data []byte, ticks []TimerTick, totalDuration float32) []BinaryMatchEvent {
	killSig := []byte{0x22, 0xD9, 0x13, 0x3C, 0xBA}
	dbnoSig := []byte{0x22, 0x96, 0xE2, 0x29, 0x7F}

	type eventKey struct {
		typ, attacker, target string
	}
	seen := make(map[eventKey]bool)
	var events []BinaryMatchEvent

	for i := 0; i+5 < len(data); i++ {
		if data[i] != 0x22 || !bytes.Equal(data[i:i+5], killSig) {
			continue
		}

		off := i + 5

		// Attacker username: 1-byte length + string
		if off >= len(data) {
			continue
		}
		attackerLen := int(data[off])
		off++
		if attackerLen > 64 || off+attackerLen > len(data) {
			continue
		}
		attacker := string(data[off : off+attackerLen])
		off += attackerLen

		// Skip 15 bytes
		off += 15
		if off >= len(data) {
			continue
		}

		// Target username: 1-byte length + string
		targetLen := int(data[off])
		off++
		if targetLen > 64 || off+targetLen > len(data) {
			continue
		}
		target := string(data[off : off+targetLen])
		off += targetLen

		// Skip 56 bytes to headshot field
		off += 56
		if off >= len(data) {
			continue
		}
		headshot := data[off] == 1

		// Validate
		if len(target) == 0 || !isPrintableASCII(target) {
			continue
		}
		if len(attacker) > 0 && !isPrintableASCII(attacker) {
			continue
		}

		// Check for DBNO marker within ±70 bytes
		isDBNO := false
		searchStart := i - 70
		if searchStart < 0 {
			searchStart = 0
		}
		searchEnd := i + 70
		if searchEnd+5 > len(data) {
			searchEnd = len(data) - 5
		}
		for j := searchStart; j <= searchEnd; j++ {
			if data[j] == 0x22 && bytes.Equal(data[j:j+5], dbnoSig) {
				isDBNO = true
				break
			}
		}

		evType := "kill"
		if len(attacker) == 0 {
			evType = "death"
		}

		key := eventKey{evType, attacker, target}
		if seen[key] {
			continue
		}
		seen[key] = true

		t := tickOffsetToElapsed(int64(i), ticks, totalDuration)
		events = append(events, BinaryMatchEvent{
			Offset:   int64(i),
			Type:     evType,
			Attacker: attacker,
			Target:   target,
			Headshot: headshot,
			TimeSecs: t,
		})

		// Emit separate DBNO event
		if isDBNO {
			dbnoKey := eventKey{"dbno", attacker, target}
			if !seen[dbnoKey] {
				seen[dbnoKey] = true
				events = append(events, BinaryMatchEvent{
					Offset:   int64(i),
					Type:     "dbno",
					Attacker: attacker,
					Target:   target,
					Headshot: headshot,
					TimeSecs: t,
				})
			}
		}
	}

	return events
}

// tickOffsetToElapsed converts a binary offset to elapsed seconds by interpolating
// within the range spanned by timer ticks, which is more accurate than using the
// full data length as the denominator.
func tickOffsetToElapsed(offset int64, ticks []TimerTick, totalDuration float32) float64 {
	if len(ticks) < 2 || totalDuration <= 0 {
		return 0
	}
	first := ticks[0].Offset
	last := ticks[len(ticks)-1].Offset
	if last <= first {
		return 0
	}
	// Binary search for the first tick at or after offset
	lo, hi := 0, len(ticks)-1
	for lo < hi {
		mid := (lo + hi) / 2
		if ticks[mid].Offset < offset {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	// Clamp to tick range
	if offset <= first {
		return 0
	}
	if offset >= last {
		return float64(totalDuration)
	}
	// Interpolate between surrounding ticks
	t0 := ticks[lo-1]
	t1 := ticks[lo]
	segFrac := float64(offset-t0.Offset) / float64(t1.Offset-t0.Offset)
	seg0 := float64(t0.Offset-first) / float64(last-first) * float64(totalDuration)
	seg1 := float64(t1.Offset-first) / float64(last-first) * float64(totalDuration)
	return seg0 + segFrac*(seg1-seg0)
}

// ExtractGameActions scans for reinforce and gadget deploy binary patterns.
// Only scans within the region anchored by timer ticks (avoids header/footer noise).
func ExtractGameActions(data []byte, ticks []TimerTick) []GameAction {
	reinforcePat := []byte{0x46, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x04, 0x35}
	gadgetPat := []byte{0x50, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x04, 0x3F}

	totalDuration := RoundDurationFromTicks(ticks)

	// Restrict scan to the region spanned by timer ticks (between first and last tick)
	// to avoid false-positive matches in the replay header or post-round data.
	scanStart := 0
	scanEnd := len(data)
	for _, t := range ticks {
		if t.Offset > 1000 && t.Seconds > 0 {
			off := int(t.Offset)
			if scanStart == 0 || off < scanStart {
				scanStart = off
			}
			if off > scanEnd {
				scanEnd = off
			}
		}
	}

	var actions []GameAction

	for i := scanStart; i+10 <= scanEnd; i++ {
		var actionType string
		if bytes.Equal(data[i:i+10], reinforcePat) {
			actionType = "reinforce"
		} else if bytes.Equal(data[i:i+10], gadgetPat) {
			actionType = "gadget_deploy"
		}
		if actionType == "" {
			continue
		}

		timeSecs := tickOffsetToElapsed(int64(i), ticks, totalDuration)

		// Skip boundary-clamped results (timeSecs==0 often means offset is before first tick)
		if timeSecs <= 0 {
			continue
		}

		// Dedup: skip if within 3 seconds of previous same-type action
		dup := false
		for _, prev := range actions {
			if prev.Type == actionType {
				dt := timeSecs - prev.TimeSecs
				if dt < 0 {
					dt = -dt
				}
				if dt < 3 {
					dup = true
					break
				}
			}
		}
		if dup {
			continue
		}

		actions = append(actions, GameAction{
			Type:     actionType,
			TimeSecs: timeSecs,
			Offset:   i,
		})
	}

	return actions
}

// ExtractPlayerInitOrder returns entity refs that appear with SPAWN counter=494
// (the player-entity counter value) in the order they first appear in the binary.
// This order matches the header player order and can be used as a mapping fallback.
func ExtractPlayerInitOrder(data []byte) []uint32 {
	pat := []byte{0x61, 0x73, 0x85, 0xFE}
	seen := make(map[uint32]bool)
	var order []uint32

	for i := 16; i+10 < len(data); i++ {
		if data[i] != pat[0] || data[i+1] != pat[1] || data[i+2] != pat[2] || data[i+3] != pat[3] {
			continue
		}
		counter := uint16(data[i+8]) | uint16(data[i+9])<<8
		if counter != 494 {
			continue
		}
		entityRef := binary.LittleEndian.Uint32(data[i-12 : i-8])
		if entityRef>>24 < 0xF0 {
			continue
		}
		if !seen[entityRef] {
			seen[entityRef] = true
			order = append(order, entityRef)
		}
	}
	return order
}

// MapPlayersFromInitBlocks builds an entity→playerIndex map using the init-block
// appearance order of player entities (counter=494). Falls back gracefully when
// fewer entities are found than expected.
func MapPlayersFromInitBlocks(data []byte, numPlayers int) map[uint32]int {
	order := ExtractPlayerInitOrder(data)
	result := make(map[uint32]int)
	for i, eid := range order {
		if i >= numPlayers {
			break
		}
		result[eid] = i
	}
	return result
}

// ExtractSpawnCounters reads entity SPAWN counter values.
// Pattern: archetype 0xFE857361, counter u16 at +8.
func ExtractSpawnCounters(data []byte) map[uint32]uint32 {
	result := make(map[uint32]uint32)
	pat := []byte{0x61, 0x73, 0x85, 0xFE}

	for i := 16; i+10 < len(data); i++ {
		if data[i] != pat[0] || data[i+1] != pat[1] || data[i+2] != pat[2] || data[i+3] != pat[3] {
			continue
		}
		entityRef := binary.LittleEndian.Uint32(data[i-12 : i-8])
		counter := uint16(data[i+8]) | uint16(data[i+9])<<8
		if _, exists := result[entityRef]; !exists {
			result[entityRef] = uint32(counter)
		}
	}

	return result
}

// ClassifyEntities classifies non-player entities based on SPAWN counters
// and FC-UPDATE flags.
func ClassifyEntities(tracks []*internalTrack, counters map[uint32]uint32,
	data []byte, players []PlayerInfo) {

	for _, tr := range tracks {
		counter, hasCounter := counters[tr.EntityID]
		tr.SpawnCounter = counter

		if !hasCounter {
			continue
		}

		switch counter {
		case 154: // drone
			// Will be classified as "drone" in SplitTracks
		case 146: // gadget
			tr.IsGadget = true
		case 130: // barricade
			tr.IsBarricade = true
			tr.BarricadeType = "barricade"
		case 138, 254: // weapon
			tr.IsWeapon = true
		}
	}
}

// ExtractLoadouts scans binary header for equipment loadout records.
// Uses auxHash slot identifiers to classify items into loadout slots.
// Each record: [GameID u64] [auxHash u32] [category u32] (16 bytes)
func ExtractLoadouts(data []byte, players []PlayerInfo) []PlayerLoadout {
	if len(data) < 192 {
		return nil
	}

	const (
		catWep  = 0x0A
		catGadg = 0x03

		// CRC32 hashes of slot names
		slotPrimaryWeapon   uint32 = 3268402276 // "PrimaryWeapon"
		slotMeleeWeapon     uint32 = 1696241262 // "MeleeWeapon"
		slotReinforcement   uint32 = 2606078005 // "Reinforcement"
		slotSecondaryWeapon uint32 = 1893246388 // "SecondaryWeapon"
		slotSecondaryGadget uint32 = 2947831256 // "SecondaryGadget"
	)

	scanLimit := len(data) / 4
	if scanLimit < 1024 {
		scanLimit = len(data)
	}

	// Scan for all records with known slot auxHash values
	type slotRecord struct {
		offset  int
		gameID  uint64
		auxHash uint32
		cat     uint32
	}
	var records []slotRecord

	for off := 0; off+16 <= scanLimit; off++ {
		auxHash := binary.LittleEndian.Uint32(data[off+8 : off+12])

		// Filter to known slot hashes
		switch auxHash {
		case slotPrimaryWeapon, slotMeleeWeapon, slotReinforcement, slotSecondaryWeapon, slotSecondaryGadget:
			// Valid slot hash
		default:
			continue
		}

		gameID := binary.LittleEndian.Uint64(data[off : off+8])
		cat := binary.LittleEndian.Uint32(data[off+12 : off+16])

		// Validate game ID format
		hiByte := (gameID >> 32) & 0xFF
		upper3 := gameID >> 40
		if gameID == 0 || upper3 != 0 || hiByte == 0 {
			continue
		}

		// Category must be weapon (10) or gadget (3)
		if cat != catWep && cat != catGadg {
			continue
		}

		records = append(records, slotRecord{off, gameID, auxHash, cat})
	}

	if len(records) == 0 {
		return nil
	}

	// Group records into loadout blocks by proximity
	// Gaps: ~48 between slots in group, ~192 between weapon/gadget groups, ~202+ between players
	// Use 195 to include weapon+gadget groups but split between players
	const blockGap = 195
	var blocks [][]slotRecord
	var currentBlock []slotRecord

	for i, rec := range records {
		if i == 0 {
			currentBlock = append(currentBlock, rec)
			continue
		}

		// Check if this record is near the previous one
		prevOff := records[i-1].offset
		if rec.offset-prevOff <= blockGap {
			currentBlock = append(currentBlock, rec)
		} else {
			// Start new block
			if len(currentBlock) > 0 {
				blocks = append(blocks, currentBlock)
			}
			currentBlock = []slotRecord{rec}
		}
	}
	if len(currentBlock) > 0 {
		blocks = append(blocks, currentBlock)
	}

	// Build loadouts from blocks
	var loadouts []PlayerLoadout
	for idx, block := range blocks {
		if len(block) < 2 {
			continue // Need at least 2 items for a valid loadout
		}

		pl := PlayerLoadout{
			PlayerIndex: idx,
		}

		for _, rec := range block {
			item := LoadoutItem{
				GameID:   rec.gameID,
				AuxHash:  rec.auxHash,
				Name:     resolveItemName(rec.gameID),
				Category: int(rec.cat),
			}

			switch rec.auxHash {
			case slotPrimaryWeapon:
				if pl.PrimaryWeapon.GameID == 0 {
					pl.PrimaryWeapon = item
				}
			case slotMeleeWeapon:
				if pl.SecondaryWeapon.GameID == 0 {
					pl.SecondaryWeapon = item
				}
			case slotReinforcement:
				if pl.PrimaryGadget.GameID == 0 {
					pl.PrimaryGadget = item
				}
			case slotSecondaryWeapon:
				if pl.SecondaryWeapon.GameID == 0 {
					pl.SecondaryWeapon = item
				}
			case slotSecondaryGadget:
				if pl.SecondaryGadget.GameID == 0 {
					pl.SecondaryGadget = item
				}
			}
		}

		loadouts = append(loadouts, pl)
	}

	// Match loadouts to players (blocks appear in header player order)
	for i := range loadouts {
		if i < len(players) {
			loadouts[i].PlayerIndex = i
		}
	}

	return loadouts
}

// ExtractDestructionEventsFromHealth derives entity destruction events from health updates.
// An entity is "destroyed" when its health transitions from positive to zero.
func ExtractDestructionEventsFromHealth(allHealth []HealthUpdate, entities []EntityTrack) []DestructionEvent {
	// Build entity ref → EntityTrack lookup
	entityMap := make(map[uint32]*EntityTrack, len(entities))
	for i := range entities {
		entityMap[entities[i].EntityID] = &entities[i]
	}

	// Collect non-player health updates grouped by entity ref
	entityHealthMap := make(map[uint32][]HealthUpdate)
	for _, h := range allHealth {
		if h.PlayerIndex >= 0 || h.EntityRef == 0 {
			continue
		}
		entityHealthMap[h.EntityRef] = append(entityHealthMap[h.EntityRef], h)
	}

	var events []DestructionEvent
	for entityRef, updates := range entityHealthMap {
		sort.Slice(updates, func(i, j int) bool {
			return updates[i].BinOffset < updates[j].BinOffset
		})
		prevHP := float32(-1)
		for _, u := range updates {
			if prevHP > 0 && u.Health == 0 {
				entityType := "unknown"
				gadgetType := ""
				entityHex := ""
				if et, ok := entityMap[entityRef]; ok {
					entityType = et.Type
					gadgetType = et.GadgetType
					entityHex = et.EntityHex
				}
				events = append(events, DestructionEvent{
					EntityID:   entityRef,
					EntityHex:  entityHex,
					EntityType: entityType,
					GadgetType: gadgetType,
					TimeSecs:   u.TimeSecs,
					BinOffset:  int64(u.BinOffset),
				})
				break
			}
			prevHP = u.Health
		}
	}
	return events
}

// ExtractReviveEvents detects when a downed player is revived by scanning health updates
// for HP transitions from near-zero (≤5) back to a meaningful value (≥15).
func ExtractReviveEvents(healthUpdates []HealthUpdate) []ReviveEvent {
	// Group by player, sort by offset
	byPlayer := make(map[int][]HealthUpdate)
	for _, h := range healthUpdates {
		if h.PlayerIndex >= 0 {
			byPlayer[h.PlayerIndex] = append(byPlayer[h.PlayerIndex], h)
		}
	}
	var events []ReviveEvent
	for pIdx, updates := range byPlayer {
		sort.Slice(updates, func(i, j int) bool {
			return updates[i].BinOffset < updates[j].BinOffset
		})
		prevHP := float32(-1)
		for _, u := range updates {
			if prevHP >= 0 && prevHP <= 5 && u.Health >= 15 {
				events = append(events, ReviveEvent{
					PlayerIndex: pIdx,
					TimeSecs:    u.TimeSecs,
					BinOffset:   u.BinOffset,
				})
			}
			prevHP = u.Health
		}
	}
	return events
}

// ExtractEquipmentSwitches detects weapon switches by tracking which weapon entity
// has the most recent ammo event per player. A switch is emitted whenever the active
// weapon EID changes for a player.
func ExtractEquipmentSwitches(ammoEvents []AmmoEvent, weapons map[int]*PlayerWeapons, ticks []TimerTick, totalDuration float32) []EquipmentSwitchEvent {
	if len(ammoEvents) == 0 || len(weapons) == 0 {
		return nil
	}

	// Build weapon EID → player index and whether it is primary
	eidToPlayer := make(map[uint32]int)
	isPrimary := make(map[uint32]bool)
	for pIdx, pw := range weapons {
		for _, w := range pw.AllWeapons {
			eidToPlayer[w.WeaponEID] = pIdx
			isPrimary[w.WeaponEID] = w.IsPrimary
		}
	}

	// Sort events by binary offset
	sorted := make([]AmmoEvent, len(ammoEvents))
	copy(sorted, ammoEvents)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Offset < sorted[j].Offset
	})

	lastWeapon := make(map[int]uint32)
	var events []EquipmentSwitchEvent

	for _, ev := range sorted {
		pIdx, ok := eidToPlayer[ev.WeaponEID]
		if !ok {
			continue
		}
		prev, hasPrev := lastWeapon[pIdx]
		lastWeapon[pIdx] = ev.WeaponEID
		if !hasPrev || prev == ev.WeaponEID {
			continue
		}
		t := float32(tickOffsetToElapsed(ev.Offset, ticks, totalDuration))
		events = append(events, EquipmentSwitchEvent{
			PlayerIndex:  pIdx,
			FromWeaponID: prev,
			ToWeaponID:   ev.WeaponEID,
			IsPrimaryNow: isPrimary[ev.WeaponEID],
			TimeSecs:     t,
			BinOffset:    ev.Offset,
		})
	}
	return events
}

// BuildGameEvents assembles a sorted unified event timeline suitable for replay visualization.
func BuildGameEvents(feedback []BinaryMatchEvent, phases []TimerPhase, duration float32) []GameEvent {
	var events []GameEvent

	for _, f := range feedback {
		var text string
		switch f.Type {
		case "kill":
			if f.Attacker != "" {
				text = f.Attacker + " killed " + f.Target
			} else {
				text = f.Target + " died"
			}
		case "death":
			text = f.Target + " died"
		case "dbno":
			if f.Attacker != "" {
				text = f.Attacker + " downed " + f.Target
			} else {
				text = f.Target + " downed"
			}
		}
		events = append(events, GameEvent{
			Type:     f.Type,
			TimeSecs: float32(f.TimeSecs),
			Text:     text,
			Headshot: f.Headshot,
		})
	}

	if duration > 0 {
		events = append(events, GameEvent{
			Type:     "round_end",
			TimeSecs: duration,
			Text:     "Round ended",
		})
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].TimeSecs < events[j].TimeSecs
	})
	return events
}

// ExtractOperatorSwaps scans for mid-round attacker operator swap events.
//
// Binary layout after pattern [22 A9 26 0B E4] (5 bytes already at i):
//
//	i+5      : skip byte (type indicator consumed by Uint64)
//	i+6..13  : new operator uint64 LE
//
// Pre-Y9S3 path (used when data looks like pre-caster-view era):
//
//	i+14..18 : 5 skip bytes
//	i+19..22 : DissectID [4]byte (player identifier)
//
// For Y10S4+ replays this function is a fallback — the library's reconcileOperatorSwaps
// already emits OperatorSwap entries via reader.MatchFeedback which main.go prefers.
// This scanner handles pre-library cases and deduces operator name from the game ID.
func ExtractOperatorSwaps(data []byte, players []PlayerInfo, ticks []TimerTick) []OperatorSwapEvent {
	pat := []byte{0x22, 0xA9, 0x26, 0x0B, 0xE4}
	totalDuration := RoundDurationFromTicks(ticks)
	var events []OperatorSwapEvent

	for i := 0; i+25 <= len(data); i++ {
		if data[i] != pat[0] || data[i+1] != pat[1] || data[i+2] != pat[2] ||
			data[i+3] != pat[3] || data[i+4] != pat[4] {
			continue
		}

		// i+5: skip byte, i+6..i+13: operator uint64 LE
		opID := binary.LittleEndian.Uint64(data[i+6 : i+14])
		if opID == 0 {
			continue
		}
		opName := resolveItemName(opID)

		// i+14..i+18: 5 skip bytes, i+19..i+22: DissectID
		dissectID := data[i+19 : i+23]
		pIdx := -1
		// Try to match DissectID to a player via operator cross-reference
		// (full DissectID matching requires the dissect library; here we match by operator name)
		for j, p := range players {
			if p.Operator == opName {
				pIdx = j
				break
			}
		}

		t := float32(tickOffsetToElapsed(int64(i), ticks, totalDuration))
		ev := OperatorSwapEvent{
			PlayerIndex: pIdx,
			ToOperator:  opName,
			Offset:      int64(i),
			TimeSecs:    t,
		}
		// Attach DissectID as hex for debugging if player not matched
		if pIdx < 0 {
			ev.ToOperator = resolveItemName(opID)
			_ = dissectID
		}
		events = append(events, ev)
	}
	return events
}
