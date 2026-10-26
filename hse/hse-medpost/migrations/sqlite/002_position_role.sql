-- Position → app role mapping (admin settings).
CREATE TABLE IF NOT EXISTS PositionRoleTable (
    PositionID   INTEGER PRIMARY KEY AUTOINCREMENT,
    PositionName TEXT NOT NULL UNIQUE,
    Role         TEXT NOT NULL
        CHECK (Role IN ('admin', 'manager', 'staff'))
);
