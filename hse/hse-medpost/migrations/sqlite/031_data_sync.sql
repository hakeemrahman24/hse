-- Automatic data refresh (owner, 2026-11-13): the Indonesia region list (every 6 months by default)
-- and the Kemenkes malaria endemicity map (scraped, applied only after an admin reviews it).
-- MariaDB gets the same tables from migrations/mysql/047.
--
-- DataSyncSetting: one row per source ('region', 'endemicity'). LastRunAt is the last SUCCESSFUL
-- run (UTC, YYYY-MM-DDTHH:MM:SS); LastAttemptAt also counts failures, so a failing source is
-- retried once a day instead of every hourly check. AppliedAt is used by 'endemicity' only: when an
-- admin applied the reviewed data. The region row starts at the time the snapshot in
-- indonesia-region/data/VERSION.json was fetched, so the first automatic refresh is 6 months after it.
CREATE TABLE IF NOT EXISTS DataSyncSetting (
    Source         TEXT PRIMARY KEY,
    Enabled        INTEGER NOT NULL DEFAULT 1,
    IntervalMonths INTEGER NOT NULL DEFAULT 6,      -- 1, 3, 6 or 12, checked in Go
    LastRunAt      TEXT NULL,
    LastAttemptAt  TEXT NULL,
    LastStatus     TEXT NOT NULL DEFAULT '',        -- '', 'ok' or 'error'
    LastMessage    TEXT NOT NULL DEFAULT '',
    AppliedAt      TEXT NULL
);
INSERT OR IGNORE INTO DataSyncSetting (Source, Enabled, IntervalMonths, LastRunAt, LastStatus, LastMessage)
VALUES ('region', 1, 6, '2026-10-20T07:58:45', 'ok', 'Loaded by hand from indonesia-region (Kepmendagri 300.2.2-2138 Tahun 2025).');
INSERT OR IGNORE INTO DataSyncSetting (Source, Enabled, IntervalMonths) VALUES ('endemicity', 1, 3);

-- Malaria endemicity per regency, labels exactly as the Kemenkes map publishes them ('Eliminasi',
-- 'Endemis Rendah', 'Endemis Sedang', 'Endemis Tinggi I', 'II', 'III'). RegencyCode, RegencyName and
-- ProvinceName are medpost's own (the Region table), found by name because Kemenkes uses different
-- codes for some regencies. DataYear is the map year the row came from: 2024 for regencies the 2025
-- map does not have yet. Cases and Population are the map's own numbers (not validated by Kemenkes).
CREATE TABLE IF NOT EXISTS MalariaEndemicity (
    RegencyCode     TEXT PRIMARY KEY,
    RegencyName     TEXT NOT NULL,
    ProvinceName    TEXT NOT NULL,
    Label           TEXT NOT NULL,
    DataYear        INTEGER NOT NULL,
    Cases           INTEGER NOT NULL DEFAULT 0,
    Population      INTEGER NOT NULL DEFAULT 0,
    SourceUpdatedAt TEXT NOT NULL DEFAULT ''
);
-- The last scrape, waiting for an admin to apply or discard it. Same columns.
CREATE TABLE IF NOT EXISTS MalariaEndemicityStaged (
    RegencyCode     TEXT PRIMARY KEY,
    RegencyName     TEXT NOT NULL,
    ProvinceName    TEXT NOT NULL,
    Label           TEXT NOT NULL,
    DataYear        INTEGER NOT NULL,
    Cases           INTEGER NOT NULL DEFAULT 0,
    Population      INTEGER NOT NULL DEFAULT 0,
    SourceUpdatedAt TEXT NOT NULL DEFAULT ''
);
