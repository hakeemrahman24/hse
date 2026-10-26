-- SOC keyword list v8 (SOC_Keyword_Improvement_Plan_v8.md, SOC_Form_Spec_v1.1.md). Run by hand,
-- after 014, before starting this build:
--   mysql hse_register < migrations/015_soc_v8.sql
--
-- * Topics get the plain Indonesian label the reporter sees, the high-potential flag and the draft
--   IOGP Life-Saving Rule. The values are filled on the next start from migrations/seed (only where
--   label_id is still empty, so later edits in the database are kept).
-- * Keywords are NOT reloaded: ref_keyword is seeded only while empty, so a database seeded with the
--   pre-v8 list keeps its keywords (admins may have edited them). The v8 list differs from v6/v7 by
--   two spellings (km/jam, x-ray, no effect on matching) and the weight of "bising" (1 -> 2); change
--   that one in Settings > Keywords if wanted. The new matching rules work on the existing list.
-- * ref_picklist holds the answers of the SOC form questions (what was unsafe, PPE item, what was
--   done well, ...). It is filled on the first start from ref_picklist_seed.csv while empty.
-- * A SOC post records what the topic suggestion showed and how the reporter chose, the free
--   accuracy measure of plan v8 section 5: top-1 / top-3 rate, "browse list" rate, "none" rate.

ALTER TABLE ref_topic
    ADD COLUMN IF NOT EXISTS label_id          VARCHAR(120) NULL AFTER hazard_id,
    ADD COLUMN IF NOT EXISTS high_potential    TINYINT(1)   NOT NULL DEFAULT 0 AFTER label_id,
    ADD COLUMN IF NOT EXISTS life_saving_rule  VARCHAR(80)  NULL AFTER high_potential;

CREATE TABLE IF NOT EXISTS ref_picklist (
    id           INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    list_name    VARCHAR(40)  NOT NULL,           -- unsafe_reason, ppe_item, good_practice, stopped_by, yes_no
    option_order SMALLINT UNSIGNED NOT NULL,
    label_id     VARCHAR(160) NOT NULL,           -- Indonesian wording shown on the form
    control_tag  VARCHAR(80)  NULL,               -- the control topic this answer sets (comma separated if several)
    used_in_path VARCHAR(40)  NULL,
    note         VARCHAR(255) NULL,
    UNIQUE KEY uq_picklist (list_name, option_order)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- topic_pick: 'suggested' = the reporter chose one of the 3 suggestions, 'list' = chose from the
-- full list, 'none' = no topic picked (the post waits for HSE review). NULL for posts that are not
-- from the SOC form or were sent before v8. suggestions_shown is the JSON list of {topic id, score}.
ALTER TABLE observations_observation
    ADD COLUMN IF NOT EXISTS topic_pick        ENUM('suggested','list','none') NULL AFTER hazard_id,
    ADD COLUMN IF NOT EXISTS suggestions_shown VARCHAR(255) NULL AFTER topic_pick;
