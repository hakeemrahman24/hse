#!/usr/bin/env bash
# One-time host setup for hse-workpermit, hse-observation, medical-activities.
# Run once with sudo: sudo bash setup-host.sh
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "This script needs root (MariaDB root@localhost uses socket auth, and apt needs root)." >&2
  echo "Run: sudo bash setup-host.sh" >&2
  exit 1
fi

PROJECTS_DIR="/home/orangepi/projects"
APPS=(hse-workpermit hse-observation medical-activities)
DBNAMES=(hse_workpermit hse_observation medical_activities)

echo "==> Installing python3.11-venv"
apt-get update -qq
apt-get install -y python3.11-venv >/dev/null

for i in "${!APPS[@]}"; do
  APP="${APPS[$i]}"
  DB="${DBNAMES[$i]}"
  DBUSER="${DB}_user"
  PASS="$(openssl rand -base64 32 | tr -dc 'A-Za-z0-9' | head -c 32)"
  ENV_FILE="${PROJECTS_DIR}/${APP}/db.env"

  if [ ! -d "${PROJECTS_DIR}/${APP}" ]; then
    echo "Skipping ${APP}: ${PROJECTS_DIR}/${APP} does not exist" >&2
    continue
  fi

  echo "==> Creating database '${DB}' and user '${DBUSER}'"
  mysql <<SQL
CREATE DATABASE IF NOT EXISTS \`${DB}\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER IF NOT EXISTS '${DBUSER}'@'localhost' IDENTIFIED BY '${PASS}';
ALTER USER '${DBUSER}'@'localhost' IDENTIFIED BY '${PASS}';
GRANT ALL PRIVILEGES ON \`${DB}\`.* TO '${DBUSER}'@'localhost';
FLUSH PRIVILEGES;
SQL

  cat > "${ENV_FILE}" <<EOF
DB_NAME=${DB}
DB_USER=${DBUSER}
DB_PASSWORD=${PASS}
DB_HOST=127.0.0.1
DB_PORT=3306
EOF
  chown orangepi:orangepi "${ENV_FILE}"
  chmod 600 "${ENV_FILE}"
  echo "    wrote ${ENV_FILE}"
done

echo
echo "Done. Each app's db.env now has its MariaDB credentials."
echo
echo "Optional, to keep 'systemctl --user' services running without an active login session:"
echo "  loginctl enable-linger orangepi"
