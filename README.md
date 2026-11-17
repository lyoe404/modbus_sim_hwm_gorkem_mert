# Industrial Hardware Monitoring & Control Platform

A Go backend that polls independent Modbus TCP simulators over real sockets, persists readings, switches relays with read-back confirmation, applies temperature-driven hysteresis control, and gates everything behind capability-checked JWTs. A React UI, Prometheus and Grafana complete the stack, all started with one `docker compose up`.

**Assessment:** Industrial Hardware Monitoring & Control, Technical Assessment v3.1
**Scope delivered:** Core complete. Extended: simulators for thermocouple, PWM and remote reset exist, with partial backend support. The Django identity service (§2.11) is not built.

## 1. Scope

### Core (target scope)

| Brief item | Status | Notes |
|---|---|---|
| §2.1 Temp/humidity sensor | Done | Separate Modbus TCP simulator, bounded random walk (18–35 °C, 30–80 %RH), polled every 2 s, persisted with timestamps, retry with backoff, visible connection status |
| §2.2 Relay switching | Done | 8 coils, labels from config, write followed by read-back, distinct `write_failed` / `readback_failed` / `readback_mismatch` errors |
| §2.4 Go backend | Done | Layered packages, one goroutine per device, context cancellation, clean shutdown, `go test -race` clean |
| §2.5 Hysteresis fan control | Done | Two thresholds, runtime-editable, AUTO/MANUAL mode, coil reconciliation, every transition audited with its trigger value |
| §2.7 React UI | Done | Login, live charts (2 s polling), relay controls, threshold form, event log, connection/freshness states, controls disabled per granted capability |
| §2.8 Prometheus + Grafana | Done | `/metrics`, Prometheus scrape, provisioned dashboard, a Prometheus-sourced value shown in the UI |
| §2.10 Auth baseline | Done | JWT, viewer/operator, bcrypt, signing key from environment, 401 vs 403, `token_expired` returns the UI to login |
| §3 Mandatory platform | Done | One-command Compose, two-role JWT, Grafana with an importable dashboard JSON |

### Extended

| Brief item | Status | What exists / what is missing |
|---|---|---|
| §2.3 Thermocouple | Partial | 4-channel simulator with per-channel `0x8000` fault, backend poller, SQLite history (`thermocouple_ch0..3`). **Missing:** channel fault state in the API and UI (a faulted channel is currently omitted from the data, not reported), UI panel, metrics |
| §2.6 Variable fan speed | Partial | PWM simulator, `GET/POST /api/fan/speed` with read-back, command/feedback mismatch flag, audited writes, poller. **Missing:** temperature-to-speed curve, manual/auto speed mode, UI controls, mismatch shown in the UI |
| §2.9 Remote reset | Partial | TCP reset simulator (`PING`, `STATUS`, `RESET <device>`, four targets, timed auto-recovery, tests). **Missing:** backend client and `POST /api/reset` guarded by `write:reset`, audit with the JWT username, UI confirmation, "unknown" marking of dependent data |
| §2.11 Django identity service | Not built | See section 11 for the plan |
| WebSocket / SSE | Not built | The UI polls; the brief accepts polling |
| Bonus (alerts, RTU, config history, tracing, health score, presets) | Not built | Unit and integration tests exist; config changes are logged with old and new values in the event log |

**Scope decision.** §2.11 is Extended and the brief says a clean §2.10 baseline is a complete Core submission. I prioritised a verified Core over spreading thin. Routes already check capabilities from JWT claims, not roles, so adding admin-managed grants later changes where `perms` comes from, not any route.

## 2. Quick start

Requirements: Docker with Compose. Python 3 is only needed for the integration scripts, Go only for running tests outside Docker.

```bash
cp .env.example .env          # Windows PowerShell: Copy-Item .env.example .env
docker compose up --build -d
docker compose ps
curl http://localhost:8080/healthz
```

Stop with `docker compose down`; add `-v` to also delete the database and monitoring volumes.

| Service | URL | Notes |
|---|---|---|
| React UI | http://localhost:3000 | Nginx serves the UI and proxies `/api/` to the backend |
| Go API | http://localhost:8080 | `/healthz` and `/metrics` are unauthenticated, `/api/*` needs a JWT |
| Prometheus | http://localhost:9090 | Scrapes the backend every 2 s |
| Grafana | http://localhost:3001 | Dashboard "Industrial Hardware Monitoring & Control" is provisioned |
| Sensor simulator admin | http://127.0.0.1:5121 | Fault injection, temperature pinning. Loopback only |
| Relay / thermocouple / PWM admin | http://127.0.0.1:5122 / 5123 / 5124 | Loopback only |
| Remote reset simulator | 127.0.0.1:9001 | Raw TCP text protocol |

Modbus ports 5021–5024 exist only inside the Compose network.

### Test logins

Accounts are created at startup from `.env`. With the values in `.env.example`:

| Role | Username | Password | Capabilities |
|---|---|---|---|
| Viewer | `viewer` | `viewer-dev-pass` | all `read:*`, no `write:*` |
| Operator | `operator` | `operator-dev-pass` | all `read:*` and all `write:*` |
| Grafana | `admin` | `example-pass` (`GRAFANA_PASSWORD`) | Grafana admin |

These are development placeholders, not secrets. Change them in `.env` for anything beyond a local demo. `.env` is git-ignored; `.env.example` lists every required variable.

### Smoke test (reviewer path)

```bash
TOKEN=$(curl -s localhost:8080/api/auth/login -H 'Content-Type: application/json' \
  -d '{"username":"operator","password":"operator-dev-pass"}' | sed 's/.*"token":"\([^"]*\)".*/\1/')

curl -s localhost:8080/api/devices -H "Authorization: Bearer $TOKEN"
curl -s -X POST localhost:8080/api/relays/3 -H "Authorization: Bearer $TOKEN" -d '{"on":true}'   # 200, read-back state
```

Repeat the relay call with the viewer's token: `403`. An operator write to the AUTO-controlled fan channel (CH0) returns `409`; other channels stay writable.

## 3. Architecture

```
                 React + TypeScript (Nginx :3000)
                              |  REST (polling)
                              v
        Go backend :8080  (REST, JWT capability guard, /metrics)
        pollers (goroutine per device) | control engine | SQLite
                              |  Modbus TCP
     +-----------+------------+------------+-----------+
  sensor:5021  relay:5022  thermocouple:5023  pwm:5024      reset TCP :9001
     (separate simulator processes, own Go module)

        Prometheus :9090 --scrapes /metrics--> Grafana :3001
```

```
backend/
  cmd/backend/       wiring, config from env, graceful shutdown
  internal/poller/   one goroutine per device; backoff + jitter; online/degraded/offline
  internal/relay/    coil write + read-back; distinct error kinds
  internal/control/  pure hysteresis rule + Engine (runtime config, safety rules, audit)
  internal/speed/    PWM Modbus client (Extended)
  internal/store/    SQLite: measurements, events, device_states, config
  internal/auth/     JWT issue/verify, bcrypt users, capability catalog, Guard middleware
  internal/api/      REST routes (each guarded by its own capability), /metrics
  internal/envfile/  optional .env loader (real environment wins)
simulators/          cmd/{temp-humidity,relay,thermocouple,speed-pwm,remote-reset}-sim, internal/*
frontend/            React + Vite + Tailwind, served by Nginx
observability/       Prometheus config, Grafana provisioning and dashboard JSON
scripts/             smoke_test.py, hysteresis_integration.py, fault_recovery_integration.py, race_tests.ps1
```

### Design decisions

- **Go `net/http` with the standard mux, no framework.** The route table is small and the capability guard is about 30 lines, so a framework adds nothing.
- **SQLite (`modernc.org/sqlite`, pure Go) instead of Postgres/Timescale.** One file, no cgo, trivial in Docker. A single connection with WAL and `busy_timeout` avoids writer contention. Four tables keep concerns separate: `measurements`, `events` (audit), `device_states`, `config`.
- **HS256 shared secret instead of RS256.** The brief allows this simplification. It is sufficient while the Go backend issues its own tokens. With a Django issuer it should become RS256 so the backend holds only the public key.
- **Capabilities, not roles.** Every route requires specific capabilities from the token's `perms` claim. A role is shorthand that `auth.PermsForRole` expands to a list.
- **401 vs 403.** Missing, invalid or expired token: 401 (expired uses the distinct code `token_expired`). Valid token without the capability: 403. Only HS256 is accepted, so `alg: none` is rejected (tested).
- **Login hardening.** bcrypt; unknown usernames are compared against a dummy hash so timing does not reveal which users exist.
- **REST polling instead of WebSocket/SSE.** Allowed by the brief; keeps the UI simple. A reading is shown as LIVE only if the sensor is online and the latest sample is recent.
- **Relay honesty.** `POST /api/relays/{id}` writes the coil, then reads it back. The UI renders the read-back, never the command, and clears relay state when the read fails.
- **Real protocol.** The backend speaks Modbus TCP to simulator processes over sockets. Nothing is generated in-process.

Shutdown order: SIGINT/SIGTERM cancels the context, pollers, engine and HTTP server stop, and only then does the database close.

## 4. Modbus register map

Defined in `simulators/internal/*/registers.go` (single source of truth). This is this implementation's map, not the brief's suggested one.

| Device | Service | Object | Address | Meaning |
|---|---|---|---|---|
| Temp/humidity | `:5021` | Input reg (FC4) | 0 | Temperature, °C × 10 |
| Temp/humidity | `:5021` | Input reg (FC4) | 1 | Humidity, % × 10 |
| Relay | `:5022` | Coil (FC1 read, FC5 write, FC15 fallback) | 0–7 | Channels 0–7 on/off |
| Thermocouple | `:5023` | Input reg (FC4) | 0–3 | Channels 0–3, °C × 10 |
| Thermocouple | `:5023` | Sentinel | `0x8000` | Channel fault / open circuit |
| PWM | `:5024` | Holding reg (FC3/FC6) | 0 | Speed command, 0–100 % |
| PWM | `:5024` | Holding reg (FC3) | 1 | Tachometer feedback, RPM (nominal 30 RPM per %) |

Relay labels default to `Fan 1, Heater, Lighting, Aux Power, Channel 5..8`. Set `RELAY_LABELS_FILE` to a JSON array (1–8 strings) to rename channels with no code change.

## 5. API and capabilities

| Method | Path | Capability |
|---|---|---|
| POST | `/api/auth/login` | public |
| GET | `/api/auth/me` | any valid token |
| GET | `/api/devices` | `read:device_status` |
| GET | `/api/measurements?source=&metric=&since=&limit=` | `read:sensor` (`read:thermocouple` for `thermocouple_ch0..3`, `read:fan_speed` for `fan_speed`; unknown sources return 400) |
| GET | `/api/relays` | `read:relay` |
| POST | `/api/relays/{id}` body `{"on":bool}` | `write:relay` |
| GET | `/api/events?limit=` | `read:events` |
| GET | `/api/config/control` | `read:events` |
| PUT | `/api/config/control` | `write:thresholds` |
| GET | `/api/fan/speed` | `read:fan_speed` (Extended) |
| POST | `/api/fan/speed` body `{"percent":0..100}` | `write:fan_speed` (Extended) |
| GET | `/healthz`, `/metrics` | public |

Catalog (flat; no capability implies another): `read:sensor`, `read:relay`, `read:thermocouple`, `read:fan_speed`, `read:events`, `read:device_status`, `write:relay`, `write:fan_speed`, `write:thresholds`, `write:reset`.

`write:reset` is defined, but `/api/reset` does not exist yet.

## 6. Fan control and safety

Genuine two-threshold hysteresis (`internal/control`): turn ON when `temp > on_above`, turn OFF when `temp < off_below`, hold inside the band. Defaults 30 / 27 °C. Example: 28 (off) → 31 (on) → 29 (stays on) → 26 (off) gives exactly one on and one off event.

- **Runtime configuration.** Thresholds, fan channel and mode live in the SQLite `config` table, are validated (`on_above > off_below`, plausible range, existing channel), saved before being applied, and take effect on the next reading with no restart. Each change is an audited `config_change` event with old and new values and the acting user.
- **AUTO / MANUAL.** `enabled=true` is AUTO: the fan relay is locked and writes to it return `409`. `enabled=false` is MANUAL: operators with `write:relay` can switch it. Other channels are always writable. Mode switches are audited.
- **Hardware truth before a decision.** On every fresh reading the engine reads the fan coil back over Modbus. If that read fails, control suspends instead of assuming a state.
- **Stale or bad data.** A reading older than three polls, or outside −40..125 °C, suspends control (`control_suspended`); the next good reading writes `control_resumed`. The fan holds its last state while suspended. This is a deliberate choice, not a hardware fail-safe.
- **Confirmed state only.** The engine's belief about the fan changes only after the relay layer's read-back confirmed the write. A failed write is retried on the next reading and logged once per outage (`fan_actuation_failed`).
- **Non-blocking.** The poller hands readings to the engine through a one-slot "newest wins" channel, so a slow relay write never blocks polling.

Event kinds: `status_change`, `relay_write`, `fan_speed_write`, `fan_transition`, `fan_sync`, `fan_actuation_failed`, `control_suspended`, `control_resumed`, `config_change`, `control_mode_change`.

## 7. Persistence and observability

| Table | Purpose |
|---|---|
| `measurements` | Timestamped history by source and metric |
| `events` | Audit log (relay writes with username, state changes, control events) |
| `device_states` | Last observed status, last seen, error, failures. Not trusted after restart: live pollers start as `unknown` |
| `config` | Runtime control configuration with updated time and actor |

`/metrics` exports: `hwm_device_online`, `hwm_device_consecutive_failures`, `hwm_sensor_temperature_celsius`, `hwm_sensor_humidity_percent`, `hwm_relay_channel_state`, `hwm_control_threshold_celsius`, `hwm_control_suspended`. Sensor gauges are omitted when the sensor is offline or the sample is older than 15 s; use `hwm_device_online` to tell absence from zero.

Prometheus scrapes the backend. Grafana provisions `observability/grafana/dashboards/industrial_dashboard.json` (7 panels: connectivity, temperature, humidity, control loop, temperature with hysteresis band, humidity, relay states). The UI shows the temperature as queried from Prometheus (through Nginx's read-only `/prometheus/api/v1/query` proxy) and links to the Grafana dashboard. The link's port comes from `GRAFANA_PORT` at frontend build time, so rebuild the frontend after changing it.

## 8. Fault injection

Test tools only: simulator admin endpoints are unauthenticated and published on host loopback.

```bash
curl -X POST 'localhost:5121/fault?mode=down'     # none | slow | timeout | error | garbage | down  [&delay=3s]
curl -X POST 'localhost:5121/temp?value=31'       # pin temperature; value=auto resumes the random walk
curl -X POST 'localhost:5121/fault?mode=none'     # recover
```

`GET /api/devices` goes `online` → `degraded` → `offline` and recovers by itself. The backend stays up and every state change is written to the event log. To drive the hysteresis band: `for t in 25 28 31 29 26; do curl -s -X POST "localhost:5121/temp?value=$t" >/dev/null; sleep 3; done`, then read `/api/events`.

## 9. Tests and verification

```bash
cd backend    && go test -race ./...
cd simulators && go test -race ./...
# Windows with Docker Desktop (Go 1.26 in a Linux container):
powershell -ExecutionPolicy Bypass -File scripts/race_tests.ps1

# with the stack running:
python scripts/smoke_test.py
python scripts/hysteresis_integration.py
python scripts/fault_recovery_integration.py      # --max-wait 150 on slow machines
```

The integration scripts change simulator behaviour: run them only on a local test stack.

Results on my local Docker stack (2026-11-17):

| Check | Result |
|---|---|
| `go test -race ./...` in `backend` and `simulators` (Go 1.26) | PASS, no race reports |
| `scripts/smoke_test.py` | PASS |
| `scripts/hysteresis_integration.py` | PASS: exactly one audited ON and one OFF |
| `scripts/fault_recovery_integration.py` (`timeout`, `garbage`) | PASS: backend stayed healthy, sensor went offline and recovered, `last_seen` not falsely updated |
| Frontend `npm ci` + `npm run build` | PASS |

Covered by tests: capability checks per route, `alg: none` rejection, relay failure kinds, hysteresis sweep, stale/implausible data, runtime config, failed-write retry, backoff and state transitions, store, simulator register behaviour and remote-reset recovery. A clean `-race` run covers the exercised paths only. Packages without tests: `cmd/backend` (wiring) and `internal/speed`.

## 10. Known limitations

- **`/api/measurements` capability mapping is incomplete.** Only `thermocouple*` sources need their own capability; `source=fan_speed` is readable with `read:sensor`, and unknown sources are not rejected. It should map every source to its read capability and reject the rest.
- **`suspended` flag outlives AUTO.** If control is suspended and the operator switches to MANUAL, the flag (and `hwm_control_suspended`) stays set until AUTO resumes with a fresh reading.
- **Unbounded `measurements`.** About 86k rows per day per series at 2 s. No retention or downsampling job.
- **No login rate limiting.** Failed-login usernames are logged verbatim.
- **No token refresh.** The TTL is one hour; the UI returns to login on `token_expired`. Capability changes would apply only at the next login, and there is no revocation before expiry.
- **Modbus client calls do not take a context.** A relay write (connect, write, read-back) can take about three client timeouts, longer than the API's 5 s deadline.
- **Config update and its audit event are two separate SQL operations**, not one transaction.
- **A fan with no fresh data keeps its last state.** There is no independent hardware interlock.
- **Simulator admin APIs have no authentication.** They are test tools bound to loopback.
- **The UI stores the JWT in `localStorage`**, which is simple but exposed to XSS.

## 11. Cuts and how I would finish them

I stopped at a verified Core, as the brief recommends. In the order I would do them:

1. **Thermocouple fault state.** Return per-channel `{value, fault}` from the poller instead of dropping faulted channels, expose it on a read endpoint guarded by `read:thermocouple`, add a UI panel and a metric.
2. **Remote reset.** Backend client for the TCP service, `POST /api/reset` guarded by `write:reset`, audit event with the JWT username, a confirmation dialog in the UI, and marking the target device's data `unknown` until it recovers.
3. **Variable fan speed.** Implement the linear curve (20 % at 25 °C to 100 % at 35 °C, clamped) behind a manual/auto speed mode, add UI controls, and show command-versus-feedback mismatch.
4. **Django identity service (§2.11).** Django 5 + DRF + simplejwt with RS256. Models: `User` and an append-only `CapabilityGrant` (who granted, when, revoked_at and revoked_by). Capabilities become data edited in the Django admin. The token carries the resolved `perms`, and the Go backend verifies it locally with the public key only. The route guards stay as they are; only the token verifier changes from HS256 to RS256. Separate database and Compose service. Django must not poll devices or run control logic.

## 12. Frontend development

```bash
cd frontend
npm ci
npm run dev        # Vite dev server
npm run build      # tsc + vite build
```

The Docker build uses `package-lock.json` and `npm ci`. `frontend/node_modules/` and `frontend/dist/` are git-ignored.

## 13. AI usage

See [`AI_USAGE.md`](AI_USAGE.md).
### Core correctness checks

The UI checks backend liveness through `/healthz` independently of device endpoints and permissions. Device failures do not mark the backend offline. Vite and Nginx proxy this route to Go. Polling shares one active cycle across timer ticks and manual refreshes, waits for all endpoint requests, and aborts on cleanup; requests time out after 10 seconds. Profile loading retries transient failures.

Run frontend regression tests with `cd frontend && npm ci && npm test`, then `npm run build`. Run backend tests with `cd backend && go test ./...`; race checks require a supported 64-bit Go toolchain and C compiler (`go test -race ./...`).


## Extended implementation (?2.3, ?2.6, ?2.9)

The existing Go/JWT viewer and operator architecture is retained. Device values come
from independent Modbus TCP simulators; reset commands use the raw TCP reset service.

| Endpoint | Capability | Contract |
| --- | --- | --- |
| GET /api/thermocouple | read:thermocouple | `channels[4]` with source, status (`live`, `fault`, `unknown`), nullable `temperature_c`, `last_valid_at`; `all_channels_faulted` |
| GET /api/fan/speed | read:fan_speed | verified `command_pct`, `feedback_rpm`, sustained `mismatch` |
| POST /api/fan/speed | write:fan_speed | `{ "percent": 0..100 }`; AUTO returns 409; device/read-back failures return 502 |
| POST /api/reset | write:reset | `{ "target": "relay" }`; 202 initiated; invalid target 400; unavailable 503; TCP failure 502 or context timeout 504 |

Thermocouple FC4 input registers 0?3 contain signed int16 tenths of a degree.
`0x8000` exclusively denotes channel fault; `0xffff` represents ?0.1?C.
Valid temperatures span ?200 through 1300?C. A fault omits the sample from history,
clears the current value and preserves the last-valid timestamp. Even four probe
faults leave the transport online. Connection failures/reset make channels unknown.
Current channel snapshots expire after six seconds without a successful poll.

PWM FC3 holding register 0 is command percent (FC6 writable); register 1 is actual
feedback RPM (0?3000). AUTO preserves relay hysteresis and uses confirmed relay
feedback: OFF commands zero, ON uses `clamp(round(20 + (T?25)*8), 0, 100)`.
Only fresh plausible ambient samples authorize automatic actions. MANUAL permits
operator commands. Mode changes, manual commands, automatic steps and reset
invalidation share the engine operation mutex. Commands require read-back.
RPM error over 300 RPM must persist for five seconds, after a five-second command
settling period, before mismatch is reported. Automatic write transitions and
failure/recovery edges are audited instead of every poll.

Reset accepts exactly `temp-humidity`, `relay`, `thermocouple`, `speed-pwm`.
Configure `RESET_TCP_ADDR` (Compose: `remote-reset-sim:9001`); each TCP operation
has a four-second timeout and request cancellation closes its connection. The API
never calls simulator admin HTTP for reset. Attempts and initiation/failure results
include the JWT username and timestamp in audit events. During reset, telemetry is
invalidated and pollers expose `recovering`, `offline`, `unknown`, or
`recovery_failed` as reported by the TCP service. `online` additionally requires a
successful fresh device read. Generation checks prevent old recovery probes from
overriding later resets. Reset invalidates controller relay belief and queued data.
The UI requests explicit confirmation naming the target before sending a reset.
Historical charts remain historical while device data is unavailable.

Fault injection (local simulator admin ports, for development):

```sh
curl -X POST 'http://localhost:5123/channel-fault?ch=1&broken=true'
curl -X POST 'http://localhost:5123/channel-fault?ch=1&broken=false'
curl -X POST 'http://localhost:5124/feedback?rpm=0'
curl -X POST 'http://localhost:5124/feedback?rpm=auto'
curl -X POST 'http://localhost:5121/temp?value=35'
curl -X POST 'http://localhost:5121/temp?value=auto'
```

With `.env` configured, run `docker compose config --quiet`, then
`docker compose up --build -d`. Run `python scripts/smoke_test.py` for Core and
`python scripts/extended_smoke_test.py` for Extended acceptance against live
simulators. The Extended script temporarily changes control configuration and
injects faults, then restores configuration and automatic simulation in `finally`.
It exercises channel fault isolation, all-channel faults/recovery, AUTO/PWM,
manual read-back, feedback divergence, four device resets, viewer denial and audit.
Negative encoding, timeout/connection cleanup and generation behavior have Go tests.
Run `go test ./...` and `go test -race ./...` in each Go module, plus `npm test`
and `npm run build` in frontend. Race tests require a supported architecture and C
compiler (the local Windows/386 Go toolchain does not support race detection).
