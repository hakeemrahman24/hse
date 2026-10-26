#!/usr/bin/env bash
# Move the MariaDB datadir and ~/projects from the eMMC to the NVMe, reversibly.
# Run ON THE PI (needs root; MariaDB root@localhost uses socket auth):
#   sudo bash move-to-nvme.sh check      # read-only preflight, changes nothing
#   sudo bash move-to-nvme.sh apply      # do the move; auto-rolls back on any failure
#   sudo bash move-to-nvme.sh rollback   # undo a finished move (syncs new writes back first)
#
# Safety rules this script follows:
#   * It never deletes the old copies (/var/lib/mysql.old, ~/projects.old). You do that later.
#   * It only switches over after the copy is verified identical.
#   * It only keeps the switch if MariaDB, every table's row count, and every app unit check out.
#   * On any error or Ctrl-C it puts everything back and restarts the apps.
set -Eeuo pipefail

MODE="${1:-}"
APP_USER="${APP_USER:-orangepi}"
DATA="${DATA:-/mnt/data}"
PROJECTS="/home/${APP_USER}/projects"
NEW_PROJECTS="${DATA}/projects"
OLD_DATADIR="/var/lib/mysql"
NEW_DATADIR="${DATA}/mysql"
read -r -a UNIT_GLOBS <<< "${UNIT_GLOBS:-hse-* medical-activities-* adminer*}"
read -r -a HEALTH_URLS <<< "${HEALTH_URLS:-}"   # optional, e.g. HEALTH_URLS="http://127.0.0.1:8113/"
STATE="/var/lib/move-to-nvme"             # lives on the eMMC on purpose (tiny, survives a missing SSD)
LOG="/var/log/move-to-nvme.log"
CNF="/etc/mysql/mariadb.conf.d/99-nvme-datadir.cnf"
DROPIN_DIR="/etc/systemd/system/mariadb.service.d"
DROPIN="${DROPIN_DIR}/nvme.conf"
AA_LOCAL="/etc/apparmor.d/local/usr.sbin.mysqld"
AA_BEGIN="# BEGIN move-to-nvme"
AA_END="# END move-to-nvme"

log() { printf '[%s] %s\n' "$(date +%T)" "$*"; }
die() { log "ABORT: $*"; exit 1; }

[ "$(id -u)" -eq 0 ] || die "run with sudo"
case "$MODE" in check|apply|rollback) ;; *) die "usage: sudo bash $0 check|apply|rollback" ;; esac
mkdir -p "$STATE"
exec > >(tee -a "$LOG") 2>&1

APP_UID="$(id -u "$APP_USER")"
UNITS=()
TOUCHED=0        # 1 while a change is in flight and must be undone on failure
APPS_STOPPED=0   # 1 while we are holding the app units down

user_ctl() {
  runuser -u "$APP_USER" -- env XDG_RUNTIME_DIR="/run/user/${APP_UID}" \
    DBUS_SESSION_BUS_ADDRESS="unix:path=/run/user/${APP_UID}/bus" systemctl --user "$@"
}

discover_units() {
  mapfile -t UNITS < <(user_ctl list-units --state=active --no-legend --plain "${UNIT_GLOBS[@]}" | awk '{print $1}')
}

stop_apps() {
  [ "${#UNITS[@]}" -gt 0 ] || return 0
  log "stopping app units: ${UNITS[*]}"
  APPS_STOPPED=1
  user_ctl stop "${UNITS[@]}"
}

start_apps() {
  [ "${#UNITS[@]}" -gt 0 ] || return 0
  log "starting app units: ${UNITS[*]}"
  user_ctl start "${UNITS[@]}"
  APPS_STOPPED=0
}

verify_apps() {
  [ "${#UNITS[@]}" -gt 0 ] || return 0
  sleep 8   # long enough for a crash-loop to show
  local u
  for u in "${UNITS[@]}"; do
    user_ctl is-active --quiet "$u" || { log "unit not active: $u"; return 1; }
  done
  for u in "${HEALTH_URLS[@]}"; do
    curl -fsS -m 10 -o /dev/null "$u" || { log "health check failed: $u"; return 1; }
  done
}

wait_mariadb() {
  local i
  for i in $(seq 60); do mysqladmin ping --silent 2>/dev/null && return 0; sleep 1; done
  return 1
}

# "schema.table<TAB>rowcount" for every user table: a cheap before/after integrity fingerprint.
fingerprint() {
  mysql -Nse "SELECT table_schema, table_name FROM information_schema.tables
              WHERE table_type='BASE TABLE'
                AND table_schema NOT IN ('mysql','information_schema','performance_schema','sys')
              ORDER BY 1,2" |
  while IFS=$'\t' read -r s t; do
    printf '%s.%s\t%s\n' "$s" "$t" "$(mysql -Nse "SELECT COUNT(*) FROM \`$s\`.\`$t\`")"
  done
}

current_datadir() { mysql -Nse 'SELECT @@datadir' | sed 's:/*$::'; }

# rsync that must end up identical: copy, then a dry-run diff has to print nothing.
copy_verified() {
  local src="$1" dst="$2"
  rsync -aHAX "$src/" "$dst/"
  local diff_out
  diff_out="$(rsync -aHAXn --delete --itemize-changes "$src/" "$dst/")"
  [ -z "$diff_out" ] || { log "copy differs after rsync:"; echo "$diff_out" | head -20; return 1; }
}

# ---------- rollback (each is tolerant: inspects real state, safe to run when nothing was done) ----------

rollback_projects() {
  if [ -L "$PROJECTS" ] && [ -d "${PROJECTS}.old" ]; then
    log "rollback: projects -> eMMC (syncing any new writes back first)"
    rsync -aHAX --delete "${NEW_PROJECTS}/" "${PROJECTS}.old/"
    rm "$PROJECTS"            # removes the symlink only
    mv "${PROJECTS}.old" "$PROJECTS"
  fi
}

rollback_mariadb() {
  log "rollback: mariadb -> eMMC datadir"
  systemctl stop mariadb || true
  rm -f "$CNF" "$DROPIN"
  rmdir "$DROPIN_DIR" 2>/dev/null || true
  if [ -f "$AA_LOCAL" ]; then sed -i "/${AA_BEGIN}/,/${AA_END}/d" "$AA_LOCAL"; fi
  systemctl daemon-reload
  if [ -d "${OLD_DATADIR}.old" ] && [ ! -e "$OLD_DATADIR" ]; then
    if [ -d "$NEW_DATADIR" ]; then rsync -aHAX --delete "${NEW_DATADIR}/" "${OLD_DATADIR}.old/"; fi
    mv "${OLD_DATADIR}.old" "$OLD_DATADIR"
  fi
  if [ -f /etc/apparmor.d/usr.sbin.mysqld ] && command -v apparmor_parser >/dev/null; then
    apparmor_parser -r /etc/apparmor.d/usr.sbin.mysqld || true
  fi
  systemctl start mariadb
}

rollback_all() {
  set +e
  if [ "$APPS_STOPPED" != 1 ]; then discover_units; stop_apps; fi
  rollback_projects
  rollback_mariadb
  rm -f "$STATE/done"
  set -e
}

cleanup() {
  local rc=$?
  trap - EXIT ERR
  if [ "$rc" -ne 0 ] && [ "$TOUCHED" = 1 ]; then
    log "FAILED (exit $rc) - rolling back"
    rollback_all || log "ROLLBACK HIT ERRORS - check 'systemctl status mariadb' and ${LOG}"
  fi
  if [ "$APPS_STOPPED" = 1 ]; then start_apps || true; fi
  exit "$rc"
}
trap 'log "error at line $LINENO"' ERR
trap cleanup EXIT
trap 'exit 130' INT TERM

# ---------- rollback mode ----------

if [ "$MODE" = rollback ]; then
  [ -e "$STATE/done" ] || die "no completed move recorded in $STATE/done - nothing to roll back"
  rollback_all
  start_apps
  verify_apps
  log "rolled back. The NVMe copies are untouched; remove them yourself if you want the space."
  exit 0
fi

# ---------- preflight (also the whole of 'check') ----------

log "== preflight =="
[ -d "/home/${APP_USER}" ] || die "no /home/${APP_USER}: is this the Pi?"
systemctl is-active --quiet mariadb || die "mariadb is not running here: is this the Pi?"
[ -d "/run/user/${APP_UID}" ] || die "no user manager for ${APP_USER}; run: loginctl enable-linger ${APP_USER}"

mountpoint -q "$DATA" || die "$DATA is not a mounted filesystem (mount the NVMe first)"
[ "$(stat -c %d "$DATA")" != "$(stat -c %d /)" ] || die "$DATA is on the same device as / - NVMe not mounted there"
findmnt --fstab "$DATA" >/dev/null || die "$DATA has no /etc/fstab entry; after a reboot MariaDB would not start. Add it (UUID=..., nofail) first"
{ touch "$DATA/.write-test" && rm "$DATA/.write-test"; } || die "$DATA is not writable"

DD="$(current_datadir)"
if [ "$DD" = "$NEW_DATADIR" ]; then DB_DONE=1; else DB_DONE=0; fi
if [ "$DB_DONE" = 0 ]; then [ "$DD" = "$OLD_DATADIR" ] || die "unexpected datadir '$DD' (expected $OLD_DATADIR)"; fi
if [ -L "$PROJECTS" ]; then PR_DONE=1; else PR_DONE=0; fi
if [ "$PR_DONE" = 0 ]; then [ -d "$PROJECTS" ] || die "$PROJECTS not found"; fi
[ "$DB_DONE$PR_DONE" != "11" ] || die "both already moved; nothing to do"
if [ "$DB_DONE" = 0 ] && [ -e "${OLD_DATADIR}.old" ]; then die "${OLD_DATADIR}.old already exists; move it away first"; fi
if [ "$PR_DONE" = 0 ] && [ -e "${PROJECTS}.old" ]; then die "${PROJECTS}.old already exists; move it away first"; fi

need=0
if [ "$DB_DONE" = 0 ]; then need=$((need + $(du -sb "$OLD_DATADIR" | cut -f1))); fi
if [ "$PR_DONE" = 0 ]; then need=$((need + $(du -sb "$PROJECTS" | cut -f1))); fi
avail="$(df --output=avail -B1 "$DATA" | tail -1 | tr -d ' ')"
log "to copy: $((need / 1048576)) MiB, free on $DATA: $((avail / 1048576)) MiB"
[ "$avail" -gt $((need * 2 + 1073741824)) ] || die "not enough free space on $DATA (need 2x + 1 GiB)"

if grep -lE '^(ProtectHome|ProtectSystem|ReadWritePaths|ReadOnlyPaths|RootDirectory)' \
     "/home/${APP_USER}/.config/systemd/user/"*.service 2>/dev/null; then
  log "WARNING: the units above use sandbox options; they will be checked after the move"
fi

discover_units
log "app units that will be stopped and restarted: ${UNITS[*]:-(none found)}"
if [ "$MODE" = check ]; then
  log "preflight OK. Nothing changed. Run 'sudo bash $0 apply' to do the move."
  exit 0
fi

# ---------- apply ----------

log "== backup =="
BACKUP="/home/${APP_USER}/pre-move/all-dbs-$(date +%F-%H%M%S).sql"
mkdir -p "$(dirname "$BACKUP")"
mysqldump --all-databases --single-transaction --routines --triggers --events > "$BACKUP"
tail -n 3 "$BACKUP" | grep -q "Dump completed" || die "backup looks incomplete: $BACKUP"
chown -R "${APP_USER}:${APP_USER}" "$(dirname "$BACKUP")"
log "backup OK: $BACKUP ($(du -h "$BACKUP" | cut -f1))"

TOUCHED=1
stop_apps
if [ "$PR_DONE" = 0 ] && command -v lsof >/dev/null; then
  # lsof exits 1 when nothing is open (the good case); don't let pipefail treat that as failure
  stray="$({ lsof -t +D "$PROJECTS" 2>/dev/null || true; } | sort -u | tr '\n' ' ')"
  [ -z "$stray" ] || die "processes still use $PROJECTS (PIDs: $stray) - stop them (leftover nohup?) and retry"
fi

fingerprint > "$STATE/before.tsv"
log "fingerprint: $(wc -l < "$STATE/before.tsv") tables"
LISTEN_BEFORE=0; if ss -ltn | grep -q ':3306 '; then LISTEN_BEFORE=1; fi

if [ "$DB_DONE" = 0 ]; then
  log "== mariadb =="
  systemctl stop mariadb
  mkdir -p "$NEW_DATADIR"
  copy_verified "$OLD_DATADIR" "$NEW_DATADIR"
  chown mysql:mysql "$NEW_DATADIR"
  mv "$OLD_DATADIR" "${OLD_DATADIR}.old"

  printf '[mysqld]\ndatadir = %s\n' "$NEW_DATADIR" > "$CNF"
  mkdir -p "$DROPIN_DIR"
  printf '[Unit]\nRequiresMountsFor=%s\n\n[Service]\nReadWritePaths=%s\n' "$NEW_DATADIR" "$NEW_DATADIR" > "$DROPIN"
  if [ -f /etc/apparmor.d/usr.sbin.mysqld ]; then
    mkdir -p "$(dirname "$AA_LOCAL")"
    { echo "$AA_BEGIN"; echo "${NEW_DATADIR}/ r,"; echo "${NEW_DATADIR}/** rwk,"; echo "$AA_END"; } >> "$AA_LOCAL"
    apparmor_parser -r /etc/apparmor.d/usr.sbin.mysqld
  fi
  systemctl daemon-reload
  systemctl start mariadb
  wait_mariadb || die "mariadb did not come up on the NVMe datadir"
  [ "$(current_datadir)" = "$NEW_DATADIR" ] || die "mariadb is not using $NEW_DATADIR"
  mysqlcheck --all-databases >/dev/null || die "mysqlcheck reported problems"
  fingerprint > "$STATE/after.tsv"
  diff "$STATE/before.tsv" "$STATE/after.tsv" >/dev/null || die "row counts changed after the move"
  if [ "$LISTEN_BEFORE" = 1 ]; then ss -ltn | grep -q ':3306 ' || die "mariadb no longer listens on :3306"; fi
  log "mariadb verified on NVMe (config, integrity check, row counts identical)"
fi

if [ "$PR_DONE" = 0 ]; then
  log "== projects =="
  mkdir -p "$NEW_PROJECTS"
  copy_verified "$PROJECTS" "$NEW_PROJECTS"
  mv "$PROJECTS" "${PROJECTS}.old"
  ln -s "$NEW_PROJECTS" "$PROJECTS"
  chown -h "${APP_USER}:${APP_USER}" "$PROJECTS"
  runuser -u "$APP_USER" -- test -r "$PROJECTS" || die "$APP_USER cannot read $PROJECTS"
  log "projects switched via symlink"
fi

start_apps
verify_apps || die "an app unit or health check failed after the move"

touch "$STATE/done"
TOUCHED=0
log "== DONE: apps are running from the NVMe =="
log "Old copies kept: ${OLD_DATADIR}.old and ${PROJECTS}.old. After a few days of normal use:"
log "  sudo rm -rf ${OLD_DATADIR}.old ; rm -rf ${PROJECTS}.old"
log "To undo any time before that: sudo bash $0 rollback"
