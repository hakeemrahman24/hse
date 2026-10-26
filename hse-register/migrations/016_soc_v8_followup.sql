-- SOC v8 follow-up. Run by hand after 015, before starting this build:
--   mysql hse_register < migrations/016_soc_v8_followup.sql
--
-- * suggested_count / picked_count: how often a keyword helped suggest a topic that was shown to a
--   reporter, and how often that topic was then picked (plan v8, change 6). A keyword often
--   suggested but never picked is a removal candidate. Counted when a SOC post is first sent.
-- * ppe_topic_id: for a PPE keyword, the hazard topic it implies (helm -> Line of Fire). Offered
--   only when no hazard topic matched at all. Filled on the next start from ref_keyword_seed.csv
--   (only where still empty).
-- The list of words that typo tolerance must never correct is kept in app_setting (migration 014)
-- under the name typo_guard_words, edited in Settings > Typo guard words.

ALTER TABLE ref_keyword
    ADD COLUMN IF NOT EXISTS suggested_count INT UNSIGNED NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS picked_count    INT UNSIGNED NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS ppe_topic_id    INT UNSIGNED NULL,
    ADD CONSTRAINT fk_keyword_ppe_topic FOREIGN KEY (ppe_topic_id) REFERENCES ref_topic (id) ON DELETE SET NULL;
