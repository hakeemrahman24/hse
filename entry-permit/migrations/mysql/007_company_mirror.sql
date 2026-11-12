-- 007 company mirror (MariaDB). Keep in step with migrations/sqlite/007_company_mirror.sql.
-- A read-only copy of hse-medpost's company list (plan 4.3), replaced by the sync.
CREATE TABLE company (
    company_id   BIGINT       NOT NULL PRIMARY KEY,
    name         VARCHAR(160) NOT NULL,
    abbreviation VARCHAR(40)  NOT NULL DEFAULT '',
    entity       VARCHAR(60)  NOT NULL DEFAULT '',
    gov          INTEGER      NOT NULL DEFAULT 0,
    archived     INTEGER      NOT NULL DEFAULT 0,
    synced_at    CHAR(19)     NOT NULL
);

CREATE INDEX idx_company_name ON company (name);

CREATE TABLE company_parent (
    company_id  BIGINT       NOT NULL,
    depth       INTEGER      NOT NULL,
    parent_name VARCHAR(160) NOT NULL,
    PRIMARY KEY (company_id, depth, parent_name)
);

CREATE TABLE company_sync (
    id    INTEGER  NOT NULL PRIMARY KEY,
    at    CHAR(19) NOT NULL,
    total INTEGER  NOT NULL
);
