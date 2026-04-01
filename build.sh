#!/bin/bash
# Build script for Zephyr Emulator Server

set -e

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [[ ! -f "$ROOT_DIR/cmd/server/main.go" ]]; then
	echo "Error: expected backend entrypoint at cmd/server/main.go"
	echo "Current root: $ROOT_DIR"
	exit 1
fi

echo "Building zephyr-emulator base image..."
docker build -f "$ROOT_DIR/Dockerfile.emulator" -t zephyr-emulator:latest "$ROOT_DIR"

echo "Building backend container..."
docker build -f "$ROOT_DIR/Dockerfile" -t zephyr-backend:latest "$ROOT_DIR"

echo "✅ All images built successfully!"
echo ""
echo "Next: Start services with:"
echo "  docker compose up -d"
