//go:build nakama

// Nakama runtime module — thin adapter. The galaxy lives in package sim.
//
// Architecture (locked 2026-08-30, rebuilt same day):
//   - RPCs are the write path (enlist, fleet_order, salvage).
//   - A process ticker (5s) is the sim clock. NOT MatchLoop.
//   - After each tick, notifications push battle/arrival events.
//   - A long-lived authoritative match per system is a broadcast room only:
//     1 Hz personalized SystemView. Orders sent as match data are ignored.
//   - World snapshots go to Nakama storage so a restart does not goldfish rim-1.
package main

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/heroiclabs/nakama-common/runtime"

	"vergefall/server/sim"
)

const (
	systemID        = "rim-1"
	tickInterval    = 5 * time.Second
	persistEvery    = 15 * time.Second
	notifyBattle    = 1
	notifyArrival   = 2
	notifySalvage   = 3
	collectionWorld = "vergefall_world"
	storageKey      = "rim-1"
)

var (
	world   *sim.World
	worldMu sync.RWMutex
)

func InitModule(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, initializer runtime.Initializer) error {
	world = loadWorld(ctx, nk, logger)

	if err := initializer.RegisterRpc("vergefall.get_fleet", rpcGetFleet); err != nil {
		return err
	}
	if err := initializer.RegisterRpc("vergefall.fleet_order", rpcFleetOrder); err != nil {
		return err
	}
	if err := initializer.RegisterRpc("vergefall.battle_reports", rpcBattleReports); err != nil {
		return err
	}
	if err := initializer.RegisterRpc("vergefall.enlist", rpcEnlist); err != nil {
		return err
	}
	if err := initializer.RegisterRpc("vergefall.system_view", rpcSystemView); err != nil {
		return err
	}
	if err := initializer.RegisterRpc("vergefall.system_join", rpcSystemJoin); err != nil {
		return err
	}
	if err := initializer.RegisterRpc("vergefall.whoami", rpcWhoami); err != nil {
		return err
	}

	if err := initializer.RegisterMatch(matchModule, func(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule) (runtime.Match, error) {
		return &systemMatch{}, nil
	}); err != nil {
		return err
	}

	go runTicker(ctx, logger, nk)

	logger.Info("Vergefall module loaded — system %s online (rpc writes, ticker sim, match-as-room feed, storage snapshot)", systemID)
	return nil
}

func runTicker(ctx context.Context, logger runtime.Logger, nk runtime.NakamaModule) {
	t := time.NewTicker(tickInterval)
	defer t.Stop()
	p := time.NewTicker(persistEvery)
	defer p.Stop()
	for {
		select {
		case <-ctx.Done():
			saveWorld(context.Background(), nk, logger)
			return
		case now := <-t.C:
			pulse := tickWorld(now.UTC())
			pushPulse(ctx, nk, logger, pulse)
		case <-p.C:
			saveWorld(ctx, nk, logger)
		}
	}
}

func tickWorld(now time.Time) sim.Pulse {
	worldMu.Lock()
	defer worldMu.Unlock()
	return world.Tick(now)
}

func currentWorld() *sim.World {
	worldMu.RLock()
	defer worldMu.RUnlock()
	return world
}

func callerID(ctx context.Context) (string, error) {
	uid, ok := ctx.Value(runtime.RUNTIME_CTX_USER_ID).(string)
	if !ok || uid == "" {
		return "", runtime.NewError("authentication required", 16) // UNAUTHENTICATED
	}
	return uid, nil
}

func seedPirates(w *sim.World) {
	spots := []sim.Hex{{Q: 4, R: -2}, {Q: -3, R: 5}, {Q: 6, R: 1}}
	for i, h := range spots {
		id := "npc-pirate-" + string(rune('a'+i))
		w.Fleets[id] = &sim.Fleet{
			ID: id, OwnerID: "npc:pirate",
			Stacks: []sim.Stack{sim.NewStack(sim.Bomber, 30+10*i)},
			Pos:    h,
		}
	}
}

func fleetID(uid string) string { return "fleet-" + uid }
