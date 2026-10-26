package entrymcu

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"hse-medpost/internal/db"
)

func TestCheckupPurposeAndWorkLocationSeed(t *testing.T) {
	database := openLookupDB(t)
	if _, err := database.Exec(`CREATE TABLE CheckUpCriteriaTable (
		CheckUpCriteriaID INTEGER PRIMARY KEY AUTOINCREMENT,
		Name TEXT NOT NULL UNIQUE,
		Status TEXT NOT NULL DEFAULT 'Active'
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE TaskCategoryTable (
		TaskCategoryID INTEGER PRIMARY KEY AUTOINCREMENT,
		Name TEXT NOT NULL UNIQUE,
		Status TEXT NOT NULL DEFAULT 'Active'
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO CheckUpCriteriaTable (Name) VALUES ('Pre Employment'), ('Annual'), ('Bi-annual'), ('Fit for Task')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO TaskCategoryTable (Name) VALUES ('Remote Site Worker'), ('Food Handler')`); err != nil {
		t.Fatal(err)
	}
	if err := EnsureLookupTables(database); err != nil {
		t.Fatal(err)
	}
	if err := EnsureCheckUpCriteriaSeed(database); err != nil {
		t.Fatal(err)
	}
	if err := EnsureRemoteTaskCategoriesRemoved(database); err != nil {
		t.Fatal(err)
	}
	if err := EnsureReviewResultSeed(database); err != nil {
		t.Fatal(err)
	}
	if err := EnsureWorkLocationSeed(database); err != nil {
		t.Fatal(err)
	}

	if got := countQuery(t, database, `SELECT COUNT(*) FROM CheckUpCriteriaTable WHERE Name = 'PreEmployment'`); got != 1 {
		t.Fatalf("PreEmployment count = %d", got)
	}
	if got := countQuery(t, database, `SELECT COUNT(*) FROM CheckUpCriteriaTable WHERE Name = 'Bi-Annual'`); got != 1 {
		t.Fatalf("Bi-Annual count = %d", got)
	}
	if got := countQuery(t, database, `SELECT COUNT(*) FROM CheckUpCriteriaTable WHERE Name = 'ConsultationResult'`); got != 1 {
		t.Fatalf("ConsultationResult count = %d", got)
	}
	if got := countQuery(t, database, `SELECT COUNT(*) FROM CheckUpCriteriaTable WHERE Name = 'Fit for Task'`); got != 1 {
		t.Fatalf("Fit for Task count = %d", got)
	}
	if got := countQuery(t, database, `SELECT COUNT(*) FROM TaskCategoryTable WHERE Name = 'Remote Site Worker'`); got != 0 {
		t.Fatalf("remote task still present: %d", got)
	}
	if got := countQuery(t, database, `SELECT COUNT(*) FROM TaskCategoryTable WHERE Name = 'Food Handler'`); got != 1 {
		t.Fatalf("food handler count = %d", got)
	}
	if got := countQuery(t, database, `SELECT COUNT(*) FROM ReviewResultCriteriaTable`); got != len(reviewResultNames) {
		t.Fatalf("review results = %d", got)
	}
	if got := countQuery(t, database, `SELECT COUNT(*) FROM WorkLocationTable WHERE Name = 'NonRemote'`); got != 1 {
		t.Fatalf("work location count = %d", got)
	}
}

func TestDuplicateCheckupPurposesMerge(t *testing.T) {
	database := openLookupDB(t)
	if _, err := database.Exec(`CREATE TABLE CheckUpCriteriaTable (
		CheckUpCriteriaID INTEGER PRIMARY KEY AUTOINCREMENT,
		Name TEXT NOT NULL UNIQUE,
		Status TEXT NOT NULL DEFAULT 'Active'
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE McuRecord (
		McuRecordID INTEGER PRIMARY KEY,
		FK_CheckUpCriteriaID INTEGER
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO CheckUpCriteriaTable (CheckUpCriteriaID, Name, Status) VALUES
			(1, 'Fit for Task', 'Active'),
			(2, 'FitForTask', 'Archived'),
			(3, 'Follow Up', 'Active'),
			(4, 'FollowUp', 'Archived'),
			(5, 'PreEmployment', 'Active'),
			(6, 'Pre Employment', 'Active');
		INSERT INTO McuRecord (McuRecordID, FK_CheckUpCriteriaID) VALUES
			(10, 2),
			(11, 4),
			(12, 6);
	`); err != nil {
		t.Fatal(err)
	}

	if err := EnsureCheckUpCriteriaSeed(database); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"Fit for Task", "FitForTask", "Follow Up", "FollowUp", "PreEmployment", "Pre Employment"} {
		if got := countQuery(t, database, `SELECT COUNT(*) FROM CheckUpCriteriaTable WHERE Name = '`+name+`'`); got != 1 {
			t.Fatalf("%s count = %d", name, got)
		}
	}
	var fitID, followID, preID int64
	if err := database.QueryRow(`SELECT FK_CheckUpCriteriaID FROM McuRecord WHERE McuRecordID = 10`).Scan(&fitID); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT FK_CheckUpCriteriaID FROM McuRecord WHERE McuRecordID = 11`).Scan(&followID); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT FK_CheckUpCriteriaID FROM McuRecord WHERE McuRecordID = 12`).Scan(&preID); err != nil {
		t.Fatal(err)
	}
	if fitID != 2 || followID != 4 || preID != 6 {
		t.Fatalf("records point at %d, %d, %d", fitID, followID, preID)
	}
	var archived string
	if err := database.QueryRow(`SELECT Status FROM CheckUpCriteriaTable WHERE Name = 'FitForTask'`).Scan(&archived); err != nil {
		t.Fatal(err)
	}
	if archived != "Archived" {
		t.Fatalf("FitForTask status = %s", archived)
	}
}

func TestCheckupPurposeSeedKeepsInUseRow(t *testing.T) {
	database := openLookupDB(t)
	if _, err := database.Exec(`CREATE TABLE CheckUpCriteriaTable (
		CheckUpCriteriaID INTEGER PRIMARY KEY AUTOINCREMENT,
		Name TEXT NOT NULL UNIQUE,
		Status TEXT NOT NULL DEFAULT 'Active'
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE McuRecord (
		McuRecordID INTEGER PRIMARY KEY,
		FK_CheckUpCriteriaID INTEGER
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO CheckUpCriteriaTable (CheckUpCriteriaID, Name, Status) VALUES (3, 'Bi-Annual', 'Active');
		INSERT INTO McuRecord (McuRecordID, FK_CheckUpCriteriaID) VALUES (1, 3);
	`); err != nil {
		t.Fatal(err)
	}
	if err := EnsureCheckUpCriteriaSeed(database); err != nil {
		t.Fatal(err)
	}
	if got := countQuery(t, database, `SELECT COUNT(*) FROM CheckUpCriteriaTable WHERE Name = 'Bi-Annual'`); got != 1 {
		t.Fatalf("Bi-Annual count = %d", got)
	}
	var id int64
	if err := database.QueryRow(`SELECT FK_CheckUpCriteriaID FROM McuRecord WHERE McuRecordID = 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id != 3 {
		t.Fatalf("checkup purpose id = %d", id)
	}
}

func TestRenameReviewResultUpdatesRecords(t *testing.T) {
	database := openLookupDB(t)
	if err := EnsureLookupTables(database); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE McuRecord (
		McuRecordID INTEGER PRIMARY KEY,
		ResultCriteria TEXT NULL CHECK (ResultCriteria IN ('Fit','FitWithNote','Unfit','TemporaryUnfit','NeedFollowUp'))
	)`); err != nil {
		t.Fatal(err)
	}
	if err := EnsureResultCriteriaEditable(database); err != nil {
		t.Fatal(err)
	}
	if err := EnsureReviewResultSeed(database); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO McuRecord (ResultCriteria) VALUES ('Fit'), ('IncompleteResult')`); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := database.QueryRow(`SELECT ReviewResultCriteriaID FROM ReviewResultCriteriaTable WHERE Name = 'Fit'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	api := &API{DB: database}
	req := httptest.NewRequest(http.MethodPut, "/api/review-result-criteria/1", strings.NewReader(`{"name":"FitForDuty"}`))
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	rec := httptest.NewRecorder()
	api.UpdateReviewResultCriteria(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var fitRows, other string
	if err := database.QueryRow(`SELECT ResultCriteria FROM McuRecord WHERE McuRecordID = 1`).Scan(&fitRows); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT ResultCriteria FROM McuRecord WHERE McuRecordID = 2`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if fitRows != "FitForDuty" || other != "IncompleteResult" {
		t.Fatalf("stored results %q and %q", fitRows, other)
	}
}

func TestOccupationalRiskTriggerWords(t *testing.T) {
	database := openLookupDB(t)
	if _, err := database.Exec(`CREATE TABLE TaskCategoryTable (
		TaskCategoryID INTEGER PRIMARY KEY AUTOINCREMENT,
		Name TEXT NOT NULL UNIQUE,
		Status TEXT NOT NULL DEFAULT 'Active'
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO TaskCategoryTable (Name) VALUES ('Food Handler')`); err != nil {
		t.Fatal(err)
	}
	if err := EnsureTaskCategoryTriggerWordSeed(database); err != nil {
		t.Fatal(err)
	}
	var seeded string
	if err := database.QueryRow(`SELECT TriggerWords FROM TaskCategoryTable WHERE Name = 'Food Handler'`).Scan(&seeded); err != nil {
		t.Fatal(err)
	}
	if seeded != "food handler, cook" {
		t.Fatalf("seeded triggers = %q", seeded)
	}
	if _, err := database.Exec(`UPDATE TaskCategoryTable SET TriggerWords = 'chef' WHERE Name = 'Food Handler'`); err != nil {
		t.Fatal(err)
	}
	if err := EnsureTaskCategoryTriggerWordSeed(database); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT TriggerWords FROM TaskCategoryTable WHERE Name = 'Food Handler'`).Scan(&seeded); err != nil {
		t.Fatal(err)
	}
	if seeded != "chef" {
		t.Fatalf("admin trigger overwritten: %q", seeded)
	}

	api := &API{DB: database}
	req := httptest.NewRequest(http.MethodPost, "/api/task-categories", strings.NewReader(`{"name":"Welder Risk","triggerWords":"Welder, welder,  "}`))
	rec := httptest.NewRecorder()
	api.CreateTaskCategory(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status %d body %s", rec.Code, rec.Body.String())
	}
	var created string
	if err := database.QueryRow(`SELECT TriggerWords FROM TaskCategoryTable WHERE Name = 'Welder Risk'`).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if created != "welder" {
		t.Fatalf("created triggers = %q", created)
	}

	var id int64
	if err := database.QueryRow(`SELECT TaskCategoryID FROM TaskCategoryTable WHERE Name = 'Food Handler'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	put := httptest.NewRequest(http.MethodPut, "/api/task-categories/1", strings.NewReader(`{"name":"Food Handler","triggerWords":"cook, food handler"}`))
	put.SetPathValue("id", strconv.FormatInt(id, 10))
	updated := httptest.NewRecorder()
	api.UpdateTaskCategory(updated, put)
	if updated.Code != http.StatusOK {
		t.Fatalf("update status %d body %s", updated.Code, updated.Body.String())
	}
	if err := database.QueryRow(`SELECT TriggerWords FROM TaskCategoryTable WHERE TaskCategoryID = ?`, id).Scan(&seeded); err != nil {
		t.Fatal(err)
	}
	if seeded != "cook, food handler" {
		t.Fatalf("updated triggers = %q", seeded)
	}
}

func TestOccupationalRiskRevision(t *testing.T) {
	database := openLookupDB(t)
	if _, err := database.Exec(`CREATE TABLE TaskCategoryTable (
		TaskCategoryID INTEGER PRIMARY KEY AUTOINCREMENT,
		Name TEXT NOT NULL UNIQUE,
		Status TEXT NOT NULL DEFAULT 'Active'
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE McuRecord (
		McuRecordID INTEGER PRIMARY KEY,
		TaskCategoryNames TEXT
	)`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"Food Handler",
		"Health Professionals",
		"Other Professional (Pilot, Seafarer, Diver)",
		"Remote Site Visitor",
		"Confined Space or SCBA/Respirator Users",
		"Professional Drivers",
	} {
		if _, err := database.Exec(`INSERT INTO TaskCategoryTable (Name) VALUES (?)`, name); err != nil {
			t.Fatal(err)
		}
	}
	if err := EnsureTaskCategoryTriggerWordsColumn(database); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(
		`UPDATE TaskCategoryTable SET TriggerWords = ? WHERE Name = ?`,
		"health professional, nurse, doctor, paramedic, medic", "Health Professionals",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO McuRecord (McuRecordID, TaskCategoryNames) VALUES
		(1, 'Health Professionals; Food Handler'),
		(2, 'Other Professional (Pilot, Seafarer, Diver)')`); err != nil {
		t.Fatal(err)
	}
	if err := EnsureOccupationalRiskRevision(database); err != nil {
		t.Fatal(err)
	}
	if err := EnsureTaskCategoryTriggerWordSeed(database); err != nil {
		t.Fatal(err)
	}

	var status, words string
	if err := database.QueryRow(`SELECT Status, COALESCE(TriggerWords,'') FROM TaskCategoryTable WHERE Name = 'Health care worker'`).Scan(&status, &words); err != nil {
		t.Fatal(err)
	}
	if status != "Active" || words != "health professional, nurse, doctor, paramedic, medic" {
		t.Fatalf("health care worker status=%s words=%q", status, words)
	}
	if got := countQuery(t, database, `SELECT COUNT(*) FROM TaskCategoryTable WHERE Name = 'Health Professionals'`); got != 0 {
		t.Fatalf("Health Professionals still present: %d", got)
	}
	for _, name := range retiredOccupationalRiskNames {
		var archived string
		if err := database.QueryRow(`SELECT Status FROM TaskCategoryTable WHERE Name = ?`, name).Scan(&archived); err != nil {
			t.Fatal(name, err)
		}
		if archived != "Archived" {
			t.Fatalf("%s status = %s", name, archived)
		}
	}
	checks := map[string]string{
		"Seafarer":           "seafarer",
		"Confined space":     "confined space",
		"Respirator or SCBA": "scba, respirator",
	}
	for name, want := range checks {
		var got, st string
		if err := database.QueryRow(`SELECT Status, COALESCE(TriggerWords,'') FROM TaskCategoryTable WHERE Name = ?`, name).Scan(&st, &got); err != nil {
			t.Fatal(name, err)
		}
		if st != "Active" || got != want {
			t.Fatalf("%s status=%s words=%q", name, st, got)
		}
	}
	var renamed, kept string
	if err := database.QueryRow(`SELECT TaskCategoryNames FROM McuRecord WHERE McuRecordID = 1`).Scan(&renamed); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT TaskCategoryNames FROM McuRecord WHERE McuRecordID = 2`).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if renamed != "Health care worker; Food Handler" {
		t.Fatalf("renamed tasks = %q", renamed)
	}
	if kept != "Other Professional (Pilot, Seafarer, Diver)" {
		t.Fatalf("archived tasks rewritten: %q", kept)
	}
	if err := EnsureOccupationalRiskRevision(database); err != nil {
		t.Fatal(err)
	}
	if err := EnsureTaskCategoryTriggerWordSeed(database); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COALESCE(TriggerWords,'') FROM TaskCategoryTable WHERE Name = 'Health care worker'`).Scan(&words); err != nil {
		t.Fatal(err)
	}
	if words != "health professional, nurse, doctor, paramedic, medic" {
		t.Fatalf("second run overwrote triggers: %q", words)
	}
}

func openLookupDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open("sqlite", filepath.Join(t.TempDir(), "lookup.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func countQuery(t *testing.T, database *sql.DB, query string) int {
	t.Helper()
	var n int
	if err := database.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
