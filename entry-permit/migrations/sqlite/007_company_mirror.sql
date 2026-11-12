-- 007 company mirror: a read-only copy of hse-medpost's company list (plan 4.3).
-- Nothing here is edited in this app; the nightly sync (or the admin's
-- "Refresh now") replaces the rows. medpost's CompanyID is kept as the key.
CREATE TABLE company (
    company_id   BIGINT       NOT NULL PRIMARY KEY,
    name         VARCHAR(160) NOT NULL,
    abbreviation VARCHAR(40)  NOT NULL DEFAULT '',
    entity       VARCHAR(60)  NOT NULL DEFAULT '',
    gov          INTEGER      NOT NULL DEFAULT 0,   -- 1 = government office (waiver form)
    archived     INTEGER      NOT NULL DEFAULT 0,   -- hidden from pickers, kept for old permits
    synced_at    CHAR(19)     NOT NULL
);

CREATE INDEX idx_company_name ON company (name);

-- Parents nearest first (depth 1 is the direct parent).
CREATE TABLE company_parent (
    company_id  BIGINT       NOT NULL,
    depth       INTEGER      NOT NULL,
    parent_name VARCHAR(160) NOT NULL,
    PRIMARY KEY (company_id, depth, parent_name)
);

-- One row (id 1): when the mirror was last refreshed and how many companies it holds.
CREATE TABLE company_sync (
    id    INTEGER  NOT NULL PRIMARY KEY,
    at    CHAR(19) NOT NULL,
    total INTEGER  NOT NULL
);
