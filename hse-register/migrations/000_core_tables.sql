-- Core domain tables, previously owned by Django's migrations (now removed).
-- On the Pi these already exist from the original Django deploy - this file
-- only matters for a fresh database (e.g. local dev):
--   mysql hse_register < migrations/000_core_tables.sql

CREATE TABLE IF NOT EXISTS workpermitregister (
    id            BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    permit_number VARCHAR(64) NOT NULL,
    title         VARCHAR(255) NOT NULL,
    location      VARCHAR(255) NOT NULL,
    work_type     VARCHAR(64) NOT NULL DEFAULT 'general',
    requested_by  VARCHAR(255) NOT NULL,
    status        VARCHAR(32) NOT NULL DEFAULT 'draft',
    description   TEXT NOT NULL,
    start_date    DATETIME NOT NULL,
    end_date      DATETIME NOT NULL,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS observations_observation (
    id            BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    observer_name VARCHAR(255) NOT NULL,
    location      VARCHAR(255) NOT NULL,
    category      VARCHAR(64) NOT NULL,
    severity      VARCHAR(32) NOT NULL DEFAULT 'low',
    description   TEXT NOT NULL,
    status        VARCHAR(32) NOT NULL DEFAULT 'open',
    observed_at   DATETIME NOT NULL,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
