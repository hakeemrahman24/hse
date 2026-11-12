// Command import-sheets loads the old Google Sheet into the new database.
//
//	import-sheets --file data/sheet-snapshot/2026-11-20/entry-permit.xlsx --dry-run
//	import-sheets --file data/sheet-snapshot/2026-11-20/entry-permit.xlsx
//
// It reads a local Excel download only (no Google account, no network). Always run with
// --dry-run first: it prints exactly what the real run would do and writes nothing. It is
// safe to run again: rows that did not change are skipped, rows that changed in the sheet are
// replaced, and a permit the new system has changed since is never overwritten. Rows that
// cannot be imported are listed with the reason, and the command exits with status 1 when
// there are any, so a script cannot miss them.
//
// It imports work permits, waivers and tickets (with their review decisions) in one
// transaction, in that order, because a ticket needs its permit or waiver. The gate's scan log
// and state are NOT imported: the gate Worker holds them and the Pi pulls them at the first
// sync.
//
// With --files <drive-files folder> it then copies each imported work permit's documents (ID,
// photo, visa, RPTKA, immigration and police letters, certificate, BPJS, contract) from the
// downloaded Drive folders into UPLOAD_DIR. The sheet holds Drive ids but the downloaded files
// carry names, so files are matched by worker name (and date where the name has one). Where a
// worker's files and requests cannot be paired with certainty nothing is attached and the
// request id is listed. It only fills empty columns, so it is safe to run again; with
// --dry-run it reports for permits an earlier real run already imported. Waivers (letter,
// ID, photo, ITAS) and ticket medication photos are attached the same way.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"

	"entry-permit/internal/config"
	"entry-permit/internal/dates"
	"entry-permit/internal/db"
	"entry-permit/internal/filestore"
	"entry-permit/internal/importer"
	"entry-permit/internal/settings"
	"entry-permit/migrations"
)

// The sheets read, by name, and where the column headings end (row 4; data from row 5).
const (
	sheetPermits = "WorkEntryPermitLog"
	sheetWaivers = "WaiverLog"
	sheetTickets = "TripTicket"
	headerRows   = 4
)

func main() {
	rejected, err := run(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "import-sheets:", err)
		os.Exit(2)
	}
	if rejected {
		os.Exit(1)
	}
}

// run returns whether any row was rejected.
func run(args []string) (bool, error) {
	fs := flag.NewFlagSet("import-sheets", flag.ContinueOnError)
	file := fs.String("file", "", "the Excel download of the old sheet (required)")
	dry := fs.Bool("dry-run", false, "print what would happen and write nothing")
	files := fs.String("files", "", "the downloaded Drive folders (drive-files); copies each imported permit's documents into the upload folder")
	fill := fs.Bool("fill-valid-until", false, "compute the LEP valid-until day of permits whose sheet cell is empty, by the rule Final approval uses")
	if err := fs.Parse(args); err != nil {
		return false, err
	}
	if *file == "" {
		return false, fmt.Errorf("--file is required")
	}
	var book importer.Book
	for _, sh := range []struct {
		name string
		into *[][]string
	}{{sheetPermits, &book.Permits}, {sheetWaivers, &book.Waivers}, {sheetTickets, &book.Tickets}} {
		rows, err := importer.ReadSheet(*file, sh.name)
		if err != nil {
			return false, err
		}
		*sh.into = rows
	}
	cfg := config.Load(os.Getenv)
	database, err := db.Open(cfg.DBDriver, cfg.DSN())
	if err != nil {
		return false, err
	}
	defer database.Close()
	// The same migrations the server runs, so the import never meets a missing table.
	if err := db.Migrate(database, migrations.FS, migrations.Dir(cfg.DBDriver)); err != nil {
		return false, err
	}
	ctx, clock := context.Background(), dates.SystemClock{}
	// The rule numbers (grace weeks and so on) are the ones saved in Settings, as for the server.
	svc := settings.New(database, clock)
	if err := svc.Seed(ctx); err != nil {
		return false, err
	}
	reps, err := importer.RunAll(ctx, database, book, importer.Options{
		HeaderRows: headerRows, DryRun: *dry, Clock: clock, FillValidUntil: *fill, Policy: svc.Policy(),
	})
	importer.WriteAll(os.Stdout, reps)
	rejected := false
	for _, r := range reps {
		rejected = rejected || len(r.Rejected) > 0
	}
	if err != nil || *files == "" {
		return rejected, err
	}
	// WHY a second step after the import: the documents are attached to permits that exist, so
	// they come after the import is saved. It only fills empty columns, so it can be run again.
	// A dry run therefore reports for permits an earlier real run already imported.
	opt := importer.FileOptions{DriveDir: *files, Store: filestore.Store{Dir: cfg.UploadDir}, HeaderRows: headerRows, DryRun: *dry}
	var frep []importer.FileReport
	for _, attach := range []struct {
		rows [][]string
		fn   func(context.Context, *sql.DB, [][]string, importer.FileOptions) ([]importer.FileReport, error)
	}{{book.Permits, importer.AttachPermitFiles}, {book.Waivers, importer.AttachWaiverFiles}, {book.Tickets, importer.AttachTicketFiles}} {
		got, aerr := attach.fn(ctx, database, attach.rows, opt)
		frep = append(frep, got...)
		if aerr != nil {
			err = aerr
			break
		}
	}
	importer.WriteFiles(os.Stdout, frep, *dry)
	return rejected, err
}
