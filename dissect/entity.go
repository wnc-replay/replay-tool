package dissect

// EntityType classifies non-player entities tracked in the replay.
type EntityType string

const (
	EntityDrone      EntityType = "drone"
	EntityCamera     EntityType = "camera"
	EntityGadget     EntityType = "gadget"
	EntityProjectile EntityType = "projectile"
	EntityWeapon     EntityType = "weapon"
	EntityBarricade  EntityType = "barricade"
	EntityUnknown    EntityType = "unknown"
)

// TrackedEntity represents a non-player entity (drone, camera, gadget, etc.)
// with classified type and full position history for map visualization.
type TrackedEntity struct {
	EntityRef      uint32           `json:"entityRef"`
	Type           EntityType       `json:"type"`
	GadgetName     string           `json:"gadgetName,omitempty"`     // human-readable name e.g. "Shock Drone"
	ProjectileType string           `json:"projectileType,omitempty"` // "impact", "grapple", "" (from FC-UPDATE flags)
	GadgetType     string           `json:"gadgetType,omitempty"`     // "trip", "gu", "electroclaw", "prisma", "kiba" (from binary fingerprint)
	IsBarricade    bool             `json:"isBarricade,omitempty"`    // true for barricade entities (doors/windows, counter 130 + flag 0x1FE0)
	BarricadeType  string           `json:"barricadeType,omitempty"`  // barricade sub-type: "barricade", "single_door", etc.
	OwnerIndex     int              `json:"ownerIndex"`               // player who deployed/owns the drone, -1 if unknown
	OperatorIndex  int              `json:"operatorIndex"`            // player currently viewing/controlling the drone, -1 if unknown
	Team           TeamRole         `json:"team"`                     // "Attack" or "Defense"
	SpawnCounter   uint32           `json:"spawnCounter,omitempty"`   // SPAWN counter value (146=gadget, 154=drone, 130=barricade, 138/254=weapon)
	SpawnHashA     uint32           `json:"spawnHashA,omitempty"`     // hashA at SPAWN +60 — gadget/sub-type identifier; see docs/BINARY_FORMAT.md "Spawn hashA distribution per counter"
	HealthEvents   []HealthEvent    `json:"healthEvents,omitempty"`   // health changes extracted from binary
	Positions      []EntityPosition `json:"positions"`
}

// EntityPosition is a single position sample for a tracked entity.
type EntityPosition struct {
	X     float32 `json:"x"`
	Y     float32 `json:"y"`
	Z     float32 `json:"z"`
	Yaw   float32 `json:"yaw,omitempty"`   // degrees, 0 = no rotation data
	Pitch float32 `json:"pitch,omitempty"` // degrees, positive = looking up
	Seq   int     `json:"seq"`             // sequence in the main position stream
}

// WeaponInfo describes a weapon detected from ammo events.
type WeaponInfo struct {
	EntityRef  uint32 `json:"entityRef"`
	InitialCap uint32 `json:"initialCapacity"`
	Hash1      uint32 `json:"hash1"`
	Hash2      uint32 `json:"hash2"`
	IsPrimary  bool   `json:"isPrimary"`
}

// ShotEvent represents a single shot fired, reconstructed from ammo decreases.
type ShotEvent struct {
	PlayerIndex   int     `json:"playerIndex"`
	X             float32 `json:"x"`
	Y             float32 `json:"y"`
	Z             float32 `json:"z"`
	Yaw           float32 `json:"yaw"`           // degrees
	Pitch         float32 `json:"pitch"`         // degrees
	HeadQX        float32 `json:"hqX,omitempty"` // head bone quaternion X
	HeadQY        float32 `json:"hqY,omitempty"` // head bone quaternion Y
	HeadQZ        float32 `json:"hqZ,omitempty"` // head bone quaternion Z
	HeadQW        float32 `json:"hqW,omitempty"` // head bone quaternion W
	TimeInSeconds float64 `json:"timeInSeconds"`
	Seq           int     `json:"seq"` // closest position seq
}

// HealthEvent is a single health change detected in the replay binary.
type HealthEvent struct {
	Offset      int64   `json:"offset"`      // byte offset in decompressed binary
	HP          int     `json:"hp"`          // current health points (0–125+)
	HPFraction  float32 `json:"hpFraction"`  // HP / maxHP as float (0.0–1.0)
	TimeSeconds float32 `json:"timeSeconds"` // elapsed seconds since round start (0 if unknown)
}

// GadgetLoadout holds the primary and secondary gadget counts for a player,
// extracted from inventory state packets in the decompressed binary.
type GadgetLoadout struct {
	PrimaryCount   int  `json:"primaryCount"`   // number of primary gadget items
	SecondaryCount int  `json:"secondaryCount"` // number of secondary gadget items
	HasPrimary     bool `json:"hasPrimary"`     // true when primary count was found
	HasSecondary   bool `json:"hasSecondary"`   // true when secondary count was found
}

// PlayerLoadout contains the detected primary and secondary weapons for a player.
type PlayerLoadout struct {
	PlayerIndex   int            `json:"playerIndex"`
	Username      string         `json:"username"`
	Primary       *WeaponInfo    `json:"primary,omitempty"`
	Secondary     *WeaponInfo    `json:"secondary,omitempty"`
	GadgetLoadout *GadgetLoadout `json:"gadgetLoadout,omitempty"` // gadget counts from inventory packets
}

// operatorGadgetType describes what kind of special moving entity an operator's
// unique ability produces, if any. Only operators whose gadgets create tracked
// entities visible on the map are listed.
type operatorGadgetType struct {
	entityType EntityType
	maxCount   int    // max number of active instances (per round)
	gadgetName string // human-readable name shown in viewer
}

// operatorMovingGadgets maps operator names to their special moving gadgets.
// Only includes gadgets that produce positional data (i.e., entities that move on the map).
var operatorMovingGadgets = map[string]operatorGadgetType{
	// ── Attackers with special drones ──
	"Twitch": {EntityDrone, 2, "Shock Drone"},
	"Flores": {EntityDrone, 4, "RCE-Ratero"},
	"Brava":  {EntityDrone, 2, "Kludge Drone"},

	// ── Attackers with cameras ──
	"Zero": {EntityCamera, 4, "ARGUS Cam"},

	// ── Attackers with throwable/deployable gadgets ──
	"Ying":     {EntityGadget, 4, "Candela"},
	"Hibana":   {EntityGadget, 3, "X-KAIROS"},
	"Fuze":     {EntityGadget, 4, "Cluster Charge"},
	"Ash":      {EntityProjectile, 2, "Breaching Round"},
	"Capitao":  {EntityGadget, 4, "Asphyxiating Bolt"},
	"Gridlock": {EntityGadget, 3, "Trax Stinger"},
	"Nomad":    {EntityGadget, 3, "Airjab"},
	"Grim":     {EntityGadget, 4, "Kawan Hive"},
	"Deimos":   {EntityGadget, 1, "DeathMARK"},
	"Sens":     {EntityGadget, 1, "R.O.U. Projector"},
	"Ace":      {EntityGadget, 3, "S.E.L.M.A."},
	"Thermite": {EntityGadget, 2, "Exothermic Charge"},
	"Maverick": {EntityGadget, 1, "Breaching Torch"},
	"Iana":     {EntityDrone, 1, "Gemini Replicator"},
	"Kali":     {EntityProjectile, 1, "LV Explosive Lance"},
	"Skopos":   {EntityDrone, 1, "Falcon Shield"},

	// ── Defenders with special drones ──
	"Echo":   {EntityDrone, 2, "Yokai"},
	"Mozzie": {EntityDrone, 3, "Pest"},

	// ── Defenders with cameras ──
	"Maestro":  {EntityCamera, 2, "Evil Eye"},
	"Valkyrie": {EntityCamera, 3, "Black Eye"},

	// ── Defenders with deployable gadgets ──
	"Alibi":       {EntityGadget, 3, "Prisma"},
	"Smoke":       {EntityGadget, 3, "Gas Canister"},
	"Mute":        {EntityGadget, 4, "Signal Disruptor"},
	"Fenrir":      {EntityGadget, 5, "Dread Mine"},
	"Kapkan":      {EntityGadget, 5, "EDD"},
	"Frost":       {EntityGadget, 3, "Welcome Mat"},
	"Lesion":      {EntityGadget, 8, "Gu Mine"},
	"Ela":         {EntityGadget, 3, "Grzmot Mine"},
	"Melusi":      {EntityGadget, 3, "Banshee"},
	"Thorn":       {EntityGadget, 3, "Razorbloom"},
	"Aruni":       {EntityGadget, 3, "Surya Gate"},
	"Jager":       {EntityGadget, 3, "ADS"},
	"Wamai":       {EntityGadget, 5, "Mag-NET"},
	"Bandit":      {EntityGadget, 4, "Shock Wire"},
	"Kaid":        {EntityGadget, 2, "Rtila"},
	"Castle":      {EntityGadget, 3, "Armor Panel"},
	"Goyo":        {EntityGadget, 2, "Volcán Shield"},
	"Azami":       {EntityGadget, 5, "Kiba Barrier"},
	"Thunderbird": {EntityGadget, 3, "Kona Station"},
	"Sentry":      {EntityGadget, 3, "Barricade"},
	"Denari":      {EntityGadget, 2, "Viperstrike"},
	"Solis":       {EntityGadget, 1, "SPEC-IO"},
	"Tubarao":     {EntityGadget, 2, "Zoto Canister"},
}
