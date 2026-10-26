-- MalariaScreening gets its own key (MariaDB), so one visit can hold more than one
-- malaria test (an RDT, then a smear). Same shape as migrations/sqlite/014_visit_malaria.sql.
--
-- Not loaded by cmd/server: when DB_DRIVER=mysql the app skips migrations/
-- entirely, so this runs by hand, once, against the production database, BEFORE
-- deploying the build that reads MalariaID. Take a mysqldump backup first.
-- Run 023_malaria_purpose.sql before this one.
-- NOT safe to run twice: the second run fails at the first statement (no
-- fk_malaria_visit any more), which changes nothing.
--
-- Every saved test keeps its number: its MalariaID is set to its old FK_VisitID,
-- so Test IDs (MAL-00008) do not change.

ALTER TABLE MalariaScreening DROP FOREIGN KEY fk_malaria_visit;

ALTER TABLE MalariaScreening
    DROP PRIMARY KEY,
    ADD COLUMN MalariaID INT NOT NULL FIRST;

UPDATE MalariaScreening SET MalariaID = FK_VisitID;

ALTER TABLE MalariaScreening
    MODIFY MalariaID INT NOT NULL AUTO_INCREMENT,
    ADD PRIMARY KEY (MalariaID),
    ADD KEY idx_malaria_visit (FK_VisitID),
    ADD CONSTRAINT fk_malaria_visit FOREIGN KEY (FK_VisitID) REFERENCES Visit(VisitID);
