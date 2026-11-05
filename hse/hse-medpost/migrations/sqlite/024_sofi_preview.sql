-- SOFI preview switch: one row while an administrator has /sofi open for preview, no row when it is
-- off. Kept in the database (not in memory) so a server restart does not close the preview early.
-- UntilAt is UTC, YYYY-MM-DDTHH:MM:SS; it turns itself off after that (internal/sofi/preview.go).
CREATE TABLE IF NOT EXISTS SofiPreview (
    PreviewID INTEGER PRIMARY KEY,
    UntilAt   TEXT NOT NULL,
    SetBy     TEXT NOT NULL DEFAULT '',
    SetAt     TEXT NOT NULL
);
