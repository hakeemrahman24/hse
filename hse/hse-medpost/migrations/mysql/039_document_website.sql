-- Lets DocumentTable.DocumentsType hold 'Website' (MariaDB). SQLite does the same on its own at
-- startup (activitylog.EnsureWebsiteDocumentType); when DB_DRIVER=mysql the app skips migrations/,
-- so this runs by hand, once, against the production database, BEFORE deploying the build that
-- offers the Website type. Take a mysqldump backup first. Safe to run twice.
-- Check the current definition first:  SHOW CREATE TABLE DocumentTable;
-- If the column is not NOT NULL, or the list differs, adjust the line below to match.
ALTER TABLE DocumentTable
    MODIFY DocumentsType ENUM('Procedure','SocializationMaterial','Forms','Template','Website') NOT NULL;
