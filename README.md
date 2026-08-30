# Vergefall: Empires — Server

Server-authoritative galaxy simulation per GDD §8.
**Nakama is the chassis, not the room.** The galaxy is a Go `World`. Nakama
provides auth, RPCs, sockets, storage, and a 1 Hz *feed* match.

```
UE5 / Helm
    │  RPCs (writes)                 socket (one connection)
    │  enlist, fleet_order,          join system match
    │  salvage                       receive deltas + notifications
    ▼
Nakama module
    ├── RPCs        → World.SubmitMove / SubmitSalvage
    ├── ticker 5s   → World.Tick          (the sim clock)
    ├── after tick  → NotificationSend    (battle, arrival)
    ├── match 1 Hz  → personalized SystemView  (broadcast room, not truth)
    └── storage     → World snapshot every 15s
World (pure Go)  ──snapshot──► Nakama storage  (Postgres underneath)
```

Orders sent as match data are **dropped**. Relayed matches are not used.

## Layout

```
sim/       Pure-Go, dependency-free simulation core (unit tested)
  types.go   Resources, ship classes, counter matrix, admirals
  json.go    Name-based JSON for class/doctrine/order kind (q/r hexes)
  hex.go     Axial hex math, travel-time bands (GDD 5.1.1)
  combat.go  v1.2 resolver
  tick.go    World, orders, Tick → Pulse (reports, arrivals, salvage, decay)
  persist.go Snapshot / restore (restart must not goldfish the rim)
nakama/    Thin adapter (build tag: nakama)
  RPCs: vergefall.enlist / whoami / get_fleet / fleet_order /
        battle_reports / system_view / system_join
  Match module "system": 1 Hz feed, opcode 1 = SystemView
  Notifications: code 1 battle, code 2 arrival (persistent for battles)
Dockerfile         Multi-stage plugin bake (pluginbuilder 3.21.1 → nakama 3.21.1)
docker-compose.yml Postgres 15 + the baked image
```

## Tests (no Nakama needed)

```
go test ./sim/ -v
```

Steel thread, counter band, rout-is-withdrawal, wreck merge/decay, fuel gate,
snapshot round-trip, JSON names, hostile fog in system view.

## Run locally

```
docker compose up --build
```

Success log: `Vergefall module loaded — system rim-1 online` and
`Found runtime modules ... count:1`.

Console: http://127.0.0.1:7351  (admin / localdev — change it).

### Client steel thread

1. Authenticate (device auth is fine).
2. Open a socket.
3. `vergefall.enlist`
4. `vergefall.system_join` → `{ match_id }` then `socket.joinMatch(match_id)`
5. `vergefall.fleet_order {"fleet_id":"...","kind":"move","q":4,"r":-2}`
6. Wait for notification code 1 (battle) **or** match opcode 1 (system delta
   showing a wreck) — do not poll `battle_reports` unless recovering.

`system_view.hostiles` is fog: pirate hexes, no stack counts. Composition is
the battle report.

### Match opcodes (server → client only)

| op | payload |
|---:|---|
| 1 | personalized `SystemView` JSON |
| 2 | reserved (`{"report_id"}`) |
| 3 | reserved (`{"fleet_id"}`) |

## Railway / any always-on box

Sleep / Serverless / scale-to-zero **off**. A sleeping galaxy is a broken galaxy.

- Build from this Dockerfile (plugin is baked; do not volume-mount a Windows `.so`).
- Separate Postgres. Set `DATABASE_URL` or `DATABASE_ADDRESS`.
- Public HTTPS on **7350** (HTTP + WebSocket) for the JS Helm.
- UE5 gRPC is **7349** — native port on a VPS; Railway TCP proxy is a last resort.
- Console 7351 stays private.

Version pairing is strict: **pluginbuilder 3.21.1 / nakama 3.21.1 / nakama-common v1.31.0**.

## Deliberate M1 simplifications

- One system (`rim-1`). Five-system topology is M2; snapshot shape is already
  per-system so that swap does not touch the resolver.
- Doctrine effects still no-op (`DoctrineEffects` / `NoDoctrineEffects`).
- Station economy, mining, capture/ransom: M2/M3.
- Snapshot is a JSON blob in Nakama storage, not a relational galaxy schema.
