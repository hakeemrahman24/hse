-- 018 sheet importer record (MariaDB). Keep in step with migrations/sqlite/018_import_row.sql.
-- Run by hand after a mysqldump.
CREATE TABLE import_row (
    source    VARCHAR(20)  NOT NULL,
    source_id VARCHAR(40)  NOT NULL,
    hash      CHAR(64)     NOT NULL,
    state     VARCHAR(120) NOT NULL,
    at        CHAR(19)     NOT NULL,
    PRIMARY KEY (source, source_id)
);
