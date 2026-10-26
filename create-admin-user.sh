#!/usr/bin/env bash
# Creates one MariaDB user with full privileges across all 3 project databases,
# for logging into Adminer once instead of using per-app credentials.
# Run once with sudo: sudo bash create-admin-user.sh
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "Run: sudo bash create-admin-user.sh" >&2
  exit 1
fi

DBUSER="projects_admin"
PASS="$(openssl rand -base64 32 | tr -dc 'A-Za-z0-9' | head -c 32)"
DBNAMES=(hse_workpermit hse_observation medical_activities)

echo "==> Creating user '${DBUSER}' with privileges on: ${DBNAMES[*]}"

GRANTS=""
for DB in "${DBNAMES[@]}"; do
  GRANTS="${GRANTS}GRANT ALL PRIVILEGES ON \`${DB}\`.* TO '${DBUSER}'@'localhost';
"
done

mysql <<SQL
CREATE USER IF NOT EXISTS '${DBUSER}'@'localhost' IDENTIFIED BY '${PASS}';
ALTER USER '${DBUSER}'@'localhost' IDENTIFIED BY '${PASS}';
${GRANTS}
FLUSH PRIVILEGES;
SQL

ENV_FILE="/home/orangepi/projects/admin-db.env"
cat > "${ENV_FILE}" <<EOF
DB_ADMIN_USER=${DBUSER}
DB_ADMIN_PASSWORD=${PASS}
DB_ADMIN_DATABASES=${DBNAMES[*]}
EOF
chown orangepi:orangepi "${ENV_FILE}"
chmod 600 "${ENV_FILE}"

echo
echo "Done. Login to Adminer (http://orangepi3b:8091/) with:"
echo "  Server:   127.0.0.1"
echo "  Username: ${DBUSER}"
echo "  Password: (see ${ENV_FILE})"
