# Architecture Overview — Zephyr Remote Emulator

## System Design

This document describes the high-level architecture, data flow, and component interactions for the Zephyr Remote Emulator system.

## High-Level Architecture Diagram

```
┌─────────────────────────────────────────────────────────────┐
│                  Client Browser (React SPA)                 │
│  ┌──────────────┬────────────────┬──────────────────────┐  │
│  │ Upload Page  │ Emulator Panel │ Terminal View (SSE)  │  │
│  └──────────────┴────────────────┴──────────────────────┘  │
└────────────────────────────┬────────────────────────────────┘
                             │
                    HTTP + SSE (EventSource)
                             │
┌────────────────────────────▼────────────────────────────────┐
│              Caddy Reverse Proxy (:80, :443)                │
│             (Route /api/* → Backend, / → Frontend)         │
└────────────────────────────┬────────────────────────────────┘
                             │
                    HTTP/1.1 (Reverse Proxy)
                             │
┌────────────────────────────▼────────────────────────────────┐
│                 Backend API Server (Go)                     │
│  ┌────────────────────────────────────────────────────────┐ │
│  │  HTTP Router (stdlib net/http)                         │ │
│  │  ├── POST /api/binaries                                │ │
│  │  ├── GET /api/sessions                                 │ │
│  │  ├── POST /api/sessions/{id}/start                    │ │
│  │  └── GET /api/sse (SSE streamer)                      │ │
│  └────────────────────────────────────────────────────────┘ │
│  ┌─────────────────────────┬──────────────────┐            │
│  │ Container Manager       │ UART Multiplexer │ Session Mgr│
│  │ (Docker SDK)            │  (FIFO readers)  │ (Snapshots)│
│  └─────────────────────────┴──────────────────┘            │
│  ┌────────────────────────────────────────────────────────┐ │
│  │ PocketBase (SQLite DB + Embedded Server)               │ │
│  │ Collections: binaries, sessions, uart_logs, configs    │ │
│  └────────────────────────────────────────────────────────┘ │
└────────┬──────────────────────────────────────────────────┬──┘
         │                                                   │
   Docker Socket                               SQLite File
  /var/run/docker.sock                    (./data/pb_data)
         │                                                   │
         │                                                   │
┌────────▼──────────────────────────────────────────────────▼──┐
│              Docker Daemon (Host Machine)                     │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  Emulator Containers (gVisor runsc runtime)         │   │
│  │  ┌─────────────────────────────────────────────┐    │   │
│  │  │ Container: session-{sessionId}              │    │   │
│  │  │ ├── Binary: /usr/bin/native_sim              │   │   │
│  │  │ ├── Volumes: /emu (flash.bin, eeprom)        │   │   │
│  │  │ ├── Named Pipes: /tmp/session-*-uart*.fifo  │   │   │
│  │  │ └── Network: bridge (no host access)        │   │   │
│  │  └─────────────────────────────────────────────┘    │   │
│  │  ┌─────────────────────────────────────────────┐    │   │
│  │  │ Container: session-{sessionId}              │    │   │
│  │  │ (. . . more emulator instances . . .)       │    │   │
│  │  └─────────────────────────────────────────────┘    │   │
│  └──────────────────────────────────────────────────────┘   │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  Volumes: /var/lib/zephyr-emu/sessions/{id}         │   │
│  │  ├── flash.bin (NVS storage)                         │   │
│  │  └── eeprom.bin (EEPROM storage)                     │   │
│  └──────────────────────────────────────────────────────┘   │
└───────────────────────────────────────────────────────────────┘
```

## Core Components

### 1. Backend (Go)

#### HTTP Router & Handlers (`cmd/server/main.go`, `internal/handlers/handlers.go`)
- **Responsibility:** REST API endpoint handlers, request parsing, response serialization
- **Key Endpoints:**
  - `POST /api/binaries` — Upload and analyze binary
  - `POST /api/sessions` — Create session
  - `POST /api/sessions/{id}/start` — Start emulator in container
  - `GET /api/sse?session={id}` — SSE stream for real-time updates
- **Context Passing:** Each handler receives `container.Manager` and `uart.Multiplexer` to execute operations

#### Container Manager (`internal/container/manager.go`)
- **Responsibility:** Docker lifecycle orchestration
- **Key Methods:**
  - `CreateContainer()` — Build container config, manage volumes
  - `StartContainer()` — Execute `native_sim` binary
  - `PauseContainer()` / `ResumeContainer()` — Hibernation
  - `StopContainer()` — Graceful shutdown
- **Integration:** Uses Docker SDK to manage remote containers

#### Binary Analyzer (`internal/container/binary.go`)
- **Responsibility:** Static analysis of uploaded ELF binaries
- **Detection:**
  - 32/64-bit architecture via ELF header
  - Static linking (presence of `.dynamic` section)
  - Zephyr version (from `.note.zephyr` or `readelf` fallback)
- **Output:** `Binary` struct with metadata + checksum

#### Flags Builder (`internal/container/flags.go`)
- **Responsibility:** Translate UI settings → native_sim CLI arguments
- **Example:** `FlagConfig{Seed: 12345, UseRealTime: true}` → `["--seed=12345", "--rt"]`
- **Validation:** Checks if binary supports requested flags

#### UART Multiplexer (`internal/uart/multiplexer.go`)
- **Responsibility:** Read from UART FIFO backends, buffer output, broadcast to clients
- **Architecture:**
  - Per-UART reader goroutines (e.g., UART0, UART1)
  - Circular buffer (10K lines by default) to avoid OOM
  - Subscriber channels for SSE clients
- **Flow:**
  1. Session created → FIFOs created at `/tmp/session-{id}-uart{N}.fifo`
  2. Container `native_sim` writes to FIFOs (via `--uart-bin` flag)
  3. Multiplexer reads from FIFOs → buffers → broadcasts SSE events
  4. Browser SSE client receives real-time UART output

#### Session Snapshots (`internal/session/snapshots.go`)
- **Responsibility:** Serialize/deserialize session state for persistence
- **Data Captured:**
  - Session ID, binary ID, state, seed, flags
  - Volume paths (flash.bin, eeprom.bin)
- **Use Case:** Stop session → snapshot stored in DB → later restore from snapshot + Docker volumes

#### PocketBase Integration (`internal/pocketbase/db.go`)
- **Responsibility:** Database initialization, collections schema
- **Collections:**
  - `binaries` — Uploaded binary metadata
  - `sessions` — Active/paused/stopped sessions
  - `uart_logs` — Terminal output history (optional archival)
  - `configs` — System defaults (timeout, seed, etc.)

### 2. Frontend (React + TypeScript)

#### Pages
- **Dashboard** (`web/src/pages/Dashboard.tsx`) — Overview of sessions, quick stats
- **Upload** (`web/src/pages/Upload.tsx`) — Drag-drop binary upload, metadata display
- **Emulator** (`web/src/pages/Emulator.tsx`) — Main control panel + terminal interface
- **Sessions** (`web/src/pages/Sessions.tsx`) — List/manage/restore sessions

#### Components
- **Layout** (`web/src/components/Layout.tsx`) — Header, nav, footer
- **UARTTerminal** — Multi-tab terminal with copy/clear
- **ControlPanel** — Start/stop/pause controls and seed tuning

#### Utilities
- **API client** (`web/src/utils/api.ts`) — Axios wrapper for `/api/*` routes
- **SSE hook** (`web/src/hooks/useSSE.ts`) — EventSource listener for real-time updates

### 3. Infrastructure

#### Docker Compose (`docker-compose.yml`)
- **Services:**
  - `backend` — Go server (port 8080)
  - `frontend` — React dev server (port 5173 internal, exposed via Caddy)
  - `caddy` — Reverse proxy (port 80)
- **Networking:** Named network `zephyr-network` for service communication
- **Volumes:** Bind mounts for development, named volumes for Caddy data

#### Caddy (`Caddyfile`)
- **Entry Point:** `:80`
- **Routes:**
  - `/api/*` → `backend:8080` (API requests)
  - `/api/sse` → `backend:8080` (SSE with keep-alive headers)
  - `/` → `frontend:5173` (SPA fallback routing)

#### Dockerfile (`Dockerfile`)
- **Multi-stage build:**
  1. Builder stage (Go compile)
  2. Final stage (Alpine + Go binary + runtime tools)
- **Runtime tools:** `docker` CLI, `readelf`, `ca-certificates`

## Data Flow

### Scenario 1: Upload Binary

```
1. User selects .elf file
   ↓
2. Frontend: POST /api/binaries (multipart form-data)
   ↓
3. Backend handler: handlers.HandleUploadBinary()
   - Save file to /tmp/binaries/{filename}
   - Call analyzer.Analyze(filePath)
   ↓
4. Analyzer: ELF parsing
   - Open ELF header
   - Detect bits (ELFCLASS32 vs ELFCLASS64)
   - Check for .dynamic section → static linking
   - Extract Zephyr version from .note.zephyr
   - Calculate SHA256 checksum
   ↓
5. Store Binary record in PocketBase DB
   ↓
6. Return JSON with binary metadata
   ↓
7. Frontend: Display success + binary metadata
```

### Scenario 2: Start Emulator Session

```
1. User clicks "Start" → selects binary + seed
   ↓
2. Frontend: POST /api/sessions/{id}/start
   ↓
3. Backend: handlers.HandleStartSession()
   ↓
4. Create container:
   - Validate binary exists + is compatible
   - Generate CLI flags (--seed, --rt, --uart-bin, etc.)
   - Create Docker volumes (/var/lib/zephyr-emu/sessions/{id}/)
   - Create UART FIFOs (/tmp/session-{id}-uart{0,1}.fifo)
   - Build container config
   - Call manager.CreateContainer()
   ↓
5. Docker daemon creates container with gVisor runtime
   ↓
6. Start Multiplexer UART readers:
   - Spawn goroutine for UART0 reader
   - Wait on O_NONBLOCK read from /tmp/session-{id}-uart0.fifo
   ↓
7. Execute container:
   - docker run native_sim --seed=12345 --rt --uart-bin0=/tmp/session-{id}-uart0.fifo
   ↓
8. native_sim starts → writes to UART0 FIFO
   ↓
9. Multiplexer reads UART0 → buffers → broadcasts SSE event
   ↓
10. Frontend SSE listener receives event → adds line to terminal
    ↓
11. Return success to frontend → update UI state to "running"
```

### Scenario 3: Pause/Resume Session

```
Pause:
  Frontend: POST /api/sessions/{id}/pause
    → Backend: container.Pause()
    → Docker: container state = "paused"
    → State persisted to DB

Resume:
  Frontend: POST /api/sessions/{id}/resume
    → Backend: container.Unpause()
    → Docker: container state = "running"
    → UART readers continue from where they left off
```

### Scenario 4: Restore Session Snapshot

```
1. User selects "Restore from snapshot"
   ↓
2. Frontend: POST /api/sessions/{id}/restore
   ↓
3. Backend:
   - Load snapshot JSON from DB
   - Validate snapshot (check binary still exists, volumes present)
   - Create new session with same flags
   - Create new container
   - Copy volumes from old session to new (docker volume cp)
   ↓
4. Start new container (volumes now contain old emulator state)
   ↓
5. Return new session ID to frontend
```

## State Machine

### Session States

```
┌─────────┐
│ STOPPED │
└────┬────┘
     │ POST /api/sessions/{id}/start
     ▼
┌─────────────┐
│   RUNNING   │ ◄──┐
└────┬────────┘    │
     │             │ resume
     │ pause    ┌──┴─────┐
     │          │ PAUSED │
     └─────────►└────────┘

POST /api/sessions/{id}/stop
  (any state) → STOPPED

Docker.Stop() triggers cleanup:
  - Volumes preserved
  - Snapshots stored in DB
  - Container removed
```

## Error Handling Strategy

1. **Binary Validation** — ELF parse errors → 400 Bad Request + error message
2. **Container Errors** — Docker daemon errors → 500 Internal Server Error + rollback
3. **State Conflicts** — e.g., stop paused session → 409 Conflict
4. **Resource Limits** — Handle max concurrent sessions limit → 429 Too Many Requests

## Security Considerations

### Container Isolation
1. **Runtime:** gVisor (`runsc`) prevents breakout attacks
2. **Capabilities:** Drop dangerous caps (NET_ADMIN, SYS_ADMIN)
3. **Filesystems:** Read-only root, only allow `/tmp` and `/emu` writes
4. **Networking:** Bridge mode (no host network access) for Core platform

### Data Isolation
- Sessions have dedicated Docker volumes (no cross-session access)
- UART FIFOs are session-specific
- Local username/password authentication with JWT bearer tokens
- OAuth2/OIDC provider integration planned as next auth step

### Input Validation
- Binary upload: Check ELF magic number, size limits
- Session create: Validate seed range, timeout bounds
- CLI flags: Sanitize to prevent injection attacks

## Performance Considerations

### Circular Buffer
- UART multiplexer uses 10K line circular buffer
- Prevents OOM on long-running sessions
- SSE clients can request history via `/api/sessions/{id}/uart-history`

### Goroutine Pooling
- UART reader goroutines detach from request context
- Cleanup via `multiplexer.Stop()` or context cancellation

### Container Limits
- CPU quota: 0.5 CPUs per container (adjustable)
- Memory limit: 512MB default (configurable)

## Planned Architecture Extensions

### Networking Extensions
- Add `internal/bus/socketcan.go` for CAN frame handling
- Extend container config: `--net=host` mode for SocketCAN
- Add network bridge layer between host vcan and container

### Debugging Extensions
- Add `internal/debug/gdb_proxy.go` — WebSocket↔gdbserver proxy
- Container: map gdbserver to host port 5900+{N}
- Frontend: WebSocket connection to proxy endpoint

## Scalability Path

- **Current target:** Single machine, ~10 concurrent containers
- **Coverage and sanitizers:** Kubernetes helm chart, multi-node orchestration, persistent storage (shared NFS for sessions)

---

**Last Updated:** 2026-03-30  
**Version:** 0.1.0
