# Testing Guide

## Overview

This document describes expected outputs for each example and how to verify they work correctly with the Zephyr Simulator Server.

## Smoke Test Checklist

### Before Testing

1. ✅ Build all examples: `firmware/_scripts/build_all.sh`
2. ✅ Server running: `docker compose up` (in root directory)
3. ✅ Upload binaries: `firmware/_scripts/upload_all.sh http://localhost:8080`

### Quick Verification

For each binary, create a session and verify output appears.

## Per-Example Testing

### 1. hello_world

**Purpose:** Verify basic binary upload and UART output streaming

**Build:**
```bash
cd firmware/examples/00-hello-world
make
```

**Create Session:**
```bash
curl -X POST http://localhost:8080/api/sessions \
  -H "Content-Type: application/json" \
  -d '{"binary_id": "<binary_id>", "seed": 0, "use_real_time": false, "timeout_seconds": 5}'
```

**Start Session:**
```bash
curl -X POST http://localhost:8080/api/sessions/<session_id>/start
```

**Stream Output (in another terminal):**
```bash
curl -s 'http://localhost:8080/api/sse?session_id=<session_id>' \
  -H "Accept: text/event-stream"
```

**Expected Output:**
```
Hello World!
```

**Verification:**
- ✅ Output appears within 1 second
- ✅ No corruption or extra characters
- ✅ Session stops cleanly after output
- ✅ Container exits successfully

---

### 2. deterministic_output

**Purpose:** Verify reproducible execution with `--seed` flag

**Build:**
```bash
cd firmware/examples/01-deterministic-output
make
```

**Test Reproducibility:**

Create TWO sessions with the same seed:

**Session 1:**
```bash
curl -X POST http://localhost:8080/api/sessions \
  -H "Content-Type: application/json" \
  -d '{"binary_id": "<binary_id>", "seed": 42, "use_real_time": false, "timeout_seconds": 10}'

curl -X POST http://localhost:8080/api/sessions/<session_id_1>/start

# Stream and save output
curl -s 'http://localhost:8080/api/sse?session_id=<session_id_1>' \
  -H "Accept: text/event-stream" > output1.txt
```

**Session 2:**
```bash
curl -X POST http://localhost:8080/api/sessions \
  -H "Content-Type: application/json" \
  -d '{"binary_id": "<binary_id>", "seed": 42, "use_real_time": false, "timeout_seconds": 10}'

curl -X POST http://localhost:8080/api/sessions/<session_id_2>/start

# Stream and save output
curl -s 'http://localhost:8080/api/sse?session_id=<session_id_2>' \
  -H "Accept: text/event-stream" > output2.txt
```

**Expected Output (both identical):**
```
Random value 0: 12657
Random value 1: 66273
Random value 2: 8231
Random value 3: 45167
Random value 4: 92847
Random value 5: 31462
Random value 6: 78234
Random value 7: 56891
Random value 8: 23456
Random value 9: 89234
```

**Verification:**
- ✅ `diff output1.txt output2.txt` returns empty (no differences)
- ✅ Same seed always produces same sequence
- ✅ Different seed produces different sequence (test with seed=99)

---

### 3. multi_uart

**Purpose:** Verify multi-channel UART multiplexing

**Build:**
```bash
cd firmware/examples/02-multi-uart
make
```

**Create Session:**
```bash
curl -X POST http://localhost:8080/api/sessions \
  -H "Content-Type: application/json" \
  -d '{"binary_id": "<binary_id>", "seed": 0, "use_real_time": false, "timeout_seconds": 10}'

curl -X POST http://localhost:8080/api/sessions/<session_id>/start

curl -s 'http://localhost:8080/api/sse?session_id=<session_id>' \
  -H "Accept: text/event-stream"
```

**Expected Output:**
```
[UART0] Message 0 from UART0
[UART1] Message 0 from UART1
[UART0] Message 1 from UART0
[UART1] Message 1 from UART1
[UART0] Message 2 from UART0
[UART1] Message 2 from UART1
[UART0] Message 3 from UART0
[UART1] Message 3 from UART1
[UART0] Message 4 from UART0
[UART1] Message 4 from UART1
```

**Verification:**
- ✅ Output from both UART0 and UART1 appear
- ✅ Messages are interleaved (not garbled)
- ✅ Each message appears in correct order
- ✅ No missing or duplicated lines
- ✅ Data integrity: bytes match exactly

**Advanced Check:**
Capture and analyze UART streams separately (if server implements per-UART stream endpoints):
- UART0: 5 messages
- UART1: 5 messages

---

### 4. timer_intervals

**Purpose:** Verify timing accuracy and `--rt` flag impact

**Build:**
```bash
cd firmware/examples/03-timer-intervals
make
```

**Test WITHOUT Real-Time Mode (deterministic):**
```bash
curl -X POST http://localhost:8080/api/sessions \
  -H "Content-Type: application/json" \
  -d '{"binary_id": "<binary_id>", "seed": 0, "use_real_time": false, "timeout_seconds": 10}'

curl -X POST http://localhost:8080/api/sessions/<session_id>/start

# Capture with timing
time curl -s 'http://localhost:8080/api/sse?session_id=<session_id>' \
  -H "Accept: text/event-stream"
```

**Expected Output (deterministic timing):**
```
Sleep test starting...
[0.000 s] Iteration 1
[1.000 s] Iteration 2
[2.000 s] Iteration 3
[3.000 s] Iteration 4
[4.000 s] Iteration 5
Sleep test complete.
```

**Test WITH Real-Time Mode (wall-clock time):**
```bash
curl -X POST http://localhost:8080/api/sessions \
  -H "Content-Type: application/json" \
  -d '{"binary_id": "<binary_id>", "seed": 0, "use_real_time": true, "timeout_seconds": 10}'

curl -X POST http://localhost:8080/api/sessions/<session_id>/start

time curl -s 'http://localhost:8080/api/sse?session_id=<session_id>' \
  -H "Accept: text/event-stream"
```

**Expected Output (wall-clock timing):**
```
Sleep test starting...
[0.001 s] Iteration 1
[1.015 s] Iteration 2
[2.008 s] Iteration 3
[3.022 s] Iteration 4
[4.005 s] Iteration 5
Sleep test complete.
```

(Timing jitter ±50ms is expected; values should be ~1 second apart)

**Verification:**
- ✅ Non-RT: Timestamps are exactly 1.000s apart
- ✅ RT: Timestamps are ~1 second apart (wall-clock)
- ✅ Without `--rt`: Session completes in 1-2 real seconds
- ✅ With `--rt`: Session takes ~6-7 real seconds (5 sleeps + overhead)
- ✅ Output always completes (no timeout)

---

### 5. state_machine

**Purpose:** Verify state persistence across pause/resume

**Build:**
```bash
cd firmware/examples/04-state-machine
make
```

**Create Session:**
```bash
curl -X POST http://localhost:8080/api/sessions \
  -H "Content-Type: application/json" \
  -d '{"binary_id": "<binary_id>", "seed": 0, "use_real_time": false, "timeout_seconds": 30}'

curl -X POST http://localhost:8080/api/sessions/<session_id>/start
```

**Scenario 1: Let it run to completion**

```bash
curl -s 'http://localhost:8080/api/sse?session_id=<session_id>' \
  -H "Accept: text/event-stream"
```

**Expected Output:**
```
State: INIT
State: READY
State: RUNNING
State: CLEANUP
State: DONE
State: INIT    # Loop restarts
State: READY
State: RUNNING
...
```

**Scenario 2: Pause and Resume**

```bash
# Start session
curl -X POST http://localhost:8080/api/sessions/<session_id>/start

# Let it run for ~2 seconds, then pause
sleep 2
curl -X POST http://localhost:8080/api/sessions/<session_id>/pause

# Resume
sleep 2
curl -X POST http://localhost:8080/api/sessions/<session_id>/resume

# Stream remainder
curl -s 'http://localhost:8080/api/sse?session_id=<session_id>' \
  -H "Accept: text/event-stream"
```

**Expected Behavior:**
- ✅ Pause stops output
- ✅ Resume continues from exact same state
- ✅ No state corruption or skipped states
- ✅ Output log shows states before pause, then resumes correctly

**Verification:**
- ✅ State sequence is always: INIT → READY → RUNNING → CLEANUP → DONE → (repeat)
- ✅ Pause/resume doesn't lose or corrupt state
- ✅ Output is deterministic across runs

---

### 6. shell_echo

**Purpose:** Verify bidirectional UART I/O

**Build:**
```bash
cd firmware/examples/05-shell-echo
make
```

**Create Session:**
```bash
curl -X POST http://localhost:8080/api/sessions \
  -H "Content-Type: application/json" \
  -d '{"binary_id": "<binary_id>", "seed": 0, "use_real_time": false, "timeout_seconds": 30}'

curl -X POST http://localhost:8080/api/sessions/<session_id>/start
```

**Stream Initial Output:**
```bash
curl -s 'http://localhost:8080/api/sse?session_id=<session_id>' \
  -H "Accept: text/event-stream"
```

**Expected Initial Output:**
```
echo>
```

**Send Input to UART:**

(This requires a UART input endpoint on the server; verify it exists first)

```bash
curl -X POST http://localhost:8080/api/sessions/<session_id>/uart/input \
  -H "Content-Type: application/json" \
  -d '{"uart_idx": 0, "data": "hello\n"}'
```

**Expected Output:**
```
echo> hello
echo>
```

**Verification:**
- ✅ Initial prompt `"echo>"` appears
- ✅ Input `"hello"` is echoed back
- ✅ New prompt appears after input
- ✅ Multiple echo cycles work
- ✅ No data corruption

**Advanced Test (if input endpoint supported):**
```bash
# Send multiple messages
curl -X POST http://localhost:8080/api/sessions/<session_id>/uart/input \
  -d '{"uart_idx": 0, "data": "test1\n"}'

curl -X POST http://localhost:8080/api/sessions/<session_id>/uart/input \
  -d '{"uart_idx": 0, "data": "test2\n"}'

curl -X POST http://localhost:8080/api/sessions/<session_id>/uart/input \
  -d '{"uart_idx": 0, "data": "quit\n"}'
```

**Expected Output:**
```
echo> test1
echo> test2
echo> quit
echo>
```

---

## Integration Testing Workflow

### 1. Build Binaries
```bash
cd firmware
./_scripts/build_all.sh
# Verify all binaries created in firmware/binaries/
```

### 2. Upload Binaries
```bash
firmware/_scripts/upload_all.sh http://localhost:8080
# Verify all uploads succeed; note binary_ids
```

### 3. Test Each Example
For each binary, create a session and verify outputs match expectations above.

### 4. Batch Test Script

Create `firmware/test_all.sh` for automated testing:
```bash
#!/bin/bash
# (Pseudocode; adapt to your needs)
for binary in firmware/binaries/*; do
  id=$(basename "$binary")
  response=$(curl -s -F "binary=@$binary" http://localhost:8080/api/binaries)
  binary_id=$(echo "$response" | jq -r '.data.id')

  # Create session
  session=$(curl -s -X POST http://localhost:8080/api/sessions \
    -H "Content-Type: application/json" \
    -d "{\"binary_id\": \"$binary_id\", \"seed\": 0}")
  session_id=$(echo "$session" | jq -r '.data.id')

  # Start and stream
  curl -X POST http://localhost:8080/api/sessions/$session_id/start
  curl -s "http://localhost:8080/api/sse?session_id=$session_id" \
    -H "Accept: text/event-stream" | head -20
done
```

## Troubleshooting Test Failures

### No Output Received
- Check server is running: `docker compose ps`
- Check binary_id is correct: `curl http://localhost:8080/api/binaries`
- Check session is started: `curl http://localhost:8080/api/sessions/<id>`

### Output Corrupted or Garbled
- Check binary built successfully: `file firmware/binaries/<name>`
- Verify UART configuration: check `prj.conf` for example
- Check container logs: `docker logs <container_id>`

### Timing Tests Fail
- Ensure `--rt` flag is passed correctly in session config
- Test without real-time first: `"use_real_time": false`
- Check system clock: `date`

### State Machine Test Fails
- Verify container supports pause/resume
- Check pause succeeds: HTTP response code should be 200
- Verify state is persisted in volumes

## Performance Benchmarks (Expected)

| Example | Binary Size | Build Time | Runtime |
|---------|-------------|-----------|---------|
| hello_world | ~1MB | 20-30s | <1s |
| deterministic_output | ~1MB | 5-10s | ~1s |
| multi_uart | ~1MB | 5-10s | ~1s |
| timer_intervals | ~1MB | 5-10s | ~6-7s (RT) or <2s (deterministic) |
| state_machine | ~1MB | 5-10s | ~3-5s |
| shell_echo | ~1MB | 5-10s | ~2-5s |

(Timings vary based on system performance)

## Next Steps

1. Run through each example's test case
2. Document any failures or unexpected behavior
3. See [../NATIVE_SIM_TESTABLE_FEATURES.md](../NATIVE_SIM_TESTABLE_FEATURES.md) for comprehensive feature testing
