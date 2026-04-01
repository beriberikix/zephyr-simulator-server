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
target_backend_image_ref=""

if [[ -z "$target_commit" ]]; then
  echo "Previous commit record is empty. Cannot rollback."
  exit 1
fi

git fetch origin main
git checkout "$target_commit"

echo "$current_commit" > "$DEPLOY_STATE_DIR/rollback_from_commit"

if [[ -f "$DEPLOY_STATE_DIR/previous_backend_image" ]]; then
  target_backend_image_ref="$(cat "$DEPLOY_STATE_DIR/previous_backend_image")"
  BACKEND_IMAGE_REPO="${target_backend_image_ref%:*}"
  BACKEND_IMAGE_TAG="${target_backend_image_ref##*:}"
  export BACKEND_IMAGE_REPO BACKEND_IMAGE_TAG
  echo "Using rollback backend image: $target_backend_image_ref"
fi

"$ROOT_DIR/scripts/deploy/deploy_prod.sh"

echo "Rollback succeeded to commit: $target_commit"
if [[ -n "$target_backend_image_ref" ]]; then
  echo "Rollback backend image: $target_backend_image_ref"
fi
