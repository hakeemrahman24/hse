#!/usr/bin/env bash
# One-time setup of the Orange Pi for entry-permit. Run ON the Pi, as the
# orangepi user (it uses sudo only for MariaDB). Safe to run again: it never
# overwrites an existing .env or database.
#
#   1. folders on the NVMe (/mnt/data/projects/entry-permit/{uploads,signing})
#   2. ~/projects/entry-permit symlink is expected to resolve inside the NVMe
#   3. database entry_permit + user entry_permit_user with a generated password,
#      written to .env (chmod 600). The password is not printed.
#   4. the user systemd unit
set -euo pipefail

DATA_MOUNT="${DATA_MOUNT:-/mnt/data}"
APP_DIR="$DATA_MOUNT/projects/entry-permit"
DB_NAME="entry_permit"
DB_USER="entry_permit_user"

mountpoint -q "$DATA_MOUNT" || { echo "abort: $DATA_MOUNT is not mounted" >&2; exit 1; }
mkdir -p "$APP_DIR/uploads" "$APP_DIR/signing" "$HOME/projects" "$HOME/.config/systemd/user"
[ -e "$HOME/projects/entry-permit" ] || ln -s "$APP_DIR" "$HOME/projects/entry-permit"

if [ ! -f "$APP_DIR/.env" ]; then
  PW=$(head -c 24 /dev/urandom | base64 | tr -d '/+=' | head -c 24)
  SECRET=$(head -c 48 /dev/urandom | base64 | tr -d '/+=' | head -c 48)
  sudo mariadb <<SQL
CREATE DATABASE IF NOT EXISTS $DB_NAME CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER IF NOT EXISTS '$DB_USER'@'localhost' IDENTIFIED BY '$PW';
ALTER USER '$DB_USER'@'localhost' IDENTIFIED BY '$PW';
GRANT ALL PRIVILEGES ON $DB_NAME.* TO '$DB_USER'@'localhost';
FLUSH PRIVILEGES;
SQL
  umask 077
  {
    echo "PORT=8114"
    echo "DB_DRIVER=mysql"
    echo "DB_HOST=127.0.0.1"
    echo "DB_USER=$DB_USER"
    echo "DB_PASSWORD=$PW"
    echo "DB_NAME=$DB_NAME"
    echo "UPLOAD_DIR=$APP_DIR/uploads"
    echo "SESSION_SECRET=$SECRET"
    echo "MEDPOST_API_URL=http://127.0.0.1:8113"
    echo "MEDPOST_API_TOKEN="
    echo "GATE_WORKER_URL="
    echo "GATE_SHARED_SECRET="
  } > "$APP_DIR/.env"
  echo "created $APP_DIR/.env (fill MEDPOST_API_TOKEN, GATE_* and BASE_URL by hand)"
else
  echo ".env already exists, left untouched"
fi

echo "Copy systemd/entry-permit.service to ~/.config/systemd/user/, then:"
echo "  systemctl --user daemon-reload && systemctl --user enable entry-permit"
