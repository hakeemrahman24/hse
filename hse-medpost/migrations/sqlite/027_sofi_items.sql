-- The 20 SOFI questionnaire items: the dimension each belongs to (a foreign key to SofiDimension, so
-- an item can never point at a dimension that does not exist) and its wording in the three languages
-- of /sofi. The server fills this table itself at start-up from internal/sofi/scoring.go (rows
-- already here are never overwritten); the administrator rewords an item in Settings. Scoring does
-- not read this table: an item's dimension and position are fixed in code, so a wording change
-- cannot move a score.
CREATE TABLE IF NOT EXISTS SofiItem (
    ItemNumber   INTEGER PRIMARY KEY,
    DimensionKey TEXT NOT NULL REFERENCES SofiDimension(DimensionKey),
    TextEN       TEXT NOT NULL,
    TextID       TEXT NOT NULL,
    TextZH       TEXT NOT NULL,
    UpdatedBy    TEXT NOT NULL DEFAULT '',
    UpdatedAt    TEXT NOT NULL
);
