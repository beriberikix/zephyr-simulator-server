# Operations Guide

## Runtime Prerequisites

- Docker and Docker Compose
- gVisor (`runsc`) recommended for sandbox runtime
- Host tools for networking workflows: `ip`, optional `can-utils`, Bluetooth tools

## Environment Variables

Backend defaults:

```bash
PORT=8080
BASE_IMAGE_URL=zephyr-emulator:latest
RUNTIME_NAME=runsc
DATA_DIR=/data/pb_data
```

Optional values:

```bash
STATE_FILE_PATH=/data/state.json
PCAP_BASE_DIR=/data/pcaps
PCAP_RETENTION=24h
PCAP_PRUNER_INTERVAL=1h
```

## Service Startup

```bash
docker build -f Dockerfile.emulator -t zephyr-emulator:latest .
docker compose up -d
```

Endpoints:

- Frontend: http://localhost:80
- API: http://localhost:8080/api
- Health: http://localhost:8080/api/health
- PocketBase admin: http://localhost:8080/_/

## Production Deployment and Updates

For production deployment on a droplet with Cloudflare + Caddy, use:

- docs/deploy/PRODUCTION_DEPLOYMENT.md

Source-code update path:

```bash
./scripts/deploy/upgrade_prod.sh
```

Rollback path:

```bash
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

## Authentication and Admin

Create or reset first PocketBase superuser:

```bash
docker exec zephyr-backend ./server superuser upsert admin@example.com strongpassword123
```

## Host Setup Examples

### SocketCAN

```bash
sudo ip link add dev vcan0 type vcan
sudo ip link set vcan0 up
```

### TAP

```bash
sudo ip tuntap add dev tap0 mode tap
sudo ip link set tap0 up
sudo ip addr add 192.168.100.1/24 dev tap0
```

### Bluetooth HCI

```bash
hciconfig
sudo chown root:docker /dev/hci0
sudo chmod 660 /dev/hci0
```

## Security Defaults

- Runtime isolation through gVisor when available.
- Restricted Linux capabilities with dynamic capability retention when networking features require them.
- Baseline response hardening headers are enabled.

## Artifact Lifecycle

- PCAP files are removed on session deletion.
- Anonymous session cleanup removes associated artifacts.
- Orphaned artifacts are pruned on interval.

## Common Issues

### Image Not Found

```bash
docker build -f Dockerfile.emulator -t zephyr-emulator:latest .
```

### Docker Connection Issues

```bash
docker ps
```

### gVisor Runtime Missing

```bash
export RUNTIME_NAME=runc
```

### API Reachability

```bash
curl http://localhost:8080/api/health
```
