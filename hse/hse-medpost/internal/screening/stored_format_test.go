package screening

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hse-medpost/internal/db"
)

// accepted decodes the "accepted" list from an upload response.
func accepted(t *testing.T, body []byte) []string {
	t.Helper()
	var out struct {
		Accepted []string `json:"accepted"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out.Accepted
}

// The browser sends toISOString() (24 characters, milliseconds, "Z"). It must
// be stored in the 19-character UTC layout, and SubmittedAt must be UTC even
// when the server clock is not.
func TestDrugAlcoholUploadStoresTimestampsInUTCLayout(t *testing.T) {
	database, _ := openScreeningTestDB(t)
	api := newAPI(database)
	api.Now = func() time.Time {
		return time.Date(2026, 10, 7, 18, 30, 0, 0, time.FixedZone("WIT", 9*60*60))
	}
	token := login(t, api)
	body := map[string]any{
		"form": formDrugAlcohol, "formVersion": "20270228",
		"records": []any{map[string]any{"id": "ts-1", "created_at": "2026-10-07T09:00:00.000Z"}},
	}
	rec := postJSON(t, api.SubmitDrugAlcohol, "/api/screening/drug-alcohol", body, token)
	if got := accepted(t, rec.Body.Bytes()); len(got) != 1 {
		t.Fatalf("accepted = %v, want [ts-1]", got)
	}
	var createdAt, submittedAt string
	if err := database.QueryRow(`SELECT CreatedAt, SubmittedAt FROM DrugAlcoholScreening WHERE ClientID = 'ts-1'`).Scan(&createdAt, &submittedAt); err != nil {
		t.Fatal(err)
	}
	if createdAt != "2026-10-07T09:00:00" || submittedAt != "2026-10-07T09:30:00" {
		t.Fatalf("CreatedAt = %q, SubmittedAt = %q, want 2026-10-07T09:00:00 and 2026-10-07T09:30:00", createdAt, submittedAt)
	}
}

func TestFatigueUploadStoresTimestampsInUTCLayout(t *testing.T) {
	database, _ := openScreeningTestDB(t)
	api := newAPI(database)
	token := login(t, api)
	body := map[string]any{
		"form": formFatigue, "formVersion": "20271006",
		"records": []any{map[string]any{"id": "ts-f1", "created_at": "2026-10-07T18:00:00+09:00"}},
	}
	rec := postJSON(t, api.SubmitFatigue, "/api/screening/fatigue", body, token)
	if got := accepted(t, rec.Body.Bytes()); len(got) != 1 {
		t.Fatalf("accepted = %v, want [ts-f1]", got)
	}
	var createdAt, submittedAt string
	if err := database.QueryRow(`SELECT CreatedAt, SubmittedAt FROM FatigueScreening WHERE ClientID = 'ts-f1'`).Scan(&createdAt, &submittedAt); err != nil {
		t.Fatal(err)
	}
	if createdAt != "2026-10-07T09:00:00" || submittedAt != "2026-10-07T09:30:00" {
		t.Fatalf("CreatedAt = %q, SubmittedAt = %q, want 2026-10-07T09:00:00 and 2026-10-07T09:30:00", createdAt, submittedAt)
	}
}

// An unparsable created_at is rejected like any other invalid record: it is
// not listed as accepted, so the form keeps it queued, and the good records
// in the same batch are still stored.
func TestUploadRejectsUnparsableCreatedAt(t *testing.T) {
	database, _ := openScreeningTestDB(t)
	api := newAPI(database)
	token := login(t, api)

	drug := map[string]any{
		"form": formDrugAlcohol, "formVersion": "20270228",
		"records": []any{
			map[string]any{"id": "bad", "created_at": "yesterday"},
			map[string]any{"id": "good", "created_at": "2026-10-07T09:00:00.000Z"},
		},
	}
	rec := postJSON(t, api.SubmitDrugAlcohol, "/api/screening/drug-alcohol", drug, token)
	if got := accepted(t, rec.Body.Bytes()); len(got) != 1 || got[0] != "good" {
		t.Fatalf("drug accepted = %v, want [good]", got)
	}

	fatigue := map[string]any{
		"form": formFatigue, "formVersion": "20271006",
		"records": []any{
			map[string]any{"id": "bad", "created_at": "2026-13-45T99:00:00Z"},
			map[string]any{"id": "good", "created_at": "2026-10-07T09:00:00.000Z"},
		},
	}
	rec = postJSON(t, api.SubmitFatigue, "/api/screening/fatigue", fatigue, token)
	if got := accepted(t, rec.Body.Bytes()); len(got) != 1 || got[0] != "good" {
		t.Fatalf("fatigue accepted = %v, want [good]", got)
	}
}

// The form sends the raw hours it scores from (sleep_24h = X, sleep_48h = Y,
// awake_hours = Z); Score24h and friends hold the points computed from them.
func TestFatigueUploadStoresRawSleepHours(t *testing.T) {
	database, _ := openScreeningTestDB(t)
	api := newAPI(database)
	token := login(t, api)
	body := map[string]any{
		"form": formFatigue, "formVersion": "20271006",
		"records": []any{map[string]any{
			"id": "h-1", "sleep_24h": 5.5, "sleep_48h": 11, "awake_hours": 16,
			"score_24h": 2, "score_48h": 2, "score_awake": 2, "score_total": 6,
		}},
	}
	rec := postJSON(t, api.SubmitFatigue, "/api/screening/fatigue", body, token)
	if got := accepted(t, rec.Body.Bytes()); len(got) != 1 {
		t.Fatalf("accepted = %v, want [h-1]", got)
	}
	var x, y, z string
	if err := database.QueryRow(`SELECT Sleep24h, Sleep48h, AwakeHours FROM FatigueScreening WHERE ClientID = 'h-1'`).Scan(&x, &y, &z); err != nil {
		t.Fatal(err)
	}
	if x != "5.5" || y != "11" || z != "16" {
		t.Fatalf("hours = %q %q %q, want \"5.5\" \"11\" \"16\"", x, y, z)
	}
}

// A record that omits the hours (an older form version) still uploads, with
// blank hour columns, like every other optional field.
func TestFatigueUploadWithoutHoursStoresBlanks(t *testing.T) {
	database, _ := openScreeningTestDB(t)
	api := newAPI(database)
	token := login(t, api)
	body := map[string]any{
		"form": formFatigue, "formVersion": "20271006",
		"records": []any{map[string]any{"id": "h-2", "name": "Budi"}},
	}
	postJSON(t, api.SubmitFatigue, "/api/screening/fatigue", body, token)
	var x, y, z string
	if err := database.QueryRow(`SELECT Sleep24h, Sleep48h, AwakeHours FROM FatigueScreening WHERE ClientID = 'h-2'`).Scan(&x, &y, &z); err != nil {
		t.Fatal(err)
	}
	if x != "" || y != "" || z != "" {
		t.Fatalf("hours = %q %q %q, want all blank", x, y, z)
	}
}

func openBareDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open("sqlite", filepath.Join(t.TempDir(), "rename.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func tableExists(t *testing.T, database *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

// A database created before the rename keeps its rows, and a table that
// points at the old name (ActivityTable.FK_SubmissionID in the real schema)
// is rewritten to point at the new one.
func TestEnsureScreeningTableNamesRenamesLegacyTables(t *testing.T) {
	database := openBareDB(t)
	for _, stmt := range []string{
		`CREATE TABLE DrugAlcoholSubmission (SubmissionID INTEGER PRIMARY KEY AUTOINCREMENT, ClientID TEXT)`,
		`CREATE TABLE FatigueSubmission (SubmissionID INTEGER PRIMARY KEY AUTOINCREMENT, ClientID TEXT)`,
		`CREATE TABLE Child (ID INTEGER PRIMARY KEY, FK_SubmissionID INTEGER REFERENCES DrugAlcoholSubmission(SubmissionID))`,
		`INSERT INTO DrugAlcoholSubmission (ClientID) VALUES ('old-row')`,
	} {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := EnsureScreeningTableNames(database); err != nil {
		t.Fatal(err)
	}
	for _, old := range []string{"DrugAlcoholSubmission", "FatigueSubmission"} {
		if tableExists(t, database, old) {
			t.Errorf("table %s still exists after the rename", old)
		}
	}
	var clientID string
	if err := database.QueryRow(`SELECT ClientID FROM DrugAlcoholScreening`).Scan(&clientID); err != nil || clientID != "old-row" {
		t.Fatalf("renamed table rows = %q, %v; want the old row kept", clientID, err)
	}
	if !tableExists(t, database, "FatigueScreening") {
		t.Fatal("FatigueScreening was not created by the rename")
	}
	var childSQL string
	if err := database.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'Child'`).Scan(&childSQL); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(childSQL, "DrugAlcoholScreening") || strings.Contains(childSQL, "REFERENCES DrugAlcoholSubmission") {
		t.Fatalf("Child still references the old name: %s", childSQL)
	}
}

// On a fresh database, or one already renamed, the rename does nothing, so it
// is safe to run on every start.
func TestEnsureScreeningTableNamesIsIdempotent(t *testing.T) {
	database := openBareDB(t)
	if err := EnsureScreeningTableNames(database); err != nil {
		t.Fatalf("fresh database: %v", err)
	}
	if _, err := database.Exec(`CREATE TABLE DrugAlcoholScreening (SubmissionID INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := EnsureScreeningTableNames(database); err != nil {
		t.Fatalf("already renamed: %v", err)
	}
}

// Both names present means someone created the new table by hand while the old
// one still holds rows. Guessing which to keep could lose test results, so the
// start-up stops with a message instead.
func TestEnsureScreeningTableNamesRefusesWhenBothExist(t *testing.T) {
	database := openBareDB(t)
	for _, stmt := range []string{
		`CREATE TABLE DrugAlcoholSubmission (SubmissionID INTEGER PRIMARY KEY)`,
		`CREATE TABLE DrugAlcoholScreening (SubmissionID INTEGER PRIMARY KEY)`,
	} {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := EnsureScreeningTableNames(database); err == nil {
		t.Fatal("want an error when both the old and the new table exist")
	}
}
