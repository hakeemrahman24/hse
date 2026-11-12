-- 009 position lookup (MariaDB; keep in step with sqlite/009_position.sql) (plan 3b): four tables, loaded once from seed/ at start-up when empty.
-- seq keeps the sheet's row order, because the order of keyword and class rules decides the winner.
CREATE TABLE position_canonical (
    name          VARCHAR(120) NOT NULL PRIMARY KEY,
    category      VARCHAR(80)  NOT NULL DEFAULT '',
    risk_tier     VARCHAR(20)  NOT NULL DEFAULT '',
    certificate   VARCHAR(200) NOT NULL DEFAULT '',
    cert_required VARCHAR(4)   NOT NULL DEFAULT 'N',
    seq           INT          NOT NULL
);

CREATE TABLE position_alias (
    seq       INT          NOT NULL PRIMARY KEY,
    alias     VARCHAR(160) NOT NULL,
    canonical VARCHAR(120) NOT NULL
);

CREATE TABLE position_keyword (
    seq       INT          NOT NULL PRIMARY KEY,
    keywords  VARCHAR(400) NOT NULL,
    canonical VARCHAR(120) NOT NULL,
    risk_tier VARCHAR(20)  NOT NULL DEFAULT ''
);

CREATE TABLE position_class (
    seq            INT          NOT NULL PRIMARY KEY,
    keywords       VARCHAR(400) NOT NULL,
    classification VARCHAR(40)  NOT NULL,
    risk_tier      VARCHAR(20)  NOT NULL DEFAULT ''
);
