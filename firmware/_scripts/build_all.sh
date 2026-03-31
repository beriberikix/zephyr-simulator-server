#!/bin/bash
set -e

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
FIRMWARE_DIR="$( dirname "$SCRIPT_DIR" )"
EXAMPLES_DIR="$FIRMWARE_DIR/examples"
BINARIES_DIR="$FIRMWARE_DIR/binaries"

# Initialize binaries directory
mkdir -p "$BINARIES_DIR"

# List of examples
EXAMPLES=(
    "00-hello-world"
    "01-deterministic-output"
    "02-multi-uart"
    "03-timer-intervals"
    "04-state-machine"
    "05-shell-echo"
)

echo "=== Zephyr Native_sim Examples Build ==="
echo

# Prefer project-local west from .venv when available.
if [ -x "$FIRMWARE_DIR/.venv/bin/west" ]; then
    export PATH="$FIRMWARE_DIR/.venv/bin:$PATH"
fi

if ! command -v west >/dev/null 2>&1; then
    echo "Error: west not found."
    echo "Hint: use uv (recommended) or activate your Zephyr venv, then retry."
    echo "  uv venv .venv"
    echo "  uv pip install west"
    exit 1
fi

# Initialize west workspace if not already done
if [ ! -d "$FIRMWARE_DIR/.west" ]; then
    echo "Initializing west workspace..."
    cd "$FIRMWARE_DIR"
    west init -l .
    west update
fi

cd "$FIRMWARE_DIR"

# Always point west to Zephyr's manifest once Zephyr is present.
# This avoids self-project recursion in Kconfig module discovery.
if [ -f "$FIRMWARE_DIR/zephyr/west.yml" ]; then
    west config manifest.path zephyr
    west config manifest.file west.yml
elif ! west list >/dev/null 2>&1; then
    # Heal common workspace drift where manifest.path points to a stale folder (e.g. "foo").
    echo "Repairing west manifest configuration..."
    west config manifest.path .
    west config manifest.file west.yml
fi

# Ensure Zephyr extension commands are available (west build, west flash, ...).
if ! west help build >/dev/null 2>&1; then
    echo "Refreshing west projects so extension commands are registered..."
    west update
fi

BUILT=0
FAILED=0

for example in "${EXAMPLES[@]}"; do
    echo "Building $example..."
    cd "$EXAMPLES_DIR/$example"
    
    if make clean && make; then
        # Extract binary name from directory
        BINARY_NAME=$(echo "$example" | sed 's/^[0-9]*-//')

        # For native_sim, prefer the runnable executable artifact.
        ARTIFACT=""
        for candidate in "build/zephyr/zephyr.exe" "build/zephyr/zephyr"; do
            if [ -f "$candidate" ]; then
                ARTIFACT="$candidate"
                break
            fi
        done

        if [ -z "$ARTIFACT" ] && [ -f "build/zephyr/zephyr.elf" ]; then
            ARTIFACT="build/zephyr/zephyr.elf"
            echo "Warning: runnable native_sim artifact not found, falling back to zephyr.elf"
        fi

        if [ -z "$ARTIFACT" ]; then
            echo "✗ $example failed"
            echo "  Missing build artifact: expected one of"
            echo "    - build/zephyr/zephyr.exe"
            echo "    - build/zephyr/zephyr"
            echo "    - build/zephyr/zephyr.elf"
            FAILED=$((FAILED + 1))
            echo
            continue
        fi

        cp "$ARTIFACT" "$BINARIES_DIR/$BINARY_NAME"
        echo "✓ $example -> $BINARIES_DIR/$BINARY_NAME"
        BUILT=$((BUILT + 1))
    else
        echo "✗ $example failed"
        FAILED=$((FAILED + 1))
    fi
    echo
done

echo "=== Build Summary ==="
echo "Built: $BUILT"
echo "Failed: $FAILED"
echo
echo "Binaries location: $BINARIES_DIR"
ls -lh "$BINARIES_DIR"/*
