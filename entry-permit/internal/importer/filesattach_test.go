package importer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"entry-permit/internal/filestore"
)

var jpegBytes = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00")

const driveLink = "https://drive.google.com/file/d/abc123/view?usp=drivesdk"

// driveWith builds a drive-files folder holding the given files under the given sub-folders.
func driveWith(t *testing.T, files map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	for rel, data := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestAttachCopiesTheMatchedFileAndSetsTheColumn(t *testing.T) {
	d := newDB(t)
	rows := sheet(sheetRow(map[int]string{25: driveLink}))
	run(t, d, rows, false)
	drive := driveWith(t, map[string][]byte{"id/BudiSantoso_KTP.jpg": jpegBytes})
	uploads := t.TempDir()

	reps, err := AttachPermitFiles(context.Background(), d, rows, FileOptions{DriveDir: drive, Store: filestore.Store{Dir: uploads}, HeaderRows: 4})
	if err != nil {
		t.Fatal(err)
	}
	var got string
	if err := d.QueryRow(`SELECT id_doc FROM permit WHERE request_id = 'WEP-AAAAA'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "WEP-AAAAA/id_doc.jpg" {
		t.Fatalf("id_doc = %q", got)
	}
	data, err := os.ReadFile(filepath.Join(uploads, got))
	if err != nil || !bytes.Equal(data, jpegBytes) {
		t.Fatalf("copied file wrong: %v", err)
	}
	if r := reportFor(reps, "id_doc"); r.Linked != 1 || r.Copied != 1 {
		t.Fatalf("report %+v", r)
	}
}

func TestAttachDryRunCountsTheSameAndWritesNothing(t *testing.T) {
	d := newDB(t)
	rows := sheet(sheetRow(map[int]string{25: driveLink}))
	run(t, d, rows, false)
	drive := driveWith(t, map[string][]byte{"id/BudiSantoso_KTP.jpg": jpegBytes})
	uploads := t.TempDir()

	reps, err := AttachPermitFiles(context.Background(), d, rows, FileOptions{DriveDir: drive, Store: filestore.Store{Dir: uploads}, HeaderRows: 4, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if r := reportFor(reps, "id_doc"); r.Copied != 1 {
		t.Fatalf("a dry run must report what a real run would copy: %+v", r)
	}
	if n := count(t, d, `SELECT COUNT(*) FROM permit WHERE id_doc <> ''`); n != 0 {
		t.Fatalf("%d permits were changed by a dry run", n)
	}
	if entries, _ := os.ReadDir(uploads); len(entries) != 0 {
		t.Fatalf("a dry run copied %d folders", len(entries))
	}
}

func TestAttachTwiceDoesNothingTheSecondTime(t *testing.T) {
	d := newDB(t)
	rows := sheet(sheetRow(map[int]string{25: driveLink}))
	run(t, d, rows, false)
	drive := driveWith(t, map[string][]byte{"id/BudiSantoso_KTP.jpg": jpegBytes})
	opt := FileOptions{DriveDir: drive, Store: filestore.Store{Dir: t.TempDir()}, HeaderRows: 4}
	if _, err := AttachPermitFiles(context.Background(), d, rows, opt); err != nil {
		t.Fatal(err)
	}
	reps, err := AttachPermitFiles(context.Background(), d, rows, opt)
	if err != nil {
		t.Fatal(err)
	}
	if r := reportFor(reps, "id_doc"); r.Linked != 0 || r.Copied != 0 {
		t.Fatalf("a permit that already has its file is not touched again: %+v", r)
	}
}

func TestAttachSkipsRequestsThatWereNotImported(t *testing.T) {
	d := newDB(t) // nothing imported
	rows := sheet(sheetRow(map[int]string{25: driveLink}))
	drive := driveWith(t, map[string][]byte{"id/BudiSantoso_KTP.jpg": jpegBytes})
	reps, err := AttachPermitFiles(context.Background(), d, rows, FileOptions{DriveDir: drive, Store: filestore.Store{Dir: t.TempDir()}, HeaderRows: 4})
	if err != nil {
		t.Fatal(err)
	}
	if r := reportFor(reps, "id_doc"); r.Linked != 0 || r.Copied != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestAttachListsMissingAndAmbiguousRequestsByIdOnly(t *testing.T) {
	d := newDB(t)
	rows := sheet(
		sheetRow(map[int]string{25: driveLink, 22: driveLink}),
		sheetRow(map[int]string{1: "WEP-BBBBB", 92: "LEP-BBBBB", 5: "Ayu Lestari", 25: driveLink}),
	)
	run(t, d, rows, false)
	drive := driveWith(t, map[string][]byte{
		// A passport expires and is uploaded again, so two files for one request is a real question
		// (a KTP would take the newest, see the test below).
		"id/BudiSantoso_PassportID.jpg":     jpegBytes,
		"id/BudiSantoso_PassportID (1).jpg": jpegBytes,
	})
	reps, err := AttachPermitFiles(context.Background(), d, rows, FileOptions{DriveDir: drive, Store: filestore.Store{Dir: t.TempDir()}, HeaderRows: 4})
	if err != nil {
		t.Fatal(err)
	}
	r := reportFor(reps, "id_doc")
	if r.Copied != 0 || len(r.AmbiguousIDs) != 1 || r.AmbiguousIDs[0] != "WEP-AAAAA" || len(r.MissingIDs) != 1 || r.MissingIDs[0] != "WEP-BBBBB" {
		t.Fatalf("report %+v", r)
	}
	if p := reportFor(reps, "id_photo"); len(p.MissingIDs) != 1 {
		t.Fatalf("the photo folder does not exist, so its request is missing: %+v", p)
	}
}

func TestAttachRefusesAFileThatIsNotADocumentAndKeepsTheColumnEmpty(t *testing.T) {
	d := newDB(t)
	rows := sheet(sheetRow(map[int]string{25: driveLink}))
	run(t, d, rows, false)
	drive := driveWith(t, map[string][]byte{"id/BudiSantoso_KTP.jpg": []byte("not a picture")})
	reps, err := AttachPermitFiles(context.Background(), d, rows, FileOptions{DriveDir: drive, Store: filestore.Store{Dir: t.TempDir()}, HeaderRows: 4})
	if err != nil {
		t.Fatal(err)
	}
	if r := reportFor(reps, "id_doc"); r.Failed != 1 || r.Copied != 0 {
		t.Fatalf("report %+v", r)
	}
	if n := count(t, d, `SELECT COUNT(*) FROM permit WHERE id_doc <> ''`); n != 0 {
		t.Fatal("a refused file must not be recorded")
	}
}

func reportFor(reps []FileReport, field string) FileReport {
	for _, r := range reps {
		if r.Field == field {
			return r
		}
	}
	return FileReport{Field: "not found"}
}

func TestFileReportPrintsCountsAndOnlyAFewRequestIds(t *testing.T) {
	var ids []string
	for i := 0; i < 15; i++ {
		ids = append(ids, "WEP-"+strings.Repeat("A", 4)+string(rune('A'+i)))
	}
	var b strings.Builder
	WriteFiles(&b, []FileReport{{Field: "id_doc", Linked: 20, Copied: 4, Failed: 1, FailedIDs: ids[:1], MissingIDs: ids, AmbiguousIDs: ids[:2], Unrecognised: 3}}, true)
	out := b.String()
	for _, want := range []string{"DRY RUN", "id_doc", "20 linked", "4 copied", "1 not a document", "15 missing", "2 ambiguous", "3 files in the folder do not follow the old naming", "and 5 more"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, ids[14]) {
		t.Errorf("the 15th id should be cut:\n%s", out)
	}
}

func TestAttachPairsAgainstEveryLinkedRowSoARerunDoesNotShiftTheOrder(t *testing.T) {
	d := newDB(t)
	rows := sheet(
		sheetRow(map[int]string{25: driveLink}),
		sheetRow(map[int]string{1: "WEP-BBBBB", 92: "LEP-BBBBB", 0: "9/1/2026 10:00:00", 25: driveLink}), // same worker, later
	)
	run(t, d, rows, false)
	// The first request already has its file, so only the second is still waiting.
	if _, err := d.Exec(`UPDATE permit SET id_doc = 'WEP-AAAAA/id_doc.jpg' WHERE request_id = 'WEP-AAAAA'`); err != nil {
		t.Fatal(err)
	}
	drive := driveWith(t, map[string][]byte{
		"id/BudiSantoso_KTP.jpg":     jpegBytes,
		"id/BudiSantoso_KTP (1).jpg": append([]byte{}, jpegBytes...),
	})
	reps, err := AttachPermitFiles(context.Background(), d, rows, FileOptions{DriveDir: drive, Store: filestore.Store{Dir: t.TempDir()}, HeaderRows: 4})
	if err != nil {
		t.Fatal(err)
	}
	r := reportFor(reps, "id_doc")
	if r.Linked != 1 || r.Copied != 1 || len(r.AmbiguousIDs) != 0 {
		t.Fatalf("the later request takes the file named (1), not an ambiguity: %+v", r)
	}
}

func TestAttachWaiverFilesUsesTheVisitEndDayForTheLetter(t *testing.T) {
	d := newDB(t)
	book := Book{Waivers: sheet(waiverRow(map[int]string{65: driveLink, 66: driveLink, 89: driveLink}))}
	if _, err := RunAll(context.Background(), d, Book{Permits: sheet(), Waivers: book.Waivers, Tickets: sheet()}, Options{HeaderRows: 4, Clock: clock}); err != nil {
		t.Fatal(err)
	}
	drive := driveWith(t, map[string][]byte{
		"waiver-assignment/WVR_AppointmentLetter_TamuSatu_05112026.pdf": []byte("%PDF-1.4\n"),
		"waiver-assignment/WVR_AppointmentLetter_TamuSatu_12112026.pdf": []byte("%PDF-1.4\n1 0 obj\n"),
		"id/TamuSatu_KTP.jpg":             jpegBytes,
		"id-photo/TamuSatu_KTP_Photo.jpg": jpegBytes,
	})
	reps, err := AttachWaiverFiles(context.Background(), d, book.Waivers, FileOptions{DriveDir: drive, Store: filestore.Store{Dir: t.TempDir()}, HeaderRows: 4})
	if err != nil {
		t.Fatal(err)
	}
	var letter, idDoc, photo string
	if err := d.QueryRow(`SELECT letter_doc, id_doc, id_photo FROM waiver WHERE request_id = 'WVR-AAAAA'`).Scan(&letter, &idDoc, &photo); err != nil {
		t.Fatal(err)
	}
	if letter != "WVR-AAAAA/letter_doc.pdf" || idDoc != "WVR-AAAAA/id_doc.jpg" || photo != "WVR-AAAAA/id_photo.jpg" {
		t.Fatalf("paths %q %q %q (reports %+v)", letter, idDoc, photo, reps)
	}
	if r := reportFor(reps, "letter_doc"); r.Table != "waiver" || r.Copied != 1 {
		t.Fatalf("%+v", r)
	}
}

func TestAttachTicketFilesMatchesTheMedicationPhotoByTicketId(t *testing.T) {
	d := newDB(t)
	permits, tickets := sheet(sheetRow(nil)), sheet(ticketRow(map[int]string{5: "Budi Santoso", 30: driveLink}))
	if _, err := RunAll(context.Background(), d, Book{Permits: permits, Waivers: sheet(), Tickets: tickets}, Options{HeaderRows: 4, Clock: clock}); err != nil {
		t.Fatal(err)
	}
	drive := driveWith(t, map[string][]byte{
		"ticket-medication/BudiSantoso_Medicine_TKT-AAAAA.jpg": jpegBytes,
		"ticket-medication/BudiSantoso_Medicine_TKT-ZZZZZ.jpg": jpegBytes,
	})
	reps, err := AttachTicketFiles(context.Background(), d, tickets, FileOptions{DriveDir: drive, Store: filestore.Store{Dir: t.TempDir()}, HeaderRows: 4})
	if err != nil {
		t.Fatal(err)
	}
	var got string
	if err := d.QueryRow(`SELECT med_photo FROM ticket WHERE ticket_id = 'TKT-AAAAA'`).Scan(&got); err != nil || got != "TKT-AAAAA/med_photo.jpg" {
		t.Fatalf("med_photo %q err %v (reports %+v)", got, err, reps)
	}
}

// Owner rule, 2026-11-08: a photo and a KTP are uploaded once and never expire, so when a worker
// has more files than requests the newest file is used instead of leaving the permit without one.
func TestAttachTakesTheNewestPhotoAndKtpWhenFilesOutnumberRequests(t *testing.T) {
	d := newDB(t)
	rows := sheet(sheetRow(map[int]string{25: driveLink, 22: driveLink}))
	run(t, d, rows, false)
	old, newest := append(append([]byte{}, jpegBytes...), 'O'), append(append([]byte{}, jpegBytes...), 'N')
	drive := driveWith(t, map[string][]byte{
		"id-photo/BudiSantoso_KTP_Photo.jpg":     old,
		"id-photo/BudiSantoso_KTP_Photo (1).jpg": newest,
		"id/BudiSantoso_KTP.jpg":                 old,
		"id/BudiSantoso_KTP (1).jpg":             newest,
	})
	uploads := t.TempDir()
	reps, err := AttachPermitFiles(context.Background(), d, rows, FileOptions{DriveDir: drive, Store: filestore.Store{Dir: uploads}, HeaderRows: 4})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"id_photo", "id_doc"} {
		var rel string
		if err := d.QueryRow(`SELECT ` + field + ` FROM permit WHERE request_id = 'WEP-AAAAA'`).Scan(&rel); err != nil || rel == "" {
			t.Fatalf("%s not attached: %q %v", field, rel, err)
		}
		if data, err := os.ReadFile(filepath.Join(uploads, rel)); err != nil || !bytes.Equal(data, newest) {
			t.Errorf("%s: the newest file was not the one copied (%v)", field, err)
		}
		if r := reportFor(reps, field); r.Copied != 1 || r.Inferred != 1 || len(r.AmbiguousIDs) != 0 {
			t.Errorf("%s report %+v", field, r)
		}
	}
	var out strings.Builder
	WriteFiles(&out, reps, false)
	if !strings.Contains(out.String(), "1 chosen by the newest-file rule") {
		t.Errorf("the report does not say how many were chosen by the rule:\n%s", out.String())
	}
}
