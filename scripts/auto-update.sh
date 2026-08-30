#!/bin/sh
# Pull origin/main every minute. Rebuild only when HEAD moves.
# .env is untracked and survives git reset --hard.
set -eu
REPO="${REPO_DIR:-/opt/vergefall-server}"
cd "$REPO"

if ! command -v git >/dev/null 2>&1; then
  apk add --no-cache git >/dev/null
fi
git config --global --add safe.directory "$REPO" 2>/dev/null || true

echo "auto-update watching $REPO"

while true; do
  if git fetch --quiet origin main; then
    LOCAL=$(git rev-parse HEAD)
    REMOTE=$(git rev-parse origin/main)
    if [ "$LOCAL" != "$REMOTE" ]; then
      echo "updating $LOCAL -> $REMOTE"
      git reset --hard origin/main
      docker compose -f "$REPO/docker-compose.yml" --project-directory "$REPO" up -d --build
    fi
  else
    echo "git fetch failed"
  fi
  sleep 60
done
