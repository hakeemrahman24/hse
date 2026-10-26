#!/usr/bin/env bash
# Deploy an update to the Pi, from the repo root or anywhere (it finds the repo).
#
#   deploy/deploy.sh web    only web/ changed (templates, JS, CSS): about 1 MB, no build
#   deploy/deploy.sh full   Go code changed: builds the arm64 binary, then also sends web/
#
# Either way the Pi keeps rollback copies (hse-medpost.bak-<TS>, web.bak-<TS>),
# swaps the new files in, restarts the service and checks it answers. Templates
# are parsed at startup, so a web-only deploy still restarts the service.
#
# The binary is about 27 MB. It is sent with rsync -z against the copy already on
# the Pi, so only the differing parts travel (and an interrupted upload resumes).
#
# DRY_RUN=1 prints every step instead of running it.
# HOST and OUT can be overridden for another machine or build folder.
# Before sending anything it checks that the Pi's NVMe is mounted at DATA_MOUNT
# (default /mnt/data) and that ~/projects resolves inside it; DATA_MOUNT= skips that
# for a machine without an NVMe.
# A new migrations/mysql/0NN_*.sql is NOT run by this: see README, "Deploy an update".
set -euo pipefail

HOST="${HOST:-orangepi@orangepi3b}"
APP="projects/hse-medpost"
DATA_MOUNT="${DATA_MOUNT-/mnt/data}"   # "-" not ":-", so DATA_MOUNT= (empty) really disables the check
OUT="${OUT:-${TMPDIR:-/tmp}/hse-medpost-deploy}"
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

# Since 2026-10-26 the Pi keeps ~/projects on the NVMe (a symlink to /mnt/data/projects).
# If the SSD failed to mount, that symlink dangles and a deploy would die half way with a
# confusing "No such file" after the upload; stop here instead, before anything is sent.
if [ -n "$DATA_MOUNT" ]; then
  PREFLIGHT="mountpoint -q $DATA_MOUNT && case \$(readlink -f ~/$APP) in $DATA_MOUNT/*) ;; *) false ;; esac"
  run $SSH "$HOST" "$PREFLIGHT" || {
    echo "abort: $DATA_MOUNT is not mounted on $HOST, or ~/$APP does not resolve inside it." >&2
    echo "Check 'findmnt $DATA_MOUNT' and 'ls -l ~/projects' on the Pi. Set DATA_MOUNT= to skip this check." >&2
    exit 1
  }
fi

if [ "$mode" = "full" ]; then
  mkdir -p "$OUT"
  run env GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o "$OUT/hse-medpost" ./cmd/server
  # Seed the staging name with the running binary so rsync sends only the difference.
  run $SSH "$HOST" "cd ~/$APP && cp -p hse-medpost hse-medpost.new"
  run rsync -z --partial -e "$SSH" "$OUT/hse-medpost" "$HOST:$APP/hse-medpost.new"
fi

# Same idea for web/: start from the live copy, so only changed files travel.
run $SSH "$HOST" "cd ~/$APP && rm -rf web.new && cp -a web web.new"
run rsync -az --delete -e "$SSH" web/ "$HOST:$APP/web.new/"

REMOTE_STEPS='set -e
cd ~/projects/hse-medpost
TS=$(date +%Y%m%d%H%M)
cp -a web web.bak-$TS
if [ "$MODE" = "full" ]; then
  cp -p hse-medpost hse-medpost.bak-$TS
  chmod 755 hse-medpost.new && mv hse-medpost.new hse-medpost
fi
rm -rf web && mv web.new web
systemctl --user restart hse-medpost
sleep 3
systemctl --user is-active hse-medpost
P=$(grep -E "^PORT=" .env | cut -d= -f2)
for u in "/ root" "/emr emr" "/api/malaria-screenings list"; do
  set -- $u
  curl -s -o /dev/null -w "$2 %{http_code}\n" "http://127.0.0.1:$P$1"
done
journalctl --user -u hse-medpost -n 2 --no-pager | cut -c1-160
echo "rollback copies: web.bak-$TS$([ "$MODE" = full ] && echo " hse-medpost.bak-$TS")"'

run $SSH "$HOST" "MODE=$mode bash -c '$REMOTE_STEPS'"
