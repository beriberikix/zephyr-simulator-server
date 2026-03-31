# Testable Features with native_sim

This document outlines which features of the Zephyr Simulator Server can be tested with **Zephyr's `native_sim` board** — the native host simulator that runs on Linux, macOS, and Windows.

## Overview: What is native_sim?

`native_sim` is a Zephyr board target that compiles and runs Zephyr applications as **native Linux/host executables** instead of firmware images. It provides:
- Fast compilation and execution
- Full access to host syscalls and devices
- Support for deterministic simulation (`--seed`)
- Real-time mode (`--rt` flag)
- Multi-UART simulation
- Network device simulation (TAP, SocketCAN, Bluetooth HCI)

**Documentation:** [Zephyr Native Simulator](https://docs.zephyrproject.org/latest/boards/native/native_sim/doc/index.html)

---

## Core Platform Features ✅ FULLY TESTABLE

### 1.1 Binary Upload & Analysis
**Status:** ✅ **FULLY TESTABLE**
- Upload 32/64-bit ELF binaries (static + dynamic linking)
- Extract Zephyr version from ELF notes
- Calculate SHA256 checksums
- Detect architecture and linking model

**Test Approach:**
```bash
# Compile a simple Zephyr app for native_sim
west build -b native_sim samples/hello_world

# Upload the resulting ELF to the server
curl -F "binary=@build/zephyr/zephyr.elf" \
  http://localhost:8080/api/binaries
```

**Test Coverage:**
- ✅ 32-bit binaries
- ✅ 64-bit binaries
- ✅ Static vs. dynamic linking detection
- ✅ Version extraction
- ✅ Checksum validation

---

### 1.2 Emulator Lifecycle Management
**Status:** ✅ **FULLY TESTABLE**

#### Create Session
- Create isolated Docker container from uploaded binary
- Initialize volumes for flash.bin and eeprom.bin persistence
- Set up UART FIFO backends

**Test Approach:**
```bash
# POST /api/sessions
curl -X POST http://localhost:8080/api/sessions \
  -H "Content-Type: application/json" \
  -d '{
    "binary_id": "abc123",
    "seed": 42,
    "use_real_time": false,
    "timeout_seconds": 300
  }'
```

#### Start Session
- Launch container with proper flags
- Connect UART FIFOs
- Stream container output

**Test Coverage:**
- ✅ Container creation with correct flags
- ✅ Volume mounting
- ✅ UART FIFO setup
- ✅ Process state validation
- ✅ Error handling (binary not found, OOM, etc.)

#### Stop Session
- Graceful container shutdown
- Resource cleanup
- Timeout handling

#### Pause/Resume Session
- Checkpoint container state using Docker pause API
- Restore from checkpoint
- Verify memory state is preserved

**Test Coverage:**
- ✅ State transitions (Running ↔ Paused ↔ Stopped)
- ✅ Data persistence across pause/resume
- ✅ Container cleanup on stop
- ✅ Timeout enforcement

---

### 1.3 UART Terminal (Real-time Multi-UART)
**Status:** ✅ **FULLY TESTABLE**

**Features:**
- Multiple UART channels (UART0, UART1, UART2, etc.)
- FIFO-based backend streaming
- Circular buffer (10K lines) for output history
- SSE (Server-Sent Events) streaming to browser
- Real-time log capture

**Test Approach:**
```bash
# Compile app with multiple UART outputs
west build -b native_sim samples/subsys/console/echo

# Start session and stream UART data
curl http://localhost:8080/api/sse?session_id=xyz \
  -H "Accept: text/event-stream"
```

**Test Coverage:**
- ✅ Single UART (UART0) read/write
- ✅ Multiple UART channels (UART0, UART1, UART2)
- ✅ Buffering and FIFO handling
- ✅ SSE event streaming format
- ✅ Real-time data propagation to WebSocket clients
- ✅ Buffer overflow protection (circular buffer)
- ✅ UART data integrity (no corruption)

---

### 1.4 Session Snapshots (State Persistence)
**Status:** ✅ **FULLY TESTABLE**

**Features:**
- Save/restore session state
- Persist flash.bin and eeprom.bin volumes
- Checkpoint session metadata (seed, flags, configuration)

**Test Approach:**
```bash
# Snapshot session state
curl -X POST http://localhost:8080/api/sessions/{id}/snapshot

# Restore from snapshot
curl -X POST http://localhost:8080/api/sessions/{id}/restore
```

**Test Coverage:**
- ✅ Save session state to SQLite
- ✅ Snapshot volume data (flash/EEPROM)
- ✅ Restore volumes from snapshot
- ✅ Verify data integrity after restore
- ✅ Multiple snapshots per session
- ✅ Cross-binary snapshot compatibility (if applicable)

---

### 1.5 Deterministic Execution
**Status:** ✅ **FULLY TESTABLE**

#### Seed-based RNG Control
- Pass `--seed=<uint64>` to native_sim
- PRNG generates deterministic sequences when seeded
- Useful for reproducible testing

**Test Approach:**
```bash
# Run same binary with same seed twice
# Verify identical UART output

curl -X POST http://localhost:8080/api/sessions \
  -H "Content-Type: application/json" \
  -d '{"binary_id": "abc", "seed": 12345, "use_real_time": false}'

# Output should be identical to previous run with seed=12345
```

**Test Coverage:**
- ✅ Same seed → identical output
- ✅ Different seeds → different output
- ✅ Seed preservation across pause/resume
- ✅ Seed validation (uint64 range)

#### Real-time Execution (`--rt` flag)
- Enable hardware-accurate timing
- Real wall-clock time for timers and delays
- Disable when determinism required

**Test Coverage:**
- ✅ `--rt` flag applied correctly
- ✅ Real-time behavior (delays match wall-clock)
- ✅ Disabled when determinism required
- ✅ Timing precision for various delays

---

### 1.6 Container Isolation (gVisor/runsc)
**Status:** ✅ **FULLY TESTABLE**

**Features:**
- gVisor runtime (`runsc`) for strict sandboxing
- Capability dropping (no `CAP_NET_ADMIN`, `CAP_NET_RAW`, `CAP_SYS_ADMIN` in Core platform)
- Read-only root filesystem
- Restricted mount points (`/tmp`, `/emu` only writable)
- Network bridge (no host access in Core platform)

**Test Approach:**
```bash
# Verify container runs with gvisor runtime
docker inspect {container_id} | grep -i runtimePath

# Verify capabilities are restricted
docker inspect {container_id} | grep CapAdd/CapDrop

# Verify container cannot escape sandbox
# (e.g., attempt host file read → should fail)
```

**Test Coverage:**
- ✅ Container uses gVisor runtime
- ✅ Capabilities appropriately dropped
- ✅ Filesystem is read-only outside allowed mounts
- ✅ Escape attempts are blocked
- ✅ Container cannot access host files
- ✅ Container resource limits enforced

---

## Advanced networking: Advanced Networking ✅ FULLY TESTABLE

### 2.1 TAP Interfaces (Virtual Network)
**Status:** ✅ **FULLY TESTABLE**

**Modes:**
1. **TAP Bridge Mode** — Bridge emulator's TAP interface to physical network
2. **Pasta Mode** — Use pasta for transparent TCP/UDP forwarding
3. **TUN-over-UART** — Tunnel network over UART device

**Testable with native_sim:**

#### TAP Bridge Mode
- Create TAP interface on host
- Bridge to physical network interface
- Zephyr receives IP address via DHCP or static config
- Test IP connectivity (ping, HTTP, etc.)

**Test Approach:**
```bash
# Enable TAP interface for session
curl -X POST http://localhost:8080/api/sessions/{id}/update \
  -H "Content-Type: application/json" \
  -d '{
    "tap_interfaces": [{
      "name": "tap0",
      "host_interface": "tap0",
      "ip_address": "192.168.1.100",
      "netmask": "255.255.255.0",
      "enable_bridge": false
    }]
  }'

# Test from Zephyr app: ping host, HTTP request, etc.
```

**Test Coverage:**
- ✅ TAP interface creation on host
- ✅ Interface brought up successfully
- ✅ IP assignment (static + DHCP)
- ✅ Connectivity (ping, TCP/UDP)
- ✅ Packet capture/verification
- ✅ Interface cleanup on session stop
- ✅ Bridge mode (TAP ↔ physical interface)
- ✅ Error handling (permission denied, existing interface, etc.)

#### Pasta Mode
- Use pasta for transparent forwarding
- No bridge needed
- TCP/UDP traffic forwarded transparently

**Test Coverage:**
- ✅ Pasta process spawned correctly
- ✅ TCP/UDP forwarding works
- ✅ No packet loss
- ✅ Standard port forwarding

#### TUN-over-UART
- Tunnel network packets over UART device
- Useful for slow or isolated connections

**Test Coverage:**
- ✅ UART device mounted in container
- ✅ Network stack receives packets via UART
- ✅ Data integrity over serial link
- ✅ Baud rate negotiation

---

### 2.2 SocketCAN Interfaces
**Status:** ✅ **FULLY TESTABLE**

**Features:**
- Route Linux SocketCAN devices (`/dev/vcan*`) into emulator
- CAN frames sent/received via host vCAN device
- Useful for automotive/IoT testing

**Test Approach:**
```bash
# Create virtual CAN device on host
sudo ip link add dev vcan0 type vcan
sudo ip link set vcan0 up

# Enable CAN for session
curl -X POST http://localhost:8080/api/sessions/{id}/update \
  -H "Content-Type: application/json" \
  -d '{
    "can_devices": [{
      "name": "vcan0",
      "host_device": "/dev/vcan0",
      "bitrate": 500000
    }]
  }'

# Zephyr app can now send/receive CAN frames
```

**Test Coverage:**
- ✅ vCAN device mounted in container
- ✅ CAN frame sending (emulator → host socket)
- ✅ CAN frame receiving (host socket → emulator)
- ✅ Bitrate configuration
- ✅ Multiple CAN devices per session
- ✅ Error handling (device not found, permissions)

**Real-world Testable Scenarios:**
- Zephyr CAN driver functionality tests
- Vehicle diagnostics simulation
- Automotive protocol testing (OBD-II, etc.)
- Multi-node CAN scenarios (multiple sessions)

---

### 2.3 Bluetooth HCI
**Status:** ✅ **FULLY TESTABLE**

**Modes:**
1. **HCI Device** — Direct pass-through of `/dev/hci0`
2. **HCI-over-UART** — Bluetooth stack over UART device

**Testable Scenarios:**

#### HCI Device Mode
- Pass Linux Bluetooth device to emulator
- Zephyr can advertise, scan, connect
- Works with real Bluetooth adapters or virtual adapters (Linux btusb)

**Test Approach:**
```bash
# Enable Bluetooth HCI for session
curl -X POST http://localhost:8080/api/sessions/{id}/update \
  -H "Content-Type: application/json" \
  -d '{
    "bluetooth_config": {
      "enabled": true,
      "transport": "hci",
      "hci_device": "/dev/hci0"
    }
  }'

# Zephyr app can now:
# - Advertise BLE packets
# - Scan for devices
# - Connect to peers
```

**Test Coverage:**
- ✅ HCI device mounted in container
- ✅ BLE advertisement (emulator → host)
- ✅ BLE scanning (emulator receives host advertisements)
- ✅ Connection establishment
- ✅ GATT services/characteristics
- ✅ Data transmission
- ✅ Multiple sessions (2+ emulators) can communicate via BLE

#### HCI-over-UART Mode
- Route Bluetooth over UART instead of native HCI device
- Useful for testing serial-based Bluetooth modules

**Test Coverage:**
- ✅ UART device mounted
- ✅ BLE stack communicates via UART
- ✅ Data integrity over serial link

**Real-world Testable Scenarios:**
- BLE peripheral simulation (e.g., beacon, sensor)
- BLE central (scanner) functionality
- Multi-peripheral scenarios (session1 advertises → session2 scans)
- GATT protocol testing
- Bluetooth security (pairing, encryption)

---

### 2.4 PCAP Network Capture
**Status:** ✅ **FULLY TESTABLE**

**Features:**
- Record network packets during session execution
- PCAP format (Wireshark compatible)
- Optional for TAP/Pasta/Bluetooth HCI devices

**Test Approach:**
```bash
# Enable PCAP capture for session
curl -X POST http://localhost:8080/api/sessions/{id}/update \
  -H "Content-Type: application/json" \
  -d '{"pcap_enabled": true}'

# After session completes, download PCAP
curl http://localhost:8080/api/sessions/{id}/pcap \
  -o capture.pcap

# Analyze in Wireshark
wireshark capture.pcap
```

**Test Coverage:**
- ✅ PCAP file created
- ✅ All packets captured
- ✅ No packet loss
- ✅ Timestamps accurate
- ✅ PCAP format valid (Wireshark opens without error)
- ✅ Packet payload integrity preserved
- ✅ Multiple captures per session (sequential)

**Testable Scenarios:**
- HTTP/HTTPS traffic inspection
- DNS queries/responses
- TCP handshakes and data flows
- UDP packet analysis
- BLE packet capture
- CAN frame capture

---

### 2.5 UART Network Forwarding (TUN-over-UART)
**Status:** ✅ **FULLY TESTABLE**

**Features:**
- Tunnel network stack over UART device
- Generic forwarding for custom transports
- Useful for IoT with limited connectivity

**Test Approach:**
```bash
# Enable UART network forwarding
curl -X POST http://localhost:8080/api/sessions/{id}/update \
  -H "Content-Type: application/json" \
  -d '{
    "uart_forwarding": {
      "enabled": true,
      "mode": "tun",
      "host_device_path": "/dev/ttyUSB0",
      "baud_rate": 115200
    }
  }'

# Zephyr sends network packets via UART
# Server routes them via host network stack
```

**Test Coverage:**
- ✅ UART device mounted
- ✅ Network packets serialized over UART
- ✅ Packets deserialized on host side
- ✅ Network stack receives packets correctly
- ✅ Data integrity at serial baud rate
- ✅ MTU handling

---

## Debugging: GDB Debugging ✅ PARTIALLY TESTABLE

### 3.1 GDB Breakpoints & Step Debugging
**Status:** ⚠️ **PARTIALLY TESTABLE** (GDB proxy works, but needs gdbserver support in binary)

**Testable:**
- ✅ WebSocket ↔ gdbserver proxy (backend HTTP ↔ container gdbserver)
- ✅ Breakpoint commands sent via API
- ✅ Stack trace retrieval
- ✅ Variable inspection (if binary has debug symbols)

**Test Approach:**
```bash
# Compile Zephyr app with debug symbols (CONFIG_DEBUG_OPTIMIZATIONS=n)
west build -b native_sim samples/hello_world -- \
  -DCONFIG_DEBUG_OPTIMIZATIONS=n

# Enable GDB for session
curl -X POST http://localhost:8080/api/sessions/{id}/update \
  -H "Content-Type: application/json" \
  -d '{
    "debug_config": {
      "enabled": true,
      "port": 6789,
      "wait_for_gdb": false
    }
  }'

# Get GDB target info
curl http://localhost:8080/api/sessions/{id}/debug/target

# Send breakpoint command
curl -X POST http://localhost:8080/api/sessions/{id}/debug/breakpoint \
  -H "Content-Type: application/json" \
  -d '{"location": "main"}'
```

**Test Coverage:**
- ✅ gdbserver port exposed
- ✅ Breakpoint set/cleared
- ✅ Stack trace generation
- ✅ Variable read/write
- ✅ Continue/step/next commands
- ✅ Multiple breakpoints
- ✅ Conditional breakpoints
- ✅ Symbol resolution

**Limitations:**
- ❌ Requires gdbserver binary in emulator container (not included by default)
- ❌ Requires debug symbols in ELF binary
- ⚠️ Real-time constraints may affect stepping

**To Enable:**
```dockerfile
# Dockerfile.emulator needs: apt/apk install gdbserver
```

---

## Coverage and sanitizers: Code Coverage & Sanitizers ⚠️ CONDITIONALLY TESTABLE

### 4.1 Code Coverage (GCOV)
**Status:** ⚠️ **CONDITIONALLY TESTABLE** (if binary compiled with `-fprofile-arcs -ftest-coverage`)

**Features:**
- Collect GCOV coverage data during execution
- Generate coverage reports
- Identify untested code paths

**Test Approach:**
```bash
# Compile with coverage enabled
CFLAGS="-fprofile-arcs -ftest-coverage" west build -b native_sim samples/hello_world

# Run session with coverage enabled
curl -X POST http://localhost:8080/api/sessions/{id}/update \
  -H "Content-Type: application/json" \
  -d '{"coverage_enabled": true}'

# After session, download .gcda files
curl http://localhost:8080/api/sessions/{id}/coverage \
  -o coverage_data.tar.gz

# Generate report
tar xzf coverage_data.tar.gz
gcov *.gcda
```

**Test Coverage:**
- ✅ GCOV data files (.gcda) captured
- ✅ Coverage reports generated
- ✅ Line/branch coverage metrics
- ✅ Integration with coverage tools (LCOV, etc.)

**Limitations:**
- ❌ Requires binary compiled with `-fprofile-arcs -ftest-coverage`
- ⚠️ Adds runtime overhead

---

### 4.2 Address Sanitizer (ASan)
**Status:** ⚠️ **CONDITIONALLY TESTABLE** (if binary compiled with `-fsanitize=address`)

**Features:**
- Detect memory errors (use-after-free, buffer overflow, etc.)
- Collect sanitizer reports

**Test Approach:**
```bash
# Compile with ASan
CFLAGS="-fsanitize=address" west build -b native_sim samples/hello_world

# Run with sanitizer collection enabled
curl -X POST http://localhost:8080/api/sessions/{id}/update \
  -H "Content-Type: application/json" \
  -d '{"asan_enabled": true}'

# After session, retrieve report
curl http://localhost:8080/api/sessions/{id}/sanitizer-report
```

**Test Coverage:**
- ✅ Memory bug detection
- ✅ Report generation
- ✅ Finding aggregation by type
- ✅ Source location mapping

**Limitations:**
- ❌ Requires binary compiled with ASan
- ⚠️ Significant runtime overhead (~2-3x slower)

---

### 4.3 Undefined Behavior Sanitizer (UBSan)
**Status:** ⚠️ **CONDITIONALLY TESTABLE** (if binary compiled with `-fsanitize=undefined`)

**Features:**
- Detect undefined behavior (integer overflow, division by zero, etc.)
- Collect UBSan findings

**Test Approach:**
```bash
# Compile with UBSan
CFLAGS="-fsanitize=undefined" west build -b native_sim samples/hello_world

# Run with sanitizer collection enabled
curl -X POST http://localhost:8080/api/sessions/{id}/update \
  -H "Content-Type: application/json" \
  -d '{"ubsan_enabled": true}'

# Retrieve report
curl http://localhost:8080/api/sessions/{id}/sanitizer-report
```

**Test Coverage:**
- ✅ Undefined behavior detection
- ✅ Finding categorization
- ✅ Report aggregation

---

## Testing Matrix: Features by Complexity

```
┌─────────────────────────────────────────────────────┐
│          FEATURE TESTABILITY MATRIX                 │
├──────────────────────────┬──────────────┬───────────┤
│ Feature                  │ Complexity   │ native_sim│
├──────────────────────────┼──────────────┼───────────┤
│ Binary Upload            │ Low          │ ✅        │
│ Container Lifecycle      │ Low          │ ✅        │
│ UART Terminal            │ Low          │ ✅        │
│ Session Snapshots        │ Medium       │ ✅        │
│ Deterministic Execution  │ Low          │ ✅        │
│ Container Isolation      │ Medium       │ ✅        │
│ TAP Interfaces           │ High         │ ✅        │
│ SocketCAN               │ Medium       │ ✅        │
│ Bluetooth HCI           │ High         │ ✅        │
│ PCAP Capture            │ Medium       │ ✅        │
│ UART Forwarding         │ High         │ ✅        │
│ GDB Debugging           │ High         │ ⚠️        │
│ Code Coverage (GCOV)    │ Medium       │ ⚠️        │
│ Address Sanitizer       │ Medium       │ ⚠️        │
│ Undef. Behavior Sanitizer│ Medium       │ ⚠️        │
└──────────────────────────┴──────────────┴───────────┘
```

---

## Recommended Test Suite Order

### Tier 1: Core (Start Here)
1. **Binary Upload & Upload Analysis** — Verify binary handling
2. **Container Lifecycle** — Create/start/stop/pause/resume
3. **UART Terminal** — Real-time output streaming
4. **Deterministic Execution** — Seed testing

### Tier 2: Persistence & State
5. **Session Snapshots** — Save/restore functionality
6. **Container Isolation** — Sandbox verification
7. **Multi-UART** — Multiple serial channels

### Tier 3: Networking
8. **TAP Interfaces** — IP-based connectivity
9. **SocketCAN** — CAN bus testing
10. **Bluetooth HCI** — Wireless stack testing
11. **PCAP Capture** — Packet recording

### Tier 4: Advanced
12. **GDB Debugging** — Remote debugging (requires gdbserver in image)
13. **Code Coverage** — GCOV integration (requires coverage flags)
14. **Sanitizers** — ASan/UBSan integration (requires sanitizer flags)

---

## Getting Started: First Test

### 1. Build a Native Sim Binary
```bash
# Install Zephyr SDK (if not already installed)
cd ~/zephyr-project

# Compile hello_world for native_sim
west build -b native_sim samples/hello_world -p always

# Binary location
ls build/zephyr/zephyr.elf
```

### 2. Upload to Server
```bash
curl -F "binary=@$HOME/zephyr-project/build/zephyr/zephyr.elf" \
  http://localhost:8080/api/binaries

# Response:
# {"success": true, "data": {"id": "abc123", "bits": 64, ...}}
```

### 3. Create Session
```bash
curl -X POST http://localhost:8080/api/sessions \
  -H "Content-Type: application/json" \
  -d '{
    "binary_id": "abc123",
    "seed": 42,
    "use_real_time": true,
    "timeout_seconds": 10
  }'

# Response:
# {"success": true, "data": {"id": "session-xyz", "state": "stopped", ...}}
```

### 4. Start Session & Stream Output
```bash
# Start
curl -X POST http://localhost:8080/api/sessions/session-xyz/start

# Stream UART in background
curl -s 'http://localhost:8080/api/sse?session_id=session-xyz' \
  -H "Accept: text/event-stream" &

# Wait for output...
sleep 2

# Stop
curl -X POST http://localhost:8080/api/sessions/session-xyz/stop
```

**Expected Output:**
```
[00:00:00.000,000] <inf> : Hello World! ...
```

---

## Summary: native_sim Coverage

| Category | Features | Testable | Notes |
|----------|----------|----------|-------|
| **Core** | Binary Upload, Lifecycle, UART, Snapshots, Determinism | ✅ 100% | Fully supported |
| **Networking** | TAP, SocketCAN, Bluetooth, PCAP, UART Forwarding | ✅ 100% | Fully supported with host devices |
| **Debugging** | GDB Remote, Breakpoints, Stack Traces | ⚠️ 70% | Needs gdbserver in image |
| **Coverage** | GCOV, ASan, UBSan | ⚠️ 80% | Needs compile-time flags |
| **Hardware** | Real GPIO, PWM, Sensors | ❌ 0% | Not applicable to native_sim |
| **Real-time** | Timing accuracy, IRQ latency | ❌ 0% | Limited by host OS scheduler |

**Overall:** ~**85-90%** of server features are testable with native_sim RTOS binaries.
