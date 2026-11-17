## Project: Industrial Hardware Monitoring & Control Platform
## 1. AI Tools Used

**OpenAI ChatGPT** was used as a development assistant throughout selected stages of the project.

AI assistance was primarily used for technical research, architectural discussions, code review, debugging, test planning, and documentation.

## 2. Areas of AI Assistance

### Architecture and Design

AI was consulted to discuss architectural decisions and implementation approaches, including:

- Separation of Modbus device simulators, the Go backend, and the React frontend.
- Polling strategies, concurrency management, and graceful shutdown.
- Device fault handling, reconnection, and exponential backoff.
- Hysteresis-based automatic control and manual override behavior.
- JWT authentication and capability-based authorization.
- SQLite persistence, audit logging, and device health monitoring.

### Testing and Validation

AI assisted in designing and reviewing automated tests and integration scenarios, including:

- API authentication and authorization tests.
- Relay switching and read-back verification.
- Hysteresis boundary and transition tests.
- Modbus timeout, malformed-response, and recovery scenarios.
- Go race-detector testing.
- Frontend build and Docker configuration validation.

Tests were executed in the local development environment. Successful results were evaluated using the actual test outputs rather than relying solely on AI-generated expectations.

### Documentation

AI assistance was used to organize and improve project documentation, including:

- README structure and technical explanations.
- Architecture and component descriptions.
- Setup and deployment instructions.
- Core and Extended scope documentation.
- Known limitations and testing instructions.
- This AI usage disclosure.

## 3. Developer Responsibility

AI was used as a supporting tool, not as a substitute for engineering judgment.

Responsibility for the project architecture, selected technologies, implementation decisions, integration, testing, and final submission remains with the developer.

Generated suggestions were subject to review, modification, and validation against the project requirements.
