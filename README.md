# r6-replay-tool

Comprehensive Rainbow Six Siege `.rec` replay file analyzer. Extracts position tracking, bone/aim data, weapon analytics, equipment loadouts, shot reconstruction, health monitoring, entity classification, camera frames, kill TLVs, defuser ticks, scoreboard, and derived analytics from replay files.

## Features

### Position & Movement
- **SPAWN + FC-UPDATE extraction** — decodes all entity position packets (pattern `60 73 85 FE`)
- **Rotation/quaternion** from 0x03xx type packets (yaw + pitch from trail quaternion)
- **Stance detection** — standing/crouching/prone inferred from Z-height deviation
- **Yaw unwrapping** — continuous yaw without ±180° discontinuities

### Bone & Aim Data
- **Head bone** (BMA: `02 00 70 88 98 58`) — head offset + aim quaternion (36-byte payload)
- **Chest bone** (BMB: `00 2C 36 14 9B`) — chest offset + rotation quaternion
- ~74% coverage of position updates, enables precise aim direction reconstruction

### Weapons & Ammo
- **Decoded ammo TLV hashes** (`0x29C80A40` mag, `0x3E6D5B6D` loaded, `0xAA4BBC34` reserve, etc.)
- **Hash2 weapon-slot decoder** — `0x00000000`=primary, `0x29C80A40`=secondary, `0xAA4BBC34`=grenade, `0x653E26DD`=op_gadget
- **Shot detection** — counts magazine decrements per weapon
- **Per-loadout slot metadata** — `slotType`, `maxCap`, `shotCount` populated from real ammo events
- **Session-variable ID resolver** — prefers `slotSecondaryWeapon` (canonical) over `slotMeleeWeapon` (session)

### Equipment Loadouts
- **All 5 loadout slots decoded**: primary weapon, secondary weapon, **operator gadget** (auxHash `0x1DA32C08`), secondary gadget, reinforcement
- **Y11 game-data dump integrated** — ~500 weapon/gadget hashes including all V2 instances, drone families, OSP grenades
- All Y11 operator signature gadgets resolve: SHOCK WIRE (Bandit), KIBA BARRIER (Azami), KULAKOV (Tachanka), ERC-7 (Vigil), GLANCE SMART GLASSES (Warden), RTILA ELECTROCLAW (Kaid), BANSHEE (Melusi), WELCOME MAT (Frost), SIGNAL DISRUPTOR (Mute), VIPERSTRIKE MINE (Denari), RADAR (SolidSnake), RAM BU-GI AUTOBREACHER (Ram), and more
- **Library loadouts** (`libraryLoadouts[]`) — top-level array with weapon entity refs, init capacities, hashes

### Health Tracking
- Health property hash `0x4171D3C3` with co-located sub-properties:
  - `0xC2D846F8` — max HP
  - `0x475BB68B` — damage rate
  - `0xF634093A` — hit counter
  - `0x848F67CF` — health-event time
- **Health state labels** — `alive` / `dbno` (1–5 hp range, bleed-out fraction) / `dead`
- Per-player health state changes with full sub-property decoding

### Kill / DBNO Events (extended TLVs, Y11S1+)
- **DBNO window** widened from 70 to 256 bytes for Y11S1 layout
- **Decoded kill TLV fields**:
  - `0x790009E3` — weapon entity ref (u64)
  - `0x8F0292B5` — kill flag (u8)
  - `0x5BC4BC84` `0x37BF3E90` `0xD13DA88D` `0x3187B853` `0x0B64ADA5` — five kill enums
  - `0x65DD6CF8` — canonical weapon hash per kill (consistent across same-weapon kills)
  - `0x4EA45BC3` — corroborating headshot byte
- **Decoded enums**: `KillEnum3 - 1 = AttackerTeam`, `KillEnum4 - 1 = VictimTeam` (verified across 7 kills)
- **DBNO attribution** — `dbnoBy`, `finishedBy` fields on `matchFeedback` entries
- **Sub-stream kill TLVs** (decoded from secondary kill-record copies, ±2048 byte window):
  - `roundTimeMs` (`0x6C463718`) — ms since round start, matches `MatchFeedback.timeInSeconds` within 1 s
  - `killDamage` (`0xC9527BDD`) — f32 damage of killing bullet / max-health (HS mean 0.63 vs 0.33 body, n=95)
  - `killRange` (`0xFB9DBF08`) — f32 weapon-class range / falloff factor
  - `hitZone` (`0xA5F688E7`) — body-part enum 0/1/2/3/4 for the killing shot
  - `victimEnumA/B`, `victimCounter`, `victimEnt64` — entity-scoped TLVs (`0x23` marker) on the victim pawn

### Round-End Scoreboard
- `header.scoreboard[]` — final tallies per player ID (score, assists, round-assists)
- Eliminates need to sum scoreUpdate deltas

### Defuser Timer Ticks
- `defuserTicks[]` — per-frame tick stream with `state` ("planting" / "disabling" / "planted_idle")
- Enables progress-bar rendering and exact frame completion detection

### Entity Classification
- **SPAWN counters**: 494=player, 154=drone, 146=gadget, 130=barricade, 138/254=weapon
- **FC-UPDATE flag fingerprinting** for projectile sub-types
- **Transient entity filter** — drops single-frame entities with no spawn counter (visual stubs / particles)

### Camera Frames
- Per-entity camera detection
- **Spectator POV periods** — recording-player camera target as contiguous time windows

### Timer & Game Events
- Timer ticks + prep/action phase detection
- Match feedback with raw countdown (`timeSecs`) AND decoded countdown→elapsed conversion
- Game actions: reinforce, gadget deploy
- Operator-swap events (Y10S4+ via `MatchFeedback`, pre-Y10S4 via binary scan)

### Derived Analytics (post-pipeline enrichments)
- **`hits[]`** — kill→shot correlation: every kill / DBNO mapped to the killer's last shot
- **`trades[]`** — kills within a 5s trade window of teammate's death
- **`reinforcements[]`** — wall reinforce events deduped + attributed to nearest defender XYZ (capped at game-rule max of 10)
- **`spectatorPeriods[]`** — recording-player POV target with frame counts
- **`bombPlant`** — planter index, time, XYZ
- **`bombSite`** — floor + description from defender spawn metadata + Z-cluster center
- **`outcome`** — winning team + role + win condition (KilledOpponents / DefusedBomb / DisabledDefuser / PlantDetonation / Time)
- **`shotDamages[]`** — per-shot damage estimates from kill events
- **`destructionEvents[]`** — entity destructions from `TrackedEntities.HealthEvents` with entity-type classification

### Maps
- All Y9–Y11 map IDs including `HouseY11` (`434715462383`) added to `dissect/map_string.go`

## Installation

```bash
# Clone
git clone https://github.com/wnc-replay/replay-tool.git
cd replay-tool

# Build
go build -o r6-replay-tool.exe .
```

### Requirements
- Go 1.23+
- Dependencies auto-resolved via `go mod`

## Usage

```bash
# Analyze a round and output JSON to stdout
r6-replay-tool match-R01.rec

# Pretty-printed JSON
r6-replay-tool -pretty match-R01.rec

# Write to file
r6-replay-tool -o analysis.json match-R01.rec

# Header only (fast, no binary analysis)
r6-replay-tool -header match-R01.rec
```

### RE Inspection Tools

The `cmd/` directory contains binary inspection tools used for reverse engineering. Each is built and run independently:

| Tool | Purpose |
|------|---------|
| `cmd/inspect` | Dump context around mystery hashes + low-HP samples |
| `cmd/probe` | Kill-marker neighborhood + event-name string search |
| `cmd/deepscan` | Exhaustive byte survey (HP histogram, hash freq, ASCII strings, ammo refs, sg auxHash anchors) |
| `cmd/killdecode` | Per-kill TLV table for enum decoding |
| `cmd/healthdump` | Health sub-property distance/coverage analysis |
| `cmd/sesweapon` | Loadout slot trace + per-kill weapon-ID extraction |
| `cmd/opslot` | auxHash discovery for operator-gadget slot |
| `cmd/ammoref` | Per-player weapon entity refs + Hash1/Hash2 |
| `cmd/hashscan` | Cross-replay hash search (locate-by-block-index) |
| `cmd/enumstats` | Aggregate kill TLV stats across many replays (decoded killEnum / weaponEntRef64 across 402 kills / 85 replays) |

```bash
go run ./cmd/probe match-R01.rec
go run ./cmd/killdecode match-R01.rec
```

## Output Format

```jsonc
{
  "header": {
    "gameVersion": "Y11S1_Alpha03",
    "matchId": "...",
    "mapName": "HouseY11",
    "gameMode": "Bomb",
    "roundNumber": 5,
    "teams": [...],
    "players": [...],
    "matchFeedback": [
      {
        "type": "Kill",
        "username": "Killer",
        "target": "Victim",
        "headshot": true,
        "time": "1:03",       // mm:ss countdown
        "timeSecs": 63,        // raw countdown seconds
        "dbnoBy": "Knocker",   // who downed first (if different)
        "finishedBy": "Killer"
      }
    ],
    "scoreboard": [
      { "playerId": "d28713f0", "score": 3513, "assists": 2, "assistsFromRound": 2 }
    ]
  },
  "analysis": {
    "players": [...],
    "entities": [...],
    "weapons": {...},
    "loadouts": [
      {
        "playerIndex": 3,
        "operatorName": "Bandit",
        "primaryWeapon": { "name": "MP7", "slotType": "primary", "shotCount": 8, "maxCap": 150 },
        "secondaryWeapon": { "name": "KERATOS .357", "slotType": "secondary", "shotCount": 50, "maxCap": 148 },
        "primaryGadget": { "name": "SHOCK WIRE", "slotType": "op_gadget" },
        "secondaryGadget": { "name": "NITRO CELL (BANDIT)", "slotType": "grenade" },
        "reinforcement": { "name": "REINFORCEMENT" }
      }
    ],
    "shots": [...],
    "healthUpdates": [
      {
        "playerIndex": 5, "health": 100, "state": "alive",
        "maxHealth": 100, "damageRate": 0.08, "hitCounter": 1, "healthTime": 0
      }
    ],
    "binaryFeedback": [
      {
        "type": "kill", "attacker": "...", "target": "...", "headshot": true,
        "weaponId": "0x516BCE20",      // canonical weapon hash for this kill
        "attackerTeam": 0, "victimTeam": 1,
        "killEnum1": 2, "killEnum2": 1, "killEnum3": 1, "killEnum4": 2, "killEnum5": 0
      }
    ],
    "gameEvents": [...],            // all event types: kill/dbno/plant_*/defuse_*/locate_objective/operator_swap
    "hits": [...],                  // kill→shot correlation
    "trades": [...],                // 5s trade-window kills
    "reinforcements": [...],        // deduped reinforce + deployer XYZ
    "spectatorPeriods": [...],
    "bombPlant": { "planterIndex": 5, "timeSecs": 142, "x": ..., "y": ..., "z": ... },
    "bombSite": { "floor": "1F", "description": "1F Kitchen, 1F Cafeteria", "centerX": 58.9, ... },
    "outcome": { "winningTeam": 0, "winningRole": "Defense", "winCondition": "KilledOpponents", "attackersDead": 5, "defendersDead": 2 },
    "destructionEvents": [...],
    "recordingPlayer": 2,
    "roundDuration": 193
  },
  "libraryLoadouts": [
    {
      "playerIndex": 2, "username": "...",
      "primary":   { "entityRef": 4028433332, "initialCapacity": 125, "hash1": 1047608685, "hash2": 2857960500, "isPrimary": true },
      "secondary": { "entityRef": 4028433329, "initialCapacity": 91,  "hash1": 1047608685, "hash2": 2857960500, "isPrimary": false }
    }
  ],
  "defuserTicks": [
    { "timeSecs": 142, "time": "1:18", "rawValue": 7.0, "prevValue": 7.4, "state": "disabling" }
  ]
}
```

## Binary Format Reference

See [docs/BINARY_FORMAT.md](docs/BINARY_FORMAT.md) for the full hash table, packet layouts, and TLV semantics.

### Key Patterns

| Pattern | Purpose |
|---------|---------|
| `60 73 85 FE` | FC-UPDATE archetype (movement) |
| `61 73 85 FE` | SPAWN archetype (entity init) |
| `77 CA 96 DE` | Ammo event marker |
| `1F 07 EF C9` | Timer tick |
| `22 D9 13 3C BA` | Kill event indicator |
| `22 96 E2 29 7F` | DBNO marker (within ±256 bytes of kill in Y11S1+) |
| `02 00 70 88 98 58` | Head bone magic (BMA) |
| `00 2C 36 14 9B` | Chest bone magic (BMB) |
| `5F 85 CC 85` | Weapon init block |
| `0xa5b2f3a5` | Camera quaternion signature |
| `61 78 8C 1D` | Operator gadget slot auxHash (`0x1DA32C08`) |

## Project Structure

```
r6-replay-tool/
├── main.go              # CLI + buildOutput pipeline + scoreboard/loadout/defuser wiring
├── enrich.go            # Post-pipeline analytics (hits, trades, bomb plant, outcome, etc.)
├── analysis/
│   ├── analysis.go      # Main pipeline
│   ├── types.go         # All data structures
│   ├── positions.go     # Position extraction + entity mapping
│   ├── ammo.go          # Ammo tracking + shot reconstruction
│   ├── bone.go          # Head/chest bone data
│   ├── camera.go        # Camera frame detection
│   ├── events.go        # Kill/DBNO + extended TLVs, game actions, loadouts (6 slots), classification
│   ├── health.go        # Health monitoring + sub-property decoder (FillHealthSubProps)
│   ├── timer.go         # Timer ticks + phases
│   └── names.go         # Y11 game-data hash → name database (~500 entries)
├── cmd/                 # RE inspection tools (run with: go run ./cmd/<tool> file.rec)
│   ├── ammoref/
│   ├── deepscan/
│   ├── hashscan/
│   ├── healthdump/
│   ├── inspect/
│   ├── killdecode/
│   ├── opslot/
│   ├── probe/
│   └── sesweapon/
├── dissect/             # Vendored r6-dissect fork with extensions
│   ├── defuse.go        # ext: DefuserTick + per-frame recording
│   ├── reader.go        # ext: DefuserTicks []DefuserTick field
│   ├── header.go        # ext: HouseY11 = 434715462383
│   ├── map_string.go    # ext: HouseY11 in stringer table
│   └── ... (rest of upstream library)
├── docs/
│   └── BINARY_FORMAT.md # Extended format reference
├── README.md
├── LICENSE
└── go.mod
```

## TODO

### Open
- [ ] **Map Y11 Sledge secondary `0x63DC6FC00D`** — likely Reaper MK2 (family `0x63DC6FC0__`); not in current dump
- [ ] **Refine `killEnum1=2` semantics** — verified Y11S1-only flag (39% of Y11 kills); specific meaning still unknown (wallbang? DBNO finish? marked target?)
- [ ] **Web UI** — browser-based 2D/3D replay viewer with timeline scrubbing
- [ ] **Multi-round batch processing** — analyze entire match folders, aggregate stats across rounds
- [ ] **Hostage/bomb objective detection** — identify objective entities in the binary
- [ ] **Per-round entity prefix validation** — empirical findings contradict R01=0xF006/R02=0xF005 docs

### Done
- [x] Position extraction (SPAWN + FC-UPDATE)
- [x] Rotation/quaternion from 0x03xx packets
- [x] Head + chest bone data (BMA/BMB)
- [x] Ammo tracking (TLV hashes + slot decoder)
- [x] Equipment loadout extraction with all 6 slots (primary/secondary weapon, melee, op gadget, sec gadget, reinforcement)
- [x] Health property scanning + sub-property decoder
- [x] Timer tick extraction + phase detection
- [x] Binary match feedback (kill/DBNO/death) + extended TLVs
- [x] DBNO window expansion to 256 bytes (Y11S1+)
- [x] Kill enum decoding (attacker/victim team derivation)
- [x] Game action detection (reinforce, gadget deploy)
- [x] Camera frame detection (per-entity)
- [x] Stance inference (standing/crouching/prone)
- [x] Entity classification (drone/gadget/barricade/weapon/projectile)
- [x] Weapon/gadget name database (~500 items from Y11 game-data dump)
- [x] HouseY11 map ID
- [x] Operator gadget slot decoding (auxHash `0x1DA32C08`)
- [x] Hit detection (kill→shot correlation)
- [x] Trade kill detection
- [x] Reinforcement positions
- [x] Spectator POV periods
- [x] Bomb plant location + bomb site classification
- [x] Round outcome (winning team + win condition)
- [x] Round-end scoreboard
- [x] DBNO attribution (`dbnoBy` / `finishedBy`)
- [x] Library loadouts (entity refs + capacities)
- [x] Defuser timer ticks
- [x] Operator swap events (Y10S4+ library + pre-Y10S4 binary)
- [x] Drone connect/disconnect lifecycle
- [x] Score delta tracking
- [x] Destruction events from TrackedEntities.HealthEvents
- [x] CLI with JSON output

## Credits

- [Nam-Nam](https://github.com/N4m-N4m) — original binary format research, ammo/loadout/weapon tracking, equipment name database, entity classification
- [SorrowXXX](https://github.com/SorrowXXX) — extended kill TLVs, health sub-properties, DBNO window expansion, scoreboard surfacing, library loadouts, defuser tick stream, DBNO attribution (PR #1, PR #2)
- Based on [r6-dissect](https://github.com/redraskal/r6-dissect) by redraskal

## License

MIT
