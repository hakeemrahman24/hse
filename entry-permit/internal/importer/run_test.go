package importer

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"entry-permit/internal/dates"
	"entry-permit/internal/db"
	"entry-permit/internal/permitlog"
	"entry-permit/migrations"
)

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	d.SetMaxOpenConns(1)
	if err := db.Migrate(d, migrations.FS, "sqlite"); err != nil {
		t.Fatal(err)
	}
	return d
}

var clock = dates.FixedClock("2026-11-10 09:00:00")

// sheet puts four header rows above the data rows, as the real sheet has.
func sheet(data ...[]string) [][]string {
	rows := [][]string{{"title"}, {}, {}, {"Timestamp", "RequestID"}}
	return append(rows, data...)
}

func run(t *testing.T, d *sql.DB, rows [][]string, dry bool) Report {
	t.Helper()
	rep, err := Run(context.Background(), d, rows, Options{HeaderRows: 4, DryRun: dry, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func count(t *testing.T, d *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := d.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func two() [][]string {
	return sheet(
		sheetRow(nil),
		sheetRow(map[int]string{1: "WEP-BBBBB", 92: "LEP-BBBBB", 5: "Ayu Lestari", 91: "Expired", 93: "01-15-26"}),
	)
}

func TestFirstRunStoresPermitsAndStepsAndRecordsWhatItImported(t *testing.T) {
	d := newDB(t)
	rep := run(t, d, two(), false)
	if rep.Inserted != 2 || rep.Replaced+rep.Unchanged+len(rep.Rejected) != 0 {
		t.Fatalf("report %+v", rep)
	}
	if rep.ByStatus["Approved"] != 1 || rep.ByStatus["Expired"] != 1 {
		t.Errorf("by status %v", rep.ByStatus)
	}
	if n := count(t, d, `SELECT COUNT(*) FROM permit`); n != 2 {
		t.Errorf("%d permits stored", n)
	}
	if n := count(t, d, `SELECT COUNT(*) FROM review_step WHERE subject_id = 'WEP-AAAAA'`); n != 2 {
		t.Errorf("%d review steps for the Approved permit", n)
	}
	if n := count(t, d, `SELECT COUNT(*) FROM import_row WHERE source = 'wep'`); n != 2 {
		t.Errorf("%d import records", n)
	}
	// the stored permit works with the existing lookups (card, ticket page, gate sync)
	got, ok, err := (permitlog.Store{DB: d}).Access(context.Background(), "LEP-AAAAA")
	if err != nil || !ok || got.DOB != "1988-03-05" || got.Status != "Approved" {
		t.Errorf("access %+v ok=%v err=%v", got, ok, err)
	}
}

func TestDryRunReportsTheSameCountsAndWritesNothing(t *testing.T) {
	d := newDB(t)
	dry := run(t, d, two(), true)
	if !dry.DryRun || dry.Inserted != 2 {
		t.Fatalf("dry report %+v", dry)
	}
	if n := count(t, d, `SELECT (SELECT COUNT(*) FROM permit) + (SELECT COUNT(*) FROM review_step) + (SELECT COUNT(*) FROM import_row)`); n != 0 {
		t.Fatalf("a dry run wrote %d rows", n)
	}
	real := run(t, d, two(), false)
	if real.Inserted != dry.Inserted || len(real.Rejected) != len(dry.Rejected) || len(real.Warnings) != len(dry.Warnings) {
		t.Errorf("dry %+v and real %+v disagree", dry, real)
	}
}

func TestRunningTwiceChangesNothingTheSecondTime(t *testing.T) {
	d := newDB(t)
	run(t, d, two(), false)
	rep := run(t, d, two(), false)
	if rep.Unchanged != 2 || rep.Inserted != 0 || rep.Replaced != 0 {
		t.Fatalf("second run %+v", rep)
	}
	if n := count(t, d, `SELECT COUNT(*) FROM review_step`); n != 4 {
		t.Errorf("%d review steps after two runs, want 4: two permits with two decisions each, none duplicated", n)
	}
}

func TestAChangedSheetRowReplacesThePermitAndItsStepsOnce(t *testing.T) {
	d := newDB(t)
	run(t, d, two(), false)
	changed := sheet(sheetRow(map[int]string{83: "second look: fine", 91: "Resigned"}), two()[5])
	rep := run(t, d, changed, false)
	if rep.Replaced != 1 || rep.Unchanged != 1 {
		t.Fatalf("report %+v", rep)
	}
	var status, note string
	if err := d.QueryRow(`SELECT status FROM permit WHERE request_id = 'WEP-AAAAA'`).Scan(&status); err != nil || status != "Resigned" {
		t.Errorf("status %q err %v", status, err)
	}
	if err := d.QueryRow(`SELECT note FROM review_step WHERE subject_id = 'WEP-AAAAA' AND stage = 'stage1'`).Scan(&note); err != nil || note != "second look: fine" {
		t.Errorf("note %q err %v", note, err)
	}
	if n := count(t, d, `SELECT COUNT(*) FROM review_step WHERE subject_id = 'WEP-AAAAA'`); n != 2 {
		t.Errorf("%d steps, want 2 (replaced, not added to)", n)
	}
}

func TestAPermitTheNewAppHasChangedSinceTheImportIsNeverOverwritten(t *testing.T) {
	// After cutover the sheet is frozen, but a re-run during the freeze window must not undo
	// work already done in the new system.
	d := newDB(t)
	run(t, d, two(), false)
	if _, err := d.Exec(`UPDATE permit SET status = 'Resigned' WHERE request_id = 'WEP-AAAAA'`); err != nil {
		t.Fatal(err)
	}
	rep := run(t, d, sheet(sheetRow(map[int]string{83: "a changed note"}), two()[5]), false)
	if rep.Replaced != 0 || len(rep.Rejected) != 1 || !strings.Contains(rep.Rejected[0].Msg, "changed in the new system") {
		t.Fatalf("report %+v", rep)
	}
	var status string
	_ = d.QueryRow(`SELECT status FROM permit WHERE request_id = 'WEP-AAAAA'`).Scan(&status)
	if status != "Resigned" {
		t.Errorf("status %q: the new system's change was overwritten", status)
	}
}

func TestAnExistingPermitThatWasNotImportedIsLeftAloneAndReported(t *testing.T) {
	d := newDB(t)
	native := permitlog.NewPermit{RequestID: "WEP-AAAAA", Status: "PendingStage1", WorkerName: "Made in the new app", RequesterName: "R", RequesterEmail: "r@x.id",
		WorkLocation: "Onshore", CreatedAt: "2026-11-09 08:00:00"}
	if err := (permitlog.Store{DB: d}).Create(context.Background(), native); err != nil {
		t.Fatal(err)
	}
	rep := run(t, d, two(), false)
	if rep.Inserted != 1 || len(rep.Rejected) != 1 || rep.Rejected[0].ID != "WEP-AAAAA" || !strings.Contains(rep.Rejected[0].Msg, "already exists") {
		t.Fatalf("report %+v", rep)
	}
	var name string
	_ = d.QueryRow(`SELECT worker_name FROM permit WHERE request_id = 'WEP-AAAAA'`).Scan(&name)
	if name != "Made in the new app" {
		t.Errorf("name %q: the new app's permit was overwritten", name)
	}
}

func TestDuplicateIdsAndLEPNumbersInTheSheetAreRejectedAfterTheFirst(t *testing.T) {
	d := newDB(t)
	rows := sheet(
		sheetRow(nil),
		sheetRow(map[int]string{5: "Second With Same Id"}), // same request id
		sheetRow(map[int]string{1: "WEP-CCCCC"}),           // same LEP number as the first
		sheetRow(map[int]string{1: "WEP-DDDDD", 92: "LEP-DDDDD"}),
	)
	rep := run(t, d, rows, false)
	if rep.Inserted != 2 || len(rep.Rejected) != 2 {
		t.Fatalf("report %+v", rep)
	}
	for _, r := range rep.Rejected {
		if r.Row != 6 && r.Row != 7 {
			t.Errorf("rejected row %d", r.Row)
		}
	}
}

func TestOneBadRowIsRejectedAndTheRestAreImported(t *testing.T) {
	d := newDB(t)
	rows := sheet(sheetRow(nil), sheetRow(map[int]string{1: "WEP-BADRW", 20: "Mars"}), sheetRow(map[int]string{1: "WEP-OKOKO", 92: "LEP-OKOKO"}))
	rep := run(t, d, rows, false)
	if rep.Inserted != 2 || len(rep.Rejected) != 1 {
		t.Fatalf("report %+v", rep)
	}
	r := rep.Rejected[0]
	if r.Row != 6 || r.ID != "WEP-BADRW" || !strings.Contains(r.Msg, "work location") {
		t.Errorf("reject %+v", r)
	}
}

func TestBlankRowsAreSkippedAndWarningsAreGroupedWithAFewExamples(t *testing.T) {
	d := newDB(t)
	var data [][]string
	for i, id := range []string{"WEP-W0001", "WEP-W0002", "WEP-W0003", "WEP-W0004", "WEP-W0005"} {
		data = append(data, sheetRow(map[int]string{1: id, 92: "LEP-W000" + string(rune('1'+i)), 14: "04 Oct 20266"}))
	}
	data = append(data, make([]string, 98), []string{}) // two blank rows
	rep := run(t, d, sheet(data...), false)
	if rep.Inserted != 5 || rep.Skipped != 2 {
		t.Fatalf("report %+v", rep)
	}
	w := rep.Warnings["mcu_valid_until"]
	if w.Count != 5 || len(w.Examples) != 3 {
		t.Errorf("warning group %+v: want 5 rows and 3 example ids", w)
	}
}

func runWith(t *testing.T, d *sql.DB, rows [][]string, opt Options) Report {
	t.Helper()
	opt.HeaderRows, opt.Clock = 4, clock
	rep, err := Run(context.Background(), d, rows, opt)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestABlankValidUntilIsComputedByTheApprovalRuleOnlyWhenAskedAndOnlyWhenBlank(t *testing.T) {
	// 18 Approved permits in the real sheet have an LEP number but an empty valid-until cell.
	// Final approval computes that date from the MCU, contract, visa and passport dates; the
	// importer can do the same, but only when the owner asks (--fill-valid-until).
	blank := sheet(sheetRow(map[int]string{93: ""}))
	d := newDB(t)
	if rep := runWith(t, d, blank, Options{DryRun: true}); len(rep.Rejected) != 1 || rep.Inserted != 0 {
		t.Fatalf("without the flag the row must be rejected: %+v", rep)
	}

	rep := runWith(t, d, blank, Options{FillValidUntil: true, Policy: policyDefault()})
	if rep.Inserted != 1 || len(rep.Rejected) != 0 {
		t.Fatalf("with the flag: %+v", rep)
	}
	var valid string
	if err := d.QueryRow(`SELECT lep_valid_until FROM permit WHERE request_id = 'WEP-AAAAA'`).Scan(&valid); err != nil || valid != "2026-09-20" {
		t.Errorf("computed valid-until %q (MCU 4 Oct 2026 less two weeks) err %v", valid, err)
	}
	if g := rep.Warnings["lep_valid_until_computed"]; g.Count != 1 || len(g.Examples) != 1 {
		t.Errorf("the computed date must be reported: %+v", rep.Warnings)
	}

	// a typo is not a blank: it stays rejected so someone looks at it
	d2 := newDB(t)
	typo := sheet(sheetRow(map[int]string{93: "09-20-266"}))
	if rep := runWith(t, d2, typo, Options{FillValidUntil: true, Policy: policyDefault()}); len(rep.Rejected) != 1 {
		t.Errorf("an unreadable date was filled in: %+v", rep)
	}
	// nothing to compute from: rejected with the rule's own reason
	d3 := newDB(t)
	none := sheet(sheetRow(map[int]string{93: "", 14: ""}))
	rep = runWith(t, d3, none, Options{FillValidUntil: true, Policy: policyDefault()})
	if len(rep.Rejected) != 1 || !strings.Contains(rep.Rejected[0].Msg, "validity") {
		t.Errorf("no dates at all: %+v", rep)
	}
}
