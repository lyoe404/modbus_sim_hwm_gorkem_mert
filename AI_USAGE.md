# AI tool usage disclosure

Project: Industrial Hardware Monitoring & Control Platform (HWM Take-Home, v3.1)
Disclosure required by brief §1.4.

## Tools used

| Tool | Where it was used |
|---|---|
| **Claude (Anthropic)** | Claude Code used as general coding assistant agent. ToolCode review of the backend; Dockerfiles, `docker-compose.yml` and `.env.example`; the `internal/control` hysteresis engine and its tests; the `config` table, the `/api/config/control` routes and their wiring in `cmd/backend`; small fixes (`errors.Is` in the auth guard, aligning the simulator Go version); a final read-through of the whole repository against the brief, which produced the gap list used to clean up this README. |
| **OpenAI ChatGPT** | Architecture discussions (simulator / backend / frontend split, polling and backoff, graceful shutdown, JWT capabilities, SQLite audit log); debugging; test planning (relay read-back, hysteresis boundaries, Modbus timeout/garbage scenarios, race detector); drafting documentation. |

## How AI output was handled

- Generated code was read, modified and validated against the brief before being committed.
- Where documentation written with AI help disagreed with the code, the code was treated as the source of truth and the documentation was corrected.
- I can explain and extend any part of the running code in the technical discussion (§8.3), including adding a relay channel, a second hysteresis zone, or a new capability end to end.


Core review follow-up: OpenAI Codex reviewed the supplied v3.1 assessment and corrected measurement-source capability enforcement, overlapping React polling, and backend health reporting. Added source authorization, polling cleanup/recovery, and health regression tests. Overall 
