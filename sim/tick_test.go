package sim

import (
	"testing"
	"time"
)

// TestSteelThread is milestone M1 in a single test, plus the first two legs
// of the MVP acceptance loop:
//
//	fleet order -> server tick -> pirate battle auto-resolves ->
//	battle report returns -> wreck -> salvage -> cargo.
func TestSteelThread(t *testing.T) {
	t0 := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	w := NewWorld("rim-1", t0)

	// A Commander's fleet at the station hex, fueled.
	player := &Fleet{
		ID: "fleet-1", OwnerID: "user-abc",
		Admiral: &Admiral{ID: "adm-1", Name: "Ress Vahl", Doctrine: Hunter},
		Stacks:  []Stack{NewStack(Interceptor, 60), NewStack(Cruiser, 20)},
		Pos:     Hex{0, 0}, Fuel: 10_000,
	}
	// Pirates squatting a nearby field: bomber-heavy — the counter lesson.
	pirates := &Fleet{
		ID: "npc-1", OwnerID: "npc:pirate",
		Stacks: []Stack{NewStack(Bomber, 50)},
		Pos:    Hex{4, -2},
	}
	w.Fleets[player.ID] = player
	w.Fleets[pirates.ID] = pirates

	// 1. Fleet order.
	o, err := w.SubmitMove("user-abc", "fleet-1", Hex{4, -2})
	if err != nil {
		t.Fatalf("move order rejected: %v", err)
	}
	if !o.ArriveAt.After(t0) {
		t.Fatal("travel must take time — timers are the game")
	}
	if player.Fuel >= 10_000 {
		t.Fatal("move must burn Volatiles")
	}

	// A second order for the same fleet must be rejected (one pending order).
	if _, err := w.SubmitMove("user-abc", "fleet-1", Hex{1, 1}); err != ErrBusy {
		t.Fatalf("expected ErrBusy, got %v", err)
	}
	// Other players cannot command the fleet.
	if _, err := w.SubmitMove("user-eve", "fleet-1", Hex{1, 1}); err != ErrNotYours {
		t.Fatalf("expected ErrNotYours, got %v", err)
	}

	// 2. Tick before arrival: nothing happens.
	w.Tick(t0.Add(1 * time.Minute))
	if player.Pos != (Hex{0, 0}) {
		t.Fatal("fleet teleported")
	}

	// 3. Tick past arrival: move lands, battle auto-resolves.
	w.Tick(o.ArriveAt.Add(time.Second))
	if player.Pos != (Hex{4, -2}) {
		t.Fatal("fleet did not arrive")
	}

	// 4. Battle report returns — for both sides, tier 4.
	reps := w.ReportsFor("user-abc")
	if len(reps) != 1 {
		t.Fatalf("want 1 battle report, got %d", len(reps))
	}
	rep := reps[0]
	if !rep.Attacker.Won {
		t.Fatalf("counter comp lost to pirates: %s", rep.Narrative)
	}
	if rep.Attacker.Doctrine != "Hunter" {
		t.Fatal("report must carry doctrine (tier-4)")
	}
	if len(w.ReportsFor("npc:pirate")) != 1 {
		t.Fatal("defender must receive the same report")
	}

	// Pirates destroyed and reaped; a wreck field remains.
	if _, ok := w.Fleets["npc-1"]; ok {
		t.Fatal("destroyed pirate fleet should be reaped")
	}
	if len(w.Wrecks) != 1 {
		t.Fatalf("want 1 wreck field, got %d", len(w.Wrecks))
	}
	var wreckID string
	for id := range w.Wrecks {
		wreckID = id
	}

	// 5. Salvage the wreck (fleet is on the hex after the fight).
	if _, err := w.SubmitSalvage("user-abc", "fleet-1", wreckID); err != nil {
		t.Fatalf("salvage rejected: %v", err)
	}
	w.Tick(w.Now.Add(6 * time.Minute))
	if player.Cargo.Get(Hullsteel) <= 0 || player.Cargo.Get(Components) <= 0 {
		t.Fatalf("salvage yielded nothing: %+v", player.Cargo)
	}
	if len(w.Wrecks) != 0 {
		t.Fatal("salvaged wreck should be gone")
	}
	t.Logf("steel thread OK — report: %s", rep.Narrative)
	t.Logf("salvage haul: %d Hullsteel, %d Components", player.Cargo.Get(Hullsteel), player.Cargo.Get(Components))
}

// TestWreckDecayRace: an unsalvaged wreck vanishes after the decay window —
// the "Wreck field · 3h" urgency shown on the tile is real.
func TestWreckDecayRace(t *testing.T) {
	t0 := time.Unix(0, 0)
	w := NewWorld("rim-1", t0)
	w.Wrecks["wr-1"] = &WreckField{Hex: Hex{1, 1}, DecaysAt: t0.Add(WreckDecay)}
	w.Tick(t0.Add(WreckDecay - time.Minute))
	if len(w.Wrecks) != 1 {
		t.Fatal("wreck decayed early")
	}
	w.Tick(t0.Add(WreckDecay))
	if len(w.Wrecks) != 0 {
		t.Fatal("wreck failed to decay")
	}
}

// TestFuelGate: a move beyond the fuel envelope is rejected — an empire's
// borders are where its fuel runs out, and so are a fleet's.
func TestFuelGate(t *testing.T) {
	t0 := time.Unix(0, 0)
	w := NewWorld("rim-1", t0)
	f := &Fleet{ID: "f", OwnerID: "u", Stacks: []Stack{NewStack(Cruiser, 10)}, Fuel: 5}
	w.Fleets["f"] = f
	if _, err := w.SubmitMove("u", "f", Hex{10, 10}); err != ErrNoFuel {
		t.Fatalf("expected ErrNoFuel, got %v", err)
	}
}

// TestRoutIsWithdrawal reproduces the first-live-battle bug (2026-08-30):
// a routed fleet must fall back a hex and must NOT re-engage on the next
// tick. One order, one battle — not a 3-battle annihilation grind.
func TestRoutIsWithdrawal(t *testing.T) {
	t0 := time.Date(2026, 8, 30, 14, 0, 0, 0, time.UTC)
	w := NewWorld("rim-1", t0)
	// The exact live comp: 20 interceptors + 10 cruisers vs 30 bombers.
	player := &Fleet{ID: "f1", OwnerID: "u1",
		Stacks: []Stack{NewStack(Interceptor, 20), NewStack(Cruiser, 10)},
		Pos:    Hex{0, 0}, Fuel: 100000}
	pirates := &Fleet{ID: "npc", OwnerID: "npc:pirate",
		Stacks: []Stack{NewStack(Bomber, 30)}, Pos: Hex{4, -2}}
	w.Fleets["f1"] = player
	w.Fleets["npc"] = pirates

	o, _ := w.SubmitMove("u1", "f1", Hex{4, -2})
	w.Tick(o.ArriveAt.Add(time.Second))

	if got := len(w.ReportsFor("u1")); got != 1 {
		t.Fatalf("want exactly 1 battle after arrival, got %d", got)
	}
	rep := w.ReportsFor("u1")[0]
	if !rep.Attacker.Routed {
		t.Skip("comp no longer routs — retune this scenario")
	}
	if player.Pos == (Hex{4, -2}) {
		t.Fatal("routed fleet must withdraw off the battle hex")
	}
	if player.Pos.Dist(Hex{0, 0}) != 3 {
		t.Fatalf("retreat should step one hex toward home, got %v", player.Pos)
	}
	// Several more ticks while reforming: no new battles even though the
	// pirate hex is adjacent.
	for i := 1; i <= 5; i++ {
		w.Tick(w.Now.Add(30 * time.Second))
	}
	if got := len(w.ReportsFor("u1")); got != 1 {
		t.Fatalf("reforming fleet re-engaged: %d battles", got)
	}
}

// TestWrecksMergePerHex: repeated fights on one hex build ONE debris field.
func TestWrecksMergePerHex(t *testing.T) {
	t0 := time.Unix(0, 0)
	w := NewWorld("rim-1", t0)
	w.addWreck(&WreckField{Hex: Hex{2, 2}, Contents: Wallet{100, 0, 40, 0, 0}, DecaysAt: t0.Add(time.Hour)})
	w.addWreck(&WreckField{Hex: Hex{2, 2}, Contents: Wallet{50, 0, 20, 0, 0}, DecaysAt: t0.Add(2 * time.Hour)})
	if len(w.Wrecks) != 1 {
		t.Fatalf("want merged wreck, got %d fields", len(w.Wrecks))
	}
	for _, wr := range w.Wrecks {
		if wr.Contents.Get(Hullsteel) != 150 || !wr.DecaysAt.Equal(t0.Add(2*time.Hour)) {
			t.Fatalf("bad merge: %+v", wr)
		}
	}
}

// TestSystemViewShowsWrecks: the client must be able to discover wreck ids.
func TestSystemViewShowsWrecks(t *testing.T) {
	t0 := time.Unix(0, 0)
	w := NewWorld("rim-1", t0)
	w.Fleets["f1"] = &Fleet{ID: "f1", OwnerID: "u1", Stacks: []Stack{NewStack(Cruiser, 5)}}
	w.addWreck(&WreckField{Hex: Hex{1, 1}, Contents: Wallet{10, 0, 5, 0, 0}, DecaysAt: t0.Add(time.Hour)})
	v := w.SystemViewFor("u1")
	if len(v.Fleets) != 1 || len(v.Wrecks) != 1 || v.Wrecks[0].ID == "" {
		t.Fatalf("bad system view: %+v", v)
	}
}
