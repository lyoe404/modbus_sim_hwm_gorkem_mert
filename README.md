# Industrial Hardware Monitoring & Control Platform

A full-stack industrial device monitoring and control platform built around a **Go backend**, **independent Modbus TCP simulators**, **React/TypeScript**, **SQLite**, **Prometheus**, and **Grafana**. The backend communicates with separate simulator processes over real TCP sockets, validates relay writes through physical read-back, applies temperature-driven hysteresis, and exposes capability-protected REST endpoints.

**Assessment:** Industrial Hardware Monitoring & Control — Technical Assessment v3.1  
**Implementation scope:** Core implemented; Extended device integrations partially implemented. Django identity/authorization is not implemented.

> **Verification:** Core smoke, hysteresis, fault-recovery, and Go race tests were run successfully on a local Docker-based setup (reported on 2026-11-17). The source archive was subsequently reviewed, but the updated checkout was **not** independently rebuilt in this README-writing environment. Re-run the checks below after changing dependencies or Docker configuration.

## 1. Features and scope

### Core

| Requirement | Status | Implementation |
|---|---|---|
| §2.1 Temperature/humidity sensor | Implemented and tested | Independent Modbus TCP simulator; bounded random walk; polling and SQLite history; fault injection and automatic recovery |
| §2.2 Multi-channel relay controller | Implemented and tested | 8 coils, configurable labels, write/read-back verification, distinct failure responses |
| §2.4 Go device backend | Implemented and tested | Layered packages, concurrent pollers, backoff, graceful shutdown, race-tested code |
| §2.5 Automatic fan control | Implemented and tested | Runtime-configurable hysteresis, AUTO/MANUAL mode, coil reconciliation, audited transitions |
| §2.7 React UI | Implemented | Live charts via 2-second polling, relay controls, threshold form, events, device-health/freshness states, capability gating |
| §2.8 Prometheus + Grafana | Implemented and exercised | `/metrics`, Prometheus scraping, provisioned dashboard, Prometheus-backed value in React |
| §2.10 JWT viewer/operator | Implemented and tested | Bcrypt passwords, environment-provided HS256 secret, per-route capability checks, 401/403 handling |
| Mandatory Compose deployment | Configured and previously exercised | Separate services with `docker compose up --build`; reproduce from a clean checkout before submission |

### Extended

| Requirement | Status | Remaining work |
|---|---|---|
| §2.3 Thermocouple | Partial | Four-channel simulator, poller, SQLite history and read capability exist; channel-specific fault status needs explicit API/UI presentation |
| §2.6 PWM fan speed | Partial | Simulator and read/write API exist; automatic speed curve, UI controls and visible feedback mismatch are incomplete |
| §2.9 Remote reset | Partial | TCP reset simulator and recovery exist; authenticated Go reset endpoint, confirmation UI, audit and dependent-data unknown state remain |
| §2.11 Django identity service | Not implemented | Django admin/user/grant models, RS256 issuance/refresh and Go public-key validation remain |

**Scope decision:** §2.11 is explicitly Extended in the brief. Core uses Go-issued HS256 JWTs with `viewer` and `operator` roles. Routes already evaluate capabilities from JWT claims rather than hard-coded role checks; this is not the same as having admin-managed capability grants.

## 2. Architecture

```text
                        React + TypeScript (Nginx)
                      localhost:3000 (HTTP / REST)
                                  |
                                  v
                   Go REST API + JWT authorization
                            localhost:8080
                    /healthz  /api/*  /metrics
                     /              |           \
                    v               v            v
             Pollers +          Hysteresis      SQLite
             health state       control engine  (volume)
                  |                  |
                  +------------------+
                             Modbus TCP
                                  |
               +------------------+------------------+
               |                  |                  |
       Temp/Humidity:5021     Relay:5022      Thermocouple:5023
                                                  + PWM:5024

         Prometheus:9090  --> scrapes /metrics --> Grafana:3001

         Remote reset TCP simulator:9001 (Extended infrastructure only)
```

Each device simulator runs as a **separate service/process**. Go uses real Modbus TCP operations; sensor readings are not generated in the backend process. The remote-reset simulator is independent, but its API/UI integration is unfinished.

### Repository layout

```text
backend/
  cmd/backend/          bootstrap, runtime wiring, shutdown
  internal/api/         REST handlers, metric exposition, authorization boundaries
  internal/auth/        login, bcrypt users, JWT, capability catalog
  internal/control/     hysteresis, mode handling, audit, safety
  internal/poller/      Modbus readers, retry/backoff, health status
  internal/relay/       relay writes, read-back, reconciliation
  internal/speed/       PWM speed Modbus controller (Extended)
  internal/store/       SQLite measurements, events, config, device_states
  internal/envfile/     optional environment loading
simulators/
  cmd/                  five simulator entry points
  internal/             temp/humidity, relay, thermocouple, PWM, reset, faults
frontend/
  src/                  React/TypeScript UI
  Dockerfile            Vite build + Nginx runtime
observability/
  prometheus/           scrape configuration
  grafana/              datasource/dashboard provisioning and dashboard JSON
scripts/
  smoke_test.py
  hysteresis_integration.py
  fault_recovery_integration.py
  race_tests.ps1
.env.example
AI_USAGE.md
README.md
```

## 3. Requirements

- Docker Engine + Docker Compose (Docker Desktop on Windows is supported).
- Free host ports: frontend `3000`, backend `8080`, Prometheus `9090`, Grafana `3001`, and simulator admin `5121`–`5124` / TCP reset `9001`, unless changed in Compose.
- Python 3 for integration tests (scripts use the Python standard library).
- Go toolchain only for running Go tests locally; the application is built inside Docker.
- Node.js/npm only for local frontend development; not necessary to run the Compose stack.

## 4. Quick start

### Configure environment

macOS/Linux:

```bash
cp .env.example .env
```

Windows PowerShell:

```powershell
Copy-Item .env.example .env
```

Edit `.env` and set **unique local values** for `JWT_SECRET` (minimum 32 characters), `VIEWER_PASSWORD` and `OPERATOR_PASSWORD` (minimum 8 characters). The example file documents the variables; do not commit the populated `.env`.

> **Current archived Compose configuration:** Grafana uses `GF_SECURITY_ADMIN_PASSWORD: ${GRAFANA_PASSWORD:-admin}`. Set `GRAFANA_PASSWORD` in your `.env` before running and preferably change Compose to `${GRAFANA_PASSWORD:?set GRAFANA_PASSWORD in .env}` to eliminate the insecure fallback. Add `GRAFANA_PASSWORD=` to `.env.example` when making this change.

### Start the stack

```bash
docker compose up --build -d
docker compose ps
```

Check backend health:

```bash
curl http://localhost:8080/healthz
```

View logs or stop:

```bash
docker compose logs -f backend
docker compose down
```

To remove persisted database and monitoring data **as well** (destructive):

```bash
docker compose down -v
```

### Endpoints

| Service | Address | Notes |
|---|---|---|
| React UI | http://localhost:3000 | Main application |
| Go API | http://localhost:8080 | `/healthz`, authenticated `/api/*` |
| Prometheus | http://localhost:9090 | Scrapes backend `/metrics` |
| Grafana | http://localhost:3001 | Provisioned dashboard |
| Sensor simulator admin | http://127.0.0.1:5121 | Fault and temperature control, **local testing only** |
| Relay simulator admin | http://127.0.0.1:5122 | Simulator state, **local testing only** |
| Thermocouple simulator admin | http://127.0.0.1:5123 | Extended simulator |
| PWM simulator admin | http://127.0.0.1:5124 | Extended simulator |
| Remote reset TCP simulator | 127.0.0.1:9001 | TCP command service, not HTTP |

The Modbus endpoints `5021`–`5024` are reachable **inside the Compose network** and are not published to the host.

### Test accounts

The identities are created by the Go backend from your **local `.env`** at startup:

| Role | Default username | Password source | Effective permissions |
|---|---|---|---|
| Viewer | `viewer` | `VIEWER_PASSWORD` | All `read:*`, no `write:*` |
| Operator | `operator` | `OPERATOR_PASSWORD` | All defined `read:*` and `write:*` claims |

`VIEWER_USERNAME` and `OPERATOR_USERNAME` can override the names. **Do not use sample development passwords outside a local test environment.** Having a `write:reset` claim does not mean the unfinished `/api/reset` endpoint exists.

## 5. Modbus register map

The mapping below describes **this implementation**, not the brief's optional example register addresses. See `simulators/internal/*/registers.go` for source definitions.

| Device | Service | Modbus object | Address | Meaning |
|---|---|---|---|---|
| Temp/humidity | `temp-humidity-sim:5021` | Input register, FC4 | 0 | Temperature, °C × 10 |
| Temp/humidity | `temp-humidity-sim:5021` | Input register, FC4 | 1 | Relative humidity, % × 10 |
| Relay | `relay-sim:5022` | Coil, FC1/FC5 (FC15 fallback) | 0–7 | Eight independently controlled loads |
| Thermocouple | `thermocouple-sim:5023` | Input register, FC4 | 0–3 | Channels CH0–CH3, °C × 10 |
| Thermocouple | `thermocouple-sim:5023` | Input-register sentinel | `0x8000` | Channel fault/open circuit |
| PWM | `speed-pwm-sim:5024` | Holding register, FC3/FC6 | 0 | Speed command, 0–100% |
| PWM | `speed-pwm-sim:5024` | Holding register, FC3 | 1 | Tachometer feedback, 0–3000 RPM nominal |

Temperature and humidity simulator values follow bounded random walks; the ambient ranges are approximately **18–35 °C** and **30–80% RH**.

Relay labels are loaded from `RELAY_LABELS_FILE` (JSON array) if configured. Default labels cover Fan 1, Heater, Lighting, Aux Power and additional channels. Renaming does not require editing controller logic.

## 6. Authentication and API

Authentication uses bearer JWTs signed with an environment-provided **HS256** secret and one-hour expiration. Passwords are hashed with bcrypt. Go checks permissions on each route; lack of authentication yields **401**, whereas a valid token without the required permission yields **403**.

| Method | Endpoint | Required capability | Purpose |
|---|---|---|---|
| POST | `/api/auth/login` | Public | Username/password to JWT |
| GET | `/api/auth/me` | Valid JWT | Identity, role and capabilities |
| GET | `/api/devices` | `read:device_status` | Live poller status |
| GET | `/api/measurements` | `read:sensor` or `read:thermocouple` depending on source | Historical series |
| GET | `/api/relays` | `read:relay` | Live coil state and labels |
| POST | `/api/relays/{id}` | `write:relay` | Coil write and confirmed read-back |
| GET | `/api/events` | `read:events` | Audit/event history |
| GET | `/api/config/control` | `read:events` | Control settings and mode |
| PUT | `/api/config/control` | `write:thresholds` | Update control thresholds/mode |
| GET | `/api/fan/speed` | `read:fan_speed` | PWM command and feedback (Extended API) |
| POST | `/api/fan/speed` | `write:fan_speed` | Set manual PWM command (Extended API) |
| GET | `/healthz` | Public | Backend HTTP health |
| GET | `/metrics` | Public | Prometheus exposition |

**Not implemented:** `/api/reset`, Django `/api/auth/refresh`, WebSocket/SSE. The current frontend uses periodic REST polling (accepted by the assessment).

Capability catalog: `read:sensor`, `read:relay`, `read:thermocouple`, `read:fan_speed`, `read:events`, `read:device_status`, `write:relay`, `write:fan_speed`, `write:thresholds`, `write:reset`.

### Example API calls

Log in:

```bash
curl -X POST http://localhost:8080/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"operator","password":"<YOUR_OPERATOR_PASSWORD>"}'
```

Copy the returned `token` into `TOKEN` (shell environment variable). Then:

```bash
curl http://localhost:8080/api/devices -H "Authorization: Bearer $TOKEN"
curl http://localhost:8080/api/relays -H "Authorization: Bearer $TOKEN"
curl http://localhost:8080/api/events -H "Authorization: Bearer $TOKEN"

# Control an auxiliary coil (channel 3; does not conflict with AUTO Fan 1)
curl -X POST http://localhost:8080/api/relays/3 \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"on":true}'
```

With a viewer JWT, the relay write must return `403`. With the fan configured in AUTO mode, an operator write to its controlled coil (default CH0) must return `409 Conflict`; other coils remain writable.

## 7. Automatic fan control and safety

The control engine uses true two-threshold hysteresis:

- Turn **ON** when temperature is strictly greater than `on_above`.
- Turn **OFF** when temperature is strictly lower than `off_below`.
- Inside the band, preserve the current relay state.

Example: `on_above = 30`, `off_below = 27` yields `28°C OFF → 31°C ON → 29°C ON → 26°C OFF` when starting OFF.

Control configuration is stored in SQLite's `config` table and can be edited at runtime via the capability-gated API/UI. `enabled = true` means **AUTO**; `enabled = false` means **MANUAL**. In AUTO, writes to the selected fan relay are blocked to avoid conflicting controllers. On each valid sensor update the engine reconciles the physical fan coil state before making a decision.

If measurements are stale, implausible or unavailable, the controller suspends automated decisions and **holds the last fan state**. This is an explicit design decision, not a hardware fail-safe guarantee. The engine writes audited `fan_transition`, `fan_sync`, `control_suspended`, `control_resumed`, `fan_actuation_failed`, `config_change` and `control_mode_change` events as applicable.

## 8. Persistence and observability

The backend uses `modernc.org/sqlite` (pure Go, no CGO dependency), with WAL and a single database connection. The `backend-data` named Docker volume persists data across ordinary container restarts.

| SQLite table | Purpose |
|---|---|
| `measurements` | Timestamped metric history, by source and metric |
| `events` | State transitions and user-action audit records |
| `device_states` | Last persisted status, last-seen time, error and consecutive failures |
| `config` | Runtime control configuration, modification timestamp and actor |

Historical `device_states` rows are **not** trusted as current truth after restart. The live pollers start in `unknown` and report actual device status as they observe it.

The backend exports Prometheus text metrics at `/metrics`; Prometheus scrapes the backend and Grafana provisions a dashboard from `observability/grafana/dashboards/industrial_dashboard.json`. The React UI includes a Prometheus-backed metric and a Grafana dashboard link. The Grafana URL is compiled using the `VITE_GRAFANA_PORT` build argument, supplied from Compose `GRAFANA_PORT`; rebuild the frontend after changing that port.

## 9. Fault injection and recovery

**Development/testing only.** Simulator admin HTTP endpoints intentionally lack authentication and are exposed on host loopback by Compose. Do not publish them outside a trusted local environment.

```bash
# Simulated loss of communication
curl -X POST 'http://localhost:5121/fault?mode=down'

# Restore normal communications
curl -X POST 'http://localhost:5121/fault?mode=none'

# Pin temperature; restore random walk after the test
curl -X POST 'http://localhost:5121/temp?value=31'
curl -X POST 'http://localhost:5121/temp?value=auto'
```

Supported sensor fault modes include `none`, `slow`, `timeout`, `error`, `garbage` and `down`. During a fault the Go backend should stay responsive, transition device status through degraded/offline as configured, preserve honest last-seen information, omit unconfirmed measurements, and recover once the simulator does.

## 10. Tests and verification

### Integration tests (start Compose first)

```bash
python scripts/smoke_test.py
python scripts/hysteresis_integration.py
python scripts/fault_recovery_integration.py
```

`smoke_test.py` checks HTTP health, login, viewer write denial, devices, measurements, relay read-back, control config and events. `hysteresis_integration.py` drives real simulated temperature changes, verifies ON/OFF transitions and event logging, and restores the original mode and pinned temperature. `fault_recovery_integration.py` tests timeout and malformed responses, backend survival, offline Prometheus state, last-seen integrity, and recovery; use `--max-wait 150` on slow machines if necessary.

These tests change simulator behavior; run only on a local test stack.

### Go tests and race detector

From the project root, with Go available:

```bash
cd backend
go test ./...
go test -race ./...

cd ../simulators
go test ./...
go test -race ./...
```

Windows Docker Desktop (Linux containers):

```powershell
powershell -ExecutionPolicy Bypass -File scripts/race_tests.ps1
```

The Windows script executes race-enabled tests in a Linux/amd64 Go container. A clean `-race` run covers exercised concurrency paths; it does not guarantee the absence of every possible race or device fault.

### Verification record

The following checks were **reported passing on a local Docker stack on 2026-11-17**:

| Check | Recorded result |
|---|---|
| Core HTTP smoke | PASS |
| Temperature hysteresis: OFF → ON → hold → OFF | PASS; exactly one ON and one OFF audit event |
| Simulated service disconnection and restart | Backend remained operational, status recovered |
| Fault recovery (`timeout`, `garbage`) | PASS; no fabricated successful sensor sample or incorrect `last_seen` update |
| Go backend `-race` suite | PASS |
| Go simulator `-race` suite | PASS |
| Frontend `npm ci` and `npm run build` | PASS, reported subsequently |

After lockfile/Dockerfile changes, rerun `docker compose build frontend` (and then `docker compose up -d`) from a clean checkout. The latest dependency audit also reported Vite/esbuild development-tool vulnerabilities; evaluate them separately from the runtime image, and do not claim the npm audit is clean until retested.

## 11. Frontend development and builds

```bash
cd frontend
npm ci
npm run dev
```

Production assets:

```bash
npm run build
```

The intended reproducible Docker build copies **both** `package.json` and `package-lock.json`, then uses `npm ci`:

```dockerfile
COPY package.json package-lock.json ./
RUN npm ci
```

If your checkout still lacks `frontend/package-lock.json`, generate and commit it with `npm install --package-lock-only` before using `npm ci` in Docker. In the uploaded archive reviewed for this README, the lockfile and Dockerfile had **not yet** been updated together; local changes described afterward should be included in the final commit.

Do not commit `frontend/node_modules/` or `frontend/dist/`; both can be regenerated. Put those paths and `.env` in the root `.gitignore`. If previously tracked, remove them from Git's index using `git rm --cached` while keeping local files.

## 12. Limitations and next steps

### Core hardening

1. **Secrets:** remove Grafana's default `admin` password fallback, make `GRAFANA_PASSWORD` mandatory in Compose, and keep populated `.env` out of Git or shared ZIPs.
2. **Reproducible frontend builds:** commit `package-lock.json`, copy it explicitly in `frontend/Dockerfile`, and verify `npm ci` in Docker.
3. **Audit consistency:** configuration update and related event are separate SQL operations; consider one transaction for strong audit guarantees.
4. **Data retention:** `measurements` has no scheduled cleanup/downsampling. Long-running installations require a retention plan.
5. **Security:** login rate limiting is not implemented; simulator admin APIs are unauthenticated local test tools.
6. **Network/deadlines:** Modbus calls use client timeouts, not fully context-cancellable operations; align API and device timeout budgets.
7. **Realtime transport:** React uses REST polling; WebSocket/SSE is not implemented and is optional for Core.
8. **JWT renewal:** Core has login/expiry handling, but no refresh-token endpoint or independent administrative capability revocation.

### Extended scope intentionally unfinished

- **Thermocouple:** return and display explicit fault state per channel, including all-channels-faulted behavior.
- **PWM:** automatic temperature-to-speed curve, manual/auto UI, and visible `command_pct` versus `feedback_rpm` mismatch.
- **Remote reset:** add a capability-protected Go endpoint, user confirmation, target-specific unknown state and audit records.
- **Django:** add independent identity database/schema, admin-managed grant/revoke audit, RS256 JWT issuance/refresh and local Go public-key verification. Django must not poll devices or implement device-control logic.
- **Optional bonuses:** alerts, Modbus RTU, versioned configuration history, tracing, numerical health scores and capability presets are not implemented.

The Core was prioritized over incomplete feature breadth, as recommended by the assessment brief.

## 13. Submission checklist

- [ ] A clean checkout starts with `docker compose up --build` using documented `.env` values.
- [ ] Docker frontend build succeeds with committed `package-lock.json` and `npm ci`.
- [ ] `.env`, private keys and real passwords are absent from Git history and submitted archives.
- [ ] Grafana password fallback has been removed or explicitly secured for local-only use.
- [ ] Viewer cannot write; operator can change an auxiliary relay and read back its state.
- [ ] Hysteresis transitions and audit log pass the integration script.
- [ ] Fault injection causes honest degraded/offline/recovery behavior.
- [ ] Grafana dashboard loads live metrics from Prometheus.
- [ ] Go backend and simulator race tests pass.
- [ ] `AI_USAGE.md` accurately discloses assistant/tool usage.
- [ ] The README reflects the final source tree and accurately labels unfinished Extended features.

## 14. AI usage

AI-assisted development is disclosed separately in [`AI_USAGE.md`](AI_USAGE.md), as required by the technical assessment. All generated or assisted changes should be understood, verified, and maintainable by the submitting engineer.
