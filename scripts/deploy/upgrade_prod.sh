#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

DEPLOY_STATE_DIR="$ROOT_DIR/.deploy"
mkdir -p "$DEPLOY_STATE_DIR"

if [[ ! -f ".env.production" ]]; then
	echo "Missing .env.production. Copy .env.production.example and edit values first."
	exit 1
fi

get_env_value() {
	local key="$1"
	local default_value="$2"
	local value

	value="$(grep -E "^${key}=" .env.production | tail -n 1 | cut -d '=' -f 2- || true)"
	if [[ -z "$value" ]]; then
		value="$default_value"
	fi

	echo "$value"
}

if ! git symbolic-ref --quiet --short HEAD >/dev/null; then
	echo "Detached HEAD detected. Switching to main before upgrade."
	git checkout main
fi

git checkout main

current_commit="$(git rev-parse HEAD)"
current_backend_image_ref="$(get_env_value "BACKEND_IMAGE_REPO" "ghcr.io/beriberikix/zephyr-simulator-server-backend"):$(get_env_value "BACKEND_IMAGE_TAG" "latest")"
echo "$current_commit" > "$DEPLOY_STATE_DIR/previous_commit"
echo "$current_backend_image_ref" > "$DEPLOY_STATE_DIR/previous_backend_image"

git fetch origin main
git pull --ff-only origin main

new_commit="$(git rev-parse HEAD)"
new_backend_image_ref="$(get_env_value "BACKEND_IMAGE_REPO" "ghcr.io/beriberikix/zephyr-simulator-server-backend"):$(get_env_value "BACKEND_IMAGE_TAG" "latest")"
echo "$new_commit" > "$DEPLOY_STATE_DIR/current_commit"
echo "$new_backend_image_ref" > "$DEPLOY_STATE_DIR/current_backend_image"

"$ROOT_DIR/scripts/deploy/deploy_prod.sh"

echo "Upgrade succeeded."
echo "Previous commit: $current_commit"
echo "Current commit:  $new_commit"
echo "Previous backend image: $current_backend_image_ref"
echo "Current backend image:  $new_backend_image_ref"
