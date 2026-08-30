//go:build nakama

package main

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"sync"

	"github.com/heroiclabs/nakama-common/runtime"
)

const (
	matchModule   = "system"
	opSystemDelta = 1 // personalized SystemView JSON
	opBattleID    = 2 // {"report_id": "..."}  — fetch full report via RPC
	opArrival     = 3 // {"fleet_id": "..."}
)

// systemMatch is a broadcast room, not the galaxy. World is the source of
// truth; this handler never accepts orders over match data.
type systemMatch struct{}

type systemMatchState struct {
	systemID  string
	presences map[string]runtime.Presence
	lastHash  map[string]string
}

func (m *systemMatch) MatchInit(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, params map[string]interface{}) (interface{}, int, string) {
	sid := systemID
	if v, ok := params["system_id"].(string); ok && v != "" {
		sid = v
	}
	label, _ := json.Marshal(map[string]string{"system": sid, "kind": "feed"})
	return &systemMatchState{
		systemID:  sid,
		presences: map[string]runtime.Presence{},
		lastHash:  map[string]string{},
	}, 1, string(label) // 1 Hz feed
}

func (m *systemMatch) MatchJoinAttempt(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, presence runtime.Presence, metadata map[string]string) (interface{}, bool, string) {
	return state, true, ""
}

func (m *systemMatch) MatchJoin(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, presences []runtime.Presence) interface{} {
	s := state.(*systemMatchState)
	for _, p := range presences {
		s.presences[p.GetSessionId()] = p
		delete(s.lastHash, p.GetSessionId())
	}
	return s
}

func (m *systemMatch) MatchLeave(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, presences []runtime.Presence) interface{} {
	s := state.(*systemMatchState)
	for _, p := range presences {
		delete(s.presences, p.GetSessionId())
		delete(s.lastHash, p.GetSessionId())
	}
	return s
}

func (m *systemMatch) MatchLoop(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, messages []runtime.MatchData) interface{} {
	s := state.(*systemMatchState)
	// Orders belong on RPCs. Drop anything a client stuffed into the pipe.
	_ = messages

	if len(s.presences) == 0 {
		return s // stay alive; the galaxy ticks whether anyone is watching
	}

	w := currentWorld()
	if w == nil {
		return s
	}

	for _, p := range s.presences {
		worldMu.RLock()
		view := w.SystemViewFor(p.GetUserId())
		worldMu.RUnlock()
		payload, err := json.Marshal(view)
		if err != nil {
			continue
		}
		sum := sha1.Sum(payload)
		h := hex.EncodeToString(sum[:])
		if s.lastHash[p.GetSessionId()] == h {
			continue
		}
		s.lastHash[p.GetSessionId()] = h
		_ = dispatcher.BroadcastMessage(opSystemDelta, payload, []runtime.Presence{p}, nil, true)
	}
	return s
}

func (m *systemMatch) MatchTerminate(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, graceSeconds int) interface{} {
	return state
}

func (m *systemMatch) MatchSignal(ctx context.Context, logger runtime.Logger, db *sql.DB, nk runtime.NakamaModule, dispatcher runtime.MatchDispatcher, tick int64, state interface{}, data string) (interface{}, string) {
	return state, ""
}

var (
	cachedMatchID string
	cachedMatchMu sync.Mutex
)

func ensureSystemMatch(ctx context.Context, nk runtime.NakamaModule) (string, error) {
	cachedMatchMu.Lock()
	defer cachedMatchMu.Unlock()
	if cachedMatchID != "" {
		return cachedMatchID, nil
	}
	min, max := 0, 256
	list, err := nk.MatchList(ctx, 8, true, "", &min, &max, "+label.system:"+systemID)
	if err == nil {
		for _, mt := range list {
			if mt != nil && mt.GetMatchId() != "" {
				cachedMatchID = mt.GetMatchId()
				return cachedMatchID, nil
			}
		}
	}
	id, err := nk.MatchCreate(ctx, matchModule, map[string]interface{}{"system_id": systemID})
	if err != nil {
		return "", err
	}
	cachedMatchID = id
	return id, nil
}
