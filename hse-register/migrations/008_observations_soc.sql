-- Brings the observation table to the new field list (plan section 5.2): one table for the HSE
-- crew's entries and the public SOC form. Run by hand, after 007:
--   mysql hse_register < migrations/008_observations_soc.sql
--
-- Existing rows keep their data:
--   * location stays and now holds the Area.
--   * source / verification stay NULL for old rows ("not recorded"); new rows always have them.
--   * category is mapped to the three new values (see the UPDATE below).
--   * severity is dropped (no longer part of the form).
-- DOB is deliberately NOT stored anywhere (plan section 7.1).

-- Old category values -> Unsafe Action / Unsafe Condition / Positive Finding.
-- near_miss and suggestion have no exact match; they become unsafe_condition and HSE can correct them.
UPDATE observations_observation SET category = CASE category
    WHEN 'unsafe_act'       THEN 'unsafe_action'
    WHEN 'unsafe_action'    THEN 'unsafe_action'
    WHEN 'safe_action'      THEN 'positive_finding'
    WHEN 'safe_condition'   THEN 'positive_finding'
    WHEN 'good_practice'    THEN 'positive_finding'
    ELSE 'unsafe_condition'
END;

ALTER TABLE observations_observation
  DROP COLUMN severity,
  MODIFY COLUMN category ENUM('unsafe_action','unsafe_condition','positive_finding') NOT NULL,
  ADD COLUMN source ENUM('SOC','Internal Audit','External Audit','Management Walkthrough','Project Monitoring Action Report') NULL AFTER id,
  ADD COLUMN source_detail ENUM('Site Audit','HO Audit','KL Audit','3rd Party Company','Government') NULL AFTER source,
  ADD COLUMN employee_id VARCHAR(30)  NULL AFTER observer_name,
  ADD COLUMN designation VARCHAR(100) NULL AFTER employee_id,
  ADD COLUMN company VARCHAR(255) NULL AFTER designation,
  ADD COLUMN action_taken   TEXT NULL AFTER description,
  ADD COLUMN recommendation TEXT NULL AFTER action_taken,
  ADD COLUMN topic_id  INT UNSIGNED NULL AFTER recommendation,
  ADD COLUMN hazard_id INT UNSIGNED NULL AFTER topic_id,
  ADD COLUMN responsible_company VARCHAR(255) NULL AFTER location,
  ADD COLUMN verification ENUM('Not yet','Verified') NULL AFTER status,
  ADD COLUMN created_by VARCHAR(100) NULL AFTER verification,   -- staff username that created the post (SOC posts: employee_id is the owner)
  ADD INDEX idx_obs_source (source),
  ADD INDEX idx_obs_employee (employee_id),
  ADD CONSTRAINT fk_obs_topic  FOREIGN KEY (topic_id)  REFERENCES ref_topic (id) ON DELETE SET NULL,
  ADD CONSTRAINT fk_obs_hazard FOREIGN KEY (hazard_id) REFERENCES ref_hazard_category (id) ON DELETE SET NULL;
