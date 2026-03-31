# Zephyr Native_sim Firmware Examples

Standalone Core platform native_sim applications for smokescreen testing of the Zephyr Simulator Server.

Each example is a minimal, self-contained Zephyr application compiled as an ELF binary for the `native_sim` board. These binaries are built on your host machine and tested with the remote emulator server.

## Prerequisites

- **Zephyr SDK** v0.16.x or latest ([install guide](https://docs.zephyrproject.org/latest/develop/getting_started/index.html))
- **Python 3.8+** with venv support
- **UV** (recommended) or **pip** for package management
- ~2GB free disk space
- Linux, macOS, or WSL (Windows)

## Quick Start

### 1. Set Up Python Environment

The Zephyr SDK includes a Python virtual environment. Activate it:

**Option A: Standard venv (SDK provides this)**
```bash
source ~/zephyr-project/venv/bin/activate
```

**Option B: UV (faster, recommended)**
```bash
# Install UV globally if needed
curl -LsSf https://astral.sh/uv/install.sh | sh

# Create venv with UV
uv venv ~/.zephyr-venv
source ~/.zephyr-venv/bin/activate

# Install west with UV
uv pip install west
```

### 2. Initialize West Workspace

```bash
cd firmware
west init .
west update
```

The `west init .` command initializes the workspace using the local `west.yml` manifest, and `west update` clones the Zephyr repository and downloads dependencies.

### 3. Build All Examples

```bash
./_scripts/build_all.sh
```

This compiles all 6 examples and copies binaries to `firmware/binaries/`.

Alternatively, build a single example:

```bash
cd examples/00-hello-world
make
```

### 4. Upload to Running Server

Ensure the Zephyr Simulator Server is running locally (http://localhost:8080):

```bash
./_scripts/upload_all.sh http://localhost:8080
```

This uploads all binaries and displays session creation commands for each.

## Examples Overview

| Example | Purpose | Tests |
|---------|---------|-------|
| **00-hello-world** | Basic output | Binary upload, container start, UART output streaming |
| **01-deterministic-output** | Reproducible execution | `--seed` flag, deterministic PRNG |
| **02-multi-uart** | Multiple channels | UART multiplexer handles multiple streams |
| **03-timer-intervals** | Timing accuracy | `--rt` flag impact, sleep precision |
| **04-state-machine** | State persistence | Pause/resume preserves application state |
| **05-shell-echo** | Bidirectional I/O | UART read/write, server responsiveness |

## Directory Structure

```
firmware/
├── west.yml                              # Shared manifest
├── README.md                             # This file
├── BUILD.md                              # Detailed build instructions
├── TESTING.md                            # Expected outputs, verification steps
├── .gitignore
├── _scripts/
│   ├── build_all.sh                      # Build all examples
│   └── upload_all.sh                     # Upload all binaries to server
├── examples/
│   ├── 00-hello-world/                   # Basic "Hello World" demo
│   │   ├── Makefile
│   │   ├── prj.conf
│   │   └── src/main.c
│   ├── 01-deterministic-output/          # Seeded PRNG demonstration
│   │   ├── Makefile
│   │   ├── prj.conf
│   │   └── src/main.c
│   ├── 02-multi-uart/                    # Multi-UART streaming
│   │   ├── Makefile
│   │   ├── prj.conf
│   │   └── src/main.c
│   ├── 03-timer-intervals/               # Sleep and timing tests
│   │   ├── Makefile
│   │   ├── prj.conf
│   │   └── src/main.c
│   ├── 04-state-machine/                 # FSM for state persistence testing
│   │   ├── Makefile
│   │   ├── prj.conf
│   │   └── src/main.c
│   └── 05-shell-echo/                    # UART echo server
│       ├── Makefile
│       ├── prj.conf
│       └── src/main.c
└── binaries/                             # (auto-created on build)
    ├── hello_world
    ├── deterministic_output
    ├── multi_uart
    ├── timer_intervals
    ├── state_machine
    └── shell_echo
```

## Building

### Build All Examples

```bash
firmware/_scripts/build_all.sh
```

This:
1. Initializes west workspace (if not already done)
2. Builds each example sequentially
3. Copies binaries to `firmware/binaries/`
4. Prints summary report

### Build Single Example

```bash
cd firmware/examples/00-hello-world
make build       # or just: make
make clean       # clean build artifacts
```

### Detailed Build Instructions

See [BUILD.md](BUILD.md) for troubleshooting and manual build steps.

## Testing

### Smoke Test (Quick)

Upload binaries and verify they start:

```bash
./_scripts/upload_all.sh http://localhost:8080
```

Create a session for each binary and verify output.

### Detailed Testing

See [TESTING.md](TESTING.md) for:
- Expected outputs for each example
- Verification steps
- Integration with server
- Session creation examples

## Troubleshooting

### Error: "west not found"

Ensure Zephyr SDK is sourced:

```bash
source ~/zephyr-project/venv/bin/activate
```

Or install SDK: https://docs.zephyrproject.org/latest/develop/getting_started/index.html

### Error: "zephyr: unknown board" during build

Run west update:

```bash
cd firmware
west update
```

### Build fails with missing dependencies

Clean and rebuild:

```bash
cd firmware/examples/XXX
make clean
make
```

### Binaries not created

Check west is initialized:

```bash
cd firmware
ls -la .west/
```

If missing, run: `west init -m west.yml .`

## Integration with Server

Each binary can be uploaded to the Zephyr Simulator Server for remote execution.

### Manual Upload

```bash
curl -F "binary=@firmware/binaries/hello_world" \
  http://localhost:8080/api/binaries
```

### Batch Upload

All binaries:

```bash
firmware/_scripts/upload_all.sh http://localhost:8080
```

## Examples are ~50-100 LOC

Each example is intentionally simple:
- Minimal dependencies (only kernel + stdio)
- Single-purpose demonstrations
- Easy to modify and extend
- Suitable for testing server features

## Next Steps

1. [BUILD.md](BUILD.md) — Step-by-step build guide
2. [TESTING.md](TESTING.md) — How to test each example
3. [../NATIVE_SIM_TESTABLE_FEATURES.md](../NATIVE_SIM_TESTABLE_FEATURES.md) — Full feature testing guide
