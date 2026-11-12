package waiverlog

import (
	"context"
	"testing"

	"entry-permit/internal/db"
	"entry-permit/internal/ticketlog"
	"entry-permit/internal/waiver"
	"entry-permit/migrations"
)

func newStore(t *testing.T) Store {
	t.Helper()
	d, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	d.SetMaxOpenConns(1)
	if err := db.Migrate(d, migrations.FS, migrations.Dir("sqlite")); err != nil {
		t.Fatal(err)
	}
	return Store{DB: d}
}

func sample(id string) NewWaiver {
	return NewWaiver{
		RequestID: id, Status: waiver.StatusPendingDoctor, ApplicantType: waiver.TypeVisitorCompany,
		CompanyID: 7, Company: "Acme Ltd", Name: "Budi Santoso", Gender: "Male", DOB: "1990-04-02",
		Nationality: "Indonesia", NIK: "3201020304900001", Position: "Auditor", Department: "HSE",
		VisitLocation: "Site A", Purpose: "Annual audit", VisitFrom: "2026-11-10", VisitTo: "2026-11-12", Days: 3,
		RequesterName: "Siti Aminah", RequesterEmail: "siti@example.com", Phone: "081234567890",
		Risk: "r", Precaution: "p", Prevent: "v", DeclaredAt: "2026-11-06 08:00:00", CreatedAt: "2026-11-06 08:00:00",
	}
}

func TestCreateThenExists(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if ok, err := s.Exists(ctx, "WVR-AAAAA"); err != nil || ok {
		t.Fatalf("before: %v %v", ok, err)
	}
	if err := s.Create(ctx, sample("WVR-AAAAA")); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Exists(ctx, "WVR-AAAAA"); err != nil || !ok {
		t.Fatalf("after: %v %v", ok, err)
	}
	// The primary key is the last guard against two requests drawing one id.
	if err := s.Create(ctx, sample("WVR-AAAAA")); err == nil {
		t.Fatal("a second row with the same id must fail")
	}
}

func TestCreateWithTicketStoresBothOrNeither(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tk := ticketlog.NewTicket{ID: "TKT-AAAAA", LEP: "WVR-AAAAA", Work: false, Status: "PendingDoctor",
		Arrival: "2026-11-10", Departure: "2026-11-12", Pattern: "Normal", RotationNo: 1, SubmittedAt: "2026-11-06 08:00:00"}
	if err := s.CreateWithTicket(ctx, sample("WVR-AAAAA"), tk); err != nil {
		t.Fatal(err)
	}
	count := func(table string) int {
		var n int
		if err := s.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count("waiver") != 1 || count("ticket") != 1 {
		t.Fatalf("waiver %d ticket %d, want 1 and 1", count("waiver"), count("ticket"))
	}
	// A ticket id that is already taken must roll the waiver back too.
	if err := s.CreateWithTicket(ctx, sample("WVR-BBBBB"), tk); err == nil {
		t.Fatal("a duplicate ticket id must fail")
	}
	if count("waiver") != 1 || count("ticket") != 1 {
		t.Errorf("after the failure: waiver %d ticket %d, want 1 and 1", count("waiver"), count("ticket"))
	}
}

func TestAccessFindsAWaiverByItsNumberInAnyCase(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if err := s.Create(ctx, sample("WVR-AAAAA")); err != nil {
		t.Fatal(err)
	}
	a, ok, err := s.Access(ctx, " wvr-aaaaa ")
	if err != nil || !ok {
		t.Fatalf("access: %v %v", ok, err)
	}
	if a.RequestID != "WVR-AAAAA" || a.Status != waiver.StatusPendingDoctor || a.DOB != "1990-04-02" ||
		a.Name != "Budi Santoso" || a.From != "2026-11-10" || a.To != "2026-11-12" {
		t.Errorf("got %+v", a)
	}
	if _, ok, _ := s.Access(ctx, "WVR-ZZZZZ"); ok {
		t.Error("an unknown waiver number was found")
	}
}

func TestOpenReturnsWaiversThatCanBlock(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	live := sample("WVR-AAAAA")
	past := sample("WVR-BBBBB")
	past.VisitFrom, past.VisitTo = "2026-10-01", "2026-10-03"
	if err := s.Create(ctx, live); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, past); err != nil {
		t.Fatal(err)
	}
	got, err := s.Open(ctx, "2026-11-06")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "WVR-AAAAA" || got[0].NIK != "3201020304900001" || got[0].Company != "Acme Ltd" ||
		got[0].From != "2026-11-10" || got[0].To != "2026-11-12" || got[0].Type != waiver.TypeVisitorCompany {
		t.Fatalf("got %+v", got)
	}
	// What Open returns must really block a second request for the same person.
	cand := waiver.Waiver{NIK: "3201020304900001", Type: waiver.TypeVisitorCompany}
	if waiver.Blocking(cand, got, "2026-11-06") == nil {
		t.Error("the stored waiver does not block its own NIK")
	}
}

func TestCapRowsAreTheWaiversOfTheYear(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	a := sample("WVR-AAAAA") // 3 days, Acme, 2026
	b := sample("WVR-BBBBB")
	b.Company, b.NIK = "Other Co", "3201020304900002"
	c := sample("WVR-CCCCC")
	c.VisitFrom, c.VisitTo, c.Days, c.NIK = "2025-12-30", "2025-12-31", 2, "3201020304900003"
	for _, w := range []NewWaiver{a, b, c} {
		if err := s.Create(ctx, w); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.CapRows(ctx, "2026")
	if err != nil {
		t.Fatal(err)
	}
	if got := waiver.CapUsed(rows, "acme  ltd", "2026"); got != 3 {
		t.Errorf("Acme used %d, want 3", got)
	}
	if got := waiver.CapUsed(rows, "Other Co", "2026"); got != 3 {
		t.Errorf("Other Co used %d, want 3", got)
	}
}
