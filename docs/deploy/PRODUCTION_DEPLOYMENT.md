# Production Deployment (DigitalOcean + Cloudflare + Caddy)

This guide deploys zephyr-simulator-server on one droplet and supports in-place source-code upgrades with rollback.

## 1. Prepare the droplet

Install prerequisites:

```bash
sudo apt-get update
sudo apt-get install -y ca-certificates curl git
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker "$USER"
newgrp docker
```

Open ports 22, 80, 443 in both:
- DigitalOcean networking firewall
- Host firewall (ufw or equivalent)

## 2. Clone and configure

```bash
sudo mkdir -p /srv
cd /srv
git clone https://github.com/beriberikix/zephyr-simulator-server.git
cd zephyr-simulator-server
cp .env.production.example .env.production
```

Edit `.env.production`:
- `DOMAIN=coap.cloud`
- `ACME_EMAIL=jmberi@gmail.com`
- `RUNTIME_NAME=runsc` (or `runc` if gVisor is unavailable)

## 3. Cloudflare DNS for first certificate issuance

Set `coap.cloud` DNS record to the droplet IP as `DNS only` (gray cloud).

Keep Cloudflare SSL mode at `Full (strict)`; proxy can be re-enabled after first successful issuance.

## 4. First deployment

```bash
./scripts/deploy/deploy_prod.sh
```

What this does:
- Builds emulator and backend images
- Starts stack with production compose overlay
- Runs local health checks through Caddy
- Records known-good commit in `.deploy/known_good_commit`

## 5. Verify

```bash
curl -fsS https://coap.cloud/health
curl -fsS https://coap.cloud/api/health
```

After success, switch DNS record to `Proxied` (orange cloud).

## 6. Upgrade to latest source code

Run on the droplet from repository root:

```bash
./scripts/deploy/upgrade_prod.sh
```

What this does:
- Stores current commit in `.deploy/previous_commit`
- Fast-forwards local `main` from `origin/main`
- Rebuilds and redeploys
- Verifies health checks

## 7. Rollback path

If upgrade verification fails, run:

```bash
./scripts/deploy/rollback_prod.sh
```

What this does:
- Checks out `.deploy/previous_commit`
- Rebuilds and redeploys that commit
- Re-runs health checks

## 8. PocketBase superuser bootstrap

```bash
docker exec zephyr-backend ./server superuser upsert admin@example.com 'strong-password'
```

## 9. Notes

- App data is stored in `./data` (host-mounted volume).
- Caddy certificates are stored in `caddy_data` Docker volume.
- Rollback script checks out a specific commit and may leave git in detached HEAD state. To return to branch tracking after rollback, use:

```bash
git checkout main
```