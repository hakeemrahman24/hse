-- SofiPreview (MariaDB). Same shape as migrations/sqlite/024_sofi_preview.sql.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/ entirely, so this runs
-- by hand, once, against the production database. Take a mysqldump backup first. Safe to run
-- twice (IF NOT EXISTS).
--
-- Run it BEFORE deploying the build with SOFI preview. Without it the preview button answers
-- "Could not change preview" and /sofi simply stays closed outside a window; nothing else breaks.

CREATE TABLE IF NOT EXISTS SofiPreview (
    PreviewID INT PRIMARY KEY,
    UntilAt   VARCHAR(32) NOT NULL,
    SetBy     VARCHAR(150) NOT NULL DEFAULT '',
    SetAt     VARCHAR(32) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
