#!/usr/bin/env bash
# One-time host setup for hse-register on a native (non-Docker) MariaDB install.
# Run with sudo from this project's deploy/ directory, or from ~/projects/hse-register.
#
# Assumes: MariaDB server already installed and running (native, not containerized),
# Tailscale and cloudflared already configured separately (they front this app's
# localhost-bound ports; this script does not touch either).
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DB_ENV="$PROJECT_DIR/db.env"

if [ ! -f "$DB_ENV" ]; then
  echo "Missing $DB_ENV — generate .env/db.env before running this script." >&2
  exit 1
fi

# shellcheck disable=SC1090
source "$DB_ENV"

apt-get update
apt-get install -y python3.11-venv mariadb-server

mysql <<SQL
CREATE DATABASE IF NOT EXISTS ${DB_NAME} CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER IF NOT EXISTS '${DB_USER}'@'localhost' IDENTIFIED BY '${DB_PASSWORD}';
GRANT ALL PRIVILEGES ON ${DB_NAME}.* TO '${DB_USER}'@'localhost';
FLUSH PRIVILEGES;
SQL

echo "Database '${DB_NAME}' and user '${DB_USER}' ready."
