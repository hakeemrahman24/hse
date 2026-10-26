#!/usr/bin/env bash
# Weekly cleanup of the hse-medpost rollback copies and manual database dumps
# on the Pi. Keeps the KEEP newest of each kind (by modification time) and
# deletes the rest. Run from cron on Mondays, after the nightly backup job.
#
# Set DRY_RUN=1 to list what would be deleted without deleting anything.
set -euo pipefail

KEEP=5
APP="$HOME/projects/hse-medpost"
DRY_RUN="${DRY_RUN:-0}"

# prune_kind <find args...>: delete all but the KEEP newest matches.
prune_kind() {
  local matches
  matches=$(find "$@" -print0 2>/dev/null | xargs -0 -r stat -c '%Y %n' | sort -rn | tail -n +$((KEEP + 1)) | cut -d' ' -f2-)
  [ -z "$matches" ] && return 0
  while IFS= read -r path; do
    if [ "$DRY_RUN" = "1" ]; then
      echo "$(date -Is) would remove $path"
    else
      rm -rf -- "$path"
      echo "$(date -Is) removed $path"
    fi
  done <<< "$matches"
}

prune_kind "$APP" -maxdepth 1 -name 'hse-medpost.bak-*'
prune_kind "$APP" -maxdepth 1 -name 'web.bak-*'
prune_kind "$HOME" -maxdepth 1 -name 'hse-medpost-before-fatigue-*.sql'
