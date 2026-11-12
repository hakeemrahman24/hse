-- 003 reference list items (MariaDB). Keep in step with migrations/sqlite/003_ref_item.sql.
-- See the SQLite file for the reasoning (one generic table, definitions in Go).

CREATE TABLE ref_item (
    id         BIGINT AUTO_INCREMENT PRIMARY KEY,
    list_key   VARCHAR(30)  NOT NULL,
    item_key   VARCHAR(255) NOT NULL,
    c1         VARCHAR(160) NOT NULL DEFAULT '',
    c2         VARCHAR(160) NOT NULL DEFAULT '',
    c3         VARCHAR(160) NOT NULL DEFAULT '',
    c4         VARCHAR(160) NOT NULL DEFAULT '',
    status     VARCHAR(10)  NOT NULL DEFAULT 'Active',
    sort_order INT          NOT NULL DEFAULT 0,
    created_at CHAR(19)     NOT NULL,
    updated_at CHAR(19)     NOT NULL,
    UNIQUE KEY uq_ref_item_key (list_key, item_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
