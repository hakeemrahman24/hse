package activitylog

import (
	"database/sql"
	"fmt"
)

// SeedIfEmpty seeds the default program list and a few demo activities when
// HealthProcedureTable is empty. Demo activities are attributed to whatever
// staff row exists first (e.g. the admin seeded by staff.EnsureAdminAccount),
// looked up here rather than passed in, so this package has no compile-time
// dependency on internal/staff — if no staff exists yet, demo activities are
// skipped rather than failing the whole seed.
func SeedIfEmpty(database *sql.DB) error {
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM HealthProcedureTable`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}

	procs := []struct {
		name        string
		testLogKind string
	}{
		{"FitnessForWork", ""},
		{"SiteMedicalEmergencyResponse", ""},
		{"MalariaManagement", ""},
		{"PestAndVectorManagement", ""},
		{"FatigueManagementProgram", "fatigue"},
		{"DrugAndAlcoholTesting", "alcohol"},
		{"RespiratoryProtectionProgram", ""},
		{"IndustrialHygieneManagement", ""},
	}
	for _, p := range procs {
		kind := sql.NullString{String: p.testLogKind, Valid: p.testLogKind != ""}
		if _, err := database.Exec(
			`INSERT INTO HealthProcedureTable (HealthProcedure, Status, TestLogKind) VALUES (?, 'Active', ?)`,
			p.name, kind,
		); err != nil {
			return err
		}
	}

	var staffID int64
	err := database.QueryRow(`SELECT StaffID FROM MedpostStaffTable ORDER BY StaffID LIMIT 1`).Scan(&staffID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}

	// Demo activities for FitnessForWork (ProcID 1) so the grouped view has content.
	acts := []struct {
		typ, title, summary, date string
	}{
		{"Monitoring/Inspection", "Site walkthrough", "Checked PPE compliance at gate A.", "2026-08-01"},
		{"Monitoring/Inspection", "Medpost equipment check", "Defibrillator and oxygen inventory verified.", "2026-08-15"},
		{"Socialization", "Toolbox talk", "Briefed crew on heat stress and hydration.", "2026-08-10"},
	}
	for _, a := range acts {
		ar, err := database.Exec(
			`INSERT INTO ActivityTable (FK_ProcID, FK_StaffID, ActivityType, ActivityTitle, ActivitySummary, Location, ActivityDate)
			 VALUES (1, ?, ?, ?, ?, 'Medpost', ?)`,
			staffID, a.typ, a.title, a.summary, a.date,
		)
		if err != nil {
			return err
		}
		aid, err := ar.LastInsertId()
		if err != nil {
			return err
		}
		// Placeholder photo evidence (served as SVG placeholders until real uploads exist).
		for i := 1; i <= 3; i++ {
			link := fmt.Sprintf("placeholder:%d:%d", aid, i)
			if _, err := database.Exec(
				`INSERT INTO EvidenceTable (FK_IDActivity, EvidenceType, FileLink, UploadDate) VALUES (?, 'Photos', ?, ?)`,
				aid, link, a.date,
			); err != nil {
				return err
			}
		}
	}

	return nil
}

// EnsureEvidenceUploader adds EvidenceTable.FK_StaffID (who uploaded that
// file) on databases created before this column existed. SQLite's ADD
// COLUMN isn't safe to re-run unconditionally, so check first via
// PRAGMA table_info. MariaDB is migrated by hand (see README) — this only
// runs for SQLite.
func EnsureEvidenceUploader(database *sql.DB) error {
	rows, err := database.Query(`PRAGMA table_info(EvidenceTable)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == "FK_StaffID" {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = database.Exec(`ALTER TABLE EvidenceTable ADD COLUMN FK_StaffID INTEGER REFERENCES MedpostStaffTable(StaffID)`)
	return err
}
