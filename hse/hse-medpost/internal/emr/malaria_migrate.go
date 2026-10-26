package emr

import "database/sql"

// EnsureMalariaPurpose upgrades a SQLite database that was created before the
// screening purpose existed: it adds the lookup table and MalariaScreening.FK_PurposeID,
// and every row already saved becomes 1, On arrival.
// SQLite cannot add a foreign-key column with a default to an existing table, so
// an upgraded dev database has the column without the constraint; the code checks
// the id against the lookup table anyway. Fresh databases get both
// from migrations/sqlite/014_visit_malaria.sql, and MariaDB from
// migrations/mysql/023_malaria_purpose.sql, run by hand.
func EnsureMalariaPurpose(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS MalariaPurposeTable (
			PurposeID   INTEGER PRIMARY KEY,
			PurposeName TEXT NOT NULL UNIQUE
		);
		INSERT OR IGNORE INTO MalariaPurposeTable (PurposeID, PurposeName) VALUES (1, 'On arrival'), (2, 'Symptomatic');`); err != nil {
		return err
	}
	has, err := hasColumn(db, "MalariaScreening", "FK_PurposeID")
	if err != nil || has {
		return err
	}
	_, err = db.Exec(`ALTER TABLE MalariaScreening ADD COLUMN FK_PurposeID INTEGER NOT NULL DEFAULT 1`)
	return err
}

func hasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// malariaColumns is every MalariaScreening column except MalariaID, in table order.
const malariaColumns = `FK_VisitID, FK_PurposeID, TestDate, SlideInitial, AgeYears, WeightKg, HeadOfHousehold, HeadIsPatient,
	RegencyCode, RegencyName, DistrictCode, DistrictName, VillageCode, VillageName, WorkPosition, Company, ExamType, Result`

// EnsureMalariaID rebuilds MalariaScreening in a SQLite database that still keys
// it by FK_VisitID alone, so one visit can hold more than one test. Every row
// keeps its number: its new MalariaID is its old visit id, so Test IDs do not
// change. Fresh databases get the new shape from migrations/sqlite/014; MariaDB
// from migrations/mysql/024_malaria_id.sql, run by hand. Run it after
// EnsureMalariaPurpose, which adds the purpose column the rebuild copies.
func EnsureMalariaID(db *sql.DB) error {
	has, err := hasColumn(db, "MalariaScreening", "MalariaID")
	if err != nil || has {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`CREATE TABLE MalariaScreeningNew (
			MalariaID       INTEGER PRIMARY KEY,
			FK_VisitID      INTEGER NOT NULL REFERENCES Visit(VisitID),
			FK_PurposeID    INTEGER NOT NULL DEFAULT 1 REFERENCES MalariaPurposeTable(PurposeID),
			TestDate        TEXT NOT NULL,
			SlideInitial    TEXT NOT NULL,
			AgeYears        INTEGER NULL,
			WeightKg        REAL NOT NULL CHECK (WeightKg > 0 AND WeightKg <= 150),
			HeadOfHousehold TEXT NOT NULL,
			HeadIsPatient   INTEGER NOT NULL DEFAULT 0,
			RegencyCode     TEXT NOT NULL,
			RegencyName     TEXT NOT NULL,
			DistrictCode    TEXT NOT NULL,
			DistrictName    TEXT NOT NULL,
			VillageCode     TEXT NOT NULL,
			VillageName     TEXT NOT NULL,
			WorkPosition    TEXT NOT NULL DEFAULT '',
			Company         TEXT NOT NULL DEFAULT '',
			ExamType        TEXT NOT NULL CHECK (ExamType IN ('RDT', 'BloodSmear')),
			Result          TEXT NULL CHECK (Result IN ('Negative', 'Pv', 'Pf', 'Po', 'Mixed'))
		)`,
		`INSERT INTO MalariaScreeningNew (MalariaID, ` + malariaColumns + `) SELECT FK_VisitID, ` + malariaColumns + ` FROM MalariaScreening`,
		`DROP TABLE MalariaScreening`,
		`ALTER TABLE MalariaScreeningNew RENAME TO MalariaScreening`,
		`CREATE INDEX idx_malaria_visit ON MalariaScreening (FK_VisitID)`,
		`CREATE INDEX IF NOT EXISTS idx_malaria_testdate ON MalariaScreening (TestDate)`,
		`CREATE INDEX IF NOT EXISTS idx_malaria_slide ON MalariaScreening (TestDate, SlideInitial)`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return tx.Commit()
}
