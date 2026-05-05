// Package analysis provides comprehensive binary analysis of R6 Siege .rec replay files.
// It combines position extraction, ammo/weapon tracking, bone data, health monitoring,
// entity classification, camera frames, timer ticks, shot reconstruction, and game events.
package analysis

// RoundAnalysis is the top-level output for a fully analyzed round.
type RoundAnalysis struct {
	// Player entities with full position/rotation history
	Players []PlayerTrack `json:"players"`
	// Non-player tracked entities (drones, cameras, gadgets, projectiles, barricades)
	Entities []EntityTrack `json:"entities,omitempty"`
	// Per-player weapon/ammo tracking
	Weapons map[int]*PlayerWeapons `json:"weapons,omitempty"`
	// Per-player equipment loadout (weapon + gadget names)
	Loadouts []PlayerLoadout `json:"loadouts,omitempty"`
	// Reconstructed shot events (position + aim at moment of fire)
	Shots []ShotEvent `json:"shots,omitempty"`
	// Health state changes per player
	HealthUpdates []HealthUpdate `json:"healthUpdates,omitempty"`
	// Timer ticks and round phases
	TimerTicks  []TimerTick  `json:"timerTicks,omitempty"`
	TimerPhases []TimerPhase `json:"timerPhases,omitempty"`
	// Kill/DBNO events parsed directly from binary
	BinaryFeedback []BinaryMatchEvent `json:"binaryFeedback,omitempty"`
	// Game actions (reinforce, gadget deploy) from binary scan
	GameActions []GameAction `json:"gameActions,omitempty"`
	// Game actions from the dissect library (more reliable for newer seasons)
	LibraryGameActions []LibraryGameAction `json:"libraryGameActions,omitempty"`
	// Camera look-direction frames from the dissect library
	CameraFrames []LibraryCameraFrame `json:"cameraFrames,omitempty"`
	// Shot events reconstructed by the dissect library
	LibraryShots []LibraryShotEntry `json:"libraryShots,omitempty"`
	// Raw ammo state updates from the dissect library
	LibraryAmmoUpdates []LibraryAmmoUpdate `json:"libraryAmmoUpdates,omitempty"`
	// Mid-round attacker operator swap events
	OperatorSwaps []OperatorSwapEvent `json:"operatorSwaps,omitempty"`
	// Player score delta events (points earned per action)
	ScoreUpdates []ScoreUpdateEvent `json:"scoreUpdates,omitempty"`
	// Drone connect/disconnect lifecycle events
	DroneEvents []DroneEventEntry `json:"droneEvents,omitempty"`
	// Last significant movement positions for players who died
	DeathTimings []DeathTimingEntry `json:"deathTimings,omitempty"`
	// Destruction events (drone/gadget/barricade HP reached zero)
	DestructionEvents []DestructionEvent `json:"destructionEvents,omitempty"`
	// Revive events (downed player brought back)
	ReviveEvents []ReviveEvent `json:"reviveEvents,omitempty"`
	// Equipment switch events (player changed active weapon)
	EquipmentSwitches []EquipmentSwitchEvent `json:"equipmentSwitches,omitempty"`
	// Timed game events for visualization
	GameEvents []GameEvent `json:"gameEvents,omitempty"`
	// Recording player index
	RecordingPlayer int `json:"recordingPlayer"`
	// Round duration in seconds
	RoundDuration float32 `json:"roundDuration"`
}

// ---------- Position & Movement ----------

// PosFrame is one position+rotation sample for an entity.
type PosFrame struct {
	Offset   int64   `json:"offset"`
	EntityID uint32  `json:"entityId"`
	X        float32 `json:"x"`
	Y        float32 `json:"y"`
	Z        float32 `json:"z"`
	Qx       float32 `json:"qx"`
	Qy       float32 `json:"qy"`
	Qz       float32 `json:"qz"`
	Qw       float32 `json:"qw"`
	YawDeg   float32 `json:"yawDeg"`
	PitchDeg float32 `json:"pitchDeg"`
	TimeSecs float32 `json:"timeSecs,omitempty"`
	IsCamera bool    `json:"isCamera,omitempty"`
	Stance   string  `json:"stance,omitempty"` // "standing", "crouching", "prone"
	// Bone data (head)
	HeadOffX float32 `json:"hoX,omitempty"`
	HeadOffY float32 `json:"hoY,omitempty"`
	HeadOffZ float32 `json:"hoZ,omitempty"`
	HeadQX   float32 `json:"hqX,omitempty"`
	HeadQY   float32 `json:"hqY,omitempty"`
	HeadQZ   float32 `json:"hqZ,omitempty"`
	HeadQW   float32 `json:"hqW,omitempty"`
	// Bone data (chest)
	ChestOffX float32 `json:"coX,omitempty"`
	ChestOffY float32 `json:"coY,omitempty"`
	ChestOffZ float32 `json:"coZ,omitempty"`
	ChestQX   float32 `json:"cqX,omitempty"`
	ChestQY   float32 `json:"cqY,omitempty"`
	ChestQZ   float32 `json:"cqZ,omitempty"`
	ChestQW   float32 `json:"cqW,omitempty"`
	// World-space head aim (body_quat × head_bone_quat)
	HeadAimYaw   float32 `json:"headAimYaw,omitempty"`
	HeadAimPitch float32 `json:"headAimPitch,omitempty"`
}

// PlayerTrack is a player entity's full position history with metadata.
type PlayerTrack struct {
	EntityID    uint32     `json:"entityId"`
	PlayerIndex int        `json:"playerIndex"`
	Username    string     `json:"username"`
	Operator    string     `json:"operator"`
	TeamIndex   int        `json:"teamIndex"`
	IsAttacker  bool       `json:"isAttacker"`
	KilledAt    float32    `json:"killedAtSecs,omitempty"`
	Frames      []PosFrame `json:"frames"`
}

// EntityTrack is a non-player entity's position history with classification.
type EntityTrack struct {
	EntityID       uint32        `json:"entityId"`
	EntityHex      string        `json:"entityHex"`
	Type           string        `json:"type"` // "drone", "camera", "gadget", "projectile", "barricade", "weapon"
	GadgetType     string        `json:"gadgetType,omitempty"`
	ProjectileType string        `json:"projectileType,omitempty"`
	BarricadeType  string        `json:"barricadeType,omitempty"`
	OwnerLabel     string        `json:"ownerLabel,omitempty"`
	TeamIndex      int           `json:"teamIndex"`
	SpawnCounter   uint32        `json:"spawnCounter,omitempty"`
	HealthEvents   []HealthEvent `json:"healthEvents,omitempty"`
	Frames         []PosFrame    `json:"frames"`
}

// ---------- Ammo & Weapons ----------

// AmmoEvent is a single ammo state update from the binary.
type AmmoEvent struct {
	Offset    int64       `json:"offset"`
	WeaponEID uint32      `json:"weaponEid"`
	TimeSecs  float32     `json:"timeSecs,omitempty"`
	Fields    []AmmoField `json:"fields"`
}

// AmmoField is one TLV field within an ammo event.
type AmmoField struct {
	Value uint32 `json:"value"`
	Hash  uint32 `json:"hash"`
}

// Ammo property hashes (little-endian u32).
const (
	AmmoHashCurrentMag   = 0x29C80A40 // current ammo in magazine (decrements per shot)
	AmmoHashLoadedAmmo   = 0x3E6D5B6D // magazine + chambered round
	AmmoHashReservePool  = 0xAA4BBC34 // reserve ammo (not in magazine)
	AmmoHashSmallCounter = 0x0A44F556 // small counter (init events)
	AmmoHashGrandTotal   = 0x219E95DE // reserve + loaded
	AmmoHashRunningTotal = 0x653E26DD // running total remaining
)

// WeaponAmmoInfo holds aggregated ammo data for one weapon entity.
type WeaponAmmoInfo struct {
	WeaponEID      uint32 `json:"weaponEid"`
	PlayerIndex    int    `json:"playerIndex"`
	IsPrimary      bool   `json:"isPrimary"`
	WeaponName     string `json:"weaponName,omitempty"`
	WeaponCategory string `json:"weaponCategory,omitempty"`
	MagazineSize   int    `json:"magazineSize"`
	InitialAmmo    int    `json:"initialAmmo"`
	FinalAmmo      int    `json:"finalAmmo"`
	ShotsFired     int    `json:"shotsFired"`
	TotalEvents    int    `json:"totalEvents"`
}

// PlayerWeapons holds all weapon tracking for one player.
type PlayerWeapons struct {
	PlayerIndex int              `json:"playerIndex"`
	Primary     *WeaponAmmoInfo  `json:"primary,omitempty"`
	Secondary   *WeaponAmmoInfo  `json:"secondary,omitempty"`
	AllWeapons  []WeaponAmmoInfo `json:"allWeapons,omitempty"`
}

// ---------- Equipment Loadout ----------

// LoadoutItem is one equipped item (weapon, gadget, operator).
type LoadoutItem struct {
	GameID   uint64 `json:"gameId"`
	AuxHash  uint32 `json:"auxHash,omitempty"`
	Name     string `json:"name"`
	Category int    `json:"category"` // 10=weapon, 3=gadget, 22/24=operator
}

// PlayerLoadout holds the full equipment loadout for one player.
type PlayerLoadout struct {
	PlayerIndex     int         `json:"playerIndex"`
	OperatorID      uint64      `json:"operatorId"`
	OperatorName    string      `json:"operatorName"`
	PrimaryWeapon   LoadoutItem `json:"primaryWeapon"`
	SecondaryWeapon LoadoutItem `json:"secondaryWeapon"`
	PrimaryGadget   LoadoutItem `json:"primaryGadget"`
	SecondaryGadget LoadoutItem `json:"secondaryGadget"`
}

// ---------- Shots ----------

// ShotEvent is a single shot fired, with position and aim direction.
type ShotEvent struct {
	PlayerIndex int     `json:"playerIndex"`
	X           float32 `json:"x"`
	Y           float32 `json:"y"`
	Z           float32 `json:"z"`
	YawDeg      float32 `json:"yawDeg"`
	PitchDeg    float32 `json:"pitchDeg"`
	HeadQX      float32 `json:"hqX,omitempty"`
	HeadQY      float32 `json:"hqY,omitempty"`
	HeadQZ      float32 `json:"hqZ,omitempty"`
	HeadQW      float32 `json:"hqW,omitempty"`
	TimeSecs    float64 `json:"timeSecs"`
	Seq         int     `json:"seq"`
}

// ---------- Health ----------

// HealthUpdate is a health state change for a player or entity.
type HealthUpdate struct {
	PlayerIndex int     `json:"playerIndex"`
	Health      float32 `json:"health"`
	EntityRef   uint32  `json:"entityRef,omitempty"` // non-zero for non-player entity health events
	TimeSecs    float32 `json:"timeSecs,omitempty"`
	BinOffset   int     `json:"binOffset"`
}

// HealthEvent is a health change for any entity (player, drone, gadget).
type HealthEvent struct {
	Offset   int64   `json:"offset"`
	HP       int     `json:"hp"`
	TimeSecs float32 `json:"timeSecs,omitempty"`
	EntityID uint32  `json:"entityId,omitempty"`
}

// ---------- Timer ----------

// TimerTick is a single round-timer tick from the binary.
type TimerTick struct {
	Offset  int64   `json:"offset"`
	Seconds float32 `json:"seconds"`
}

// TimerPhase is a detected round phase (prep or action).
type TimerPhase struct {
	Name     string  `json:"name"` // "prep" or "action"
	StartSec float32 `json:"startSec"`
	EndSec   float32 `json:"endSec"`
	Duration float32 `json:"duration"`
}

// ---------- Events ----------

// BinaryMatchEvent is a kill/death/DBNO parsed from binary.
type BinaryMatchEvent struct {
	Offset   int64   `json:"offset"`
	Type     string  `json:"type"` // "kill", "death", "dbno"
	Attacker string  `json:"attacker"`
	Target   string  `json:"target"`
	Headshot bool    `json:"headshot"`
	TimeSecs float64 `json:"timeSecs,omitempty"`
}

// LibraryCameraFrame is a camera look-direction sample from the dissect library.
type LibraryCameraFrame struct {
	PlayerIndex int     `json:"playerIndex"`
	Qx          float32 `json:"qx"`
	Qy          float32 `json:"qy"`
	Qz          float32 `json:"qz"`
	Qw          float32 `json:"qw"`
	YawDeg      float32 `json:"yawDeg"`
	PitchDeg    float32 `json:"pitchDeg"`
	TimeSecs    float32 `json:"timeSecs,omitempty"`
	BinOffset   int     `json:"binOffset"`
}

// LibraryShotEntry is a shot event reconstructed by the dissect library.
type LibraryShotEntry struct {
	PlayerIndex   int     `json:"playerIndex"`
	X             float32 `json:"x"`
	Y             float32 `json:"y"`
	Z             float32 `json:"z"`
	Yaw           float32 `json:"yaw"`
	Pitch         float32 `json:"pitch"`
	HeadQX        float32 `json:"hqX,omitempty"`
	HeadQY        float32 `json:"hqY,omitempty"`
	HeadQZ        float32 `json:"hqZ,omitempty"`
	HeadQW        float32 `json:"hqW,omitempty"`
	TimeSecs      float64 `json:"timeSecs"`
	Seq           int     `json:"seq"`
}

// LibraryAmmoUpdate is a raw ammo state update from the dissect library.
type LibraryAmmoUpdate struct {
	PlayerIndex int     `json:"playerIndex"`
	Available   uint32  `json:"available"`
	Capacity    uint32  `json:"capacity"`
	TimeSecs    float32 `json:"timeSecs,omitempty"`
	BinOffset   int     `json:"binOffset"`
}

// LibraryGameAction is a game action event from the dissect library.
type LibraryGameAction struct {
	Type      string  `json:"type"`
	TimeSecs  float32 `json:"timeSecs,omitempty"`
	BinOffset int     `json:"binOffset"`
}

// GameAction is a detected game action (reinforce, gadget deploy).
type GameAction struct {
	Type     string  `json:"type"`
	TimeSecs float64 `json:"timeSecs"`
	Offset   int     `json:"offset"`
}

// ScoreUpdateEvent is a single score delta event for a player.
type ScoreUpdateEvent struct {
	PlayerIndex int     `json:"playerIndex"`
	Username    string  `json:"username"`
	PrevScore   int     `json:"prevScore"`
	NewScore    int     `json:"newScore"`
	Delta       int     `json:"delta"`
	TimeSecs    float32 `json:"timeSecs,omitempty"`
	BinOffset   int     `json:"binOffset"`
}

// DeathTimingEntry records the last significant movement position for a player
// who died, estimated from position stream data.
type DeathTimingEntry struct {
	PlayerIndex         int     `json:"playerIndex"`
	LastMovementSeq     int     `json:"lastMovementSeq"`
	LastMovementTimeSec float64 `json:"lastMovementTimeSec"` // countdown seconds remaining (from game timer)
	LastX               float32 `json:"lastX"`
	LastY               float32 `json:"lastY"`
	LastZ               float32 `json:"lastZ"`
}

// DroneEventEntry is a drone connect/disconnect lifecycle event.
type DroneEventEntry struct {
	PlayerRef uint32  `json:"playerRef"`
	DroneRef  uint32  `json:"droneRef"`
	Seq       int     `json:"seq"`
	Connect   bool    `json:"connect"`
	TimeSecs  float32 `json:"timeSecs,omitempty"`
}

// OperatorSwapEvent is a detected mid-round attacker operator change.
type OperatorSwapEvent struct {
	PlayerIndex  int     `json:"playerIndex"`
	Username     string  `json:"username,omitempty"`
	FromOperator string  `json:"fromOperator,omitempty"`
	ToOperator   string  `json:"toOperator"`
	Offset       int64   `json:"offset,omitempty"`
	TimeSecs     float32 `json:"timeSecs,omitempty"`
}

// GameEvent is a timed event for visualization (kill feed, phase changes).
type GameEvent struct {
	Type     string  `json:"type"` // "kill", "death", "dbno", "action_start", "round_end"
	TimeSecs float32 `json:"timeSecs"`
	Text     string  `json:"text"`
	Headshot bool    `json:"headshot,omitempty"`
}

// DestructionEvent is emitted when a non-player entity's health reaches zero.
type DestructionEvent struct {
	EntityID   uint32  `json:"entityId"`
	EntityHex  string  `json:"entityHex,omitempty"`
	EntityType string  `json:"entityType"` // "drone", "barricade", "gadget", "projectile", "unknown"
	GadgetType string  `json:"gadgetType,omitempty"`
	TimeSecs   float32 `json:"timeSecs,omitempty"`
	BinOffset  int64   `json:"binOffset"`
}

// ReviveEvent is emitted when a downed player is revived (HP rises from near-zero).
type ReviveEvent struct {
	PlayerIndex int     `json:"playerIndex"`
	TimeSecs    float32 `json:"timeSecs,omitempty"`
	BinOffset   int     `json:"binOffset"`
}

// EquipmentSwitchEvent is emitted when a player changes their active weapon.
type EquipmentSwitchEvent struct {
	PlayerIndex  int     `json:"playerIndex"`
	FromWeaponID uint32  `json:"fromWeaponId,omitempty"`
	ToWeaponID   uint32  `json:"toWeaponId,omitempty"`
	IsPrimaryNow bool    `json:"isPrimaryNow"`
	TimeSecs     float32 `json:"timeSecs,omitempty"`
	BinOffset    int64   `json:"binOffset"`
}

// LibraryPosition represents a position update from the dissect library.
// Used to pass pre-parsed position data instead of doing binary extraction.
type LibraryPosition struct {
	EntityRef   uint32
	PlayerIndex int // -1 for non-player entities
	X, Y, Z     float32
	Yaw         float32 // degrees
	Pitch       float32 // degrees, positive = looking up
	IsDroneView bool
	BinOffset   int // byte offset in decompressed stream (for bone/tick matching)
}
