-- SofiDimension (MariaDB). Same shape as migrations/sqlite/026_sofi_dimensions.sql.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/ entirely, so this runs
-- by hand, once, against the production database. Take a mysqldump backup first. Safe to run
-- twice (IF NOT EXISTS). Run 040 first, then 041 (SofiItem has a foreign key to this table).
--
-- The table is created empty: the server fills it with the five dimensions when it starts, so
-- nothing needs to be inserted by hand. Run both files BEFORE deploying the build with the
-- questionnaire editor; without them the page shows the built-in wording and saving answers
-- "Could not save", and /sofi itself keeps working.

CREATE TABLE IF NOT EXISTS SofiDimension (
    DimensionKey VARCHAR(40) PRIMARY KEY,
    SortOrder    INT NOT NULL,
    NameEN       VARCHAR(200) NOT NULL,
    NameID       VARCHAR(200) NOT NULL,
    NameZH       VARCHAR(200) NOT NULL,
    UpdatedBy    VARCHAR(150) NOT NULL DEFAULT '',
    UpdatedAt    VARCHAR(32) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
