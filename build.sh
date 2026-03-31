#!/bin/bash
# Build script for Zephyr Emulator Server

set -e

echo "Building zephyr-emulator base image..."
docker build -f Dockerfile.emulator -t zephyr-emulator:latest .

echo "Building backend container..."
docker build -f Dockerfile -t zephyr-backend:latest .

echo "✅ All images built successfully!"
echo ""
echo "Next: Start services with:"
echo "  docker compose up -d"
