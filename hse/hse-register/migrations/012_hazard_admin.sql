-- Lets administrators manage hazard classes and categories in Settings: add, rename, archive,
-- restore and delete. Run by hand, after 011, before starting this build:
--   mysql hse_register < migrations/012_hazard_admin.sql
--
-- A hazard class used to exist only as text on each category. It now has its own table, filled
-- from the classes already in use (numbered in the order they first appear). Categories keep the
-- class name as text, so observations and the seed files are unchanged.
-- Archived classes and categories stay on the observations that already carry them and in the
-- sidebar counts, but can no longer be chosen for new ones.

CREATE TABLE IF NOT EXISTS ref_hazard_class (
    id          INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    name        VARCHAR(40) NOT NULL,
    sort_order  SMALLINT UNSIGNED NOT NULL,
    is_archived TINYINT(1) NOT NULL DEFAULT 0,
    UNIQUE KEY uq_hazard_class_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT IGNORE INTO ref_hazard_class (name, sort_order)
SELECT hazard_class, ROW_NUMBER() OVER (ORDER BY MIN(id)) FROM ref_hazard_category GROUP BY hazard_class;

ALTER TABLE ref_hazard_category
    ADD COLUMN IF NOT EXISTS is_archived TINYINT(1) NOT NULL DEFAULT 0;

-- Sources (Internal Audit, External Audit, ...) become a list administrators can extend. The
-- four existing ones are loaded here; the program refuses to rename or delete them (the dashboard
-- and the audit "Type" choices rely on their names) but they can be archived. "SOC" is not in the
-- list: it is set only by the public form.
CREATE TABLE IF NOT EXISTS ref_source (
    id          INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    name        VARCHAR(80) NOT NULL,
    sort_order  SMALLINT UNSIGNED NOT NULL,
    is_archived TINYINT(1) NOT NULL DEFAULT 0,
    UNIQUE KEY uq_source_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT IGNORE INTO ref_source (name, sort_order) VALUES
    ('Internal Audit', 1), ('External Audit', 2), ('Management Walkthrough', 3),
    ('Project Monitoring Action Report', 4);

-- The column was an ENUM of the five fixed names; a text column keeps every stored value and
-- lets new sources be saved.
ALTER TABLE observations_observation MODIFY COLUMN source VARCHAR(80) NULL;
