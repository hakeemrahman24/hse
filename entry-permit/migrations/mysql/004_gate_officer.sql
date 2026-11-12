-- 004 gate officers (MariaDB). Keep in step with migrations/sqlite/004_gate_officer.sql.
CREATE TABLE gate_officer (
    id         BIGINT AUTO_INCREMENT PRIMARY KEY,
    name       VARCHAR(120) NOT NULL,
    code_hash  VARCHAR(200) NOT NULL,
    status     VARCHAR(10)  NOT NULL DEFAULT 'Active',
    created_at CHAR(19)     NOT NULL,
    updated_at CHAR(19)     NOT NULL,
    UNIQUE KEY uq_gate_officer_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
