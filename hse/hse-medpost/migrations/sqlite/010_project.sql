-- ProjectTable is a soft lookup for CompanyRelation.Project (still a plain
-- TEXT column, unchanged) — same non-FK pattern as PositionRole.PositionName
-- feeding the staff-position dropdown: it only supplies pick-list options,
-- it doesn't constrain what CompanyRelation.Project can hold.
CREATE TABLE IF NOT EXISTS ProjectTable (
    ProjectID INTEGER PRIMARY KEY AUTOINCREMENT,
    Name      TEXT NOT NULL UNIQUE,
    Status    TEXT NOT NULL DEFAULT 'Active'
);
