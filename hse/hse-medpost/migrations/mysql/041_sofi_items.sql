-- SofiItem (MariaDB). Same shape as migrations/sqlite/027_sofi_items.sql. Run 040_sofi_dimensions.sql
-- first: this table has a foreign key to it.
--
-- Not loaded by cmd/server (see 040). Take a mysqldump backup first. Safe to run twice
-- (IF NOT EXISTS). Created empty: the server fills it with the 20 items when it starts.

CREATE TABLE IF NOT EXISTS SofiItem (
    ItemNumber   INT PRIMARY KEY,
    DimensionKey VARCHAR(40) NOT NULL,
    TextEN       VARCHAR(400) NOT NULL,
    TextID       VARCHAR(400) NOT NULL,
    TextZH       VARCHAR(400) NOT NULL,
    UpdatedBy    VARCHAR(150) NOT NULL DEFAULT '',
    UpdatedAt    VARCHAR(32) NOT NULL,
    CONSTRAINT fk_sofiitem_dimension FOREIGN KEY (DimensionKey) REFERENCES SofiDimension (DimensionKey)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
