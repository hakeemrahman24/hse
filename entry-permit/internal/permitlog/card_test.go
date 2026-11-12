package permitlog

import (
	"context"
	"testing"
)

func TestCardReadsOnlyWhatTheCardShows(t *testing.T) {
	s, d := newStore(t)
	insert(t, d, "WEP-ONE01", "Approved", 1, "", "2026-06-01 08:00:00")
	if _, err := d.Exec(`UPDATE permit SET entity = 'Contractor', work_position = 'Welder',
		parents = 'PT LNG (1)', dob = '1988-03-05' WHERE request_id = 'WEP-ONE01'`); err != nil {
		t.Fatal(err)
	}
	c, ok, err := s.Card(context.Background(), "lep-aaaaa") // any case
	if err != nil || !ok {
		t.Fatalf("Card: ok=%v err=%v", ok, err)
	}
	if c.LEP != "LEP-AAAAA" || c.Status != "Approved" || c.WorkerName != "Budi" || c.Company != "PT Maju" ||
		c.Entity != "Contractor" || c.Parents != "PT LNG (1)" || c.Position != "Welder" || c.ValidUntil != "2027-01-17" {
		t.Errorf("card = %+v", c)
	}
}

func TestCardNeverMatchesABlankOrUnknownNumber(t *testing.T) {
	s, d := newStore(t)
	insert(t, d, "WEP-ONE01", "Pending Stage 1", 1, "", "2026-06-01 08:00:00")
	if _, err := d.Exec(`UPDATE permit SET lep_no = ''`); err != nil { // waiting permits have no LEP yet
		t.Fatal(err)
	}
	for _, lep := range []string{"", "LEP-ZZZZZ"} {
		if _, ok, err := s.Card(context.Background(), lep); ok || err != nil {
			t.Errorf("Card(%q): ok=%v err=%v", lep, ok, err)
		}
	}
}
