-- Adds Company.Archived so a company can be hidden from pickers without
-- deleting it. Existing companies stay active (0).
--
-- Not loaded by cmd/server when DB_DRIVER=mysql: run by hand, once, against
-- the production database. Take a mysqldump backup first.

ALTER TABLE Company ADD COLUMN Archived TINYINT(1) NOT NULL DEFAULT 0;
