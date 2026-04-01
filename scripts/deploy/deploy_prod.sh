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

BACKEND_IMAGE_REPO="${BACKEND_IMAGE_REPO:-$(get_env_value "BACKEND_IMAGE_REPO" "ghcr.io/beriberikix/zephyr-simulator-server-backend")}"  # env can override for rollback pinning
BACKEND_IMAGE_TAG="${BACKEND_IMAGE_TAG:-$(get_env_value "BACKEND_IMAGE_TAG" "latest")}"  # env can override for rollback pinning
BACKEND_IMAGE_REF="${BACKEND_IMAGE_REPO}:${BACKEND_IMAGE_TAG}"

COMPOSE_CMD=(docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.production)

current_commit="$(git rev-parse HEAD)"
echo "$current_commit" > "$DEPLOY_STATE_DIR/current_commit"

echo "$BACKEND_IMAGE_REF" > "$DEPLOY_STATE_DIR/current_backend_image"

echo "Building local emulator image..."
docker build -f "$ROOT_DIR/Dockerfile.emulator" -t zephyr-emulator:latest "$ROOT_DIR"

echo "Pulling backend image: $BACKEND_IMAGE_REF"
docker pull "$BACKEND_IMAGE_REF"

"${COMPOSE_CMD[@]}" up -d --no-build --remove-orphans

# Health checks through the public reverse proxy on the droplet.
for i in {1..20}; do
  if curl -fsS "http://127.0.0.1/health" >/dev/null && curl -fsS "http://127.0.0.1/api/health" >/dev/null; then
    echo "Deployment health checks passed."
    echo "$current_commit" > "$DEPLOY_STATE_DIR/known_good_commit"
    echo "$BACKEND_IMAGE_REF" > "$DEPLOY_STATE_DIR/known_good_backend_image"
    exit 0
  fi
  sleep 3
done

echo "Deployment completed but health checks failed."
exit 1
