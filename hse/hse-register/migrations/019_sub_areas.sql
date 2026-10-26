-- The second level of the area list: sub-areas under an area (Camp -> Mess hall, Dormitory A).
-- Administrators edit them in Settings > Areas (Manage next to an area). The SOC wizard asks for the
-- sub-area only when the chosen area has some. Run by hand after 018, before deploying:
--   mysql hse_register < migrations/019_sub_areas.sql
--
-- Like the area, a report keeps the sub-area as text (observations_observation.sub_area), so renaming or
-- deleting a sub-area in Settings never breaks an old report: a rename also renames it on the reports
-- that carry it, a delete leaves them as they are. Deleting an area deletes its sub-areas from the list.

CREATE TABLE IF NOT EXISTS ref_subarea (
    id      INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    area_id INT UNSIGNED NOT NULL,
    name    VARCHAR(100) NOT NULL,
    UNIQUE KEY uq_subarea (area_id, name),
    CONSTRAINT fk_subarea_area FOREIGN KEY (area_id) REFERENCES ref_area (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

ALTER TABLE observations_observation
    ADD COLUMN IF NOT EXISTS sub_area VARCHAR(100) NULL AFTER location;
