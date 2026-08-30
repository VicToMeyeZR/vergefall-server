package sim

import (
	"encoding/json"
	"fmt"
	"time"
)

// SnapshotVersion bumps when the persisted shape changes. Loader refuses
// unknown future versions rather than silently misreading a galaxy.
const SnapshotVersion = 1

// Snapshot is the durable form of one system. The sim stays in-memory for
// the tick; Nakama storage (or any blob store) is just the bookkeeper so a
// restart does not goldfish the rim.
type Snapshot struct {
	Version        int                      `json:"version"`
	SystemID       string                   `json:"system_id"`
	Now            time.Time                `json:"now"`
	Fleets         map[string]*Fleet        `json:"fleets"`
	Wrecks         map[string]*WreckField   `json:"wrecks"`
	Reports        map[string]*BattleReport `json:"reports"`
	ReportsByOwner map[string][]string      `json:"reports_by_owner"`
	Orders         []*Order                 `json:"orders"`
	BattleSeq      int                      `json:"battle_seq"`
	WreckSeq       int                      `json:"wreck_seq"`
}

// MarshalSnapshot is the persist boundary. All mutation still flows through
// World methods; this is a freeze-frame, not a second source of truth.
func (w *World) MarshalSnapshot() ([]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	snap := Snapshot{
		Version:        SnapshotVersion,
		SystemID:       w.SystemID,
		Now:            w.Now,
		Fleets:         w.Fleets,
		Wrecks:         w.Wrecks,
		Reports:        w.Reports,
		ReportsByOwner: w.ReportsByOwner,
		Orders:         w.orders,
		BattleSeq:      w.battleSeq,
		WreckSeq:       w.wreckSeq,
	}
	return json.Marshal(snap)
}

// UnmarshalSnapshot restores a World. Maps are re-initialized if a legacy
// blob omitted them so Tick never nil-derefs.
func UnmarshalSnapshot(raw []byte) (*World, error) {
	var snap Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, err
	}
	if snap.Version != SnapshotVersion {
		return nil, fmt.Errorf("unsupported snapshot version %d", snap.Version)
	}
	w := NewWorld(snap.SystemID, snap.Now)
	if snap.Fleets != nil {
		w.Fleets = snap.Fleets
	}
	if snap.Wrecks != nil {
		w.Wrecks = snap.Wrecks
	}
	if snap.Reports != nil {
		w.Reports = snap.Reports
	}
	if snap.ReportsByOwner != nil {
		w.ReportsByOwner = snap.ReportsByOwner
	}
	w.orders = snap.Orders
	w.battleSeq = snap.BattleSeq
	w.wreckSeq = snap.WreckSeq
	return w, nil
}
