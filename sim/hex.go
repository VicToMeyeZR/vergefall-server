package sim

import "time"

// Hex is an axial-coordinate cell inside a system (GDD: systems are
// internally hex-based; "empty space is empty" is a rendering rule, not a
// data rule — the server only stores occupied hexes).
type Hex struct {
	Q int `json:"q"`
	R int `json:"r"`
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// Dist is axial hex distance.
func (a Hex) Dist(b Hex) int {
	dq := a.Q - b.Q
	dr := a.R - b.R
	return (abs(dq) + abs(dr) + abs(dq+dr)) / 2
}

// TravelTime maps in-system hex distance onto the LOCKED timer bands
// (GDD 5.1.1): nearby hexes 1–5m, across a system 5–20m. Linear within the
// band; cross-system travel (jump lanes) is a separate order type post-M1.
//
// Tuning constant: minutes per hex chosen so a ~500-hex system's long axis
// lands near the 20-minute ceiling.
const minutesPerHex = 0.9

func TravelTime(from, to Hex) time.Duration {
	d := from.Dist(to)
	if d == 0 {
		return 0
	}
	m := float64(d) * minutesPerHex
	if m < 1 {
		m = 1
	}
	if m > 20 {
		m = 20
	}
	return time.Duration(m * float64(time.Minute))
}

// FuelCost returns Volatiles consumed by a fleet over a move. Fuel is
// strategy, not chores: the client shows range rings; the server just
// enforces the arithmetic.
func FuelCost(f *Fleet, from, to Hex) float64 {
	d := float64(from.Dist(to))
	var perHex float64
	for _, s := range f.Stacks {
		if s.Count > 0 {
			perHex += StatsFor(s.Class).FuelPerHex * float64(s.Count)
		}
	}
	return perHex * d
}
