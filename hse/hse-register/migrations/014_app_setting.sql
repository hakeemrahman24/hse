-- Settings that administrators change in the Settings pages instead of in the server's
-- environment. Today it holds the HSE Medpost address and token (Settings > Medpost API); the
-- token is saved encrypted, never as plain text. Run by hand after 013, before starting this build:
--   mysql hse_register < migrations/014_app_setting.sql
--
-- Until this table exists the program falls back to MEDPOST_URL and MEDPOST_DIRECTORY_TOKEN, so
-- running this build first does not break the SOC form; saving from Settings does need the table.

CREATE TABLE IF NOT EXISTS app_setting (
    name       VARCHAR(60) NOT NULL PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
