-- The type under a source (Internal Audit -> Site Audit / HO Audit / Corporate Audit,
-- External Audit -> 3rd Party Company / Government) becomes a list administrators manage in
-- Settings > Source types. Run by hand after 012, before starting this build:
--   mysql hse_register < migrations/013_source_details.sql
--
-- "KL Audit" (the old third Internal Audit type) is kept but archived, so observations that
-- already carry it stay valid; new posts choose Corporate Audit instead. An administrator can
-- restore it or rename it in Settings.

CREATE TABLE IF NOT EXISTS ref_source_detail (
    id          INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    source_id   INT UNSIGNED NOT NULL,
    name        VARCHAR(80) NOT NULL,
    sort_order  SMALLINT UNSIGNED NOT NULL,
    is_archived TINYINT(1) NOT NULL DEFAULT 0,
    UNIQUE KEY uq_source_detail (source_id, name),
    CONSTRAINT fk_detail_source FOREIGN KEY (source_id) REFERENCES ref_source (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT IGNORE INTO ref_source_detail (source_id, name, sort_order, is_archived)
SELECT s.id, t.name, t.sort_order, t.is_archived
FROM ref_source s
JOIN (
    SELECT 'Internal Audit' AS source, 'Site Audit' AS name, 1 AS sort_order, 0 AS is_archived
    UNION ALL SELECT 'Internal Audit', 'HO Audit', 2, 0
    UNION ALL SELECT 'Internal Audit', 'Corporate Audit', 3, 0
    UNION ALL SELECT 'Internal Audit', 'KL Audit', 4, 1
    UNION ALL SELECT 'External Audit', '3rd Party Company', 1, 0
    UNION ALL SELECT 'External Audit', 'Government', 2, 0
) t ON t.source = s.name;

-- The column was an ENUM of five fixed names; text keeps every stored value.
ALTER TABLE observations_observation MODIFY COLUMN source_detail VARCHAR(80) NULL;
