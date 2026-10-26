package activitylog

import "database/sql"

// EnsureSubHeaderMerge folds SubHeaderEntryTable/EntryEvidenceTable into
// ActivityTable/EvidenceTable via the nullable FK_SubHeaderID/FormLink
// columns (see migrations/001_sqlite.sql and 003_subheaders.sql) — a
// sub-header entry becomes an activity scoped to a sub-header instead of a
// fully duplicated parallel table pair. Existing rows in the old tables are
// carried across before those tables are dropped:
//   - ActivityType has no equivalent in the old entry shape, so migrated
//     rows are backfilled to 'Monitoring/Inspection' (an arbitrary but valid
//     placeholder — new entries created after this migration behave the
//     same way, see CreateSubHeaderEntry).
//   - EvidenceTable has no 'Document' EvidenceType (only AttendanceList/
//     ReportAttachment/Photos); migrated 'Document' rows map to
//     'ReportAttachment', the closest existing bucket, so the CHECK
//     constraint doesn't need to change.
//
// Same self-migration shape as db.EnsureEvidenceUploader: idempotent, keyed
// off whether ActivityTable already has FK_SubHeaderID, safe to call on
// every startup. SQLite only — matches EnsureEvidenceUploader's scope, since
// MariaDB has never had the old schema deployed to it.
func EnsureSubHeaderMerge(db *sql.DB) error {
	migrated, err := hasColumn(db, "ActivityTable", "FK_SubHeaderID")
	if err != nil {
		return err
	}
	if migrated {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`ALTER TABLE ActivityTable ADD COLUMN FK_SubHeaderID INTEGER REFERENCES SubHeaderTable(SubHeaderID) ON DELETE CASCADE`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE ActivityTable ADD COLUMN FormLink TEXT`); err != nil {
		return err
	}

	hasLegacyTable, err := hasTable(db, "SubHeaderEntryTable")
	if err != nil {
		return err
	}
	if hasLegacyTable {
		if err := migrateLegacySubHeaderEntries(tx); err != nil {
			return err
		}
		if _, err := tx.Exec(`DROP TABLE IF EXISTS EntryEvidenceTable`); err != nil {
			return err
		}
		if _, err := tx.Exec(`DROP TABLE IF EXISTS SubHeaderEntryTable`); err != nil {
			return err
		}
	}

	return tx.Commit()
}

type legacySubHeaderEntry struct {
	entryID     int64
	procID      int64
	subHeaderID int64
	staffID     int64
	entryDate   string
	title       string
	description sql.NullString
	formLink    sql.NullString
}

type legacyEntryEvidence struct {
	staffID    sql.NullInt64
	evType     string
	fileLink   string
	uploadDate sql.NullString
}

// migrateLegacySubHeaderEntries copies every SubHeaderEntryTable row into
// ActivityTable (and its EntryEvidenceTable rows into EvidenceTable),
// preserving the new ActivityID so evidence lands on the right row. Buffers
// each query's rows into a slice before issuing further statements on the
// same *sql.Tx — SQLite pins a transaction to one connection, so a second
// query while the first's Rows are still open would deadlock.
func migrateLegacySubHeaderEntries(tx *sql.Tx) error {
	rows, err := tx.Query(`
		SELECT e.EntryID, sh.FK_ProcID, e.FK_SubHeaderID, e.FK_StaffID,
		       COALESCE(e.EntryDate,''), e.Title, e.Description, e.FormLink
		FROM SubHeaderEntryTable e
		JOIN SubHeaderTable sh ON sh.SubHeaderID = e.FK_SubHeaderID`)
	if err != nil {
		return err
	}
	var entries []legacySubHeaderEntry
	for rows.Next() {
		var le legacySubHeaderEntry
		if err := rows.Scan(&le.entryID, &le.procID, &le.subHeaderID, &le.staffID,
			&le.entryDate, &le.title, &le.description, &le.formLink); err != nil {
			rows.Close()
			return err
		}
		entries = append(entries, le)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()

	for _, le := range entries {
		res, err := tx.Exec(`
			INSERT INTO ActivityTable
				(FK_ProcID, FK_StaffID, ActivityType, ActivityTitle, ActivitySummary, ActivityDate, FK_SubHeaderID, FormLink)
			VALUES (?, ?, 'Monitoring/Inspection', ?, ?, ?, ?, ?)`,
			le.procID, le.staffID, le.title, le.description, le.entryDate, le.subHeaderID, le.formLink)
		if err != nil {
			return err
		}
		newActivityID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if err := migrateLegacyEntryEvidence(tx, le.entryID, newActivityID); err != nil {
			return err
		}
	}
	return nil
}

func migrateLegacyEntryEvidence(tx *sql.Tx, oldEntryID, newActivityID int64) error {
	erows, err := tx.Query(`
		SELECT FK_StaffID, EvidenceType, FileLink, UploadDate
		FROM EntryEvidenceTable WHERE FK_EntryID = ?`, oldEntryID)
	if err != nil {
		return err
	}
	var evs []legacyEntryEvidence
	for erows.Next() {
		var ev legacyEntryEvidence
		if err := erows.Scan(&ev.staffID, &ev.evType, &ev.fileLink, &ev.uploadDate); err != nil {
			erows.Close()
			return err
		}
		evs = append(evs, ev)
	}
	if err := erows.Err(); err != nil {
		return err
	}
	erows.Close()

	for _, ev := range evs {
		etype := ev.evType
		if etype == "Document" {
			etype = "ReportAttachment"
		}
		if _, err := tx.Exec(`
			INSERT INTO EvidenceTable (FK_IDActivity, FK_StaffID, EvidenceType, FileLink, UploadDate)
			VALUES (?, ?, ?, ?, ?)`, newActivityID, ev.staffID, etype, ev.fileLink, ev.uploadDate); err != nil {
			return err
		}
	}
	return nil
}

// EnsureTestLogKindColumn adds HealthProcedureTable.TestLogKind to a SQLite
// database that predates it. The MySQL database gets the same column from
// migrations/mysql/016_test_log_kind.sql, which is run by hand. The column is
// nullable: a program with no value shows no test log tab (see
// screening.KindLabel for the set of valid values).
func EnsureTestLogKindColumn(db *sql.DB) error {
	has, err := hasColumn(db, "HealthProcedureTable", "TestLogKind")
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	if _, err := db.Exec(`ALTER TABLE HealthProcedureTable ADD COLUMN TestLogKind TEXT`); err != nil {
		return err
	}
	// Backfill the two programs the old TEST_LOG_BY_PROGRAM map (formerly in
	// web/static/app.js) used to grant a test log tab by name, so upgrading
	// an existing database doesn't silently drop those tabs.
	_, err = db.Exec(`
		UPDATE HealthProcedureTable SET TestLogKind = 'alcohol' WHERE HealthProcedure = 'DrugAndAlcoholTesting';
		UPDATE HealthProcedureTable SET TestLogKind = 'fatigue' WHERE HealthProcedure = 'FatigueManagementProgram';
	`)
	return err
}

// EnsureTestLogTitleColumn adds HealthProcedureTable.TestLogTitle to a
// SQLite database that predates it. The MySQL database gets the same column
// from migrations/mysql/017_test_log_title.sql, which is run by hand. The
// column is nullable: an empty value falls back to the test log kind's
// default tab label (see screening.KindLabel) instead of a custom one, so
// this lets any program using a given kind show its own tab title (e.g. a
// second alcohol-testing program that wants a differently worded tab)
// without changing which underlying data the tab reads.
func EnsureTestLogTitleColumn(db *sql.DB) error {
	has, err := hasColumn(db, "HealthProcedureTable", "TestLogTitle")
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	_, err = db.Exec(`ALTER TABLE HealthProcedureTable ADD COLUMN TestLogTitle TEXT`)
	return err
}

// EnsureAuditLockedColumn adds SubHeaderTable.AuditLocked to a SQLite
// database that predates it. The MySQL database gets the same column from
// migrations/mysql/018_audit_documents.sql, which is run by hand. A locked
// sub-header's entries can be created but never edited or deleted (see
// subHeaderLocked and its callers in subheaders.go) — this is what makes a
// sub-header suitable for legally-sensitive records (e.g. the Drug and
// Alcohol program's consent/positive-refusal/suspicion documents) without
// needing a separate table: any program can lock any of its sub-headers.
func EnsureAuditLockedColumn(db *sql.DB) error {
	has, err := hasColumn(db, "SubHeaderTable", "AuditLocked")
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	_, err = db.Exec(`ALTER TABLE SubHeaderTable ADD COLUMN AuditLocked INTEGER NOT NULL DEFAULT 0`)
	return err
}

// EnsureEntrySubmissionLinkColumn adds ActivityTable.FK_SubmissionID to a
// SQLite database that predates it. The MySQL database gets the same column
// from migrations/mysql/018_audit_documents.sql, which is run by hand. It
// optionally ties a sub-header entry (most usefully, a locked one) to the
// exact DrugAlcoholScreening row it documents, instead of relying on a
// human matching donor name/date between the two.
func EnsureEntrySubmissionLinkColumn(db *sql.DB) error {
	has, err := hasColumn(db, "ActivityTable", "FK_SubmissionID")
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	_, err = db.Exec(`ALTER TABLE ActivityTable ADD COLUMN FK_SubmissionID INTEGER REFERENCES DrugAlcoholScreening(SubmissionID)`)
	return err
}

// EnsureEvidenceFileHashColumn adds EvidenceTable.FileHash to a SQLite
// database that predates it. The MySQL database gets the same column from
// migrations/mysql/018_audit_documents.sql, which is run by hand. It's a
// SHA-256 of the uploaded content recorded at upload time, so a later
// mismatch between a stored file and this hash reveals tampering or
// corruption. Populated for sub-header entry uploads (see
// saveEvidenceWithHash); left NULL for older/regular-activity evidence.
func EnsureEvidenceFileHashColumn(db *sql.DB) error {
	has, err := hasColumn(db, "EvidenceTable", "FileHash")
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	_, err = db.Exec(`ALTER TABLE EvidenceTable ADD COLUMN FileHash TEXT`)
	return err
}

func hasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
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

func hasTable(db *sql.DB, table string) (bool, error) {
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}
