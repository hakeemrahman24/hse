-- 004 gate officers (SQLite). Keep in step with migrations/mysql/004_gate_officer.sql.
-- The code is stored only as a salted PBKDF2-SHA256 verifier (format in
-- internal/gateofficer), the same form the gate Worker will be sent (plan 4.2).
CREATE TABLE gate_officer (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       VARCHAR(120) NOT NULL UNIQUE,
    code_hash  VARCHAR(200) NOT NULL,
    status     VARCHAR(10)  NOT NULL DEFAULT 'Active',
    created_at CHAR(19)     NOT NULL,
    updated_at CHAR(19)     NOT NULL
);
