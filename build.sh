#!/bin/bash
# Build script for Zephyr Emulator Server

set -e

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

BACKEND_MAIN_TARGET="./cmd/server/main.go"
if [[ ! -f "$ROOT_DIR/cmd/server/main.go" ]]; then
	found_main="$(find "$ROOT_DIR/cmd" -type f -name main.go 2>/dev/null | head -n 1 || true)"
	if [[ -n "$found_main" ]]; then
		BACKEND_MAIN_TARGET=".${found_main#"$ROOT_DIR"}"
	else
		echo "Error: could not find a backend entrypoint under ./cmd/**/main.go"
		echo "Current root: $ROOT_DIR"
		exit 1
	fi
fi

echo "Building zephyr-emulator base image..."
docker build -f "$ROOT_DIR/Dockerfile.emulator" -t zephyr-emulator:latest "$ROOT_DIR"

echo "Building backend container..."
docker build \
	--build-arg BACKEND_MAIN_TARGET="$BACKEND_MAIN_TARGET" \
	-f "$ROOT_DIR/Dockerfile" \
	-t zephyr-backend:latest \
	"$ROOT_DIR"

echo "✅ All images built successfully!"
echo ""
echo "Next: Start services with:"
echo "  docker compose up -d"
