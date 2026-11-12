-- 003 reference list items (SQLite). Keep in step with migrations/mysql/003_ref_item.sql.
-- One table holds every small admin-editable list (nationality, ring, malaria
-- regions, purpose area, gate names, checkout reasons, notification emails).
-- The list definitions (column names and labels) live in Go (internal/reflist),
-- so adding a list needs no migration. c1..c4 are the list's columns in order.
-- item_key is the lower-cased key column(s), so duplicates are refused in SQL too.
-- Items are archived, never deleted: permits snapshot the text, but history must stay readable.

CREATE TABLE ref_item (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    list_key   VARCHAR(30)  NOT NULL,
    item_key   VARCHAR(255) NOT NULL,
    c1         VARCHAR(160) NOT NULL DEFAULT '',
    c2         VARCHAR(160) NOT NULL DEFAULT '',
    c3         VARCHAR(160) NOT NULL DEFAULT '',
    c4         VARCHAR(160) NOT NULL DEFAULT '',
    status     VARCHAR(10)  NOT NULL DEFAULT 'Active',  -- Active or Archived
    sort_order INTEGER      NOT NULL DEFAULT 0,
    created_at CHAR(19)     NOT NULL,
    updated_at CHAR(19)     NOT NULL,
    UNIQUE (list_key, item_key)
);
