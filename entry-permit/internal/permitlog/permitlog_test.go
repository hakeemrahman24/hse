package permitlog

import (
	"context"
	"database/sql"
	"testing"

	"entry-permit/internal/db"
	"entry-permit/migrations"
)

func newStore(t *testing.T) (Store, *sql.DB) {
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
	return Store{DB: d}, d
}

func insert(t *testing.T, d *sql.DB, id, status string, patient int64, personal, created string) {
	t.Helper()
	_, err := d.Exec(`INSERT INTO permit (request_id, lep_no, status, patient_id, personal_id, worker_name, company,
		requester_name, requester_email, work_location, mcu_valid_until, lep_valid_until, created_at)
		VALUES (?, 'LEP-AAAAA', ?, ?, ?, 'Budi', 'PT Maju', 'Req', 'r@x.id', 'Onshore', '2027-01-31', '2027-01-17', ?)`,
		id, status, patient, personal, created)
	if err != nil {
		t.Fatal(err)
	}
}

func TestAccessFindsAPermitByItsLEPInAnyCase(t *testing.T) {
	s, d := newStore(t)
	insert(t, d, "WEP-ONE01", "Approved", 1, "", "2026-06-01 08:00:00")
	if _, err := d.Exec(`UPDATE permit SET dob = '1988-03-05', nationality = 'Indonesia', mcu_result = 'Fit' WHERE request_id = 'WEP-ONE01'`); err != nil {
		t.Fatal(err)
	}
	a, ok, err := s.Access(context.Background(), " lep-aaaaa ")
	if err != nil || !ok {
		t.Fatalf("access: %v %v", ok, err)
	}
	if a.RequestID != "WEP-ONE01" || a.Status != "Approved" || a.DOB != "1988-03-05" || a.WorkerName != "Budi" ||
		a.Nationality != "Indonesia" || a.WorkLocation != "Onshore" || a.McuResult != "Fit" ||
		a.LepValidUntil != "2027-01-17" || a.RequesterEmail != "r@x.id" {
		t.Errorf("got %+v", a)
	}
	if _, ok, _ := s.Access(context.Background(), "LEP-ZZZZZ"); ok {
		t.Error("an unknown permit number was found")
	}
	// A permit that has no LEP yet (blank) must never match a blank search.
	if _, err := d.Exec(`UPDATE permit SET lep_no = ''`); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Access(context.Background(), ""); ok {
		t.Error("a blank permit number matched a permit without an LEP")
	}
}

func TestLatestNone(t *testing.T) {
	s, _ := newStore(t)
	f, err := s.Latest(context.Background(), 1, "")
	if err != nil || f.Previous.Exists {
		t.Fatalf("empty table: %+v %v", f, err)
	}
}

func TestLatestPicksNewestForThePatient(t *testing.T) {
	s, d := newStore(t)
	insert(t, d, "WEP-OLD01", "Renewed by LEP-BBBBB", 1, "", "2026-01-01 08:00:00")
	insert(t, d, "WEP-NEW01", "Approved", 1, "", "2026-06-01 08:00:00")
	insert(t, d, "WEP-OTHER", "Approved", 2, "", "2026-09-01 08:00:00")
	f, err := s.Latest(context.Background(), 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if f.RequestID != "WEP-NEW01" || f.Previous.Status != "Approved" || f.Previous.Company != "PT Maju" ||
		f.Previous.McuValidUntil != "2027-01-31" || f.Previous.LepValidUntil != "2027-01-17" || f.LepNo != "LEP-AAAAA" {
		t.Fatalf("found = %+v", f)
	}
}

func TestLatestMatchesOnPersonalIDWhenThePatientIDChanged(t *testing.T) {
	s, d := newStore(t)
	insert(t, d, "WEP-AAAA1", "Approved", 7, "3174010101010001", "2026-06-01 08:00:00")
	f, _ := s.Latest(context.Background(), 99, "3174010101010001")
	if f.RequestID != "WEP-AAAA1" {
		t.Fatalf("by personal id: %+v", f)
	}
	// A blank personal id must never match every blank row.
	insert(t, d, "WEP-BLANK", "Approved", 8, "", "2026-06-02 08:00:00")
	if f, _ := s.Latest(context.Background(), 99, ""); f.Previous.Exists {
		t.Fatalf("blank personal id matched %+v", f)
	}
}

// insertNamed stores a permit with a name and a birth date and no medpost link (patient 0), the way
// the spreadsheet import leaves one.
func insertNamed(t *testing.T, d *sql.DB, id string, patient int64, personal, name, dob string) {
	t.Helper()
	_, err := d.Exec(`INSERT INTO permit (request_id, lep_no, status, patient_id, personal_id, worker_name, dob, company,
		requester_name, requester_email, work_location, mcu_valid_until, lep_valid_until, created_at)
		VALUES (?, 'LEP-AAAAA', 'Approved', ?, ?, ?, ?, 'PT Maju', 'Req', 'r@x.id', 'Onshore', '2027-01-31', '2027-01-17', '2026-06-01 08:00:00')`,
		id, patient, personal, name, dob)
	if err != nil {
		t.Fatal(err)
	}
}

func linkedTo(t *testing.T, d *sql.DB, id string) (int64, string) {
	t.Helper()
	var patient int64
	var personal string
	if err := d.QueryRow(`SELECT patient_id, personal_id FROM permit WHERE request_id = ?`, id).Scan(&patient, &personal); err != nil {
		t.Fatal(err)
	}
	return patient, personal
}

// Imported permits carry a name and a birth date but no medpost patient id. Adopt links them to the
// worker medpost has just confirmed (same name and birth date), so every later lookup finds them.
func TestAdoptLinksAnUnlinkedPermitByNameAndBirthDate(t *testing.T) {
	s, d := newStore(t)
	insertNamed(t, d, "WEP-IMP01", 0, "", "Budi Santoso", "1990-01-01")
	n, err := s.Adopt(context.Background(), 7, "", "  budi   SANTOSO ", "1990-01-01")
	if err != nil || n != 1 {
		t.Fatalf("Adopt = %d, %v; want 1, nil", n, err)
	}
	if p, _ := linkedTo(t, d, "WEP-IMP01"); p != 7 {
		t.Errorf("patient id = %d, want 7", p)
	}
	if f, err := s.Latest(context.Background(), 7, ""); err != nil || !f.Previous.Exists {
		t.Errorf("Latest after adopt = %+v, %v; want the permit found", f, err)
	}
}

func TestAdoptLeavesOtherPeoplesAndAlreadyLinkedPermitsAlone(t *testing.T) {
	s, d := newStore(t)
	insertNamed(t, d, "WEP-OTHERDOB", 0, "", "Budi Santoso", "1991-02-02")  // same name, other birth date
	insertNamed(t, d, "WEP-OTHERNAME", 0, "", "Budi Santosa", "1990-01-01") // other name, same birth date
	insertNamed(t, d, "WEP-LINKED", 5, "", "Budi Santoso", "1990-01-01")    // already belongs to patient 5
	n, err := s.Adopt(context.Background(), 7, "", "Budi Santoso", "1990-01-01")
	if err != nil || n != 0 {
		t.Fatalf("Adopt = %d, %v; want 0, nil", n, err)
	}
	for id, want := range map[string]int64{"WEP-OTHERDOB": 0, "WEP-OTHERNAME": 0, "WEP-LINKED": 5} {
		if p, _ := linkedTo(t, d, id); p != want {
			t.Errorf("%s patient id = %d, want %d", id, p, want)
		}
	}
	if n, _ := s.Adopt(context.Background(), 7, "", "Budi Santoso", ""); n != 0 { // no birth date: never match on a name alone
		t.Errorf("a blank birth date adopted %d permits", n)
	}
}

// WHY the personal id: the NIK or passport number becomes the long-term key (plan 4.1). A permit
// that already holds a different one is somebody else; an empty one is filled in.
func TestAdoptStampsThePersonalIDAndRefusesAConflictingOne(t *testing.T) {
	s, d := newStore(t)
	insertNamed(t, d, "WEP-BLANK", 0, "", "Budi Santoso", "1990-01-01")
	insertNamed(t, d, "WEP-SAME", 0, "3201010101900001", "Budi Santoso", "1990-01-01")
	insertNamed(t, d, "WEP-OTHER", 0, "9999999999999999", "Budi Santoso", "1990-01-01")
	n, err := s.Adopt(context.Background(), 7, "3201010101900001", "Budi Santoso", "1990-01-01")
	if err != nil || n != 2 {
		t.Fatalf("Adopt = %d, %v; want 2, nil", n, err)
	}
	for id, want := range map[string]string{"WEP-BLANK": "3201010101900001", "WEP-SAME": "3201010101900001", "WEP-OTHER": "9999999999999999"} {
		if _, p := linkedTo(t, d, id); p != want {
			t.Errorf("%s personal id = %q, want %q", id, p, want)
		}
	}
	if p, _ := linkedTo(t, d, "WEP-OTHER"); p != 0 {
		t.Errorf("a permit with another personal id must stay unlinked, got patient %d", p)
	}
}

// Until medpost sends the personal id, the permit keeps the one from the sheet.
func TestAdoptKeepsThePermitsPersonalIDWhenMedpostSendsNone(t *testing.T) {
	s, d := newStore(t)
	insertNamed(t, d, "WEP-SHEET", 0, "3201010101900001", "Budi Santoso", "1990-01-01")
	if n, err := s.Adopt(context.Background(), 7, "", "Budi Santoso", "1990-01-01"); err != nil || n != 1 {
		t.Fatalf("Adopt = %d, %v; want 1, nil", n, err)
	}
	if p, personal := linkedTo(t, d, "WEP-SHEET"); p != 7 || personal != "3201010101900001" {
		t.Errorf("linked to %d with personal id %q", p, personal)
	}
}

func sample(id string) NewPermit {
	return NewPermit{
		RequestID: id, Status: "PendingStage1", PatientID: 7, PersonalID: "3201010101900001",
		WorkerName: "Budi Santoso", Company: "PT Maju", Entity: "Contractor", WorkPosition: "Welder",
		Parents: "PT Induk (1)", Nationality: "Indonesia", RequesterName: "Rina", RequesterEmail: "rina@example.com",
		WorkLocation: "Onshore", McuCheckupID: "CU-1", McuResult: "Fit", McuValidUntil: "2027-01-31", LepValidUntil: "2027-01-17",
		Phone: "6281234567890", EmergencyName: "Siti", EmergencyPhone: "6281298765432",
		Province: "Aceh", Regency: "Kabupaten Aceh Selatan", District: "Tapaktuan", Village: "Pasar", Street: "Jl. Merdeka 1",
		IDDoc: "WEP-AB12C/id_doc.pdf", IDPhoto: "WEP-AB12C/id_photo.jpg",
		ContractUntil: "2027-03-01", ContractDoc: "WEP-AB12C/contract_doc.pdf", BpjsKes: "0001234567890", CertUntil: "2027-05-01",
		CertDoc: "WEP-AB12C/cert_doc.pdf", CertName: "SIO Crane", PositionCanonical: "Crane Operator", PositionRisk: "CRITICAL", CertRequired: "Y",
		DeclaredAt: "2026-11-06 08:00:00", CreatedAt: "2026-11-06 08:00:00",
	}
}

func TestCreateStoresThePermitAndLatestFindsIt(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if err := s.Create(ctx, sample("WEP-AB12C")); err != nil {
		t.Fatal(err)
	}
	ok, err := s.Exists(ctx, "WEP-AB12C")
	if err != nil || !ok {
		t.Fatalf("Exists = %v %v", ok, err)
	}
	if ok, _ := s.Exists(ctx, "WEP-ZZZZZ"); ok {
		t.Error("an unknown id exists")
	}
	f, err := s.Latest(ctx, 7, "")
	if err != nil || f.RequestID != "WEP-AB12C" || f.Previous.Status != "PendingStage1" {
		t.Fatalf("Latest = %+v %v", f, err)
	}
	var cn, cu, cr string
	if err := s.DB.QueryRowContext(ctx, `SELECT position_canonical, cert_until, cert_required FROM permit WHERE request_id = 'WEP-AB12C'`).Scan(&cn, &cu, &cr); err != nil || cn != "Crane Operator" || cu != "2027-05-01" || cr != "Y" {
		t.Errorf("snapshot of position and certificate not stored: %q %q %q %v", cn, cu, cr, err)
	}
	if f.Prefill.Phone != "6281234567890" || f.Prefill.Street != "Jl. Merdeka 1" || f.Prefill.EmergencyName != "Siti" {
		t.Errorf("prefill = %+v", f.Prefill)
	}
}

func TestCreateRefusesADuplicateRequestID(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if err := s.Create(ctx, sample("WEP-AB12C")); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, sample("WEP-AB12C")); err == nil {
		t.Error("the same request id was stored twice")
	}
}

func TestCreateInKeepsTheLEPNumberAndJoinsTheCallersTransaction(t *testing.T) {
	// The importer stores permits that already have an LEP number, and must be able to
	// roll a whole run back: so the write has to happen inside the caller's transaction.
	s, d := newStore(t)
	ctx := context.Background()
	p := NewPermit{RequestID: "WEP-IMP01", LepNo: "LEP-IMPAA", Status: "Approved", WorkerName: "Budi", RequesterName: "R", RequesterEmail: "r@x.id",
		WorkLocation: "Onshore", LepValidUntil: "2027-01-17", DOB: "1988-03-05", CreatedAt: "2026-06-01 08:00:00"}

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateIn(ctx, tx, p); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Exists(ctx, "WEP-IMP01"); ok {
		t.Fatal("a rolled-back permit is still stored")
	}

	if err := s.CreateIn(ctx, d, p); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Access(ctx, "LEP-IMPAA")
	if err != nil || !ok || got.RequestID != "WEP-IMP01" || got.DOB != "1988-03-05" {
		t.Fatalf("access %+v ok=%v err=%v", got, ok, err)
	}
	// the normal submit path still leaves the LEP number blank until Final approval
	q := p
	q.RequestID, q.LepNo = "WEP-NEW01", ""
	if err := s.Create(ctx, q); err != nil {
		t.Fatal(err)
	}
	var lep string
	if err := d.QueryRow(`SELECT lep_no FROM permit WHERE request_id = 'WEP-NEW01'`).Scan(&lep); err != nil || lep != "" {
		t.Errorf("a submitted permit got LEP %q", lep)
	}
}
