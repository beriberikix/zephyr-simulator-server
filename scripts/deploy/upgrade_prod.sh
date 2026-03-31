#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

DEPLOY_STATE_DIR="$ROOT_DIR/.deploy"
mkdir -p "$DEPLOY_STATE_DIR"

if ! git symbolic-ref --quiet --short HEAD >/dev/null; then
	echo "Detached HEAD detected. Switching to main before upgrade."
	git checkout main
fi

git checkout main

current_commit="$(git rev-parse HEAD)"
echo "$current_commit" > "$DEPLOY_STATE_DIR/previous_commit"

git fetch origin main
git pull --ff-only origin main

new_commit="$(git rev-parse HEAD)"
echo "$new_commit" > "$DEPLOY_STATE_DIR/current_commit"

"$ROOT_DIR/scripts/deploy/deploy_prod.sh"

echo "Upgrade succeeded."
echo "Previous commit: $current_commit"
echo "Current commit:  $new_commit"
