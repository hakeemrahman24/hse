-- The five SOFI dimensions and their names in the three languages of /sofi. The server fills this
-- table itself at start-up from the built-in names in internal/sofi/scoring.go (rows already here
-- are never overwritten), and the administrator renames a dimension in Settings. The key, the
-- order and which items belong to a dimension cannot be changed there, and scoring does not read
-- this table: only the names shown change.
CREATE TABLE IF NOT EXISTS SofiDimension (
    DimensionKey TEXT PRIMARY KEY,
    SortOrder    INTEGER NOT NULL,
    NameEN       TEXT NOT NULL,
    NameID       TEXT NOT NULL,
    NameZH       TEXT NOT NULL,
    UpdatedBy    TEXT NOT NULL DEFAULT '',
    UpdatedAt    TEXT NOT NULL
);
