-- 002 staff accounts and roles (MariaDB). Keep in step with migrations/sqlite/002_staff.sql.
-- The PIN is stored only as a bcrypt hash. Moments are CHAR(19) Jakarta text.

CREATE TABLE staff_user (
    id           BIGINT AUTO_INCREMENT PRIMARY KEY,
    username     VARCHAR(40)  NOT NULL,
    display_name VARCHAR(120) NOT NULL,
    email        VARCHAR(160) NOT NULL DEFAULT '',
    pin_hash     VARCHAR(100) NOT NULL,
    active       TINYINT(1)   NOT NULL DEFAULT 1,
    failed_count INT          NOT NULL DEFAULT 0,
    locked_until CHAR(19)     NOT NULL DEFAULT '',
    created_at   CHAR(19)     NOT NULL,
    updated_at   CHAR(19)     NOT NULL,
    UNIQUE KEY uq_staff_user_username (username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE staff_user_role (
    user_id BIGINT      NOT NULL,
    role    VARCHAR(20) NOT NULL,
    PRIMARY KEY (user_id, role),
    CONSTRAINT fk_staff_user_role_user FOREIGN KEY (user_id) REFERENCES staff_user (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
