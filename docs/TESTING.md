# Testing Guide

## Go Tests

Run all tests:

```bash
go test -v ./...
```

Run core lifecycle integration tests:

```bash
go test -v ./internal/handlers -run TestSessionLifecycleHandlers
```

Run advanced networking integration tests:

```bash
go test -v ./internal/handlers -run TestAdvancedNetworkingIntegration
```

## Manual API Validation

1. Upload a binary.
2. Create a session.
3. Start and stream output using SSE.
4. Exercise pause/resume/stop.
5. Validate restore behavior.

## Advanced Networking Validation

Checklist:

- Configure SocketCAN and verify mount/access.
- Configure TAP interfaces and verify host setup.
- Configure Bluetooth HCI transport.
- Enable PCAP and verify download.
- Validate capability behavior based on enabled features.

## Coverage and Sanitizers

To get non-empty artifacts, compile target binaries with instrumentation.

Coverage archive:

```bash
curl -L -o coverage.tar.gz http://localhost:8080/api/sessions/<session-id>/coverage
```

Sanitizer archive/report:

```bash
curl -L -o sanitizers.tar.gz http://localhost:8080/api/sessions/<session-id>/sanitizers
curl "http://localhost:8080/api/sessions/<session-id>/sanitizers/report?tool=asan&limit=50"
```

## Firmware Example Flow

Build and upload examples:

```bash
cd firmware
./_scripts/build_all.sh
./_scripts/upload_all.sh http://localhost:8080
```

For firmware-specific expected outputs, see `firmware/TESTING.md`.
