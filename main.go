// r6-replay-tool — comprehensive Rainbow Six Siege .rec replay analyzer.
//
// Combines position tracking, bone/aim data, ammo/weapon analysis, equipment
// loadouts, shot reconstruction, health monitoring, entity classification,
// camera frames, timer ticks, and game event extraction into a single tool.
//
// Usage:
//
//	r6-replay-tool <file.rec>              # analyze and print JSON to stdout
//	r6-replay-tool -o output.json file.rec # write to file
//	r6-replay-tool -pretty file.rec        # pretty-printed JSON
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/wnc-replay/replay-tool/analysis"
	"github.com/wnc-replay/replay-tool/dissect"
)

func main() {
	outFile := flag.String("o", "", "Output JSON file (default: stdout)")
	pretty := flag.Bool("pretty", false, "Pretty-print JSON output")
	headerOnly := flag.Bool("header", false, "Only parse header info (fast)")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: r6-replay-tool [flags] <file.rec>")
		fmt.Fprintln(os.Stderr, "\nFlags:")
		flag.PrintDefaults()
		os.Exit(1)
	}

	recPath := flag.Arg(0)

	// Open .rec file
	f, err := os.Open(recPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening file: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	// Create dissect reader (decompresses the replay)
	reader, err := dissect.NewReader(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading replay: %v\n", err)
		os.Exit(1)
	}

	// Capture decompressed bytes BEFORE Read() clears the buffer
	var rawBuf bytes.Buffer
	reader.Write(&rawBuf)
	rawData := rawBuf.Bytes()

	// Read() processes patterns: players, feedback, scores, time, ammo
	err = reader.Read()
	if err != nil && !dissect.Ok(err) {
		fmt.Fprintf(os.Stderr, "Warning: partial read: %v\n", err)
	}

	// Build output
	output := buildOutput(reader, rawData, *headerOnly)

	// Marshal JSON
	var jsonBytes []byte
	if *pretty {
		jsonBytes, err = json.MarshalIndent(output, "", "  ")
	} else {
		jsonBytes, err = json.Marshal(output)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
		os.Exit(1)
	}

	// Write output
	if *outFile != "" {
		if err := os.WriteFile(*outFile, jsonBytes, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing file: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Wrote %d bytes to %s\n", len(jsonBytes), *outFile)
	} else {
		os.Stdout.Write(jsonBytes)
		fmt.Println()
	}
}

// FullOutput combines header data with binary analysis.
type FullOutput struct {
	Header   HeaderInfo              `json:"header"`
	Analysis *analysis.RoundAnalysis `json:"analysis,omitempty"`
}

// HeaderInfo is the round header from the dissect library.
type HeaderInfo struct {
	GameVersion   string             `json:"gameVersion"`
	MatchID       string             `json:"matchId"`
	MapName       string             `json:"mapName"`
	GameMode      string             `json:"gameMode"`
	RoundNumber   int                `json:"roundNumber"`
	Teams         []TeamInfo         `json:"teams"`
	Players       []PlayerInfoHeader `json:"players"`
	MatchFeedback []FeedbackInfo     `json:"matchFeedback,omitempty"`
}

type TeamInfo struct {
	Name  string `json:"name"`
	Score int    `json:"score"`
	Won   bool   `json:"won"`
	Role  string `json:"role"`
}

type PlayerInfoHeader struct {
	Username        string `json:"username"`
	Operator        string `json:"operator"`
	InitialOperator string `json:"initialOperator,omitempty"`
	TeamIndex       int    `json:"teamIndex"`
	IsAttack        bool   `json:"isAttack"`
	Spawn           string `json:"spawn,omitempty"`
}

type FeedbackInfo struct {
	Type     string `json:"type"`
	Username string `json:"username,omitempty"`
	Target   string `json:"target,omitempty"`
	Headshot bool   `json:"headshot,omitempty"`
	Time     string `json:"time,omitempty"`
}

func buildOutput(reader *dissect.Reader, rawData []byte, headerOnly bool) FullOutput {
	// Extract header info
	header := HeaderInfo{
		GameVersion: reader.Header.GameVersion,
		MatchID:     reader.Header.MatchID,
		RoundNumber: reader.Header.RoundNumber,
	}

	// Map name
	if reader.Header.Map != 0 {
		header.MapName = reader.Header.Map.String()
	}

	// Game mode
	if reader.Header.GameMode != 0 {
		header.GameMode = reader.Header.GameMode.String()
	}

	// Teams
	for _, t := range reader.Header.Teams {
		role := ""
		if t.Role == dissect.Attack {
			role = "Attack"
		} else if t.Role == dissect.Defense {
			role = "Defense"
		}
		header.Teams = append(header.Teams, TeamInfo{
			Name:  t.Name,
			Score: t.Score,
			Won:   t.Won,
			Role:  role,
		})
	}

	// Players
	for _, p := range reader.Header.Players {
		isAttack := false
		if p.TeamIndex < len(reader.Header.Teams) {
			isAttack = reader.Header.Teams[p.TeamIndex].Role == dissect.Attack
		}
		ph := PlayerInfoHeader{
			Username:  p.Username,
			Operator:  p.Operator.String(),
			TeamIndex: p.TeamIndex,
			IsAttack:  isAttack,
			Spawn:     p.Spawn,
		}
		if p.InitialOperator != 0 {
			ph.InitialOperator = p.InitialOperator.String()
		}
		header.Players = append(header.Players, ph)
	}

	// Match feedback
	for _, mf := range reader.MatchFeedback {
		hs := false
		if mf.Headshot != nil {
			hs = *mf.Headshot
		}
		fb := FeedbackInfo{
			Type:     mf.Type.String(),
			Username: mf.Username,
			Target:   mf.Target,
			Headshot: hs,
			Time:     mf.Time,
		}
		header.MatchFeedback = append(header.MatchFeedback, fb)
	}

	output := FullOutput{Header: header}

	if headerOnly {
		return output
	}

	// Run binary analysis
	if len(rawData) > 0 {
		players := make([]analysis.PlayerInfo, len(header.Players))
		for i, p := range header.Players {
			players[i] = analysis.PlayerInfo{
				Username:  p.Username,
				Operator:  p.Operator,
				TeamIndex: p.TeamIndex,
				IsAttack:  p.IsAttack,
			}
		}

		// Extract entity->player mapping from dissect library's PositionUpdates
		entityToPlayer := make(map[uint32]int)
		for _, pu := range reader.PositionUpdates {
			if pu.PlayerIndex >= 0 && pu.PlayerIndex < len(players) {
				entityToPlayer[pu.EntityRef] = pu.PlayerIndex
			}
		}

		// Convert dissect library positions to analysis format
		libPositions := make([]analysis.LibraryPosition, len(reader.PositionUpdates))
		for i, pu := range reader.PositionUpdates {
			libPositions[i] = analysis.LibraryPosition{
				EntityRef:   pu.EntityRef,
				PlayerIndex: pu.PlayerIndex,
				X:           pu.X,
				Y:           pu.Y,
				Z:           pu.Z,
				Yaw:         pu.Yaw,
				Pitch:       pu.Pitch,
				IsDroneView: pu.IsDroneView,
				BinOffset:   pu.BinOffset,
			}
		}

		output.Analysis = analysis.AnalyzeRoundWithLibraryPositions(rawData, players, libPositions, entityToPlayer)

		// Populate operator swaps from the library's already-resolved MatchFeedback.
		// reconcileOperatorSwaps() (called inside reader.Read()) handles all season paths:
		// Y10S4+: header RoleName vs readPlayer operator, pre-Y9S3: DissectID, Y9S3+: uiID.
		usernameToIdx := make(map[string]int, len(header.Players))
		for i, p := range header.Players {
			usernameToIdx[p.Username] = i
		}

		// For Y10S4+ the library skips the binary event entirely so MatchFeedback has no
		// timing. Cross-reference with the binary scanner results (which DO have timing
		// via tick interpolation) using toOperator as the join key.
		binarySwapTime := make(map[string]float32)
		for _, bsw := range output.Analysis.OperatorSwaps {
			if bsw.ToOperator != "" && bsw.TimeSecs > 0 {
				binarySwapTime[bsw.ToOperator] = bsw.TimeSecs
			}
		}

		var libSwaps []analysis.OperatorSwapEvent
		for _, mf := range reader.MatchFeedback {
			if mf.Type != dissect.OperatorSwap {
				continue
			}
			pIdx, ok := usernameToIdx[mf.Username]
			if !ok {
				continue
			}
			from := ""
			if pIdx < len(header.Players) && header.Players[pIdx].InitialOperator != "" {
				from = header.Players[pIdx].InitialOperator
			}
			toName := mf.Operator.String()
			t := float32(mf.TimeInSeconds)
			if t == 0 {
				if bt, ok := binarySwapTime[toName]; ok {
					t = bt
				}
			}
			libSwaps = append(libSwaps, analysis.OperatorSwapEvent{
				PlayerIndex:  pIdx,
				Username:     mf.Username,
				FromOperator: from,
				ToOperator:   toName,
				TimeSecs:     t,
			})
		}
		output.Analysis.OperatorSwaps = libSwaps

		// Build tick elapsed map once for reuse across event arrays
		tickElapsed := analysis.BuildTickElapsedMap(output.Analysis.TimerTicks, output.Analysis.RoundDuration)

		// Score delta events — already fully parsed by the library
		for _, su := range reader.ScoreUpdates {
			output.Analysis.ScoreUpdates = append(output.Analysis.ScoreUpdates, analysis.ScoreUpdateEvent{
				PlayerIndex: su.PlayerIndex,
				Username:    su.Username,
				PrevScore:   su.PrevScore,
				NewScore:    su.NewScore,
				Delta:       su.Delta,
				TimeSecs:    float32(tickElapsed(int64(su.BinOffset))),
				BinOffset:   su.BinOffset,
			})
		}

		// Build seq-number → BinOffset map (Seq is a stream sequence number, NOT an array index).
		posLen := len(reader.PositionUpdates)
		seqToBinOff := make(map[int]int64, posLen)
		for _, pu := range reader.PositionUpdates {
			seqToBinOff[pu.Seq] = int64(pu.BinOffset)
		}

		// Drone lifecycle events — map Seq (stream number) → BinOffset → elapsed time
		for _, de := range reader.DroneEvents {
			binOff := seqToBinOff[de.Seq] // 0 if not found (safe default)
			output.Analysis.DroneEvents = append(output.Analysis.DroneEvents, analysis.DroneEventEntry{
				PlayerRef: de.PlayerRef,
				DroneRef:  de.DroneRef,
				Seq:       de.Seq,
				Connect:   de.Connect,
				TimeSecs:  float32(tickElapsed(binOff)),
			})
		}

		// Death timings — last significant movement position for each player who died
		for _, dt := range reader.DeathTimings {
			output.Analysis.DeathTimings = append(output.Analysis.DeathTimings, analysis.DeathTimingEntry{
				PlayerIndex:         dt.PlayerIndex,
				LastMovementSeq:     dt.LastMovementSeq,
				LastMovementTimeSec: dt.LastMovementTime,
				LastX:               dt.LastX,
				LastY:               dt.LastY,
				LastZ:               dt.LastZ,
			})
		}

		// PlayerTrack.KilledAt — use the last frame's TimeSecs (reliable after timing fix).
		// In Y11S1+, position BinOffsets are in a different byte range than timer ticks, so
		// seq→BinOffset→tickElapsed returns 0. The last frame time is a better approximation.
		killedPlayers := make(map[int]bool, len(reader.DeathTimings))
		for _, dt := range reader.DeathTimings {
			killedPlayers[dt.PlayerIndex] = true
		}
		for i := range output.Analysis.Players {
			pt := &output.Analysis.Players[i]
			if !killedPlayers[pt.PlayerIndex] {
				continue
			}
			// Scan backward for last non-camera frame with a valid time.
			// Camera frames appended during extraction may have unreliable offsets.
			for j := len(pt.Frames) - 1; j >= 0; j-- {
				f := pt.Frames[j]
				if !f.IsCamera && f.TimeSecs > 0 {
					pt.KilledAt = f.TimeSecs
					break
				}
			}
		}

		// Camera frames from the dissect library (spectator/POV look directions).
		// Camera frame BinOffsets may be in a different byte range than timer ticks;
		// use tickElapsed which returns 0 for out-of-range offsets (harmless for visualization).
		for _, cf := range reader.CameraFrames {
			output.Analysis.CameraFrames = append(output.Analysis.CameraFrames, analysis.LibraryCameraFrame{
				PlayerIndex: cf.PlayerIndex,
				Qx:          cf.Qx,
				Qy:          cf.Qy,
				Qz:          cf.Qz,
				Qw:          cf.Qw,
				YawDeg:      cf.YawDeg,
				PitchDeg:    cf.PitchDeg,
				TimeSecs:    float32(tickElapsed(int64(cf.BinOffset))),
				BinOffset:   cf.BinOffset,
			})
		}

		// Library shot events — TimeInSeconds is a COUNTDOWN (seconds remaining).
		// Convert to elapsed time: elapsed = roundDuration - countdown.
		// The Seq→BinOffset path fails in Y11S1+ where shot Seq values exceed the
		// position-update Seq range; TimeInSeconds is always populated correctly.
		shotRoundDur := float64(output.Analysis.RoundDuration)
		for _, se := range reader.ShotEvents {
			elapsed := shotRoundDur - se.TimeInSeconds
			if elapsed < 0 {
				elapsed = 0
			}
			output.Analysis.LibraryShots = append(output.Analysis.LibraryShots, analysis.LibraryShotEntry{
				PlayerIndex: se.PlayerIndex,
				X:           se.X,
				Y:           se.Y,
				Z:           se.Z,
				Yaw:         se.Yaw,
				Pitch:       se.Pitch,
				HeadQX:      se.HeadQX,
				HeadQY:      se.HeadQY,
				HeadQZ:      se.HeadQZ,
				HeadQW:      se.HeadQW,
				TimeSecs:    elapsed,
				Seq:         se.Seq,
			})
		}

		// Library ammo updates (raw ammo state per weapon event)
		for _, au := range reader.AmmoUpdates {
			output.Analysis.LibraryAmmoUpdates = append(output.Analysis.LibraryAmmoUpdates, analysis.LibraryAmmoUpdate{
				PlayerIndex: au.PlayerIndex,
				Available:   au.Available,
				Capacity:    au.Capacity,
				TimeSecs:    float32(tickElapsed(int64(au.BinOffset))),
				BinOffset:   au.BinOffset,
			})
		}

		// Library game actions (more reliable than binary scan for newer seasons)
		for _, ga := range reader.GameActions {
			output.Analysis.LibraryGameActions = append(output.Analysis.LibraryGameActions, analysis.LibraryGameAction{
				Type:      ga.Type,
				TimeSecs:  float32(tickElapsed(int64(ga.BinOffset))),
				BinOffset: ga.BinOffset,
			})
		}

		// Library health updates — supplement the binary scanner's results.
		// The library's scanner finds hp=0 events (deaths/DBNOs) with correct player attribution.
		// Their BinOffset is in the 56-61 MB region (between position stream and timer stream).
		// Assign timing via min-max over the combined health update offset range.
		if len(reader.HealthUpdates) > 0 {
			for _, hu := range reader.HealthUpdates {
				output.Analysis.HealthUpdates = append(output.Analysis.HealthUpdates, analysis.HealthUpdate{
					PlayerIndex: hu.PlayerIndex,
					Health:      hu.Health,
					BinOffset:   hu.BinOffset,
				})
			}
			// Re-apply min-max timing over all health updates (binary scanner + library).
			if len(output.Analysis.HealthUpdates) > 0 {
				minOff, maxOff := int64(output.Analysis.HealthUpdates[0].BinOffset), int64(output.Analysis.HealthUpdates[0].BinOffset)
				for _, hu := range output.Analysis.HealthUpdates {
					o := int64(hu.BinOffset)
					if o < minOff {
						minOff = o
					}
					if o > maxOff {
						maxOff = o
					}
				}
				if maxOff > minOff {
					for i := range output.Analysis.HealthUpdates {
						frac := float64(int64(output.Analysis.HealthUpdates[i].BinOffset)-minOff) / float64(maxOff-minOff)
						output.Analysis.HealthUpdates[i].TimeSecs = float32(frac * float64(output.Analysis.RoundDuration))
					}
				}
			}
		}

		// Entity classification from library TrackedEntities.
		// The binary SPAWN counter scanner uses different byte offsets than the library,
		// causing ref mismatches. Prefer the library's already-classified entity types.
		if len(reader.TrackedEntities) > 0 {
			trackedMap := make(map[uint32]dissect.TrackedEntity, len(reader.TrackedEntities))
			for _, te := range reader.TrackedEntities {
				trackedMap[te.EntityRef] = te
			}
			for i := range output.Analysis.Entities {
				te, ok := trackedMap[output.Analysis.Entities[i].EntityID]
				if !ok {
					continue
				}
				if te.Type != "" && te.Type != dissect.EntityUnknown {
					output.Analysis.Entities[i].Type = string(te.Type)
				}
				if te.GadgetName != "" {
					output.Analysis.Entities[i].GadgetType = te.GadgetName
				}
				if te.BarricadeType != "" {
					output.Analysis.Entities[i].BarricadeType = te.BarricadeType
				}
			}
		}
	}

	return output
}
