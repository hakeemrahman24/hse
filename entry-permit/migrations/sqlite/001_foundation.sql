-- 001 foundation: typed settings, audit log, job runs.
-- Days are CHAR(10) 'yyyy-mm-dd', moments CHAR(19) 'yyyy-mm-dd hh:mm:ss',
-- both Jakarta wall-clock text (plan section 2b). No DATETIME columns.

CREATE TABLE setting (
    setting_key   VARCHAR(80)  NOT NULL PRIMARY KEY,
    grp           VARCHAR(40)  NOT NULL,
    label         VARCHAR(200) NOT NULL,
    value_type    VARCHAR(10)  NOT NULL,  -- int, bool, text, color, hour
    unit          VARCHAR(20)  NOT NULL DEFAULT '',
    value         VARCHAR(255) NOT NULL,
    default_value VARCHAR(255) NOT NULL,
    min_value     VARCHAR(40)  NOT NULL DEFAULT '',
    max_value     VARCHAR(40)  NOT NULL DEFAULT '',
    description   VARCHAR(500) NOT NULL DEFAULT '',
    updated_at    CHAR(19)     NOT NULL DEFAULT '',
    updated_by    VARCHAR(80)  NOT NULL DEFAULT ''
);

CREATE TABLE audit_log (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    at         CHAR(19)     NOT NULL,
    actor      VARCHAR(80)  NOT NULL,
    action     VARCHAR(40)  NOT NULL,
    target     VARCHAR(120) NOT NULL,
    old_value  TEXT         NOT NULL DEFAULT '',
    new_value  TEXT         NOT NULL DEFAULT ''
);

CREATE INDEX idx_audit_log_target ON audit_log (target, at);

-- One row per job per day: a restart cannot send a reminder twice.
CREATE TABLE job_run (
    job_name VARCHAR(60) NOT NULL,
    run_day  CHAR(10)    NOT NULL,
    result   VARCHAR(20) NOT NULL,
    at       CHAR(19)    NOT NULL,
    PRIMARY KEY (job_name, run_day)
);
