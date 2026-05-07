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
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"

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

	// Run derived analytics (hits, trades, bomb plant, outcome, etc.)
	if !*headerOnly {
		enrichAnalysis(&output, reader)
	}

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
	Header          HeaderInfo              `json:"header"`
	Analysis        *analysis.RoundAnalysis `json:"analysis,omitempty"`
	LibraryLoadouts []LibraryLoadoutEntry   `json:"libraryLoadouts,omitempty"`
	DefuserTicks    []DefuserTickEntry      `json:"defuserTicks,omitempty"`
}

// DefuserTickEntry mirrors dissect.DefuserTick at the JSON layer.
type DefuserTickEntry struct {
	TimeSecs  float64 `json:"timeSecs"`
	Time      string  `json:"time,omitempty"`
	RawValue  float64 `json:"rawValue"`
	PrevValue float64 `json:"prevValue,omitempty"`
	State     string  `json:"state"`
}

// LibraryLoadoutEntry exposes the dissect library's per-player loadout — richer than
// analysis.Loadouts (entity refs, hashes, capacities, gadget counts).
type LibraryLoadoutEntry struct {
	PlayerIndex   int                  `json:"playerIndex"`
	Username      string               `json:"username"`
	Primary       *LibraryWeaponInfo   `json:"primary,omitempty"`
	Secondary     *LibraryWeaponInfo   `json:"secondary,omitempty"`
	GadgetLoadout *LibraryGadgetCounts `json:"gadgetLoadout,omitempty"`
}

type LibraryWeaponInfo struct {
	EntityRef       uint32 `json:"entityRef"`
	InitialCapacity uint32 `json:"initialCapacity"`
	Hash1           uint32 `json:"hash1,omitempty"`
	Hash2           uint32 `json:"hash2,omitempty"`
	IsPrimary       bool   `json:"isPrimary"`
}

type LibraryGadgetCounts struct {
	PrimaryCount   int  `json:"primaryCount"`
	SecondaryCount int  `json:"secondaryCount"`
	HasPrimary     bool `json:"hasPrimary"`
	HasSecondary   bool `json:"hasSecondary"`
}

// ScoreboardEntry is one row of the round-end scoreboard (final tallies).
type ScoreboardEntry struct {
	PlayerID         string `json:"playerId"`
	Score            uint32 `json:"score"`
	Assists          uint32 `json:"assists"`
	AssistsFromRound uint32 `json:"assistsFromRound,omitempty"`
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
	Scoreboard    []ScoreboardEntry  `json:"scoreboard,omitempty"`
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
	Type       string  `json:"type"`
	Username   string  `json:"username,omitempty"`
	Target     string  `json:"target,omitempty"`
	Headshot   bool    `json:"headshot,omitempty"`
	Time       string  `json:"time,omitempty"`       // mm:ss countdown string
	TimeSecs   float64 `json:"timeSecs,omitempty"`   // raw countdown seconds (TimeInSeconds)
	DBNOBy     string  `json:"dbnoBy,omitempty"`     // who downed the target (if different from finisher)
	FinishedBy string  `json:"finishedBy,omitempty"` // who finished the kill on a downed target
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
			Type:       mf.Type.String(),
			Username:   mf.Username,
			Target:     mf.Target,
			Headshot:   hs,
			Time:       mf.Time,
			TimeSecs:   mf.TimeInSeconds,
			DBNOBy:     mf.DBNOBy,
			FinishedBy: mf.FinishedBy,
		}
		header.MatchFeedback = append(header.MatchFeedback, fb)
	}

	// Round-end scoreboard (final tallies per player ID).
	for _, sp := range reader.Scoreboard.Players {
		header.Scoreboard = append(header.Scoreboard, ScoreboardEntry{
			PlayerID:         hex.EncodeToString(sp.ID),
			Score:            sp.Score,
			Assists:          sp.Assists,
			AssistsFromRound: sp.AssistsFromRound,
		})
	}

	output := FullOutput{Header: header}

	// Library detailed loadouts (entity refs, weapon hashes, gadget counts).
	for _, l := range reader.Loadouts {
		entry := LibraryLoadoutEntry{
			PlayerIndex: l.PlayerIndex,
			Username:    l.Username,
		}
		if l.Primary != nil {
			entry.Primary = &LibraryWeaponInfo{
				EntityRef:       l.Primary.EntityRef,
				InitialCapacity: l.Primary.InitialCap,
				Hash1:           l.Primary.Hash1,
				Hash2:           l.Primary.Hash2,
				IsPrimary:       l.Primary.IsPrimary,
			}
		}
		if l.Secondary != nil {
			entry.Secondary = &LibraryWeaponInfo{
				EntityRef:       l.Secondary.EntityRef,
				InitialCapacity: l.Secondary.InitialCap,
				Hash1:           l.Secondary.Hash1,
				Hash2:           l.Secondary.Hash2,
				IsPrimary:       l.Secondary.IsPrimary,
			}
		}
		if l.GadgetLoadout != nil {
			entry.GadgetLoadout = &LibraryGadgetCounts{
				PrimaryCount:   l.GadgetLoadout.PrimaryCount,
				SecondaryCount: l.GadgetLoadout.SecondaryCount,
				HasPrimary:     l.GadgetLoadout.HasPrimary,
				HasSecondary:   l.GadgetLoadout.HasSecondary,
			}
		}
		output.LibraryLoadouts = append(output.LibraryLoadouts, entry)
	}

	// Defuser timer ticks (per-frame plant/disable progress).
	for _, dt := range reader.DefuserTicks {
		output.DefuserTicks = append(output.DefuserTicks, DefuserTickEntry{
			TimeSecs:  dt.TimeInSeconds,
			Time:      dt.Time,
			RawValue:  dt.RawValue,
			PrevValue: dt.PrevValue,
			State:     dt.State,
		})
	}

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

		// Rebuild GameEvents from library MatchFeedback. The binary-feedback derived events
		// have all timestamps clamped near round end because their offsets sit at 62MB+
		// (kill-feedback region) which falls past the last timer tick anchor. The library
		// captures countdown values per event — convert via elapsed = roundDuration - countdown.
		// Also surface event types beyond Kill/DBNO: plant/defuse/locate/operator_swap.
		rd := float64(output.Analysis.RoundDuration)
		var gameEvents []analysis.GameEvent
		for _, mf := range reader.MatchFeedback {
			elapsed := rd - mf.TimeInSeconds
			if elapsed < 0 {
				elapsed = 0
			}
			var typeStr, text string
			hs := false
			if mf.Headshot != nil {
				hs = *mf.Headshot
			}
			switch mf.Type {
			case dissect.Kill:
				typeStr = "kill"
				if mf.Username != "" {
					text = mf.Username + " killed " + mf.Target
				} else {
					text = mf.Target + " died"
				}
			case dissect.Death:
				typeStr = "death"
				text = mf.Target + " died"
			case dissect.DBNO:
				typeStr = "dbno"
				if mf.Username != "" {
					text = mf.Username + " downed " + mf.Target
				} else {
					text = mf.Target + " downed"
				}
			case dissect.DefuserPlantStart:
				typeStr = "plant_start"
				text = mf.Username + " started planting"
			case dissect.DefuserPlantComplete:
				typeStr = "plant_complete"
				text = mf.Username + " planted the defuser"
			case dissect.DefuserDisableStart:
				typeStr = "defuse_start"
				text = mf.Username + " started defusing"
			case dissect.DefuserDisableComplete:
				typeStr = "defuse_complete"
				text = mf.Username + " disabled the defuser"
			case dissect.LocateObjective:
				typeStr = "locate_objective"
				text = mf.Username + " located the objective"
			case dissect.OperatorSwap:
				typeStr = "operator_swap"
				text = mf.Username + " swapped to " + mf.Operator.String()
			case dissect.PlayerLeave:
				typeStr = "player_leave"
				text = mf.Username + " left the match"
			case dissect.Battleye:
				typeStr = "battleye"
				text = mf.Username + " kicked by BattlEye"
			default:
				continue
			}
			gameEvents = append(gameEvents, analysis.GameEvent{
				Type:     typeStr,
				TimeSecs: float32(elapsed),
				Text:     text,
				Headshot: hs,
			})
		}
		gameEvents = append(gameEvents, analysis.GameEvent{
			Type:     "round_end",
			TimeSecs: output.Analysis.RoundDuration,
			Text:     "Round ended",
		})
		sort.Slice(gameEvents, func(i, j int) bool { return gameEvents[i].TimeSecs < gameEvents[j].TimeSecs })
		output.Analysis.GameEvents = gameEvents

		// Also fix BinaryFeedback timing using the same library data: match each binary
		// kill/dbno event to a library MatchFeedback entry by (attacker, target) and copy
		// the corrected timestamp.
		feedbackByPair := make(map[string]float64, len(reader.MatchFeedback))
		for _, mf := range reader.MatchFeedback {
			if mf.Type != dissect.Kill && mf.Type != dissect.DBNO {
				continue
			}
			feedbackByPair[mf.Username+"|"+mf.Target] = rd - mf.TimeInSeconds
		}
		for i := range output.Analysis.BinaryFeedback {
			bf := &output.Analysis.BinaryFeedback[i]
			key := bf.Attacker + "|" + bf.Target
			if t, ok := feedbackByPair[key]; ok && t >= 0 {
				bf.TimeSecs = t
			}
		}

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
				Hash1:       au.Hash1,
				Hash2:       au.Hash2,
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
				h := analysis.HealthUpdate{
					PlayerIndex: hu.PlayerIndex,
					Health:      hu.Health,
					BinOffset:   hu.BinOffset,
				}
				// Apply the same sub-property scan as the binary extractor.
				analysis.FillHealthSubProps(rawData, hu.BinOffset, &h)
				output.Analysis.HealthUpdates = append(output.Analysis.HealthUpdates, h)
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
			// Label health state. Values cluster into three regimes (deduced from binary
			// inspection of the 0x4171D3C3 health hash: only 0, (0,5), and >=100 appear —
			// no values between 5 and 100). The (0,5) range is the bleeding-out DBNO HP
			// fraction; once it reaches 0 the player is dead.
			for i := range output.Analysis.HealthUpdates {
				h := output.Analysis.HealthUpdates[i].Health
				switch {
				case h == 0:
					output.Analysis.HealthUpdates[i].State = "dead"
				case h < 5:
					output.Analysis.HealthUpdates[i].State = "dbno"
				default:
					output.Analysis.HealthUpdates[i].State = "alive"
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
				if te.SpawnCounter != 0 && output.Analysis.Entities[i].SpawnCounter == 0 {
					output.Analysis.Entities[i].SpawnCounter = te.SpawnCounter
				}
				if te.SpawnHashA != 0 && output.Analysis.Entities[i].SpawnHashA == 0 {
					output.Analysis.Entities[i].SpawnHashA = te.SpawnHashA
				}
			}
			// Filter transient/sub-entities. Binary inspection of the SPAWN pattern (61 73 85 FE)
			// shows ALL 376 matches have counter=0 — the real classification (counter=130/138/146/154/254)
			// happens elsewhere in the binary and is reflected in reader.TrackedEntities. Entities not
			// in TrackedEntities AND with only 1 position frame are visual stubs (bullet impacts,
			// particle effects, network sync placeholders), not gameplay entities.
			cleaned := output.Analysis.Entities[:0]
			for _, ent := range output.Analysis.Entities {
				_, isTracked := trackedMap[ent.EntityID]
				if !isTracked && len(ent.Frames) <= 1 && ent.Type == "unknown" {
					continue
				}
				cleaned = append(cleaned, ent)
			}
			output.Analysis.Entities = cleaned
			// Once entity types are finalised, attach each barricade to the nearest
			// same-team player at spawn time. Must run after library reclassification
			// because the binary scanner often labels these entities "unknown".
			analysis.AssignBarricadeOwners(output.Analysis.Entities, output.Analysis.Players)
			// Rebuild destruction events from TrackedEntities.HealthEvents. The library's
			// scanner properly attributes health events to entities via entity-ref proximity.
			// The binary scanner in our analysis pipeline produces many false positives
			// (entity refs like 0xF0000000 are padding alignment in the health stream).
			var libDestructions []analysis.DestructionEvent
			var allOffsets []int64
			for _, te := range reader.TrackedEntities {
				if len(te.HealthEvents) < 2 {
					continue
				}
				prevHP := -1
				for _, he := range te.HealthEvents {
					if prevHP > 0 && he.HP == 0 {
						entityType := string(te.Type)
						if entityType == "" {
							entityType = "unknown"
						}
						libDestructions = append(libDestructions, analysis.DestructionEvent{
							EntityID:   te.EntityRef,
							EntityType: entityType,
							GadgetType: te.GadgetName,
							BinOffset:  he.Offset,
						})
						allOffsets = append(allOffsets, he.Offset)
						break
					}
					prevHP = he.HP
				}
			}
			// Assign timing using the player health update offset range as a reference frame.
			// In Y11S1 the health stream sits below the first timer tick anchor (tickElapsed → 0),
			// and a single destruction event has no min-max range of its own. Player health
			// updates (already populated by min-max above) span the full round and share the
			// same byte region as entity health events.
			if len(libDestructions) > 0 && len(output.Analysis.HealthUpdates) >= 2 {
				huMin, huMax := int64(output.Analysis.HealthUpdates[0].BinOffset), int64(output.Analysis.HealthUpdates[0].BinOffset)
				for _, hu := range output.Analysis.HealthUpdates {
					o := int64(hu.BinOffset)
					if o < huMin {
						huMin = o
					}
					if o > huMax {
						huMax = o
					}
				}
				rd := float64(output.Analysis.RoundDuration)
				if huMax > huMin {
					for i := range libDestructions {
						o := libDestructions[i].BinOffset
						if o < huMin {
							o = huMin
						}
						if o > huMax {
							o = huMax
						}
						frac := float64(o-huMin) / float64(huMax-huMin)
						libDestructions[i].TimeSecs = float32(frac * rd)
					}
				}
			}
			output.Analysis.DestructionEvents = libDestructions
		}
	}

	return output
}
