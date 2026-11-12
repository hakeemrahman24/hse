package correctiveaction

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"hse-medpost/internal/db"
)

// openTestDB builds the minimum schema the store touches, from the real migration files, so a column
// renamed in a migration fails here instead of in production.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	for _, name := range []string{"001_sqlite.sql", "003_subheaders.sql", "004_company.sql", "005_patient.sql", "028_corrective_action.sql", "030_corrective_action_document.sql"} {
		sqlText, err := db.LoadMigrationFile(filepath.Join("..", "..", "migrations", "sqlite", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(database, sqlText); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	return database
}

type fixture struct {
	store      *Store
	staffID    int64
	procID     int64
	patientID  int64
	patient2ID int64
}

// newFixture seeds one staff member, one Active program ("Malaria") and two employees, one of whom
// shares a name prefix with the other so search tests can tell them apart.
func newFixture(t *testing.T) fixture {
	t.Helper()
	database := openTestDB(t)
	exec := func(query string, args ...any) int64 {
		t.Helper()
		res, err := database.Exec(query, args...)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	staff := exec(`INSERT INTO MedpostStaffTable (StaffName, Position) VALUES ('Nurse A', 'Nurse')`)
	proc := exec(`INSERT INTO HealthProcedureTable (HealthProcedure) VALUES ('Malaria')`)
	company := exec(`INSERT INTO Company (Name) VALUES ('PT Contoh')`)
	p1 := exec(`INSERT INTO Patient (Name, EmployeeID, FK_CompanyID, WorkPosition) VALUES ('Budi Santoso', 'E-001', ?, 'Cook')`, company)
	p2 := exec(`INSERT INTO Patient (Name, EmployeeID, WorkPosition) VALUES ('Budiman Hakim', 'E-002', 'Driver')`)
	return fixture{store: &Store{DB: database}, staffID: staff, procID: proc, patientID: p1, patient2ID: p2}
}

func (f fixture) input() Input {
	return Input{
		ProcID:         f.procID,
		InspectionDate: "2026-11-01",
		Location:       "Mess hall",
		Finding:        "Food stored on the floor",
		Recommendation: "Use shelves 15 cm above the floor",
		PicPatientID:   f.patientID,
		DueDate:        "2026-11-20",
		Severity:       "Major",
	}
}

func (f fixture) create(t *testing.T) int64 {
	t.Helper()
	id, err := f.store.Create(f.input(), f.staffID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return id
}

func isValidation(err error) bool {
	var v ValidationError
	return errors.As(err, &v)
}

func TestCreateCopiesPicFromPatientAndLogsHistory(t *testing.T) {
	f := newFixture(t)
	id := f.create(t)

	got, err := f.store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.PicName != "Budi Santoso" || got.PicEmployeeID != "E-001" {
		t.Fatalf("PIC = %q / %q, want a copy of the picked employee", got.PicName, got.PicEmployeeID)
	}
	if got.Status != "Open" || got.Severity != "Major" || got.ProgramName != "Malaria" {
		t.Fatalf("unexpected row %+v", got)
	}
	hist, err := f.store.History(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 || hist[0].Field != "created" || hist[0].StaffName != "Nurse A" {
		t.Fatalf("history = %+v, want one 'created' row by Nurse A", hist)
	}
}

func TestCreateRejectsBadInput(t *testing.T) {
	f := newFixture(t)
	archived, err := f.store.DB.Exec(`INSERT INTO HealthProcedureTable (HealthProcedure, Status) VALUES ('Old', 'Archived')`)
	if err != nil {
		t.Fatal(err)
	}
	archivedID, _ := archived.LastInsertId()

	cases := []struct {
		name   string
		mutate func(*Input)
	}{
		{"no finding", func(in *Input) { in.Finding = "  " }},
		{"no recommendation", func(in *Input) { in.Recommendation = "" }},
		{"no PIC picked", func(in *Input) { in.PicPatientID = 0 }},
		{"PIC not in the employee list", func(in *Input) { in.PicPatientID = 9999 }},
		{"bad inspection date", func(in *Input) { in.InspectionDate = "01/11/2026" }},
		{"missing inspection date", func(in *Input) { in.InspectionDate = "" }},
		{"bad due date", func(in *Input) { in.DueDate = "2026-13-40" }},
		{"due before inspection", func(in *Input) { in.DueDate = "2026-10-01" }},
		{"bad severity", func(in *Input) { in.Severity = "Huge" }},
		{"unknown program", func(in *Input) { in.ProcID = 9999 }},
		{"archived program", func(in *Input) { in.ProcID = archivedID }},
		{"unknown source activity", func(in *Input) { in.ActivityID = 9999 }},
		{"Closed status on create", func(in *Input) { in.Status = "Closed" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := f.input()
			c.mutate(&in)
			if _, err := f.store.Create(in, f.staffID); !isValidation(err) {
				t.Fatalf("Create error = %v, want a ValidationError", err)
			}
		})
	}
}

func TestCreateDefaultsSeverityAndStatus(t *testing.T) {
	f := newFixture(t)
	in := f.input()
	in.Severity, in.Status = "", ""
	id, err := f.store.Create(in, f.staffID)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := f.store.Get(id)
	if got.Severity != "Minor" || got.Status != "Open" {
		t.Fatalf("defaults = %s / %s, want Minor / Open", got.Severity, got.Status)
	}
}

func TestCreateRejectsActivityFromAnotherProgram(t *testing.T) {
	f := newFixture(t)
	other, _ := f.store.DB.Exec(`INSERT INTO HealthProcedureTable (HealthProcedure) VALUES ('Dengue')`)
	otherProc, _ := other.LastInsertId()
	act, err := f.store.DB.Exec(`INSERT INTO ActivityTable (FK_ProcID, FK_StaffID, ActivityType, ActivityTitle) VALUES (?, ?, 'Monitoring/Inspection', 'Round')`, otherProc, f.staffID)
	if err != nil {
		t.Fatal(err)
	}
	actID, _ := act.LastInsertId()
	in := f.input()
	in.ActivityID = actID
	if _, err := f.store.Create(in, f.staffID); !isValidation(err) {
		t.Fatalf("error = %v, want a ValidationError (activity belongs to another program)", err)
	}
}

func TestUpdateLogsOneHistoryRowPerChangedField(t *testing.T) {
	f := newFixture(t)
	id := f.create(t)

	in := f.input()
	in.Finding = "Food and chemicals stored on the floor"
	in.Severity = "Critical"
	in.PicPatientID = 0 // unchanged PIC: the page does not resend the id
	if err := f.store.Update(id, in, f.staffID); err != nil {
		t.Fatal(err)
	}

	got, _ := f.store.Get(id)
	if got.Finding != in.Finding || got.Severity != "Critical" {
		t.Fatalf("not updated: %+v", got)
	}
	if got.PicName != "Budi Santoso" {
		t.Fatalf("PIC = %q, want it kept when no new employee is picked", got.PicName)
	}
	hist, _ := f.store.History(id)
	fields := map[string][2]string{}
	for _, h := range hist {
		fields[h.Field] = [2]string{h.OldValue, h.NewValue}
	}
	if len(hist) != 3 { // created + finding + severity
		t.Fatalf("history rows = %d (%+v), want 3", len(hist), hist)
	}
	if fields["severity"] != [2]string{"Major", "Critical"} {
		t.Fatalf("severity change = %v", fields["severity"])
	}
}

func TestUpdateReplacesPicWhenAnotherEmployeeIsPicked(t *testing.T) {
	f := newFixture(t)
	id := f.create(t)
	in := f.input()
	in.PicPatientID = f.patient2ID
	if err := f.store.Update(id, in, f.staffID); err != nil {
		t.Fatal(err)
	}
	got, _ := f.store.Get(id)
	if got.PicName != "Budiman Hakim" || got.PicEmployeeID != "E-002" {
		t.Fatalf("PIC = %q / %q", got.PicName, got.PicEmployeeID)
	}
	in.PicPatientID = 9999
	if err := f.store.Update(id, in, f.staffID); !isValidation(err) {
		t.Fatalf("error = %v, want a ValidationError for an unlisted employee", err)
	}
}

func TestUpdateStatusOnlyOpenOrInProgress(t *testing.T) {
	f := newFixture(t)
	id := f.create(t)
	in := f.input()
	in.Status = "In Progress"
	if err := f.store.Update(id, in, f.staffID); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.store.Get(id); got.Status != "In Progress" {
		t.Fatalf("status = %s", got.Status)
	}
	in.Status = "Closed"
	if err := f.store.Update(id, in, f.staffID); !isValidation(err) {
		t.Fatalf("error = %v, want closing to go through Close only", err)
	}
	if err := f.store.Update(9999, f.input(), f.staffID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestPhotoLimitIsThreePerPhase(t *testing.T) {
	f := newFixture(t)
	id := f.create(t)
	for i := 0; i < 3; i++ {
		if _, err := f.store.AddPhoto(id, "Before", "/data/b.jpg", f.staffID); err != nil {
			t.Fatalf("photo %d: %v", i+1, err)
		}
	}
	if _, err := f.store.AddPhoto(id, "Before", "/data/b4.jpg", f.staffID); !isValidation(err) {
		t.Fatalf("4th Before photo error = %v, want a ValidationError", err)
	}
	if _, err := f.store.AddPhoto(id, "After", "/data/a.jpg", f.staffID); err != nil {
		t.Fatalf("After photos have their own limit: %v", err)
	}
	if _, err := f.store.AddPhoto(id, "Sideways", "/data/x.jpg", f.staffID); !isValidation(err) {
		t.Fatalf("bad phase error = %v", err)
	}
	if _, err := f.store.AddPhoto(9999, "Before", "/data/x.jpg", f.staffID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown finding error = %v", err)
	}
	got, _ := f.store.Get(id)
	if len(got.BeforePhotos) != 3 || len(got.AfterPhotos) != 1 {
		t.Fatalf("photos before/after = %d/%d, want 3/1", len(got.BeforePhotos), len(got.AfterPhotos))
	}
	if got.BeforePhotos[0].SortOrder != 1 || got.BeforePhotos[2].SortOrder != 3 {
		t.Fatalf("sort order = %+v", got.BeforePhotos)
	}
}

func TestCloseNeedsActionTakenAndAfterPhoto(t *testing.T) {
	f := newFixture(t)
	id := f.create(t)

	if err := f.store.Close(id, "2026-11-10", "Shelves installed", f.staffID); !isValidation(err) {
		t.Fatalf("no after photo: error = %v, want a ValidationError", err)
	}
	if _, err := f.store.AddPhoto(id, "After", "/data/a.jpg", f.staffID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(id, "2026-11-10", "   ", f.staffID); !isValidation(err) {
		t.Fatalf("no action taken: error = %v", err)
	}
	if err := f.store.Close(id, "2026-10-01", "Shelves installed", f.staffID); !isValidation(err) {
		t.Fatalf("closing before inspection: error = %v", err)
	}
	if err := f.store.Close(id, "2026-11-10", "Shelves installed", f.staffID); err != nil {
		t.Fatal(err)
	}
	got, _ := f.store.Get(id)
	if got.Status != "Closed" || got.ClosingDate != "2026-11-10" || got.ActionTaken != "Shelves installed" || got.ClosedByName != "Nurse A" {
		t.Fatalf("closed row = %+v", got)
	}
	if got.DaysOpen != 9 {
		t.Fatalf("days open = %d, want 9 (inspection 11-01 to closing 11-10)", got.DaysOpen)
	}
	if err := f.store.Close(id, "2026-11-11", "again", f.staffID); !isValidation(err) {
		t.Fatalf("closing twice: error = %v", err)
	}
}

func TestCloseDefaultsToTodayWhenDateBlank(t *testing.T) {
	f := newFixture(t)
	in := f.input()
	in.InspectionDate = "2020-01-01"
	in.DueDate = ""
	id, err := f.store.Create(in, f.staffID)
	if err != nil {
		t.Fatal(err)
	}
	f.store.AddPhoto(id, "After", "/data/a.jpg", f.staffID)
	if err := f.store.Close(id, "", "Done", f.staffID); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.store.Get(id); len(got.ClosingDate) != 10 {
		t.Fatalf("closing date = %q, want today filled in", got.ClosingDate)
	}
}

func TestReopenClearsClosingFieldsAndKeepsHistory(t *testing.T) {
	f := newFixture(t)
	id := f.create(t)
	f.store.AddPhoto(id, "After", "/data/a.jpg", f.staffID)
	if err := f.store.Reopen(id, f.staffID); !isValidation(err) {
		t.Fatalf("reopening an open finding: error = %v", err)
	}
	if err := f.store.Close(id, "2026-11-10", "Shelves installed", f.staffID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Reopen(id, f.staffID); err != nil {
		t.Fatal(err)
	}
	got, _ := f.store.Get(id)
	if got.Status != "Open" || got.ClosingDate != "" || got.ClosedByName != "" {
		t.Fatalf("reopened row = %+v", got)
	}
	hist, _ := f.store.History(id)
	var closed, reopened bool
	for _, h := range hist {
		closed = closed || h.Field == "closed"
		reopened = reopened || h.Field == "reopened"
	}
	if !closed || !reopened {
		t.Fatalf("history %+v must keep the close and the reopen", hist)
	}
}

func TestDeletePhotoCannotRemoveLastAfterPhotoOfClosedFinding(t *testing.T) {
	f := newFixture(t)
	id := f.create(t)
	photoID, _ := f.store.AddPhoto(id, "After", "/data/a.jpg", f.staffID)
	f.store.Close(id, "2026-11-10", "Done", f.staffID)
	if _, err := f.store.DeletePhoto(photoID, f.staffID); !isValidation(err) {
		t.Fatalf("error = %v, want the last After photo of a closed finding protected", err)
	}
	second, _ := f.store.AddPhoto(id, "After", "/data/a2.jpg", f.staffID)
	path, err := f.store.DeletePhoto(second, f.staffID)
	if err != nil || path != "/data/a2.jpg" {
		t.Fatalf("DeletePhoto = %q, %v", path, err)
	}
	if _, err := f.store.DeletePhoto(9999, f.staffID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v", err)
	}
}

func TestDeleteRemovesRowsAndReturnsFilePaths(t *testing.T) {
	f := newFixture(t)
	id := f.create(t)
	f.store.AddPhoto(id, "Before", "/data/b.jpg", f.staffID)
	f.store.AddPhoto(id, "After", "/data/a.jpg", f.staffID)

	paths, err := f.store.Delete(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("paths = %v, want both photo files returned for the caller to remove", paths)
	}
	if _, err := f.store.Get(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete error = %v", err)
	}
	for _, table := range []string{"CorrectiveActionPhotoTable", "CorrectiveActionHistoryTable"} {
		var n int
		if err := f.store.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s rows = %d, %v; want 0 (deleted with the finding)", table, n, err)
		}
	}
	if _, err := f.store.Delete(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete error = %v", err)
	}
}

func TestListFiltersAndComputedFields(t *testing.T) {
	f := newFixture(t)
	overdueIn := f.input()
	overdueIn.DueDate = "2026-11-02"
	overdueID, _ := f.store.Create(overdueIn, f.staffID)

	futureIn := f.input()
	futureIn.Finding = "Cracked tiles near the sink"
	futureIn.Severity = "Minor"
	futureIn.DueDate = "2999-01-01"
	futureIn.InspectionDate = "2026-11-05"
	futureID, _ := f.store.Create(futureIn, f.staffID)

	f.store.AddPhoto(overdueID, "Before", "/data/b.jpg", f.staffID)

	all, err := f.store.List(Filter{})
	if err != nil || len(all) != 2 {
		t.Fatalf("List = %d rows, %v; want 2", len(all), err)
	}
	if all[0].ID != futureID {
		t.Fatalf("newest inspection date first: got id %d, want %d", all[0].ID, futureID)
	}
	byID := map[int64]Action{all[0].ID: all[0], all[1].ID: all[1]}
	if !byID[overdueID].IsOverdue || byID[futureID].IsOverdue {
		t.Fatalf("overdue flags = %v / %v", byID[overdueID].IsOverdue, byID[futureID].IsOverdue)
	}
	if len(byID[overdueID].BeforePhotos) != 1 {
		t.Fatalf("photos must be loaded with the list: %+v", byID[overdueID])
	}

	check := func(name string, flt Filter, want ...int64) {
		t.Helper()
		got, err := f.store.List(flt)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(got) != len(want) {
			t.Fatalf("%s: got %d rows, want %d", name, len(got), len(want))
		}
		for i, id := range want {
			if got[i].ID != id {
				t.Fatalf("%s: row %d = id %d, want %d", name, i, got[i].ID, id)
			}
		}
	}
	check("overdue", Filter{Status: "Overdue"}, overdueID)
	check("open", Filter{Status: "Open"}, futureID, overdueID)
	check("severity", Filter{Severity: "Minor"}, futureID)
	check("search finding", Filter{Q: "tiles"}, futureID)
	check("search PIC", Filter{Q: "budi sant"}, futureID, overdueID)
	check("from date", Filter{From: "2026-11-03"}, futureID)
	check("to date", Filter{To: "2026-11-03"}, overdueID)
	check("program", Filter{ProcID: f.procID}, futureID, overdueID)
	check("other program", Filter{ProcID: 9999})
	check("limit", Filter{Limit: 1}, futureID)

	f.store.AddPhoto(overdueID, "After", "/data/a.jpg", f.staffID)
	f.store.Close(overdueID, "2026-11-04", "Fixed", f.staffID)
	check("closed is never overdue", Filter{Status: "Overdue"})
	check("closed", Filter{Status: "Closed"}, overdueID)
}

func TestSearchEmployees(t *testing.T) {
	f := newFixture(t)

	got, err := f.store.SearchEmployees("budi", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "Budi Santoso" || got[0].Company != "PT Contoh" || got[0].Position != "Cook" || got[0].EmployeeID != "E-001" {
		t.Fatalf("results = %+v", got)
	}
	byCode, _ := f.store.SearchEmployees("E-002", 10)
	if len(byCode) != 1 || byCode[0].Name != "Budiman Hakim" {
		t.Fatalf("search by employee id = %+v", byCode)
	}
	if limited, _ := f.store.SearchEmployees("budi", 1); len(limited) != 1 {
		t.Fatalf("limit not applied: %+v", limited)
	}
	for _, blank := range []string{"", "  ", "a"} {
		if res, err := f.store.SearchEmployees(blank, 10); err != nil || len(res) != 0 {
			t.Fatalf("query %q: %+v, %v; want no results for a query under 2 characters", blank, res, err)
		}
	}
}

// A finding raised from an activity carries that activity's title and date, so the details can say
// "From activity: ..." without a second request.
func TestGetShowsSourceActivityTitleAndDate(t *testing.T) {
	f := newFixture(t)
	res, err := f.store.DB.Exec(`INSERT INTO ActivityTable (FK_ProcID, FK_StaffID, ActivityType, ActivityTitle, ActivityDate) VALUES (?, ?, 'Monitoring/Inspection', 'Kitchen round', '2026-10-30')`, f.procID, f.staffID)
	if err != nil {
		t.Fatal(err)
	}
	actID, _ := res.LastInsertId()
	in := f.input()
	in.ActivityID = actID
	id, err := f.store.Create(in, f.staffID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.ActivityID != actID || got.ActivityTitle != "Kitchen round" || got.ActivityDate != "2026-10-30" {
		t.Fatalf("activity = %d %q %q, want %d 'Kitchen round' 2026-10-30", got.ActivityID, got.ActivityTitle, got.ActivityDate, actID)
	}
	plain, _ := f.store.Get(f.create(t))
	if plain.ActivityID != 0 || plain.ActivityTitle != "" {
		t.Fatalf("an unlinked finding shows activity %d %q", plain.ActivityID, plain.ActivityTitle)
	}
}
