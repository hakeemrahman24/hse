-- SOC form v8 wizard (SOC_Form_Spec_v1.1.md): the four paths A unsafe action, B unsafe condition,
-- C safe observation, D suggestion. Run by hand after 017, before deploying:
--   mysql hse_register < migrations/018_soc_wizard.sql
--
-- path             which of the four paths the report came from (NULL: staff post or sent before the wizard)
-- unsafe_reason    Path A: what was unsafe (the picklist wording at the time of sending)
-- ppe_item         Path A, when the reason was PPE: which item
-- stop_work        Path A: 1 = the work was stopped, 0 = not; stopped_by says who
-- immediate_danger / barricaded   Path B: yes/no answers (1/0)
-- good_practice    Path C: what was done well
-- control_topic_id the control tag (PPE, Permit To Work, ...) set by the picklist answer; it is kept
--                  apart from topic_id, which is the hazard topic the reporter picked
-- review_flags     reasons HSE should look at the report, separated by "; "
--                  (topic not picked, company not in list, urgent, stop work not used on high-potential work)

ALTER TABLE observations_observation
    ADD COLUMN IF NOT EXISTS path             ENUM('A','B','C','D') NULL,
    ADD COLUMN IF NOT EXISTS unsafe_reason    VARCHAR(160) NULL,
    ADD COLUMN IF NOT EXISTS ppe_item         VARCHAR(80)  NULL,
    ADD COLUMN IF NOT EXISTS stop_work        TINYINT(1)   NULL,
    ADD COLUMN IF NOT EXISTS stopped_by       VARCHAR(80)  NULL,
    ADD COLUMN IF NOT EXISTS immediate_danger TINYINT(1)   NULL,
    ADD COLUMN IF NOT EXISTS barricaded       TINYINT(1)   NULL,
    ADD COLUMN IF NOT EXISTS good_practice    VARCHAR(160) NULL,
    ADD COLUMN IF NOT EXISTS control_topic_id INT UNSIGNED NULL,
    ADD COLUMN IF NOT EXISTS review_flags     VARCHAR(255) NULL,
    ADD INDEX IF NOT EXISTS idx_obs_path (path),
    ADD CONSTRAINT fk_obs_control_topic FOREIGN KEY (control_topic_id) REFERENCES ref_topic (id) ON DELETE SET NULL;
