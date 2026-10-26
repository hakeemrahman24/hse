-- Region: Indonesia's administrative areas, one row per area (MariaDB flavour,
-- for production). The SQLite version is region.sqlite.sql; read its header for
-- what Code, Level and ParentCode mean.
--
-- Collation matches every other table in the live database
-- (utf8mb4_unicode_ci). Safe to run more than once.

CREATE TABLE IF NOT EXISTS Region (
    Code       VARCHAR(13)      NOT NULL,
    Name       VARCHAR(100)     NOT NULL,
    Level      TINYINT UNSIGNED NOT NULL,
    ParentCode VARCHAR(13)      NULL,
    PRIMARY KEY (Code),
    KEY idx_region_parent (ParentCode),
    CONSTRAINT chk_region_level CHECK (Level BETWEEN 1 AND 4)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
