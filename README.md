# r6-replay-tool

Comprehensive Rainbow Six Siege `.rec` replay file analyzer. Extracts position tracking, bone/aim data, weapon analytics, equipment loadouts, shot reconstruction, health monitoring, entity classification, camera frames, and game events from replay files.

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
- **6 decoded ammo TLV hashes**:
  - `0x29C80A40` — current magazine ammo (decrements per shot)
  - `0x3E6D5B6D` — loaded ammo (magazine + chambered)
  - `0xAA4BBC34` — reserve ammo pool
  - `0x219E95DE` — grand total (reserve + loaded)
  - `0x653E26DD` — running total remaining
  - `0x0A44F556` — small counter (init events)
- **Shot detection** — counts magazine decrements per weapon
- **Weapon→player mapping** via `5F 85 CC 85` init blocks (last 5% of binary)
- **Primary/secondary classification** by ammo pool size and weapon name

### Equipment Loadouts
- Full equipment extraction from binary header: operator, primary weapon, secondary weapon, primary gadget, secondary gadget
- **100+ weapon/gadget name database** (C8-SFW, MP5, Vector .45, ALDA 556, etc.)
- Primary vs secondary classification using name lookup tables

### Health Tracking
- Health property hash `0x4171D3C3` in post-80% binary region
- Per-player health state changes (0=dead, 100=full, intermediates=damage)
- Entity health events (drone destruction, gadget damage)

### Entity Classification
- **SPAWN counters**: 494=player, 154=drone, 146=gadget, 130=barricade, 138/254=weapon
- **FC-UPDATE flag fingerprinting** for projectile sub-types (impact, grapple)
- Barricade detection (counter=130 + flag `0x1FE0`)

### Camera Frames
- **Per-entity camera detection** — signature `0xa5b2f3a5` + forward scan for archetype `0xFE857360`
- Maps camera look-direction to individual player entities (multi-player replay support)
- Camera frame filtering (strips camera data from gadgets/weapons/barricades)

### Timer & Events
- **Timer ticks** — pattern `1F 07 EF C9 04` + countdown seconds
- **Phase detection** — prep (~44s) and action (~180s) phases from tick gaps
- **Binary match feedback** — kill/DBNO/death events from binary signatures
- **Game actions** — reinforce and gadget deploy detection

### Shot Reconstruction
- Matches ammo decrease events to nearest player position/rotation
- Produces per-shot events with: position (XYZ), aim direction (yaw/pitch), head bone quaternion
- Enables bullet trace visualization

## Installation

```bash
# Clone
git clone https://github.com/N4m-N4m/r6-replay-tool.git
cd r6-replay-tool

# Build
go build -o r6-replay-tool.exe .

# Or with 64-bit explicitly (recommended for large replays)
set GOARCH=amd64
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

## Output Format

The JSON output has two top-level sections:

```jsonc
{
  "header": {
    "gameVersion": "Y11S1",
    "matchId": "...",
    "mapName": "Oregon",
    "gameMode": "Bomb",
    "roundNumber": 1,
    "teams": [...],
    "players": [...],
    "matchFeedback": [...]
  },
  "analysis": {
    "players": [...],          // Per-player position/rotation tracks
    "entities": [...],         // Drones, cameras, gadgets, projectiles
    "weapons": {...},          // Per-player ammo tracking
    "loadouts": [...],         // Equipment (weapon/gadget names)
    "shots": [...],            // Reconstructed shot events
    "healthUpdates": [...],    // Health state changes
    "timerTicks": [...],       // Round timer ticks
    "timerPhases": [...],      // Prep/action phases
    "binaryFeedback": [...],   // Kill/DBNO events from binary
    "gameActions": [...],      // Reinforce, gadget deploy
    "recordingPlayer": 0,      // Index of recorded POV
    "roundDuration": 223.5     // Total round duration (seconds)
  }
}
```

### Player Track

```jsonc
{
  "entityId": 4027031636,
  "playerIndex": 0,
  "username": "PlayerName",
  "operator": "Sledge",
  "teamIndex": 0,
  "isAttacker": true,
  "killedAtSecs": 145.2,
  "frames": [
    {
      "offset": 1856,
      "x": 3.08, "y": 8.30, "z": 4.02,
      "yawDeg": 120.0, "pitchDeg": -5.2,
      "timeSecs": 12.5,
      "stance": "standing",
      "hoX": 0.05, "hoY": -0.02, "hoZ": 0.12,  // head bone offset
      "hqX": 0.1, "hqY": 0.0, "hqZ": 0.7, "hqW": 0.7  // head aim quat
    }
  ]
}
```

### Weapon Tracking

```jsonc
{
  "playerIndex": 0,
  "primary": {
    "weaponEid": 4028426207,
    "isPrimary": true,
    "weaponName": "L85A2",
    "weaponCategory": "AR/SMG",
    "magazineSize": 31,
    "initialAmmo": 186,
    "finalAmmo": 22,
    "shotsFired": 47
  },
  "secondary": {
    "weaponEid": 4028426204,
    "weaponName": "P226 MK 25",
    "magazineSize": 16,
    "initialAmmo": 61,
    "shotsFired": 0
  }
}
```

## Binary Format Reference

### Key Patterns

| Pattern | Purpose |
|---------|---------|
| `60 73 85 FE` | FC-UPDATE archetype (0xFE857360) — movement/entity packets |
| `61 73 85 FE` | SPAWN archetype (0xFE857361) — entity init records |
| `77 CA 96 DE` | Ammo event marker |
| `1F 07 EF C9` | Timer tick |
| `22 D9 13 3C BA` | Kill event indicator |
| `22 96 E2 29 7F` | DBNO marker |
| `02 00 70 88 98 58` | Head bone magic (BMA) |
| `00 2C 36 14 9B` | Chest bone magic (BMB) |
| `5F 85 CC 85` | Weapon init block (late file) |
| `0xa5b2f3a5` | Camera quaternion signature |
| `EC DA 4F 80` | Scoreboard score |
| `1C D2 B1 9D` | Scoreboard kills |
| `4D 73 7F 9E` | Scoreboard assists |

### Binary Layout (Y10S4+)

```
[0% - 5%]    Header: player info, team data, operator loadouts
[5% - 25%]   Entity init blocks (SPAWN records, loadout definitions)
[25% - 95%]  Movement data (position/rotation packets, ~40K per round)
[95% - 98%]  Weapon init blocks (5F 85 CC 85)
[98% - 100%] Timer ticks, scoreboard, match feedback, health updates
```

### Coordinate System

```
Replay coords:  rX = east,  rY = north, rZ = up
Three.js/World:  X = -rX,    Y = rZ,     Z = rY
With offsets:    worldX = -rX + offsetX
                 worldY = rZ + offsetY
                 worldZ = rY + offsetZ
Default Bank:    offset = (-65, -10, +17)
```

### Entity ID Prefixes

```
R01: 0xF006xxxx    R05: 0xF002xxxx
R02: 0xF005xxxx    R06: 0xF001xxxx
R03: 0xF004xxxx    R07: 0xF000xxxx
R04: 0xF003xxxx    R08: 0xF00Fxxxx (rolls over)
```

## Project Structure

```
r6-replay-tool/
├── main.go              # CLI entry point
├── analysis/
│   ├── analysis.go      # Main pipeline
│   ├── types.go         # All data structures
│   ├── positions.go     # Position extraction + entity mapping
│   ├── ammo.go          # Ammo tracking + shot reconstruction
│   ├── bone.go          # Head/chest bone data
│   ├── camera.go        # Camera frame detection
│   ├── events.go        # Kill/DBNO, game actions, loadouts, classification
│   ├── health.go        # Health monitoring
│   ├── timer.go         # Timer ticks + phases
│   └── names.go         # Weapon/gadget name database
├── docs/
│   └── BINARY_FORMAT.md # Extended format reference
├── README.md
├── LICENSE
└── go.mod
```

## TODO

### High Priority
- [x] **Fix player-entity mapping** — solved via `dissect.validateEntityMapBySpawn` (3D centroid swap detection) + position-based fallback
- [x] **Match time syncing** — `RoundDurationFromTicks` and `tickElapsed` interpolation now drive every timed event
- [ ] **Fix look angles** — better matching between camera rotation and movement rotation data
- [x] **Shoot/hit detection & bullet traces** — `analysis.ReconstructShots` plus library `ShotEvents` (with `PlayerIndex`) cover this
- [x] **Remove the `replace` directive** — `dissect/` is vendored in-tree

### Medium Priority
- [ ] **Hostage/bomb/container detection** — identify objective entities in the binary
- [x] **Attacker operator swap** — `readAtkOpSwap` listener on `22 A9 26 0B E4`
- [x] **Improve health mapping** — `findPrecedingEntity` walks back 256 bytes to the F0-prefix ref
- [x] **Score delta tracking** — Y10S4+ detects defuser planter/disabler from the +100 bonus
- [x] **Entity init block fallback** — `MapPlayersFromInitBlocks` uses `61 73 85 FE` order
- [x] **Drone connect/disconnect state machine** — `playerDroneState` + `droneViewMarkers` produce confirmed `DroneEvents`
- [x] **Game action timestamps** — reinforce / gadget deploy now use tick-interpolated time

### Low Priority
- [ ] **Rappel detection** — identify rappel state from movement packets
- [ ] **Gadget effective area marking** — visualize gadget influence zones
- [ ] **Per-round entity prefix validation** — R01=0xF006, R02=0xF005, etc. instead of heuristic prefix detection
- [ ] **Barricade proximity ownership** — assign barricades to nearest same-team player
- [x] **Entity health events** — `DestructionEvents` and `ReviveEvents` cover drone / gadget HP transitions
- [ ] **Web UI** — browser-based 2D/3D replay viewer with timeline scrubbing
- [ ] **Multi-round batch processing** — analyze entire match folders, aggregate stats across rounds

### Done
- [x] Position extraction (SPAWN + FC-UPDATE)
- [x] Rotation/quaternion from 0x03xx packets
- [x] Head + chest bone data (BMA/BMB)
- [x] Ammo tracking (6 TLV hashes)
- [x] Weapon init block mapping
- [x] Equipment loadout extraction (weapon/gadget names)
- [x] Health property scanning
- [x] Timer tick extraction + phase detection
- [x] Binary match feedback (kill/DBNO/death)
- [x] Game action detection (reinforce, gadget deploy)
- [x] Camera frame detection (per-entity)
- [x] Stance inference (standing/crouching/prone)
- [x] Entity classification (drone/gadget/barricade/weapon/projectile)
- [x] Weapon/gadget name database (100+ items)
- [x] CLI with JSON output

## Credits

- [Nam-Nam](https://github.com/N4m-N4m) — binary format research, ammo/loadout/weapon tracking, equipment name database, entity classification, web viewer
- Based on [r6-dissect](https://github.com/redraskal/r6-dissect) by redraskal

## License

MIT
