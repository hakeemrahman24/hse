-- Company/CompanyRelation/EntityType — global reference data (see plan §2
-- "Common"). Lifted from entry-mcu/plan/sql schema unchanged in shape;
-- FK_ParentID is CompanyRelation's own self-FK for the client/contractor
-- hierarchy entry-mcu's Sheet export already encodes (Depth = how many
-- levels down from the top-level client a relation sits).
CREATE TABLE IF NOT EXISTS EntityType (
    EntityTypeID INTEGER PRIMARY KEY AUTOINCREMENT,
    LegacyCode   TEXT NULL,
    Name         TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS Company (
    CompanyID       INTEGER PRIMARY KEY AUTOINCREMENT,
    LegacyCode      TEXT NULL UNIQUE,
    Name            TEXT NOT NULL UNIQUE,
    Abbreviation    TEXT NULL,
    FK_EntityTypeID INTEGER NULL REFERENCES EntityType(EntityTypeID)
);

CREATE TABLE IF NOT EXISTS CompanyRelation (
    CompanyRelationID INTEGER PRIMARY KEY AUTOINCREMENT,
    RelationCode TEXT NOT NULL UNIQUE,
    FK_CompanyID INTEGER NOT NULL REFERENCES Company(CompanyID),
    FK_ParentID  INTEGER NULL REFERENCES Company(CompanyID),
    Project      TEXT NULL,
    Depth        INTEGER NULL
);
