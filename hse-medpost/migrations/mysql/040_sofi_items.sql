-- SofiItem (MariaDB). Same shape as migrations/sqlite/026_sofi_items.sql.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/ entirely, so this runs
-- by hand, once, against the production database. Take a mysqldump backup first. Safe to run
-- twice (IF NOT EXISTS).
--
-- Run it BEFORE deploying the build with the SOFI questionnaire editor. Without it the editor
-- shows the built-in wording and answers "Could not save" when an item is changed; /sofi itself
-- keeps working with the built-in wording.

CREATE TABLE IF NOT EXISTS SofiItem (
    ItemNumber INT PRIMARY KEY,
    TextEN     VARCHAR(400) NOT NULL DEFAULT '',
    TextID     VARCHAR(400) NOT NULL DEFAULT '',
    TextZH     VARCHAR(400) NOT NULL DEFAULT '',
    UpdatedBy  VARCHAR(150) NOT NULL DEFAULT '',
    UpdatedAt  VARCHAR(32) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
