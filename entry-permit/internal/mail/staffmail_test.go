package mail

import (
	"strings"
	"testing"
)

func TestComposeTheStaleReviewReminderCountsAndMarksTheLateOnes(t *testing.T) {
	m, err := ComposeStale(StaleNotice{
		To: []string{"Anna@Example.com", "budi@example.com"}, Stage: "Stage 1 Reviewer", ThresholdHours: 24,
		Link: "https://permit.example/staff/stage1",
		Items: []StaleItem{
			{ID: "WEP-AAAAA", Name: "Fresh One", Company: "PT Maju", Position: "Welder", Hours: 5},
			{ID: "WEP-BBBBB", Name: "Late <b>One</b>", Company: "PT Maju", Position: "Rigger", Hours: 49},
			{ID: "WVR-CCCCC", Name: "Late Two", Company: "PT Tamu", Position: "Auditor", Hours: 26},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "[Entry Permit] 3 pending Stage 1 Reviewer (2 over 24h)" {
		t.Errorf("subject %q", m.Subject)
	}
	if m.Kind != KindStaleReminder || strings.Join(m.To, ",") != "anna@example.com,budi@example.com" {
		t.Errorf("addressing %+v", m)
	}
	// longest wait first, so the reviewer sees the worst case at the top
	if a, b, c := strings.Index(m.Text, "WEP-BBBBB"), strings.Index(m.Text, "WVR-CCCCC"), strings.Index(m.Text, "WEP-AAAAA"); !(a < b && b < c) {
		t.Errorf("order wrong in:\n%s", m.Text)
	}
	for _, want := range []string{"49h", "5h", "https://permit.example/staff/stage1", "Hello,"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("text lacks %q", want)
		}
	}
	if strings.Contains(m.Text, "Dear Requester") || strings.Contains(m.HTML, "Dear Requester") {
		t.Error("a staff mail must not greet the requester")
	}
	// a name typed into a public form must not add markup to the mail
	if strings.Contains(m.HTML, "<b>One</b>") || !strings.Contains(m.HTML, "&lt;b&gt;One&lt;/b&gt;") {
		t.Errorf("name not escaped in HTML")
	}
}

func TestStaleReminderWithNothingLateOmitsTheCount(t *testing.T) {
	m, err := ComposeStale(StaleNotice{To: []string{"a@example.com"}, Stage: "Doctor", ThresholdHours: 24,
		Items: []StaleItem{{ID: "WVR-AAAAA", Name: "X", Hours: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "[Entry Permit] 1 pending Doctor" {
		t.Errorf("subject %q", m.Subject)
	}
}

func TestComposeTheAdminDigestListsBothGroupsAndMarksUrgent(t *testing.T) {
	m, err := ComposeDigest(DigestNotice{
		To: []string{"admin@example.com"}, Registered: 120, Active: 45, WindowDays: 30,
		Expiring: []DigestItem{
			{ID: "LEP-AAAAA", Name: "Soon", Company: "PT Maju", Position: "Welder", ValidUntil: "12 Nov 2026", Left: "5d", Urgent: true},
			{ID: "LEP-BBBBB", Name: "Later", Company: "PT Maju", Position: "Rigger", ValidUntil: "30 Nov 2026", Left: "23d"},
		},
		Grace: []DigestItem{{ID: "LEP-CCCCC", Name: "Grace", Company: "PT Tamu", Position: "Cook", ValidUntil: "1 Nov 2026", Left: "8d left"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "[Entry Permit] Permit Digest – 120 registered, 45 active, 2 ending within 30 days, 1 in grace period" {
		t.Errorf("subject %q", m.Subject)
	}
	for _, want := range []string{"120", "45", "LEP-AAAAA", "5d", "URGENT", "LEP-CCCCC", "8d left", "Hello,"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("digest text lacks %q:\n%s", want, m.Text)
		}
	}
	if strings.Count(m.Text, "URGENT") != 1 {
		t.Errorf("only the 5d permit is urgent:\n%s", m.Text)
	}
}

func TestDigestWithNothingToListSaysNone(t *testing.T) {
	m, err := ComposeDigest(DigestNotice{To: []string{"admin@example.com"}, Registered: 3, Active: 1, WindowDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(m.Text, "None.") != 2 {
		t.Errorf("both empty groups should say None:\n%s", m.Text)
	}
}

func TestStaffMailRefusesNoRecipientAndABadAddress(t *testing.T) {
	if _, err := ComposeStale(StaleNotice{Stage: "Doctor", Items: []StaleItem{{ID: "x"}}}); err == nil {
		t.Error("stale mail with no recipient was composed")
	}
	if _, err := ComposeDigest(DigestNotice{To: []string{"a@b.c\r\nBcc: x@y.z"}}); err == nil {
		t.Error("digest with a line break in the address was composed")
	}
}
