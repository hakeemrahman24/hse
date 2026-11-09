-- SOFI questionnaire wording the administrator has changed in Settings. One row per item (1-20) that
-- differs from the built-in wording in internal/sofi/scoring.go; no row means the built-in wording.
-- A blank column inside a row also means the built-in text for that language. Scoring never reads
-- this table: only the words shown to the worker change, not which dimension an item belongs to.
CREATE TABLE IF NOT EXISTS SofiItem (
    ItemNumber INTEGER PRIMARY KEY,
    TextEN     TEXT NOT NULL DEFAULT '',
    TextID     TEXT NOT NULL DEFAULT '',
    TextZH     TEXT NOT NULL DEFAULT '',
    UpdatedBy  TEXT NOT NULL DEFAULT '',
    UpdatedAt  TEXT NOT NULL
);
