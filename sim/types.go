// Package sim is the deterministic, dependency-free core of the Vergefall
// galaxy simulation. It is deliberately pure Go stdlib so it can be unit
// tested in isolation and embedded in the Nakama runtime module unchanged.
//
// Design sources: GDD "Vergefall: Empires" (living draft, 2026-08-29).
// Combat model: v1.2 LOCKED — counter-weighted Lanchester, auto-resolved,
// counter band 1.25 / 1.00 / 0.82, no mid-battle inputs.
package sim

import "time"

// ---------- Resources ----------

// Resource identities are FINAL per decision log 2026-08-29.
type Resource int

const (
	Hullsteel  Resource = iota // safe foundation; ship hulls, station structure
	Volatiles                  // fuel; consumed by movement + forward ops
	Components                 // modules, repairs; salvage is fastest source
	Echo                       // contested-space only; zero home production
	Scrip                      // currency: trade / upkeep / ransom
	numResources
)

var resourceNames = [...]string{"Hullsteel", "Volatiles", "Components", "Echo", "Scrip"}

func (r Resource) String() string { return resourceNames[r] }

// Wallet is a resource bundle (station stores, hauler cargo, wreck contents).
type Wallet [numResources]int64

func (w *Wallet) Add(r Resource, n int64) { w[r] += n }
func (w *Wallet) Get(r Resource) int64    { return w[r] }
func (w *Wallet) CanAfford(cost Wallet) bool {
	for i := range w {
		if w[i] < cost[i] {
			return false
		}
	}
	return true
}

// ---------- Ship classes ----------

// ShipClass covers the MVP hull set: three combat classes forming the counter
// triangle, the hauler (vulnerable logistics), and one flagship type
// (capital: disabled -> repair queue, never destroyed — loss model, GDD 6.4).
type ShipClass int

const (
	Interceptor ShipClass = iota
	Bomber
	Cruiser
	Hauler
	Flagship
	numShipClasses
)

var shipClassNames = [...]string{"Interceptor", "Bomber", "Cruiser", "Hauler", "Flagship"}

func (c ShipClass) String() string { return shipClassNames[c] }

// IsCapital reports whether losses disable (repair queue) rather than destroy.
func (c ShipClass) IsCapital() bool { return c == Flagship }

// ShipStats — PROVISIONAL M1 tuning values, pending combat workbook v2.
// v1.2 finding honored: escort classes are cost-tuned to NEUTRAL
// power-per-Hullsteel PARITY (identical cost-normalized HP/ATK); all
// differentiation lives in the counter matrix, so the pillar-1 test isolates
// composition, not hidden cost imbalance.
type ShipStats struct {
	HP         float64
	Attack     float64
	CostSteel  int64 // Hullsteel per hull
	FuelPerHex float64
}

var baseStats = map[ShipClass]ShipStats{
	Interceptor: {HP: 100, Attack: 10, CostSteel: 100, FuelPerHex: 1.0},
	Bomber:      {HP: 100, Attack: 10, CostSteel: 100, FuelPerHex: 1.2},
	Cruiser:     {HP: 100, Attack: 10, CostSteel: 100, FuelPerHex: 1.5},
	Hauler:      {HP: 80, Attack: 0.5, CostSteel: 60, FuelPerHex: 1.2},
	Flagship:    {HP: 800, Attack: 40, CostSteel: 1200, FuelPerHex: 4.0},
}

func StatsFor(c ShipClass) ShipStats { return baseStats[c] }

// counterMatrix implements the OWNER-LOCKED band: hard 1.25 / neutral 1.00 /
// bad 0.82. Triangle: Interceptor > Bomber > Cruiser > Interceptor.
// Haulers are hard-countered by every combat class and are bad against all
// (hauler vulnerability is depth-stack item #1 alongside the triangle).
// Flagships fight everything at neutral in M1 (identity comes from admirals
// and modules later, not from the matrix).
const (
	HardCounter = 1.25
	NeutralMult = 1.00
	BadMatchup  = 0.82
)

func CounterMult(attacker, target ShipClass) float64 {
	switch attacker {
	case Interceptor:
		switch target {
		case Bomber:
			return HardCounter
		case Cruiser:
			return BadMatchup
		case Hauler:
			return HardCounter
		}
	case Bomber:
		switch target {
		case Cruiser:
			return HardCounter
		case Interceptor:
			return BadMatchup
		case Hauler:
			return HardCounter
		}
	case Cruiser:
		switch target {
		case Interceptor:
			return HardCounter
		case Bomber:
			return BadMatchup
		case Hauler:
			return HardCounter
		}
	case Hauler:
		return BadMatchup
	case Flagship:
		return NeutralMult
	}
	return NeutralMult
}

// ---------- Admirals ----------

// Doctrine is the ONE solver-read tag per admiral (GDD 6.3). Effects are
// CRITICAL PATH pending combat workbook v2 — the DoctrineEffects interface
// below is the socket they plug into; M1 ships with NoDoctrineEffects.
type Doctrine int

const (
	NoDoctrine Doctrine = iota
	Screen
	Hunter
	Salvager
	Haulmaster
	Siege
	Surveyor
)

var doctrineNames = [...]string{"None", "Screen", "Hunter", "Salvager", "Haulmaster", "Siege", "Surveyor"}

func (d Doctrine) String() string { return doctrineNames[d] }

// DoctrineEffects is the solver hook. Workbook v2 will define real
// implementations; the solver already calls it so no combat-code rework is
// needed when effects land.
type DoctrineEffects interface {
	// AttackMult lets a doctrine scale one side's effective attack for a
	// given attacker/target class pair. Must be deterministic.
	AttackMult(d Doctrine, attacker, target ShipClass) float64
}

// NoDoctrineEffects is the M1 stub: doctrines are recorded and reported
// (scouting tier 4 / battle reports) but do not yet alter resolution.
type NoDoctrineEffects struct{}

func (NoDoctrineEffects) AttackMult(Doctrine, ShipClass, ShipClass) float64 { return 1.0 }

// Admiral commands a fleet. Trees are private numbers (not in M1); doctrine
// is public vocabulary.
type Admiral struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Doctrine Doctrine `json:"doctrine"`
	Level    int      `json:"level"`
}

// ---------- Fleets ----------

// Stack is a homogeneous group of hulls inside a fleet.
type Stack struct {
	Class  ShipClass `json:"class"`
	Count  int       `json:"count"`
	HPPool float64   `json:"hp_pool"`
}

func NewStack(c ShipClass, n int) Stack {
	return Stack{Class: c, Count: n, HPPool: float64(n) * StatsFor(c).HP}
}

// Fleet is the unit of orders and combat.
type Fleet struct {
	ID      string   `json:"id"`
	OwnerID string   `json:"owner_id"` // player user id, or "npc:pirate" etc.
	Admiral *Admiral `json:"admiral,omitempty"`
	Stacks  []Stack  `json:"stacks"`
	Pos     Hex      `json:"pos"`
	Fuel    float64  `json:"fuel"`
	Cargo   Wallet   `json:"cargo"`
	// FlagshipDisabled marks the capital as in need of a repair queue.
	FlagshipDisabled bool `json:"flagship_disabled"`
	// ReformingUntil: a routed fleet cannot engage until this time passes.
	ReformingUntil time.Time `json:"reforming_until"`
}

// CostSteel returns total Hullsteel value — used for equal-cost balance tests
// and (later) wreck-field contents.
func (f *Fleet) CostSteel() int64 {
	var t int64
	for _, s := range f.Stacks {
		t += int64(s.Count) * StatsFor(s.Class).CostSteel
	}
	return t
}

// Alive reports whether the fleet retains any combat-capable hulls.
func (f *Fleet) Alive() bool {
	for _, s := range f.Stacks {
		if s.Count > 0 && !(s.Class.IsCapital() && f.FlagshipDisabled) {
			return true
		}
	}
	return false
}
