package analysis

import (
	"encoding/binary"
	"math"
)

const healthHash = 0x4171D3C3 // health property hash

// ExtractHealthUpdates scans the full binary for health property updates
// (hash 0x4171D3C3) and maps them to players via entity ref attribution.
func ExtractHealthUpdates(data []byte, entityToPlayer map[uint32]int, ticks []TimerTick) []HealthUpdate {
	if len(data) < 100 {
		return nil
	}

	var updates []HealthUpdate
	hashBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(hashBytes, healthHash)

	for i := 0; i+8 <= len(data); i++ {
		if data[i] != hashBytes[0] || data[i+1] != hashBytes[1] ||
			data[i+2] != hashBytes[2] || data[i+3] != hashBytes[3] {
			continue
		}

		// Health value (float32) immediately follows the hash
		hpBits := binary.LittleEndian.Uint32(data[i+4 : i+8])
		hp := math.Float32frombits(hpBits)

		// Validate: health should be 0-130 (some operators have extra HP buffs)
		if hp < 0 || hp > 130 || math.IsNaN(float64(hp)) || math.IsInf(float64(hp), 0) {
			continue
		}

		// Find owning entity: scan BACKWARD up to 256 bytes for F0-prefix entity ref.
		// The entity ref always precedes the property hash in TLV packet layout.
		entityRef := findPrecedingEntity(data, i, 256)
		if entityRef == 0 {
			// Also try a short forward scan for edge cases
			entityRef = findNearbyEntity(data, i, 64)
		}
		if entityRef == 0 {
			continue
		}

		pIdx, ok := entityToPlayer[entityRef]
		if !ok {
			pIdx = -1
		}

		updates = append(updates, HealthUpdate{
			PlayerIndex: pIdx,
			Health:      hp,
			EntityRef:   entityRef,
			BinOffset:   i,
		})
	}

	return updates
}

// findPrecedingEntity scans BACKWARD up to radius bytes for the nearest
// F0-prefix entity ref (which comes before property hashes in TLV packets).
func findPrecedingEntity(data []byte, offset, radius int) uint32 {
	start := offset - radius
	if start < 4 {
		start = 4
	}
	for j := offset - 4; j >= start; j-- {
		ref := binary.LittleEndian.Uint32(data[j : j+4])
		if ref>>24 >= 0xF0 {
			return ref
		}
	}
	return 0
}

// findNearbyEntity scans ±radius bytes from offset for an F0-prefix entity ref.
func findNearbyEntity(data []byte, offset, radius int) uint32 {
	start := offset - radius
	if start < 0 {
		start = 0
	}
	end := offset + radius
	if end+4 > len(data) {
		end = len(data) - 4
	}

	bestRef := uint32(0)
	bestDist := radius + 1

	for j := start; j <= end; j++ {
		ref := binary.LittleEndian.Uint32(data[j : j+4])
		if ref>>24 >= 0xF0 {
			d := j - offset
			if d < 0 {
				d = -d
			}
			if d < bestDist {
				bestDist = d
				bestRef = ref
			}
		}
	}

	return bestRef
}
