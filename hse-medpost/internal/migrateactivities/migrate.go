package migrateactivities

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"hse-medpost/internal/db"
)

// Options selects the mysqldump, the legacy upload tree, and a fresh destination.
// Callers: cmd/migrate-activities/main.go and migrate_test.go.
type Options struct {
	DumpPath      string
	SrcUploads    string
	SrcUploadRoot string
	DestDB        string
	DestUploads   string
	MigrationsDir string
}

// Result is the row counts written into the new database.
type Result struct {
	Programs   int
	Staff      int
	Users      int
	Activities int
	Evidence   int
	Documents  int
	SubHeaders int
	Files      int
}

// migrationFiles matches cmd/server/main.go. The importer applies them itself
// so the app is not started against an empty database (that would seed demo rows).
var migrationFiles = []string{
	"001_sqlite.sql",
	"002_position_role.sql",
	"003_subheaders.sql",
	"004_company.sql",
	"005_patient.sql",
	"006_entrymcu.sql",
	"007_mcu_certificate_log.sql",
	"008_mcu_email_notification.sql",
	"009_patient_name_index.sql",
	"010_project.sql",
}

// Run copies medical-activities into a fresh hse-medpost SQLite database.
// Primary keys are preserved. Sub-header entries become activities.
// FileLink values are rewritten to DestUploads, and _ClinicWide becomes _MedpostWide.
func Run(opts Options) (result Result, err error) {
	if opts.DumpPath == "" || opts.SrcUploads == "" || opts.DestDB == "" || opts.DestUploads == "" || opts.MigrationsDir == "" {
		return Result{}, fmt.Errorf("dump, source uploads, destination database, destination uploads, and migrations dir are required")
	}
	if opts.SrcUploadRoot == "" {
		opts.SrcUploadRoot = "/home/orangepi/projects/medical-activities/uploads"
	}
	opts.DestDB, err = filepath.Abs(opts.DestDB)
	if err != nil {
		return Result{}, err
	}
	opts.DestUploads, err = filepath.Abs(opts.DestUploads)
	if err != nil {
		return Result{}, err
	}
	if _, statErr := os.Stat(opts.DestDB); statErr == nil {
		return Result{}, fmt.Errorf("destination database already exists: %s", opts.DestDB)
	}
	if err = destDirEmpty(opts.DestUploads); err != nil {
		return Result{}, err
	}

	raw, err := os.ReadFile(opts.DumpPath)
	if err != nil {
		return Result{}, err
	}
	dump, err := ParseDump(string(raw))
	if err != nil {
		return Result{}, err
	}
	plan, err := buildPlan(dump, opts)
	if err != nil {
		return Result{}, err
	}

	createdUploads := false
	createdDB := false
	defer func() {
		if err == nil {
			return
		}
		if createdDB {
			_ = os.Remove(opts.DestDB)
		}
		if createdUploads {
			_ = os.RemoveAll(opts.DestUploads)
		}
	}()

	if err = os.MkdirAll(opts.DestUploads, 0o755); err != nil {
		return Result{}, err
	}
	createdUploads = true
	result.Files, err = copyUploads(opts.SrcUploads, opts.DestUploads)
	if err != nil {
		return Result{}, err
	}
	if err = plan.linksExist(); err != nil {
		return Result{}, err
	}

	database, err := db.Open("sqlite", opts.DestDB+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return Result{}, err
	}
	createdDB = true
	defer database.Close()
	if err = applyMigrations(database, opts.MigrationsDir); err != nil {
		return Result{}, err
	}
	if err = insertPlan(database, plan); err != nil {
		return Result{}, err
	}
	result.Programs = len(plan.programs)
	result.Staff = len(plan.staff)
	result.Users = len(plan.users)
	result.Activities = len(plan.activities)
	result.Evidence = len(plan.evidence)
	result.Documents = len(plan.documents)
	result.SubHeaders = len(plan.subheaders)
	return result, nil
}

func destDirEmpty(path string) error {
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("destination uploads is not empty: %s", path)
	}
	return nil
}

type fileLink struct {
	src  string
	dest string
}

type plan struct {
	programs    []Row
	staff       []Row
	positions   []Row
	users       []Row
	supervisors []Row
	subheaders  []Row
	activities  []activityRow
	evidence    []evidenceRow
	documents   []documentRow
	links       []fileLink
}

type activityRow struct {
	id, procID, staffID int64
	subHeaderID         sql.NullInt64
	activityType, title string
	summary, location   sql.NullString
	activityDate        sql.NullString
	notes, formLink     sql.NullString
}

type evidenceRow struct {
	id, activityID int64
	staffID        sql.NullInt64
	evidenceType   string
	fileLink       string
	uploadDate     sql.NullString
}

type documentRow struct {
	id            int64
	procID        sql.NullInt64
	documentsType string
	title         string
	formLink      sql.NullString
	fileLink      string
	notes         sql.NullString
}

func (p *plan) linksExist() error {
	for _, link := range p.links {
		info, err := os.Stat(link.dest)
		if err != nil || info.IsDir() {
			return fmt.Errorf("migrated file missing: %s", link.dest)
		}
	}
	return nil
}

func buildPlan(dump *Dump, opts Options) (*plan, error) {
	p := &plan{}
	var err error
	if p.programs, err = table(dump, "HealthProcedureTable"); err != nil {
		return nil, err
	}
	if p.staff, err = table(dump, "ClinicStaffTable"); err != nil {
		return nil, err
	}
	if p.positions, err = table(dump, "PositionRoleTable"); err != nil {
		return nil, err
	}
	if p.users, err = table(dump, "AppUserTable"); err != nil {
		return nil, err
	}
	if p.supervisors, err = table(dump, "SupervisorTable"); err != nil {
		return nil, err
	}
	if p.subheaders, err = table(dump, "SubHeaderTable"); err != nil {
		return nil, err
	}
	activities, err := table(dump, "ActivityTable")
	if err != nil {
		return nil, err
	}
	evidence, err := table(dump, "EvidenceTable")
	if err != nil {
		return nil, err
	}
	documents, err := table(dump, "DocumentTable")
	if err != nil {
		return nil, err
	}
	entries, err := table(dump, "SubHeaderEntryTable")
	if err != nil {
		return nil, err
	}
	entryEvidence, err := table(dump, "EntryEvidenceTable")
	if err != nil {
		return nil, err
	}

	procBySubHeader := map[int64]int64{}
	for _, row := range p.subheaders {
		id, err := row.Int("SubHeaderID")
		if err != nil {
			return nil, err
		}
		procID, err := row.Int("FK_ProcID")
		if err != nil {
			return nil, err
		}
		procBySubHeader[id] = procID
	}

	var maxActivity, maxEvidence int64
	for _, row := range activities {
		item, err := activityFromRow(row)
		if err != nil {
			return nil, err
		}
		if item.id > maxActivity {
			maxActivity = item.id
		}
		p.activities = append(p.activities, item)
	}
	for _, row := range evidence {
		item, link, err := evidenceFromRow(row, opts)
		if err != nil {
			return nil, err
		}
		if item.id > maxEvidence {
			maxEvidence = item.id
		}
		p.evidence = append(p.evidence, item)
		if link != nil {
			p.links = append(p.links, *link)
		}
	}
	for _, row := range documents {
		item, link, err := documentFromRow(row, opts)
		if err != nil {
			return nil, err
		}
		p.documents = append(p.documents, item)
		if link != nil {
			p.links = append(p.links, *link)
		}
	}

	entryActivity := map[int64]int64{}
	for _, row := range entries {
		folded, entryID, err := foldEntry(row, procBySubHeader, maxActivity)
		if err != nil {
			return nil, err
		}
		maxActivity = folded.id
		entryActivity[entryID] = folded.id
		p.activities = append(p.activities, folded)
	}
	for _, row := range entryEvidence {
		item, link, nextID, err := foldEntryEvidence(row, entryActivity, maxEvidence, opts)
		if err != nil {
			return nil, err
		}
		maxEvidence = nextID
		p.evidence = append(p.evidence, item)
		if link != nil {
			p.links = append(p.links, *link)
		}
	}

	if err = expectCounts(dump, p); err != nil {
		return nil, err
	}
	return p, nil
}

func foldEntry(row Row, procBySubHeader map[int64]int64, maxActivity int64) (activityRow, int64, error) {
	entryID, err := row.Int("EntryID")
	if err != nil {
		return activityRow{}, 0, err
	}
	subID, err := row.Int("FK_SubHeaderID")
	if err != nil {
		return activityRow{}, 0, err
	}
	procID, ok := procBySubHeader[subID]
	if !ok {
		return activityRow{}, 0, fmt.Errorf("sub-header entry %d references missing sub-header %d", entryID, subID)
	}
	staffID, err := row.Int("FK_StaffID")
	if err != nil {
		return activityRow{}, 0, err
	}
	date, err := dateOrNull(row.Cells["EntryDate"])
	if err != nil {
		return activityRow{}, 0, err
	}
	return activityRow{
		id:           maxActivity + 1,
		procID:       procID,
		staffID:      staffID,
		subHeaderID:  sql.NullInt64{Int64: subID, Valid: true},
		activityType: "Monitoring/Inspection",
		title:        row.Text("Title"),
		summary:      nullString(row.Cells["Description"]),
		activityDate: date,
		formLink:     nullString(row.Cells["FormLink"]),
	}, entryID, nil
}

func foldEntryEvidence(row Row, entryActivity map[int64]int64, maxEvidence int64, opts Options) (evidenceRow, *fileLink, int64, error) {
	entryID, err := row.Int("FK_EntryID")
	if err != nil {
		return evidenceRow{}, nil, 0, err
	}
	activityID, ok := entryActivity[entryID]
	if !ok {
		return evidenceRow{}, nil, 0, fmt.Errorf("entry evidence references missing entry %d", entryID)
	}
	etype := row.Text("EvidenceType")
	if etype == "Document" {
		etype = "ReportAttachment"
	}
	linkText, link, err := rewriteStoredLink(row.Text("FileLink"), opts)
	if err != nil {
		return evidenceRow{}, nil, 0, err
	}
	staffID, err := nullIntCell(row.Cells["FK_StaffID"])
	if err != nil {
		return evidenceRow{}, nil, 0, err
	}
	date, err := dateOrNull(row.Cells["UploadDate"])
	if err != nil {
		return evidenceRow{}, nil, 0, err
	}
	nextID := maxEvidence + 1
	return evidenceRow{
		id:           nextID,
		activityID:   activityID,
		staffID:      staffID,
		evidenceType: etype,
		fileLink:     linkText,
		uploadDate:   date,
	}, link, nextID, nil
}

func table(dump *Dump, name string) ([]Row, error) {
	rows, ok := dump.Tables[name]
	if !ok {
		return nil, fmt.Errorf("dump has no %s", name)
	}
	return rows, nil
}

func expectCounts(dump *Dump, p *plan) error {
	if len(p.activities) != len(dump.Tables["ActivityTable"])+len(dump.Tables["SubHeaderEntryTable"]) {
		return fmt.Errorf("activity count mismatch")
	}
	if len(p.evidence) != len(dump.Tables["EvidenceTable"])+len(dump.Tables["EntryEvidenceTable"]) {
		return fmt.Errorf("evidence count mismatch")
	}
	if len(p.programs) != len(dump.Tables["HealthProcedureTable"]) ||
		len(p.staff) != len(dump.Tables["ClinicStaffTable"]) ||
		len(p.users) != len(dump.Tables["AppUserTable"]) ||
		len(p.documents) != len(dump.Tables["DocumentTable"]) ||
		len(p.subheaders) != len(dump.Tables["SubHeaderTable"]) {
		return fmt.Errorf("row count mismatch")
	}
	return nil
}

func activityFromRow(row Row) (activityRow, error) {
	id, err := row.Int("ActivityID")
	if err != nil {
		return activityRow{}, err
	}
	procID, err := row.Int("FK_ProcID")
	if err != nil {
		return activityRow{}, err
	}
	staffID, err := row.Int("FK_StaffID")
	if err != nil {
		return activityRow{}, err
	}
	date, err := dateOrNull(row.Cells["ActivityDate"])
	if err != nil {
		return activityRow{}, err
	}
	return activityRow{
		id:           id,
		procID:       procID,
		staffID:      staffID,
		activityType: row.Text("ActivityType"),
		title:        row.Text("ActivityTitle"),
		summary:      nullString(row.Cells["ActivitySummary"]),
		location:     nullString(row.Cells["Location"]),
		activityDate: date,
		notes:        nullString(row.Cells["Notes"]),
	}, nil
}

func evidenceFromRow(row Row, opts Options) (evidenceRow, *fileLink, error) {
	id, err := row.Int("EvidenceID")
	if err != nil {
		return evidenceRow{}, nil, err
	}
	activityID, err := row.Int("FK_IDActivity")
	if err != nil {
		return evidenceRow{}, nil, err
	}
	staffID, err := nullIntCell(row.Cells["FK_StaffID"])
	if err != nil {
		return evidenceRow{}, nil, err
	}
	date, err := dateOrNull(row.Cells["UploadDate"])
	if err != nil {
		return evidenceRow{}, nil, err
	}
	linkText, link, err := rewriteStoredLink(row.Text("FileLink"), opts)
	if err != nil {
		return evidenceRow{}, nil, err
	}
	return evidenceRow{
		id:           id,
		activityID:   activityID,
		staffID:      staffID,
		evidenceType: row.Text("EvidenceType"),
		fileLink:     linkText,
		uploadDate:   date,
	}, link, nil
}

func documentFromRow(row Row, opts Options) (documentRow, *fileLink, error) {
	id, err := row.Int("DocumentID")
	if err != nil {
		return documentRow{}, nil, err
	}
	procID, err := nullIntCell(row.Cells["FK_ProcID"])
	if err != nil {
		return documentRow{}, nil, err
	}
	stored, link, err := rewriteStoredLink(row.Text("FileLink"), opts)
	if err != nil {
		return documentRow{}, nil, err
	}
	return documentRow{
		id:            id,
		procID:        procID,
		documentsType: row.Text("DocumentsType"),
		title:         row.Text("DocumentTitle"),
		formLink:      nullString(row.Cells["FormLink"]),
		fileLink:      stored,
		notes:         nullString(row.Cells["Notes"]),
	}, link, nil
}

func rewriteStoredLink(link string, opts Options) (string, *fileLink, error) {
	if link == "" || strings.HasPrefix(link, "placeholder:") {
		return link, nil, nil
	}
	srcRoot := strings.TrimRight(opts.SrcUploadRoot, "/")
	if !strings.HasPrefix(link, srcRoot+"/") {
		return "", nil, fmt.Errorf("FileLink is outside the upload root: %s", link)
	}
	rel := strings.TrimPrefix(link, srcRoot+"/")
	parts := strings.Split(rel, "/")
	src := filepath.Join(append([]string{opts.SrcUploads}, parts...)...)
	info, err := os.Stat(src)
	if err != nil || info.IsDir() {
		return "", nil, fmt.Errorf("source file missing: %s", src)
	}
	renamed := append([]string(nil), parts...)
	for i, part := range renamed {
		if part == "_ClinicWide" {
			renamed[i] = "_MedpostWide"
		}
	}
	dest := filepath.Join(append([]string{opts.DestUploads}, renamed...)...)
	return dest, &fileLink{src: src, dest: dest}, nil
}

func nullString(c Cell) sql.NullString {
	if c.Null {
		return sql.NullString{}
	}
	return sql.NullString{String: c.Text, Valid: true}
}

func nullIntCell(c Cell) (sql.NullInt64, error) {
	if c.Null || c.Text == "" {
		return sql.NullInt64{}, nil
	}
	n, err := strconvParse(c.Text)
	if err != nil {
		return sql.NullInt64{}, err
	}
	return sql.NullInt64{Int64: n, Valid: true}, nil
}

func dateOrNull(c Cell) (sql.NullString, error) {
	if c.Null || c.Text == "" {
		return sql.NullString{}, nil
	}
	if len(c.Text) != 10 {
		return sql.NullString{}, fmt.Errorf("date must be YYYY-MM-DD, got %q", c.Text)
	}
	return sql.NullString{String: c.Text, Valid: true}, nil
}

func strconvParse(s string) (int64, error) {
	var n int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not an integer: %s", s)
		}
		n = n*10 + int64(r-'0')
	}
	return n, nil
}

func copyUploads(src, dest string) (int, error) {
	count := 0
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		parts := strings.Split(rel, string(os.PathSeparator))
		for i, part := range parts {
			if part == "_ClinicWide" {
				parts[i] = "_MedpostWide"
			}
		}
		target := filepath.Join(append([]string{dest}, parts...)...)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := copyFile(path, target); err != nil {
			return err
		}
		count++
		return nil
	})
	return count, err
}

func copyFile(src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func applyMigrations(database *sql.DB, dir string) error {
	for _, name := range migrationFiles {
		sqlText, err := db.LoadMigrationFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if err := db.Migrate(database, sqlText); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

func insertPlan(database *sql.DB, p *plan) error {
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	steps := []func() error{
		func() error { return insertPrograms(tx, p.programs) },
		func() error { return insertStaff(tx, p.staff) },
		func() error { return insertPositions(tx, p.positions) },
		func() error { return insertUsers(tx, p.users) },
		func() error { return insertSupervisors(tx, p.supervisors) },
		func() error { return insertSubHeaders(tx, p.subheaders) },
		func() error { return insertActivities(tx, p.activities) },
		func() error { return insertEvidence(tx, p.evidence) },
		func() error { return insertDocuments(tx, p.documents) },
		func() error { return setSequences(tx) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func insertPrograms(tx *sql.Tx, rows []Row) error {
	for _, row := range rows {
		id, err := row.Int("ProcID")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT INTO HealthProcedureTable (ProcID, HealthProcedure, Status) VALUES (?, ?, ?)`,
			id, row.Text("HealthProcedure"), row.Text("Status"),
		); err != nil {
			return err
		}
	}
	return nil
}

func insertStaff(tx *sql.Tx, rows []Row) error {
	for _, row := range rows {
		id, err := row.Int("StaffID")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT INTO MedpostStaffTable (StaffID, StaffName, Position, Status) VALUES (?, ?, ?, ?)`,
			id, row.Text("StaffName"), nullString(row.Cells["Position"]), row.Text("Status"),
		); err != nil {
			return err
		}
	}
	return nil
}

func insertPositions(tx *sql.Tx, rows []Row) error {
	for _, row := range rows {
		id, err := row.Int("PositionID")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT INTO PositionRoleTable (PositionID, PositionName, Role) VALUES (?, ?, ?)`,
			id, row.Text("PositionName"), row.Text("Role"),
		); err != nil {
			return err
		}
	}
	return nil
}

func insertUsers(tx *sql.Tx, rows []Row) error {
	for _, row := range rows {
		id, err := row.Int("UserID")
		if err != nil {
			return err
		}
		staffID, err := row.Int("FK_StaffID")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT INTO AppUserTable (UserID, FK_StaffID, Username, PasswordHash, Role) VALUES (?, ?, ?, ?, ?)`,
			id, staffID, row.Text("Username"), row.Text("PasswordHash"), row.Text("Role"),
		); err != nil {
			return err
		}
	}
	return nil
}

func insertSupervisors(tx *sql.Tx, rows []Row) error {
	for _, row := range rows {
		id, err := row.Int("SupervisorID")
		if err != nil {
			return err
		}
		procID, err := row.Int("FK_ProcID")
		if err != nil {
			return err
		}
		staffID, err := row.Int("FK_StaffID")
		if err != nil {
			return err
		}
		start, err := dateOrNull(row.Cells["StartDate"])
		if err != nil {
			return err
		}
		end, err := dateOrNull(row.Cells["EndDate"])
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT INTO SupervisorTable (SupervisorID, FK_ProcID, FK_StaffID, StartDate, EndDate) VALUES (?, ?, ?, ?, ?)`,
			id, procID, staffID, start, end,
		); err != nil {
			return err
		}
	}
	return nil
}

func insertSubHeaders(tx *sql.Tx, rows []Row) error {
	for _, row := range rows {
		id, err := row.Int("SubHeaderID")
		if err != nil {
			return err
		}
		procID, err := row.Int("FK_ProcID")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT INTO SubHeaderTable (SubHeaderID, FK_ProcID, SubHeaderName, Status) VALUES (?, ?, ?, ?)`,
			id, procID, row.Text("SubHeaderName"), row.Text("Status"),
		); err != nil {
			return err
		}
	}
	return nil
}

func insertActivities(tx *sql.Tx, rows []activityRow) error {
	for _, row := range rows {
		if _, err := tx.Exec(`
			INSERT INTO ActivityTable (
				ActivityID, FK_ProcID, FK_StaffID, FK_SubHeaderID, ActivityType,
				ActivityTitle, ActivitySummary, Location, ActivityDate, Notes, FormLink
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			row.id, row.procID, row.staffID, row.subHeaderID, row.activityType,
			row.title, row.summary, row.location, row.activityDate, row.notes, row.formLink,
		); err != nil {
			return err
		}
	}
	return nil
}

func insertEvidence(tx *sql.Tx, rows []evidenceRow) error {
	for _, row := range rows {
		var link any
		if row.fileLink != "" {
			link = row.fileLink
		}
		if _, err := tx.Exec(`
			INSERT INTO EvidenceTable (EvidenceID, FK_IDActivity, FK_StaffID, EvidenceType, FileLink, UploadDate)
			VALUES (?, ?, ?, ?, ?, ?)`,
			row.id, row.activityID, row.staffID, row.evidenceType, link, row.uploadDate,
		); err != nil {
			return err
		}
	}
	return nil
}

func insertDocuments(tx *sql.Tx, rows []documentRow) error {
	for _, row := range rows {
		var link any
		if row.fileLink != "" {
			link = row.fileLink
		}
		if _, err := tx.Exec(`
			INSERT INTO DocumentTable (DocumentID, FK_ProcID, DocumentsType, DocumentTitle, FormLink, FileLink, Notes)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			row.id, row.procID, row.documentsType, row.title, row.formLink, link, row.notes,
		); err != nil {
			return err
		}
	}
	return nil
}

func setSequences(tx *sql.Tx) error {
	sequences := []struct{ table, column string }{
		{"HealthProcedureTable", "ProcID"},
		{"MedpostStaffTable", "StaffID"},
		{"PositionRoleTable", "PositionID"},
		{"AppUserTable", "UserID"},
		{"SupervisorTable", "SupervisorID"},
		{"SubHeaderTable", "SubHeaderID"},
		{"ActivityTable", "ActivityID"},
		{"EvidenceTable", "EvidenceID"},
		{"DocumentTable", "DocumentID"},
	}
	for _, seq := range sequences {
		var maxID sql.NullInt64
		query := fmt.Sprintf("SELECT MAX(%s) FROM %s", seq.column, seq.table)
		if err := tx.QueryRow(query).Scan(&maxID); err != nil {
			return err
		}
		if !maxID.Valid {
			continue
		}
		res, err := tx.Exec(`UPDATE sqlite_sequence SET seq = ? WHERE name = ?`, maxID.Int64, seq.table)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			if _, err := tx.Exec(`INSERT INTO sqlite_sequence(name, seq) VALUES(?, ?)`, seq.table, maxID.Int64); err != nil {
				return err
			}
		}
	}
	return nil
}
