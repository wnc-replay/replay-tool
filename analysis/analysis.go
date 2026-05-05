package analysis

import (
	"encoding/binary"
	"math"
)

// AnalyzeRound runs the full analysis pipeline on a decompressed .rec buffer.
// players provides header-parsed player info (username, operator, team).
// Returns the complete round analysis.
func AnalyzeRound(data []byte, players []PlayerInfo) *RoundAnalysis {
	// Use empty mapping - will fall back to position-based inference
	return AnalyzeRoundWithMapping(data, players, nil)
}

// AnalyzeRoundWithMapping runs the full analysis pipeline with a pre-built
// entity-to-player mapping from the dissect library.
func AnalyzeRoundWithMapping(data []byte, players []PlayerInfo, entityToPlayer map[uint32]int) *RoundAnalysis {
	if len(data) < 1000 {
		return &RoundAnalysis{RecordingPlayer: -1}
	}

	result := &RoundAnalysis{RecordingPlayer: -1}

	// Step 1: Extract timer ticks (needed for time assignment)
	result.TimerTicks = ExtractTimerTicks(data)
	result.TimerPhases = BuildTimerPhases(result.TimerTicks)
	result.RoundDuration = RoundDurationFromTicks(result.TimerTicks)

	// Step 2: Extract all entity positions (SPAWN + FC-UPDATE)
	allTracks := ExtractEntityPositions(data)

	// Step 3: Extract bone data (head + chest aim quaternions)
	ExtractBoneData(data, allTracks)
	ComputeHeadWorldAim(allTracks)

	// Step 4: Map entities to players
	if entityToPlayer == nil || len(entityToPlayer) == 0 {
		// Try binary SPAWN counter=494 pattern method
		entityToPlayer = MapEntitiesToPlayers(data, len(players))
		if len(entityToPlayer) < len(players)/2 {
			// Fallback: use init-block appearance order (61 73 85 FE, counter=494)
			entityToPlayer = MapPlayersFromInitBlocks(data, len(players))
		}
		if len(entityToPlayer) < len(players)/2 {
			// Final fallback: infer from position track proximity
			entityToPlayer = MapEntitiesToPlayersFromTracks(allTracks, players)
		}
	}

	// Step 5: Camera frames (Pass 4 — per-entity detection)
	ExtractCameraFrames(data, allTracks)

	// Step 6: Assign timestamps to frames (after camera frames are appended)
	AssignFrameTimes(allTracks, result.TimerTicks, result.RoundDuration)

	// Step 7: Infer stance from Z-height
	InferStance(allTracks)

	// Step 8: Classify entities (gadgets, drones, barricades, weapons, projectiles)
	spawnCounters := ExtractSpawnCounters(data)
	ClassifyEntities(allTracks, spawnCounters, data, players)

	// Step 9: Split into player tracks and entity tracks
	result.Players, result.Entities = SplitTracks(allTracks, entityToPlayer, players)

	// Step 10: Detect recording player
	result.RecordingPlayer = DetectRecordingPlayer(allTracks, entityToPlayer)

	// Step 11: Ammo events + weapon tracking
	ammoEvents := ExtractAmmoEvents(data)
	AssignAmmoTimes(ammoEvents, result.TimerTicks, result.RoundDuration)
	result.Weapons = BuildWeaponTracking(data, ammoEvents, players, entityToPlayer)
	result.EquipmentSwitches = ExtractEquipmentSwitches(ammoEvents, result.Weapons, result.TimerTicks, result.RoundDuration)

	// Step 12: Equipment loadouts (weapon/gadget names)
	result.Loadouts = ExtractLoadouts(data, players)

	// Step 13: Shot event reconstruction
	result.Shots = ReconstructShots(data, ammoEvents, result.Players, players)

	// Step 14: Health updates (filter unattributed entries where playerIndex == -1)
	allHealth := ExtractHealthUpdates(data, entityToPlayer, result.TimerTicks)
	AssignHealthTimes(allHealth, result.TimerTicks, result.RoundDuration)
	for _, h := range allHealth {
		if h.PlayerIndex >= 0 {
			result.HealthUpdates = append(result.HealthUpdates, h)
		}
	}
	result.DestructionEvents = ExtractDestructionEventsFromHealth(allHealth, result.Entities)
	result.ReviveEvents = ExtractReviveEvents(result.HealthUpdates)

	// Step 15: Binary match feedback (kills/deaths/DBNOs)
	result.BinaryFeedback = ExtractBinaryFeedback(data, result.TimerTicks, result.RoundDuration)

	// Step 16: Game actions (reinforce, gadget deploy)
	result.GameActions = ExtractGameActions(data, result.TimerTicks)

	// Step 17: Operator swap events (binary fallback for pre-Y10S4 replays)
	result.OperatorSwaps = ExtractOperatorSwaps(data, players, result.TimerTicks)

	// Step 18: Unified game event timeline for visualization
	result.GameEvents = BuildGameEvents(result.BinaryFeedback, result.TimerPhases, result.RoundDuration)

	return result
}

// AnalyzeRoundWithLibraryPositions runs the analysis using position data from the
// dissect library instead of binary extraction. This is the preferred method for
// newer replay formats where binary patterns have changed.
func AnalyzeRoundWithLibraryPositions(data []byte, players []PlayerInfo, positions []LibraryPosition, entityToPlayer map[uint32]int) *RoundAnalysis {
	if len(data) < 1000 {
		return &RoundAnalysis{RecordingPlayer: -1}
	}

	result := &RoundAnalysis{RecordingPlayer: -1}

	// Step 1: Extract timer ticks (needed for time assignment)
	result.TimerTicks = ExtractTimerTicks(data)
	result.TimerPhases = BuildTimerPhases(result.TimerTicks)
	result.RoundDuration = RoundDurationFromTicks(result.TimerTicks)

	// Step 2: Build tracks from library positions (NOT binary extraction)
	allTracks := BuildTracksFromLibraryPositions(positions)

	// Step 3: Extract bone data (head + chest aim quaternions) - still from binary
	ExtractBoneData(data, allTracks)
	ComputeHeadWorldAim(allTracks)

	// Step 4: Entity-to-player mapping is already provided

	// Step 5: Camera frames (Pass 4 — per-entity detection)
	ExtractCameraFrames(data, allTracks)

	// Step 6: Assign timestamps to frames (after camera frames are appended)
	AssignFrameTimes(allTracks, result.TimerTicks, result.RoundDuration)

	// Step 7: Infer stance from Z-height
	InferStance(allTracks)

	// Step 8: Classify entities (gadgets, drones, barricades, weapons, projectiles)
	spawnCounters := ExtractSpawnCounters(data)
	ClassifyEntities(allTracks, spawnCounters, data, players)

	// Step 9: Split into player tracks and entity tracks
	result.Players, result.Entities = SplitTracks(allTracks, entityToPlayer, players)

	// Step 10: Detect recording player
	result.RecordingPlayer = DetectRecordingPlayer(allTracks, entityToPlayer)

	// Step 11: Ammo events + weapon tracking
	ammoEvents := ExtractAmmoEvents(data)
	AssignAmmoTimes(ammoEvents, result.TimerTicks, result.RoundDuration)
	result.Weapons = BuildWeaponTracking(data, ammoEvents, players, entityToPlayer)
	result.EquipmentSwitches = ExtractEquipmentSwitches(ammoEvents, result.Weapons, result.TimerTicks, result.RoundDuration)

	// Step 12: Equipment loadouts (weapon/gadget names)
	result.Loadouts = ExtractLoadouts(data, players)

	// Step 13: Shot event reconstruction
	result.Shots = ReconstructShots(data, ammoEvents, result.Players, players)

	// Step 14: Health updates (filter unattributed entries where playerIndex == -1)
	allHealth := ExtractHealthUpdates(data, entityToPlayer, result.TimerTicks)
	AssignHealthTimes(allHealth, result.TimerTicks, result.RoundDuration)
	for _, h := range allHealth {
		if h.PlayerIndex >= 0 {
			result.HealthUpdates = append(result.HealthUpdates, h)
		}
	}
	result.DestructionEvents = ExtractDestructionEventsFromHealth(allHealth, result.Entities)
	result.ReviveEvents = ExtractReviveEvents(result.HealthUpdates)

	// Step 15: Binary match feedback (kills/deaths/DBNOs)
	result.BinaryFeedback = ExtractBinaryFeedback(data, result.TimerTicks, result.RoundDuration)

	// Step 16: Game actions (reinforce, gadget deploy)
	result.GameActions = ExtractGameActions(data, result.TimerTicks)

	// Step 17: Operator swap events (binary fallback for pre-Y10S4 replays)
	result.OperatorSwaps = ExtractOperatorSwaps(data, players, result.TimerTicks)

	// Step 18: Unified game event timeline for visualization
	result.GameEvents = BuildGameEvents(result.BinaryFeedback, result.TimerPhases, result.RoundDuration)

	return result
}

// PlayerInfo is the minimal player data needed from the header.
type PlayerInfo struct {
	Username  string
	Operator  string
	TeamIndex int
	IsAttack  bool
	ID        uint64
}

// ---------- Helpers ----------

func u32(data []byte, off int) uint32 {
	if off < 0 || off+4 > len(data) {
		return 0
	}
	return binary.LittleEndian.Uint32(data[off : off+4])
}

func f32(data []byte, off int) float32 {
	return math.Float32frombits(u32(data, off))
}

func isUnitQuat(qx, qy, qz, qw float32) bool {
	mag := float64(qx)*float64(qx) + float64(qy)*float64(qy) +
		float64(qz)*float64(qz) + float64(qw)*float64(qw)
	return mag > 0.8 && mag < 1.2
}

func calcYawSimple(qz, qw float32) float32 {
	return float32(math.Atan2(2*float64(qw)*float64(qz),
		1-2*float64(qz)*float64(qz)) * 180 / math.Pi)
}

// calcYawFull computes yaw from a full quaternion.
func calcYawFull(qx, qy, qz, qw float32) float32 {
	return float32(math.Atan2(
		2*float64(qw)*float64(qz),
		1-2*(float64(qz)*float64(qz)+float64(qy)*float64(qy)),
	) * 180 / math.Pi)
}

func calcPitch(qx, qy, qz, qw float32) float32 {
	sinP := 2 * (float64(qw)*float64(qx) - float64(qy)*float64(qz))
	if sinP > 1 {
		sinP = 1
	} else if sinP < -1 {
		sinP = -1
	}
	return float32(math.Asin(sinP) * 180 / math.Pi)
}

func unwrapYaw(prev, next float32) float32 {
	for next-prev > 180 {
		next -= 360
	}
	for next-prev < -180 {
		next += 360
	}
	return next
}

func isPrintableASCII(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7F {
			return false
		}
	}
	return len(s) > 0
}

// offsetToElapsed converts a binary offset to elapsed seconds using linear interpolation.
func offsetToElapsed(off, minOff, maxOff int64, totalDuration float32) float32 {
	if maxOff <= minOff {
		return 0
	}
	frac := float64(off-minOff) / float64(maxOff-minOff)
	return float32(frac * float64(totalDuration))
}
