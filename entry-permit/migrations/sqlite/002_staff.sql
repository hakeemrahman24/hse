-- 002 staff accounts and roles (SQLite). Keep in step with migrations/mysql/002_staff.sql.
-- The PIN is stored only as a bcrypt hash. Moments are CHAR(19) Jakarta text.

CREATE TABLE staff_user (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    username     VARCHAR(40)  NOT NULL UNIQUE,   -- lower-case login key
    display_name VARCHAR(120) NOT NULL,
    email        VARCHAR(160) NOT NULL DEFAULT '',
    pin_hash     VARCHAR(100) NOT NULL,
    active       INTEGER      NOT NULL DEFAULT 1,
    failed_count INTEGER      NOT NULL DEFAULT 0,
    locked_until CHAR(19)     NOT NULL DEFAULT '',
    created_at   CHAR(19)     NOT NULL,
    updated_at   CHAR(19)     NOT NULL
);

-- One person can hold several roles: admin, doctor, stage1, final, dashboard, gate_officer.
CREATE TABLE staff_user_role (
    user_id INTEGER     NOT NULL,
    role    VARCHAR(20) NOT NULL,
    PRIMARY KEY (user_id, role),
    FOREIGN KEY (user_id) REFERENCES staff_user (id) ON DELETE CASCADE
);
