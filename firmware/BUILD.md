# Build Instructions

## Prerequisites

Before building, ensure your Python environment is activated:

**With SDK venv:**
```bash
source ~/zephyr-project/venv/bin/activate
```

**With UV (recommended):**
```bash
uv venv ~/.zephyr-venv
source ~/.zephyr-venv/bin/activate
uv pip install west
```

Then verify west is available:
```bash
west --version
```

## Quick Build

First-time setup:
```bash
cd firmware
west init .
west update
```

Then build:
```bash
./_scripts/build_all.sh
```

This builds all 6 examples and outputs binaries to `firmware/binaries/`.

## Building Individual Examples

Each example is a standalone Zephyr application:

```bash
cd firmware/examples/00-hello-world
make                 # Build
make clean           # Clean build artifacts
make build           # Explicit build target
```

## Manual Build with West

If not using the Makefile:

```bash
cd firmware/examples/00-hello-world
west build -b native_sim . -p auto
```

Binary output (native_sim runnable): `build/zephyr/zephyr.exe`

## Build Process

1. **First time only:** `west update` to clone Zephyr repository
2. **Each example:** `west build -b native_sim . -p auto`
3. **Output:** `build/zephyr/zephyr.exe` per example (script also supports fallback artifacts)
4. **Automation:** `build_all.sh` copies to `firmware/binaries/` with friendly names

## Expected Build Artifacts

```
firmware/binaries/
├── hello_world                  (from 00-hello-world)
├── deterministic_output         (from 01-deterministic-output)
├── multi_uart                   (from 02-multi-uart)
├── timer_intervals              (from 03-timer-intervals)
├── state_machine                (from 04-state-machine)
└── shell_echo                   (from 05-shell-echo)
```

All are 64-bit native_sim ELF binaries.

## Verifying Binaries

After build:

```bash
file firmware/binaries/*
# Output: ... ELF 64-bit ... x86-64 ...

readelf -h firmware/binaries/* | grep Machine
# Output: Machine: Advanced Micro Devices X86-64
```

## Troubleshooting

### "west not found"

**Symptom:** Command not found error

**Solution:** Activate your Python environment

**Option 1: SDK venv**
```bash
source ~/zephyr-project/venv/bin/activate
west --version
```

**Option 2: UV (recommended)**
```bash
source ~/.zephyr-venv/bin/activate
west --version
```

If west is not installed, install it:
```bash
uv pip install west
```

### "cmake not found"

**Symptom:** Error during west build

**Solution:** Install Zephyr SDK

```bash
cd ~
wget https://github.com/zephyrproject-rtos/sdk-ng/releases/download/v0.16.5/zephyr-sdk-0.16.5_linux-x86_64_minimal.tar.xz
tar xf zephyr-sdk-0.16.5_linux-x86_64_minimal.tar.xz
cd zephyr-sdk-0.16.5
./setup.sh
```

### "zephyr: unknown board"

**Symptom:** Build fails with "board native_sim not found"

**Solution:** Ensure west is initialized and updated

```bash
cd firmware
west init .   # If not already initialized
west update
```

### Build is very slow

**Symptom:** First build takes 5+ minutes

**Reason:** Zephyr repository is large (~2GB)

**Solution:** This is normal. Subsequent builds are faster (incremental).

To speed up, use `-p auto` (already in Makefile):

```bash
west build -b native_sim . -p auto
```

### "Permission denied" on .west/

**Symptom:** Cannot write to .west directory

**Solution:** Check ownership

```bash
ls -la firmware/.west/
sudo chown -R $USER:$USER firmware/
```

### Build passes but no binary in binaries/

**Symptom:** `build_all.sh` runs but `firmware/binaries/` is empty

**Solution:** Check build succeeded

```bash
cd firmware/examples/00-hello-world
ls -la build/zephyr/zephyr.exe
```

If missing, run `make` in that example directory.

## Build Customization

### Custom Kconfig Options

Edit `prj.conf` in any example:

```conf
CONFIG_PRINTK=y
CONFIG_SERIAL=y
CONFIG_CONSOLE=y
CONFIG_MY_CUSTOM_OPTION=y  # Add custom options here
```

Then rebuild:

```bash
cd firmware/examples/00-hello-world
make clean
make
```

### Debug Build

Add to `prj.conf`:

```conf
CONFIG_DEBUG_OPTIMIZATIONS=n    # Disable optimizations
```

This includes debug symbols but increases binary size.

### Verbose Build Output

```bash
cd firmware/examples/00-hello-world
west build -b native_sim . -p auto -- -DCMAKE_VERBOSE_MAKEFILE=ON
```

## Parallel Builds

`build_all.sh` builds examples sequentially. To parallelize manually:

```bash
cd firmware/examples/00-hello-world && make &
cd firmware/examples/01-deterministic-output && make &
# ... etc
wait
```

But sequential is recommended for clearer output and troubleshooting.

## Cleaning Up

### Remove build artifacts (keep source)

```bash
cd firmware
for dir in examples/*/; do
  (cd "$dir" && make clean)
done
```

### Remove all (including west workspace)

```bash
cd firmware
rm -rf .west zephyr build/ examples/*/build binaries/
```

Then reinitialize:

```bash
west update
./_scripts/build_all.sh
```

## Next Steps

1. Build all examples with `firmware/_scripts/build_all.sh`
2. Upload to server with `firmware/_scripts/upload_all.sh http://localhost:8080`
3. See [TESTING.md](TESTING.md) for verification steps
