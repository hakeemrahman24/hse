-- 015 gate sync (MariaDB). Keep in step with migrations/sqlite/015_gate_sync.sql.
-- Run by hand after a mysqldump. Moments are CHAR(19) 'yyyy-mm-dd hh:mm:ss' (Jakarta).
CREATE TABLE gate_log (
    id          BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
    worker_id   BIGINT       NOT NULL,
    at          CHAR(19)     NOT NULL,
    lep_no      VARCHAR(12)  NOT NULL,
    worker_name VARCHAR(120) NOT NULL DEFAULT '',
    company     VARCHAR(160) NOT NULL DEFAULT '',
    action      VARCHAR(10)  NOT NULL,
    reason      VARCHAR(255) NOT NULL DEFAULT '',
    officer     VARCHAR(120) NOT NULL DEFAULT '',
    session_id  VARCHAR(40)  NOT NULL DEFAULT '',
    day         CHAR(10)     NOT NULL,
    gate        VARCHAR(80)  NOT NULL DEFAULT '',
    received_at CHAR(19)     NOT NULL,
    UNIQUE KEY uq_gate_log_worker (worker_id)
);
CREATE INDEX idx_gate_log_lep ON gate_log (lep_no, at);

CREATE TABLE gate_state (
    lep_no            VARCHAR(12)  NOT NULL PRIMARY KEY,
    status            VARCHAR(10)  NOT NULL,
    last_action       VARCHAR(10)  NOT NULL DEFAULT '',
    last_action_at    CHAR(19)     NOT NULL DEFAULT '',
    officer           VARCHAR(120) NOT NULL DEFAULT '',
    gate              VARCHAR(80)  NOT NULL DEFAULT '',
    first_checkin     CHAR(19)     NOT NULL DEFAULT '',
    last_checkout     CHAR(19)     NOT NULL DEFAULT '',
    reason            VARCHAR(255) NOT NULL DEFAULT '',
    worker_updated_at VARCHAR(30)  NOT NULL,
    received_at       CHAR(19)     NOT NULL
);

CREATE TABLE gate_push (
    item_key  VARCHAR(40) NOT NULL PRIMARY KEY,
    hash      CHAR(64)    NOT NULL,
    pushed_at CHAR(19)    NOT NULL
);

CREATE TABLE gate_sync_run (
    id     BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
    job    VARCHAR(40)  NOT NULL,
    ran_at CHAR(19)     NOT NULL,
    ok     INTEGER      NOT NULL,
    detail VARCHAR(500) NOT NULL DEFAULT ''
);
CREATE INDEX idx_gate_sync_run ON gate_sync_run (job, ran_at);
