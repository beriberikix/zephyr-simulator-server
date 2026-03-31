#!/bin/bash
set -e

if [ -z "$1" ]; then
    echo "Usage: $0 <server_url>"
    echo "Example: $0 http://localhost:8080"
    exit 1
fi

SERVER_URL="$1"
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
FIRMWARE_DIR="$( dirname "$SCRIPT_DIR" )"
BINARIES_DIR="$FIRMWARE_DIR/binaries"

echo "=== Uploading Zephyr Native_sim Examples ==="
echo "Server: $SERVER_URL"
echo

if [ ! -d "$BINARIES_DIR" ] || [ -z "$(ls -A "$BINARIES_DIR")" ]; then
    echo "Error: No binaries found in $BINARIES_DIR"
    echo "Run: firmware/_scripts/build_all.sh"
    exit 1
fi

UPLOADED=0
FAILED=0

for binary in "$BINARIES_DIR"/*; do
    if [ ! -f "$binary" ]; then
        continue
    fi
    
    basename=$(basename "$binary")
    echo "Uploading $basename..."
    
    response=$(curl -s -F "binary=@$binary" "$SERVER_URL/api/binaries")
    
    if echo "$response" | grep -q '"success":true'; then
        binary_id=$(echo "$response" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
        echo "✓ $basename (ID: $binary_id)"
        echo "  Create session: curl -X POST $SERVER_URL/api/sessions -H 'Content-Type: application/json' -d '{\"binary_id\": \"$binary_id\", \"seed\": 42}'"
        UPLOADED=$((UPLOADED + 1))
    else
        echo "✗ $basename failed"
        echo "  Response: $response"
        FAILED=$((FAILED + 1))
    fi
    echo
done

echo "=== Upload Summary ==="
echo "Uploaded: $UPLOADED"
echo "Failed: $FAILED"
