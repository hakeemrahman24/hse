-- 009 position lookup (plan 3b): four tables, loaded once from seed/ at start-up when empty.
-- seq keeps the sheet's row order, because the order of keyword and class rules decides the winner.
CREATE TABLE position_canonical (
    name          VARCHAR(120) NOT NULL PRIMARY KEY,
    category      VARCHAR(80)  NOT NULL DEFAULT '',
    risk_tier     VARCHAR(20)  NOT NULL DEFAULT '',
    certificate   VARCHAR(200) NOT NULL DEFAULT '',
    cert_required VARCHAR(4)   NOT NULL DEFAULT 'N',
    seq           INTEGER      NOT NULL
);

CREATE TABLE position_alias (
    seq       INTEGER      NOT NULL PRIMARY KEY,
    alias     VARCHAR(160) NOT NULL,
    canonical VARCHAR(120) NOT NULL
);

CREATE TABLE position_keyword (
    seq       INTEGER      NOT NULL PRIMARY KEY,
    keywords  VARCHAR(400) NOT NULL,
    canonical VARCHAR(120) NOT NULL,
    risk_tier VARCHAR(20)  NOT NULL DEFAULT ''
);

CREATE TABLE position_class (
    seq            INTEGER      NOT NULL PRIMARY KEY,
    keywords       VARCHAR(400) NOT NULL,
    classification VARCHAR(40)  NOT NULL,
    risk_tier      VARCHAR(20)  NOT NULL DEFAULT ''
);
