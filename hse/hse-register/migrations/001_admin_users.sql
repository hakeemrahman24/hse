-- Login accounts for the Go-rendered admin (replaces Django's auth_user).
-- Not loaded automatically by the Go binary: run by hand, once, against hse_register.
--   mysql hse_register < migrations/001_admin_users.sql

CREATE TABLE IF NOT EXISTS admin_users (
    id            BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    username      VARCHAR(64) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
