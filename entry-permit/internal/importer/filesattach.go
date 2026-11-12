package importer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"entry-permit/internal/filestore"
)

// docKind ties one stored document to its sheet link columns and its old Drive folder.
type docKind struct {
	DocSpec
	Cols []int // sheet columns whose link says "this request had this document"
}

// docSet describes one sheet and the table its rows were imported into.
type docSet struct {
	Table, IDColumn string
	ID              func(row []string) string
	NameCol, AtCol  int
	Key             func(row []string) string // optional: the exact key of a file's "date" group
	Kinds           []docKind
}

// spec builds a DocSpec. The old script made file names from letters and digits only, so a
// worker name never holds an underscore, which is why the name group is [^_]+.
func spec(field, folder, pattern string) DocSpec {
	return DocSpec{Field: field, Folder: folder, Pattern: regexp.MustCompile(pattern)}
}

// idDocSpec: the old script named the ID upload "<Name>_KTP" (Indonesian) or "<Name>_PassportID"
// (foreign), and the sheet's downloads also hold an older "<Name>_ID.<original extension>" for
// almost every worker. Both are that worker's ID, so the script's own label is preferred.
func idDocSpec() DocSpec {
	d := spec("id_doc", "id", `^(?P<name>[^_]+)_(?P<kind>KTP|PassportID)$`)
	d.Fallback = regexp.MustCompile(`^(?P<name>[^_]+)_ID$`)
	// A KTP is uploaded once and never expires, so the newest file stands in when the counts differ.
	// A passport expires and is uploaded again, so it is never guessed (owner, 2026-11-08).
	d.OneTimeKind = "KTP"
	return d
}

func idPhotoSpec() DocSpec {
	d := spec("id_photo", "id-photo", `^(?P<name>[^_]+)_(KTP|PassportID|ID)_Photo$`)
	d.NewestWins = true // a face does not expire: any of the worker's photos is right, the newest is best
	return d
}

// permitSet: the folder and the file label are from the old script's upload code; the columns
// are the sheet's link columns (PassportLink, column 25, is where the old sheet kept the ID
// upload, and IDPhotoLink, column 22, the photo).
var permitSet = docSet{
	Table: "permit", IDColumn: "request_id", NameCol: 5, AtCol: 0,
	ID: func(row []string) string { return cell(row, 1) },
	Kinds: []docKind{
		{idDocSpec(), []int{25}},
		{idPhotoSpec(), []int{22}},
		{spec("visa_doc", "work-visa", `^WorkVisa_(?P<name>[^_]+)_\d+$`), []int{28, 31, 35}},
		{spec("rptka_doc", "rptka", `^RPTKA_(?P<name>[^_]+)_\d+$`), []int{39}},
		{spec("immigration_doc", "surat-melapor", `^SuratMelaporImigrasi_(?P<name>[^_]+)_\d+$`), []int{40}},
		{spec("police_doc", "surat-kepolisian", `^SuratKeteranganKepolisian_(?P<name>[^_]+)_\d+$`), []int{42}},
		{spec("cert_doc", "certificates", `^(?P<name>[^_]+)_CertificateOfCompetence$`), []int{53}},
		{spec("bpjs_kes_doc", "bpjs-kes", `^(?P<name>[^_]+)_.*BPJSKes$`), []int{57}},
		{spec("bpjs_tk_doc", "bpjs-tk", `^(?P<name>[^_]+)_.*BPJSTK$`), []int{61}},
		{spec("contract_doc", "contracts", `^(?P<name>[^_]+)_Contract_(?P<date>\d{8})$`), []int{64}},
	},
}

// waiverSet: a visitor's letter is "WVR_AppointmentLetter_<Name>_<ddMMyyyy of the visit end>".
// The ITAS file is "<Name>_ITAS_Valid<date>" and a visa file reuses the permit's WorkVisa name.
var waiverSet = docSet{
	Table: "waiver", IDColumn: "request_id", NameCol: wName, AtCol: wTimestamp,
	ID: func(row []string) string { return firstOf(cell(row, wWaiverID), cell(row, wRequestNo)) },
	Key: func(row []string) string {
		day, ok := Day(cell(row, wTo))
		if !ok {
			return ""
		}
		return day[8:10] + day[5:7] + day[0:4] // yyyy-mm-dd -> ddMMyyyy
	},
	Kinds: []docKind{
		{idDocSpec(), []int{66}},
		{idPhotoSpec(), []int{89}},
		{spec("letter_doc", "waiver-assignment", `^WVR_AppointmentLetter_(?P<name>.+)_(?P<date>\d{8})$`), []int{65}},
		{visaWithFallback(), []int{67}},
	},
}

func visaWithFallback() DocSpec {
	d := spec("visa_doc", "work-visa", `^(?P<name>[^_]+)_ITAS_Valid.+$`)
	d.Fallback = regexp.MustCompile(`^WorkVisa_(?P<name>[^_]+)_\d+$`)
	return d
}

// ticketSet: a medication photo is "<Name>_Medicine_<ticket id>", so the ticket id is an exact key.
var ticketSet = docSet{
	Table: "ticket", IDColumn: "ticket_id", NameCol: 5, AtCol: 0,
	ID:  func(row []string) string { return cell(row, 1) },
	Key: func(row []string) string { return cell(row, 1) },
	Kinds: []docKind{
		{spec("med_photo", "ticket-medication", `^(?P<name>.+)_Medicine_(?P<date>TKT-[A-Za-z0-9]+)$`), []int{30}},
	},
}

// FileOptions say where the files are and where they go.
type FileOptions struct {
	DriveDir   string          // the drive-files folder, one sub-folder per kind of document
	Store      filestore.Store // the server's upload folder
	HeaderRows int
	DryRun     bool
}

// FileReport is one kind of document. Counts and request ids only, never a name or a path.
type FileReport struct {
	Table, Field string
	Linked       int // requests that had a link and still lack the file
	Copied       int
	Failed       int // the file was found but is not a PDF, JPG or PNG, or could not be read
	Unrecognised int // files in the folder that do not follow the old naming
	MissingIDs   []string
	AmbiguousIDs []string
	FailedIDs    []string
	Inferred     int // of those copied, how many were chosen by the newest-file rule, not paired exactly
}

// AttachPermitFiles copies each imported permit's documents from the downloaded Drive folders
// into the upload folder, in the shape the server writes (<request id>/<field>.<ext>), and
// stores the path on the permit.
//
// WHY it only fills an empty column: a permit whose document the new system already holds
// (a re-upload, a copy from an earlier run) is never touched, so running it twice is safe.
// A dry run copies into a throw-away folder and removes it, so its counts equal a real run's.
func AttachPermitFiles(ctx context.Context, db *sql.DB, rows [][]string, opt FileOptions) ([]FileReport, error) {
	return attachSet(ctx, db, rows, opt, permitSet)
}

// AttachWaiverFiles does the same for visitor waivers (letter, ID, photo, ITAS or visa).
func AttachWaiverFiles(ctx context.Context, db *sql.DB, rows [][]string, opt FileOptions) ([]FileReport, error) {
	return attachSet(ctx, db, rows, opt, waiverSet)
}

// AttachTicketFiles does the same for the medication photo of a trip ticket.
func AttachTicketFiles(ctx context.Context, db *sql.DB, rows [][]string, opt FileOptions) ([]FileReport, error) {
	return attachSet(ctx, db, rows, opt, ticketSet)
}

func attachSet(ctx context.Context, db *sql.DB, rows [][]string, opt FileOptions, set docSet) ([]FileReport, error) {
	store := opt.Store
	if opt.DryRun {
		tmp, err := os.MkdirTemp("", "attach-dry-")
		if err != nil {
			return nil, fmt.Errorf("make dry-run folder: %w", err)
		}
		defer os.RemoveAll(tmp)
		store = filestore.Store{Dir: tmp}
	}
	var out []FileReport
	for _, kind := range set.Kinds {
		rep, err := attachOne(ctx, db, rows, opt, store, set, kind)
		if err != nil {
			return out, err
		}
		out = append(out, rep)
	}
	return out, nil
}

func attachOne(ctx context.Context, db *sql.DB, rows [][]string, opt FileOptions, store filestore.Store, set docSet, doc docKind) (FileReport, error) {
	rep := FileReport{Table: set.Table, Field: doc.Field}
	// set.Table, set.IDColumn and doc.Field come from the constant tables above, never from input.
	empty, err := idsWithEmpty(ctx, db, set, doc.Field)
	if err != nil {
		return rep, err
	}
	// WHY every linked row is paired, not only the ones still waiting: the plain file name
	// belongs to a worker's first request and " (1)" to the next. Dropping the requests that
	// already have their file would shift that order on a re-run.
	var reqs []Request
	waiting := 0
	if len(rows) > opt.HeaderRows {
		for _, row := range rows[opt.HeaderRows:] {
			id := set.ID(row)
			if id == "" || !hasLink(row, doc.Cols) {
				continue
			}
			r := Request{ID: id, Name: cell(row, set.NameCol), At: cell(row, set.AtCol)}
			if set.Key != nil {
				r.Key = set.Key(row)
			}
			reqs = append(reqs, r)
			if empty[id] {
				waiting++
			}
		}
	}
	rep.Linked = waiting
	names, err := listFiles(filepath.Join(opt.DriveDir, doc.Folder))
	if err != nil {
		return rep, err
	}
	pairing := PairFiles(doc.DocSpec, reqs, names)
	rep.Unrecognised = pairing.Unrecognised
	rep.MissingIDs, rep.AmbiguousIDs = onlyWaiting(pairing.Missing, empty), onlyWaiting(pairing.Ambiguous, empty)
	rep.Inferred = len(onlyWaiting(pairing.Inferred, empty))
	for id := range pairing.Matched {
		if !empty[id] {
			delete(pairing.Matched, id)
		}
	}

	ids := make([]string, 0, len(pairing.Matched))
	for id := range pairing.Matched {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		data, err := os.ReadFile(filepath.Join(opt.DriveDir, doc.Folder, pairing.Matched[id]))
		if err != nil {
			rep.Failed, rep.FailedIDs = rep.Failed+1, append(rep.FailedIDs, id)
			continue
		}
		rel, err := store.Save(id, doc.Field, data, 0)
		if err != nil {
			rep.Failed, rep.FailedIDs = rep.Failed+1, append(rep.FailedIDs, id)
			continue
		}
		if !opt.DryRun {
			q := `UPDATE ` + set.Table + ` SET ` + doc.Field + ` = ? WHERE ` + set.IDColumn + ` = ? AND ` + doc.Field + ` = ''`
			if _, err := db.ExecContext(ctx, q, rel, id); err != nil {
				return rep, fmt.Errorf("record %s: %w", doc.Field, err)
			}
		}
		rep.Copied++
	}
	return rep, nil
}

// onlyWaiting keeps the request ids that still lack the file.
func onlyWaiting(ids []string, empty map[string]bool) []string {
	var out []string
	for _, id := range ids {
		if empty[id] {
			out = append(out, id)
		}
	}
	return out
}

// idsWithEmpty returns the ids of the rows whose column holds no file yet.
func idsWithEmpty(ctx context.Context, db *sql.DB, set docSet, field string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+set.IDColumn+` FROM `+set.Table+` WHERE `+field+` = ''`)
	if err != nil {
		return nil, fmt.Errorf("read permits without %s: %w", field, err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("read permit: %w", err)
		}
		out[id] = true
	}
	return out, rows.Err()
}

// hasLink says the sheet row holds a Drive link in any of the columns.
func hasLink(row []string, cols []int) bool {
	for _, c := range cols {
		if strings.HasPrefix(cell(row, c), "http") {
			return true
		}
	}
	return false
}

// listFiles returns the file names of a folder. A folder that does not exist is simply empty:
// the requests that needed it are then listed as missing.
func listFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read folder %s: %w", filepath.Base(dir), err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// maxIDsShown keeps the report readable: the full list would be hundreds of ids.
const maxIDsShown = 10

// WriteFiles prints one block per kind of document: counts, then a few request ids for each
// problem so an admin knows where to look. Ids only, never a name or a path.
func WriteFiles(w io.Writer, reps []FileReport, dry bool) {
	fmt.Fprintln(w, "== Documents ==")
	if dry {
		fmt.Fprintln(w, "DRY RUN: nothing was copied or written.")
	}
	for _, r := range reps {
		fmt.Fprintf(w, "%s %s: %d linked, %d copied, %d not a document, %d missing, %d ambiguous\n",
			r.Table, r.Field, r.Linked, r.Copied, r.Failed, len(r.MissingIDs), len(r.AmbiguousIDs))
		if r.Unrecognised > 0 {
			fmt.Fprintf(w, "  %d files in the folder do not follow the old naming and were not used\n", r.Unrecognised)
		}
		if r.Inferred > 0 {
			fmt.Fprintf(w, "  %d chosen by the newest-file rule (the worker's files and requests differ in number; a photo or KTP never expires)\n", r.Inferred)
		}
		writeIDs(w, "not a PDF, JPG or PNG, or unreadable", r.FailedIDs)
		writeIDs(w, "no file found", r.MissingIDs)
		writeIDs(w, "several files fit, none taken", r.AmbiguousIDs)
	}
}

func writeIDs(w io.Writer, what string, ids []string) {
	if len(ids) == 0 {
		return
	}
	shown := ids
	if len(shown) > maxIDsShown {
		shown = shown[:maxIDsShown]
	}
	fmt.Fprintf(w, "  %s: %s", what, strings.Join(shown, ", "))
	if more := len(ids) - len(shown); more > 0 {
		fmt.Fprintf(w, " and %d more", more)
	}
	fmt.Fprintln(w)
}
