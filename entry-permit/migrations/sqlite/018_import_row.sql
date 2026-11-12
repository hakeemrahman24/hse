-- 018 what the sheet importer has stored, so a second run can tell an unchanged row from a
-- changed one, and never overwrites a permit the new system has changed since.
--   hash  fingerprint of the sheet row's content
--   state fingerprint of the stored permit right after the import (status, LEP number, number of review steps)
CREATE TABLE import_row (
    source    VARCHAR(20) NOT NULL,   -- wep, waiver, ticket ...
    source_id VARCHAR(40) NOT NULL,   -- the request id in the sheet
    hash      CHAR(64)    NOT NULL,
    state     VARCHAR(120) NOT NULL,
    at        CHAR(19)    NOT NULL,
    PRIMARY KEY (source, source_id)
);
