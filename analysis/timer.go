package analysis

import (
	"encoding/binary"
	"math"
)

// ExtractTimerTicks scans for round timer ticks.
// Pattern: 1F 07 EF C9 04 [seconds u32 LE]
func ExtractTimerTicks(data []byte) []TimerTick {
	pat := []byte{0x1F, 0x07, 0xEF, 0xC9}
	var ticks []TimerTick

	for i := 0; i+9 < len(data); i++ {
		if data[i] != pat[0] || data[i+1] != pat[1] || data[i+2] != pat[2] || data[i+3] != pat[3] {
			continue
		}
		if data[i+4] != 0x04 {
			continue
		}
		seconds := binary.LittleEndian.Uint32(data[i+5 : i+9])
		if seconds > 600 { // sanity: no round lasts > 10 minutes
			continue
		}
		ticks = append(ticks, TimerTick{
			Offset:  int64(i),
			Seconds: float32(seconds),
		})
	}

	return ticks
}

// BuildTimerPhases groups timer ticks into prep and action phases.
// A gap > 5 seconds between consecutive ticks marks a phase boundary.
func BuildTimerPhases(ticks []TimerTick) []TimerPhase {
	// Filter sentinels (seconds=0 at low offsets)
	var real []TimerTick
	for _, t := range ticks {
		if t.Offset > 1000 && t.Seconds > 0 {
			real = append(real, t)
		}
	}
	if len(real) < 2 {
		return nil
	}

	var phases []TimerPhase
	phaseStart := real[0]
	prevTick := real[0]

	for i := 1; i < len(real); i++ {
		gap := math.Abs(float64(real[i].Seconds - prevTick.Seconds))
		if gap > 5 {
			// Phase boundary
			dur := math.Abs(float64(phaseStart.Seconds - prevTick.Seconds))
			if dur >= 2 { // skip phases < 2s
				name := "prep"
				if len(phases) > 0 {
					name = "action"
				}
				phases = append(phases, TimerPhase{
					Name:     name,
					StartSec: phaseStart.Seconds,
					EndSec:   prevTick.Seconds,
					Duration: float32(dur),
				})
			}
			phaseStart = real[i]
		}
		prevTick = real[i]
	}

	// Final phase
	dur := math.Abs(float64(phaseStart.Seconds - prevTick.Seconds))
	if dur >= 2 {
		name := "action"
		if len(phases) == 0 {
			name = "prep"
		}
		phases = append(phases, TimerPhase{
			Name:     name,
			StartSec: phaseStart.Seconds,
			EndSec:   prevTick.Seconds,
			Duration: float32(dur),
		})
	}

	return phases
}

// RoundDurationFromTicks computes total round duration from timer phases.
func RoundDurationFromTicks(ticks []TimerTick) float32 {
	phases := BuildTimerPhases(ticks)
	total := float32(0)
	for _, p := range phases {
		total += p.Duration
	}
	return total
}

// AssignFrameTimes assigns elapsed time to position frames.
// When timer ticks are available and frame offsets are real binary offsets
// (not sequential indices), it uses piecewise tick interpolation for accuracy.
// Otherwise it falls back to min-max linear interpolation.
func AssignFrameTimes(tracks []*internalTrack, ticks []TimerTick, totalDuration float32) {
	if totalDuration <= 0 || len(tracks) == 0 {
		return
	}

	// Find min/max offsets across all non-camera frames.
	// Camera frames appended by ExtractCameraFrames use raw binary scan offsets
	// that can extend into the 62 MB timer region and would contaminate the range.
	minOff := int64(math.MaxInt64)
	maxOff := int64(0)
	totalFrames := int64(0)
	for _, tr := range tracks {
		for _, f := range tr.Frames {
			if f.IsCamera {
				continue
			}
			totalFrames++
			if f.Offset < minOff {
				minOff = f.Offset
			}
			if f.Offset > maxOff {
				maxOff = f.Offset
			}
		}
	}

	if maxOff <= minOff {
		return
	}

	// If we have timer ticks and offsets look like real binary offsets (not
	// sequential 0..N indices), use the tick-anchored elapsed map for accuracy.
	// Threshold: max offset > 10× frame count means they are binary positions.
	// Additionally require that at least one real tick falls within the frame offset
	// range — in Y11S1+, the position stream (0–55 MB) and timer stream (61.9 MB+)
	// are in separate byte regions, so tick-based mapping returns 0 for all frames.
	inTimerRange := false
	for _, t := range ticks {
		if t.Offset > 1000 && t.Seconds > 0 && t.Offset <= maxOff {
			inTimerRange = true
			break
		}
	}
	useTicks := len(ticks) >= 2 && maxOff > totalFrames*10 && inTimerRange

	if useTicks {
		elapsed := buildTickElapsedMap(ticks, totalDuration)
		for _, tr := range tracks {
			for i := range tr.Frames {
				tr.Frames[i].TimeSecs = float32(elapsed(tr.Frames[i].Offset))
			}
		}
		return
	}

	// Fallback: simple linear interpolation by offset fraction
	for _, tr := range tracks {
		for i := range tr.Frames {
			frac := float64(tr.Frames[i].Offset-minOff) / float64(maxOff-minOff)
			tr.Frames[i].TimeSecs = float32(frac * float64(totalDuration))
		}
	}
}

// BuildTickElapsedMap returns a closure that converts a binary offset to elapsed
// seconds using piecewise linear interpolation anchored to timer ticks.
// Ticks count DOWN (seconds remaining); elapsed is derived from the countdown delta.
func BuildTickElapsedMap(ticks []TimerTick, totalDuration float32) func(int64) float64 {
	return buildTickElapsedMap(ticks, totalDuration)
}

func buildTickElapsedMap(ticks []TimerTick, totalDuration float32) func(int64) float64 {
	// Filter sentinel ticks
	var real []TimerTick
	for _, t := range ticks {
		if t.Offset > 1000 && t.Seconds > 0 {
			real = append(real, t)
		}
	}
	if len(real) < 2 {
		// No ticks: return linear interpolation based on first/last tick offsets
		return func(off int64) float64 { return 0 }
	}

	// Build cumulative elapsed time at each tick offset.
	// Phase boundary: when seconds INCREASE by > 5 (new phase reset to higher value).
	type anchorPoint struct {
		offset  int64
		elapsed float64
	}
	anchors := make([]anchorPoint, len(real))
	anchors[0] = anchorPoint{offset: real[0].Offset, elapsed: 0}
	cumulative := float64(0)
	for i := 1; i < len(real); i++ {
		delta := float64(real[i-1].Seconds - real[i].Seconds)
		if delta < 0 {
			delta = -delta
		}
		if real[i].Seconds > real[i-1].Seconds+5 {
			// Phase boundary: timer reset to higher value, no elapsed time added for the gap
			delta = 0
		}
		cumulative += delta
		anchors[i] = anchorPoint{offset: real[i].Offset, elapsed: cumulative}
	}

	// Cap at totalDuration
	if totalDuration > 0 && cumulative > float64(totalDuration) {
		scale := float64(totalDuration) / cumulative
		for i := range anchors {
			anchors[i].elapsed *= scale
		}
	}

	return func(off int64) float64 {
		if off <= anchors[0].offset {
			return 0
		}
		if off >= anchors[len(anchors)-1].offset {
			return anchors[len(anchors)-1].elapsed
		}
		// Binary search
		lo, hi := 0, len(anchors)-1
		for lo < hi {
			mid := (lo + hi) / 2
			if anchors[mid].offset < off {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		a0, a1 := anchors[lo-1], anchors[lo]
		frac := float64(off-a0.offset) / float64(a1.offset-a0.offset)
		return a0.elapsed + frac*(a1.elapsed-a0.elapsed)
	}
}

// AssignHealthTimes assigns elapsed time to health updates using tick-based interpolation.
// Falls back to min-max linear over the health update offsets when tick-based gives 0
// for all events (e.g. in Y11S1+ where health updates occupy a byte region between the
// position stream and the timer-tick stream).
func AssignHealthTimes(updates []HealthUpdate, ticks []TimerTick, totalDuration float32) {
	if len(updates) == 0 || totalDuration <= 0 {
		return
	}
	elapsed := buildTickElapsedMap(ticks, totalDuration)
	anyNonZero := false
	for i := range updates {
		t := float32(elapsed(int64(updates[i].BinOffset)))
		updates[i].TimeSecs = t
		if t > 0 {
			anyNonZero = true
		}
	}
	if anyNonZero {
		return
	}
	// Fallback: linear interpolation over the offset range of the updates themselves.
	minOff, maxOff := int64(math.MaxInt64), int64(0)
	for _, u := range updates {
		o := int64(u.BinOffset)
		if o < minOff {
			minOff = o
		}
		if o > maxOff {
			maxOff = o
		}
	}
	if maxOff <= minOff {
		return
	}
	for i := range updates {
		frac := float64(int64(updates[i].BinOffset)-minOff) / float64(maxOff-minOff)
		updates[i].TimeSecs = float32(frac * float64(totalDuration))
	}
}

// AssignAmmoTimes assigns elapsed time to ammo events using offset interpolation.
func AssignAmmoTimes(events []AmmoEvent, ticks []TimerTick, totalDuration float32) {
	if len(events) == 0 || totalDuration <= 0 {
		return
	}

	minOff := int64(math.MaxInt64)
	maxOff := int64(0)
	for _, ev := range events {
		if ev.Offset < minOff {
			minOff = ev.Offset
		}
		if ev.Offset > maxOff {
			maxOff = ev.Offset
		}
	}

	if maxOff <= minOff {
		return
	}

	for i := range events {
		frac := float64(events[i].Offset-minOff) / float64(maxOff-minOff)
		events[i].TimeSecs = float32(frac * float64(totalDuration))
	}
}
