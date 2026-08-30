package sim

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// World is one system's authoritative state for the M1 steel thread.
// (M2/M3 grow this to the five-system MVP topology with jump lanes; the
// per-system shape stays the same.)
//
// All mutation happens inside Tick or under the mutex via order submission —
// the server is authoritative for timers, combat, inventory (GDD 8).
type World struct {
	mu sync.Mutex

	SystemID string
	Now      time.Time

	Fleets  map[string]*Fleet
	Wrecks  map[string]*WreckField // key: wreck id
	Reports map[string]*BattleReport
	// ReportsByOwner indexes report ids for quick "my battle reports" reads.
	ReportsByOwner map[string][]string

	orders    []*Order
	battleSeq int
	wreckSeq  int
}

// OrderKind — M1 verbs. Attack-fleet, mine, escort etc. arrive with M2/M3.
type OrderKind int

const (
	OrderMove OrderKind = iota
	OrderSalvage
)

// Order is a queued fleet instruction with a server-computed arrival time.
// No mid-flight edits in M1 (recall lands later; composition locks at commit
// per the rally rule — same principle here).
type Order struct {
	FleetID  string    `json:"fleet_id"`
	Kind     OrderKind `json:"kind"`
	Dest     Hex       `json:"dest"`
	From     Hex       `json:"from"`
	TargetID string    `json:"target_id,omitempty"`
	ArriveAt time.Time `json:"arrive_at"`
	Done     bool      `json:"done"`
}

// Pulse is what one Tick changed — the adapter turns this into notifications
// and match opcodes. The sim does not know Nakama exists.
type Pulse struct {
	Reports  []*BattleReport `json:"reports,omitempty"`
	Arrived  []string        `json:"arrived,omitempty"`
	Salvaged []string        `json:"salvaged,omitempty"`
	Decayed  []string        `json:"decayed,omitempty"`
}

func NewWorld(systemID string, now time.Time) *World {
	return &World{
		SystemID:       systemID,
		Now:            now,
		Fleets:         map[string]*Fleet{},
		Wrecks:         map[string]*WreckField{},
		Reports:        map[string]*BattleReport{},
		ReportsByOwner: map[string][]string{},
	}
}

var (
	ErrNoFleet    = errors.New("no such fleet")
	ErrNotYours   = errors.New("fleet not owned by caller")
	ErrNoFuel     = errors.New("insufficient Volatiles for that move")
	ErrBusy       = errors.New("fleet already has a pending order")
	ErrNoWreck    = errors.New("no such wreck field (it may have decayed)")
	ErrOutOfRange = errors.New("salvage requires the fleet on the wreck hex")
	ErrFleetDown  = errors.New("fleet is not combat-capable")
)

// SubmitMove validates fuel and queues a move. Fuel is deducted at commit —
// "fleets burn fuel, reach IS power" (logistics core decision).
func (w *World) SubmitMove(callerID, fleetID string, dest Hex) (*Order, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	f, err := w.ownedFleet(callerID, fleetID)
	if err != nil {
		return nil, err
	}
	if w.pending(fleetID) != nil {
		return nil, ErrBusy
	}
	cost := FuelCost(f, f.Pos, dest)
	if f.Fuel < cost {
		return nil, ErrNoFuel
	}
	f.Fuel -= cost
	o := &Order{
		FleetID:  fleetID,
		Kind:     OrderMove,
		Dest:     dest,
		From:     f.Pos,
		ArriveAt: w.Now.Add(TravelTime(f.Pos, dest)),
	}
	w.orders = append(w.orders, o)
	return o, nil
}

// SubmitSalvage queues collection of a wreck field the fleet is sitting on.
// Wrecks are OPEN salvage — anyone on the hex may take them before decay.
func (w *World) SubmitSalvage(callerID, fleetID, wreckID string) (*Order, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	f, err := w.ownedFleet(callerID, fleetID)
	if err != nil {
		return nil, err
	}
	if w.pending(fleetID) != nil {
		return nil, ErrBusy
	}
	wr, ok := w.Wrecks[wreckID]
	if !ok {
		return nil, ErrNoWreck
	}
	if f.Pos != wr.Hex {
		return nil, ErrOutOfRange
	}
	o := &Order{
		FleetID:  fleetID,
		Kind:     OrderSalvage,
		From:     f.Pos,
		Dest:     f.Pos,
		TargetID: wreckID,
		// Flat 5-minute salvage op in M1 (Salvager doctrine will bend this).
		ArriveAt: w.Now.Add(5 * time.Minute),
	}
	w.orders = append(w.orders, o)
	return o, nil
}

// Tick advances world time and resolves everything due. Deterministic given
// the same state + now. The returned Pulse is the push surface: arrivals,
// salvage, decay, and new battle reports.
func (w *World) Tick(now time.Time) Pulse {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.Now = now
	p := Pulse{}

	// 1. Wreck decay.
	for id, wr := range w.Wrecks {
		if !now.Before(wr.DecaysAt) {
			p.Decayed = append(p.Decayed, id)
			delete(w.Wrecks, id)
		}
	}

	// 2. Due orders, in submission sequence (stable, fair).
	remaining := w.orders[:0]
	for _, o := range w.orders {
		if o.Done || now.Before(o.ArriveAt) {
			if !o.Done {
				remaining = append(remaining, o)
			}
			continue
		}
		kind := o.Kind
		w.execute(o)
		o.Done = true
		switch kind {
		case OrderMove:
			p.Arrived = append(p.Arrived, o.FleetID)
		case OrderSalvage:
			p.Salvaged = append(p.Salvaged, o.TargetID)
		}
	}
	w.orders = remaining

	// 3. Auto-engagement: hostile fleets sharing a hex fight. "Fleets in a
	// fight's hex are in the fight." M1 hostility rule: pirates are hostile
	// to everyone.
	p.Reports = w.resolveEngagements()
	return p
}

func (w *World) execute(o *Order) {
	f := w.Fleets[o.FleetID]
	if f == nil {
		return
	}
	switch o.Kind {
	case OrderMove:
		f.Pos = o.Dest
	case OrderSalvage:
		wr, ok := w.Wrecks[o.TargetID]
		if !ok || f.Pos != wr.Hex {
			return // decayed or fleet displaced — order fizzles
		}
		for r := Resource(0); r < numResources; r++ {
			f.Cargo.Add(r, wr.Contents.Get(r))
		}
		delete(w.Wrecks, o.TargetID)
	}
}

func (w *World) resolveEngagements() []*BattleReport {
	// Group fleets by hex.
	byHex := map[Hex][]*Fleet{}
	for _, f := range w.Fleets {
		if f.Alive() {
			byHex[f.Pos] = append(byHex[f.Pos], f)
		}
	}
	var reports []*BattleReport
	for hex, fleets := range byHex {
		if len(fleets) < 2 {
			continue
		}
		var pirate, player *Fleet
		for _, f := range fleets {
			if IsPirate(f) && pirate == nil {
				pirate = f
			}
			if !IsPirate(f) && player == nil && w.Now.After(f.ReformingUntil) {
				player = f
			}
		}
		if pirate == nil || player == nil {
			continue
		}
		w.battleSeq++
		id := fmt.Sprintf("battle-%s-%d", w.SystemID, w.battleSeq)
		// Player is the attacker when moving onto the pirate hex; pirates
		// fight to destruction (they have nowhere to go).
		rep := Resolve(id, hex, player, pirate, true, w.Now, NoDoctrineEffects{})
		w.storeReport(rep)
		reports = append(reports, rep)
		if rep.Wreck != nil {
			w.addWreck(rep.Wreck)
		}
		// ROUT = WITHDRAWAL: a routed fleet falls back one hex toward home
		// and spends time reforming before it can engage again. Without
		// this, a routed fleet re-engages every tick until annihilation
		// (found in the first live battle, 2026-08-30).
		if rep.Attacker.Routed {
			player.Pos = retreatHex(player.Pos)
			player.ReformingUntil = w.Now.Add(ReformTime)
		}
		w.reap()
	}
	return reports
}

// ReformTime is how long a routed fleet needs before it can fight again.
const ReformTime = 10 * time.Minute

// retreatHex steps one hex from `from` toward the system origin (the
// station anchor at 0,0). If already at origin, fall back along +Q.
func retreatHex(from Hex) Hex {
	if from == (Hex{0, 0}) {
		return Hex{1, 0}
	}
	best, bestD := from, from.Dist(Hex{0, 0})
	for _, n := range []Hex{
		{from.Q + 1, from.R}, {from.Q - 1, from.R},
		{from.Q, from.R + 1}, {from.Q, from.R - 1},
		{from.Q + 1, from.R - 1}, {from.Q - 1, from.R + 1},
	} {
		if d := n.Dist(Hex{0, 0}); d < bestD {
			best, bestD = n, d
		}
	}
	return best
}

// addWreck merges into an existing field on the same hex (battles in the
// same spot make one debris field, refreshing the decay clock) or creates
// a new one.
func (w *World) addWreck(nw *WreckField) {
	for _, existing := range w.Wrecks {
		if existing.Hex == nw.Hex {
			for r := Resource(0); r < numResources; r++ {
				existing.Contents.Add(r, nw.Contents.Get(r))
			}
			if nw.DecaysAt.After(existing.DecaysAt) {
				existing.DecaysAt = nw.DecaysAt
			}
			return
		}
	}
	w.wreckSeq++
	w.Wrecks[fmt.Sprintf("wreck-%s-%d", w.SystemID, w.wreckSeq)] = nw
}

func (w *World) storeReport(r *BattleReport) {
	w.Reports[r.ID] = r
	for _, owner := range []string{r.Attacker.OwnerID, r.Defender.OwnerID} {
		w.ReportsByOwner[owner] = append(w.ReportsByOwner[owner], r.ID)
	}
}

// reap removes fully destroyed NPC fleets (player fleets persist even at
// zero escorts — the Commander decides what happens to the hulk).
func (w *World) reap() {
	for id, f := range w.Fleets {
		if IsPirate(f) && !f.Alive() {
			delete(w.Fleets, id)
		}
	}
}

func (w *World) ownedFleet(callerID, fleetID string) (*Fleet, error) {
	f, ok := w.Fleets[fleetID]
	if !ok {
		return nil, ErrNoFleet
	}
	if f.OwnerID != callerID {
		return nil, ErrNotYours
	}
	if !f.Alive() {
		return nil, ErrFleetDown
	}
	return f, nil
}

func (w *World) pending(fleetID string) *Order {
	for _, o := range w.orders {
		if !o.Done && o.FleetID == fleetID {
			return o
		}
	}
	return nil
}

// IsPirate: M1 NPC convention.
func IsPirate(f *Fleet) bool { return f.OwnerID == "npc:pirate" }

// ---------- Read models (what RPCs return) ----------

// FleetView is the owner's view of their fleet.
type FleetView struct {
	ID               string    `json:"id"`
	Pos              Hex       `json:"pos"`
	Fuel             float64   `json:"fuel"`
	Cargo            Wallet    `json:"cargo"`
	Stacks           []Stack   `json:"stacks"`
	FlagshipDisabled bool      `json:"flagship_disabled"`
	Busy             bool      `json:"busy"`
	ArriveAt         time.Time `json:"arrive_at,omitempty"`
	From             *Hex      `json:"from,omitempty"`
	Dest             *Hex      `json:"dest,omitempty"`
	OrderKind        string    `json:"order_kind,omitempty"`
}

func (w *World) FleetViewFor(callerID, fleetID string) (*FleetView, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	f, ok := w.Fleets[fleetID]
	if !ok {
		return nil, ErrNoFleet
	}
	if f.OwnerID != callerID {
		return nil, ErrNotYours
	}
	v := &FleetView{
		ID: f.ID, Pos: f.Pos, Fuel: f.Fuel, Cargo: f.Cargo,
		Stacks: append([]Stack(nil), f.Stacks...), FlagshipDisabled: f.FlagshipDisabled,
	}
	if o := w.pending(fleetID); o != nil {
		from, dest := o.From, o.Dest
		v.Busy = true
		v.ArriveAt = o.ArriveAt
		v.From = &from
		v.Dest = &dest
		v.OrderKind = o.Kind.String()
	}
	return v, nil
}

// WreckView exposes a wreck field with its id so clients can salvage it.
type WreckView struct {
	ID       string    `json:"id"`
	Hex      Hex       `json:"hex"`
	Contents Wallet    `json:"contents"`
	DecaysAt time.Time `json:"decays_at"`
}

// SystemView is the caller's picture of the system: their own fleets in
// full detail, plus visible wreck fields. (Hostile-fleet visibility goes
// through scouting tiers in M2 — M1 shows no other fleets.)
type SystemView struct {
	SystemID string       `json:"system_id"`
	Now      time.Time    `json:"now"`
	Fleets   []*FleetView `json:"fleets"`
	Wrecks   []WreckView  `json:"wrecks"`
	// Hostiles are occupied pirate hexes with NO stack counts — M1 fog.
	// Composition stays a scout/report reveal (GDD 6.4.1). The steel thread
	// still has a place to send the fleet.
	Hostiles []Hex `json:"hostiles,omitempty"`
}

func (w *World) SystemViewFor(callerID string) *SystemView {
	w.mu.Lock()
	defer w.mu.Unlock()
	v := &SystemView{SystemID: w.SystemID, Now: w.Now}
	for _, f := range w.Fleets {
		if f.OwnerID != callerID {
			continue
		}
		fv := &FleetView{
			ID: f.ID, Pos: f.Pos, Fuel: f.Fuel, Cargo: f.Cargo,
			Stacks: append([]Stack(nil), f.Stacks...), FlagshipDisabled: f.FlagshipDisabled,
		}
		if o := w.pending(f.ID); o != nil {
			from, dest := o.From, o.Dest
			fv.Busy = true
			fv.ArriveAt = o.ArriveAt
			fv.From = &from
			fv.Dest = &dest
			fv.OrderKind = o.Kind.String()
		}
		v.Fleets = append(v.Fleets, fv)
	}
	for id, wr := range w.Wrecks {
		v.Wrecks = append(v.Wrecks, WreckView{ID: id, Hex: wr.Hex, Contents: wr.Contents, DecaysAt: wr.DecaysAt})
	}
	for _, f := range w.Fleets {
		if IsPirate(f) && f.Alive() {
			v.Hostiles = append(v.Hostiles, f.Pos)
		}
	}
	return v
}

// ReportsFor returns the caller's battle reports, newest last.
func (w *World) ReportsFor(callerID string) []*BattleReport {
	w.mu.Lock()
	defer w.mu.Unlock()
	ids := w.ReportsByOwner[callerID]
	out := make([]*BattleReport, 0, len(ids))
	for _, id := range ids {
		if r, ok := w.Reports[id]; ok {
			out = append(out, r)
		}
	}
	return out
}
