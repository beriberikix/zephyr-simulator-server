# API Reference

This document contains the full HTTP API surface for Zephyr Simulator Server.

## Base URL

- Local: http://localhost:8080
- API root: http://localhost:8080/api

## Health

- `GET /api/health` — Service health status.

## Binary Endpoints

- `POST /api/binaries` — Upload a Zephyr native_sim ELF.
- `GET /api/binaries` — List uploaded binaries.
- `GET /api/binaries/{id}` — Fetch one binary.
- `DELETE /api/binaries/{id}` — Delete one binary.

Upload example:

```bash
curl -X POST http://localhost:8080/api/binaries \
  -F "binary=@./build/zephyr/zephyr.elf"
```

## Session Endpoints

- `POST /api/sessions` — Create session.
- `GET /api/sessions` — List sessions.
- `GET /api/sessions/{id}` — Get session details.
- `PATCH /api/sessions/{id}` — Update session config.
- `DELETE /api/sessions/{id}` — Delete session.
- `POST /api/sessions/{id}/start` — Start session.
- `POST /api/sessions/{id}/stop` — Stop session.
- `POST /api/sessions/{id}/pause` — Pause session.
- `POST /api/sessions/{id}/resume` — Resume session.
- `POST /api/sessions/{id}/restore` — Restore from persisted runtime state.

Create session example:

```bash
curl -X POST http://localhost:8080/api/sessions \
  -H "Content-Type: application/json" \
  -d '{
    "binary_id": "<binary-id>",
    "seed": 42,
    "use_real_time": false,
    "timeout_seconds": 300
  }'
```

## Streaming Endpoints

- `GET /api/sse?session={sessionId}` — SSE stream for live UART/container output.

## Advanced Networking Endpoints

- `PATCH /api/sessions/{id}` with networking fields:
  - `can_devices`
  - `tap_interfaces`
  - `bluetooth_config`
  - `uart_forwarding`
  - `pcap_enabled`
- `GET /api/sessions/{id}/pcap` — Download PCAP artifact.
- `POST /api/network/setup` — Create and configure required host network resources.
- `POST /api/network/benchmark` — Estimate throughput from session PCAP artifact.

Configure CAN example:

```bash
curl -X PATCH http://localhost:8080/api/sessions/<session-id> \
  -H "Content-Type: application/json" \
  -d '{
    "can_devices": [
      {
        "name": "vcan0",
        "host_device": "/dev/vcan0",
        "bitrate": 500000
      }
    ]
  }'
```

Configure TAP (bridge mode) example:

```bash
curl -X PATCH http://localhost:8080/api/sessions/<session-id> \
  -H "Content-Type: application/json" \
  -d '{
    "tap_interfaces": [
      {
        "name": "tap0",
        "host_interface": "tap0",
        "ip_address": "192.168.100.2",
        "netmask": "255.255.255.0",
        "enable_bridge": true,
        "bridge_interface": "br0"
      }
    ]
  }'
```

Configure TAP (pasta mode) example:

```bash
curl -X PATCH http://localhost:8080/api/sessions/<session-id> \
  -H "Content-Type: application/json" \
  -d '{
    "tap_interfaces": [
      {
        "name": "tap0",
        "host_interface": "tap0",
        "pasta_mode": true
      }
    ]
  }'
```

Configure Bluetooth HCI-over-UART example:

```bash
curl -X PATCH http://localhost:8080/api/sessions/<session-id> \
  -H "Content-Type: application/json" \
  -d '{
    "bluetooth_config": {
      "enabled": true,
      "transport": "hci_uart",
      "uart_device_path": "/dev/ttyUSB0",
      "uart_baud_rate": 1000000,
      "advertising_mode": "connectable"
    }
  }'
```

Configure UART network forwarding example:

```bash
curl -X PATCH http://localhost:8080/api/sessions/<session-id> \
  -H "Content-Type: application/json" \
  -d '{
    "uart_forwarding": {
      "enabled": true,
      "mode": "tun",
      "host_device_path": "/dev/ttyUSB1",
      "container_device_path": "/dev/ttyTUN0",
      "baud_rate": 115200
    }
  }'
```

Enable PCAP example:

```bash
curl -X PATCH http://localhost:8080/api/sessions/<session-id> \
  -H "Content-Type: application/json" \
  -d '{"pcap_enabled": true}'
```

Host networking setup example:

```bash
curl -X POST http://localhost:8080/api/network/setup \
  -H "Content-Type: application/json" \
  -d '{
    "can_devices": [{"name": "vcan0", "host_device": "/dev/vcan0"}],
    "tap_interfaces": [{"host_interface": "tap0", "enable_bridge": true, "bridge_interface": "br0"}]
  }'
```

## Debugging Endpoints

- `PATCH /api/sessions/{id}` with `debug_config`.
- `GET /api/sessions/{id}/debug-target` — Debugger host/port details.
- `GET /api/sessions/{id}/debug/ws` — WebSocket byte stream proxy to gdbserver.
- `GET /api/sessions/{id}/debug/breakpoints` — List breakpoints.
- `POST /api/sessions/{id}/debug/breakpoints` — Add breakpoint.
- `DELETE /api/sessions/{id}/debug/breakpoints/{number}` — Remove breakpoint.
- `GET /api/sessions/{id}/debug/stack` — Current stack frames.

Enable debugging example:

```bash
curl -X PATCH http://localhost:8080/api/sessions/<session-id> \
  -H "Content-Type: application/json" \
  -d '{
    "debug_config": {
      "enabled": true,
      "port": 4444,
      "wait_for_gdb": true
    }
  }'
```

## Coverage and Sanitizer Endpoints

- `PATCH /api/sessions/{id}` with:
  - `coverage_enabled`
  - `asan_enabled`
  - `ubsan_enabled`
- `GET /api/sessions/{id}/coverage` — Download coverage archive.
- `GET /api/sessions/{id}/sanitizers` — Download sanitizer archive.
- `GET /api/sessions/{id}/sanitizers/report` — Parsed sanitizer findings (`tool`, `q`, `limit` query params supported).

Coverage example:

```bash
curl -X PATCH http://localhost:8080/api/sessions/<session-id> \
  -H "Content-Type: application/json" \
  -d '{"coverage_enabled": true}'
```

Sanitizer example:

```bash
curl -X PATCH http://localhost:8080/api/sessions/<session-id> \
  -H "Content-Type: application/json" \
  -d '{"asan_enabled": true, "ubsan_enabled": true}'
```

## Notes

- Debugging endpoints require the session to be running and debug mode enabled.
- Coverage and sanitizer outputs require binaries compiled with the relevant instrumentation flags.
- PCAP artifacts are cleaned on session deletion and periodic prune operations.
