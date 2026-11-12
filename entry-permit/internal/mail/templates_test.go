package mail

import (
	"strings"
	"testing"
)

func approvedWEP() Notice {
	return Notice{
		Kind: KindWEPApproved, To: "Rina@example.com, boss@x.id", Name: "Budi Santoso", Company: "PT Maju",
		Position: "Driver LV", Number: "LEP-ABCDE", ValidUntil: "30 Jun 2027", Link: "https://permit.example/?page=ticket&lep=LEP-ABCDE",
	}
}

func TestComposeWEPApproved(t *testing.T) {
	m, err := Compose(approvedWEP())
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "[NO-REPLY] LNG Entry Permit – Approved – Budi Santoso – LEP-ABCDE" {
		t.Errorf("subject %q", m.Subject)
	}
	if strings.Join(m.To, ",") != "rina@example.com,boss@x.id" || m.Kind != KindWEPApproved || m.SourceKey != "LEP-ABCDE" {
		t.Errorf("addressing %+v", m)
	}
	for _, want := range []string{"Budi Santoso", "PT Maju", "Driver LV", "LEP-ABCDE", "30 Jun 2027", "https://permit.example/?page=ticket&lep=LEP-ABCDE"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("text lacks %q", want)
		}
	}
	if !strings.Contains(m.HTML, `href="https://permit.example/?page=ticket&amp;lep=LEP-ABCDE"`) {
		t.Errorf("html link not escaped as an attribute:\n%s", m.HTML)
	}
	if !strings.Contains(m.Text, "Please do not reply") || !strings.Contains(m.HTML, "Please do not reply") {
		t.Error("both bodies carry the automated-message footer")
	}
}

func TestComposeWEPRejectedShowsStageReviewerAndNote(t *testing.T) {
	m, err := Compose(Notice{Kind: KindWEPRejected, To: "rina@example.com", Name: "Budi Santoso", Number: "WEP-ONE01",
		Stage: "Final approval", Reviewer: "Sari", Note: "Passport page is unreadable, upload again."})
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "[NO-REPLY] LNG Entry Permit – Rejected – Budi Santoso – WEP-ONE01" {
		t.Errorf("subject %q", m.Subject)
	}
	for _, want := range []string{"Final approval", "Sari", "Passport page is unreadable, upload again."} {
		if !strings.Contains(m.Text, want) || !strings.Contains(m.HTML, want) {
			t.Errorf("a rejection mail must show %q", want)
		}
	}
}

func TestComposeWaiverMails(t *testing.T) {
	ok, err := Compose(Notice{Kind: KindWaiverApproved, To: "rina@example.com", Name: "Dewi Visitor", Company: "PT Tamu", Number: "WVR-ABCDE",
		VisitFrom: "01 Dec 2026", VisitTo: "03 Dec 2026", Location: "Onshore", Link: "https://permit.example/?page=ticket&lep=WVR-ABCDE"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ok.Subject, "Visitor Entry Permit – Approved – Dewi Visitor – WVR-ABCDE") {
		t.Errorf("subject %q", ok.Subject)
	}
	for _, want := range []string{"01 Dec 2026", "03 Dec 2026", "Onshore", "WVR-ABCDE"} {
		if !strings.Contains(ok.Text, want) {
			t.Errorf("waiver approval lacks %q", want)
		}
	}
	no, err := Compose(Notice{Kind: KindWaiverRejected, To: "rina@example.com", Name: "Dewi Visitor", Number: "WVR-ABCDE", Stage: "Doctor review", Reviewer: "Dina", Note: "Please give the medicine name."})
	if err != nil || !strings.Contains(no.Subject, "Rejected") || !strings.Contains(no.Text, "Please give the medicine name.") {
		t.Errorf("waiver rejection: %+v err=%v", no, err)
	}
}

func TestComposeEscapesWhatPeopleTypeAndKeepsHeadersClean(t *testing.T) {
	n := approvedWEP()
	n.Name = "Budi <script>alert(1)</script>"
	n.Company = "A & B \"Co\""
	m, err := Compose(n)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.HTML, "<script>") || !strings.Contains(m.HTML, "&lt;script&gt;") || !strings.Contains(m.HTML, "A &amp; B") {
		t.Errorf("html did not escape input:\n%s", m.HTML)
	}
	// A name with a newline must not reach the Subject header.
	n.Name = "Budi\r\nBcc: evil@x.id"
	m, err = Compose(n)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(m.Subject, "\r\n") {
		t.Errorf("subject has a line break: %q", m.Subject)
	}
}

func TestComposeRefusesAnUnknownKindAndABadRecipient(t *testing.T) {
	if _, err := Compose(Notice{Kind: "nope", To: "a@b.id"}); err == nil {
		t.Error("unknown kind")
	}
	n := approvedWEP()
	n.To = "nobody"
	if _, err := Compose(n); err == nil {
		t.Error("bad recipient")
	}
}
