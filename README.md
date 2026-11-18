# Industrial Hardware Monitoring & Control Platform

**HWM Full-Stack Technical Assessment v3.1 — Go · React · Modbus TCP · SQLite · Prometheus · Grafana**

A monitoring and control platform for independent simulated field devices. The device-facing backend is written in Go: it polls real Modbus TCP sockets, stores measurements, evaluates temperature-based hysteresis, verifies physical relay/PWM feedback, exposes authenticated control APIs, and exports Prometheus metrics. A React frontend provides live views and controls; a separate TCP reset simulator supports power-cycle workflows. The whole stack is described by Docker Compose.

> **Scope and verification:** The source includes the **Minimum Bar, Core, thermocouple (§2.3), variable-speed fan (§2.6), and remote reset (§2.9)** implementation paths. The Django identity/authorization service (§2.11) is intentionally excluded because it is Extended, not Core. Source presence is not a claim that every acceptance test passed. See [Verification and limitations](#verification-and-limitations).

## 1. Quick start

Requirements: Docker Desktop/Engine with Docker Compose. Python 3 is needed only for the optional HTTP/integration smoke scripts. Go and Node are not required to *start* the Docker stack.

From the **repository root**:

```bash
# Bash / Linux / macOS
cp .env.example .env
# Review .env and replace demonstration-only secrets before any nonlocal exposure.
docker compose config --quiet
docker compose up --build -d
docker compose ps
```

PowerShell equivalent for the first command:

```powershell
if (!(Test-Path .env)) { Copy-Item .env.example .env }
// OR JUST COPY .env.example ctx -> .env  // 
```

Do not overwrite an existing `.env`. Wait for health checks to settle; simulator dependencies are health-gated. Inspect failures with `docker compose logs --tail=100 backend` or `docker compose logs --tail=100 <service>`.

| Component | Default host address | Notes |
|---|---|---|
| React application | http://localhost:3000 | Nginx proxies `/api/`, `/healthz`, and a limited Prometheus query path |
| Go backend | http://localhost:8080 | `/healthz`, `/metrics`, and `/api/*` |
| Prometheus | http://localhost:9090/targets | `hwm-backend` should be UP |
| Grafana | http://localhost:3001 | Dashboard UID `hwm-industrial-overview` |
| Sensor fault admin | http://127.0.0.1:5121 | Test-only, unauthenticated |
| Relay fault admin | http://127.0.0.1:5122 | Test-only, unauthenticated |
| Thermocouple fault admin | http://127.0.0.1:5123 | Test-only, unauthenticated |
| PWM fault admin | http://127.0.0.1:5124 | Test-only, unauthenticated |
| Remote-reset TCP | 127.0.0.1:9001 | Line-oriented protocol; not an HTTP endpoint |

The Modbus ports (`5021`–`5024`) are reachable **inside the Compose network**, not directly published to the host. The backend, frontend, Prometheus, and Grafana host port mappings are configurable but not restricted to loopback by default; **do not expose this demo configuration to an untrusted network**.

To stop without destroying persistent measurements/monitoring data:

```bash
docker compose down
```

**Do not use `docker compose down -v`** unless you deliberately want to delete the named volumes.

### Demo credentials

Usernames/passwords are loaded at backend startup from `VIEWER_USERNAME`, `VIEWER_PASSWORD`, `OPERATOR_USERNAME`, and `OPERATOR_PASSWORD`. The example file supplies local demonstration values; never treat those values as private credentials. `JWT_SECRET` must also be changed outside a trusted local environment. Grafana uses `GRAFANA_USER` (default `admin`) and `GRAFANA_PASSWORD` from `.env`. The real `.env` should be excluded from commits and submission archives.

- **Viewer:** authorized read surfaces only; privileged controls are hidden/disabled and rejected by the Go API.
- **Operator:** authorized read surfaces and relay, speed, threshold/mode, and reset operations.

No Django user administration, role editing or token refresh is provided. Both roles are configured through environment variables; passwords use bcrypt hashes in memory and JWT signing uses an environment secret. The UI redirects to login after token expiration (one-hour token lifetime).

## 2. Architecture and responsibility boundaries

```text
React / TypeScript (Vite build -> Nginx :3000)
      | REST polling (~2 s), JWT Authorization header
      v
Go backend :8080
  ├─ Auth: viewer/operator JWT + per-route capability checks
  ├─ REST API, /healthz, /metrics
  ├─ Pollers: per-device lifecycle, retry/backoff, health, fresh snapshots
  ├─ Control engine: hysteresis, AUTO/MANUAL, relay/PWM coordination
  ├─ SQLite: measurements, events, device_states, config
  ├─ Modbus TCP ---> temp-humidity-sim :5021
  ├─ Modbus TCP ---> relay-sim         :5022
  ├─ Modbus TCP ---> thermocouple-sim  :5023
  ├─ Modbus TCP ---> speed-pwm-sim     :5024
  └─ raw TCP -----> remote-reset-sim  :9001

Prometheus :9090 --scrapes--> Go /metrics
Grafana :3001 ----queries---> Prometheus
React --------------------GET read-only Prometheus query through Nginx
```

Five simulators run in independent processes; the four Modbus devices use network sockets and the reset controller uses raw TCP. Device polling, business rules, storage and device-facing APIs remain in Go. **There is no SSE/WebSocket implementation:** the frontend uses bounded REST polling, which the assessment permits.

| Directory | Purpose |
|---|---|
| `backend/cmd/backend/` | Startup, dependencies, pollers, HTTP lifecycle |
| `backend/internal/poller/` | Per-device polling, fault transitions, reset generations |
| `backend/internal/control/` | Fan hysteresis, AUTO/MANUAL ownership and synchronization |
| `backend/internal/relay/`, `speed/`, `reset/` | Device protocol clients and feedback handling |
| `backend/internal/auth/`, `api/` | JWT/capabilities, REST endpoints, Prometheus metrics |
| `backend/internal/store/` | SQLite schema and persistence |
| `simulators/cmd/`, `simulators/internal/` | Independent Modbus and reset simulator processes |
| `frontend/src/` | React UI and HTTP polling |
| `observability/` | Prometheus scrape config and provisioned Grafana dashboard |
| `scripts/` | Core/Extended smoke, fault/recovery and race helpers |

## 3. Implemented scope versus the assessment

| Requirement | Source implementation |
|---|---|
| **§2.1 Sensor (Core)** | Independent Modbus TCP sensor; 18–35 °C / 30–80% RH bounded random walk; FC4; two-second polling; timestamped SQLite measurements; fault injection, retry/backoff and honest status |
| **§2.2 Relays (Core)** | Eight coils with configurable labels, Modbus write followed by coil read-back, distinct error conditions and authenticated control |
| **§2.4 Go backend (Core)** | Device-facing Go packages, goroutine pollers, state snapshots, mutexes, cancellations and error handling; race verification requires a supported toolchain |
| **§2.5 Hysteresis (Core)** | Configurable upper/lower thresholds, strict transition rules, sensor freshness, read-back reconciliation, runtime edits and audit events |
| **§2.7 React UI (Core)** | JWT login, monitoring graphs, device status, relays, thresholds/mode, thermocouple/PWM/reset controls and capability-aware rendering |
| **§2.8 Prometheus/Grafana (Core)** | Go metrics endpoint, two-second scrape configuration, automatically provisioned Grafana dashboard, Prometheus-origin temperature shown in React |
| **§2.10 JWT/RBAC (Core)** | Viewer/operator roles, bcrypt passwords, signed expiring JWTs, 401/403 and capability-guarded actions |
| **§2.3 Thermocouple (Extended)** | Four independently tagged channels, signed tenth-degree decoding, explicit `0x8000` faults, live/fault/unknown API and React display |
| **§2.6 Variable-speed fan (Extended)** | Modbus PWM with command read-back and independent tachometer RPM, MANUAL/AUTO, reference speed curve and persistent mismatch reporting |
| **§2.9 Remote reset (Extended)** | Independent TCP reset server, four allowed targets, verified JWT actor, audit, explicit React confirmation, invalidation and recovery verification |
| **§2.11 Django identity (Extended)** | **Intentionally not implemented**. Baseline two-role authentication is retained. |
| **§4 Optional bonuses** | Automated tests and operational counters are implemented; alerting, Modbus RTU, OTel tracing, health scoring and capability presets are not claimed. |

This table describes source coverage, **not independent live acceptance certification**. In particular, clean Docker startup, live Grafana rendering and race tests must be checked on the reviewer's system.

## 4. Modbus and reset protocol contracts

The following register maps are project-specific; they are not vendor maps.

| Device | Modbus TCP socket | Function / address | Semantics |
|---|---|---|---|
| Temperature/Humidity | `temp-humidity-sim:5021` | FC4 input registers `0`, `1` | Temperature ×10 °C; humidity ×10 %RH |
| Relay card | `relay-sim:5022` | FC1 coils `0..7`; FC5/FC15 writes | Eight independently addressable loads; read-back is authoritative |
| Thermocouple | `thermocouple-sim:5023` | FC4 input registers `0..3` | Signed `int16` tenths °C; `0x8000` means channel fault |
| PWM/Fan drive | `speed-pwm-sim:5024` | FC3 holding `0`,`1`; FC6/FC16 writes | Register 0: command 0–100%; register 1: measured 0–3000 RPM |

Thermocouple sources are `thermocouple_ch0` through `thermocouple_ch3`, persisted separately. Faults do **not** generate zero-valued measurements, and `last_valid_at` remains the prior valid timestamp. Transport faults make current channel values unknown. The thermocouple module may be online even if all four probes report faults.

The reset service accepts `PING`, `STATUS` and `RESET <target>` via **raw TCP**. Valid targets are `temp-humidity`, `relay`, `thermocouple`, and `speed-pwm`. A reset causes simulated downtime (Compose default five seconds) and recovery attempts. Backend telemetry is invalidated and must be restored from subsequent verified device reads, not from a guessed status.

### Relay labels

`RELAY_LABELS_FILE` selects a JSON label file read by the **Go backend at startup**; otherwise built-in channel labels are used. When using Compose, provide both the environment variable **and a readable file mount**, for example using a local override:

```yaml
services:
  backend:
    environment:
      RELAY_LABELS_FILE: /app/relay-labels.json
    volumes:
      - ./relay-labels.json:/app/relay-labels.json:ro
```

This wiring is **not part of the base `docker-compose.yml`**. The file contains a JSON **array of 1–8 strings**, for example `["Fan 1", "Heater", "Lighting", "Aux Power"]`; changing labels requires backend restart, not a source-code edit.

## 5. Fan control behavior

Initial configuration: AUTO mode, `on_above=30` °C, `off_below=27` °C, fan on relay channel 0. These values may be changed at runtime and are persisted in SQLite.

- **AUTO hysteresis:** temperature strictly above `on_above` switches the configured relay ON; strictly below `off_below` switches it OFF; between the thresholds (including boundaries) the confirmed previous state is held.
- **Hardware confirmation:** the engine reads the actual fan coil before decisions; completed writes are confirmed by read-back. Failed operations are surfaced and logged.
- **Safety and freshness:** stale, implausible or unavailable sensor values suspend automation; the last fan state is held rather than forcibly switched OFF. This is **not** an independent hardware safety interlock.
- **MANUAL:** operators with `write:relay` may write the fan coil and use `write:fan_speed` for manual PWM commands. AUTO rejects direct writes to the owned fan channel or speed with HTTP `409`.
- **PWM AUTO:** hysteresis remains authoritative. Confirmed relay OFF commands 0%; when ON, the reference is `clamp(round(20 + 8 × (T − 25)), 0, 100)` percent: 20% at 25 °C and 100% at 35 °C.
- **RPM divergence:** command and actual tachometer feedback are separate readings. A difference of more than 300 RPM is reported as persistent only after settling and sustained divergence windows (five seconds each).

Device writes, mode changes and reset invalidation share a controller operation mutex. Sensor/poller generations reject obsolete reset-cycle samples. Status is represented honestly as online, degraded, offline, recovering, recovery failed or unknown where appropriate.

## 6. REST API and authorization

With the exception of login, health and Prometheus exposition, routes require a valid JWT. The backend verifies capabilities independently of which controls React displays.

| Method | Endpoint | Required capability | Notes |
|---|---|---|---|
| POST | `/api/auth/login` | Public | `{"username":"...","password":"..."}` → JWT token and expiration |
| GET | `/api/auth/me` | Valid JWT | Username, role and issued permissions |
| GET | `/api/devices` | `read:device_status` | Current connectivity/status per device |
| GET | `/api/measurements` | By `source` | `sensor`/empty → `read:sensor`; `thermocouple_ch0..3` → `read:thermocouple`; `fan_speed` → `read:fan_speed` |
| GET | `/api/relays` | `read:relay` | Actual coil channel read-back; may return 503 during reset |
| POST | `/api/relays/{id}` | `write:relay` | Body `{"on":true}`; fan in AUTO → 409 |
| GET | `/api/config/control` | `read:events` | Current config and controller snapshot |
| PUT | `/api/config/control` | `write:thresholds` | Partial config body; merges/persists under serialized control lock |
| GET | `/api/events` | `read:events` | Recent audit/status events; optional `limit` |
| GET | `/api/thermocouple` | `read:thermocouple` | Four live/fault/unknown channels, nullable temperature and all-fault flag |
| GET | `/api/fan/speed` | `read:fan_speed` | PWM command %, actual RPM and mismatch |
| POST | `/api/fan/speed` | `write:fan_speed` | `{"percent":50}`; AUTO → 409 |
| POST | `/api/reset` | `write:reset` | `{"target":"relay"}`; accepted request → 202; confirmation is required in React |
| GET | `/healthz` | Public | Backend process health, **not** a guarantee all devices are online |
| GET | `/metrics` | Public | Prometheus exposition; restrict network exposure |

Missing/invalid/expired credentials return `401`; authenticated callers lacking a required capability receive `403`. Unknown measurement sources return `400`. Other errors may include `400`, `404`, `409`, `502`, `503`, or `504` depending on the operation and device availability.

Capabilities: `read:sensor`, `read:relay`, `read:thermocouple`, `read:fan_speed`, `read:events`, `read:device_status`, `write:relay`, `write:fan_speed`, `write:thresholds`, `write:reset`. These are **fixed in the baseline auth implementation**; there is no admin-managed capability grant/revoke service.

## 7. Persistence

SQLite uses one logical database (`DB_PATH`, Compose volume `/data/backend.db`) and separate tables:

- `measurements`: timestamped `source`, `metric` and numeric value, including the four thermocouple source tags and PWM telemetry.
- `events`: timestamped audit entries (`kind`, `source`, `message`, `actor`) for actions, control transitions and device status.
- `device_states`: last observed state, last successful read, error and consecutive failures. On restart, current authorization/control does **not** trust a formerly persisted online status.
- `config`: saved control configuration and its last updater.

The measurement batch from a successful poll is transactional. Configuration writes and their event records are **separate SQL operations**; full atomic audit durability is not claimed. There is no automatic long-term retention/downsampling job.

## 8. Observability: Prometheus and Grafana

Prometheus scrapes `http://backend:8080/metrics` every two seconds. Grafana automatically provisions its Prometheus datasource and the dashboard at:

`http://localhost:3001/d/hwm-industrial-overview`

The dashboard contains **38 data panels plus six section rows (44 JSON panel entries)**:

1. **System Overview:** per-device state, AUTO/MANUAL and loop status, fan coil, environmental values, PWM/RPM, active faults.
2. **Environmental Monitoring:** temperature with live threshold histories, humidity, sensor availability.
3. **Fan and Relay Control:** eight coil states, ON/OFF timeline, commanded PWM, RPM and mismatch.
4. **Thermocouple Monitoring:** per-channel temperatures, faults, current state and last valid reading age.
5. **Device Diagnostics:** availability, consecutive errors, poll success/failure, classified Modbus errors, latency quantiles and event counters.
6. **Remote Reset Operations:** attempts, TCP acknowledgments/failures and recovery outcomes.

Selected metric families:

| Family | Semantics |
|---|---|
| `hwm_device_online{device}` | Stable device identity, 1 online / 0 otherwise; **no changing `state` label** |
| `hwm_device_state_code{device}` | 0 unknown, 1 online, 2 degraded, 3 offline, 4 recovering, 5 recovery failed |
| `hwm_device_consecutive_failures{device}` | Count of sequential failed polls |
| `hwm_sensor_temperature_celsius`, `hwm_sensor_humidity_percent` | Fresh ambient values only |
| `hwm_control_threshold_celsius{bound}` | ON/OFF thresholds |
| `hwm_control_auto_enabled`, `hwm_control_suspended`, `hwm_control_state_code` | Distinguishes MANUAL, AUTO ACTIVE and AUTO SUSPENDED |
| `hwm_control_fan_on`, `hwm_relay_channel_state{channel,label}` | Verified physical relay state |
| `hwm_fan_speed_command_percent`, `hwm_fan_speed_feedback_rpm`, `hwm_fan_speed_mismatch` | PWM command, tachometer and persistent mismatch |
| `hwm_thermocouple_temperature_celsius{channel}`, `hwm_thermocouple_fault{channel}`, `hwm_thermocouple_state_code{channel}` | Independent per-channel telemetry/quality |
| `hwm_thermocouple_last_valid_timestamp_seconds{channel}` | Last verified value timestamp |
| `hwm_modbus_polls_total{device,result}`, `hwm_modbus_errors_total{device,reason}` | Poll results and bounded error categories |
| `hwm_modbus_poll_duration_seconds{device}` | Prometheus histogram for polling duration |
| `hwm_reset_total{target,result}`, `hwm_reset_recovery_total{target,result}` | Submitted commands and separately verified recovery results |
| `hwm_control_errors_total{operation}`, `hwm_operational_events_total{kind}` | Control failures and committed audit events |

Unknown/unavailable measurements are **not** intentionally exported as fresh zeroes. Live samples are limited by six-second freshness checks; missing values appear as gaps, not fabricated data. Metric counters reset when the Go process restarts. The React application also shows a metric sourced through Nginx's read-only `/prometheus/api/v1/query` proxy, not a duplicated REST sensor endpoint.

**Historical-series note:** Earlier versions of `hwm_device_online` used a changing `state` label, which caused duplicate relay series. The current metric uses only `device`; old Prometheus history may still exist until it ages out of the selected range/retention period. No destructive volume reset is required.

## 9. Simulator fault injection (local testing only)

Admin HTTP endpoints are not protected by JWT and are intended for localhost testing only. Examples:

```bash
# Sensor: none, slow, timeout, error, garbage, malformed, down
curl -X POST 'http://127.0.0.1:5121/fault?mode=down'
curl -X POST 'http://127.0.0.1:5121/fault?mode=none'
curl -X POST 'http://127.0.0.1:5121/fault?mode=malformed'
curl -X POST 'http://127.0.0.1:5121/temp?value=31'
curl -X POST 'http://127.0.0.1:5121/temp?value=auto'

# Thermocouple channel 1 fault and restore
curl -X POST 'http://127.0.0.1:5123/channel-fault?ch=1&broken=true'
curl -X POST 'http://127.0.0.1:5123/channel-fault?ch=1&broken=false'

# PWM persistent disagreement and recovery
curl -X POST 'http://127.0.0.1:5124/feedback?rpm=0'
curl -X POST 'http://127.0.0.1:5124/feedback?rpm=auto'
```

`malformed` mode uses a separate TCP responder that returns a deliberately truncated Modbus read frame. Fault injection can change physical-state simulations and automatic controller behavior; run it on an isolated development stack and restore state afterward.

## 10. Verification

### Test commands

Run each Go module **from its own directory**:

```bash
(cd backend && go test -count=1 ./... && go vet ./... && go test -race ./...)
(cd simulators && go test -count=1 ./... && go vet ./... && go test -race ./...)
(cd frontend && npm ci && npm test && npm run build)
```

Race testing requires a supported operating system/architecture and C compiler. Both Go modules declare **Go 1.26.0**; their Dockerfiles use `golang:1.26-alpine`.

With the full Compose stack already running, execute **from repository root**:

```bash
python scripts/smoke_test.py
python scripts/hysteresis_integration.py
python scripts/fault_recovery_integration.py --max-wait 150
python scripts/extended_smoke_test.py
```

These scripts exercise live simulator state and should be run sequentially in a disposable local test environment. Additional manual browser checks should cover login/token expiry, permissions, reset confirmation, faults and live dashboard values.


## 11. Submission hygiene

Keep source code, Dockerfiles/Compose, `.env.example`, Grafana provisioning/dashboard, tests, `README.md`, and [`AI_USAGE.md`](AI_USAGE.md). **Do not package `.env`, `.git`, private tokens, local databases, compiled binaries, `node_modules` or generated frontend assets.** Commit intended source changes before producing a clean-checkout submission archive. The audit does not modify application source code.

For AI assistance disclosures see [`AI_USAGE.md`](AI_USAGE.md).
