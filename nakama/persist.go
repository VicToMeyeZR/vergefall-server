//go:build nakama

package main

import (
	"context"
	"time"

	"github.com/heroiclabs/nakama-common/runtime"

	"vergefall/server/sim"
)

func loadWorld(ctx context.Context, nk runtime.NakamaModule, logger runtime.Logger) *sim.World {
	objects, err := nk.StorageRead(ctx, []*runtime.StorageRead{{
		Collection: collectionWorld,
		Key:        storageKey,
		UserID:     "",
	}})
	if err != nil {
		logger.Warn("world storage read failed, seeding rim-1: %v", err)
		return freshWorld()
	}
	if len(objects) == 0 || objects[0] == nil || objects[0].GetValue() == "" {
		logger.Info("no world snapshot — seeding rim-1")
		return freshWorld()
	}
	w, err := sim.UnmarshalSnapshot([]byte(objects[0].GetValue()))
	if err != nil {
		logger.Error("world snapshot corrupt, reseeding: %v", err)
		return freshWorld()
	}
	logger.Info("restored %s from storage (%d fleets, %d wrecks)", w.SystemID, len(w.Fleets), len(w.Wrecks))
	return w
}

func freshWorld() *sim.World {
	w := sim.NewWorld(systemID, time.Now().UTC())
	seedPirates(w)
	return w
}

func saveWorld(ctx context.Context, nk runtime.NakamaModule, logger runtime.Logger) {
	worldMu.RLock()
	raw, err := world.MarshalSnapshot()
	worldMu.RUnlock()
	if err != nil {
		logger.Error("snapshot marshal: %v", err)
		return
	}
	if _, err := nk.StorageWrite(ctx, []*runtime.StorageWrite{{
		Collection:      collectionWorld,
		Key:             storageKey,
		UserID:          "",
		Value:           string(raw),
		PermissionRead:  0,
		PermissionWrite: 0,
	}}); err != nil {
		logger.Error("world storage write: %v", err)
	}
}

func pushPulse(ctx context.Context, nk runtime.NakamaModule, logger runtime.Logger, pulse sim.Pulse) {
	for _, rep := range pulse.Reports {
		content := map[string]interface{}{
			"report_id": rep.ID,
			"hex":       map[string]int{"q": rep.Hex.Q, "r": rep.Hex.R},
			"won":       rep.Attacker.Won,
		}
		if err := notifyUser(ctx, nk, rep.Attacker.OwnerID, "Battle report", content, notifyBattle); err != nil {
			logger.Warn("notify battle %s: %v", rep.Attacker.OwnerID, err)
		}
	}
	for _, id := range pulse.Arrived {
		worldMu.RLock()
		f := world.Fleets[id]
		var uid string
		if f != nil {
			uid = f.OwnerID
		}
		worldMu.RUnlock()
		if uid == "" || uid == "npc:pirate" {
			continue
		}
		_ = notifyUser(ctx, nk, uid, "Fleet arrived", map[string]interface{}{"fleet_id": id}, notifyArrival)
	}
}

func notifyUser(ctx context.Context, nk runtime.NakamaModule, userID, subject string, content map[string]interface{}, code int) error {
	if userID == "" || userID == "npc:pirate" {
		return nil
	}
	persistent := code == notifyBattle
	return nk.NotificationSend(ctx, userID, subject, content, code, "", persistent)
}
