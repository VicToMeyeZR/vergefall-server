//go:build nakama

package main

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/heroiclabs/nakama-common/runtime"

	"vergefall/server/sim"
)

func rpcEnlist(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	uid, err := callerID(ctx)
	if err != nil {
		return "", err
	}
	id := fleetID(uid)
	worldMu.Lock()
	if _, exists := world.Fleets[id]; !exists {
		world.Fleets[id] = &sim.Fleet{
			ID: id, OwnerID: uid,
			Admiral: &sim.Admiral{ID: "adm-" + uid, Name: "First Officer", Doctrine: sim.Screen},
			Stacks:  []sim.Stack{sim.NewStack(sim.Interceptor, 20), sim.NewStack(sim.Cruiser, 10)},
			Pos:     sim.Hex{Q: 0, R: 0}, Fuel: 5000,
		}
	}
	worldMu.Unlock()
	b, _ := json.Marshal(map[string]string{"fleet_id": id, "user_id": uid})
	return string(b), nil
}

func rpcWhoami(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	uid, err := callerID(ctx)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(map[string]string{"user_id": uid, "fleet_id": fleetID(uid)})
	return string(b), nil
}

func rpcGetFleet(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	uid, err := callerID(ctx)
	if err != nil {
		return "", err
	}
	var req struct {
		FleetID string `json:"fleet_id"`
	}
	if err := json.Unmarshal([]byte(payload), &req); err != nil {
		return "", runtime.NewError("bad payload", 3)
	}
	worldMu.RLock()
	v, err := world.FleetViewFor(uid, req.FleetID)
	worldMu.RUnlock()
	if err != nil {
		return "", runtime.NewError(err.Error(), 5)
	}
	b, _ := json.Marshal(v)
	return string(b), nil
}

func rpcFleetOrder(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	uid, err := callerID(ctx)
	if err != nil {
		return "", err
	}
	var req struct {
		FleetID string `json:"fleet_id"`
		Kind    string `json:"kind"` // "move" | "salvage"
		Q       int    `json:"q"`
		R       int    `json:"r"`
		WreckID string `json:"wreck_id"`
	}
	if err := json.Unmarshal([]byte(payload), &req); err != nil {
		return "", runtime.NewError("bad payload", 3)
	}
	worldMu.Lock()
	var o *sim.Order
	switch req.Kind {
	case "move":
		o, err = world.SubmitMove(uid, req.FleetID, sim.Hex{Q: req.Q, R: req.R})
	case "salvage":
		o, err = world.SubmitSalvage(uid, req.FleetID, req.WreckID)
	default:
		worldMu.Unlock()
		return "", runtime.NewError("unknown order kind", 3)
	}
	worldMu.Unlock()
	if err != nil {
		return "", runtime.NewError(err.Error(), 9) // FAILED_PRECONDITION
	}
	resp, _ := json.Marshal(map[string]any{
		"arrive_at": o.ArriveAt,
		"from":      o.From,
		"dest":      o.Dest,
		"kind":      o.Kind.String(),
	})
	return string(resp), nil
}

func rpcBattleReports(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	uid, err := callerID(ctx)
	if err != nil {
		return "", err
	}
	worldMu.RLock()
	reps := world.ReportsFor(uid)
	worldMu.RUnlock()
	b, _ := json.Marshal(reps)
	return string(b), nil
}

func rpcSystemView(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	uid, err := callerID(ctx)
	if err != nil {
		return "", err
	}
	worldMu.RLock()
	v := world.SystemViewFor(uid)
	worldMu.RUnlock()
	b, _ := json.Marshal(v)
	return string(b), nil
}

func rpcSystemJoin(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, payload string) (string, error) {
	if _, err := callerID(ctx); err != nil {
		return "", err
	}
	id, err := ensureSystemMatch(ctx, nk)
	if err != nil {
		logger.Error("system match: %v", err)
		return "", runtime.NewError("system feed unavailable", 13)
	}
	b, _ := json.Marshal(map[string]string{"match_id": id, "system_id": systemID})
	return string(b), nil
}
