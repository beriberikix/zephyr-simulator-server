# Zephyr Simulator Server

Web platform for uploading and running Zephyr `native_sim` binaries inside isolated containers, with live UART streaming, advanced networking options, debugging hooks, and artifact collection.

## Core Capabilities

- Binary upload and ELF metadata analysis
- Session lifecycle management (create/start/stop/pause/resume/restore)
- Live UART streaming with SSE
- Advanced networking support:
  - SocketCAN
  - TAP interfaces (bridge or pasta mode)
  - Bluetooth HCI and HCI-over-UART
  - UART-based network forwarding
  - PCAP capture and download
- Remote debugger integration (gdbserver proxy)
- Coverage and sanitizer artifact collection

## Quick Start

### Prerequisites

- Docker and Docker Compose
- Go 1.25+
- Node.js 20+
- gVisor `runsc` (recommended)

### Setup

```bash
cd zephyr-simulator-server
git submodule update --init --recursive
docker build -f Dockerfile.emulator -t zephyr-emulator:latest .
go mod download
docker compose up -d
```

### Endpoints

- Frontend: http://localhost:80
- API: http://localhost:8080/api
- Health: http://localhost:8080/api/health

## Production Deployment

Production deployment for DigitalOcean + Cloudflare + Caddy is documented in [docs/deploy/PRODUCTION_DEPLOYMENT.md](docs/deploy/PRODUCTION_DEPLOYMENT.md).

Core commands on the droplet:

```bash
./scripts/deploy/deploy_prod.sh
./scripts/deploy/upgrade_prod.sh
./scripts/deploy/rollback_prod.sh
```

## Local Development

Backend:

```bash
go run ./cmd/server/main.go
```

Frontend:

```bash
cd web
npm install
npm run dev
```

## Testing

```bash
go test -v ./...
```

Focused handler tests:

```bash
go test -v ./internal/handlers -run TestSessionLifecycleHandlers
go test -v ./internal/handlers -run TestAdvancedNetworkingIntegration
```

## Firmware Examples

The `firmware/` directory contains buildable Zephyr `native_sim` examples for smoke and feature validation.

Quick flow:

```bash
cd firmware
./_scripts/build_all.sh
./_scripts/upload_all.sh http://localhost:8080
```

See `firmware/README.md` and `firmware/TESTING.md` for details.

## Documentation

- `docs/API_REFERENCE.md` — Full endpoint list and request examples
- `docs/OPERATIONS.md` — Runtime config, host setup, and ops troubleshooting
- `docs/TESTING.md` — Test strategy and execution commands
- `docs/ARCHITECTURE.md` — System architecture overview
- `docs/DATABASE_SCHEMA.md` — PocketBase collection schema
- `NATIVE_SIM_TESTABLE_FEATURES.md` — Native simulator feature coverage matrix

## License

Apache 2.0 (see `LICENSE`)
