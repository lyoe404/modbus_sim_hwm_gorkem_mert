# Simulator development notes

The project runs four independent Modbus TCP simulators (temperature/humidity on 5021, relay on 5022, thermocouple on 5023, speed/PWM on 5024) and a raw TCP remote-reset service on 9001. All original Modbus register maps and fault modes are retained.

## Device behavior

- **Temperature/humidity:** independent bounded random walks; temperature can be pinned using the existing `/temp` admin endpoint.
- **Relay:** eight independently writable/readable coils, including multiple-write read-back.
- **Thermocouple:** four independent random walks; per-channel pin state and per-channel `0x8000` fault sentinel. The store now exposes `UnpinChannel(ch)`, and the admin API provides `POST /channel-unpin?ch=0` to release one channel. Existing `UnpinAll` is preserved.
- **Speed/PWM:** Holding register 0 is the writable PWM command (0–100); register 1 is read-only measured RPM (0–3000). Multi-register writes validate every address and value before changing state. For persistent command/feedback divergence, use `POST /feedback?rpm=700`; return to simulated convergence with `POST /feedback?rpm=auto`. The normal ramp remains 150 RPM per second.
- **Remote reset:** `PING`, `STATUS`, `RESET <device>` retain their original responses. `STATUS <device>` additionally reports the device's last known reset state (`online`, `offline`, `recovering`, `recovery_failed`). Repeated resets for the same device extend the offline window without allowing an older recovery worker to bring it online early. Recovery failures are logged and retried up to three times. Successful resets log the device and UTC timestamp. User-identity audit belongs in the authenticated backend, not this simulator.

## Docker

The remote-reset container listens on port 9001, now published as `127.0.0.1:9001:9001`. Its Compose healthcheck sends a TCP `PING` and expects `PONG`. Other service ports and backend wiring are unchanged.

## Tests

From `simulators/`:

```sh
go test ./...
go test -race ./...
```

Unit tests cover device bounds, relay read-back, thermocouple channel isolation, PWM write atomicity/feedback, and remote-reset commands, recovery and reset extension. The existing fault-injection and timeout implementation has intentionally **not** been changed. In particular, timeout still waits and then returns Modbus exception 0x04.
