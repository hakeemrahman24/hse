package entrymcu

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// EnsureCheckUpStatusConstraint tightens McuRecord.CheckUpStatus's CHECK
// constraint to ('Open','Close') — migrations/006_entrymcu.sql originally
// also allowed 'Archived / Exit' there, back when Archived/Exit was
// mistakenly treated as a CheckUpStatus value. It was moved to
// HcValidityStatus instead (see checkUpStatusFor in validity.go and
// CreateMcuRecord/UpdateMcuRecord, which never write 'Archived / Exit' into
// CheckUpStatus anymore); this rebuilds the table so the schema stops
// documenting a value the code no longer produces.
//
// SQLite can't ALTER a CHECK constraint in place, so this follows SQLite's
// own 12-step "other kinds of schema change" recipe (create a replacement
// table, copy rows, drop the original, rename the replacement into its
// place) rather than a plain migrations/*.sql file, which — like every file
// main.go feeds through db.Migrate — gets re-executed verbatim on every
// startup and would fail the second time a DROP/CREATE ran. Guarded by
// checking the live schema first, so it's a no-op once applied. Any row
// still carrying the pre-fix CheckUpStatus='Archived / Exit' is remapped on
// copy rather than assumed clean — see the CASE expressions below.
func EnsureCheckUpStatusConstraint(database *sql.DB) error {
	var createSQL string
	err := database.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'McuRecord'`,
	).Scan(&createSQL)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read McuRecord schema: %w", err)
	}
	if !strings.Contains(createSQL, "CheckUpStatus IN ('Open','Close','Archived / Exit')") {
		return nil
	}

	// PRAGMA foreign_keys is per-connection and can't change inside a
	// transaction, so this pins one physical connection (via Conn) instead
	// of using database.Begin() directly — otherwise the pool could hand the
	// transaction a different connection than the one the PRAGMA ran on,
	// leaving FK enforcement on and DROP TABLE failing (observed live: the
	// rebuilt McuRecord is still the REFERENCES target of
	// McuRecordCertificateLog/McuRecordEmailNotification's FK_McuRecordID).
	ctx := context.Background()
	conn, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection for CheckUpStatus migration: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return fmt.Errorf("disable foreign keys for CheckUpStatus migration: %w", err)
	}
	defer conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`)

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin CheckUpStatus migration: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(mcuRecordTableDDLPostCheckUpStatusFix("McuRecord_checkupstatus_new")); err != nil {
		return fmt.Errorf("create replacement McuRecord table: %w", err)
	}
	// Plain `SELECT *` would violate the new CHECK on any row still carrying
	// the old CheckUpStatus='Archived / Exit' value (found live: a handful of
	// pre-fix rows created before checkUpStatusFor existed). Remap those on
	// the way through instead of assuming the data's already clean: move the
	// signal into HcValidityStatus (if nothing's there already) and
	// recompute CheckUpStatus/IsTerminal from ResultCriteria, matching what
	// checkUpStatusFor/isTerminalResult would produce today.
	const legacyArchivedCheckUpStatus = `CheckUpStatus = 'Archived / Exit'`
	const legacyTerminalResult = `ResultCriteria IN ('Fit','FitWithNote','TemporaryUnfit','Unfit')`
	copySQL := `INSERT INTO McuRecord_checkupstatus_new SELECT
		McuRecordID, FK_PatientID, Timestamp, ReviewDate, CheckUpID, Reviewer,
		FK_CheckUpCriteriaID, FK_TaskCategoryID, Age, HeightCm, WeightKg,
		FK_CompanyID, WorkPosition, DateCheckUp, IsShortProject, ValidityPeriodMonths,
		BmiValue, BmiCriteria, BmiNotes, Smoking, BpCriteria, BpNotes,
		VisionCriteria, VisionNotes, CbcCriteria, CbcNotes, LipidCriteria, LipidNotes,
		GlucoseCriteria, GlucoseNotes, LiverCriteria, LiverNotes, KidneyCriteria, KidneyNotes,
		UrinalysisCriteria, UrinalysisNotes, RadiologyResult, RadiologyNotes, EcgResult,
		TreadmillResult, EcgTreadmillNotes, HerniaHemorrhoidCriteria, HerniaHemorrhoidNotes,
		SpirometryCriteria, SpirometryNotes, AudiometryCriteria, AudiometryNotes,
		HepBCriteria, HepBNotes, HepACriteria, HepANotes, RectalSwabCriteria, RectalSwabNotes,
		HivCriteria, ResultCriteria, ResultValidUntil, ResultNotes, Recommendation,
		CASE WHEN ` + legacyArchivedCheckUpStatus + ` THEN
			(CASE WHEN ` + legacyTerminalResult + ` THEN 'Close' ELSE 'Open' END)
		ELSE CheckUpStatus END,
		FK_ParentRecordID, HealthCertificateFileName,
		CASE WHEN ` + legacyArchivedCheckUpStatus + ` AND HcValidityStatus IS NULL THEN 'Archived / Exit'
		ELSE HcValidityStatus END,
		ReviewYear,
		CASE WHEN ` + legacyArchivedCheckUpStatus + ` THEN
			(CASE WHEN ` + legacyTerminalResult + ` THEN 1 ELSE 0 END)
		ELSE IsTerminal END,
		SubmissionDate, DaysToReview, FK_CompanyRelationID
	FROM McuRecord`
	if _, err := tx.Exec(copySQL); err != nil {
		return fmt.Errorf("copy McuRecord rows: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE McuRecord`); err != nil {
		return fmt.Errorf("drop old McuRecord table: %w", err)
	}
	if _, err := tx.Exec(`ALTER TABLE McuRecord_checkupstatus_new RENAME TO McuRecord`); err != nil {
		return fmt.Errorf("rename replacement McuRecord table: %w", err)
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_mcu_review_date ON McuRecord(ReviewDate)`); err != nil {
		return fmt.Errorf("recreate idx_mcu_review_date: %w", err)
	}

	return tx.Commit()
}

// mcuRecordTableDDLPostCheckUpStatusFix is migrations/006_entrymcu.sql's
// McuRecord definition verbatim, except CheckUpStatus's CHECK no longer
// allows 'Archived / Exit' (see EnsureCheckUpStatusConstraint above) —
// column order must stay identical to what EnsureCheckUpStatusConstraint's
// copySQL produces. Frozen on purpose (not shared with later migrations'
// DDL) — EnsureTaskCategoryNamesColumn below needs a different column shape,
// and mutating this one out from under it would silently corrupt data on a
// fresh install where both migrations run back-to-back (see its comment).
func mcuRecordTableDDLPostCheckUpStatusFix(tableName string) string {
	return `CREATE TABLE ` + tableName + ` (
    McuRecordID    INTEGER PRIMARY KEY AUTOINCREMENT,
    FK_PatientID   INTEGER NOT NULL REFERENCES Patient(PatientID),
    Timestamp      TEXT NOT NULL,
    ReviewDate     TEXT NOT NULL,
    CheckUpID      TEXT NOT NULL UNIQUE,
    Reviewer       TEXT NULL,
    FK_CheckUpCriteriaID INTEGER NULL REFERENCES CheckUpCriteriaTable(CheckUpCriteriaID),
    FK_TaskCategoryID    INTEGER NULL REFERENCES TaskCategoryTable(TaskCategoryID),
    Age            INTEGER NULL,
    HeightCm       REAL NULL,
    WeightKg       REAL NULL,
    FK_CompanyID   INTEGER NULL REFERENCES Company(CompanyID),
    WorkPosition   TEXT NULL,
    DateCheckUp    TEXT NULL,
    IsShortProject INTEGER NOT NULL DEFAULT 0,
    ValidityPeriodMonths INTEGER NULL,
    BmiValue REAL NULL, BmiCriteria TEXT NULL, BmiNotes TEXT NULL,
    Smoking TEXT NULL,
    BpCriteria TEXT NULL, BpNotes TEXT NULL,
    VisionCriteria TEXT NULL, VisionNotes TEXT NULL,
    CbcCriteria TEXT NULL, CbcNotes TEXT NULL,
    LipidCriteria TEXT NULL, LipidNotes TEXT NULL,
    GlucoseCriteria TEXT NULL, GlucoseNotes TEXT NULL,
    LiverCriteria TEXT NULL, LiverNotes TEXT NULL,
    KidneyCriteria TEXT NULL, KidneyNotes TEXT NULL,
    UrinalysisCriteria TEXT NULL, UrinalysisNotes TEXT NULL,
    RadiologyResult TEXT NULL, RadiologyNotes TEXT NULL,
    EcgResult TEXT NULL,
    TreadmillResult TEXT NULL,
    EcgTreadmillNotes TEXT NULL,
    HerniaHemorrhoidCriteria TEXT NULL, HerniaHemorrhoidNotes TEXT NULL,
    SpirometryCriteria TEXT NULL, SpirometryNotes TEXT NULL,
    AudiometryCriteria TEXT NULL, AudiometryNotes TEXT NULL,
    HepBCriteria TEXT NULL, HepBNotes TEXT NULL,
    HepACriteria TEXT NULL, HepANotes TEXT NULL,
    RectalSwabCriteria TEXT NULL, RectalSwabNotes TEXT NULL,
    HivCriteria TEXT NULL,
    ResultCriteria TEXT NULL CHECK (ResultCriteria IN ('Fit','FitWithNote','Unfit','TemporaryUnfit','NeedFollowUp')),
    ResultValidUntil TEXT NULL,
    ResultNotes TEXT NULL,
    Recommendation TEXT NULL,
    CheckUpStatus TEXT NULL CHECK (CheckUpStatus IN ('Open','Close')),
    FK_ParentRecordID INTEGER NULL REFERENCES McuRecord(McuRecordID),
    HealthCertificateFileName TEXT NULL,
    HcValidityStatus TEXT NULL,
    ReviewYear INTEGER NULL,
    IsTerminal INTEGER NOT NULL DEFAULT 1,
    SubmissionDate TEXT NULL,
    DaysToReview INTEGER NULL,
    FK_CompanyRelationID INTEGER NULL REFERENCES CompanyRelation(CompanyRelationID)
)`
}

// EnsureTaskCategoryNamesColumn replaces McuRecord.FK_TaskCategoryID (a
// single FK to TaskCategoryTable) with TaskCategoryNames TEXT — Task
// category became a multi-select (a checkup can be Food Handler AND Work at
// Height at once), which a single FK column can't represent. Selected
// category names are joined with "; " (CreateMcuRecord/UpdateMcuRecord
// resolve the client's taskCategoryIds against TaskCategoryTable and store
// the joined string) instead of a junction table, matching how
// HcValidityStatus/CheckUpStatus are already plain denormalized text rather
// than normalized relations — this field is written once per checkup and
// only ever read back as a whole for display/certificates, so there's no
// query that benefits from a normalized many-to-many here.
//
// Same SQLite table-rebuild recipe as EnsureCheckUpStatusConstraint (see its
// comment) — guarded by checking for the column first, so it's a no-op once
// applied. Existing single-selection data is resolved via a correlated
// subquery against the still-live FK_TaskCategoryID during the copy, so no
// existing task-category assignment is lost.
func EnsureTaskCategoryNamesColumn(database *sql.DB) error {
	hasColumn, err := mcuRecordHasColumn(database, "TaskCategoryNames")
	if err != nil {
		return fmt.Errorf("check McuRecord schema: %w", err)
	}
	if hasColumn {
		return nil
	}

	ctx := context.Background()
	conn, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection for TaskCategoryNames migration: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return fmt.Errorf("disable foreign keys for TaskCategoryNames migration: %w", err)
	}
	defer conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`)

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin TaskCategoryNames migration: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(mcuRecordTableDDLPostTaskCategoryFix("McuRecord_taskcategory_new")); err != nil {
		return fmt.Errorf("create replacement McuRecord table: %w", err)
	}
	copySQL := `INSERT INTO McuRecord_taskcategory_new SELECT
		McuRecordID, FK_PatientID, Timestamp, ReviewDate, CheckUpID, Reviewer,
		FK_CheckUpCriteriaID,
		(SELECT tc.Name FROM TaskCategoryTable tc WHERE tc.TaskCategoryID = McuRecord.FK_TaskCategoryID),
		Age, HeightCm, WeightKg, FK_CompanyID, WorkPosition, DateCheckUp, IsShortProject, ValidityPeriodMonths,
		BmiValue, BmiCriteria, BmiNotes, Smoking, BpCriteria, BpNotes,
		VisionCriteria, VisionNotes, CbcCriteria, CbcNotes, LipidCriteria, LipidNotes,
		GlucoseCriteria, GlucoseNotes, LiverCriteria, LiverNotes, KidneyCriteria, KidneyNotes,
		UrinalysisCriteria, UrinalysisNotes, RadiologyResult, RadiologyNotes, EcgResult,
		TreadmillResult, EcgTreadmillNotes, HerniaHemorrhoidCriteria, HerniaHemorrhoidNotes,
		SpirometryCriteria, SpirometryNotes, AudiometryCriteria, AudiometryNotes,
		HepBCriteria, HepBNotes, HepACriteria, HepANotes, RectalSwabCriteria, RectalSwabNotes,
		HivCriteria, ResultCriteria, ResultValidUntil, ResultNotes, Recommendation, CheckUpStatus,
		FK_ParentRecordID, HealthCertificateFileName, HcValidityStatus, ReviewYear,
		IsTerminal, SubmissionDate, DaysToReview, FK_CompanyRelationID
	FROM McuRecord`
	if _, err := tx.Exec(copySQL); err != nil {
		return fmt.Errorf("copy McuRecord rows: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE McuRecord`); err != nil {
		return fmt.Errorf("drop old McuRecord table: %w", err)
	}
	if _, err := tx.Exec(`ALTER TABLE McuRecord_taskcategory_new RENAME TO McuRecord`); err != nil {
		return fmt.Errorf("rename replacement McuRecord table: %w", err)
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_mcu_review_date ON McuRecord(ReviewDate)`); err != nil {
		return fmt.Errorf("recreate idx_mcu_review_date: %w", err)
	}

	return tx.Commit()
}

// EnsureRadiologyColumns adds the Radiology section's new test columns
// (Echocardiography, CT Angiography, USG abdomen — each a criteria/notes
// pair) plus AdditionalNotes, a general free-text field for the whole
// checkup (not scoped to Radiology). Unlike
// EnsureCheckUpStatusConstraint/EnsureTaskCategoryNamesColumn above, none of
// these touch a CHECK constraint or change an existing column's meaning, so
// SQLite's plain `ALTER TABLE ... ADD COLUMN` suffices — no table rebuild
// needed. Guarded on the last column added, so it's a no-op once applied.
func EnsureRadiologyColumns(database *sql.DB) error {
	hasColumn, err := mcuRecordHasColumn(database, "AdditionalNotes")
	if err != nil {
		return fmt.Errorf("check McuRecord schema: %w", err)
	}
	if hasColumn {
		return nil
	}

	columns := []string{
		"EchocardiographyCriteria", "EchocardiographyNotes",
		"CtScanCriteria", "CtScanNotes",
		"UsgAbdomenCriteria", "UsgAbdomenNotes",
		"AdditionalNotes",
	}
	for _, col := range columns {
		if _, err := database.Exec(`ALTER TABLE McuRecord ADD COLUMN ` + col + ` TEXT NULL`); err != nil {
			return fmt.Errorf("add McuRecord.%s: %w", col, err)
		}
	}
	return nil
}

// EnsureCtScanColumnsRenamed handles installs that already ran
// EnsureRadiologyColumns back when it created CtAngiographyCriteria/Notes
// (the Radiology section's "CT Angiography" field was renamed to "CT scan"
// after that shipped). A fresh install never hits this — EnsureRadiologyColumns
// above now creates CtScanCriteria/Notes directly — so this only renames the
// old columns in place (preserving any data already saved under the old
// names) via SQLite's plain `ALTER TABLE ... RENAME COLUMN` (supported since
// SQLite 3.25, no table rebuild needed). Guarded: no-op if either the new
// columns already exist or the old ones never did.
func EnsureCtScanColumnsRenamed(database *sql.DB) error {
	hasNew, err := mcuRecordHasColumn(database, "CtScanCriteria")
	if err != nil {
		return fmt.Errorf("check McuRecord schema: %w", err)
	}
	if hasNew {
		return nil
	}
	hasOld, err := mcuRecordHasColumn(database, "CtAngiographyCriteria")
	if err != nil {
		return fmt.Errorf("check McuRecord schema: %w", err)
	}
	if !hasOld {
		return nil
	}
	if _, err := database.Exec(`ALTER TABLE McuRecord RENAME COLUMN CtAngiographyCriteria TO CtScanCriteria`); err != nil {
		return fmt.Errorf("rename McuRecord.CtAngiographyCriteria: %w", err)
	}
	if _, err := database.Exec(`ALTER TABLE McuRecord RENAME COLUMN CtAngiographyNotes TO CtScanNotes`); err != nil {
		return fmt.Errorf("rename McuRecord.CtAngiographyNotes: %w", err)
	}
	return nil
}

// EnsurePhysicalExamNotesColumn adds OtherPhysicalExamNotes, a free-text
// catch-all at the end of the Physical exam section (BMI, smoking,
// hernia/hemorrhoid) for findings that don't fit any of that section's
// specific fields. Plain `ALTER TABLE ... ADD COLUMN`, same as
// EnsureRadiologyColumns — no CHECK constraint involved.
func EnsurePhysicalExamNotesColumn(database *sql.DB) error {
	hasColumn, err := mcuRecordHasColumn(database, "OtherPhysicalExamNotes")
	if err != nil {
		return fmt.Errorf("check McuRecord schema: %w", err)
	}
	if hasColumn {
		return nil
	}
	if _, err := database.Exec(`ALTER TABLE McuRecord ADD COLUMN OtherPhysicalExamNotes TEXT NULL`); err != nil {
		return fmt.Errorf("add McuRecord.OtherPhysicalExamNotes: %w", err)
	}
	return nil
}

// EnsureMcuRecordPersonalIDColumn adds McuRecord.PersonalID — a per-checkup
// snapshot of the patient's NIK/passport ID, mirroring how FK_CompanyID/
// WorkPosition already snapshot company/position at save time rather than
// only being read live off Patient. Populated from Patient.PersonalID when
// the checkup is created/edited (see CreateMcuRecord/UpdateMcuRecord), but
// independently stored so a later change to the patient's own PersonalID
// doesn't rewrite a past checkup's history.
func EnsureMcuRecordPersonalIDColumn(database *sql.DB) error {
	hasColumn, err := mcuRecordHasColumn(database, "PersonalID")
	if err != nil {
		return fmt.Errorf("check McuRecord schema: %w", err)
	}
	if hasColumn {
		return nil
	}
	if _, err := database.Exec(`ALTER TABLE McuRecord ADD COLUMN PersonalID TEXT NULL`); err != nil {
		return fmt.Errorf("add McuRecord.PersonalID: %w", err)
	}
	return nil
}

// EnsureSchemaIndexes adds indexes on the hot-path FK/lookup columns that
// were missing entirely (found via a table-structure audit: only 4 indexes
// existed in the whole DB, none of them on McuRecord.FK_PatientID — the
// single most-queried column in the app — or on Patient.DateOfBirth, which
// the Name+DOB widening in ListParentCandidates/ListMcuRecords now filters
// on). CREATE INDEX IF NOT EXISTS is natively idempotent, unlike ALTER
// TABLE ADD COLUMN elsewhere in this file, so no PRAGMA guard is needed
// here — SQLite itself is the guard.
//
// idx_patient_name_dob is UNIQUE, not just an index: it closes the
// duplicate-patient gap at the source (CreatePatient, unlike the import
// path's findOrCreatePatientID, never deduped by Name+DOB, so the same real
// person could end up under two Patient rows — the reason
// ListParentCandidates/ListMcuRecords needed the Name+DOB widening in the
// first place). SQLite treats each NULL as distinct in a UNIQUE index, so
// patients with an unknown DOB are unaffected; only a genuine exact
// Name+DOB duplicate is rejected. Checked live data before adding this:
// zero existing violations.
func EnsureSchemaIndexes(database *sql.DB) error {
	stmts := []string{
		`CREATE INDEX IF NOT EXISTS idx_mcu_patient ON McuRecord(FK_PatientID)`,
		`CREATE INDEX IF NOT EXISTS idx_mcu_parent_record ON McuRecord(FK_ParentRecordID)`,
		`CREATE INDEX IF NOT EXISTS idx_mcu_company ON McuRecord(FK_CompanyID)`,
		`CREATE INDEX IF NOT EXISTS idx_patient_dob ON Patient(DateOfBirth)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_patient_name_dob ON Patient(Name, DateOfBirth)`,
		// idx_patient_personal_id backs PersonalID's role as an alternative
		// dedup key (see EnsurePatientPersonalIDColumn below and
		// CreatePatient/UpdatePatient in internal/patient/patient.go) —
		// UNIQUE so two patients can't share a NIK/passport number, but
		// SQLite's NULL-distinct behavior means any number of patients with
		// no PersonalID on file are unaffected, matching "if not empty".
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_patient_personal_id ON Patient(PersonalID)`,
	}
	for _, stmt := range stmts {
		if _, err := database.Exec(stmt); err != nil {
			return fmt.Errorf("create index (%s): %w", stmt, err)
		}
	}
	return nil
}

func patientHasColumn(database *sql.DB, column string) (bool, error) {
	rows, err := database.Query(`PRAGMA table_info(Patient)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
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

// EnsurePatientPersonalIDColumn adds Patient.PersonalID: a 16-digit NIK for
// Indonesian nationality or a 7-9 character alphanumeric passport number
// otherwise (format enforced in internal/patient/patient.go's
// validatePersonalID, not here). Optional — NULL until a staff member fills
// it in — and then usable as an alternative dedup key alongside Name+DOB via
// idx_patient_personal_id in EnsureSchemaIndexes, which must run after this.
func EnsurePatientPersonalIDColumn(database *sql.DB) error {
	hasColumn, err := patientHasColumn(database, "PersonalID")
	if err != nil {
		return fmt.Errorf("check Patient schema: %w", err)
	}
	if hasColumn {
		return nil
	}
	if _, err := database.Exec(`ALTER TABLE Patient ADD COLUMN PersonalID TEXT NULL`); err != nil {
		return fmt.Errorf("add Patient.PersonalID: %w", err)
	}
	return nil
}

func mcuRecordHasColumn(database *sql.DB, column string) (bool, error) {
	rows, err := database.Query(`PRAGMA table_info(McuRecord)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
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

// mcuRecordTableDDLPostTaskCategoryFix is
// mcuRecordTableDDLPostCheckUpStatusFix with FK_TaskCategoryID replaced by
// TaskCategoryNames (see EnsureTaskCategoryNamesColumn above). Column order
// must stay identical to what EnsureTaskCategoryNamesColumn's copySQL
// produces.
func mcuRecordTableDDLPostTaskCategoryFix(tableName string) string {
	return `CREATE TABLE ` + tableName + ` (
    McuRecordID    INTEGER PRIMARY KEY AUTOINCREMENT,
    FK_PatientID   INTEGER NOT NULL REFERENCES Patient(PatientID),
    Timestamp      TEXT NOT NULL,
    ReviewDate     TEXT NOT NULL,
    CheckUpID      TEXT NOT NULL UNIQUE,
    Reviewer       TEXT NULL,
    FK_CheckUpCriteriaID INTEGER NULL REFERENCES CheckUpCriteriaTable(CheckUpCriteriaID),
    TaskCategoryNames TEXT NULL,
    Age            INTEGER NULL,
    HeightCm       REAL NULL,
    WeightKg       REAL NULL,
    FK_CompanyID   INTEGER NULL REFERENCES Company(CompanyID),
    WorkPosition   TEXT NULL,
    DateCheckUp    TEXT NULL,
    IsShortProject INTEGER NOT NULL DEFAULT 0,
    ValidityPeriodMonths INTEGER NULL,
    BmiValue REAL NULL, BmiCriteria TEXT NULL, BmiNotes TEXT NULL,
    Smoking TEXT NULL,
    BpCriteria TEXT NULL, BpNotes TEXT NULL,
    VisionCriteria TEXT NULL, VisionNotes TEXT NULL,
    CbcCriteria TEXT NULL, CbcNotes TEXT NULL,
    LipidCriteria TEXT NULL, LipidNotes TEXT NULL,
    GlucoseCriteria TEXT NULL, GlucoseNotes TEXT NULL,
    LiverCriteria TEXT NULL, LiverNotes TEXT NULL,
    KidneyCriteria TEXT NULL, KidneyNotes TEXT NULL,
    UrinalysisCriteria TEXT NULL, UrinalysisNotes TEXT NULL,
    RadiologyResult TEXT NULL, RadiologyNotes TEXT NULL,
    EcgResult TEXT NULL,
    TreadmillResult TEXT NULL,
    EcgTreadmillNotes TEXT NULL,
    HerniaHemorrhoidCriteria TEXT NULL, HerniaHemorrhoidNotes TEXT NULL,
    SpirometryCriteria TEXT NULL, SpirometryNotes TEXT NULL,
    AudiometryCriteria TEXT NULL, AudiometryNotes TEXT NULL,
    HepBCriteria TEXT NULL, HepBNotes TEXT NULL,
    HepACriteria TEXT NULL, HepANotes TEXT NULL,
    RectalSwabCriteria TEXT NULL, RectalSwabNotes TEXT NULL,
    HivCriteria TEXT NULL,
    ResultCriteria TEXT NULL CHECK (ResultCriteria IN ('Fit','FitWithNote','Unfit','TemporaryUnfit','NeedFollowUp')),
    ResultValidUntil TEXT NULL,
    ResultNotes TEXT NULL,
    Recommendation TEXT NULL,
    CheckUpStatus TEXT NULL CHECK (CheckUpStatus IN ('Open','Close')),
    FK_ParentRecordID INTEGER NULL REFERENCES McuRecord(McuRecordID),
    HealthCertificateFileName TEXT NULL,
    HcValidityStatus TEXT NULL,
    ReviewYear INTEGER NULL,
    IsTerminal INTEGER NOT NULL DEFAULT 1,
    SubmissionDate TEXT NULL,
    DaysToReview INTEGER NULL,
    FK_CompanyRelationID INTEGER NULL REFERENCES CompanyRelation(CompanyRelationID)
)`
}
