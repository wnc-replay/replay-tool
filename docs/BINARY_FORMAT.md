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
Offset from pattern (i = first byte of pattern):
  -12..-9   Entity ref low 32b (u32 LE, F0-prefix for game entities)
   -8..-5   Entity ref high 32b — always 0 (the engine's u64 id is sparse)
   -4..-1   Packet size (u32 LE)
    0.. 3   Pattern [60 73 85 FE]
    4.. 5   Type field (u16 LE, bitfield)
   +6..+7   Echo of entity ref low 16b
    8+       Payload
```

> **NOTE**: an earlier revision of this doc placed the entity ref at `-16..-13`
> and the packet size at `-8..-5`. Those positions hold zero / arbitrary
> bytes (verified on every replay in the corpus); the real layout matches
> `dissect/movement.go` (`r.b[i-12:i-8]` for ref, `r.b[i-4:i]` for size).

**Type field bits**:
- Bit 7 of byte[0] (`& 0x80`): position data present (3× f32 XYZ at payload start)
- Type `0x0880`: drone view marker (no position data)
- `0x03xx` types: full quaternion in trail (4× f32 at +4 after XYZ)

**Position types** (with XYZ): `0x03B8`, `0x01B0`, `0x01B8`, `0x01C0`, `0x1Fxx`
**Property-only types** (no XYZ): `0x0440`, `0x0130`, `0x0420`, `0x0630`

### SPAWN Records

**Archetype**: `0xFE857361` (LE bytes: `61 73 85 FE`)

```
Offset from pattern (i = first byte of pattern):
  -12..-9   Entity ref low 32b (u32 LE)
   -8..-5   Entity ref high 32b — always 0
   -4..-1   Counter (u32 LE)
    0.. 3   Pattern [61 73 85 FE]
   +4..+7   Echo of entity ref low 32b
   +8..+11  Always 0
   +60..+63 hashA (u32 LE) — gadget/sub-type identifier (not present on all counters)
   +64..+67 hashB (u32 LE) — paired with hashA for some counter=146 entities
```

> **NOTE**: an earlier revision of this doc placed the counter at `+8..+9` as
> a `u16`. Those bytes are always zero (high 16 bits of the entity ref echo);
> the real counter is the u32 at `-4..-1`, matching `dissect/movement.go`
> (`r.b[i-4:i]`). Counters use the full 32-bit width — values up to 494
> appear in normal replays, so a `u16` read happens to truncate to the same
> low byte but mis-types the field.

**Counter values** observed across 79 Y11S1 R06 replays (31 038 SPAWN
records, by descending population):

| Counter | Per-match avg | Mapped meaning | Source |
|--------:|--------------:|----------------|--------|
| `98`  | 54.7 | projectile / VFX (paired with `266`) | distribution analysis |
| `94`  | 30.6 | unknown — not classified | – |
| `142` | 44.5 | secondary gadget (impact, claymore, jammer, …) | `dissect.classifySpawnCounters` |
| `130` | 43.9 | barricade (door / window / hatch reinforcement) | `dissect.classifySpawnCounters` |
| `146` | 35.4 | deployed gadget | `dissect.classifySpawnCounters` |
| `138` | 33.8 | primary weapon (or Azami Kiba, by spawn hash) | `dissect.classifySpawnCounters` |
| `126` | 33.5 | Alibi Prisma — and other counter-126 carriers | `dissect.classifySpawnCounters` |
| `254` | 29.8 | secondary weapon | `dissect.classifySpawnCounters` |
| `122` | 22.2 | unknown — short hashA values (`0x0000XXXX`) suggest spawn-point indices | – |
| `266` | 20.0 | projectile / VFX phase 2 (same hashA as `98`) | distribution analysis |
| `154` | 15.1 | player-controlled drone | `dissect.classifySpawnCounters` |
| `494` | 10.0 | player entity (one per loadout slot) | `dissect.classifySpawnCounters` |
| `150` | 5.7  | deployable secondary equipment (shield, …) | `dissect.classifySpawnCounters` |
| `158`/`162`/`110`/`90` | <0.1 | rare — undecoded | – |

#### Spawn hashA distribution per counter

`hashA = u32 LE at pattern + 60`. Distinct hashAs (>1 % of total) seen on
the 79-replay corpus:

| Counter | hashA | % of counter | Mapped (lib) |
|--------:|-------|-------------:|--------------|
| 154 (drone) | `0xF6E54772` | 98.9 | **not mapped** — universal attacker drone |
| 154 | `0x1CA56E9A` | 0.8 | shared family (Mira/shield) — likely Twitch shock-drone |
| 154 | `0x3413CEF7` | 0.3 | rare drone variant |
| 146 (gadget) | `0x133B51F7` | 61.5 | **not mapped** |
| 146 | `0xF69084B2` | 28.8 | **not mapped** |
| 146 | `0x133B519A` | 5.4 | partial — pairs with hashB give Kona / Banshee / ADS / Mira |
| 146 | `0x59F6D09A` | 2.8 | **not mapped** |
| 130 (barricade) | `0x1CA56E9A` | 66.5 | barricade family (also Mira/shield) |
| 130 | `0x133B51F7` | 28.1 | **not mapped** — likely reinforced wall |
| 130 | `0x133B519A` | 4.0 | Mira/Castle family |
| 142 (sec gadget) | `0x2D1E3A9A` | 4.0 | Mute Jammer ✓ |
| 142 | `0xD2F8F39A` | 2.9 | Bandit Battery ✓ |
| 142 | `0x2D20B99A` | 2.6 | **not mapped** |
| 142 | `0xFC72B39A` | 2.4 | Goyo Canister ✓ |
| 142 | `0x2D1E3B16` | 1.6 | Nitro Cell C4 ✓ |
| 138 (primary weapon) | `0x00005EFC` etc. | 28+ | **not mapped** — short IDs (likely weapon-class index, not a hash) |
| 138 | `0x9B72AE9A` | 3.2 | Azami Kiba ✓ |
| 254 (sec weapon) | `0xD65547F7` etc. | varied | **not mapped** — ~30 distinct IDs (one per pistol model) |
| 150 (deployable) | `0x86604C72` | 25.1 | **not mapped** |
| 150 | `0x133B51F7` | 21.5 | **not mapped** |
| 150 | `0x1CA56E9A` | 16.3 | Deployable Shield ✓ |
| 98 (projectile) | `0xA9CE56F7` | 98.8 | **not mapped** — universal projectile |
| 266 (projectile p2) | `0xA9CE56F7` | 99.2 | **not mapped** — same as counter 98 |

> **`__9A` vs `__16` family pattern** (counter=142): every gadget hash with
> the `9A` low byte is a *placed* utility (jammer, battery, canister, kiba);
> every hash with the `16` low byte is a *thrown* explosive (C4 = `0x2D1E3B16`,
> plus 7 unmapped `XXXX_XX16` siblings). The low byte appears to be a class
> tag in the underlying name hash — useful as a fallback classifier when
> the exact gadget is not yet identified.

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

## Per-entity TLV catalogue (0x23 marker)

Beside the kill-record TLVs documented above, the binary stream carries a
large catalogue of per-entity property updates encoded as
`0x23 [ref8] [hash u32 LE] [type] [value]`. The entity ref is the F0-prefix
id of the SPAWN entity (or a sub-entity that does not get its own SPAWN —
this is common for world-state and animation sub-objects).

The table below lists every hash that occurred more than 1 000 times across
the 79-replay corpus, with the value type observed and our best current
interpretation. Items marked **probable** match a clear value pattern but
have not been cross-validated against game behaviour; treat them as hints,
not contracts.

| Hash | Type | Occurrences | Distinct entities | Probable meaning |
|------|------|------------:|------------------:|------------------|
| `0xA374F4B6` | u32 | 697 184 | 1 369 | Per-entity tick / sequence counter (values 0..249, monotone in time) — **probable** |
| `0x6C463718` | u32 | 337 182 | 79 (= one per replay) | **Round timestamp ms** — already in `BinaryMatchEvent.RoundTimeMs` (verified against `MatchFeedback.TimeInSeconds`) |
| `0xA80080B0` | u8 | 76 146 | 3 294 | Boolean flag (~42 entities/match) — active/inactive state — **probable** |
| `0xD373835C` | f32 | 68 591 | 942 | Animation lerp 0..1 — **probable** |
| `0x54E5D055` | f32 | 69 725 | 138 | Animation lerp 0..1 — **probable** |
| `0xCA9998AF` | u64 | 27 515 | 1 667 | Sentinel `0xFFFFFFFFFFFFFFFF` — points to the SPAWN system (CRC32 of the SPAWN pattern) |
| `0xC13FD73B` | u8 | 25 454 | 4 810 | Boolean flag (~61 entities/match) — visibility / replication-side — **probable** |
| `0xC1406A0D` | f32 | 22 898 | 3 766 | Values cluster 88..110, sometimes 0; **not** entity HP (max > 100). Possibly speed scalar or animation timer — **uncertain** |
| `0x0AD3AA3E` | u64 | 14 317 | 2 850 | Entity ref (parent / owner) — **probable** |
| `0x6252FDFF` | u32 | 13 122 | 6 382 | Small enum 0/1/2 — **probable** state field |
| `0xC9EF071F` | u32 | 12 944 | 79 (= one per replay) | Per-replay tick counter (one entity = world state) — **probable** server tick |
| `0x2477AC66` | u32 | 12 819 | 1 398 | Small enum 0..3 — **probable** |
| `0xEC0D4FF6` | f32 | 10 477 | 20 (= 1 entity in 20/79 replays) | f32 0..1 lerp — only present in some rounds; candidates: defuser progress, drone deploy animation — **uncertain** |
| `0xD48DDCA4` | u8 | 10 065 | 789 | Boolean flag — **probable** |
| `0xA436B096` | f32 | 9 557 | 73 | Animation lerp 0..1 — **probable** |
| `0x88BE9E0E` | u64 | 8 655 | 7 482 | "Session item id" — values fall into families (`0x1F1E397...`, `0x516BC...`) but **none match `gameItemNames`**. Probably session-scoped instance IDs — **uncertain** |
| `0xAFB7ACBC` | u8 | 8 363 | 1 679 | Always `1` — sub-stream presence marker — **probable** |
| `0x804FDAEC` | u32 | 6 837 | 948 | Small counter — **uncertain** |
| `0xD55F88F8` | u8 | 6 508 | 1 001 | Boolean flag — **probable** |
| `0x78B46D4F` | u8 | 6 175 | 941 | Boolean flag — **probable** |
| `0x4E254E7C` | u64 | 26 | 21 | **Operator role-portrait id** — values match the `roleImage` field of player-header records (e.g. `375628889901` = Bal0uX' Azami portrait) — **probable** |

> Aggregated from `cmd/entprops23` over 79 R06 / Y11S1 replays. Type
> mixing (u8 + u32 on the same hash) was treated as separate entries; only
> single-type hashes are listed here.

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
