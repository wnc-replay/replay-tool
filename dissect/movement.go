package dissect

import (
	"encoding/binary"
	"math"
	"sort"

	"github.com/rs/zerolog/log"
)

// PositionUpdate represents a position update for an entity.
// PlayerIndex is resolved after reading; EntityRef is a stable network object identifier.
type PositionUpdate struct {
	PlayerIndex int     `json:"playerIndex"` // index into Header.Players, -1 if unknown
	EntityRef   uint32  `json:"entityRef"`
	X           float32 `json:"x"`
	Y           float32 `json:"y"`
	Z           float32 `json:"z"`
	Yaw         float32 `json:"yaw,omitempty"`         // degrees, 0 = no rotation data
	Pitch       float32 `json:"pitch,omitempty"`       // degrees, positive = looking up
	HeadOffX    float32 `json:"hoX,omitempty"`         // head local offset X (lean)
	HeadOffY    float32 `json:"hoY,omitempty"`         // head local offset Y (nod)
	HeadOffZ    float32 `json:"hoZ,omitempty"`         // head local offset Z (stance)
	HeadQX      float32 `json:"hqX,omitempty"`         // head bone quaternion X
	HeadQY      float32 `json:"hqY,omitempty"`         // head bone quaternion Y
	HeadQZ      float32 `json:"hqZ,omitempty"`         // head bone quaternion Z
	HeadQW      float32 `json:"hqW,omitempty"`         // head bone quaternion W
	ChestOffX   float32 `json:"coX,omitempty"`         // chest local offset X
	ChestOffY   float32 `json:"coY,omitempty"`         // chest local offset Y
	ChestOffZ   float32 `json:"coZ,omitempty"`         // chest local offset Z
	ChestQX     float32 `json:"cqX,omitempty"`         // chest bone quaternion X
	ChestQY     float32 `json:"cqY,omitempty"`         // chest bone quaternion Y
	ChestQZ     float32 `json:"cqZ,omitempty"`         // chest bone quaternion Z
	ChestQW     float32 `json:"cqW,omitempty"`         // chest bone quaternion W
	Stance      string  `json:"stance,omitempty"`      // "standing", "crouching", "prone" (inferred from Z-height)
	IsDroneView bool    `json:"isDroneView,omitempty"` // true if player is viewing through a drone
	Seq         int     `json:"seq"`                   // sequence number within this round's position stream
	BinOffset   int     `json:"binOffset,omitempty"`   // byte offset in decompressed stream (for temporal matching)
	typeCode    uint16  // internal: FC-UPDATE type/flag field
}

// ScoreUpdate records a change in a player's cumulative score.
type ScoreUpdate struct {
	PlayerIndex int    `json:"playerIndex"`
	Username    string `json:"username"`
	PrevScore   int    `json:"prevScore"`
	NewScore    int    `json:"newScore"`
	Delta       int    `json:"delta"`     // points earned (newScore - prevScore)
	BinOffset   int    `json:"binOffset"` // binary offset for temporal ordering
}

// GameAction represents a detected game action event (reinforce, gadget deploy, etc.)
type GameAction struct {
	Type          string  `json:"type"`          // "reinforce", "gadget_deploy"
	TimeInSeconds float64 `json:"timeInSeconds"` // round time when action occurred
	BinOffset     int     `json:"binOffset"`     // binary offset in replay data
}

// DeathTiming records when a player likely died based on their last significant movement.
type DeathTiming struct {
	PlayerIndex      int     `json:"playerIndex"`
	LastMovementSeq  int     `json:"lastMovementSeq"`  // seq of last position with significant displacement
	LastMovementTime float64 `json:"lastMovementTime"` // time of last significant movement
	LastX            float32 `json:"lastX"`
	LastY            float32 `json:"lastY"`
	LastZ            float32 `json:"lastZ"`
}

// HealthUpdate represents a health value change for a player entity.
type HealthUpdate struct {
	PlayerIndex int     `json:"playerIndex"` // index into Header.Players, -1 if unknown
	Health      float32 `json:"health"`      // current health (0-100)
	BinOffset   int     `json:"binOffset"`   // binary offset for temporal ordering
}

// CameraFrame represents a camera look-direction sample (from spectator/first-person POV).
type CameraFrame struct {
	PlayerIndex int     `json:"playerIndex"` // which player's camera
	Qx          float32 `json:"qx"`          // quaternion X
	Qy          float32 `json:"qy"`          // quaternion Y
	Qz          float32 `json:"qz"`          // quaternion Z
	Qw          float32 `json:"qw"`          // quaternion W
	YawDeg      float32 `json:"yawDeg"`      // yaw in degrees
	PitchDeg    float32 `json:"pitchDeg"`    // pitch in degrees (YXZ Euler)
	BinOffset   int     `json:"binOffset"`   // binary offset for temporal ordering
}

// TimerTick represents a single round-timer tick extracted from the binary.
type TimerTick struct {
	Offset  int64   `json:"offset"`  // byte position in decompressed stream
	Seconds float32 `json:"seconds"` // seconds remaining (countdown)
}

// TimerPhase represents a detected round phase (prep or action) from timer tick gaps.
type TimerPhase struct {
	Name     string  `json:"name"`     // "prep" or "action"
	StartSec float32 `json:"startSec"` // seconds remaining when phase started (countdown, higher = earlier)
	EndSec   float32 `json:"endSec"`   // seconds remaining when phase ended (countdown, lower = later)
	Duration float32 `json:"duration"` // phase duration in seconds
}

// Float32 reads a little-endian float32 from the replay data.
func (r *Reader) Float32() (float32, error) {
	b, err := r.Bytes(4)
	if err != nil {
		return 0, err
	}
	bits := binary.LittleEndian.Uint32(b)
	return math.Float32frombits(bits), nil
}

// DroneEvent records a confirmed state transition detected from packet data.
// Connect: player's non-position packet type has bit 3 set (0x08) in byte[0]
//
//	AND the tail contains 04 FF <len> <droneRef> <0000>.
//
// Disconnect: player's next non-position packet loses the 04 FF marker
//
//	(bit 3 cleared, or different/no drone ref in tail).
type DroneEvent struct {
	PlayerRef uint32 `json:"playerRef"` // player entity ref
	DroneRef  uint32 `json:"droneRef"`  // drone entity ref
	Seq       int    `json:"seq"`       // position stream sequence number
	Connect   bool   `json:"connect"`   // true = connect (player entered drone), false = disconnect
}

// readMovement parses position data from the movement pattern (60 73 85 FE).
//
// Packet framing (bytes relative to pattern start):
//
//	-12..-9:  entity ref (LE uint32, network object ID with 0xF0xx upper bytes)
//	 -8..-5:  flags (usually 00000000 or 60000000)
//	 -4..-1:  packet size (LE uint32, includes pattern+type+payload)
//	  0.. 3:  pattern [60 73 85 FE]
//	  4.. 5:  type field (2 bytes, bitfield)
//	  6+:     payload
//
// Type field bit 7 of byte[0] (typCode & 0x0080) indicates XYZ position data
// at the start of the payload (3× float32 LE = 12 bytes). This covers:
//
//	0x1Fxx types — absolute spawn positions (initial round state)
//	0x03B8, 0x03B0, 0x03BC — full position + rotation updates (~19K/round)
//	0x01B0, 0x01B8, 0x01BC — position + rotation + property updates (~14K/round)
//	0x01C0 — position updates for secondary entities (~8K/round)
//
// Types WITHOUT bit 7 (0x0440, 0x0130, 0x0420, 0x0630, etc.) are property-only
// updates containing no position data.
func readMovement(r *Reader) error {
	startOffset := r.offset // first byte after the 4-byte pattern (= patternStart + 4)

	// Read 2-byte type field
	typeBytes, err := r.Bytes(2)
	if err != nil {
		return nil
	}

	typeCode := uint16(typeBytes[0]) | uint16(typeBytes[1])<<8

	// Drone view marker: flag 0x0880 indicates the player is viewing through a drone.
	// Record it against the entity for later propagation to position updates.
	if typeCode == 0x0880 {
		if startOffset >= 16 {
			entityRef := binary.LittleEndian.Uint32(r.b[startOffset-16 : startOffset-12])
			if entityRef>>24 == 0xf0 {
				if r.droneViewMarkers == nil {
					r.droneViewMarkers = make(map[uint32]int)
				}
				r.droneViewMarkers[entityRef] = len(r.PositionUpdates)
			}
		}
		return nil
	}

	// Bit 7 of byte[0] indicates position data is present
	if typeBytes[0]&0x80 == 0 {
		// Non-position packets (0x0438, 0x0538, 0x0638, etc.) may contain
		// drone operator data in their tail: a 04 FF marker followed by an
		// entity ref indicates the player is viewing through that entity.
		r.scanDroneOperatorTail(startOffset, typeBytes)
		return nil
	}

	// Read XYZ position (3× float32 LE) from payload start
	x, err := r.Float32()
	if err != nil {
		return nil
	}
	y, err := r.Float32()
	if err != nil {
		return nil
	}
	z, err := r.Float32()
	if err != nil {
		return nil
	}

	// Skip NaN/Inf values (bit patterns that aren't valid positions)
	if math.IsNaN(float64(x)) || math.IsNaN(float64(y)) || math.IsNaN(float64(z)) ||
		math.IsInf(float64(x), 0) || math.IsInf(float64(y), 0) || math.IsInf(float64(z), 0) {
		return nil
	}

	// Skip null/invalid positions
	if x == 0 && y == 0 && z == 0 {
		return nil
	}
	if x == 0 && y == 0 && z <= -99 {
		return nil
	}

	// Filter non-position artifacts (very small XY = likely rotation-only data)
	if x > -0.5 && x < 0.5 && y > -0.5 && y < 0.5 {
		return nil
	}

	// Extract entity ref from 12 bytes before pattern start (= startOffset - 16)
	entityRef := uint32(0)
	if startOffset >= 16 {
		entityRef = binary.LittleEndian.Uint32(r.b[startOffset-16 : startOffset-12])
	}

	// Validate entity ref: player entities have 0xF0 as top byte (prefix varies per round:
	// R01=0xF006, R02=0xF005, ..., R07=0xF000, R08=0xF00F, etc.)
	if entityRef>>24 != 0xf0 {
		return nil
	}

	update := PositionUpdate{
		PlayerIndex: -1, // resolved after Read() via buildEntityMap
		EntityRef:   entityRef,
		X:           x,
		Y:           y,
		Z:           z,
		Seq:       len(r.PositionUpdates),
		BinOffset: startOffset,
		typeCode:  typeCode,
	}

	// Check if this entity has a recent drone-view marker (0x0880)
	if r.droneViewMarkers != nil {
		if markerSeq, ok := r.droneViewMarkers[entityRef]; ok {
			// Marker is recent (within last 50 position updates) → player is on drone
			if update.Seq-markerSeq < 50 {
				update.IsDroneView = true
			} else {
				// Stale marker, clear it
				delete(r.droneViewMarkers, entityRef)
			}
		}
	}

	// For 0x03xx type packets, extract rotation from quaternion in trail bytes.
	// Trail layout: [0:4]=unknown scalar, [4:20]=quaternion (qx,qy,qz,qw as float32 LE).
	if typeCode&0xFF00 == 0x0300 {
		trailStart := r.offset + 4 // skip 4-byte unknown scalar
		if trailStart+16 <= len(r.b) {
			qx := math.Float32frombits(binary.LittleEndian.Uint32(r.b[trailStart : trailStart+4]))
			qy := math.Float32frombits(binary.LittleEndian.Uint32(r.b[trailStart+4 : trailStart+8]))
			qz := math.Float32frombits(binary.LittleEndian.Uint32(r.b[trailStart+8 : trailStart+12]))
			qw := math.Float32frombits(binary.LittleEndian.Uint32(r.b[trailStart+12 : trailStart+16]))
			mag := float64(qx)*float64(qx) + float64(qy)*float64(qy) + float64(qz)*float64(qz) + float64(qw)*float64(qw)
			if mag > 0.9 && mag < 1.1 {
				yaw := math.Atan2(2*float64(qw)*float64(qz), 1-2*(float64(qz)*float64(qz)+float64(qy)*float64(qy)))
				update.Yaw = float32(yaw * 180 / math.Pi)
				// Extract pitch (vertical aim angle) from quaternion
				sinP := 2 * (float64(qw)*float64(qy) - float64(qz)*float64(qx))
				if sinP > 1 {
					sinP = 1
				} else if sinP < -1 {
					sinP = -1
				}
				pitch := math.Asin(sinP)
				update.Pitch = float32(pitch * 180 / math.Pi)
			}
		}
	}

	r.PositionUpdates = append(r.PositionUpdates, update)

	// Extract bone/aim data from large movement packets containing bone magic.
	// Bone magic A (02 00 70 88 98 58) followed by 36 bytes = Section A:
	//   [0:12]  head offset vec3 (float32 LE × 3)
	//   [12:16] 1.0 separator
	//   [16:32] aim quaternion (float32 LE × 4: qx, qy, qz, qw)
	//   [32:36] 1.0 separator
	r.extractBoneData(startOffset)

	// Also scan position packets for drone operator markers (04 FF).
	// Some player position packets contain the same 04 FF + drone ref in their
	// payload, especially when the player is viewing a drone.
	r.scanDroneOperatorTail(startOffset, typeBytes)

	log.Debug().
		Uint32("entity", entityRef).
		Float32("x", x).Float32("y", y).Float32("z", z).
		Int("seq", update.Seq).
		Msg("movement")

	return nil
}

// scanDroneOperatorTail detects drone connect/disconnect events from
// non-position movement packets. When a player's packet contains a
// 04 FF marker with a drone entity ref in the tail, and the player
// was NOT previously viewing that drone, a CONNECT event is recorded.
// When a player's packet no longer contains the marker (or switches
// to a different drone), a DISCONNECT event is recorded.
//
// The type code bit 3 (0x08) of byte[0] correlates with drone viewing:
//
//	0x38 = viewing (has 04 FF), 0x30 = not viewing (no 04 FF).
func (r *Reader) scanDroneOperatorTail(startOffset int, typeBytes []byte) {
	if startOffset < 16 {
		return
	}
	entityRef := binary.LittleEndian.Uint32(r.b[startOffset-16 : startOffset-12])
	if entityRef>>24 != 0xf0 {
		return
	}

	pktSize := int(binary.LittleEndian.Uint32(r.b[startOffset-8 : startOffset-4]))
	if pktSize < 200 || pktSize > 8000 {
		return
	}

	pktEnd := startOffset - 4 + pktSize
	if pktEnd > len(r.b) {
		pktEnd = len(r.b)
	}

	// Initialize per-player state map on first call
	if r.playerDroneState == nil {
		r.playerDroneState = make(map[uint32]uint32)
	}

	// Scan tail for 04 FF marker → drone ref
	var foundDrone uint32
	searchStart := pktEnd - 200
	if searchStart < startOffset {
		searchStart = startOffset
	}
	for i := searchStart; i < pktEnd-6; i++ {
		if r.b[i] == 0x04 && r.b[i+1] == 0xFF {
			refOff := i + 3
			if refOff+4 > len(r.b) {
				continue
			}
			ref := binary.LittleEndian.Uint32(r.b[refOff : refOff+4])
			if ref>>24 == 0xf0 {
				foundDrone = ref
				break
			}
		}
	}

	prevDrone := r.playerDroneState[entityRef]
	seq := len(r.PositionUpdates)

	if foundDrone != 0 && foundDrone != prevDrone {
		// Disconnect from previous drone if switching
		if prevDrone != 0 {
			r.DroneEvents = append(r.DroneEvents, DroneEvent{
				PlayerRef: entityRef,
				DroneRef:  prevDrone,
				Seq:       seq,
				Connect:   false,
			})
			log.Debug().Uint32("playerRef", entityRef).Uint32("droneRef", prevDrone).Int("seq", seq).Msg("drone_disconnect")
		}
		// Connect to new drone
		r.DroneEvents = append(r.DroneEvents, DroneEvent{
			PlayerRef: entityRef,
			DroneRef:  foundDrone,
			Seq:       seq,
			Connect:   true,
		})
		r.playerDroneState[entityRef] = foundDrone
		log.Debug().Uint32("playerRef", entityRef).Uint32("droneRef", foundDrone).Int("seq", seq).Msg("drone_connect")
	} else if foundDrone == 0 && prevDrone != 0 {
		// Was viewing, now not viewing → disconnect
		r.DroneEvents = append(r.DroneEvents, DroneEvent{
			PlayerRef: entityRef,
			DroneRef:  prevDrone,
			Seq:       seq,
			Connect:   false,
		})
		r.playerDroneState[entityRef] = 0
		log.Debug().Uint32("playerRef", entityRef).Uint32("droneRef", prevDrone).Int("seq", seq).Msg("drone_disconnect")
	}
	// foundDrone == prevDrone (still viewing same drone) or
	// foundDrone == 0 && prevDrone == 0 (still not viewing) → no event
}

// buildEntityMap scans the binary buffer for the entity ownership table
// and maps entity refs to player indices. Call after all listeners
// have processed but before r.b is freed.
//
// The table encodes player IDs (8-byte LE uint64 from Player.ID) followed
// by ownership flags ending in {01|02} ff, then the entity ref (8 bytes LE,
// 4-byte ref + 4 zero pad). Owner flag 01 = defender entity, 02 = attacker entity.
func (r *Reader) buildEntityMap() {
	entityToPlayer := make(map[uint32]int) // entityRef -> player index

	for i, p := range r.Header.Players {
		if p.ID == 0 {
			continue
		}
		idBytes := make([]byte, 8)
		binary.LittleEndian.PutUint64(idBytes, p.ID)

		// Search for the 8-byte player ID in the buffer
		for off := 0; off < len(r.b)-20; off++ {
			found := true
			for j := 0; j < 8; j++ {
				if r.b[off+j] != idBytes[j] {
					found = false
					break
				}
			}
			if !found {
				continue
			}

			// Found player ID at off. Look for ownership flag ending in {01|02} ff
			// followed by entity ref (xxxxYYf0 00000000, YY varies per round).
			// Scan the next 12 bytes for the ff marker.
			for k := off + 8; k < off+20 && k+8 < len(r.b); k++ {
				if r.b[k] != 0xff {
					continue
				}
				prevByte := r.b[k-1]
				if prevByte != 0x01 && prevByte != 0x02 {
					continue
				}
				// Check if next 8 bytes are an entity ref (xxxxYYf0 00000000)
				// where YY varies per round (06 for R01, 05 for R02, etc.)
				if k+8 >= len(r.b) {
					continue
				}
				if r.b[k+4] != 0xf0 ||
					r.b[k+5] != 0x00 || r.b[k+6] != 0x00 ||
					r.b[k+7] != 0x00 || r.b[k+8] != 0x00 {
					continue
				}
				entityRef := binary.LittleEndian.Uint32(r.b[k+1 : k+5])
				entityToPlayer[entityRef] = i
				log.Debug().
					Str("player", p.Username).
					Int("index", i).
					Uint32("entityRef", entityRef).
					Msg("entity_map")
				break
			}
		}
	}

	// Resolve entity refs in position updates to player indices
	for i := range r.PositionUpdates {
		if idx, ok := entityToPlayer[r.PositionUpdates[i].EntityRef]; ok {
			r.PositionUpdates[i].PlayerIndex = idx
		}
	}

	// For unmapped players, use elimination with the top entities (by position count).
	// Count positions per entity.
	entityCounts := make(map[uint32]int)
	for _, pu := range r.PositionUpdates {
		entityCounts[pu.EntityRef]++
	}

	// Sort all entities by count descending to find the top N (N = player count).
	type entityCount struct {
		ref   uint32
		count int
	}
	var sorted []entityCount
	for ref, count := range entityCounts {
		sorted = append(sorted, entityCount{ref, count})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].count > sorted[j].count })

	// Take the top len(players) entities as candidate player entities
	nPlayers := len(r.Header.Players)
	topEntities := make(map[uint32]bool)
	for i := 0; i < nPlayers && i < len(sorted); i++ {
		topEntities[sorted[i].ref] = true
	}

	// Find which players and top entities are still unmapped
	mappedPlayers := make(map[int]bool)
	mappedEntities := make(map[uint32]bool)
	for ent, idx := range entityToPlayer {
		mappedPlayers[idx] = true
		mappedEntities[ent] = true
	}

	var unmappedEntities []uint32
	for ref := range topEntities {
		if !mappedEntities[ref] {
			unmappedEntities = append(unmappedEntities, ref)
		}
	}
	var unmappedPlayers []int
	for i := range r.Header.Players {
		if !mappedPlayers[i] {
			unmappedPlayers = append(unmappedPlayers, i)
		}
	}

	// N:N elimination using init block order.
	// When multiple unmapped players remain, use the 61 73 85 FE init blocks
	// to establish entity order, then match unmapped entities to unmapped players
	// within each team (defense entities come first, then attack).
	if len(unmappedPlayers) > 0 && len(unmappedEntities) > 0 {
		initPat := []byte{0x61, 0x73, 0x85, 0xFE}
		type initEntry struct {
			ref    uint32
			offset int
		}
		var allInits []initEntry
		for i := 16; i < len(r.b)-4; i++ {
			if r.b[i] == initPat[0] && r.b[i+1] == initPat[1] && r.b[i+2] == initPat[2] && r.b[i+3] == initPat[3] {
				if i >= 12 {
					ref := binary.LittleEndian.Uint32(r.b[i-12 : i-8])
					allInits = append(allInits, initEntry{ref, i})
				}
			}
		}

		// Build init order for all entity refs (both mapped and unmapped)
		entityInitOrder := make(map[uint32]int) // entity ref -> init index
		for idx, entry := range allInits {
			if _, exists := entityInitOrder[entry.ref]; !exists {
				entityInitOrder[entry.ref] = idx
			}
		}

		// Determine team split: find the largest gap between consecutive player inits
		// to separate defenders (early) from attackers (late).
		type playerInitInfo struct {
			initIdx   int
			playerIdx int
			teamIndex int
		}
		var playerInits []playerInitInfo
		for ref, pIdx := range entityToPlayer {
			if iIdx, ok := entityInitOrder[ref]; ok {
				playerInits = append(playerInits, playerInitInfo{iIdx, pIdx, r.Header.Players[pIdx].TeamIndex})
			}
		}
		sort.Slice(playerInits, func(i, j int) bool { return playerInits[i].initIdx < playerInits[j].initIdx })

		// Classify unmapped entities by init position relative to known players.
		// If we have mapped players from both teams, use their init range to classify.
		// Otherwise, just sort all unmapped entities by init order and match 1:1.
		unmappedEntitySet := make(map[uint32]bool)
		for _, ref := range unmappedEntities {
			unmappedEntitySet[ref] = true
		}

		// Sort unmapped entities by their init order
		type entityInit struct {
			ref     uint32
			initIdx int
		}
		var unmappedEntityInits []entityInit
		for _, ref := range unmappedEntities {
			if iIdx, ok := entityInitOrder[ref]; ok {
				unmappedEntityInits = append(unmappedEntityInits, entityInit{ref, iIdx})
			}
		}
		sort.Slice(unmappedEntityInits, func(i, j int) bool {
			return unmappedEntityInits[i].initIdx < unmappedEntityInits[j].initIdx
		})

		// Sort unmapped players: group by team, maintain order within each team.
		// Defense players (early inits) come first, then attack players (late inits).
		// Within a team, maintain header order (which is init order).
		var unmappedDef, unmappedAtk []int
		for _, pIdx := range unmappedPlayers {
			if r.Header.Players[pIdx].TeamIndex == 0 {
				unmappedDef = append(unmappedDef, pIdx)
			} else {
				unmappedAtk = append(unmappedAtk, pIdx)
			}
		}
		// Defense first, then attack — matches init block order
		orderedUnmappedPlayers := append(unmappedDef, unmappedAtk...)

		// Match 1:1 by init order
		n := len(orderedUnmappedPlayers)
		if len(unmappedEntityInits) < n {
			n = len(unmappedEntityInits)
		}
		for i := 0; i < n; i++ {
			pIdx := orderedUnmappedPlayers[i]
			ref := unmappedEntityInits[i].ref
			entityToPlayer[ref] = pIdx
			for j := range r.PositionUpdates {
				if r.PositionUpdates[j].EntityRef == ref {
					r.PositionUpdates[j].PlayerIndex = pIdx
				}
			}
			log.Debug().
				Str("player", r.Header.Players[pIdx].Username).
				Int("index", pIdx).
				Uint32("entityRef", ref).
				Int("initOrder", unmappedEntityInits[i].initIdx).
				Msg("entity_map_init_order")
		}
	}

	// ── Spawn-position validation ──
	// The init-order heuristic can mis-assign players across teams.
	// Detect and fix by comparing each player's first position to their team's
	// average. If a defender spawns near attackers (or vice versa), find the
	// corresponding swap partner and correct both.
	r.validateEntityMapBySpawn(entityToPlayer)

	mapped := 0
	for range entityToPlayer {
		mapped++
	}
	log.Debug().Int("mapped", mapped).Int("players", len(r.Header.Players)).Msg("entity_map_complete")

	// Fill missing yaw values by carrying forward the last known yaw per entity.
	// 0x03xx packets include a quaternion for rotation, but 0x01xx packets (~44%
	// of position updates) do not. Since both types interleave for the same entity,
	// carrying forward the nearest preceding yaw provides smooth rotation data.
	r.fillMissingYaw()

	// Build weapon → player mapping for ammo events
	r.buildWeaponMap(entityToPlayer)

	// Build tracked non-player entities (drones, cameras, gadgets, etc.)
	r.buildTrackedEntities(entityToPlayer)

	// Build shot events from ammo decreases + player positions
	r.buildShotEvents(entityToPlayer)

	// Infer stance from Z-height deviation
	r.inferStance()

	// Estimate death timing from last significant movement
	r.buildDeathTimings()

	// Extract round timer ticks and detect phases (prep/action)
	// (must run before scanGameActions so timer-based time estimation is available)
	r.extractTimerTicks()

	// Scan binary for game action patterns (reinforce, gadget deploy)
	r.scanGameActions()

	// Scan binary for camera rotation quaternions (Pass 4: fixed signature)
	r.scanCameraFrames(entityToPlayer)

	// Scan for paired-quaternion camera frames (Pass 5: multi-POV custom replays)
	r.scanCameraPass5(entityToPlayer)

	// Classify entities by FC-UPDATE flags (projectile subtypes, gadget fingerprints)
	r.classifyEntityFlags()

	// Classify entities by SPAWN counter values (gadget=146, drone=154, weapon=138/254)
	r.classifySpawnCounters()

	// Filter noisy false-positive entities
	r.filterEntityNoise()

	// Detect which player's POV was recorded
	r.detectRecordingPlayer(entityToPlayer)

	// Scan for health property updates (hash 0x4171D3C3)
	r.scanHealthUpdates(entityToPlayer)

	// Scan for entity-level health events (barricades, gadgets, etc.)
	r.scanEntityHealthEvents()

	// Extract gadget loadout counts (primary/secondary gadgets per player)
	r.extractGadgetLoadout(entityToPlayer)

	// Assign unassigned camera frames to the recording player
	if r.RecordingPlayer >= 0 {
		for i := range r.CameraFrames {
			if r.CameraFrames[i].PlayerIndex < 0 {
				r.CameraFrames[i].PlayerIndex = r.RecordingPlayer
			}
		}
	}

	// Unwrap yaw to eliminate ±180° discontinuities
	r.unwrapYaw()

	// Sanitize any NaN/Inf values to prevent JSON encoding failures
	r.sanitizeFloats()
}

// fillMissingYaw carries forward the last known non-zero Yaw value to subsequent
// PositionUpdates for the same entity that lack rotation data. This covers 0x01xx
// position packets which contain XYZ but no quaternion (unlike 0x03xx packets).
// PositionUpdates are already in sequence order, so a single pass per entity suffices.
func (r *Reader) fillMissingYaw() {
	lastYaw := make(map[uint32]float32)
	lastPitch := make(map[uint32]float32)
	filledYaw := 0
	filledPitch := 0
	for i := range r.PositionUpdates {
		pu := &r.PositionUpdates[i]
		if pu.Yaw != 0 {
			lastYaw[pu.EntityRef] = pu.Yaw
		} else if prev, ok := lastYaw[pu.EntityRef]; ok {
			pu.Yaw = prev
			filledYaw++
		}
		if pu.Pitch != 0 {
			lastPitch[pu.EntityRef] = pu.Pitch
		} else if prev, ok := lastPitch[pu.EntityRef]; ok {
			pu.Pitch = prev
			filledPitch++
		}
	}
	log.Debug().Int("filledYaw", filledYaw).Int("filledPitch", filledPitch).Int("total", len(r.PositionUpdates)).Msg("fill_missing_yaw_pitch")
}

// extractBoneData scans a movement packet for bone magic A (02 00 70 88 98 58)
// and bone magic B (00 2C 36 14 9B) to extract head and chest bone data.
// Each bone provides a vec3 offset + quaternion rotation relative to the player.
func (r *Reader) extractBoneData(startOffset int) {
	if startOffset < 8 || len(r.PositionUpdates) == 0 {
		return
	}

	pktSize := int(binary.LittleEndian.Uint32(r.b[startOffset-8 : startOffset-4]))
	if pktSize < 100 || pktSize > 10000 {
		return
	}
	pktEnd := startOffset - 4 + pktSize
	if pktEnd > len(r.b) {
		pktEnd = len(r.b)
	}

	payloadStart := startOffset + 2 // after type code
	bma := [6]byte{0x02, 0x00, 0x70, 0x88, 0x98, 0x58}
	bmb := [5]byte{0x00, 0x2C, 0x36, 0x14, 0x9B}

	pu := &r.PositionUpdates[len(r.PositionUpdates)-1]

	// Find first bone magic A (head bone)
	for i := payloadStart; i+6+36 < pktEnd; i++ {
		if r.b[i] != bma[0] || r.b[i+1] != bma[1] || r.b[i+2] != bma[2] ||
			r.b[i+3] != bma[3] || r.b[i+4] != bma[4] || r.b[i+5] != bma[5] {
			continue
		}

		// Found bone magic A. Section A: v3(12) + 1.0(4) + q4(16) + 1.0(4) = 36 bytes
		secAStart := i + 6
		if secAStart+36 > pktEnd {
			return
		}

		hx := math.Float32frombits(binary.LittleEndian.Uint32(r.b[secAStart : secAStart+4]))
		hy := math.Float32frombits(binary.LittleEndian.Uint32(r.b[secAStart+4 : secAStart+8]))
		hz := math.Float32frombits(binary.LittleEndian.Uint32(r.b[secAStart+8 : secAStart+12]))
		qx := math.Float32frombits(binary.LittleEndian.Uint32(r.b[secAStart+16 : secAStart+20]))
		qy := math.Float32frombits(binary.LittleEndian.Uint32(r.b[secAStart+20 : secAStart+24]))
		qz := math.Float32frombits(binary.LittleEndian.Uint32(r.b[secAStart+24 : secAStart+28]))
		qw := math.Float32frombits(binary.LittleEndian.Uint32(r.b[secAStart+28 : secAStart+32]))

		// Validate quaternion magnitude
		mag := float64(qx)*float64(qx) + float64(qy)*float64(qy) +
			float64(qz)*float64(qz) + float64(qw)*float64(qw)
		if mag < 0.8 || mag > 1.2 {
			return
		}

		// Head offsets are small deltas, reject false positives
		if hx < -2 || hx > 2 || hy < -2 || hy > 2 || hz < -2 || hz > 2 {
			return
		}

		pu.HeadOffX = hx
		pu.HeadOffY = hy
		pu.HeadOffZ = hz
		pu.HeadQX = qx
		pu.HeadQY = qy
		pu.HeadQZ = qz
		pu.HeadQW = qw

		// Find bone magic B (chest bone) right after Section A
		bmbStart := secAStart + 36
		for j := bmbStart; j+5+36 < pktEnd && j < bmbStart+10; j++ {
			if r.b[j] != bmb[0] || r.b[j+1] != bmb[1] || r.b[j+2] != bmb[2] ||
				r.b[j+3] != bmb[3] || r.b[j+4] != bmb[4] {
				continue
			}

			secBStart := j + 5
			if secBStart+36 > pktEnd {
				break
			}

			cx := math.Float32frombits(binary.LittleEndian.Uint32(r.b[secBStart : secBStart+4]))
			cy := math.Float32frombits(binary.LittleEndian.Uint32(r.b[secBStart+4 : secBStart+8]))
			cz := math.Float32frombits(binary.LittleEndian.Uint32(r.b[secBStart+8 : secBStart+12]))
			cqx := math.Float32frombits(binary.LittleEndian.Uint32(r.b[secBStart+16 : secBStart+20]))
			cqy := math.Float32frombits(binary.LittleEndian.Uint32(r.b[secBStart+20 : secBStart+24]))
			cqz := math.Float32frombits(binary.LittleEndian.Uint32(r.b[secBStart+24 : secBStart+28]))
			cqw := math.Float32frombits(binary.LittleEndian.Uint32(r.b[secBStart+28 : secBStart+32]))

			cmag := float64(cqx)*float64(cqx) + float64(cqy)*float64(cqy) +
				float64(cqz)*float64(cqz) + float64(cqw)*float64(cqw)
			if cmag < 0.8 || cmag > 1.2 {
				break
			}
			if cx < -2 || cx > 2 || cy < -2 || cy > 2 || cz < -2 || cz > 2 {
				break
			}

			pu.ChestOffX = cx
			pu.ChestOffY = cy
			pu.ChestOffZ = cz
			pu.ChestQX = cqx
			pu.ChestQY = cqy
			pu.ChestQZ = cqz
			pu.ChestQW = cqw
			break
		}
		return
	}
}

// buildWeaponMap maps weapon entity refs from ammo events to player indices.
//
// Weapon entities are dynamically created (no init blocks or ownership table entries).
// They appear in pairs (primary + secondary) in the binary stream, spawned in the same
// order as their owner's player init blocks (61 73 85 FE). Attackers' weapons spawn
// first, then defenders', separated by a large offset gap.
//
// Algorithm:
//  1. Collect first-occurrence offset for each unique weapon entity ref
//  2. Sort weapons by first-occurrence offset
//  3. Split into attacker/defender groups at the largest offset gap
//  4. Build player init order from init blocks (61 73 85 FE)
//  5. Pair consecutive weapons (gap < 500 bytes) and assign to players in init order
//  6. Resolve PlayerIndex on all AmmoUpdates
func (r *Reader) buildWeaponMap(entityToPlayer map[uint32]int) {
	if len(r.AmmoUpdates) == 0 {
		return
	}

	initPat := []byte{0x61, 0x73, 0x85, 0xFE}

	// Step 1: Collect first-occurrence offset per weapon entity ref
	type weaponFirst struct {
		ref    uint32
		offset int
	}
	weaponFirstMap := make(map[uint32]int) // ref -> first offset
	for i := range r.AmmoUpdates {
		ref := r.AmmoUpdates[i].weaponRef
		if ref == 0 {
			continue
		}
		if _, exists := weaponFirstMap[ref]; !exists {
			weaponFirstMap[ref] = r.AmmoUpdates[i].BinOffset
		}
	}
	if len(weaponFirstMap) == 0 {
		return
	}

	// Step 2: Sort weapons by first-occurrence offset
	weapons := make([]weaponFirst, 0, len(weaponFirstMap))
	for ref, off := range weaponFirstMap {
		weapons = append(weapons, weaponFirst{ref, off})
	}
	sort.Slice(weapons, func(i, j int) bool { return weapons[i].offset < weapons[j].offset })

	// Step 3: Split into two groups at the largest offset gap
	if len(weapons) < 2 {
		// Single weapon: try to assign later
		return
	}
	maxGap := 0
	splitIdx := 0
	for i := 1; i < len(weapons); i++ {
		gap := weapons[i].offset - weapons[i-1].offset
		if gap > maxGap {
			maxGap = gap
			splitIdx = i
		}
	}

	// Only split if the gap is significantly larger than within-group gaps (>10x)
	avgGap := 0
	if len(weapons) > 2 {
		totalGap := 0
		count := 0
		for i := 1; i < len(weapons); i++ {
			if i != splitIdx {
				totalGap += weapons[i].offset - weapons[i-1].offset
				count++
			}
		}
		if count > 0 {
			avgGap = totalGap / count
		}
	}
	if avgGap == 0 || maxGap < avgGap*5 {
		// No clear team split; skip weapon mapping
		log.Debug().Msg("weapon_map: no clear team gap, skipping")
		return
	}

	group1 := weapons[:splitIdx]
	group2 := weapons[splitIdx:]

	// Step 4: Build player init order from init blocks
	type playerInit struct {
		ref    uint32
		idx    int
		offset int
	}
	var allInits []playerInit
	for i := 16; i < len(r.b)-4; i++ {
		if r.b[i] == initPat[0] && r.b[i+1] == initPat[1] && r.b[i+2] == initPat[2] && r.b[i+3] == initPat[3] {
			if i >= 12 {
				ref := binary.LittleEndian.Uint32(r.b[i-12 : i-8])
				allInits = append(allInits, playerInit{ref, len(allInits), i})
			}
		}
	}

	// Identify player inits and split into defenders (early) and attackers (late)
	// by finding the largest gap between consecutive player init positions.
	type playerInitInfo struct {
		initIdx   int
		playerIdx int
	}
	var playerInits []playerInitInfo
	for _, pi := range allInits {
		if pIdx, ok := entityToPlayer[pi.ref]; ok {
			playerInits = append(playerInits, playerInitInfo{pi.idx, pIdx})
		}
	}
	sort.Slice(playerInits, func(i, j int) bool { return playerInits[i].initIdx < playerInits[j].initIdx })

	if len(playerInits) < 2 {
		log.Debug().Msg("weapon_map: not enough player inits")
		return
	}

	// Split at the largest gap between consecutive player inits
	teamSplit := 0
	maxTeamGap := 0
	for i := 1; i < len(playerInits); i++ {
		gap := playerInits[i].initIdx - playerInits[i-1].initIdx
		if gap > maxTeamGap {
			maxTeamGap = gap
			teamSplit = i
		}
	}

	var defPlayers, atkPlayers []int // player indices in init order
	for i, pi := range playerInits {
		if i < teamSplit {
			defPlayers = append(defPlayers, pi.playerIdx)
		} else {
			atkPlayers = append(atkPlayers, pi.playerIdx)
		}
	}

	// Step 5: Assign weapon groups to teams
	// Attackers' weapons spawn first in binary order, defenders second
	atkWeapons := group1
	defWeapons := group2

	// Step 6: Pair weapons and assign to players
	weaponToPlayer := make(map[uint32]int) // weaponRef -> player index

	assignGroup := func(weaponList []weaponFirst, playerList []int) {
		pIdx := 0
		for i := 0; i < len(weaponList); {
			if pIdx >= len(playerList) {
				// Extra weapons go to last player in the group
				if len(playerList) > 0 {
					last := playerList[len(playerList)-1]
					weaponToPlayer[weaponList[i].ref] = last
				}
				i++
				continue
			}
			player := playerList[pIdx]
			weaponToPlayer[weaponList[i].ref] = player
			// Check if next weapon is a pair (gap < 500 bytes)
			if i+1 < len(weaponList) && weaponList[i+1].offset-weaponList[i].offset < 500 {
				weaponToPlayer[weaponList[i+1].ref] = player
				i += 2
			} else {
				i++
			}
			pIdx++
		}
	}

	assignGroup(atkWeapons, atkPlayers)
	assignGroup(defWeapons, defPlayers)

	// Step 7: Resolve PlayerIndex on all AmmoUpdates
	mapped := 0
	for i := range r.AmmoUpdates {
		if pIdx, ok := weaponToPlayer[r.AmmoUpdates[i].weaponRef]; ok {
			r.AmmoUpdates[i].PlayerIndex = pIdx
			mapped++
		}
	}

	log.Debug().
		Int("weapons", len(weaponToPlayer)).
		Int("ammoMapped", mapped).
		Int("ammoTotal", len(r.AmmoUpdates)).
		Msg("weapon_map_complete")

	// Step 8: Build per-player loadouts from weapon pairs
	r.buildLoadouts(weaponToPlayer)
}

// buildLoadouts creates a PlayerLoadout for each player based on their
// weapon entity assignments. The weapon with higher initial capacity is
// classified as primary; the other as secondary.
func (r *Reader) buildLoadouts(weaponToPlayer map[uint32]int) {
	// Collect initial capacity and hashes per weapon entity (from first ammo event)
	type weaponData struct {
		ref        uint32
		initialCap uint32
		hash1      uint32
		hash2      uint32
	}
	weaponInfo := make(map[uint32]*weaponData) // weaponRef -> first event data
	for i := range r.AmmoUpdates {
		ref := r.AmmoUpdates[i].weaponRef
		if ref == 0 {
			continue
		}
		if _, exists := weaponInfo[ref]; !exists {
			weaponInfo[ref] = &weaponData{
				ref:        ref,
				initialCap: r.AmmoUpdates[i].Capacity,
				hash1:      r.AmmoUpdates[i].Hash1,
				hash2:      r.AmmoUpdates[i].Hash2,
			}
		}
	}

	// Group weapons by player
	playerWeapons := make(map[int][]*weaponData) // playerIdx -> weapons
	for ref, pIdx := range weaponToPlayer {
		if wd, ok := weaponInfo[ref]; ok {
			playerWeapons[pIdx] = append(playerWeapons[pIdx], wd)
		}
	}

	// Build loadout per player
	r.Loadouts = make([]PlayerLoadout, len(r.Header.Players))
	for i := range r.Header.Players {
		r.Loadouts[i] = PlayerLoadout{
			PlayerIndex: i,
			Username:    r.Header.Players[i].Username,
		}
		weps := playerWeapons[i]
		if len(weps) == 0 {
			continue
		}
		// Sort by initial capacity descending: highest = primary
		sort.Slice(weps, func(a, b int) bool { return weps[a].initialCap > weps[b].initialCap })
		r.Loadouts[i].Primary = &WeaponInfo{
			EntityRef:  weps[0].ref,
			InitialCap: weps[0].initialCap,
			Hash1:      weps[0].hash1,
			Hash2:      weps[0].hash2,
			IsPrimary:  true,
		}
		if len(weps) > 1 {
			r.Loadouts[i].Secondary = &WeaponInfo{
				EntityRef:  weps[1].ref,
				InitialCap: weps[1].initialCap,
				Hash1:      weps[1].hash1,
				Hash2:      weps[1].hash2,
				IsPrimary:  false,
			}
		}
		log.Debug().
			Int("player", i).
			Str("username", r.Header.Players[i].Username).
			Uint32("primaryCap", r.Loadouts[i].Primary.InitialCap).
			Msg("loadout")
	}
}

// buildTrackedEntities classifies non-player entities (drones, cameras, gadgets, etc.)
// and builds TrackedEntity records with their position histories.
//
// Classification strategy:
//  1. Team zone assignment via init block offset boundary (attacker vs defender)
//  2. Behavioral heuristics (lifespan, position count, movement variance)
//  3. Operator-aware refinement: operators with special moving gadgets (Twitch shock
//     drones, Valkyrie cameras, Echo Yokai, etc.) get their gadgets identified and
//     claimed from the entity pool
//  4. Standard drones capped at 5 per attacking team (1 per player)
func (r *Reader) buildTrackedEntities(entityToPlayer map[uint32]int) {
	if len(r.PositionUpdates) == 0 {
		return
	}

	// Identify player entity refs
	playerEntities := make(map[uint32]bool)
	for ref := range entityToPlayer {
		playerEntities[ref] = true
	}

	// Determine which team is attacking from the header
	attackTeamIdx := -1
	for i, t := range r.Header.Teams {
		if t.Role == Attack {
			attackTeamIdx = i
			break
		}
	}
	defenseTeamIdx := 1 - attackTeamIdx
	if attackTeamIdx == -1 {
		defenseTeamIdx = -1
	}

	// Build per-team player index sets and collect operator names per player
	attackerPlayers := make(map[int]bool)
	defenderPlayers := make(map[int]bool)
	playerOperator := make(map[int]string) // playerIdx -> operator name
	for i, p := range r.Header.Players {
		playerOperator[i] = p.Operator.String()
		if p.TeamIndex == attackTeamIdx {
			attackerPlayers[i] = true
		} else {
			defenderPlayers[i] = true
		}
	}

	// Group non-player positions by entity ref
	type entityData struct {
		ref       uint32
		positions []EntityPosition
		firstSeq  int
		lastSeq   int
	}
	entityMap := make(map[uint32]*entityData)
	for i := range r.PositionUpdates {
		pu := &r.PositionUpdates[i]
		if playerEntities[pu.EntityRef] {
			continue
		}
		if pu.PlayerIndex >= 0 {
			continue
		}
		ed, ok := entityMap[pu.EntityRef]
		if !ok {
			ed = &entityData{ref: pu.EntityRef, firstSeq: pu.Seq, lastSeq: pu.Seq}
			entityMap[pu.EntityRef] = ed
		}
		// Reject garbage coordinates (uninitialized memory reads from binary)
		if math.IsNaN(float64(pu.X)) || math.IsNaN(float64(pu.Y)) || math.IsNaN(float64(pu.Z)) ||
			math.IsInf(float64(pu.X), 0) || math.IsInf(float64(pu.Y), 0) || math.IsInf(float64(pu.Z), 0) ||
			math.Abs(float64(pu.X)) > 500 || math.Abs(float64(pu.Y)) > 500 || math.Abs(float64(pu.Z)) > 500 {
			continue
		}
		ed.positions = append(ed.positions, EntityPosition{
			X: pu.X, Y: pu.Y, Z: pu.Z, Yaw: pu.Yaw, Pitch: pu.Pitch, Seq: pu.Seq,
		})
		ed.lastSeq = pu.Seq
	}

	// Filter garbage positions from entity tracks.
	// 1) Remove near-origin positions (within 2m of world origin) — no R6 map
	//    has playable area at (0,0,0); these are uninitialized memory reads.
	// 2) Truncate at teleport jumps (>30m between consecutive frames) caused
	//    by entity destruction/despawn.
	for _, ed := range entityMap {
		// First pass: remove near-origin garbage
		filtered := ed.positions[:0]
		for _, p := range ed.positions {
			originDist := math.Sqrt(float64(p.X)*float64(p.X) + float64(p.Y)*float64(p.Y) + float64(p.Z)*float64(p.Z))
			if originDist < 2.0 {
				continue // skip near-origin garbage
			}
			filtered = append(filtered, p)
		}
		// Second pass: truncate at teleport jumps
		clean := filtered[:0]
		for i, p := range filtered {
			if i > 0 {
				prev := clean[len(clean)-1]
				dx := float64(p.X - prev.X)
				dy := float64(p.Y - prev.Y)
				dz := float64(p.Z - prev.Z)
				dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
				if dist > 30 {
					break // teleport detected, drop this and all subsequent positions
				}
			}
			clean = append(clean, p)
		}
		ed.positions = clean
		if len(clean) > 0 {
			ed.lastSeq = clean[len(clean)-1].Seq
		}
	}

	// Build init block map for team zone detection
	initPat := []byte{0x61, 0x73, 0x85, 0xFE}
	type initEntry struct {
		ref    uint32
		offset int
	}
	var allInits []initEntry
	if len(r.b) > 0 {
		for i := 12; i < len(r.b)-4; i++ {
			if r.b[i] == initPat[0] && r.b[i+1] == initPat[1] && r.b[i+2] == initPat[2] && r.b[i+3] == initPat[3] {
				ref := binary.LittleEndian.Uint32(r.b[i-12 : i-8])
				allInits = append(allInits, initEntry{ref, i})
			}
		}
	}
	entityInitOffset := make(map[uint32]int)
	for _, ie := range allInits {
		if _, ok := entityInitOffset[ie.ref]; !ok {
			entityInitOffset[ie.ref] = ie.offset
		}
	}

	// Determine init offset zone boundary from player init offsets
	playerInitOffsets := make(map[int]int)
	for ref, pIdx := range entityToPlayer {
		if off, ok := entityInitOffset[ref]; ok {
			playerInitOffsets[pIdx] = off
		}
	}
	maxDefInitOff := 0
	minAtkInitOff := int(^uint(0) >> 1)
	for pIdx, off := range playerInitOffsets {
		if attackerPlayers[pIdx] {
			if off < minAtkInitOff {
				minAtkInitOff = off
			}
		} else {
			if off > maxDefInitOff {
				maxDefInitOff = off
			}
		}
	}
	zoneBoundary := 0
	if maxDefInitOff > 0 && minAtkInitOff < int(^uint(0)>>1) {
		zoneBoundary = (maxDefInitOff + minAtkInitOff) / 2
	}

	// Phase 1: Build raw entity list with team zone + behavioral classification
	type candidateEntity struct {
		TrackedEntity
		lifespan          int
		firstSeq          int
		reservedPrepDrone bool
	}
	var candidates []candidateEntity

	// Pre-scan SPAWN counters so we can exempt barricades from the min-3-position filter.
	spawnCounters := r.preloadSpawnCounters()

	for _, ed := range entityMap {
		isBarricadeCandidate := false
		if counter, ok := spawnCounters[ed.ref]; ok && counter == 130 {
			isBarricadeCandidate = r.isBarricadeEntity(ed.ref)
		}
		if len(ed.positions) < 3 && !isBarricadeCandidate {
			continue
		}

		isAttackerZone := false
		if entInitOff, ok := entityInitOffset[ed.ref]; ok && zoneBoundary > 0 {
			isAttackerZone = entInitOff > zoneBoundary
		}

		posCount := len(ed.positions)
		lifespan := ed.lastSeq - ed.firstSeq

		team := Defense
		teamIdx := defenseTeamIdx
		if isAttackerZone {
			team = Attack
			teamIdx = attackTeamIdx
		}
		_ = teamIdx

		// Behavioral classification
		// Compute total distance traveled to distinguish moving entities (drones)
		// from stationary ones (cameras). Real cameras don't move.
		totalDist := float64(0)
		for k := 1; k < len(ed.positions); k++ {
			dx := float64(ed.positions[k].X - ed.positions[k-1].X)
			dy := float64(ed.positions[k].Y - ed.positions[k-1].Y)
			dz := float64(ed.positions[k].Z - ed.positions[k-1].Z)
			totalDist += math.Sqrt(dx*dx + dy*dy + dz*dz)
		}

		entType := EntityGadget
		switch {
		case posCount > 30 && lifespan > 1500 && totalDist > 2.0:
			// Moves significantly over time = drone (either zone)
			entType = EntityDrone
		case posCount > 50 && lifespan > 3000 && totalDist <= 2.0:
			// Lots of samples but barely moves = stationary camera
			entType = EntityCamera
		case lifespan < 800 && posCount < 30:
			entType = EntityProjectile
		}

		candidates = append(candidates, candidateEntity{
			TrackedEntity: TrackedEntity{
				EntityRef:     ed.ref,
				Type:          entType,
				OwnerIndex:    -1, // team-level by default
				OperatorIndex: -1, // resolved later from packet-based detection
				Team:          team,
			},
			lifespan: lifespan,
			firstSeq: ed.firstSeq,
		})
	}

	// Prep-phase attacker drones are the initial drone cluster that spawns before
	// later attacker gadgets. Keep these team-level because they are standard drones,
	// and operator selection is not reliable enough to attribute them individually.
	var attackerDroneIdxs []int
	for i := range candidates {
		if candidates[i].Team == Attack && candidates[i].Type == EntityDrone {
			attackerDroneIdxs = append(attackerDroneIdxs, i)
		}
	}
	sort.Slice(attackerDroneIdxs, func(i, j int) bool {
		left := candidates[attackerDroneIdxs[i]]
		right := candidates[attackerDroneIdxs[j]]
		if left.firstSeq != right.firstSeq {
			return left.firstSeq < right.firstSeq
		}
		return len(left.Positions) > len(right.Positions)
	})
	prepDroneCount := 0
	if len(attackerDroneIdxs) > 0 {
		prepDroneCount = len(attackerPlayers)
		if prepDroneCount > len(attackerDroneIdxs) {
			prepDroneCount = len(attackerDroneIdxs)
		}
		if len(attackerDroneIdxs) > prepDroneCount {
			largestGap := 0
			largestGapIdx := -1
			for i := 1; i < len(attackerDroneIdxs); i++ {
				gap := candidates[attackerDroneIdxs[i]].firstSeq - candidates[attackerDroneIdxs[i-1]].firstSeq
				if gap > largestGap {
					largestGap = gap
					largestGapIdx = i - 1
				}
			}
			if largestGapIdx >= prepDroneCount-1 && largestGap >= 4000 {
				prepDroneCount = largestGapIdx + 1
			}
		}
		for i := 0; i < prepDroneCount; i++ {
			candidates[attackerDroneIdxs[i]].reservedPrepDrone = true
			candidates[attackerDroneIdxs[i]].OwnerIndex = -1
		}
	}

	// Phase 2: Operator-aware refinement
	// Identify which special-gadget operators are in the match per team
	type opGadgetEntry struct {
		playerIdx  int
		opName     string
		gadgetType EntityType
		gadgetName string
		maxCount   int
		claimed    int
	}
	var atkOpGadgets []opGadgetEntry
	var defOpGadgets []opGadgetEntry
	for pIdx, opName := range playerOperator {
		if gInfo, ok := operatorMovingGadgets[opName]; ok {
			entry := opGadgetEntry{
				playerIdx:  pIdx,
				opName:     opName,
				gadgetType: gInfo.entityType,
				gadgetName: gInfo.gadgetName,
				maxCount:   gInfo.maxCount,
			}
			if attackerPlayers[pIdx] {
				atkOpGadgets = append(atkOpGadgets, entry)
			} else {
				defOpGadgets = append(defOpGadgets, entry)
			}
		}
	}

	// Sort candidates by position count descending within each type so we claim
	// the most prominent entities first for operator gadgets.
	sort.Slice(candidates, func(i, j int) bool {
		return len(candidates[i].Positions) > len(candidates[j].Positions)
	})

	// Claim operator gadgets from attacker-zone entities
	for ogi := range atkOpGadgets {
		og := &atkOpGadgets[ogi]
		for ci := range candidates {
			if og.claimed >= og.maxCount {
				break
			}
			c := &candidates[ci]
			if c.Team != Attack {
				continue
			}
			if c.Type == EntityDrone && c.reservedPrepDrone {
				continue
			}
			// Match behavioral type to what the operator produces
			// e.g. Twitch produces drones, so claim drone-classified entities
			if c.Type == og.gadgetType && c.OwnerIndex == -1 {
				c.OwnerIndex = og.playerIdx
				c.GadgetName = og.gadgetName
				og.claimed++
			}
		}
	}

	// Sort defender operator gadgets: camera-producing operators first.
	// Cameras are frequently misclassified as gadgets by Phase 1 heuristics,
	// and their rescue clause must run before other operators claim them.
	sort.SliceStable(defOpGadgets, func(i, j int) bool {
		isCamI := defOpGadgets[i].gadgetType == EntityCamera
		isCamJ := defOpGadgets[j].gadgetType == EntityCamera
		if isCamI != isCamJ {
			return isCamI
		}
		return false
	})

	// Claim operator gadgets from defender-zone entities
	for ogi := range defOpGadgets {
		og := &defOpGadgets[ogi]
		for ci := range candidates {
			if og.claimed >= og.maxCount {
				break
			}
			c := &candidates[ci]
			if c.Team != Defense {
				continue
			}
			if c.Type == og.gadgetType && c.OwnerIndex == -1 {
				c.OwnerIndex = og.playerIdx
				c.GadgetName = og.gadgetName
				og.claimed++
			}
			// Also check gadgets that might have been misclassified
			// e.g. a Valkyrie camera might look like a gadget if it didn't
			// meet the strict camera thresholds
			if og.gadgetType == EntityCamera && c.Type == EntityGadget && c.OwnerIndex == -1 && c.lifespan > 1000 {
				c.Type = EntityCamera
				c.OwnerIndex = og.playerIdx
				c.GadgetName = og.gadgetName
				og.claimed++
			}
			if og.gadgetType == EntityDrone && c.Type == EntityGadget && c.OwnerIndex == -1 && len(c.Positions) > 20 && c.lifespan > 800 {
				c.Type = EntityDrone
				c.OwnerIndex = og.playerIdx
				c.GadgetName = og.gadgetName
				og.claimed++
			}
			// Also reclassify cameras as drones for drone-producing defenders
			// (e.g. Echo Yokai, Mozzie captures) in case behavioral phase was wrong
			if og.gadgetType == EntityDrone && c.Type == EntityCamera && c.OwnerIndex == -1 {
				c.Type = EntityDrone
				c.OwnerIndex = og.playerIdx
				c.GadgetName = og.gadgetName
				og.claimed++
			}
		}
	}

	// Phase 3b: Fix team for drones detected in defender zone.
	// Standard drones always belong to attackers. Defender drones only exist as
	// operator gadgets (Echo/Mozzie) which were claimed with an ownerIndex in
	// Phase 2. Any unclaimed drone still labeled Defense must be an attacker
	// drone that entered the building.
	for ci := range candidates {
		c := &candidates[ci]
		if c.Type == EntityDrone && c.Team == Defense && c.OwnerIndex == -1 {
			c.Team = Attack
		}
	}

	// Phase 3c: Drone ownership via cumulative connection duration.
	// During prep phase a player may briefly cycle through teammates' drones,
	// creating spurious "first connect" events. The actual deployer naturally
	// spends more total time connected to their own drone. We compute total
	// cumulative connection time per (player, drone) pair from confirmed 04 FF
	// connect/disconnect events and assign ownership to the player with the
	// longest total time, with a per-player cap of 2 standard drones.
	{
		refToCandidate := make(map[uint32]int)
		for ci, c := range candidates {
			refToCandidate[c.EntityRef] = ci
		}

		// Compute cumulative connection time per (player, drone) pair.
		type pairKey struct{ player, drone uint32 }
		activeConnect := make(map[pairKey]int) // seq when current connection started
		totalTime := make(map[pairKey]int)     // cumulative connected seq-duration

		for _, ev := range r.DroneEvents {
			key := pairKey{ev.PlayerRef, ev.DroneRef}
			ci, ok := refToCandidate[ev.DroneRef]
			if !ok {
				continue
			}
			if candidates[ci].OwnerIndex >= 0 {
				continue // already owned (operator gadget from Phase 2)
			}
			if ev.Connect {
				activeConnect[key] = ev.Seq
			} else {
				if start, ok := activeConnect[key]; ok {
					totalTime[key] += ev.Seq - start
					delete(activeConnect, key)
				}
			}
		}
		// Credit still-open connections (player never disconnected).
		for key, start := range activeConnect {
			totalTime[key] += 1000000 - start
		}

		// Group candidates by drone, sorted by total time descending.
		type scored struct {
			playerRef uint32
			time      int
		}
		droneCands := make(map[uint32][]scored)
		for pk, t := range totalTime {
			droneCands[pk.drone] = append(droneCands[pk.drone], scored{pk.player, t})
		}
		for d := range droneCands {
			sort.Slice(droneCands[d], func(i, j int) bool {
				return droneCands[d][i].time > droneCands[d][j].time
			})
		}

		// Assign owners: process drones with clearest ownership first (longest
		// best-candidate time), so ambiguous drones fall to remaining players.
		type droneEntry struct {
			ref      uint32
			bestTime int
		}
		var dOrder []droneEntry
		for d, cands := range droneCands {
			if len(cands) > 0 {
				dOrder = append(dOrder, droneEntry{d, cands[0].time})
			}
		}
		sort.Slice(dOrder, func(i, j int) bool {
			return dOrder[i].bestTime > dOrder[j].bestTime
		})

		playerOwned := make(map[int]int) // playerIdx -> owned standard drone count
		for _, de := range dOrder {
			ci := refToCandidate[de.ref]
			for _, sc := range droneCands[de.ref] {
				pIdx, ok := entityToPlayer[sc.playerRef]
				if !ok {
					continue
				}
				if playerOwned[pIdx] >= 2 {
					continue
				}
				candidates[ci].OwnerIndex = pIdx
				playerOwned[pIdx]++
				log.Debug().
					Uint32("droneRef", de.ref).
					Int("ownerIdx", pIdx).
					Str("owner", r.Header.Players[pIdx].Username).
					Int("totalTime", sc.time).
					Msg("drone_owner_confirmed")
				break
			}
		}
	}

	// Phase 3c-fallback: Assign unowned standard drones by spawn proximity.
	// When a drone has no 04 FF connect events (the player never actively
	// controlled it after deployment), fall back to matching the drone's first
	// recorded position against each attacker's spawn position. The deploying
	// player's drones start at their feet, so proximity is a strong signal.
	{
		// Get each attacker's first position (spawn position)
		type playerSpawn struct {
			pIdx    int
			x, y, z float32
		}
		var attackerSpawns []playerSpawn
		playerFirstSeq := make(map[int]int) // pIdx -> lowest Seq seen
		for i := range r.PositionUpdates {
			pu := &r.PositionUpdates[i]
			if pu.PlayerIndex < 0 || !attackerPlayers[pu.PlayerIndex] {
				continue
			}
			prev, ok := playerFirstSeq[pu.PlayerIndex]
			if !ok || pu.Seq < prev {
				playerFirstSeq[pu.PlayerIndex] = pu.Seq
			}
		}
		// Resolve spawn positions
		for pIdx, seq := range playerFirstSeq {
			for i := range r.PositionUpdates {
				pu := &r.PositionUpdates[i]
				if pu.PlayerIndex == pIdx && pu.Seq == seq {
					attackerSpawns = append(attackerSpawns, playerSpawn{pIdx, pu.X, pu.Y, pu.Z})
					break
				}
			}
		}

		// Count current ownership per player
		ownCount := make(map[int]int)
		for _, c := range candidates {
			if c.Type == EntityDrone && c.OwnerIndex >= 0 && c.GadgetName == "" {
				ownCount[c.OwnerIndex]++
			}
		}

		// For each unowned standard attack drone, find nearest attacker
		for ci := range candidates {
			c := &candidates[ci]
			if c.Type != EntityDrone || c.OwnerIndex >= 0 || c.Team != Attack {
				continue
			}
			ed := entityMap[c.EntityRef]
			if ed == nil || len(ed.positions) == 0 {
				continue
			}
			dp := ed.positions[0] // drone's first position (spawn point)

			bestDist := float64(math.MaxFloat32)
			bestPIdx := -1
			for _, sp := range attackerSpawns {
				if ownCount[sp.pIdx] >= 2 {
					continue
				}
				dx := float64(dp.X - sp.x)
				dy := float64(dp.Y - sp.y)
				dz := float64(dp.Z - sp.z)
				dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
				if dist < bestDist {
					bestDist = dist
					bestPIdx = sp.pIdx
				}
			}
			if bestPIdx >= 0 {
				c.OwnerIndex = bestPIdx
				ownCount[bestPIdx]++
				log.Debug().
					Uint32("droneRef", c.EntityRef).
					Int("ownerIdx", bestPIdx).
					Str("owner", r.Header.Players[bestPIdx].Username).
					Float64("dist", bestDist).
					Msg("drone_owner_proximity")
			}
		}
	}

	// Phase 3-cap: Demote excess standard Attack drones to gadgets.
	// Max standard (non-operator-claimed) drones = numAttackers * 2.
	// Prefer to keep drones that have an owner or operator from packet data;
	// demote unassigned ones with lowest position count first.
	{
		numAttackers := len(attackerPlayers)
		maxStandard := numAttackers * 2

		// Collect indices of standard Attack drones (gadgetName still empty = not operator-claimed)
		var stdIdxs []int
		for ci, c := range candidates {
			if c.Type == EntityDrone && c.Team == Attack && c.GadgetName == "" {
				stdIdxs = append(stdIdxs, ci)
			}
		}

		if len(stdIdxs) > maxStandard {
			// Sort: keep drones with owner/operator first, then by position count desc
			sort.Slice(stdIdxs, func(i, j int) bool {
				ci, cj := stdIdxs[i], stdIdxs[j]
				ai := candidates[ci]
				aj := candidates[cj]
				hasI := ai.OwnerIndex >= 0 || ai.OperatorIndex >= 0
				hasJ := aj.OwnerIndex >= 0 || aj.OperatorIndex >= 0
				if hasI != hasJ {
					return hasI // assigned before unassigned
				}
				ed1 := entityMap[ai.EntityRef]
				ed2 := entityMap[aj.EntityRef]
				p1, p2 := 0, 0
				if ed1 != nil {
					p1 = len(ed1.positions)
				}
				if ed2 != nil {
					p2 = len(ed2.positions)
				}
				return p1 > p2
			})
			for k := maxStandard; k < len(stdIdxs); k++ {
				candidates[stdIdxs[k]].Type = EntityGadget
				log.Debug().
					Uint32("entityRef", candidates[stdIdxs[k]].EntityRef).
					Msg("drone_cap_demoted")
			}
		}
	}

	// Phase 3d: Assign default gadget names to entities that weren't claimed
	for ci := range candidates {
		c := &candidates[ci]
		if c.GadgetName != "" {
			continue
		}
		switch c.Type {
		case EntityDrone:
			c.GadgetName = "Drone"
		case EntityCamera:
			c.GadgetName = "Camera"
		case EntityProjectile:
			c.GadgetName = "Projectile"
		case EntityGadget:
			c.GadgetName = "Gadget"
		}
	}

	// Phase 3e: Resolve drone operator from confirmed connect/disconnect events.
	// Walk the droneEvents timeline: the LAST player who connected to each drone
	// is the operator. This is confirmed from 04 FF packet data — the player
	// was verifiably on the drone. (At round end most players have disconnected,
	// so "currently connected" would always be -1; instead we track last confirmed user.)
	{
		refToCandidate := make(map[uint32]int)
		for ci, c := range candidates {
			refToCandidate[c.EntityRef] = ci
		}

		// For each drone, find the last connect event (most recent confirmed operator)
		type lastConn struct {
			playerRef uint32
			seq       int
		}
		droneLastConn := make(map[uint32]lastConn)
		for _, ev := range r.DroneEvents {
			if !ev.Connect {
				continue
			}
			if prev, ok := droneLastConn[ev.DroneRef]; !ok || ev.Seq > prev.seq {
				droneLastConn[ev.DroneRef] = lastConn{ev.PlayerRef, ev.Seq}
			}
		}

		for droneRef, lc := range droneLastConn {
			ci, ok := refToCandidate[droneRef]
			if !ok {
				continue
			}
			pIdx, ok := entityToPlayer[lc.playerRef]
			if !ok {
				continue
			}
			candidates[ci].OperatorIndex = pIdx
			log.Debug().
				Uint32("droneRef", droneRef).
				Int("operatorIdx", pIdx).
				Str("operator", r.Header.Players[pIdx].Username).
				Msg("drone_operator_confirmed")
		}
	}

	// Phase 3f: Proximity-based ownership for unowned entities.
	// For gadgets, cameras, and projectiles without an owner, find the player of
	// matching team whose position at the entity's first-seq time is closest.
	// Players deploy gadgets at their feet, so proximity is a strong signal.
	{
		// Build per-player position lookup: for each player, collect positions
		// sorted by sequence number for interpolation.
		type pPos struct {
			x, y, z float32
			seq     int
		}
		playerPositions := make(map[int][]pPos)
		for i := range r.PositionUpdates {
			pu := &r.PositionUpdates[i]
			if pu.PlayerIndex < 0 {
				continue
			}
			playerPositions[pu.PlayerIndex] = append(playerPositions[pu.PlayerIndex], pPos{pu.X, pu.Y, pu.Z, pu.Seq})
		}
		// Sort each player's positions by seq
		for pIdx := range playerPositions {
			pp := playerPositions[pIdx]
			sort.Slice(pp, func(i, j int) bool { return pp[i].seq < pp[j].seq })
			playerPositions[pIdx] = pp
		}

		// Find player position at a given seq (nearest by seq)
		findPlayerPos := func(pIdx, seq int) (float32, float32, float32, bool) {
			pp := playerPositions[pIdx]
			if len(pp) == 0 {
				return 0, 0, 0, false
			}
			// Binary search for closest seq
			lo, hi := 0, len(pp)-1
			for lo < hi {
				mid := (lo + hi) / 2
				if pp[mid].seq < seq {
					lo = mid + 1
				} else {
					hi = mid
				}
			}
			// Check lo and lo-1 for closest
			best := lo
			if lo > 0 {
				if abs(pp[lo-1].seq-seq) < abs(pp[lo].seq-seq) {
					best = lo - 1
				}
			}
			return pp[best].x, pp[best].y, pp[best].z, true
		}

		proximityAssigned := 0
		for ci := range candidates {
			c := &candidates[ci]
			if c.OwnerIndex >= 0 {
				continue // already has an owner
			}
			if c.Type == EntityWeapon {
				continue // weapons don't need proximity ownership
			}
			ed := entityMap[c.EntityRef]
			if ed == nil || len(ed.positions) == 0 {
				continue
			}
			ep := ed.positions[0] // entity's first position (deployment point)

			// Barricades need special handling: they appear at very early seq values
			// before players have moved, so single-point proximity fails.
			// Instead, scan ALL player positions and find whoever was closest.
			isBarricadeCandidate := false
			if counter, ok := spawnCounters[c.EntityRef]; ok && counter == 130 {
				isBarricadeCandidate = r.isBarricadeEntity(c.EntityRef)
			}

			bestDist := float64(10.0) // max radius for attribution
			bestPIdx := -1

			if isBarricadeCandidate {
				// For barricades: scan all player positions to find nearest same-team player
				// who was ever within 5m (XY-plane) of the barricade location.
				// Use 2D distance because barricade Z can differ from player Z
				// (e.g. exterior window barricades vs interior player position).
				bestDist = float64(5.0)
				for pIdx := range r.Header.Players {
					isAtkPlayer := attackerPlayers[pIdx]
					isAtkEntity := c.Team == Attack
					if isAtkPlayer != isAtkEntity {
						continue
					}
					for _, pp := range playerPositions[pIdx] {
						dx := float64(ep.X - pp.x)
						dy := float64(ep.Y - pp.y)
						dist := math.Sqrt(dx*dx + dy*dy)
						if dist < bestDist {
							bestDist = dist
							bestPIdx = pIdx
						}
					}
				}
			} else {
				// Standard proximity: find nearest player at entity's firstSeq time
				targetSeq := ed.firstSeq
				for pIdx := range r.Header.Players {
					isAtkPlayer := attackerPlayers[pIdx]
					isAtkEntity := c.Team == Attack
					if isAtkPlayer != isAtkEntity {
						continue
					}
					px, py, pz, ok := findPlayerPos(pIdx, targetSeq)
					if !ok {
						continue
					}
					dx := float64(ep.X - px)
					dy := float64(ep.Y - py)
					dz := float64(ep.Z - pz)
					dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
					if dist < bestDist {
						bestDist = dist
						bestPIdx = pIdx
					}
				}
			}
			if bestPIdx >= 0 {
				c.OwnerIndex = bestPIdx
				proximityAssigned++
			}
		}
		log.Debug().Int("proximityAssigned", proximityAssigned).Msg("entity_proximity_ownership")
	}

	// Phase 4: Build final TrackedEntities
	r.TrackedEntities = make([]TrackedEntity, 0, len(candidates))
	for _, c := range candidates {
		// Copy positions from entityMap
		if ed, ok := entityMap[c.EntityRef]; ok {
			c.Positions = ed.positions
		}
		r.TrackedEntities = append(r.TrackedEntities, c.TrackedEntity)
	}

	// Sort by entity type then position count for stable output
	sort.Slice(r.TrackedEntities, func(i, j int) bool {
		if r.TrackedEntities[i].Type != r.TrackedEntities[j].Type {
			return r.TrackedEntities[i].Type < r.TrackedEntities[j].Type
		}
		return len(r.TrackedEntities[i].Positions) > len(r.TrackedEntities[j].Positions)
	})

	drones, cameras, gadgets, projectiles, unknown := 0, 0, 0, 0, 0
	for _, te := range r.TrackedEntities {
		switch te.Type {
		case EntityDrone:
			drones++
		case EntityCamera:
			cameras++
		case EntityGadget:
			gadgets++
		case EntityProjectile:
			projectiles++
		default:
			unknown++
		}
	}

	// Log operator gadget claims for debugging
	for _, og := range append(atkOpGadgets, defOpGadgets...) {
		log.Debug().
			Str("operator", og.opName).
			Int("playerIdx", og.playerIdx).
			Str("gadgetType", string(og.gadgetType)).
			Int("claimed", og.claimed).
			Int("maxCount", og.maxCount).
			Msg("operator_gadget_claim")
	}

	log.Debug().
		Int("total", len(r.TrackedEntities)).
		Int("drones", drones).
		Int("cameras", cameras).
		Int("gadgets", gadgets).
		Int("projectiles", projectiles).
		Int("unknown", unknown).
		Msg("tracked_entities")
}

// buildShotEvents detects shots fired from ammo decrease events and pairs them
// with the player's position/orientation at that moment. Each consecutive ammo
// event where the available count decreases by 1 (same weapon, same capacity)
// is recorded as a shot. The player's nearest position update (by time) provides
// the X/Y/Z/Yaw/Pitch for bullet tracer origin.
func (r *Reader) buildShotEvents(entityToPlayer map[uint32]int) {
	if len(r.AmmoUpdates) == 0 || len(r.PositionUpdates) == 0 {
		return
	}

	// Build per-player position index sorted by binary offset for temporal matching
	type playerPos struct {
		x, y, z, yaw, pitch float32
		hqX, hqY, hqZ, hqW  float32
		binOffset           int
		seq                 int
	}
	playerPositions := make(map[int][]playerPos) // playerIndex -> sorted positions
	for _, pu := range r.PositionUpdates {
		if pu.PlayerIndex < 0 {
			continue
		}
		playerPositions[pu.PlayerIndex] = append(playerPositions[pu.PlayerIndex], playerPos{
			x: pu.X, y: pu.Y, z: pu.Z, yaw: pu.Yaw, pitch: pu.Pitch,
			hqX: pu.HeadQX, hqY: pu.HeadQY, hqZ: pu.HeadQZ, hqW: pu.HeadQW,
			binOffset: pu.BinOffset, seq: pu.Seq,
		})
	}

	// Detect ammo decreases: consecutive events for same weapon where available drops
	type weaponState struct {
		lastAvailable uint32
		lastCapacity  uint32
	}
	weaponStates := make(map[uint32]weaponState) // weaponRef -> last state

	for i := range r.AmmoUpdates {
		au := &r.AmmoUpdates[i]
		if au.PlayerIndex < 0 {
			continue
		}

		prev, exists := weaponStates[au.weaponRef]
		weaponStates[au.weaponRef] = weaponState{au.Available, au.Capacity}

		if !exists {
			continue
		}
		// A shot: available decreased (capacity may also decrease by same amount)
		if prev.lastAvailable > 0 && au.Available < prev.lastAvailable {
			decrease := prev.lastAvailable - au.Available
			// Skip reloads/weapon swaps: large jumps (>10 bullets) are not shots
			if decrease > 10 {
				continue
			}
			positions := playerPositions[au.PlayerIndex]
			if len(positions) == 0 {
				continue
			}

			// Match ammo update to the nearest player position.
			// Primary: binary search by binary offset (works when ammo and position
			// data are in the same byte region, e.g. pre-Y11S1).
			// Fallback: when all position offsets are smaller than the ammo offset
			// (Y11S1+: position stream 0-55 MB, ammo stream 61.9 MB+), use the
			// ammo event's TimeInSeconds mapped proportionally to the position array.
			targetOffset := au.BinOffset
			maxPosOffset := positions[len(positions)-1].binOffset
			bestIdx := 0

			if targetOffset > maxPosOffset {
				// au.TimeInSeconds is a COUNTDOWN (seconds remaining), not elapsed.
				// Find the maximum countdown across all ammo updates (≈ round start).
				maxT := 0.0
				for _, a2 := range r.AmmoUpdates {
					if a2.TimeInSeconds > maxT {
						maxT = a2.TimeInSeconds
					}
				}
				if maxT <= 0 {
					maxT = 150
				}
				// Convert countdown to elapsed fraction: elapsed = 1 - countdown/maxCountdown
				elapsed := 1.0 - au.TimeInSeconds/maxT
				if elapsed < 0 {
					elapsed = 0
				} else if elapsed > 1 {
					elapsed = 1
				}
				idx := int(elapsed * float64(len(positions)-1))
				if idx < 0 {
					idx = 0
				} else if idx >= len(positions) {
					idx = len(positions) - 1
				}
				bestIdx = idx
			} else {
				lo, hi := 0, len(positions)-1
				for lo <= hi {
					mid := (lo + hi) / 2
					if positions[mid].binOffset < targetOffset {
						lo = mid + 1
					} else {
						hi = mid - 1
					}
				}
				bestDist := abs(positions[0].binOffset - targetOffset)
				for _, idx := range []int{lo - 1, lo, lo + 1} {
					if idx >= 0 && idx < len(positions) {
						d := abs(positions[idx].binOffset - targetOffset)
						if d < bestDist {
							bestDist = d
							bestIdx = idx
						}
					}
				}
			}

			pos := positions[bestIdx]
			for shot := uint32(0); shot < decrease; shot++ {
				r.ShotEvents = append(r.ShotEvents, ShotEvent{
					PlayerIndex:   au.PlayerIndex,
					X:             pos.x,
					Y:             pos.y,
					Z:             pos.z,
					Yaw:           pos.yaw,
					Pitch:         pos.pitch,
					HeadQX:        pos.hqX,
					HeadQY:        pos.hqY,
					HeadQZ:        pos.hqZ,
					HeadQW:        pos.hqW,
					TimeInSeconds: au.TimeInSeconds,
					Seq:           pos.seq,
				})
			}
		}
	}

	log.Debug().Int("shots", len(r.ShotEvents)).Msg("shot_events")
}

// scanCameraFrames scans the binary buffer for camera rotation quaternions.
// Pass 4 (Single-POV): Signature 0xa5b2f3a5 at i, 0x01 at i+4, 0x02 at i+12,
// then quaternion at i+16 (4×float32). Assigned to the player entity with the
// most position frames.
func (r *Reader) scanCameraFrames(entityToPlayer map[uint32]int) {
	if len(r.b) < 32 {
		return
	}

	// Find the player with the most position frames (the recorded POV)
	entityFrameCount := make(map[uint32]int)
	for i := range r.PositionUpdates {
		pu := &r.PositionUpdates[i]
		if pu.PlayerIndex >= 0 {
			entityFrameCount[pu.EntityRef]++
		}
	}
	bestEntity := uint32(0)
	bestCount := 0
	for ref, count := range entityFrameCount {
		if ref>>24 >= 0xf0 && count > bestCount {
			bestCount = count
			bestEntity = ref
		}
	}
	bestPlayer := -1
	if bestEntity != 0 {
		if pIdx, ok := entityToPlayer[bestEntity]; ok {
			bestPlayer = pIdx
		}
	}

	// Scan for camera signature: a5 b2 f3 a5
	sig := uint32(0xa5b2f3a5)
	for i := 0; i+32 <= len(r.b); i++ {
		v := binary.LittleEndian.Uint32(r.b[i : i+4])
		if v != sig {
			continue
		}
		// Check 0x01 at i+4
		if binary.LittleEndian.Uint32(r.b[i+4:i+8]) != 0x01 {
			continue
		}
		// Check 0x02 at i+12
		if binary.LittleEndian.Uint32(r.b[i+12:i+16]) != 0x02 {
			continue
		}
		// Read quaternion at i+16
		if i+32 > len(r.b) {
			continue
		}
		qx := math.Float32frombits(binary.LittleEndian.Uint32(r.b[i+16 : i+20]))
		qy := math.Float32frombits(binary.LittleEndian.Uint32(r.b[i+20 : i+24]))
		qz := math.Float32frombits(binary.LittleEndian.Uint32(r.b[i+24 : i+28]))
		qw := math.Float32frombits(binary.LittleEndian.Uint32(r.b[i+28 : i+32]))

		// Validate unit quaternion
		mag := float64(qx)*float64(qx) + float64(qy)*float64(qy) +
			float64(qz)*float64(qz) + float64(qw)*float64(qw)
		if mag < 0.95 || mag > 1.05 {
			continue
		}

		// YXZ Euler extraction for camera pitch
		sinP := 2.0 * (float64(qw)*float64(qx) - float64(qy)*float64(qz))
		if sinP > 1 {
			sinP = 1
		} else if sinP < -1 {
			sinP = -1
		}
		pitchDeg := float32(math.Asin(sinP) * 180 / math.Pi)

		// Yaw from quaternion
		yawDeg := float32(2.0 * math.Atan2(float64(qz), float64(qw)) * 180 / math.Pi)

		r.CameraFrames = append(r.CameraFrames, CameraFrame{
			PlayerIndex: bestPlayer,
			Qx:          qx,
			Qy:          qy,
			Qz:          qz,
			Qw:          qw,
			YawDeg:      yawDeg,
			PitchDeg:    pitchDeg,
			BinOffset:   i,
		})
	}

	log.Debug().Int("pass4CameraFrames", len(r.CameraFrames)).Int("assignedPlayer", bestPlayer).Msg("camera_pass4")
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// inferStance assigns stance annotations ("standing", "crouching", "prone") to
// player position updates based on Z-height deviation from the player's baseline.
// The baseline is the minimum Z observed across all frames for each player entity.
// When standing, the player's head/body Z is highest; when prone, Z is lowest (at baseline).
// Thresholds: dz > 0.5 = standing, 0.15..0.5 = crouching, ≤0.15 = prone.
func (r *Reader) inferStance() {
	// Compute baseline Z (minimum) per entity
	baselineZ := make(map[uint32]float32)
	for i := range r.PositionUpdates {
		pu := &r.PositionUpdates[i]
		if pu.PlayerIndex < 0 {
			continue
		}
		if prev, ok := baselineZ[pu.EntityRef]; !ok || pu.Z < prev {
			baselineZ[pu.EntityRef] = pu.Z
		}
	}

	standing, crouching, prone := 0, 0, 0
	for i := range r.PositionUpdates {
		pu := &r.PositionUpdates[i]
		if pu.PlayerIndex < 0 {
			continue
		}
		bl, ok := baselineZ[pu.EntityRef]
		if !ok {
			continue
		}
		dz := pu.Z - bl
		switch {
		case dz > 0.5:
			pu.Stance = "standing"
			standing++
		case dz > 0.15:
			pu.Stance = "crouching"
			crouching++
		default:
			pu.Stance = "prone"
			prone++
		}
	}
	log.Debug().Int("standing", standing).Int("crouching", crouching).Int("prone", prone).Msg("stance_inference")
}

// buildDeathTimings estimates when each player died by finding their last position
// update with significant displacement (>0.1m from previous). Players who didn't
// die get no entry. Death events are cross-referenced from MatchFeedback.
func (r *Reader) buildDeathTimings() {
	// Find players who died from match feedback
	diedPlayers := make(map[int]bool)
	for _, mf := range r.MatchFeedback {
		if mf.Type == Kill || mf.Type == Death {
			// Find target player index
			for i, p := range r.Header.Players {
				if p.Username == mf.Target {
					diedPlayers[i] = true
					break
				}
			}
		}
	}
	if len(diedPlayers) == 0 {
		return
	}

	// For each dead player, find the last position with significant movement
	type lastMove struct {
		seq     int
		time    float64
		x, y, z float32
	}
	playerLastMove := make(map[int]*lastMove)
	playerPrevPos := make(map[int]*PositionUpdate)

	for i := range r.PositionUpdates {
		pu := &r.PositionUpdates[i]
		if pu.PlayerIndex < 0 || !diedPlayers[pu.PlayerIndex] {
			continue
		}
		prev := playerPrevPos[pu.PlayerIndex]
		playerPrevPos[pu.PlayerIndex] = pu
		if prev == nil {
			playerLastMove[pu.PlayerIndex] = &lastMove{pu.Seq, r.time, pu.X, pu.Y, pu.Z}
			continue
		}
		dx := float64(pu.X - prev.X)
		dy := float64(pu.Y - prev.Y)
		dz := float64(pu.Z - prev.Z)
		dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
		if dist > 0.1 {
			playerLastMove[pu.PlayerIndex] = &lastMove{pu.Seq, r.time, pu.X, pu.Y, pu.Z}
		}
	}

	for pIdx, lm := range playerLastMove {
		if lm == nil {
			continue
		}
		r.DeathTimings = append(r.DeathTimings, DeathTiming{
			PlayerIndex:      pIdx,
			LastMovementSeq:  lm.seq,
			LastMovementTime: lm.time,
			LastX:            lm.x,
			LastY:            lm.y,
			LastZ:            lm.z,
		})
	}
	sort.Slice(r.DeathTimings, func(i, j int) bool {
		return r.DeathTimings[i].LastMovementSeq < r.DeathTimings[j].LastMovementSeq
	})
	log.Debug().Int("deathTimings", len(r.DeathTimings)).Msg("death_timings")
}

// scanGameActions scans the binary buffer for hardcoded game action patterns.
// Reinforce Complete: 46 00 00 00 00 00 00 00 04 35
// Gadget Deployed:    50 00 00 00 00 00 00 00 04 3F
// Deduplicates actions within 1 second of each other.
func (r *Reader) scanGameActions() {
	if len(r.b) < 10 {
		return
	}

	type actionPattern struct {
		name    string
		pattern [10]byte
	}
	patterns := []actionPattern{
		{"reinforce", [10]byte{0x46, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x04, 0x35}},
		{"gadget_deploy", [10]byte{0x50, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x04, 0x3F}},
	}

	// Build sorted timer tick table for offset → time interpolation.
	// Timer ticks provide (byteOffset, secondsRemaining) pairs.
	type tickEntry struct {
		offset  int64
		seconds float32
	}
	var ticks []tickEntry
	for _, t := range r.TimerTicks {
		if t.Seconds > 0 { // skip noise ticks at exactly 0
			ticks = append(ticks, tickEntry{t.Offset, t.Seconds})
		}
	}
	sort.Slice(ticks, func(i, j int) bool { return ticks[i].offset < ticks[j].offset })

	// offsetToTime interpolates between timer ticks to estimate countdown seconds
	// remaining for a given binary offset. Returns -1 if no ticks are available.
	offsetToTime := func(off int) float64 {
		if len(ticks) == 0 {
			return -1
		}
		o := int64(off)
		// Before first tick → extrapolate from first tick
		if o <= ticks[0].offset {
			return float64(ticks[0].seconds)
		}
		// After last tick → extrapolate from last tick
		if o >= ticks[len(ticks)-1].offset {
			return float64(ticks[len(ticks)-1].seconds)
		}
		// Binary search for surrounding ticks
		lo, hi := 0, len(ticks)-1
		for lo < hi-1 {
			mid := (lo + hi) / 2
			if ticks[mid].offset <= o {
				lo = mid
			} else {
				hi = mid
			}
		}
		// Linear interpolation between ticks[lo] and ticks[hi]
		t0, t1 := ticks[lo], ticks[hi]
		frac := float64(o-t0.offset) / float64(t1.offset-t0.offset)
		return float64(t0.seconds) + frac*(float64(t1.seconds)-float64(t0.seconds))
	}

	// Track last action offset per type for dedup. The reinforce/gadget_deploy
	// byte patterns match multiple times per real event (the same pattern appears
	// in repeated data structures). Real events are typically ≥3 seconds apart in
	// the binary, so we dedup based on estimated time distance.
	lastActionTime := make(map[string]float64)

	for i := 0; i+10 <= len(r.b); i++ {
		for _, ap := range patterns {
			match := true
			for j := 0; j < 10; j++ {
				if r.b[i+j] != ap.pattern[j] {
					match = false
					break
				}
			}
			if !match {
				continue
			}

			timeSec := offsetToTime(i)
			if timeSec < 0 {
				timeSec = 0
			}

			// Deduplicate: skip if same action type within 3 seconds
			if prev, ok := lastActionTime[ap.name]; ok && math.Abs(timeSec-prev) < 3.0 {
				continue
			}
			lastActionTime[ap.name] = timeSec

			r.GameActions = append(r.GameActions, GameAction{
				Type:          ap.name,
				TimeInSeconds: timeSec,
				BinOffset:     i,
			})
		}
	}
	sort.Slice(r.GameActions, func(i, j int) bool {
		return r.GameActions[i].BinOffset < r.GameActions[j].BinOffset
	})
	log.Debug().Int("gameActions", len(r.GameActions)).Msg("game_actions")
}

// classifyEntityFlags performs flag-based entity sub-classification after
// buildTrackedEntities has done behavioral classification. It scans the binary
// for FC-UPDATE packets per entity and uses flag values to identify:
//   - Projectile subtypes: grapple (0x0280), impact (0x07C0/0x1FC0), thrown (0x0380/0x07C0/0x3FC0)
//   - Weapon entities: SPAWN counter 138 (primary) or 254 (secondary)
//   - Gadget fingerprints: flag 0x0440 pktSize + 0x03C0 presence
func (r *Reader) classifyEntityFlags() {
	if len(r.b) == 0 || len(r.TrackedEntities) == 0 {
		return
	}

	archetypeMarker := [4]byte{0x60, 0x73, 0x85, 0xFE}

	// Collect FC-UPDATE flags per entity and SPAWN counter per entity
	entityFlags := make(map[uint32]map[uint16]bool) // entityRef -> set of flags seen
	entityFirstFlag := make(map[uint32]uint16)      // first flag seen per entity
	entityPktSize0440 := make(map[uint32]uint32)    // pktSize of first 0x0440 flag
	entity0440Count := make(map[uint32]int)         // count of 0x0440 packets
	entityHas03C0 := make(map[uint32]bool)          // has flag 0x03C0

	for i := 0; i+18 <= len(r.b); i += 4 {
		if r.b[i+12] != archetypeMarker[0] || r.b[i+13] != archetypeMarker[1] ||
			r.b[i+14] != archetypeMarker[2] || r.b[i+15] != archetypeMarker[3] {
			continue
		}
		// Null check at offset +4
		if binary.LittleEndian.Uint32(r.b[i+4:i+8]) != 0 {
			continue
		}
		entityID := binary.LittleEndian.Uint32(r.b[i : i+4])
		counter := binary.LittleEndian.Uint32(r.b[i+8 : i+12])
		flag := binary.LittleEndian.Uint16(r.b[i+16 : i+18])

		if counter == 0 {
			continue
		}

		if entityFlags[entityID] == nil {
			entityFlags[entityID] = make(map[uint16]bool)
		}
		entityFlags[entityID][flag] = true

		if _, seen := entityFirstFlag[entityID]; !seen {
			entityFirstFlag[entityID] = flag
		}

		if flag == 0x0440 {
			if entity0440Count[entityID] == 0 {
				entityPktSize0440[entityID] = counter
			}
			entity0440Count[entityID]++
		}
		if flag == 0x03C0 {
			entityHas03C0[entityID] = true
		}
	}

	projectileFlags := map[uint16]bool{0x0380: true, 0x07C0: true, 0x3FC0: true}

	for ti := range r.TrackedEntities {
		te := &r.TrackedEntities[ti]
		flags := entityFlags[te.EntityRef]
		if flags == nil {
			continue
		}

		// Grapple line: unique flag 0x0280
		if flags[0x0280] {
			te.Type = EntityProjectile
			te.ProjectileType = "grapple"
			continue
		}

		// Check if ALL flags are in the projectile set
		allProjectile := true
		for f := range flags {
			if !projectileFlags[f] {
				allProjectile = false
				break
			}
		}
		if allProjectile && len(flags) > 0 {
			te.Type = EntityProjectile
			if flags[0x07C0] {
				te.ProjectileType = "impact"
			}
			continue
		}

		// Impact grenade: first flag 0x1FC0
		if te.Type == EntityProjectile && te.ProjectileType == "" {
			if entityFirstFlag[te.EntityRef] == 0x1FC0 {
				te.ProjectileType = "impact"
			}
		}

		// Gadget fingerprinting from 0x0440 flag packets
		if entity0440Count[te.EntityRef] > 0 && (te.Type == EntityGadget || te.GadgetName == "") {
			pktSz := entityPktSize0440[te.EntityRef]
			switch pktSz {
			case 24:
				// Ambiguous: trip, prisma, kiba — disambiguate by frame count
				if entity0440Count[te.EntityRef] > 200 {
					te.GadgetType = "prisma"
				}
				// Otherwise resolved later by operator proximity
			case 21:
				if entityHas03C0[te.EntityRef] {
					te.GadgetType = "gu"
				} else {
					te.GadgetType = "electroclaw"
				}
			}
		}
	}

	classified := 0
	for _, te := range r.TrackedEntities {
		if te.ProjectileType != "" || te.GadgetType != "" {
			classified++
		}
	}
	log.Debug().Int("flagClassified", classified).Msg("entity_flag_classification")
}

// spawnHashGadgetType maps the 4-byte hash at SPAWN offset +60 to a gadget type key.
// These hashes uniquely identify gadget classes from the SPAWN record.
var spawnHashGadgetType = map[uint32]string{
	// counter=142 gadgets (hash_a at +60)
	0x2D1E3A9A: "jammer",     // Mute Jammer
	0x2D1AAB9A: "welcomemat", // Frost Welcome Mat
	0xD2F8F39A: "battery",    // Bandit Battery
	0xFC72B39A: "volcan",     // Goyo Canister
	0x2D1E3B16: "c4",         // Nitro Cell (C4)
	0x9B72AE9A: "kiba",       // Azami Kiba Barrier (counter=138)
}

// spawnHashPairGadgetType maps (hash_a, hash_b) pairs at +60/+64 for counter=146 entities.
var spawnHashPairGadgetType = map[[2]uint32]string{
	{0x133B519A, 0x0CC9B9B2}: "kona",        // Thunderbird Kóna Station
	{0x133B519A, 0x4F01B6B2}: "banshee",     // Melusi Banshee
	{0x133B519A, 0x2D1C4FB2}: "ads",         // Jäger ADS
	{0x1CA56E9A, 0x2D1DAE35}: "blackmirror", // Mira Black Mirror
}

// spawnHash150GadgetType maps hash_a at +60 for counter=150 entities (secondary equipment).
var spawnHash150GadgetType = map[uint32]string{
	0x1CA56E9A: "deployableshield", // Deployable Shield
}

// spawnHash126GadgetType maps hash at +56 for counter=126 entities.
var spawnHash126GadgetType = map[uint32]string{
	0x45324600: "prisma", // Alibi Prisma
}

// classifySpawnCounters scans the binary for SPAWN records (archetype 0xFE857361)
// and uses the counter value to classify entity types:
//   - 494 = player entity (used for player entity mapping)
//   - 146 = deployed gadget
//   - 154 = player-controlled drone
//   - 142 = secondary gadget (impact grenade, proximity alarm, etc.)
//   - 126 = Alibi Prisma
//   - 150 = secondary equipment (deployable shield, etc.)
//   - 130 = barricade (door/window) — distinguished by FC-UPDATE flag 0x1FE0
//   - 138 = primary weapon (dropped) or Azami Kiba (disambiguated by SPAWN hash)
//   - 254 = secondary weapon (dropped)
func (r *Reader) classifySpawnCounters() {
	if len(r.b) == 0 || len(r.TrackedEntities) == 0 {
		return
	}

	spawnArchetype := [4]byte{0x61, 0x73, 0x85, 0xFE}

	// Map entity IDs to TrackedEntity indices for fast lookup
	entityToIdx := make(map[uint32]int)
	for ti := range r.TrackedEntities {
		entityToIdx[r.TrackedEntities[ti].EntityRef] = ti
	}

	// First pass: extract SPAWN hash gadget identifications
	spawnHashes := r.extractSpawnHashes()

	for i := 12; i+8 <= len(r.b); i++ {
		if r.b[i] != spawnArchetype[0] || r.b[i+1] != spawnArchetype[1] ||
			r.b[i+2] != spawnArchetype[2] || r.b[i+3] != spawnArchetype[3] {
			continue
		}

		entityID := binary.LittleEndian.Uint32(r.b[i-12 : i-8])
		counter := binary.LittleEndian.Uint32(r.b[i-4 : i])

		ti, ok := entityToIdx[entityID]
		if !ok {
			continue
		}
		te := &r.TrackedEntities[ti]
		if te.SpawnCounter != 0 {
			continue // already classified from an earlier SPAWN hit
		}
		te.SpawnCounter = counter

		// Apply SPAWN hash gadget type if available
		if gt, ok := spawnHashes[entityID]; ok {
			te.GadgetType = gt
		}

		switch counter {
		case 126, 142, 146, 150:
			// Gadget entities — use SPAWN hash if available, fall back to fingerprinting
			if te.Type != EntityGadget && te.Type != EntityCamera {
				te.Type = EntityGadget
			}
		case 154:
			// Player-controlled drone
			if te.Type != EntityDrone {
				te.Type = EntityDrone
			}
		case 130:
			// Barricade (door/window) — check FC-UPDATE flag 0x1FE0 to confirm
			if r.isBarricadeEntity(entityID) {
				te.Type = EntityBarricade
				te.IsBarricade = true
				te.BarricadeType = "barricade"
				te.GadgetName = "Barricade"
			}
		case 138:
			// Counter=138 can be Azami Kiba (gadget) or dropped weapon
			if gt, ok := spawnHashes[entityID]; ok {
				// SPAWN hash says it's a gadget (e.g. kiba)
				te.Type = EntityGadget
				te.GadgetType = gt
				te.GadgetName = "Kiba Barrier"
			} else {
				te.Type = EntityWeapon
				te.GadgetName = "Primary Weapon"
			}
		case 254:
			te.Type = EntityWeapon
			te.GadgetName = "Secondary Weapon"
		}
	}

	var gadgets, drones, weapons, barricades int
	for _, te := range r.TrackedEntities {
		switch {
		case te.IsBarricade:
			barricades++
		case te.SpawnCounter == 146 || te.SpawnCounter == 142 || te.SpawnCounter == 126 || te.SpawnCounter == 150:
			gadgets++
		case te.SpawnCounter == 154:
			drones++
		case te.SpawnCounter == 138 || te.SpawnCounter == 254:
			if te.Type == EntityGadget {
				gadgets++ // Kiba counted as gadget
			} else {
				weapons++
			}
		}
	}
	log.Debug().Int("spawnGadgets", gadgets).Int("spawnDrones", drones).Int("spawnWeapons", weapons).Int("barricades", barricades).Msg("spawn_counter_classification")
}

// extractSpawnHashes scans SPAWN records and extracts gadget type hashes.
// Returns a map from entity ID to gadget type key (e.g. "jammer", "welcomemat").
func (r *Reader) extractSpawnHashes() map[uint32]string {
	result := make(map[uint32]string)
	if len(r.b) < 80 {
		return result
	}

	spawnArchetype := [4]byte{0x61, 0x73, 0x85, 0xFE}

	for i := 12; i+68 <= len(r.b); i++ {
		if r.b[i] != spawnArchetype[0] || r.b[i+1] != spawnArchetype[1] ||
			r.b[i+2] != spawnArchetype[2] || r.b[i+3] != spawnArchetype[3] {
			continue
		}
		if binary.LittleEndian.Uint32(r.b[i-8:i-4]) != 0 {
			continue
		}
		eid := binary.LittleEndian.Uint32(r.b[i-12 : i-8])
		if eid>>24 < 0xF0 {
			continue
		}
		if i+68 > len(r.b) {
			continue
		}
		if binary.LittleEndian.Uint32(r.b[i+4:i+8]) != eid {
			continue
		}
		counter := binary.LittleEndian.Uint32(r.b[i-4 : i])

		// Skip player (494), drone (154), barricade (130) entities
		if counter == 494 || counter == 154 || counter == 130 {
			continue
		}

		// Already identified — skip (first SPAWN wins)
		if _, ok := result[eid]; ok {
			continue
		}

		hashA := binary.LittleEndian.Uint32(r.b[i+60 : i+64])

		switch counter {
		case 142:
			if gt, ok := spawnHashGadgetType[hashA]; ok {
				result[eid] = gt
			}
		case 146:
			hashB := binary.LittleEndian.Uint32(r.b[i+64 : i+68])
			pair := [2]uint32{hashA, hashB}
			if gt, ok := spawnHashPairGadgetType[pair]; ok {
				result[eid] = gt
			} else if gt, ok := spawnHashGadgetType[hashA]; ok {
				result[eid] = gt
			}
		case 138:
			if gt, ok := spawnHashGadgetType[hashA]; ok {
				result[eid] = gt
			}
		case 150:
			if gt, ok := spawnHash150GadgetType[hashA]; ok {
				result[eid] = gt
			}
		case 126:
			hash56 := binary.LittleEndian.Uint32(r.b[i+56 : i+60])
			if gt, ok := spawnHash126GadgetType[hash56]; ok {
				result[eid] = gt
			}
		}
	}
	return result
}

// isBarricadeEntity checks if an entity with counter=130 has FC-UPDATE flag 0x1FE0,
// which distinguishes barricade entities from sentinel/placeholder entities.
func (r *Reader) isBarricadeEntity(entityID uint32) bool {
	if len(r.b) == 0 {
		return false
	}

	archetypeMarker := [4]byte{0x60, 0x73, 0x85, 0xFE}

	for i := 0; i+18 <= len(r.b); i += 4 {
		if r.b[i+12] != archetypeMarker[0] || r.b[i+13] != archetypeMarker[1] ||
			r.b[i+14] != archetypeMarker[2] || r.b[i+15] != archetypeMarker[3] {
			continue
		}
		if binary.LittleEndian.Uint32(r.b[i+4:i+8]) != 0 {
			continue
		}
		eid := binary.LittleEndian.Uint32(r.b[i : i+4])
		if eid != entityID {
			continue
		}
		flag := binary.LittleEndian.Uint16(r.b[i+16 : i+18])
		if flag == 0x1FE0 {
			return true
		}
	}
	return false
}

// preloadSpawnCounters scans the binary for all SPAWN records and returns
// a map from entity ID to counter value, for use before TrackedEntities are built.
func (r *Reader) preloadSpawnCounters() map[uint32]uint32 {
	result := make(map[uint32]uint32)
	if len(r.b) < 40 {
		return result
	}
	spawnArchetype := [4]byte{0x61, 0x73, 0x85, 0xFE}
	for i := 12; i+8 <= len(r.b); i++ {
		if r.b[i] != spawnArchetype[0] || r.b[i+1] != spawnArchetype[1] ||
			r.b[i+2] != spawnArchetype[2] || r.b[i+3] != spawnArchetype[3] {
			continue
		}
		entityID := binary.LittleEndian.Uint32(r.b[i-12 : i-8])
		counter := binary.LittleEndian.Uint32(r.b[i-4 : i])
		if _, ok := result[entityID]; !ok {
			result[entityID] = counter
		}
	}
	return result
}

// extractTimerTicks scans the binary for round timer tick patterns and groups
// them into phases (prep/action) based on gaps in the countdown sequence.
func (r *Reader) extractTimerTicks() {
	if len(r.b) < 10 {
		return
	}

	// Y8S1+ timer pattern: 1F 07 EF C9 04 [uint32 LE seconds-remaining]
	for p := 0; p+9 <= len(r.b); p++ {
		if r.b[p] != 0x1F || r.b[p+1] != 0x07 || r.b[p+2] != 0xEF || r.b[p+3] != 0xC9 {
			continue
		}
		if r.b[p+4] != 0x04 {
			continue
		}
		secs := binary.LittleEndian.Uint32(r.b[p+5 : p+9])
		if secs > 300 { // max 5-minute round
			continue
		}
		r.TimerTicks = append(r.TimerTicks, TimerTick{
			Offset:  int64(p + 3),
			Seconds: float32(secs),
		})
	}

	// Sort by offset (ascending)
	sort.Slice(r.TimerTicks, func(i, j int) bool {
		return r.TimerTicks[i].Offset < r.TimerTicks[j].Offset
	})

	// Filter: skip ticks at very early offsets (header noise)
	var filtered []TimerTick
	for _, t := range r.TimerTicks {
		if t.Offset > 1000 {
			filtered = append(filtered, t)
		}
	}
	r.TimerTicks = filtered

	if len(r.TimerTicks) == 0 {
		log.Debug().Msg("no_timer_ticks")
		return
	}

	// Phase grouping: consecutive countdown ticks; gap > 5s = new phase boundary
	type phase struct {
		startSec float32 // highest seconds value (countdown start)
		endSec   float32 // lowest seconds value (countdown end)
	}
	var phases []phase
	cs := r.TimerTicks[0].Seconds // current phase start (high end)
	ce := r.TimerTicks[0].Seconds // current phase end (low end)

	for i := 1; i < len(r.TimerTicks); i++ {
		s := r.TimerTicks[i].Seconds
		// A new phase starts when countdown jumps UP significantly (new timer begins)
		if s > cs+5 {
			phases = append(phases, phase{cs, ce})
			cs = s
			ce = s
		} else {
			if s > cs {
				cs = s
			}
			if s < ce {
				ce = s
			}
		}
	}
	phases = append(phases, phase{cs, ce})

	// Convert to TimerPhase structs, skip phases with only 1 tick (noise)
	r.TimerPhases = make([]TimerPhase, 0, len(phases))
	phaseIdx := 0
	for _, ph := range phases {
		dur := ph.startSec - ph.endSec
		if dur < 2 {
			continue // skip single-tick or trivial phases
		}
		name := "action"
		if phaseIdx == 0 {
			name = "prep"
		}
		r.TimerPhases = append(r.TimerPhases, TimerPhase{
			Name:     name,
			StartSec: ph.startSec,
			EndSec:   ph.endSec,
			Duration: dur,
		})
		phaseIdx++
	}

	log.Debug().Int("timerTicks", len(r.TimerTicks)).Int("phases", len(r.TimerPhases)).Msg("timer_extraction")
}

// detectRecordingPlayer identifies which player's POV was recorded.
// The recording player has the most camera frames (Pass 4) and the most
// position frames among F0-prefix entities.
func (r *Reader) detectRecordingPlayer(entityToPlayer map[uint32]int) {
	// Method 1: Camera frames already assigned to a player
	if len(r.CameraFrames) > 0 {
		r.RecordingPlayer = r.CameraFrames[0].PlayerIndex
		if r.RecordingPlayer >= 0 {
			log.Debug().Int("recordingPlayer", r.RecordingPlayer).Str("method", "camera_frames").Msg("recording_player")
			return
		}
	}

	// Method 2: Player entity with most position updates
	entityFrameCount := make(map[uint32]int)
	for i := range r.PositionUpdates {
		pu := &r.PositionUpdates[i]
		if pu.PlayerIndex >= 0 {
			entityFrameCount[pu.EntityRef]++
		}
	}
	bestEntity := uint32(0)
	bestCount := 0
	for ref, count := range entityFrameCount {
		if ref>>24 >= 0xf0 && count > bestCount {
			bestCount = count
			bestEntity = ref
		}
	}
	if bestEntity != 0 {
		if pIdx, ok := entityToPlayer[bestEntity]; ok {
			r.RecordingPlayer = pIdx
			log.Debug().Int("recordingPlayer", r.RecordingPlayer).Str("method", "most_frames").Msg("recording_player")
			return
		}
	}
	log.Debug().Int("recordingPlayer", -1).Msg("recording_player_unknown")
}

// unwrapYaw provides continuous yaw values without ±180° discontinuities.
// It adjusts each yaw to be within 180° of the previous value for the same entity.
func (r *Reader) unwrapYaw() {
	lastYaw := make(map[uint32]float32)
	unwrapped := 0
	for i := range r.PositionUpdates {
		pu := &r.PositionUpdates[i]
		if pu.Yaw == 0 || math.IsNaN(float64(pu.Yaw)) || math.IsInf(float64(pu.Yaw), 0) {
			continue
		}
		prev, ok := lastYaw[pu.EntityRef]
		if !ok {
			lastYaw[pu.EntityRef] = pu.Yaw
			continue
		}
		diff := pu.Yaw - prev
		for diff > 180 {
			diff -= 360
		}
		for diff < -180 {
			diff += 360
		}
		newYaw := prev + diff
		if !math.IsNaN(float64(newYaw)) && !math.IsInf(float64(newYaw), 0) {
			pu.Yaw = newYaw
			lastYaw[pu.EntityRef] = pu.Yaw
			unwrapped++
		}
	}
	log.Debug().Int("unwrapped", unwrapped).Msg("yaw_unwrap")
}

// sanitizeFloats replaces any NaN/Inf float values in exported data with 0
// to prevent JSON encoding failures.
func (r *Reader) sanitizeFloats() {
	clean := func(v *float32) {
		if math.IsNaN(float64(*v)) || math.IsInf(float64(*v), 0) {
			*v = 0
		}
	}
	cleanF64 := func(v *float64) {
		if math.IsNaN(*v) || math.IsInf(*v, 0) {
			*v = 0
		}
	}
	for i := range r.PositionUpdates {
		pu := &r.PositionUpdates[i]
		clean(&pu.X)
		clean(&pu.Y)
		clean(&pu.Z)
		clean(&pu.Yaw)
		clean(&pu.Pitch)
		clean(&pu.HeadOffX)
		clean(&pu.HeadOffY)
		clean(&pu.HeadOffZ)
		clean(&pu.HeadQX)
		clean(&pu.HeadQY)
		clean(&pu.HeadQZ)
		clean(&pu.HeadQW)
		clean(&pu.ChestOffX)
		clean(&pu.ChestOffY)
		clean(&pu.ChestOffZ)
		clean(&pu.ChestQX)
		clean(&pu.ChestQY)
		clean(&pu.ChestQZ)
		clean(&pu.ChestQW)
	}
	for i := range r.ShotEvents {
		se := &r.ShotEvents[i]
		clean(&se.X)
		clean(&se.Y)
		clean(&se.Z)
		clean(&se.Yaw)
		clean(&se.Pitch)
		clean(&se.HeadQX)
		clean(&se.HeadQY)
		clean(&se.HeadQZ)
		clean(&se.HeadQW)
		cleanF64(&se.TimeInSeconds)
	}
	for i := range r.CameraFrames {
		cf := &r.CameraFrames[i]
		clean(&cf.Qx)
		clean(&cf.Qy)
		clean(&cf.Qz)
		clean(&cf.Qw)
		clean(&cf.YawDeg)
		clean(&cf.PitchDeg)
	}
	for i := range r.TrackedEntities {
		for j := range r.TrackedEntities[i].Positions {
			ep := &r.TrackedEntities[i].Positions[j]
			clean(&ep.X)
			clean(&ep.Y)
			clean(&ep.Z)
			clean(&ep.Yaw)
			clean(&ep.Pitch)
		}
	}
	for i := range r.DeathTimings {
		dt := &r.DeathTimings[i]
		clean(&dt.LastX)
		clean(&dt.LastY)
		clean(&dt.LastZ)
		cleanF64(&dt.LastMovementTime)
	}
	for i := range r.GameActions {
		cleanF64(&r.GameActions[i].TimeInSeconds)
	}
}

// scanCameraPass5 scans for paired-quaternion camera frames (multi-POV custom replays).
// Pattern: [12+ null bytes] [counter 4B, value 1-255] [quat1 16B] [quat2 16B]
// Both quaternions nearly identical (delta < 0.02 per component).
// Entity ownership: forward scan to find next FC-UPDATE entity.
// Frames that overlap with Pass 4 (within 200 bytes) are skipped.
func (r *Reader) scanCameraPass5(entityToPlayer map[uint32]int) {
	if len(r.b) < 48 {
		return
	}

	// Build set of existing camera frame offsets (from Pass 4) for dedup
	pass4Offsets := make(map[int]bool, len(r.CameraFrames))
	for _, cf := range r.CameraFrames {
		pass4Offsets[cf.BinOffset] = true
	}

	archetypeMarker := [4]byte{0x60, 0x73, 0x85, 0xFE}
	pass5Count := 0

	for i := 12; i+36 <= len(r.b); i += 4 {
		// 12 null bytes at i-12..i-4 (3 uint32s)
		if binary.LittleEndian.Uint32(r.b[i-12:i-8]) != 0 ||
			binary.LittleEndian.Uint32(r.b[i-8:i-4]) != 0 {
			continue
		}
		// Counter at i-4 (1 <= counter <= 255)
		cnt := binary.LittleEndian.Uint32(r.b[i-4 : i])
		if cnt == 0 || cnt > 255 {
			continue
		}

		if i+32 > len(r.b) {
			continue
		}

		// Skip if near an existing Pass 4 frame
		nearPass4 := false
		for off := range pass4Offsets {
			d := i - off
			if d < 0 {
				d = -d
			}
			if d < 200 {
				nearPass4 = true
				break
			}
		}
		if nearPass4 {
			continue
		}

		qx1 := math.Float32frombits(binary.LittleEndian.Uint32(r.b[i : i+4]))
		qy1 := math.Float32frombits(binary.LittleEndian.Uint32(r.b[i+4 : i+8]))
		qz1 := math.Float32frombits(binary.LittleEndian.Uint32(r.b[i+8 : i+12]))
		qw1 := math.Float32frombits(binary.LittleEndian.Uint32(r.b[i+12 : i+16]))
		qx2 := math.Float32frombits(binary.LittleEndian.Uint32(r.b[i+16 : i+20]))
		qy2 := math.Float32frombits(binary.LittleEndian.Uint32(r.b[i+20 : i+24]))
		qz2 := math.Float32frombits(binary.LittleEndian.Uint32(r.b[i+24 : i+28]))
		qw2 := math.Float32frombits(binary.LittleEndian.Uint32(r.b[i+28 : i+32]))

		// Reject trivial quaternions (all near-zero or identity-like with no rotation)
		if math.Abs(float64(qx1)) < 0.001 && math.Abs(float64(qy1)) < 0.001 &&
			math.Abs(float64(qz1)) < 0.001 {
			continue
		}

		// Validate both unit quaternions
		mag1 := float64(qx1)*float64(qx1) + float64(qy1)*float64(qy1) +
			float64(qz1)*float64(qz1) + float64(qw1)*float64(qw1)
		mag2 := float64(qx2)*float64(qx2) + float64(qy2)*float64(qy2) +
			float64(qz2)*float64(qz2) + float64(qw2)*float64(qw2)
		if mag1 < 0.98 || mag1 > 1.02 || mag2 < 0.98 || mag2 > 1.02 {
			continue
		}

		// Quaternion similarity check (components within 0.02)
		if math.Abs(float64(qx1-qx2)) > 0.02 || math.Abs(float64(qy1-qy2)) > 0.02 ||
			math.Abs(float64(qz1-qz2)) > 0.02 || math.Abs(float64(qw1-qw2)) > 0.02 {
			continue
		}

		// Forward scan: find next FC-UPDATE entity to determine ownership
		ownerPlayer := -1
		for j := i + 32; j+18 <= len(r.b) && j < i+2000; j += 4 {
			if r.b[j+12] != archetypeMarker[0] || r.b[j+13] != archetypeMarker[1] ||
				r.b[j+14] != archetypeMarker[2] || r.b[j+15] != archetypeMarker[3] {
				continue
			}
			eid := binary.LittleEndian.Uint32(r.b[j : j+4])
			if eid>>24 >= 0xf0 {
				if pIdx, ok := entityToPlayer[eid]; ok {
					ownerPlayer = pIdx
				}
			}
			break
		}

		// YXZ Euler pitch from first quaternion
		sinP := 2.0 * (float64(qw1)*float64(qx1) - float64(qy1)*float64(qz1))
		if sinP > 1 {
			sinP = 1
		} else if sinP < -1 {
			sinP = -1
		}
		pitchDeg := float32(math.Asin(sinP) * 180 / math.Pi)
		yawDeg := float32(2.0 * math.Atan2(float64(qz1), float64(qw1)) * 180 / math.Pi)

		r.CameraFrames = append(r.CameraFrames, CameraFrame{
			PlayerIndex: ownerPlayer,
			Qx:          qx1,
			Qy:          qy1,
			Qz:          qz1,
			Qw:          qw1,
			YawDeg:      yawDeg,
			PitchDeg:    pitchDeg,
			BinOffset:   i,
		})
		pass5Count++
	}

	log.Debug().Int("pass5CameraFrames", pass5Count).Msg("camera_pass5")
}

// scanHealthUpdates finds health property updates (hash 0x4171D3C3) in the
// post-FC-UPDATE region of the binary. Each entry uses the format:
//
//	[ref8 (8 bytes)] [hash (4 bytes)] [value (4 bytes)]
//
// The health value is a float32 in the range 0-100, where 100 = full health
// and 0 = dead. Entity ownership is determined by looking for a player entity
// ref (xxxx06F0 00000000) either immediately after the property block or
// within a proximity window.
func (r *Reader) scanHealthUpdates(entityToPlayer map[uint32]int) {
	if len(r.b) < 16 {
		return
	}

	healthHash := uint32(0x4171D3C3)

	// Build reverse entity set for faster lookups
	entitySet := make(map[uint32]bool, len(entityToPlayer))
	for ref := range entityToPlayer {
		entitySet[ref] = true
	}

	// Scan region: start at ~80% of buffer (post-FC-UPDATE region)
	// The health data appears in the last ~15% of the decompressed buffer,
	// after the FC-UPDATE movement packets and before the event packets.
	scanStart := len(r.b) / 100 * 80
	if scanStart < 8 {
		scanStart = 8
	}

	for i := scanStart; i+8 <= len(r.b); i++ {
		if binary.LittleEndian.Uint32(r.b[i:i+4]) != healthHash {
			continue
		}

		// Validate: preceding 8 bytes should be a ref8 (non-zero pointer-like value)
		if i < 8 {
			continue
		}
		ref8 := binary.LittleEndian.Uint64(r.b[i-8 : i])
		if ref8 == 0 || ref8 < 0x0000010000000000 {
			continue // not a valid ref8
		}

		hp := math.Float32frombits(binary.LittleEndian.Uint32(r.b[i+4 : i+8]))
		if hp < 0 || hp > 100.01 {
			continue // not a valid health value
		}

		// Find entity owner:
		// Strategy 1: Look after the last property in this ref8's block
		//             for an entity ref within 4 bytes (the ownership marker)
		playerIdx := -1

		// Find end of this ref8's property block
		lastPos := i + 8
		for j := i + 8; j+16 <= len(r.b) && j < i+300; j += 16 {
			nextRef := binary.LittleEndian.Uint64(r.b[j : j+8])
			if nextRef == ref8 {
				lastPos = j + 16
			} else {
				break
			}
		}

		// Check for entity ref immediately after block (within 10 bytes)
		for j := lastPos; j+8 <= lastPos+10 && j+8 < len(r.b); j++ {
			candidate := binary.LittleEndian.Uint32(r.b[j : j+4])
			if entitySet[candidate] &&
				j+7 < len(r.b) &&
				r.b[j+4] == 0x00 && r.b[j+5] == 0x00 &&
				r.b[j+6] == 0x00 && r.b[j+7] == 0x00 {
				if idx, ok := entityToPlayer[candidate]; ok {
					playerIdx = idx
					break
				}
			}
		}

		// Strategy 2: Proximity search (±128 bytes) for closest entity ref
		if playerIdx == -1 {
			bestDist := 9999
			searchStart := i - 128
			if searchStart < 0 {
				searchStart = 0
			}
			searchEnd := i + 128
			if searchEnd > len(r.b)-8 {
				searchEnd = len(r.b) - 8
			}
			for j := searchStart; j+8 <= searchEnd; j++ {
				candidate := binary.LittleEndian.Uint32(r.b[j : j+4])
				if entitySet[candidate] &&
					r.b[j+4] == 0x00 && r.b[j+5] == 0x00 &&
					r.b[j+6] == 0x00 && r.b[j+7] == 0x00 {
					d := j - i
					if d < 0 {
						d = -d
					}
					if d < bestDist {
						bestDist = d
						if idx, ok := entityToPlayer[candidate]; ok {
							playerIdx = idx
						}
					}
				}
			}
		}

		// Only include entries that map to a known player
		if playerIdx >= 0 {
			r.HealthUpdates = append(r.HealthUpdates, HealthUpdate{
				PlayerIndex: playerIdx,
				Health:      hp,
				BinOffset:   i,
			})
		}
	}

	// Sort by binary offset for temporal ordering
	sort.Slice(r.HealthUpdates, func(a, b int) bool {
		return r.HealthUpdates[a].BinOffset < r.HealthUpdates[b].BinOffset
	})

	log.Debug().Int("health_updates", len(r.HealthUpdates)).Msg("health_scan")
}

// filterEntityNoise removes false positive entities:
//  1. Entities with fewer than 3 position frames
//  2. Entities that are stationary (bounding box < 2m in XY using tail 75% of frames)
//     unless they are cameras or gadgets (which are expected to be stationary)
func (r *Reader) filterEntityNoise() {
	filtered := r.TrackedEntities[:0]
	removed := 0
	for _, te := range r.TrackedEntities {
		// Always keep barricade entities (they typically have only 1 position frame)
		if te.IsBarricade {
			filtered = append(filtered, te)
			continue
		}

		// Keep entities with at least 3 positions
		if len(te.Positions) < 3 {
			removed++
			continue
		}

		// Stationary check: only apply to drones and projectiles (cameras/gadgets are expected to be still)
		if te.Type == EntityDrone || te.Type == EntityProjectile {
			skip := len(te.Positions) / 4
			if skip < 1 {
				skip = 1
			}
			tail := te.Positions[skip:]
			if len(tail) > 0 {
				minX, maxX := tail[0].X, tail[0].X
				minY, maxY := tail[0].Y, tail[0].Y
				for _, p := range tail {
					if p.X < minX {
						minX = p.X
					}
					if p.X > maxX {
						maxX = p.X
					}
					if p.Y < minY {
						minY = p.Y
					}
					if p.Y > maxY {
						maxY = p.Y
					}
				}
				if float64(maxX-minX) < 2.0 && float64(maxY-minY) < 2.0 {
					// Stationary entity misclassified as drone/projectile — demote to gadget
					te.Type = EntityGadget
					if te.GadgetName == "Drone" || te.GadgetName == "Projectile" {
						te.GadgetName = "Gadget"
					}
				}
			}
		}

		filtered = append(filtered, te)
	}
	r.TrackedEntities = filtered
	if removed > 0 {
		log.Debug().Int("removed", removed).Msg("entity_noise_filtered")
	}
}

// validateEntityMapBySpawn checks that each player's first position is consistent
// with their team. Defenders typically spawn inside the building (distinct cluster),
// attackers spawn outside. If a player's first position is closer to the other team's
// centroid, find the best swap partner from the opposing team and fix both.
func (r *Reader) validateEntityMapBySpawn(entityToPlayer map[uint32]int) {
	if len(r.Header.Players) < 4 {
		return
	}

	// Collect first position per player index
	type vec3 struct{ x, y, z float64 }
	firstPos := make(map[int]vec3)
	for _, pu := range r.PositionUpdates {
		if pu.PlayerIndex < 0 || pu.PlayerIndex >= len(r.Header.Players) {
			continue
		}
		if _, exists := firstPos[pu.PlayerIndex]; !exists {
			firstPos[pu.PlayerIndex] = vec3{float64(pu.X), float64(pu.Y), float64(pu.Z)}
		}
	}

	// Split by team
	var team0Idxs, team1Idxs []int
	for i, p := range r.Header.Players {
		if _, ok := firstPos[i]; !ok {
			continue
		}
		if p.TeamIndex == 0 {
			team0Idxs = append(team0Idxs, i)
		} else {
			team1Idxs = append(team1Idxs, i)
		}
	}
	if len(team0Idxs) < 2 || len(team1Idxs) < 2 {
		return
	}

	// Compute centroid per team
	centroid := func(idxs []int) vec3 {
		var sx, sy, sz float64
		for _, i := range idxs {
			p := firstPos[i]
			sx += p.x
			sy += p.y
			sz += p.z
		}
		n := float64(len(idxs))
		return vec3{sx / n, sy / n, sz / n}
	}
	dist := func(a, b vec3) float64 {
		dx, dy, dz := a.x-b.x, a.y-b.y, a.z-b.z
		return dx*dx + dy*dy + dz*dz // squared distance is fine
	}

	c0 := centroid(team0Idxs)
	c1 := centroid(team1Idxs)

	// If centroids are very close, teams aren't spatially separated — skip
	if dist(c0, c1) < 100 { // 10m squared minimum separation
		return
	}

	// Find misplaced players: a player whose first pos is closer to the OTHER team's centroid
	type misplaced struct {
		playerIdx int
		team      int // 0 or 1
	}
	var misTeam0, misTeam1 []misplaced
	for _, i := range team0Idxs {
		p := firstPos[i]
		if dist(p, c1) < dist(p, c0) {
			misTeam0 = append(misTeam0, misplaced{i, 0})
		}
	}
	for _, i := range team1Idxs {
		p := firstPos[i]
		if dist(p, c0) < dist(p, c1) {
			misTeam1 = append(misTeam1, misplaced{i, 1})
		}
	}

	// Pair up misplaced players across teams and swap their entity refs
	n := len(misTeam0)
	if len(misTeam1) < n {
		n = len(misTeam1)
	}
	for i := 0; i < n; i++ {
		p0 := misTeam0[i].playerIdx
		p1 := misTeam1[i].playerIdx

		// Find entity refs for each
		var ref0, ref1 uint32
		var found0, found1 bool
		for ref, pIdx := range entityToPlayer {
			if pIdx == p0 {
				ref0 = ref
				found0 = true
			}
			if pIdx == p1 {
				ref1 = ref
				found1 = true
			}
		}
		if !found0 || !found1 {
			continue
		}

		// Swap in entityToPlayer
		entityToPlayer[ref0] = p1
		entityToPlayer[ref1] = p0

		// Swap in PositionUpdates
		for j := range r.PositionUpdates {
			if r.PositionUpdates[j].EntityRef == ref0 {
				r.PositionUpdates[j].PlayerIndex = p1
			} else if r.PositionUpdates[j].EntityRef == ref1 {
				r.PositionUpdates[j].PlayerIndex = p0
			}
		}

		log.Info().
			Str("player_a", r.Header.Players[p0].Username).
			Int("idx_a", p0).
			Str("player_b", r.Header.Players[p1].Username).
			Int("idx_b", p1).
			Msg("entity_map_swap_corrected")
	}
}

// extractGadgetLoadout scans the decompressed binary for inventory state packets
// that encode primary and secondary gadget counts per player.
//
// Binary format: property bags with structure [0x22][hash 4B][type 1B][value ...]
// Two hashes encode gadget counts:
//   - 0x4FBDD114 (LE: 14 d1 bd 4f) = primary gadget/ability count
//   - 0x44186B66 (LE: 66 6b 18 44) = secondary gadget count
func (r *Reader) extractGadgetLoadout(entityToPlayer map[uint32]int) {
	if len(r.b) < 100 {
		return
	}

	// Only scan the late portion of the binary (last 40%)
	startOff := len(r.b) / 100 * 60

	u32 := func(off int) uint32 {
		if off < 0 || off+4 > len(r.b) {
			return 0
		}
		return binary.LittleEndian.Uint32(r.b[off : off+4])
	}

	// Primary gadget hash: 14 d1 bd 4f 04
	primaryHash := [5]byte{0x14, 0xd1, 0xbd, 0x4f, 0x04}
	// Secondary gadget hash: 66 6b 18 44 04
	secondaryHash := [5]byte{0x66, 0x6b, 0x18, 0x44, 0x04}

	// Extract primary gadget counts per entity
	primaryByEntity := make(map[uint32]int)
	for i := startOff; i+9 < len(r.b); i++ {
		if r.b[i] != primaryHash[0] || r.b[i+1] != primaryHash[1] ||
			r.b[i+2] != primaryHash[2] || r.b[i+3] != primaryHash[3] ||
			r.b[i+4] != primaryHash[4] {
			continue
		}
		count := int(u32(i + 5))
		if count == 0 {
			continue
		}

		// Look back for entity reference (0x23 [eid 4B] where eid>>24 >= 0xF0)
		var eid uint32
		for back := 5; back <= 30 && i-back >= 0; back++ {
			if r.b[i-back] == 0x23 {
				candidate := u32(i - back + 1)
				if candidate>>24 >= 0xF0 {
					eid = candidate
					break
				}
			}
		}
		if eid == 0 {
			eid = 0xFFFFFFFF
		}
		if count > primaryByEntity[eid] {
			primaryByEntity[eid] = count
		}
	}

	// Extract secondary gadget counts
	secondaryByEntity := make(map[uint32]int)
	for i := startOff; i+9 < len(r.b); i++ {
		if r.b[i] != secondaryHash[0] || r.b[i+1] != secondaryHash[1] ||
			r.b[i+2] != secondaryHash[2] || r.b[i+3] != secondaryHash[3] ||
			r.b[i+4] != secondaryHash[4] {
			continue
		}
		count := int(u32(i + 5))
		if count == 0 {
			continue
		}

		var eid uint32
		for back := 5; back <= 40 && i-back >= 0; back++ {
			if r.b[i-back] == 0x23 {
				candidate := u32(i - back + 1)
				if candidate>>24 >= 0xF0 {
					eid = candidate
					break
				}
			}
		}
		if eid == 0 {
			eid = 0xFFFFFFFF
		}
		if _, exists := secondaryByEntity[eid]; !exists {
			secondaryByEntity[eid] = count
		}
	}

	// Merge into GadgetLoadout per entity, then map to player indices
	allEIDs := make(map[uint32]bool)
	for eid := range primaryByEntity {
		allEIDs[eid] = true
	}
	for eid := range secondaryByEntity {
		allEIDs[eid] = true
	}

	for eid := range allEIDs {
		gl := GadgetLoadout{}
		if pc, ok := primaryByEntity[eid]; ok {
			gl.PrimaryCount = pc
			gl.HasPrimary = true
		}
		if sc, ok := secondaryByEntity[eid]; ok {
			gl.SecondaryCount = sc
			gl.HasSecondary = true
		}

		// Map entity to player
		playerIdx := -1
		if eid == 0xFFFFFFFF {
			// Sentinel: no entity ref found, assign to player 0 for single-player
			if len(r.Header.Players) == 1 {
				playerIdx = 0
			}
		} else if idx, ok := entityToPlayer[eid]; ok {
			playerIdx = idx
		}

		if playerIdx >= 0 && playerIdx < len(r.Loadouts) {
			r.Loadouts[playerIdx].GadgetLoadout = &gl
		}
	}

	total := 0
	for _, l := range r.Loadouts {
		if l.GadgetLoadout != nil {
			total++
		}
	}
	log.Debug().Int("playersWithGadgetLoadout", total).Msg("gadget_loadout_extracted")
}

// scanEntityHealthEvents scans the binary for health property updates associated
// with tracked (non-player) entities such as drones and gadgets.
// Uses the same health hash 0x4171D3C3 that appears in the post-80% region.
//
// Note: Barricades do NOT use this health hash — they use hit-count mechanics
// instead of HP, so barricade health events will always be empty.
func (r *Reader) scanEntityHealthEvents() {
	if len(r.b) < 16 || len(r.TrackedEntities) == 0 {
		return
	}

	healthHash := uint32(0x4171D3C3)

	// Build entity ref set from TrackedEntities
	entityToIdx := make(map[uint32]int)
	for ti := range r.TrackedEntities {
		entityToIdx[r.TrackedEntities[ti].EntityRef] = ti
	}

	// Health property data appears in the post-80% region of the decompressed buffer.
	scanStart := len(r.b) / 100 * 80
	if scanStart < 8 {
		scanStart = 8
	}

	for i := scanStart; i+8 <= len(r.b); i++ {
		if binary.LittleEndian.Uint32(r.b[i:i+4]) != healthHash {
			continue
		}
		if i < 8 {
			continue
		}

		hp := math.Float32frombits(binary.LittleEndian.Uint32(r.b[i+4 : i+8]))
		if hp < 0 || hp > 200 {
			continue
		}

		// Search ±64 bytes for a tracked entity ref
		searchStart := i - 64
		if searchStart < 0 {
			searchStart = 0
		}
		searchEnd := i + 64
		if searchEnd > len(r.b)-8 {
			searchEnd = len(r.b) - 8
		}

		bestDist := 9999
		bestIdx := -1
		for j := searchStart; j+8 <= searchEnd; j++ {
			candidate := binary.LittleEndian.Uint32(r.b[j : j+4])
			if ti, ok := entityToIdx[candidate]; ok {
				if j+7 < len(r.b) &&
					r.b[j+4] == 0x00 && r.b[j+5] == 0x00 &&
					r.b[j+6] == 0x00 && r.b[j+7] == 0x00 {
					d := j - i
					if d < 0 {
						d = -d
					}
					if d < bestDist {
						bestDist = d
						bestIdx = ti
					}
				}
			}
		}

		if bestIdx >= 0 {
			te := &r.TrackedEntities[bestIdx]
			te.HealthEvents = append(te.HealthEvents, HealthEvent{
				Offset:     int64(i),
				HP:         int(hp),
				HPFraction: hp / 100.0,
			})
		}
	}

	withHealth := 0
	for _, te := range r.TrackedEntities {
		if len(te.HealthEvents) > 0 {
			withHealth++
		}
	}
	if withHealth > 0 {
		log.Debug().Int("entitiesWithHealth", withHealth).Msg("entity_health_events_scanned")
	}
}
