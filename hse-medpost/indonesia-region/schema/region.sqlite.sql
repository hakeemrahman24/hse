-- Region: Indonesia's administrative areas, one row per area (SQLite flavour,
-- for development and tests). The MariaDB version is region.mariadb.sql.
--
-- One table for all four levels, because a code says its own level and its
-- parent: "94" is a province, "94.01" a regency or city, "94.01.05" a district
-- and "94.01.05.2001" a village. Level and ParentCode are stored so a dropdown
-- can ask "the children of X" with an index instead of parsing codes.
--   Level 1 province, 2 regency/city (kabupaten/kota), 3 district (kecamatan),
--         4 village (desa/kelurahan)
--
-- Other tables should copy the Code and the Name into their own row rather
-- than point a foreign key here: the source data is updated over time, and a
-- record must keep the names it was written with.

CREATE TABLE IF NOT EXISTS Region (
    Code       TEXT    NOT NULL PRIMARY KEY,
    Name       TEXT    NOT NULL,
    Level      INTEGER NOT NULL CHECK (Level BETWEEN 1 AND 4),
    ParentCode TEXT
);

CREATE INDEX IF NOT EXISTS idx_region_parent ON Region (ParentCode);
