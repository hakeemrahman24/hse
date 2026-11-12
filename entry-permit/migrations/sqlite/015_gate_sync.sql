-- 015 gate sync (slice 5b): what the Pi keeps of the gate, and how it tracks what it sent.
-- Moments are CHAR(19) 'yyyy-mm-dd hh:mm:ss' (Jakarta); the Worker's own times are kept as sent.

-- Every scan the gate made. worker_id is the Worker's row id: UNIQUE, so applying the same
-- pulled row twice (a lost ack) can never duplicate a scan.
CREATE TABLE gate_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    worker_id   BIGINT       NOT NULL UNIQUE,
    at          CHAR(19)     NOT NULL,
    lep_no      VARCHAR(12)  NOT NULL,
    worker_name VARCHAR(120) NOT NULL DEFAULT '',
    company     VARCHAR(160) NOT NULL DEFAULT '',
    action      VARCHAR(10)  NOT NULL,               -- checkin / checkout / force
    reason      VARCHAR(255) NOT NULL DEFAULT '',
    officer     VARCHAR(120) NOT NULL DEFAULT '',
    session_id  VARCHAR(40)  NOT NULL DEFAULT '',
    day         CHAR(10)     NOT NULL,               -- Jakarta day of the scan
    gate        VARCHAR(80)  NOT NULL DEFAULT '',
    received_at CHAR(19)     NOT NULL
);
CREATE INDEX idx_gate_log_lep ON gate_log (lep_no, at);

-- One row per permit: where the worker is now. The newest Worker update wins.
CREATE TABLE gate_state (
    lep_no            VARCHAR(12)  NOT NULL PRIMARY KEY,
    status            VARCHAR(10)  NOT NULL,         -- INSIDE / OUTSIDE / OVERDUE
    last_action       VARCHAR(10)  NOT NULL DEFAULT '',
    last_action_at    CHAR(19)     NOT NULL DEFAULT '',
    officer           VARCHAR(120) NOT NULL DEFAULT '',
    gate              VARCHAR(80)  NOT NULL DEFAULT '',
    first_checkin     CHAR(19)     NOT NULL DEFAULT '',
    last_checkout     CHAR(19)     NOT NULL DEFAULT '',
    reason            VARCHAR(255) NOT NULL DEFAULT '',
    worker_updated_at VARCHAR(30)  NOT NULL,         -- the Worker's ISO time, compared as text
    received_at       CHAR(19)     NOT NULL
);

-- What was last sent to the Worker, by a hash of its content, so only changes are sent.
CREATE TABLE gate_push (
    item_key  VARCHAR(40) NOT NULL PRIMARY KEY,      -- permit:LEP-x, ticket:TKT-x, deact:LEP-x, refs
    hash      CHAR(64)    NOT NULL,
    pushed_at CHAR(19)    NOT NULL
);

-- One row per gate sync run, so the admin page can show the last success and warn when it is old.
-- (The existing job_run table is one row per job per DAY, which suits the daily jobs, not a 5-minute sync.)
CREATE TABLE gate_sync_run (
    id     INTEGER PRIMARY KEY AUTOINCREMENT,
    job    VARCHAR(40)  NOT NULL,
    ran_at CHAR(19)     NOT NULL,
    ok     INTEGER      NOT NULL,
    detail VARCHAR(500) NOT NULL DEFAULT ''
);
CREATE INDEX idx_gate_sync_run ON gate_sync_run (job, ran_at);
