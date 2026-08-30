package sim

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestSnapshotRoundtrip(t *testing.T) {
	t0 := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	w := NewWorld("rim-1", t0)
	w.Fleets["fleet-1"] = &Fleet{
		ID: "fleet-1", OwnerID: "user-abc",
		Admiral: &Admiral{ID: "adm-1", Name: "Ress Vahl", Doctrine: Screen},
		Stacks:  []Stack{NewStack(Interceptor, 20), NewStack(Cruiser, 10)},
		Pos:     Hex{0, 0}, Fuel: 5000,
	}
	w.Fleets["npc-a"] = &Fleet{
		ID: "npc-a", OwnerID: "npc:pirate",
		Stacks: []Stack{NewStack(Bomber, 30)},
		Pos:    Hex{4, -2},
	}
	if _, err := w.SubmitMove("user-abc", "fleet-1", Hex{4, -2}); err != nil {
		t.Fatal(err)
	}

	raw, err := w.MarshalSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.SystemID != "rim-1" || len(got.Fleets) != 2 {
		t.Fatalf("bad restore: id=%s fleets=%d", got.SystemID, len(got.Fleets))
	}
	f := got.Fleets["fleet-1"]
	if f.Admiral == nil || f.Admiral.Doctrine != Screen {
		t.Fatalf("doctrine did not round-trip: %+v", f.Admiral)
	}
	if f.Stacks[0].Class != Interceptor || f.Stacks[0].Count != 20 {
		t.Fatalf("stacks did not round-trip: %+v", f.Stacks)
	}
	if got.pending("fleet-1") == nil {
		t.Fatal("in-flight order lost across snapshot")
	}

	// Restarted process continues the steel thread.
	o := got.pending("fleet-1")
	got.Tick(o.ArriveAt.Add(time.Second))
	if n := len(got.ReportsFor("user-abc")); n != 1 {
		t.Fatalf("restored world did not resolve the fight, reports=%d", n)
	}
}

func TestJSONUsesNamesNotEnums(t *testing.T) {
	st := NewStack(Bomber, 7)
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"Bomber"`)) {
		t.Fatalf("class should marshal as name, got %s", b)
	}
	if !bytes.Contains(b, []byte(`"hp_pool"`)) {
		t.Fatalf("hp pool should be snake_case, got %s", b)
	}
	h, err := json.Marshal(Hex{Q: 4, R: -2})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(h, []byte(`"q":4`)) || !bytes.Contains(h, []byte(`"r":-2`)) {
		t.Fatalf("hex should be q/r, got %s", h)
	}
}

func TestSystemViewHostilesAreFog(t *testing.T) {
	t0 := time.Unix(0, 0)
	w := NewWorld("rim-1", t0)
	w.Fleets["f1"] = &Fleet{ID: "f1", OwnerID: "u1", Stacks: []Stack{NewStack(Cruiser, 5)}, Pos: Hex{0, 0}}
	w.Fleets["npc"] = &Fleet{ID: "npc", OwnerID: "npc:pirate", Stacks: []Stack{NewStack(Bomber, 30)}, Pos: Hex{4, -2}}
	v := w.SystemViewFor("u1")
	if len(v.Hostiles) != 1 || v.Hostiles[0] != (Hex{4, -2}) {
		t.Fatalf("want pirate hex as hostile fog, got %+v", v.Hostiles)
	}
	if len(v.Fleets) != 1 || v.Fleets[0].ID != "f1" {
		t.Fatal("hostile fleets must not appear in the owned fleet list")
	}
}

func TestPulseReportsBattle(t *testing.T) {
	t0 := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	w := NewWorld("rim-1", t0)
	w.Fleets["f"] = &Fleet{ID: "f", OwnerID: "u", Stacks: []Stack{NewStack(Interceptor, 60)}, Pos: Hex{0, 0}, Fuel: 10000}
	w.Fleets["npc"] = &Fleet{ID: "npc", OwnerID: "npc:pirate", Stacks: []Stack{NewStack(Bomber, 20)}, Pos: Hex{1, 0}}
	o, _ := w.SubmitMove("u", "f", Hex{1, 0})
	p := w.Tick(o.ArriveAt.Add(time.Second))
	if len(p.Arrived) != 1 || p.Arrived[0] != "f" {
		t.Fatalf("want arrival pulse, got %+v", p.Arrived)
	}
	if len(p.Reports) != 1 {
		t.Fatalf("want battle pulse, got %d", len(p.Reports))
	}
}
