#!/usr/bin/env bash
# Deploy an update to the Pi (copied from hse-medpost, APP changed).
#
#   deploy/deploy.sh web    only web/ changed (templates, JS, CSS): no build
#   deploy/deploy.sh full   Go code changed: builds the arm64 binary, then also sends web/
#
# The Pi keeps rollback copies (entry-permit.bak-<TS>, web.bak-<TS>), swaps the
# new files in, restarts the service and checks /healthz. The binary is sent
# with rsync -z against the copy already there, so only differences travel.
# Migrations run by themselves when the service starts (internal/db Migrate).
# .env is never copied: it holds secrets and lives only on the Pi.
#
# DRY_RUN=1 prints every step. HOST and OUT can be overridden.
# DATA_MOUNT= (empty) skips the NVMe-mounted check.
set -euo pipefail

HOST="${HOST:-orangepi@orangepi3b}"
APP="projects/entry-permit"
DATA_MOUNT="${DATA_MOUNT-/mnt/data}"
OUT="${OUT:-${TMPDIR:-/tmp}/entry-permit-deploy}"
DRY_RUN="${DRY_RUN:-0}"
SSH="ssh -o BatchMode=yes"

mode="${1:-}"
case "$mode" in
  web | full) ;;
  *) echo "usage: $0 web|full" >&2; exit 2 ;;
esac

cd "$(dirname "$0")/.."

run() {
  if [ "$DRY_RUN" = "1" ]; then echo "+ $*"; else "$@"; fi
}

# Stop before sending anything if the SSD is not mounted (the ~/projects symlink would dangle).
if [ -n "$DATA_MOUNT" ]; then
  PREFLIGHT="mountpoint -q $DATA_MOUNT && case \$(readlink -f ~/$APP) in $DATA_MOUNT/*) ;; *) false ;; esac"
  run $SSH "$HOST" "$PREFLIGHT" || {
    echo "abort: $DATA_MOUNT is not mounted on $HOST, or ~/$APP does not resolve inside it." >&2
    echo "Run deploy/setup-host.sh once on the Pi first. DATA_MOUNT= skips this check." >&2
    exit 1
  }
fi

if [ "$mode" = "full" ]; then
  mkdir -p "$OUT"
  run env GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o "$OUT/entry-permit" ./cmd/server
  run $SSH "$HOST" "cd ~/$APP && { [ -f entry-permit ] && cp -p entry-permit entry-permit.new || true; }"
  run rsync -z --partial -e "$SSH" "$OUT/entry-permit" "$HOST:$APP/entry-permit.new"
fi

# web/ appears in a later slice; skip it until it exists.
if [ -d web ]; then
  run $SSH "$HOST" "cd ~/$APP && rm -rf web.new && { [ -d web ] && cp -a web web.new || mkdir web.new; }"
  run rsync -az --delete -e "$SSH" web/ "$HOST:$APP/web.new/"
fi

REMOTE_STEPS='set -e
cd ~/projects/entry-permit
TS=$(date +%Y%m%d%H%M)
if [ -d web.new ]; then
  [ -d web ] && cp -a web web.bak-$TS
  rm -rf web && mv web.new web
fi
if [ "$MODE" = "full" ]; then
  [ -f entry-permit ] && cp -p entry-permit entry-permit.bak-$TS
  chmod 755 entry-permit.new && mv entry-permit.new entry-permit
fi
systemctl --user restart entry-permit
sleep 3
systemctl --user is-active entry-permit
P=$(grep -E "^PORT=" .env | cut -d= -f2)
curl -s -o /dev/null -w "healthz %{http_code}\n" "http://127.0.0.1:$P/healthz"
journalctl --user -u entry-permit -n 2 --no-pager | cut -c1-160
echo "rollback copies: entry-permit.bak-$TS"'

run $SSH "$HOST" "MODE=$mode bash -c '$REMOTE_STEPS'"
