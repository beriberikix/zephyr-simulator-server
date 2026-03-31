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

COMPOSE_CMD=(docker compose -f docker-compose.yml -f docker-compose.prod.yml --env-file .env.production)

current_commit="$(git rev-parse HEAD)"
echo "$current_commit" > "$DEPLOY_STATE_DIR/current_commit"

./build.sh
"${COMPOSE_CMD[@]}" up -d --build --remove-orphans

# Health checks through the public reverse proxy on the droplet.
for i in {1..20}; do
  if curl -fsS "http://127.0.0.1/health" >/dev/null && curl -fsS "http://127.0.0.1/api/health" >/dev/null; then
    echo "Deployment health checks passed."
    echo "$current_commit" > "$DEPLOY_STATE_DIR/known_good_commit"
    exit 0
  fi
  sleep 3
done

echo "Deployment completed but health checks failed."
exit 1
