package entrymcu

import (
	"testing"
	"time"

	"hse-medpost/internal/db"
)

// Tests isTerminalResult, checkUpStatusFor, certifiableResult, and
// displayHcValidityStatus. No HTTP handler and no database write.
// Owner instruction, 2026-10-29: "unfit, temporary unfit, need follow up,
// incomplete is open"; only Fit and Fit with note close. An earlier
// instruction still holds: do not revise stored values.
func TestOpenCloseFollowsResult(t *testing.T) {
	closed := []string{"Fit", "FitWithNote"}
	open := []string{"Unfit", "TemporaryUnfit", "NeedFollowUp", "IncompleteResult", ""}
	for _, result := range closed {
		if !isTerminalResult(result) || checkUpStatusFor(isTerminalResult(result)) != "Close" {
			t.Fatalf("%s should close the checkup", result)
		}
	}
	for _, result := range open {
		if isTerminalResult(result) || checkUpStatusFor(isTerminalResult(result)) != "Open" {
			t.Fatalf("%s should leave the checkup open", result)
		}
	}
	if !certifiableResult("Fit") || !certifiableResult("FitWithNote") {
		t.Fatal("Fit and Fit with note are the certificate results")
	}
	for _, result := range []string{"Unfit", "TemporaryUnfit", "NeedFollowUp", "IncompleteResult"} {
		if certifiableResult(result) {
			t.Fatalf("%s must not get a certificate", result)
		}
	}
}

func TestStandingIgnoresStoredCloseForOpenResults(t *testing.T) {
	closeStatus := "Close"
	until := time.Now().AddDate(0, 0, 90).Format(dateOnly)
	today := time.Now()
	for _, result := range []string{"Unfit", "TemporaryUnfit", "NeedFollowUp", "IncompleteResult"} {
		if got := displayHcValidityStatus(nil, &closeStatus, &until, &result, today); got != "" {
			t.Fatalf("%s stored Close still showed standing %q", result, got)
		}
	}
	// An explicit Archived / Exit stamp is still honoured for any result.
	archived, unfit := "Archived / Exit", "Unfit"
	if got := displayHcValidityStatus(&archived, &closeStatus, &until, &unfit, today); got != "Archived / Exit" {
		t.Fatalf("stored Archived / Exit = %q", got)
	}
	fit := "Fit"
	if got := displayHcValidityStatus(nil, &closeStatus, &until, &fit, today); got != "Valid" {
		t.Fatalf("Fit standing = %q", got)
	}
}

// The 023 / 036 migrations bring Unfit records saved while Unfit still closed into line with the
// rule, and touch nothing else.
func TestUnfitIsOpenMigrationRevisesOnlyStoredUnfitRows(t *testing.T) {
	database := openExportTestDB(t)
	if _, err := database.Exec(`INSERT INTO Patient (Name, DateOfBirth, Gender) VALUES ('Ani Test', '1990-01-02', 'Female')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO McuRecord (FK_PatientID, Timestamp, ReviewDate, CheckUpID, ResultCriteria, CheckUpStatus, IsTerminal) VALUES
		(1, '2026-01-01T00:00:00', '2026-01-01', 'UNFIT-OLD', 'Unfit', 'Close', 1),
		(1, '2026-01-02T00:00:00', '2026-01-02', 'UNFIT-OPEN', 'Unfit', 'Open', 0),
		(1, '2026-01-03T00:00:00', '2026-01-03', 'FIT', 'Fit', 'Close', 1),
		(1, '2026-01-04T00:00:00', '2026-01-04', 'TEMP', 'TemporaryUnfit', 'Open', 0)`); err != nil {
		t.Fatal(err)
	}
	sqlText, err := db.LoadMigrationFile("../../migrations/sqlite/023_unfit_is_open.sql")
	if err != nil {
		t.Fatal(err)
	}
	for run := 1; run <= 2; run++ { // it runs at every start, so a second run must change nothing
		if err := db.Migrate(database, sqlText); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
	}
	want := map[string]string{"UNFIT-OLD": "Open|0", "UNFIT-OPEN": "Open|0", "FIT": "Close|1", "TEMP": "Open|0"}
	rows, err := database.Query(`SELECT CheckUpID, CheckUpStatus || '|' || IsTerminal FROM McuRecord`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id, got string
		if err := rows.Scan(&id, &got); err != nil {
			t.Fatal(err)
		}
		if got != want[id] {
			t.Errorf("%s = %s, want %s", id, got, want[id])
		}
		n++
	}
	if n != len(want) {
		t.Errorf("read %d rows, want %d", n, len(want))
	}
}
