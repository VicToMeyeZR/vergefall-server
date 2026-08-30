#!/bin/sh
set -eu

# Nakama wants user:pass@host:port/db[?sslmode=...] — Railway/Neon give postgres:// URLs.
addr="${DATABASE_ADDRESS:-}"
if [ -z "$addr" ] && [ -n "${DATABASE_URL:-}" ]; then
  addr=$(printf '%s' "$DATABASE_URL" | sed -E 's#^postgres(ql)?://##')
fi
if [ -z "$addr" ]; then
  addr="postgres:localdev@postgres:5432/nakama"
fi

/nakama/nakama migrate up --database.address "$addr"
exec /nakama/nakama \
  --database.address "$addr" \
  --config /nakama/data/local.yml \
  --runtime.path /nakama/data \
  --session.token_expiry_sec "${SESSION_TOKEN_EXPIRY_SEC:-7200}" \
  --logger.level "${NAKAMA_LOG_LEVEL:-INFO}"
