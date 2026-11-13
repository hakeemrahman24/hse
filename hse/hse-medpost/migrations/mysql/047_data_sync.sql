-- Automatic data refresh (owner, 2026-11-13) (MariaDB). SQLite gets the same tables from
-- migrations/sqlite/031. See that file for what each column means.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/ entirely, so this runs
-- by hand, once, against the production database. Take a mysqldump backup first. Safe to run
-- twice (IF NOT EXISTS / INSERT IGNORE). Run it BEFORE deploying the build that reads the tables;
-- until they exist the refresh jobs log an error and do nothing, and the app works as before.
-- NOT TESTED against a real MariaDB server.

CREATE TABLE IF NOT EXISTS DataSyncSetting (
    Source         VARCHAR(32) NOT NULL PRIMARY KEY,
    Enabled        TINYINT NOT NULL DEFAULT 1,
    IntervalMonths INT NOT NULL DEFAULT 6,
    LastRunAt      VARCHAR(32) NULL,
    LastAttemptAt  VARCHAR(32) NULL,
    LastStatus     VARCHAR(16) NOT NULL DEFAULT '',
    LastMessage    VARCHAR(1000) NOT NULL DEFAULT '',
    AppliedAt      VARCHAR(32) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT IGNORE INTO DataSyncSetting (Source, Enabled, IntervalMonths, LastRunAt, LastStatus, LastMessage)
VALUES ('region', 1, 6, '2026-10-20T07:58:45', 'ok', 'Loaded by hand from indonesia-region (Kepmendagri 300.2.2-2138 Tahun 2025).');
INSERT IGNORE INTO DataSyncSetting (Source, Enabled, IntervalMonths) VALUES ('endemicity', 1, 3);

CREATE TABLE IF NOT EXISTS MalariaEndemicity (
    RegencyCode     VARCHAR(16) NOT NULL PRIMARY KEY,
    RegencyName     VARCHAR(120) NOT NULL,
    ProvinceName    VARCHAR(120) NOT NULL,
    Label           VARCHAR(40) NOT NULL,
    DataYear        INT NOT NULL,
    Cases           INT NOT NULL DEFAULT 0,
    Population      INT NOT NULL DEFAULT 0,
    SourceUpdatedAt VARCHAR(32) NOT NULL DEFAULT ''
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS MalariaEndemicityStaged (
    RegencyCode     VARCHAR(16) NOT NULL PRIMARY KEY,
    RegencyName     VARCHAR(120) NOT NULL,
    ProvinceName    VARCHAR(120) NOT NULL,
    Label           VARCHAR(40) NOT NULL,
    DataYear        INT NOT NULL,
    Cases           INT NOT NULL DEFAULT 0,
    Population      INT NOT NULL DEFAULT 0,
    SourceUpdatedAt VARCHAR(32) NOT NULL DEFAULT ''
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
