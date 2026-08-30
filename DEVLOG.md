# Vergefall Dev Log

Per standing directions §1: platform/engine/implementation decisions recorded
with reasoning and date, so later agents inherit the context.

## 2026-08-30 — Server rebuild: chassis, not rooms

Nakama stays. The **match engine is not the galaxy.** Rebuild implements the
architecture that investigation locked:

| Surface | Primitive | Why |
|---|---|---|
| Writes (move, salvage, enlist) | RPCs | Authoritative, auditable, work with the socket closed |
| Sim clock | Process ticker 5s | Galaxy runs empty; MatchLoop is the wrong clock |
| “You fought / you arrived” | `NotificationSend` | Kills poll-on-`battle_reports` for live clients |
| Live hex occupancy | Authoritative match **as a room** at 1 Hz | Presence + personalized `SystemView`; match data is ignored |
| Restart | Storage snapshot every 15s | In-memory World was a goldfish (DEVLOG live deploy) |
| Image | Multi-stage Dockerfile | `.so` baked against 3.21.1 — Railway/VPS cannot volume-mount a Windows plugin |

Deliberately **not** done: putting `World` in `MatchInit` state, relayed
matches, accepting orders over opcodes, app-sleep/scale-to-zero.

New RPCs: `vergefall.system_join` (returns match id), `vergefall.whoami`.
`SystemView.hostiles` lists pirate hexes with no stack counts (M1 fog; the
steel thread still has a destination). JSON now emits class/doctrine names
and `q`/`r` — breaking vs this morning's integer enums; clients must follow.

Snapshot restore is covered by `TestSnapshotRoundtrip` (in-flight order
survives a freeze-frame and still resolves the fight).

Host: always-on box. Railway is viable if Serverless stays off and 7350 is
the public port; a VPS still matches compose more cheaply. Heroic Cloud
remains the growth path.

## 2026-08-30 — M1 steel thread: server foundation built & tested

**What exists now:** `vergefall-server/` — pure-Go simulation core
(`sim/`, 11 passing tests) + Nakama runtime adapter + docker-compose.
The M1 milestone sentence (login → hex system → fleet order → server tick →
pirate battle auto-resolves in a Go module → battle report returns) is
implemented server-side and proven by `TestSteelThread`, which also covers
the first two legs of the MVP acceptance loop (…→ wreck → salvage).
Login itself is Nakama's built-in auth; the adapter maps the authenticated
user id onto sim ownership.

### Implementation decisions (all reversible, flagged where owner input helps)

| # | Decision | Reasoning |
|---|----------|-----------|
| 1 | **Sim core is dependency-free Go (stdlib only), Nakama adapter is a separate build-tagged package.** | The deterministic simulation must be testable without Docker/Nakama; balance rules become unit tests. Also a practical constraint: this dev sandbox cannot fetch `nakama-common` (Go module proxy not on the network allowlist), so the adapter compiles on the owner's machine via the pluginbuilder image instead. Clean architecture either way. |
| 2 | **Rout threshold 0.63** (a side withdraws below 63% of starting strength; scripted pirates and hex-holding defenders fight on). | This is the tuning knob that puts winner casualties at 22.5% in the hard-counter equal-cost case — inside the owner band (15–30%) and matching the v1.2 workbook reference (22%). Lanchester without a rout rule lands ~41%, outside the band. **Workbook v2 should confirm or replace this rule.** |
| 3 | **Triangle assignment: Interceptor > Bomber > Cruiser > Interceptor; every combat class hard-counters Haulers; Flagship neutral vs all.** | GDD locks the band and the existence of the triangle but not the direction. This mapping reads intuitively (fast catches slow, payload cracks armor, armor swats fast). One-line change if the workbook says otherwise. |
| 4 | **Provisional escort stats: identical cost-normalized HP/ATK (100/10 per 100 Hullsteel).** | Enforces the v1.2 cost-parity finding structurally — differentiation lives only in the counter matrix, so the pillar-1 test isolates composition. Real stats come from workbook v2. |
| 5 | **Wreck contents: 50% of destroyed Hullsteel value, split 70% Hullsteel / 30% Components; 3h decay.** | Placeholder economics honoring "salvage is the fastest Components source" and the mockup's "Wreck field · 3h" tile. Needs an economy-tuning pass. |
| 6 | **DoctrineEffects is an interface already threaded through the resolver; M1 ships a no-op.** | Doctrine effects are the flagged critical path (workbook v2). When specced, implementations plug in with zero resolver rework. Doctrine tags already appear in tier-4 reports. |
| 7 | **Nakama 3.21 + Postgres 15 pinned in compose.** | Pin versions so pluginbuilder/server Go versions match. Verify latest stable Nakama when the owner first deploys (per standing directions: verify at implementation time). |

### Critical-path items still blocking (unchanged from GDD)
1. **Doctrine effects mini-spec** (combat workbook v2) — blocks real combat depth; socket is ready.
2. **Build-stage vulnerability ruling** — blocks the M3 depot project; no code impact yet.
3. (Art, not server) **Teal color collision** — Components icon vs player identity; owner ruling pending per GDD 6.0.1 note.

### Next up (proposed)
- **M2 groundwork:** relational persistence beyond the JSON snapshot; five-system MVP
  topology + jump lanes; mining, repair queue mechanics, capture roll +
  escalating ransom (formula is fully specced in GDD 6.3 — implementable now).
- Wire the UE5 / Helm Nakama SDK to the RPCs + socket feed.

## 2026-08-30 (later) — First live deployment + first live battle

**Deployment verified on owner's machine** (Windows / Docker Desktop). Server
boots clean; all four RPCs registered; steel thread executed live end-to-end.

### Build-recipe corrections discovered during deployment (README updated)
1. `go.mod` must declare **go 1.21** — pluginbuilder 3.21.1 ships Go 1.21.6
   and refuses a 1.22 module.
2. Build command needs **`-tags nakama`** (adapter is build-tagged).
3. **nakama-common must be v1.31.0** for Nakama 3.21.x (per official release
   notes). v1.30.1 compiles but is rejected at plugin load with
   "built with a different version of package". Now pinned in go.mod.
4. PowerShell: use `${PWD}` not `$PWD` in the volume mount.
5. Added `--session.token_expiry_sec 7200` to compose for dev ergonomics
   (default 60s tokens are unusable for hand-driven testing).

### First live battle (battle-rim-1-1..3) — findings
- **Composition read validated in the wild:** owner's mixed 20-Interceptor /
  10-Cruiser fleet lost its opening engagement vs 30 pure Bombers — the
  cruiser third of the fleet was hard-countered (Bomber>Cruiser 1.25) and
  diluted the interceptor counter. Working as designed (pillar 1).
- **BUG FOUND & FIXED — rout was not withdrawal:** a routed fleet stayed on
  the hex and re-engaged every 5s tick until mutual grinding (3 battles in
  15 seconds). Fix: rout now displaces the fleet one hex toward home +
  10-minute reform cooldown before it may engage again (`ReformTime`).
  Regression test reproduces the exact live scenario.
- **Wreck fields now merge per hex** (three stacked fields from the grind
  looked wrong; one debris field per hex, decay clock refreshed).
- **Gap fixed — wrecks were invisible to clients:** added
  `vergefall.system_view` RPC (own fleets + wreck fields with salvage ids).
  The salvage loop was untestable live without it.

### Tuning note for workbook v2
Pirates fight to destruction while players rout at 63% — deliberate for M1,
but "defender fights to death" plus rout-withdrawal means a losing attack now
costs ~37% and a failed hex stays hostile. Revisit whether NPC pirates should
also rout (fleeing pirates could be a chase mechanic for Hunter doctrine).
