package sim

import (
	"testing"
	"time"
)

func mkFleet(id, owner string, stacks ...Stack) *Fleet {
	return &Fleet{ID: id, OwnerID: owner, Stacks: stacks}
}

// TestPillarOne_CounterCompWinsInBand is the v1.2 verification transplanted
// into code: at EQUAL COST, the hard-counter composition must win, with
// winner casualties inside the owner-locked 15–30% band (workbook v1.2
// reference point: 22%).
func TestPillarOne_CounterCompWinsInBand(t *testing.T) {
	cases := []struct {
		name    string
		counter ShipClass
		victim  ShipClass
	}{
		{"Interceptors_v_Bombers", Interceptor, Bomber},
		{"Bombers_v_Cruisers", Bomber, Cruiser},
		{"Cruisers_v_Interceptors", Cruiser, Interceptor},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const n = 100 // equal cost: identical cost-normalized escorts
			att := mkFleet("A", "attacker", NewStack(tc.counter, n))
			def := mkFleet("D", "defender", NewStack(tc.victim, n))

			rep := Resolve("b1", Hex{0, 0}, att, def, false, time.Unix(0, 0), nil)

			if !rep.Attacker.Won {
				t.Fatalf("counter comp did not win: %+v", rep)
			}
			cr := rep.Attacker.CasualtyRate
			if cr < 0.15 || cr > 0.30 {
				t.Fatalf("winner casualties %.1f%% outside owner band 15–30%%", cr*100)
			}
			t.Logf("winner casualties: %.1f%% (band 15–30%%, v1.2 ref 22%%)", cr*100)
		})
	}
}

// TestEqualCostNeutralIsCoinflipClose: mirror comps at neutral should grind
// to mutual rout or near-symmetric outcome — no hidden cost imbalance.
func TestEqualCostNeutralMirror(t *testing.T) {
	att := mkFleet("A", "attacker", NewStack(Cruiser, 100))
	def := mkFleet("D", "defender", NewStack(Cruiser, 100))
	rep := Resolve("b2", Hex{0, 0}, att, def, false, time.Unix(0, 0), nil)
	if rep.Attacker.Won || rep.Defender.Won {
		t.Fatalf("perfect mirror produced a winner: %s", rep.Narrative)
	}
}

// TestDeterminism: identical inputs must produce identical reports.
func TestDeterminism(t *testing.T) {
	run := func() *BattleReport {
		att := mkFleet("A", "attacker", NewStack(Interceptor, 60), NewStack(Cruiser, 40))
		def := mkFleet("D", "defender", NewStack(Bomber, 70), NewStack(Interceptor, 30))
		return Resolve("b3", Hex{2, -1}, att, def, false, time.Unix(0, 0), nil)
	}
	r1, r2 := run(), run()
	if r1.Narrative != r2.Narrative ||
		r1.Attacker.CasualtyRate != r2.Attacker.CasualtyRate ||
		r1.Defender.CasualtyRate != r2.Defender.CasualtyRate {
		t.Fatal("resolver is not deterministic")
	}
}

// TestHaulerVulnerability: an escorted hauler convoy loses haulers first-ish
// and an unescorted convoy is annihilated by a token raider — hauler
// vulnerability is depth-stack item #1.
func TestHaulerVulnerability(t *testing.T) {
	convoy := mkFleet("C", "trader", NewStack(Hauler, 10))
	raider := mkFleet("R", "raider", NewStack(Interceptor, 5))
	rep := Resolve("b4", Hex{0, 0}, raider, convoy, true, time.Unix(0, 0), nil)
	if !rep.Attacker.Won {
		t.Fatalf("5 interceptors failed to take 10 unescorted haulers")
	}
	if rep.Attacker.CasualtyRate > 0.10 {
		t.Fatalf("raiding unescorted haulers cost %.0f%% — should be nearly free", rep.Attacker.CasualtyRate*100)
	}
}

// TestFlagshipDisabledNotDestroyed: capital loss model — disabled -> repair
// queue; the report must mark it Disabled and the fleet flag must be set.
func TestFlagshipDisabledNotDestroyed(t *testing.T) {
	att := mkFleet("A", "attacker", NewStack(Cruiser, 200))
	def := mkFleet("D", "defender", NewStack(Flagship, 1))
	rep := Resolve("b5", Hex{0, 0}, att, def, true, time.Unix(0, 0), nil)
	var flag *StackReport
	for i := range rep.Defender.Stacks {
		if rep.Defender.Stacks[i].Class == Flagship {
			flag = &rep.Defender.Stacks[i]
		}
	}
	if flag == nil || !flag.Disabled {
		t.Fatal("flagship should be Disabled, not destroyed")
	}
	if flag.Lost != 0 {
		t.Fatal("capitals are never 'Lost'")
	}
	if !def.FlagshipDisabled {
		t.Fatal("fleet FlagshipDisabled flag not set")
	}
}

// TestWreckFieldCreated: losses must leave a salvageable wreck with a decay
// timer — first leg of the MVP acceptance loop.
func TestWreckFieldCreated(t *testing.T) {
	att := mkFleet("A", "attacker", NewStack(Interceptor, 100))
	def := mkFleet("D", "defender", NewStack(Bomber, 100))
	when := time.Unix(1_000_000, 0)
	rep := Resolve("b6", Hex{3, 3}, att, def, false, when, nil)
	if rep.Wreck == nil {
		t.Fatal("no wreck field after a battle with losses")
	}
	if rep.Wreck.Contents.Get(Hullsteel) <= 0 || rep.Wreck.Contents.Get(Components) <= 0 {
		t.Fatal("wreck should contain Hullsteel scrap and Components")
	}
	if got, want := rep.Wreck.DecaysAt, when.Add(WreckDecay); !got.Equal(want) {
		t.Fatalf("decay timer wrong: got %v want %v", got, want)
	}
}

// TestTierFourBothSides: the battle report reveals admiral name + doctrine
// to both parties (a fight is a full scout).
func TestTierFourBothSides(t *testing.T) {
	att := mkFleet("A", "attacker", NewStack(Interceptor, 50))
	att.Admiral = &Admiral{ID: "adm1", Name: "Ress Vahl", Doctrine: Hunter}
	def := mkFleet("D", "defender", NewStack(Bomber, 50))
	def.Admiral = &Admiral{ID: "adm2", Name: "Okoye Marr", Doctrine: Screen}
	rep := Resolve("b7", Hex{0, 0}, att, def, false, time.Unix(0, 0), nil)
	if rep.Attacker.Doctrine != "Hunter" || rep.Defender.Doctrine != "Screen" {
		t.Fatal("doctrines must be visible tier-4 in the report for both sides")
	}
}
