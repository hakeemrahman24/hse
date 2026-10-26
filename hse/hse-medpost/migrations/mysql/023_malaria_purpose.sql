-- Screening purpose for MalariaScreening (MariaDB). Same shape as the purpose
-- table in migrations/sqlite/014_visit_malaria.sql.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/
-- entirely, so this runs by hand, once, against the production database, BEFORE
-- deploying the build that sends FK_PurposeID. Take a mysqldump backup first.
-- Safe to run twice. Rows already saved become 1 (On arrival).

CREATE TABLE IF NOT EXISTS MalariaPurposeTable (
    PurposeID   TINYINT UNSIGNED NOT NULL PRIMARY KEY,
    PurposeName VARCHAR(30) NOT NULL,
    UNIQUE KEY uq_malaria_purpose_name (PurposeName)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT IGNORE INTO MalariaPurposeTable (PurposeID, PurposeName) VALUES (1, 'On arrival'), (2, 'Symptomatic');

ALTER TABLE MalariaScreening
    ADD COLUMN IF NOT EXISTS FK_PurposeID TINYINT UNSIGNED NOT NULL DEFAULT 1 AFTER FK_VisitID;

-- MariaDB has no ADD CONSTRAINT IF NOT EXISTS; this errors with 1826 (duplicate
-- key name) on a second run, which is harmless.
ALTER TABLE MalariaScreening
    ADD CONSTRAINT fk_malaria_purpose FOREIGN KEY (FK_PurposeID) REFERENCES MalariaPurposeTable(PurposeID);
