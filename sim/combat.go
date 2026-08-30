package sim

import (
	"fmt"
	"math"
	"time"
)

// Combat resolution — v1.2 LOCKED model.
//
//   - Counter-weighted Lanchester engagement, auto-resolved server-side.
//   - Counter band OWNER-LOCKED: 1.25 / 1.00 / 0.82 (see CounterMult).
//   - Deterministic: same inputs -> same report. No mid-battle inputs, ever.
//   - Loss model: escort classes destroyable; capitals disabled -> repair.
//   - Every fight reveals tier-4 intel to BOTH sides: the battle report IS
//     the full scout (GDD 6.4.1).
//   - Losers leave wreck fields: open salvage with a decay timer — the first
//     leg of the MVP acceptance loop (fight -> loss -> wreck -> salvage...).
//
// Rout rule (M1 provisional, workbook v2 to confirm): a side disengages when
// its remaining strength falls below RoutAt of starting strength, unless it
// CannotRout (e.g. defending a hex it must hold, or scripted pirates).
// This is what keeps winner casualties inside the owner's 15–30% band at
// equal cost instead of Lanchester's fight-to-annihilation ~40%.

const (
	// RoutAt: fraction of starting strength below which a side withdraws.
	RoutAt = 0.63
	// timeStep controls integration granularity (fraction of a nominal round).
	timeStep = 0.05
	// maxSteps caps runaway battles.
	maxSteps = 4000
	// hpEpsilon: below this an HP pool is combat-ineffective (matches alive()).
	hpEpsilon = 0.5
	// WreckDecay: open-salvage window shown on the tile ("Wreck field · 3h").
	WreckDecay = 3 * time.Hour
	// wreckRecovery: fraction of destroyed Hullsteel value that lands in the
	// wreck field as salvageable material (split Hullsteel/Components).
	wreckRecovery = 0.5
)

// Side is one belligerent's battle state.
type side struct {
	fleet      *Fleet
	stacks     []Stack // working copy
	startHP    float64
	cannotRout bool
	doctrine   Doctrine
	routed     bool
}

func newSide(f *Fleet, cannotRout bool) *side {
	s := &side{fleet: f, cannotRout: cannotRout}
	s.stacks = make([]Stack, len(f.Stacks))
	copy(s.stacks, f.Stacks)
	for _, st := range s.stacks {
		s.startHP += st.HPPool
	}
	if f.Admiral != nil {
		s.doctrine = f.Admiral.Doctrine
	}
	return s
}

func (s *side) hp() float64 {
	var t float64
	for _, st := range s.stacks {
		t += st.HPPool
	}
	return t
}

func (s *side) alive() bool { return s.hp() > hpEpsilon }

// StackReport is the tier-4 view of one stack, before and after.
type StackReport struct {
	Class     ShipClass `json:"class"`
	Committed int       `json:"committed"`
	Lost      int       `json:"lost"`     // destroyed (escorts)
	Disabled  bool      `json:"disabled"` // capitals only
}

// SideReport summarizes one belligerent.
type SideReport struct {
	OwnerID      string        `json:"owner_id"`
	AdmiralName  string        `json:"admiral_name,omitempty"`
	Doctrine     string        `json:"doctrine,omitempty"` // tier-4: doctrine is public vocabulary
	Stacks       []StackReport `json:"stacks"`
	CasualtyRate float64       `json:"casualty_rate"` // HP-value fraction lost
	Routed       bool          `json:"routed"`
	Won          bool          `json:"won"`
}

// WreckField is dropped on the hex after a battle with losses.
type WreckField struct {
	Hex      Hex       `json:"hex"`
	Contents Wallet    `json:"contents"`
	DecaysAt time.Time `json:"decays_at"`
}

// BattleReport is stored server-side and returned to both parties (and, per
// scouting rules, is full tier-4 for both).
type BattleReport struct {
	ID        string      `json:"id"`
	Hex       Hex         `json:"hex"`
	At        time.Time   `json:"at"`
	Attacker  SideReport  `json:"attacker"`
	Defender  SideReport  `json:"defender"`
	Wreck     *WreckField `json:"wreck,omitempty"`
	Narrative string      `json:"narrative"`
}

// Resolve runs the auto-resolver. `when` stamps the report; determinism is
// structural (no RNG in the resolver itself — capture rolls etc. live in the
// tick layer where they can use a seeded PRNG keyed by battle ID).
func Resolve(battleID string, hex Hex, attacker, defender *Fleet, defenderCannotRout bool, when time.Time, fx DoctrineEffects) *BattleReport {
	if fx == nil {
		fx = NoDoctrineEffects{}
	}
	a := newSide(attacker, false)
	d := newSide(defender, defenderCannotRout)

	for step := 0; step < maxSteps; step++ {
		if !a.alive() || !d.alive() || a.routed || d.routed {
			break
		}
		// Simultaneous fire: compute both damage vectors from the same state.
		dmgToD := volley(a, d, fx)
		dmgToA := volley(d, a, fx)
		apply(d, dmgToD)
		apply(a, dmgToA)

		if !a.cannotRout && a.hp() < a.startHP*RoutAt {
			a.routed = true
		}
		if !d.cannotRout && d.hp() < d.startHP*RoutAt {
			d.routed = true
		}
	}

	attWon := (d.routed || !d.alive()) && !a.routed && a.alive()
	defWon := (a.routed || !a.alive()) && !d.routed && d.alive()

	rep := &BattleReport{ID: battleID, Hex: hex, At: when}
	var wreck Wallet
	rep.Attacker = finish(a, attWon, &wreck)
	rep.Defender = finish(d, defWon, &wreck)

	if wreck.Get(Hullsteel) > 0 || wreck.Get(Components) > 0 {
		rep.Wreck = &WreckField{Hex: hex, Contents: wreck, DecaysAt: when.Add(WreckDecay)}
	}

	rep.Narrative = narrate(rep)
	return rep
}

// volley computes damage per defender stack for one time step.
func volley(from, to *side, fx DoctrineEffects) []float64 {
	dmg := make([]float64, len(to.stacks))
	// Target weighting by cost share (bigger, pricier presence draws fire).
	var totalCost float64
	for _, ts := range to.stacks {
		if ts.HPPool > 0 {
			totalCost += float64(ts.Count) * float64(StatsFor(ts.Class).CostSteel)
		}
	}
	if totalCost == 0 {
		return dmg
	}
	for _, as := range from.stacks {
		if as.HPPool <= 0 {
			continue
		}
		// Effective shooters scale with remaining HP pool (Lanchester-style
		// attrition of firepower).
		effCount := as.HPPool / StatsFor(as.Class).HP
		base := effCount * StatsFor(as.Class).Attack * timeStep
		for j, ts := range to.stacks {
			if ts.HPPool <= 0 {
				continue
			}
			share := float64(ts.Count) * float64(StatsFor(ts.Class).CostSteel) / totalCost
			mult := CounterMult(as.Class, ts.Class) * fx.AttackMult(from.doctrine, as.Class, ts.Class)
			dmg[j] += base * share * mult
		}
	}
	return dmg
}

func apply(s *side, dmg []float64) {
	for i := range s.stacks {
		st := &s.stacks[i]
		if st.HPPool <= 0 {
			continue
		}
		st.HPPool -= dmg[i]
		if st.HPPool < 0 {
			st.HPPool = 0
		}
		if !st.Class.IsCapital() {
			// Escorts die in whole hulls as the pool drains.
			st.Count = int(math.Ceil(st.HPPool / StatsFor(st.Class).HP))
		}
	}
}

// finish writes results back onto the real fleet, builds the side report and
// accumulates wreck contents from destroyed hulls.
func finish(s *side, won bool, wreck *Wallet) SideReport {
	sr := SideReport{OwnerID: s.fleet.OwnerID, Routed: s.routed, Won: won}
	if s.fleet.Admiral != nil {
		sr.AdmiralName = s.fleet.Admiral.Name
		sr.Doctrine = s.fleet.Admiral.Doctrine.String()
	}
	if s.startHP > 0 {
		sr.CasualtyRate = 1 - s.hp()/s.startHP
	}
	for i := range s.stacks {
		orig := s.fleet.Stacks[i]
		cur := s.stacks[i]
		r := StackReport{Class: cur.Class, Committed: orig.Count}
		if cur.Class.IsCapital() {
			if cur.HPPool <= hpEpsilon {
				r.Disabled = true
				s.fleet.FlagshipDisabled = true
				cur.HPPool = 0
			}
		} else {
			r.Lost = orig.Count - cur.Count
			steelLost := int64(float64(int64(r.Lost)*StatsFor(cur.Class).CostSteel) * wreckRecovery)
			// Wreck split: ~70% Hullsteel scrap, 30% Components — salvage is
			// the fastest Components source by design (resource spec).
			wreck.Add(Hullsteel, steelLost*7/10)
			wreck.Add(Components, steelLost*3/10)
		}
		sr.Stacks = append(sr.Stacks, r)
		// Write survivor state back to the authoritative fleet.
		s.fleet.Stacks[i] = cur
	}
	return sr
}

func narrate(r *BattleReport) string {
	w, l := r.Attacker, r.Defender
	if r.Defender.Won {
		w, l = r.Defender, r.Attacker
	}
	if !r.Attacker.Won && !r.Defender.Won {
		return "Mutual withdrawal. Both formations broke off before commitment."
	}
	verb := "destroyed"
	if l.Routed {
		verb = "routed"
	}
	who := w.OwnerID
	if w.AdmiralName != "" {
		who = w.AdmiralName
	}
	return fmt.Sprintf("%s %s the opposing force at %+d,%+d. Winner casualties: %.0f%%. There are no rescue missions in the Verge.",
		who, verb, r.Hex.Q, r.Hex.R, w.CasualtyRate*100)
}
