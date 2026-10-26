-- Evidence on observations: "before" (evidence) and "after" (action done), each with up to 4
-- photos and up to 2 PDF documents. Run by hand, after 008:
--   mysql hse_register < migrations/009_observation_files.sql
--
-- A photo is a JPEG of at most 500 KB; a PDF is at most 5 MB. The files live in the evidence
-- folder (EVIDENCE_DIR); this table only holds their random names. The limits per kind are enforced
-- by the program (photos.go, documents.go). ON DELETE CASCADE removes the rows if an observation is
-- deleted; the staff delete screen also removes the files, because the database cannot.

-- Databases that began as the old Django app have a signed id here; the foreign key below needs it
-- to match observation_files.observation_id. Re-running on an already unsigned id changes nothing.
ALTER TABLE observations_observation MODIFY id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT;

CREATE TABLE IF NOT EXISTS observation_files (
    id             BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    observation_id BIGINT UNSIGNED NOT NULL,
    kind           ENUM('before','after') NOT NULL DEFAULT 'before',
    file_type      ENUM('photo','pdf') NOT NULL DEFAULT 'photo',
    stored_name    VARCHAR(64)  NOT NULL,
    display_name   VARCHAR(200) NULL,            -- original name of a PDF, for the download label
    size_bytes     INT UNSIGNED NOT NULL,
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uq_file_name (stored_name),
    KEY idx_file_obs (observation_id, kind, file_type),
    CONSTRAINT fk_file_obs FOREIGN KEY (observation_id)
        REFERENCES observations_observation (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
