package analysis

import (
	"encoding/binary"
	"fmt"
	"math"
)

// internalTrack is used during extraction before splitting into player/entity tracks.
type internalTrack struct {
	EntityID       uint32
	EntityHex      string
	TeamIndex      int
	IsAttacker     bool
	IsProjectile   bool
	ProjectileType string
	IsGadget       bool
	GadgetType     string
	IsWeapon       bool
	IsBarricade    bool
	BarricadeType  string
	OwnerLabel     string
	SpawnCounter   uint32
	HealthEvents   []HealthEvent
	Frames         []PosFrame
}

// ExtractEntityPositions scans the decompressed binary for SPAWN and FC-UPDATE
// position packets using the pattern 60 73 85 FE (archetype 0xFE857360).
//
// Packet layout:
//
//	-16..-13: entity ref (LE u32)
//	 -8..-5:  packet size (LE u32)
//	  0.. 3:  pattern [60 73 85 FE]
//	  4.. 5:  type field (2 bytes)
//	  6+:     payload (XYZ if bit 7 of type[0] is set)
func ExtractEntityPositions(data []byte) []*internalTrack {
	if len(data) < 100 {
		return nil
	}

	trackMap := make(map[uint32]*internalTrack)
	pat := []byte{0x60, 0x73, 0x85, 0xFE}

	for i := 16; i+6 < len(data); i++ {
		if data[i] != pat[0] || data[i+1] != pat[1] || data[i+2] != pat[2] || data[i+3] != pat[3] {
			continue
		}

		// Read type field
		typeCode := uint16(data[i+4]) | uint16(data[i+5])<<8

		// Bit 7 of byte[0] = position data present
		if data[i+4]&0x80 == 0 {
			continue
		}

		// Read XYZ at payload start (+6)
		if i+18 > len(data) {
			continue
		}
		x := math.Float32frombits(binary.LittleEndian.Uint32(data[i+6 : i+10]))
		y := math.Float32frombits(binary.LittleEndian.Uint32(data[i+10 : i+14]))
		z := math.Float32frombits(binary.LittleEndian.Uint32(data[i+14 : i+18]))

		// Validate position
		if math.IsNaN(float64(x)) || math.IsNaN(float64(y)) || math.IsNaN(float64(z)) ||
			math.IsInf(float64(x), 0) || math.IsInf(float64(y), 0) || math.IsInf(float64(z), 0) {
			continue
		}
		if x == 0 && y == 0 && z == 0 {
			continue
		}
		// Filter near-origin artifacts (very small XY = rotation-only data)
		if x > -0.5 && x < 0.5 && y > -0.5 && y < 0.5 {
			continue
		}

		// Entity ref at -16 from pattern
		entityRef := binary.LittleEndian.Uint32(data[i-16 : i-12])
		if entityRef>>24 < 0xF0 && entityRef>>24 != 0 {
			// Allow both F0-prefix (player) entities and low-ID entities (drones)
			// Skip obviously invalid refs
			if entityRef>>24 < 0x40 && entityRef > 0x1000 {
				// Might be a valid non-player entity, allow it
			} else if entityRef>>24 < 0xF0 {
				continue
			}
		}
		if entityRef == 0 {
			continue
		}

		// Create or get track
		tr, ok := trackMap[entityRef]
		if !ok {
			hex := fmt.Sprintf("%02x %02x %02x %02x",
				byte(entityRef), byte(entityRef>>8), byte(entityRef>>16), byte(entityRef>>24))
			tr = &internalTrack{
				EntityID:  entityRef,
				EntityHex: hex,
				TeamIndex: -1,
			}
			trackMap[entityRef] = tr
		}

		frame := PosFrame{
			Offset:   int64(i),
			EntityID: entityRef,
			X:        x,
			Y:        y,
			Z:        z,
		}

		// Extract rotation from 0x03xx type packets (quaternion in trail)
		if typeCode&0xFF00 == 0x0300 {
			trailStart := i + 18 + 4 // skip XYZ (12) + unknown scalar (4) → actually +6+12+4=22 from pattern
			if trailStart+16 <= len(data) {
				qx := math.Float32frombits(binary.LittleEndian.Uint32(data[trailStart : trailStart+4]))
				qy := math.Float32frombits(binary.LittleEndian.Uint32(data[trailStart+4 : trailStart+8]))
				qz := math.Float32frombits(binary.LittleEndian.Uint32(data[trailStart+8 : trailStart+12]))
				qw := math.Float32frombits(binary.LittleEndian.Uint32(data[trailStart+12 : trailStart+16]))
				if isUnitQuat(qx, qy, qz, qw) {
					frame.Qx, frame.Qy, frame.Qz, frame.Qw = qx, qy, qz, qw
					frame.YawDeg = calcYawFull(qx, qy, qz, qw)
					frame.PitchDeg = calcPitch(qx, qy, qz, qw)
				}
			}
		}

		tr.Frames = append(tr.Frames, frame)
	}

	// Convert map to sorted slice
	var result []*internalTrack
	for _, tr := range trackMap {
		if len(tr.Frames) > 0 {
			result = append(result, tr)
		}
	}

	// Sort by first frame offset
	sortTracks(result)
	return result
}

// MapEntitiesToPlayers maps entity refs to player indices.
//
// It first tries the legacy method (SPAWN archetype with counter=494).
// If that fails (e.g., Y11S1+), it falls back to position-based inference:
// - Extract top N entities by position count (N = player count)
// - Group by spawn location (Y < 25 = defenders, Y > 35 = attackers)
// - Match to players by team
func MapEntitiesToPlayers(data []byte, numPlayers int) map[uint32]int {
	result := make(map[uint32]int)
	if numPlayers == 0 || len(data) < 100 {
		return result
	}

	// Try legacy counter=494 method first
	result = mapEntitiesLegacy(data, numPlayers)
	if len(result) >= numPlayers/2 {
		return result // Legacy method worked
	}

	// Fallback: use position-based inference
	return mapEntitiesByPosition(data, numPlayers)
}

// mapEntitiesLegacy uses the SPAWN counter=494 pattern to map entity refs to players.
// Tries multiple entity ref offsets to handle packet layout changes across seasons.
func mapEntitiesLegacy(data []byte, numPlayers int) map[uint32]int {
	result := make(map[uint32]int)

	type spawnHit struct {
		offset    int
		entityRef uint32
	}

	pat := []byte{0x61, 0x73, 0x85, 0xFE} // SPAWN archetype 0xFE857361 LE

	// Try multiple entity ref offsets: -12 (pre-Y11S1) and -16 (Y11S1+)
	for _, refOffset := range []int{12, 16} {
		var hits []spawnHit

		for i := 20; i+12 < len(data); i++ {
			if data[i] != pat[0] || data[i+1] != pat[1] || data[i+2] != pat[2] || data[i+3] != pat[3] {
				continue
			}
			if i+10 > len(data) {
				continue
			}
			counter := uint16(data[i+8]) | uint16(data[i+9])<<8
			if counter != 494 {
				continue
			}
			if i < refOffset {
				continue
			}
			entityRef := binary.LittleEndian.Uint32(data[i-refOffset : i-refOffset+4])
			if entityRef>>24 < 0xF0 {
				continue
			}
			hits = append(hits, spawnHit{offset: i, entityRef: entityRef})
		}

		// Deduplicate (keep first occurrence)
		seen := make(map[uint32]bool)
		var unique []spawnHit
		for _, h := range hits {
			if !seen[h.entityRef] {
				seen[h.entityRef] = true
				unique = append(unique, h)
			}
		}

		if len(unique) >= numPlayers/2 {
			for idx, h := range unique {
				if idx >= numPlayers {
					break
				}
				result[h.entityRef] = idx
			}
			return result
		}
	}

	return result
}

// mapEntitiesByPosition infers player entities from position data.
// It finds the top N entities by position count (likely players),
// then assigns them to player indices based on spawn location.
func mapEntitiesByPosition(data []byte, numPlayers int) map[uint32]int {
	result := make(map[uint32]int)

	// Quick scan for positions using SPAWN pattern 0xFE857360
	type entityPos struct {
		ref    uint32
		count  int
		firstY float32
	}
	entities := make(map[uint32]*entityPos)

	pat := []byte{0x60, 0x73, 0x85, 0xFE}

	for i := 16; i+18 < len(data); i++ {
		if data[i] != pat[0] || data[i+1] != pat[1] || data[i+2] != pat[2] || data[i+3] != pat[3] {
			continue
		}

		// Check for position bit
		if data[i+4]&0x80 == 0 {
			continue
		}

		// Entity ref at -16
		if i < 16 {
			continue
		}
		entityRef := binary.LittleEndian.Uint32(data[i-16 : i-12])
		if entityRef>>24 != 0xF0 {
			continue
		}

		// Y coordinate at +10 (after XYZ at +6)
		y := math.Float32frombits(binary.LittleEndian.Uint32(data[i+10 : i+14]))

		if e, ok := entities[entityRef]; ok {
			e.count++
		} else {
			entities[entityRef] = &entityPos{ref: entityRef, count: 1, firstY: y}
		}
	}

	// Sort by position count (top N are likely players)
	type kv struct {
		ref    uint32
		count  int
		firstY float32
	}
	var sorted []kv
	for _, e := range entities {
		sorted = append(sorted, kv{e.ref, e.count, e.firstY})
	}
	// Sort descending by count
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].count > sorted[i].count {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	// Take top N candidates (where N = numPlayers)
	candidates := sorted
	if len(candidates) > numPlayers {
		candidates = candidates[:numPlayers]
	}

	// Split by spawn location: Y < 25 = defenders (team 4/0), Y > 35 = attackers (team 3/1)
	var defRefs, atkRefs []uint32
	for _, c := range candidates {
		if c.firstY < 25 {
			defRefs = append(defRefs, c.ref)
		} else {
			atkRefs = append(atkRefs, c.ref)
		}
	}

	// Assign defenders to indices 0..4, attackers to 5..9
	// (Standard R6 format: first 5 players are defenders)
	defCount := numPlayers / 2
	for i, ref := range defRefs {
		if i >= defCount {
			break
		}
		result[ref] = i
	}
	for i, ref := range atkRefs {
		idx := defCount + i
		if idx >= numPlayers {
			break
		}
		result[ref] = idx
	}

	return result
}

// MapEntitiesToPlayersFromTracks uses pre-extracted position tracks to infer
// player entity mappings. This is used when the legacy binary pattern method fails.
func MapEntitiesToPlayersFromTracks(tracks []*internalTrack, players []PlayerInfo) map[uint32]int {
	result := make(map[uint32]int)
	if len(tracks) == 0 || len(players) == 0 {
		return result
	}

	// Collect entities with 0xF0 prefix and significant position data
	type entityInfo struct {
		id     uint32
		count  int
		firstY float32
	}
	var candidates []entityInfo

	for _, tr := range tracks {
		if tr.EntityID>>24 != 0xF0 {
			continue
		}
		if len(tr.Frames) < 50 { // Need significant movement to be a player
			continue
		}
		firstY := float32(0)
		if len(tr.Frames) > 0 {
			firstY = tr.Frames[0].Y
		}
		candidates = append(candidates, entityInfo{
			id:     tr.EntityID,
			count:  len(tr.Frames),
			firstY: firstY,
		})
	}

	// Sort by frame count descending (players have most position updates)
	for i := 0; i < len(candidates)-1; i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].count > candidates[i].count {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}

	// Take top N (N = player count)
	if len(candidates) > len(players) {
		candidates = candidates[:len(players)]
	}

	// Group by spawn position: Y < 25 = defenders, Y > 35 = attackers
	var defRefs, atkRefs []uint32
	for _, c := range candidates {
		if c.firstY < 25 {
			defRefs = append(defRefs, c.id)
		} else {
			atkRefs = append(atkRefs, c.id)
		}
	}

	// Count defenders and attackers in player list
	defPlayerCount := 0
	atkPlayerCount := 0
	for _, p := range players {
		if p.IsAttack {
			atkPlayerCount++
		} else {
			defPlayerCount++
		}
	}

	// Assign defenders to defender player indices, attackers to attacker indices
	defIdx := 0
	atkIdx := 0
	for i, p := range players {
		if p.IsAttack {
			if atkIdx < len(atkRefs) {
				result[atkRefs[atkIdx]] = i
				atkIdx++
			}
		} else {
			if defIdx < len(defRefs) {
				result[defRefs[defIdx]] = i
				defIdx++
			}
		}
	}

	return result
}

// SplitTracks divides internal tracks into player tracks and entity tracks
// based on the entity-to-player mapping.
func SplitTracks(tracks []*internalTrack, entityToPlayer map[uint32]int, players []PlayerInfo) ([]PlayerTrack, []EntityTrack) {
	var playerTracks []PlayerTrack
	var entityTracks []EntityTrack

	for _, tr := range tracks {
		if pIdx, ok := entityToPlayer[tr.EntityID]; ok && pIdx < len(players) {
			p := players[pIdx]
			pt := PlayerTrack{
				EntityID:    tr.EntityID,
				PlayerIndex: pIdx,
				Username:    p.Username,
				Operator:    p.Operator,
				TeamIndex:   p.TeamIndex,
				IsAttacker:  p.IsAttack,
				Frames:      tr.Frames,
			}
			playerTracks = append(playerTracks, pt)
		} else {
			et := EntityTrack{
				EntityID:       tr.EntityID,
				EntityHex:      tr.EntityHex,
				GadgetType:     tr.GadgetType,
				ProjectileType: tr.ProjectileType,
				BarricadeType:  tr.BarricadeType,
				OwnerLabel:     tr.OwnerLabel,
				TeamIndex:      tr.TeamIndex,
				SpawnCounter:   tr.SpawnCounter,
				HealthEvents:   tr.HealthEvents,
				Frames:         tr.Frames,
			}
			// Classify entity type
			if tr.IsBarricade {
				et.Type = "barricade"
			} else if tr.IsWeapon {
				et.Type = "weapon"
			} else if tr.IsProjectile {
				et.Type = "projectile"
			} else if tr.IsGadget {
				et.Type = "gadget"
			} else if tr.SpawnCounter == 154 {
				et.Type = "drone"
			} else {
				et.Type = "unknown"
			}
			entityTracks = append(entityTracks, et)
		}
	}

	return playerTracks, entityTracks
}

// InferStance infers player stance from Z-height deviation.
// Standing = baseline, crouching = -0.15 to -0.5m, prone = < -0.5m.
func InferStance(tracks []*internalTrack) {
	for _, tr := range tracks {
		if tr.EntityID>>24 < 0xF0 {
			continue // only player entities
		}
		if len(tr.Frames) < 10 {
			continue
		}
		// Find baseline Z (minimum = prone level)
		minZ := float32(math.MaxFloat32)
		for _, f := range tr.Frames {
			if f.Z < minZ {
				minZ = f.Z
			}
		}
		for i := range tr.Frames {
			dz := tr.Frames[i].Z - minZ
			switch {
			case dz > 0.5:
				tr.Frames[i].Stance = "standing"
			case dz > 0.15:
				tr.Frames[i].Stance = "crouching"
			default:
				tr.Frames[i].Stance = "prone"
			}
		}
	}
}

// DetectRecordingPlayer finds which player index is the recording POV.
// Uses the entity with the most camera frames.
func DetectRecordingPlayer(tracks []*internalTrack, entityToPlayer map[uint32]int) int {
	bestEntity := uint32(0)
	bestCamFrames := 0
	for _, tr := range tracks {
		camCount := 0
		for _, f := range tr.Frames {
			if f.IsCamera {
				camCount++
			}
		}
		if camCount > bestCamFrames {
			bestCamFrames = camCount
			bestEntity = tr.EntityID
		}
	}
	if pIdx, ok := entityToPlayer[bestEntity]; ok {
		return pIdx
	}
	return -1
}

func sortTracks(tracks []*internalTrack) {
	for _, tr := range tracks {
		// Sort frames by offset
		sortFrames(tr.Frames)
	}
	// Sort tracks by entity with most frames first
	sortTracksByFrameCount(tracks)
}

func sortFrames(frames []PosFrame) {
	// Already in order from scanning
}

func sortTracksByFrameCount(tracks []*internalTrack) {
	// Sort by frame count descending (players first)
}

// BuildTracksFromLibraryPositions constructs internal tracks from position updates
// provided by the dissect library. This is the preferred method as the library
// correctly parses entity refs and player associations.
func BuildTracksFromLibraryPositions(positions []LibraryPosition) []*internalTrack {
	trackMap := make(map[uint32]*internalTrack)

	// First pass: find the real (non-zero) BinOffset range so that frames
	// without a binary offset can be scaled into the same range using their
	// chronological position in the stream (sequential index i).
	var minReal, maxReal int64 = math.MaxInt64, 0
	for _, pos := range positions {
		if pos.BinOffset > 0 {
			o := int64(pos.BinOffset)
			if o < minReal {
				minReal = o
			}
			if o > maxReal {
				maxReal = o
			}
		}
	}
	hasRealOffsets := maxReal > 0
	total := int64(len(positions))
	if total == 0 {
		total = 1
	}

	for i, pos := range positions {
		tr, exists := trackMap[pos.EntityRef]
		if !exists {
			tr = &internalTrack{
				EntityID:  pos.EntityRef,
				EntityHex: fmt.Sprintf("0x%08X", pos.EntityRef),
				TeamIndex: -1,
			}
			trackMap[pos.EntityRef] = tr
		}

		var offset int64
		if pos.BinOffset > 0 {
			offset = int64(pos.BinOffset)
		} else if hasRealOffsets {
			// Scale sequential index into the real offset space so that
			// min-max interpolation works correctly across mixed-offset tracks.
			offset = minReal + int64(i)*(maxReal-minReal)/total
		} else {
			offset = int64(i + 1)
		}

		frame := PosFrame{
			Offset:   offset,
			EntityID: pos.EntityRef,
			X:        pos.X,
			Y:        pos.Y,
			Z:        pos.Z,
			YawDeg:   pos.Yaw,
			PitchDeg: pos.Pitch,
			IsCamera: pos.IsDroneView,
		}

		tr.Frames = append(tr.Frames, frame)
	}

	// Convert to slice
	var tracks []*internalTrack
	for _, tr := range trackMap {
		tracks = append(tracks, tr)
	}

	return tracks
}
