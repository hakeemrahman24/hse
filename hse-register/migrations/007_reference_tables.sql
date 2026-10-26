-- Lookup lists used by the observation forms: hazard categories, topics, topic keywords, areas.
-- Run by hand, before 008:
--   mysql hse_register < migrations/007_reference_tables.sql
--
-- The rows are loaded by the program on first start from migrations/seed/*.csv (only while the
-- tables are empty), so this file only creates the tables. Areas are edited in Settings.

CREATE TABLE IF NOT EXISTS ref_hazard_category (
    id           INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    hazard_class VARCHAR(40)  NOT NULL,          -- one of the 7 workplace hazard classes
    name         VARCHAR(100) NOT NULL,
    UNIQUE KEY uq_hazard_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS ref_topic (
    id          INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    sort_order  SMALLINT UNSIGNED NOT NULL,
    name        VARCHAR(100) NOT NULL,
    kind        ENUM('hazard','control') NOT NULL,  -- only 'hazard' topics are offered on the form
    topic_group VARCHAR(40)  NULL,
    hazard_id   INT UNSIGNED NULL,                  -- the hazard suggested with this topic
    UNIQUE KEY uq_topic_name (name),
    CONSTRAINT fk_topic_hazard FOREIGN KEY (hazard_id)
        REFERENCES ref_hazard_category (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS ref_keyword (
    id         INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    topic_id   INT UNSIGNED NOT NULL,
    keyword    VARCHAR(100) NOT NULL,
    match_type ENUM('prefix','whole') NOT NULL,    -- prefix: a word starts with it; whole: whole word(s)
    weight     TINYINT UNSIGNED NOT NULL DEFAULT 1,
    is_active  TINYINT(1) NOT NULL DEFAULT 1,
    source     VARCHAR(255) NULL,
    UNIQUE KEY uq_keyword (topic_id, keyword),
    CONSTRAINT fk_keyword_topic FOREIGN KEY (topic_id)
        REFERENCES ref_topic (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS ref_area (
    id   INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    UNIQUE KEY uq_area_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
