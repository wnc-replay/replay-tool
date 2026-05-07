# R6 Siege .rec Binary Format Reference

## File Structure

`.rec` files use zstd compression. Modern replays (Y8S4+) use chunked compression
with multiple zstd frames. Older replays are a single zstd stream.

After decompression, the binary contains:

1. **Header magic** — version identifier and game metadata
2. **Player data** — triggered by pattern `22 07 94 9B DC` (one per player)
3. **Operator swap data** — pattern `22 A9 26 0B E4` (attacker swaps during prep)
4. **Spawn points** — pattern `AF 98 99 CA`
5. **Game state replication stream** — the bulk of the file

## Movement Packets

### FC-UPDATE (Position/Rotation)

**Archetype**: `0xFE857360` (LE bytes: `60 73 85 FE`)

```
Offset from pattern:
  -16..-13  Entity ref (u32 LE, F0-prefix for players)
   -8..-5   Packet size (u32 LE)
    0.. 3   Pattern [60 73 85 FE]
    4.. 5   Type field (u16 LE, bitfield)
    6+       Payload
```

**Type field bits**:
- Bit 7 of byte[0] (`& 0x80`): position data present (3× f32 XYZ at payload start)
- Type `0x0880`: drone view marker (no position data)
- `0x03xx` types: full quaternion in trail (4× f32 at +4 after XYZ)

**Position types** (with XYZ): `0x03B8`, `0x01B0`, `0x01B8`, `0x01C0`, `0x1Fxx`
**Property-only types** (no XYZ): `0x0440`, `0x0130`, `0x0420`, `0x0630`

### SPAWN Records

**Archetype**: `0xFE857361` (LE bytes: `61 73 85 FE`)

```
  -12..-9   Entity ref (u32 LE)
    0.. 3   Pattern [61 73 85 FE]
    8.. 9   Counter (u16 LE)
```

**Counter values**:
- 494: player entity assignment record
- 154: drone
- 146: gadget
- 130: barricade (confirmed with FC-UPDATE flag `0x1FE0`)
- 138: primary weapon
- 254: secondary weapon

## Bone Data

### Head Bone (BMA)

**Magic**: `02 00 70 88 98 58` (6 bytes)

Found within large FC-UPDATE packets. Payload (36 bytes after magic):

```
[0:4]   headOffX (f32) — lean displacement
[4:8]   headOffY (f32) — nod displacement
[8:12]  headOffZ (f32) — stance displacement
[12:16] separator (always 1.0)
[16:20] aimQx (f32) — head aim quaternion X
[20:24] aimQy (f32)
[24:28] aimQz (f32)
[28:32] aimQw (f32)
[32:36] separator (always 1.0)
```

### Chest Bone (BMB)

**Magic**: `00 2C 36 14 9B` (5 bytes)

Same 36-byte payload layout as head bone but for chest.

## Ammo Events

**Pattern**: `77 CA 96 DE` (4 bytes)

```
  -8..-5   Weapon entity ID (u32 LE, F0-prefix)
  -4..-1   Zero padding (00 00 00 00)
   0.. 3   Pattern [77 CA 96 DE]
   4+       TLV fields (repeating)
```

**TLV field format**: `[04] [value u32 LE] [22 or 23] [hash u32 LE]` (10 bytes each)

**Property hashes**:
| Hash | Meaning |
|------|---------|
| `0x29C80A40` | Current magazine ammo (decrements per shot) |
| `0x3E6D5B6D` | Loaded ammo (magazine + chambered round) |
| `0xAA4BBC34` | Reserve ammo pool |
| `0x0A44F556` | Small counter (init events) |
| `0x219E95DE` | Grand total (reserve + loaded) |
| `0x653E26DD` | Running total remaining |

## Weapon Init Blocks

**Pattern**: `5F 85 CC 85` (last 5% of binary)

Two sub-types:
- **Type A**: byte at +4 = `0x1A`, weapon EID at +9
- **Type B**: byte at +4 = `0x22` and +5 = `0x14`, weapon EID at +16

Weapon EIDs are F0-prefix, matching ammo events. Two team clusters: DEF at ~98.5%, ATK at ~99%.

## Equipment Loadout

16-byte records in the header area (first 25% of file):

```
[GameID u64 LE] [auxHash u32 LE] [category u32 LE]
```

Categories:
- `0x16` / `0x18`: operator (solo/ranked)
- `0x0A`: weapon
- `0x03`: gadget

### Slot auxHashes (CRC32 of slot name)

| auxHash (decimal) | auxHash (hex) | Slot |
|-------------------|---------------|------|
| `3268402276`      | `0xC2C7C124`  | PrimaryWeapon |
| `1696241262`      | `0x651572EE`  | MeleeWeapon (session-variable IDs) |
| `1893246388`      | `0x70DAB934`  | SecondaryWeapon (canonical IDs — preferred) |
| `2606078005`      | `0x9B559835`  | Reinforcement (defender wall reinforcement) |
| `2947831256`      | `0xAFB455D8`  | SecondaryGadget (universal throwable / utility) |
| `497232904`       | `0x1DA32C08`  | **OperatorGadget** (signature gadget — Bandit's SHOCK WIRE, etc.) |

Per-player loadout block ≈ 506 bytes; defender blocks come first, then attackers. The
`MeleeWeapon` slot stores session-variable weapon IDs (`0x38C8D6___` family) that change
per match. The `SecondaryWeapon` slot stores canonical hashes that resolve to weapon names
via `gameItemNames` — always prefer `SecondaryWeapon` over `MeleeWeapon`.

### Ammo Hash2 slot identifiers

The Hash2 field in `AmmoUpdate` records identifies which slot the update is for:

| Hash2 | Slot |
|-------|------|
| `0x00000000` | Primary weapon |
| `0x29C80A40` | Secondary weapon |
| `0xAA4BBC34` | Throwable / grenade |
| `0x653E26DD` | Operator-specific gadget |

## Timer Ticks

**Pattern**: `1F 07 EF C9 04 [seconds u32 LE]`

Countdown timer (seconds remaining). Prep phase: counts down ~44s. Action phase: counts down ~180s.

## Kill Events

**Kill indicator**: `22 D9 13 3C BA` (5 bytes)

```
+0         Attacker username length (1 byte)
+1..+N     Attacker username (ASCII)
+N+1..+N+15  Skip 15 bytes
+N+16      Target username length (1 byte)
+N+17..+M  Target username (ASCII)
+M+1..+M+56  Skip 56 bytes
+M+57      Headshot flag (0 or 1)
```

**DBNO marker**: `22 96 E2 29 7F` — appears within ±256 bytes of a kill indicator when the kill was a finish (DBNO → confirm). Window was widened from 70 to 256 bytes for Y11S1+ which inserts additional TLV fields between markers.

### Kill TLV Hashes

After the kill indicator, TLV fields with these hashes:

| Hash | Meaning |
|------|---------|
| `0xD13DA88D` | **Attacker team index + 1** (u32) — `1` for team 0, `2` for team 1 (decoded R06) |
| `0x3187B853` | **Victim team index + 1** (u32) (decoded R06) |
| `0x70DE98C1` | Killer team index (u32: 1 or 2) — duplicate of `0xD13DA88D` |
| `0x700F19AC` | Target username (string) |
| `0x507B2E78` | Target team index (u32) |
| `0x4EA45BC3` | **Headshot flag (u8: 0x01)** — verified across all R06 kills |
| `0x65DD6CF8` | **Canonical weapon hash for this kill** (u64) — consistent across kills with same weapon |
| `0x41B24805` | Cumulative kill count in round (u32) |
| `0x7F29E296` | DBNO marker (f32: 5.0 or 10.0) |
| `0xF32D7DF5` | DBNO finish flag (byte) |
| `0x56B4E07A` | DBNO knocker team (u32) |
| `0xD241FB6C` | DBNO finisher team (u32) |

### Extended Kill TLVs

Additional TLV fields. Present from Y9S2+ (one or two TLVs absent in Y9S1). Scanned in a 256-byte window around each kill marker. Each TLV: marker (`0x22` or `0x23`) + hash u32 LE + type byte + value. Type bytes: `0x01`=u8, `0x04`=u32, `0x08`=u64.

Distributions verified across **402 kills** from **85 replays** spanning Y9S1, Y9S2_Beta01, Y10S3_Alpha02, Y11S1_Alpha03.

| Hash | Type | Decoded Meaning |
|------|------|-----------------|
| `0x790009E3` | u64 | Reserved sentinel — **always `0xFFFFFFFFFFFFFFFF`** when present (Y9S2+); absent (zero) in Y9S1 |
| `0x8F0292B5` | u8  | Reserved — always `0` across all 402 kills |
| `0x5BC4BC84` | u32 | **Y11-introduced kill metadata** — pre-Y11 always `1`; in Y11S1 splits 61%/39% between `1`/`2`. Specific semantics undecoded — possible candidates: wallbang flag, DBNO-finish flag, marked-target kill, or new Y11 mechanic |
| `0x37BF3E90` | u32 | Always `1` — kill-type marker |
| `0xD13DA88D` | u32 | **AttackerTeam + 1** (`1`=team 0, `2`=team 1) |
| `0x3187B853` | u32 | **VictimTeam + 1** |
| `0x0B64ADA5` | u32 | Reserved — always `0` across all 402 kills |

### Sub-stream Kill TLVs (decoded from duplicate copies)

Each kill is replicated 1–4 times in the binary by different replay sub-streams.
The hashes below live in the **secondary** copies (the first copy has only the
"primary" TLVs above), so a wider scan window is needed: `extraTLVWindowBytes =
2048` captures them while staying inside one round's worth of replication data.
With that window, coverage on a 79-replay R06 corpus is 50–70 % depending on the
hash. When absent the field is left at its zero value.

| Hash | Type | Decoded Meaning |
|------|------|-----------------|
| `0x6C463718` | u32 | **Round timestamp in milliseconds** since round start. Verified by matching against `MatchFeedback.TimeInSeconds` — `roundTimeMs / 1000 ≈ feedback time` within ±1 s. Far more accurate than the offset-based `timeSecs` on replays where the offset interpolation drifts. |
| `0xC9527BDD` | f32 | **Damage of the killing bullet** normalised to victim max-health (range `[0, 1]`). Strong headshot correlation: mean `0.63` for HS kills vs `0.33` for body kills (n = 95). HS one-shot kills cluster near `1.0`. |
| `0xFB9DBF08` | f32 | **Range / damage-falloff factor** for the killing shot (range `[0, 1]`, weapon-dependent). Same weapon clusters tightly around its weapon-class value; a shotgun-class weapon stays near `0.05`, a DMR-class weapon near `0.55`. |
| `0xA5F688E7` | u32 | **Hit-zone enum** for the killing shot (observed values `0/1/2/3/4`). Value `4` dominates HS kills (48 vs 30 non-HS), value `2` is more common on body kills. Higher values lean toward upper body / head; full mapping still being decoded. |
| `0x31C825EC` | u32 | Victim-pawn enum (1..5, entity-scoped via `0x23` marker). Skews higher on HS kills (val=3 appears 5× more often on HS). |
| `0xB53D3EFA` | u32 | Victim-pawn enum (1..4, entity-scoped). Semantics undecoded. |
| `0xA374F4B6` | u32 | Victim-pawn counter — 85 distinct values across 172 occurrences, range `0..249`; likely a tick or shot counter. |
| `0x488A15F4` | u64 | Victim-pawn entity ID (~`1.25e11`); stable per match → profile/session ID. |

#### Constant sub-stream markers

These hashes appear consistently near kill records but never carry a meaningful
value — they are present/absent flags marking which sub-stream wrote the
duplicate. They are documented for completeness; the tool does not surface them.

| Hash | Type | Value |
|------|------|-------|
| `0xD961FC99` | u8 | always `1` (~68 % of kills) |
| `0x10145FEA` | u8 | always `1` (~71 %) |
| `0x27B31851` | u8 | always `1` (~68 %) |
| `0x4766DA41` | u8 | always `1` (~9 %) |
| `0x1850F906` | u8 | always `1` (~4 %) |
| `0x80D18F8B` | u8 | always `1` (~4 %) |
| `0x5016F6EB` | u64 | always `0` (~48 %) |

## Health Property

**Hash**: `0x4171D3C3` (in post-80% region)

Record format: `[ref8 8B] [hash 4B] [value f32 4B]` = 16 bytes

Values: `0.0` (dead), `100.0` (full), intermediates = damage taken.

**Co-located properties** (same ref8 block):
| Hash | Meaning |
|------|---------|
| `0x848F67CF` | Time-related float |
| `0xF634093A` | Hit/tick counter |
| `0x475BB68B` | Damage rate (0.067=DoT, 0.133=bullets) |
| `0xC2D846F8` | Max health (0 or 100) |

## Scoreboard

| Pattern | Field |
|---------|-------|
| `EC DA 4F 80` | Cumulative score (u32) |
| `1C D2 B1 9D` | Kill count (u32) |
| `4D 73 7F 9E` | Assist count (u32) |

Offset -18 from pattern: marker `0x23`; offset -17 to -14: 4-byte player ID.

## Camera Frames

**Signature**: `[0xa5b2f3a5] [0x01] [varies 4B] [0x02] [qx qy qz qw]`

Entity ID: scan forward after quaternion for archetype `0xFE857360`, entity ref at -12 from archetype.

## Game Actions

| Pattern (10 bytes) | Action |
|---|---|
| `46 00 00 00 00 00 00 00 04 35` | Reinforce complete |
| `50 00 00 00 00 00 00 00 04 3F` | Gadget deployed |

## Defuser Timer Ticks

**Pattern**: `22 A9 C8 58 D9` (defuser timer event)

Each occurrence is a frame of plant/defuse progress. The library now emits one `DefuserTick`
per call with state derived from `r.planted` and `r.defuserDisabling`:

| State | Condition |
|-------|-----------|
| `planting` | `!r.planted` |
| `disabling` | `r.planted && (r.defuserDisabling \|\| timer increased vs prev)` |
| `planted_idle` | `r.planted` and not disabling |

Tick fields: `timeInSeconds`, `time` (mm:ss), `rawValue` (current timer), `prevValue` (previous timer), `state`.

## Other Patterns

| Pattern | Purpose |
|---------|---------|
| `22 07 94 9B DC` | Player data record |
| `22 A9 26 0B E4` | Attacker operator swap |
| `AF 98 99 CA` | Spawn point data |
| `59 34 E5 8B 04` | Match feedback (kill/DBNO/death) |
| `22 A9 C8 58 D9` | Defuser timer (per-frame tick) |
