package permitlog

import (
	"context"
	"reflect"
	"testing"
)

func seedPhotoPermit(t *testing.T, s Store, id, status string, patient int64, personal string) {
	t.Helper()
	_, err := s.DB.Exec(`INSERT INTO permit (request_id, status, patient_id, personal_id, worker_name, company,
		requester_name, requester_email, work_location, created_at)
		VALUES (?, ?, ?, ?, 'Budi', 'PT Maju', 'Rina', 'rina@example.com', 'Onshore', '2026-06-01 08:00:00')`, id, status, patient, personal)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPhotoTargetsAreThePersonsPermitsStillInUse(t *testing.T) {
	s, _ := newStore(t)
	seedPhotoPermit(t, s, "WEP-AAAAA", "Approved", 7, "")
	seedPhotoPermit(t, s, "WEP-BBBBB", "PendingStage1", 7, "")
	seedPhotoPermit(t, s, "WEP-CCCCC", "Expired in grace period", 7, "")
	seedPhotoPermit(t, s, "WEP-DDDDD", "Rejected", 7, "")
	seedPhotoPermit(t, s, "WEP-EEEEE", "Expired", 7, "")
	seedPhotoPermit(t, s, "WEP-FFFFF", "Renewed by LEP-XXXXX", 7, "")
	seedPhotoPermit(t, s, "WEP-GGGGG", "Approved", 8, "") // someone else
	got, err := s.PhotoTargets(context.Background(), 7, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"WEP-AAAAA", "WEP-BBBBB", "WEP-CCCCC"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
}

func TestPhotoTargetsAlsoMatchOnThePersonalIdForImportedPermits(t *testing.T) {
	s, _ := newStore(t)
	seedPhotoPermit(t, s, "WEP-AAAAA", "Approved", 0, "3201")
	got, err := s.PhotoTargets(context.Background(), 7, "3201")
	if err != nil || len(got) != 1 {
		t.Fatalf("targets = %v, err = %v", got, err)
	}
	// A real id is never 0, and a blank personal id must not match every imported permit.
	got, _ = s.PhotoTargets(context.Background(), 9, "")
	if len(got) != 0 {
		t.Fatalf("a blank personal id matched %v", got)
	}
}

func TestSetIDPhotoStoresThePathOfThatPermitOnly(t *testing.T) {
	s, d := newStore(t)
	seedPhotoPermit(t, s, "WEP-AAAAA", "Approved", 7, "")
	seedPhotoPermit(t, s, "WEP-BBBBB", "Approved", 7, "")
	old, err := s.SetIDPhoto(context.Background(), "WEP-AAAAA", "WEP-AAAAA/id_photo.jpg")
	if err != nil || old != "" {
		t.Fatalf("old = %q, err = %v", old, err)
	}
	old, err = s.SetIDPhoto(context.Background(), "WEP-AAAAA", "WEP-AAAAA/id_photo.jpg")
	if err != nil || old != "WEP-AAAAA/id_photo.jpg" {
		t.Fatalf("the second call must return the path it replaced: %q %v", old, err)
	}
	var other string
	if err := d.QueryRow(`SELECT id_photo FROM permit WHERE request_id = 'WEP-BBBBB'`).Scan(&other); err != nil || other != "" {
		t.Fatalf("other permit changed: %q %v", other, err)
	}
	if _, err := s.SetIDPhoto(context.Background(), "WEP-NOPE1", "x"); err == nil {
		t.Fatal("an unknown permit must be an error")
	}
}

func TestForPersonListsTheirPermitsNewestFirstWithoutRequestIds(t *testing.T) {
	s, d := newStore(t)
	seedPhotoPermit(t, s, "WEP-AAAAA", "Expired", 7, "")
	seedPhotoPermit(t, s, "WEP-BBBBB", "Approved", 7, "")
	seedPhotoPermit(t, s, "WEP-CCCCC", "Approved", 8, "") // someone else
	for id, v := range map[string][3]string{"WEP-AAAAA": {"LEP-AAAAA", "2026-01-31", "2026-01-01 08:00:00"}, "WEP-BBBBB": {"LEP-BBBBB", "2027-03-31", "2026-06-01 08:00:00"}} {
		if _, err := d.Exec(`UPDATE permit SET lep_no = ?, lep_valid_until = ?, created_at = ? WHERE request_id = ?`, v[0], v[1], v[2], id); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ForPerson(context.Background(), 7, "", 5)
	if err != nil {
		t.Fatal(err)
	}
	want := []PersonPermit{{LEP: "LEP-BBBBB", Status: "Approved", ValidUntil: "2027-03-31"}, {LEP: "LEP-AAAAA", Status: "Expired", ValidUntil: "2026-01-31"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if got, _ = s.ForPerson(context.Background(), 7, "", 1); len(got) != 1 {
		t.Fatalf("limit ignored: %d rows", len(got))
	}
}

func TestOwnedCardReturnsOnlyAPermitOfThatPerson(t *testing.T) {
	s, d := newStore(t)
	seedPhotoPermit(t, s, "WEP-AAAAA", "Approved", 7, "")
	seedPhotoPermit(t, s, "WEP-BBBBB", "Approved", 8, "")
	if _, err := d.Exec(`UPDATE permit SET lep_no = 'LEP-' || substr(request_id, 5), id_photo = request_id || '/id_photo.jpg', lep_valid_until = '2027-01-31'`); err != nil {
		t.Fatal(err)
	}
	c, ok, err := s.OwnedCard(context.Background(), 7, "", "LEP-AAAAA")
	if err != nil || !ok || c.WorkerName != "Budi" || c.IDPhoto != "WEP-AAAAA/id_photo.jpg" || c.ValidUntil != "2027-01-31" {
		t.Fatalf("own card: %+v ok=%v err=%v", c, ok, err)
	}
	if _, ok, _ := s.OwnedCard(context.Background(), 7, "", "LEP-BBBBB"); ok {
		t.Fatal("another person's card was returned")
	}
	if _, ok, _ := s.OwnedCard(context.Background(), 7, "", ""); ok {
		t.Fatal("a blank number matched")
	}
}
