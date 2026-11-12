package permit

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"entry-permit/internal/dates"
	"entry-permit/internal/policy"
)

var pol = policy.Default() // MCU grace 2 weeks, expiry grace 14, note min 3 words

func TestGolden1To7Validity(t *testing.T) {
	cases := []struct {
		name   string
		mod    func(*policy.Policy)
		in     ValidityInput
		want   dates.Day
		reason Reason
	}{
		{"1 wni mcu", nil, ValidityInput{McuValidUntil: "2027-06-30"}, "2027-06-16", ReasonMCU},
		{"2 contract", nil, ValidityInput{McuValidUntil: "2027-06-30", ContractValidUntil: "2027-03-01"}, "2027-03-01", ReasonContract},
		{"3 visa/itas earliest", nil, ValidityInput{Foreign: true, McuValidUntil: "2027-06-30", Visa: "2027-05-01", Itas: "2027-04-10", PassportValidUntil: "2028-01-01"}, "2027-04-10", ReasonVisa},
		{"4 passport", nil, ValidityInput{Foreign: true, McuValidUntil: "2027-06-30", Visa: "2027-05-01", PassportValidUntil: "2027-02-01"}, "2027-02-01", ReasonPassport},
		{"5 wni ignores visa", nil, ValidityInput{McuValidUntil: "2027-06-30", Visa: "2027-01-01", PassportValidUntil: "2027-01-01"}, "2027-06-16", ReasonMCU},
		{"6 grace 0", func(p *policy.Policy) { p.McuLepGraceValue = 0 }, ValidityInput{McuValidUntil: "2027-06-30"}, "2027-06-30", ReasonMCU},
		{"grace in days", func(p *policy.Policy) { p.McuLepGraceValue, p.McuLepGraceUnit = 10, "days" }, ValidityInput{McuValidUntil: "2027-06-30"}, "2027-06-20", ReasonMCU},
		{"tie goes to MCU", func(p *policy.Policy) { p.McuLepGraceValue = 0 }, ValidityInput{McuValidUntil: "2027-03-01", ContractValidUntil: "2027-03-01"}, "2027-03-01", ReasonMCU},
	}
	for _, c := range cases {
		p := pol
		if c.mod != nil {
			c.mod(&p)
		}
		got, reason, err := ResolveValidity(p, c.in)
		if err != nil || got != c.want || reason != c.reason {
			t.Errorf("%s: got %s %s %v", c.name, got, reason, err)
		}
	}
	// 7: nothing usable.
	if _, _, err := ResolveValidity(pol, ValidityInput{}); !errors.Is(err, ErrNoValidity) {
		t.Errorf("want ErrNoValidity, got %v", err)
	}
}

func TestGolden8To10Expiry(t *testing.T) {
	e := ExpiryStatus(pol, StatusApproved, "2027-01-09", "2027-01-10") // yesterday
	if e.Status != StatusExpiredGrace || !e.Changed || !e.LockTickets {
		t.Errorf("8: %+v", e)
	}
	if e := ExpiryStatus(pol, StatusApproved, "2026-12-27", "2027-01-10"); e.Status != StatusExpiredGrace { // 14 days ago
		t.Errorf("9a: %+v", e)
	}
	if e := ExpiryStatus(pol, StatusApproved, "2026-12-26", "2027-01-10"); e.Status != StatusExpired || !e.LockTickets { // 15
		t.Errorf("9b: %+v", e)
	}
	if e := ExpiryStatus(pol, StatusApproved, "2027-01-10", "2027-01-10"); e.Changed { // today
		t.Errorf("10: %+v", e)
	}
}

func TestExpiryOtherCases(t *testing.T) {
	// Grace moving to Expired: tickets were already locked, no new lock.
	e := ExpiryStatus(pol, StatusExpiredGrace, "2026-12-26", "2027-01-10")
	if e.Status != StatusExpired || !e.Changed || e.LockTickets {
		t.Errorf("%+v", e)
	}
	// Repair: Approved-looking Expired within grace goes back to grace.
	if e := ExpiryStatus(pol, StatusExpired, "2027-01-05", "2027-01-10"); e.Status != StatusExpiredGrace || !e.Changed {
		t.Errorf("repair: %+v", e)
	}
	// Untouched statuses and blank dates.
	for _, s := range []string{StatusPendingFinal, StatusRejected, RenewedBy("LEP-1")} {
		if e := ExpiryStatus(pol, s, "2020-01-01", "2027-01-10"); e.Changed {
			t.Errorf("%s changed", s)
		}
	}
	if e := ExpiryStatus(pol, StatusApproved, "", "2027-01-10"); e.Changed {
		t.Error("blank date changed")
	}
}

func TestGolden11To16Duplicate(t *testing.T) {
	base := Previous{Exists: true, Status: StatusApproved, Company: "PT Maju", McuValidUntil: "2027-06-30", VisaExpiry: "2027-05-01", LepValidUntil: "2027-06-16"}
	cur := Current{Company: "pt  maju", McuValidUntil: "2027-06-30"}
	today := dates.Day("2027-01-10")

	prevPending := base
	prevPending.Status = StatusPendingFinal
	if d := Decide(prevPending, cur, today); d.Kind != KindActive { // 11
		t.Errorf("11: %+v", d)
	}
	if d := Decide(base, cur, today); d.Kind != KindActive { // 12
		t.Errorf("12: %+v", d)
	}
	c13 := cur
	c13.McuValidUntil = "2027-12-31"
	if d := Decide(base, c13, today); d.Kind != KindReuse || !d.McuRenewal || d.VisaRenewal { // 13
		t.Errorf("13: %+v", d)
	}
	c14 := cur
	c14.Foreign = true
	if d := Decide(base, c14, today); d.Kind != KindReuse || !d.VisaRenewalPending { // 14
		t.Errorf("14: %+v", d)
	}
	c15 := cur
	c15.Company = "PT Lain"
	if d := Decide(base, c15, today); d.Kind != KindNone { // 15
		t.Errorf("15: %+v", d)
	}
	for _, s := range []string{StatusResigned, RenewedBy("LEP-9")} { // 16
		p := base
		p.Status = s
		if d := Decide(p, cur, today); d.Kind != KindNone {
			t.Errorf("16 %s: %+v", s, d)
		}
	}
}

func TestDuplicateRemainingBranches(t *testing.T) {
	today := dates.Day("2027-07-01")
	cur := Current{Company: "A", McuValidUntil: "2027-06-30"}
	if d := Decide(Previous{}, cur, today); d.Kind != KindNone {
		t.Error("no previous")
	}
	// Approved but LEP date passed -> reuse.
	old := Previous{Exists: true, Status: StatusApproved, Company: "A", McuValidUntil: "2027-06-30", LepValidUntil: "2027-06-16"}
	if d := Decide(old, cur, today); d.Kind != KindReuse {
		t.Errorf("lep passed: %+v", d)
	}
	for _, s := range []string{StatusExpired, StatusExpiredGrace} {
		old.Status = s
		if d := Decide(old, cur, today); d.Kind != KindReuse {
			t.Errorf("%s: %+v", s, d)
		}
	}
	// Rejected previous: nothing to reuse or block.
	old.Status = StatusRejected
	if d := Decide(old, cur, today); d.Kind != KindNone {
		t.Errorf("rejected: %+v", d)
	}
	// Foreign with a changed visa date while Approved.
	live := Previous{Exists: true, Status: StatusApproved, Company: "A", McuValidUntil: "2027-06-30", VisaExpiry: "2027-08-01", LepValidUntil: "2027-12-01"}
	d := Decide(live, Current{Company: "A", McuValidUntil: "2027-06-30", Foreign: true, VisaExpiry: "2028-01-01"}, today)
	if d.Kind != KindReuse || !d.VisaRenewal || d.McuRenewal {
		t.Errorf("visa renewal: %+v", d)
	}
	// Foreign, same visa, live: blocked.
	if d := Decide(live, Current{Company: "A", McuValidUntil: "2027-06-30", Foreign: true, VisaExpiry: "2027-08-01"}, today); d.Kind != KindActive {
		t.Errorf("foreign same: %+v", d)
	}
	// Empty status counts as in flight.
	if d := Decide(Previous{Exists: true, Company: "A"}, Current{Company: "A"}, today); d.Kind != KindActive {
		t.Errorf("empty status: %+v", d)
	}
}

func TestGolden17Renewal(t *testing.T) {
	all := []Existing{
		{ID: "WEP-1", PersonalID: "N1", Status: StatusApproved, LEP: "LEP-OLD1"},
		{ID: "WEP-2", PersonalID: "N1", Status: StatusExpired, LEP: "LEP-OLD2"},
		{ID: "WEP-3", PersonalID: "N1", Status: StatusRejected, LEP: ""},
		{ID: "WEP-4", PersonalID: "N1", Status: RenewedBy("LEP-X"), LEP: "LEP-OLD4"},
		{ID: "WEP-5", PersonalID: "N2", Status: StatusApproved, LEP: "LEP-OTHER"},
		{ID: "WEP-6", PersonalID: "N1", Status: StatusResigned, LEP: "LEP-OLD6"},
		{ID: "WEP-7", PersonalID: "N1", Status: StatusApproved, LEP: "LEP-NEW"}, // the new one itself
	}
	got := RenewalTargets("N1", "LEP-NEW", all)
	if len(got) != 2 || got[0].ID != "WEP-1" || got[1].ID != "WEP-2" || got[0].NewStatus != "Renewed by LEP-NEW" {
		t.Errorf("%+v", got)
	}
}

func TestGolden18And30ReviewOrder(t *testing.T) {
	// Two reviewers: the second sees Approved and is refused.
	next, err := Review(pol, StageFinal, StatusPendingFinal, true, "")
	if err != nil || next != StatusApproved {
		t.Fatalf("first: %s %v", next, err)
	}
	if _, err := Review(pol, StageFinal, next, true, ""); !errors.Is(err, ErrAlreadyActioned) {
		t.Errorf("second: %v", err)
	}
	// Stage1 approve on an Approved request is refused (2.1).
	if _, err := Review(pol, StageStage1, StatusApproved, true, ""); !errors.Is(err, ErrAlreadyActioned) {
		t.Errorf("out of order: %v", err)
	}
	// Empty status counts as PendingStage1.
	if n, err := Review(pol, StageStage1, "", true, ""); err != nil || n != StatusPendingFinal {
		t.Errorf("empty: %s %v", n, err)
	}
	if _, err := Review(pol, "bogus", StatusPendingFinal, true, ""); !errors.Is(err, ErrUnknownStage) {
		t.Errorf("stage: %v", err)
	}
}

func TestGolden19RejectionNote(t *testing.T) {
	if _, err := Review(pol, StageFinal, StatusPendingFinal, false, "too short"); !errors.Is(err, ErrNoteTooShort) {
		t.Errorf("2 words: %v", err)
	}
	if n, err := Review(pol, StageFinal, StatusPendingFinal, false, "  not  enough documents "); err != nil || n != StatusRejected {
		t.Errorf("3 words: %s %v", n, err)
	}
}

func TestNewID(t *testing.T) {
	id, err := NewID("LEP-")
	if err != nil || len(id) != 9 || !strings.HasPrefix(id, "LEP-") {
		t.Fatalf("%q %v", id, err)
	}
	for _, c := range id[4:] {
		if !strings.ContainsRune(idAlphabet, c) {
			t.Errorf("bad char %q", c)
		}
	}
	// Bytes >= 252 are skipped: 252,253 are rejected, then 0..4 map to A..E.
	got, err := newID("X-", bytes.NewReader([]byte{252, 253, 0, 1, 2, 3, 4}))
	if err != nil || got != "X-ABCDE" {
		t.Errorf("%q %v", got, err)
	}
	if _, err := newID("X-", bytes.NewReader([]byte{1})); err == nil {
		t.Error("short reader should fail")
	}
}

func TestUniqueID(t *testing.T) {
	calls := 0
	id, err := UniqueID("TKT-", 5, func(string) (bool, error) { calls++; return calls < 3, nil })
	if err != nil || calls != 3 || id == "" {
		t.Errorf("%q %d %v", id, calls, err)
	}
	if _, err := UniqueID("TKT-", 2, func(string) (bool, error) { return true, nil }); err == nil {
		t.Error("exhausted attempts should fail")
	}
	if _, err := UniqueID("TKT-", 2, func(string) (bool, error) { return false, errors.New("db") }); err == nil {
		t.Error("lookup error should surface")
	}
}

func TestStatusHelpers(t *testing.T) {
	if !IsRenewed(" Renewed by LEP-1") || IsRenewed("Approved") {
		t.Error("IsRenewed")
	}
	if !IsExpiredLike("Expired") || !IsExpiredLike(" Expired in grace period ") || IsExpiredLike("Approved") {
		t.Error("IsExpiredLike")
	}
}
