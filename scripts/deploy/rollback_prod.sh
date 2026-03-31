#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

DEPLOY_STATE_DIR="$ROOT_DIR/.deploy"

if [[ ! -f "$DEPLOY_STATE_DIR/previous_commit" ]]; then
  echo "No previous commit found at .deploy/previous_commit. Cannot rollback."
  exit 1
fi

target_commit="$(cat "$DEPLOY_STATE_DIR/previous_commit")"
current_commit="$(git rev-parse HEAD)"

if [[ -z "$target_commit" ]]; then
  echo "Previous commit record is empty. Cannot rollback."
  exit 1
fi

git fetch origin main
git checkout "$target_commit"

echo "$current_commit" > "$DEPLOY_STATE_DIR/rollback_from_commit"

"$ROOT_DIR/scripts/deploy/deploy_prod.sh"

echo "Rollback succeeded to commit: $target_commit"
