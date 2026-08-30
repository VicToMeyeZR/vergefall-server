# VPS setup — Ubuntu + server1.trystarbot.space

Fleet Net listens at **https://server1.trystarbot.space** (`209.74.87.104`). Caddy gets the certificate. Nakama is not on the public internet.

DNS must answer **before** you start the stack, or Let's Encrypt will fail.

---

## 1. DNS

At your registrar, A-record:

| Host | Type | Value |
|---|---|---|
| `server1.trystarbot.space` | A | `209.74.87.104` |

Confirm with `ping server1.trystarbot.space` — it must hit **209.74.87.104**, not Railway. No Cloudflare proxy (grey cloud) until certs exist.

## 2. SSH in

```bash
ssh root@209.74.87.104
```

Ubuntu 22.04 or 24.04. 2 GB RAM is enough.

## 3. Firewall

```bash
ufw allow OpenSSH
ufw allow 80/tcp
ufw allow 443/tcp
ufw --force enable
ufw status
```

Do **not** open 7350 or 7351.

## 4. Docker

```bash
apt-get update
apt-get install -y ca-certificates curl git
curl -fsSL https://get.docker.com | sh
docker --version
docker compose version
```

## 5. Clone and env

```bash
cd /opt
git clone https://github.com/VicToMeyeZR/vergefall-server.git
cd /opt/vergefall-server
cp .env.example .env
nano .env
```

Set `POSTGRES_PASSWORD` and `CONSOLE_PASSWORD` to long alphanumeric strings. **No** `@ : / #` in the postgres password.

```
DOMAIN=server1.trystarbot.space
CADDY_EMAIL=you@your-email
POSTGRES_PASSWORD=pickALongAlphanumeric
CONSOLE_PASSWORD=pickAnother
```

## 6. Build and start

First boot compiles the Go plugin. Give it a few minutes.

```bash
cd /opt/vergefall-server
docker compose -f docker-compose.vps.yml up -d --build
docker compose -f docker-compose.vps.yml logs -f nakama
```

You want:

```
Found runtime modules ... count:1
Vergefall module loaded — system rim-1 online
```

Ctrl-C leaves the stack running. Check Caddy got a cert:

```bash
docker compose -f docker-compose.vps.yml logs caddy | tail -30
curl -sI https://server1.trystarbot.space
```

`HTTP/2 401` or `200` from curl is success (Nakama rejects anonymous HTTP; TLS worked). `connection refused` / cert errors means DNS or port 80/443 is still blocked.

## 7. Helm

This preview already defaults to `https://server1.trystarbot.space`. **Connect Fleet Net** → **Join rim-1**.

Steel thread: enlist is automatic on join. System map → pirate hex `+4,−2` → commit a move. Time is real (minutes, not the local 60× compression). Battle report arrives as a notification.

## 8. Console (optional, SSH tunnel only)

```bash
ssh -L 7351:127.0.0.1:7351 root@209.74.87.104
```

Then open http://127.0.0.1:7351 — user `admin`, password from `.env`.

## 9. Later updates

```bash
cd /opt/vergefall-server
git pull
docker compose -f docker-compose.vps.yml up -d --build
```

World snapshots live in Nakama storage (Postgres volume `pgdata`). Do not delete named volumes unless you intend to wipe rim-1.

## If it fails

| Symptom | Likely cause |
|---|---|
| Caddy: `could not get certificate` | DNS still on Railway, or 80 blocked, or Cloudflare orange-cloud |
| Nakama: `plugin was built with a different version` | Image wasn't rebuilt; `--build` is required |
| Helm: mixed-content / unreachable | You pointed the Helm at `http://` instead of `https://server1.trystarbot.space` |
| Helm: 401 on RPC | Server key mismatch (keep `defaultkey` for M1) |
| Postgres: authentication failed | `.env` password contains `@ : /` or compose wasn't recreated after `.env` change (`down` then `up`) |
