#!/usr/bin/env bash
# Load the Indonesia region snapshot into the Region table.
#
#   bash load.sh --env ../.env           # MariaDB, using the app's own .env (default)
#   bash load.sh --sqlite /path/to.db    # SQLite, for development and tests
#
# Options:
#   --data FILE   the INSERT file to load (default: data/region-data.sql)
#   --env FILE    the app's .env (default: ../.env)
#
# The load is one transaction: the old rows are deleted and the new ones inserted,
# and if anything fails the old rows stay exactly as they were. Safe to run again.
# The password is read from .env and handed to the client through MYSQL_PWD; it is
# never printed and .env is never sourced (it is parsed as plain KEY=VALUE text).
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DATA="$HERE/data/region-data.sql"
ENV_FILE="$HERE/../.env"
SQLITE_DB=""

usage() {
  sed -n '2,13p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2
  exit 2
}

while [ $# -gt 0 ]; do
  case "$1" in
    --sqlite) [ $# -ge 2 ] || usage; SQLITE_DB="$2"; shift 2 ;;
    --data)   [ $# -ge 2 ] || usage; DATA="$2"; shift 2 ;;
    --env)    [ $# -ge 2 ] || usage; ENV_FILE="$2"; shift 2 ;;
    -h|--help) usage ;;
    *) echo "unknown option: $1" >&2; usage ;;
  esac
done

# Check the input before touching any database.
[ -f "$DATA" ] || { echo "data file not found: $DATA (run update.py first)" >&2; exit 1; }
[ -s "$DATA" ] || { echo "data file is empty: $DATA" >&2; exit 1; }
grep -q '^INSERT INTO Region' "$DATA" || { echo "no Region INSERTs in $DATA" >&2; exit 1; }

env_value() { # env_value KEY -> value from the .env file, without sourcing it
  grep -E "^$1=" "$ENV_FILE" | tail -n1 | cut -d= -f2- | sed -e 's/^"\(.*\)"$/\1/' -e "s/^'\(.*\)'\$/\1/" || true
}

load_sqlite() {
  command -v sqlite3 >/dev/null || { echo "sqlite3 is not installed" >&2; exit 1; }
  sqlite3 "$SQLITE_DB" < "$HERE/schema/region.sqlite.sql"
  {
    echo ".bail on"
    echo "BEGIN;"
    echo "DELETE FROM Region;"
    cat "$DATA"
    echo "COMMIT;"
  } | sqlite3 "$SQLITE_DB"
  echo "loaded $(sqlite3 "$SQLITE_DB" 'SELECT COUNT(*) FROM Region') rows into $SQLITE_DB"
}

load_mariadb() {
  [ -f "$ENV_FILE" ] || { echo "no target: give --sqlite FILE, or an --env file (not found: $ENV_FILE)" >&2; exit 2; }
  command -v mysql >/dev/null || { echo "the mysql client is not installed" >&2; exit 1; }
  local host port user name
  host="$(env_value DB_HOST)"; port="$(env_value DB_PORT)"
  user="$(env_value DB_USER)"; name="$(env_value DB_NAME)"
  MYSQL_PWD="$(env_value DB_PASSWORD)"
  export MYSQL_PWD
  [ -n "$user" ] && [ -n "$name" ] || { echo "DB_USER or DB_NAME missing in $ENV_FILE" >&2; exit 1; }
  local -a client=(mysql --host="${host:-127.0.0.1}" --port="${port:-3306}" --user="$user" --default-character-set=utf8mb4 "$name")

  "${client[@]}" < "$HERE/schema/region.mariadb.sql"
  # CREATE/DROP commit implicitly in MariaDB, but DELETE and INSERT do not, so
  # the whole swap is one transaction and a failed INSERT rolls the DELETE back.
  {
    echo "SET autocommit=0;"
    echo "START TRANSACTION;"
    echo "DELETE FROM Region;"
    cat "$DATA"
    echo "COMMIT;"
  } | "${client[@]}"
  echo "loaded $("${client[@]}" -N -e 'SELECT COUNT(*) FROM Region') rows into $name"
}

if [ -n "$SQLITE_DB" ]; then
  load_sqlite
else
  load_mariadb
fi
