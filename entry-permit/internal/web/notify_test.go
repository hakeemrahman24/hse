package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"entry-permit/internal/mail"
	"entry-permit/internal/staff"
)

// fakeMail records every message handed to it and can be told to fail.
type fakeMail struct {
	sent []mail.Message
	err  error
}

func (f *fakeMail) Send(_ context.Context, m mail.Message) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, m)
	return nil
}

func (e *testEnv) outboxRows(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM email_outbox`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *testEnv) outboxRow(t *testing.T, kind string) (status string, attempts int, lastErr string) {
	t.Helper()
	if err := e.db.QueryRow(`SELECT status, attempts, last_error FROM email_outbox WHERE kind = ?`, kind).Scan(&status, &attempts, &lastErr); err != nil {
		t.Fatalf("no %s row: %v", kind, err)
	}
	return
}

// everyStage signs in someone who may act at every stage.
func (e *testEnv) everyStage(t *testing.T, page string) (cookies []*http.Cookie, tok string) {
	t.Helper()
	c := e.reviewer(t, "all", staff.Doctor, staff.Stage1, staff.Final)
	// The CSRF token only shows on a page that has a form, so the caller names a queue that is not empty.
	return c, e.reviewToken(t, page, c)
}

func TestFinalApprovalSendsTheApprovedMailWithTheLEPAndALink(t *testing.T) {
	e := newEnv(t)
	e.reviewPermit(t, "WEP-NEW01", "PendingFinal", 1, "", "", "2027-01-31")
	c, tok := e.everyStage(t, "final")
	if code, _, _ := e.review(t, c, tok, "WEP-NEW01", "final", "approve", ""); code != 303 {
		t.Fatalf("approve: %d", code)
	}
	if len(e.mailer.sent) != 1 {
		t.Fatalf("sent %d mails, want 1", len(e.mailer.sent))
	}
	m := e.mailer.sent[0]
	var lep string
	_ = e.db.QueryRow(`SELECT lep_no FROM permit WHERE request_id = 'WEP-NEW01'`).Scan(&lep)
	if m.Kind != mail.KindWEPApproved || strings.Join(m.To, ",") != "rina@example.com" || m.SourceKey != lep {
		t.Errorf("mail %+v (lep %s)", m, lep)
	}
	for _, want := range []string{"Approved", "Budi Santoso", lep} {
		if !strings.Contains(m.Subject, want) {
			t.Errorf("subject %q lacks %q", m.Subject, want)
		}
	}
	for _, want := range []string{"PT Maju", "17 Jan 2027", "https://permit.example/?page=ticket&lep=" + lep} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("text lacks %q:\n%s", want, m.Text)
		}
	}
	if st, n, _ := e.outboxRow(t, mail.KindWEPApproved); st != "Sent" || n != 1 {
		t.Errorf("outbox row %s attempts %d", st, n)
	}
}

func TestStage1ApprovalSendsNothingButARejectionDoes(t *testing.T) {
	e := newEnv(t)
	e.reviewPermit(t, "WEP-AAAAA", "PendingStage1", 1, "", "", "2027-01-31")
	e.reviewPermit(t, "WEP-BBBBB", "PendingStage1", 2, "", "", "2027-01-31")
	c := e.reviewer(t, "sari", staff.Stage1)
	tok := e.reviewToken(t, "stage1", c)
	e.review(t, c, tok, "WEP-AAAAA", "stage1", "approve", "")
	if e.outboxRows(t) != 0 || len(e.mailer.sent) != 0 {
		t.Fatal("a Stage 1 approval only moves the permit on; the requester is told at the end")
	}
	e.review(t, c, tok, "WEP-BBBBB", "stage1", "reject", "Passport page is unreadable, upload again.")
	if len(e.mailer.sent) != 1 {
		t.Fatalf("sent %d, want the rejection", len(e.mailer.sent))
	}
	m := e.mailer.sent[0]
	if m.Kind != mail.KindWEPRejected || !strings.Contains(m.Text, "Passport page is unreadable, upload again.") ||
		!strings.Contains(m.Text, "Sari") || !strings.Contains(m.Text, "Stage 1") {
		t.Errorf("rejection mail:\n%s", m.Text)
	}
}

func TestWaiverMailsGoOutOnFinalApprovalAndOnAnyRejection(t *testing.T) {
	e := newEnv(t)
	e.reviewWaiver(t, "WVR-AAAAA")
	e.reviewWaiver(t, "WVR-BBBBB")
	c, tok := e.everyStage(t, "doctor")
	e.review(t, c, tok, "WVR-AAAAA", "doctor", "approve", "")
	e.review(t, c, tok, "WVR-AAAAA", "stage1", "approve", "")
	if len(e.mailer.sent) != 0 {
		t.Fatal("no mail before the last approval")
	}
	e.review(t, c, tok, "WVR-AAAAA", "final", "approve", "")
	if len(e.mailer.sent) != 1 || e.mailer.sent[0].Kind != mail.KindWaiverApproved {
		t.Fatalf("sent %+v", e.mailer.sent)
	}
	m := e.mailer.sent[0]
	if strings.Join(m.To, ",") != "r@x.id" || !strings.Contains(m.Text, "10 Nov 2026") || !strings.Contains(m.Text, "12 Nov 2026") ||
		!strings.Contains(m.Text, "Site A") || !strings.Contains(m.Text, "https://permit.example/?page=ticket&lep=WVR-AAAAA") {
		t.Errorf("waiver approval mail:\n%s", m.Text)
	}
	e.review(t, c, tok, "WVR-BBBBB", "doctor", "reject", "Please give the medicine name.")
	if len(e.mailer.sent) != 2 || e.mailer.sent[1].Kind != mail.KindWaiverRejected || !strings.Contains(e.mailer.sent[1].Text, "Please give the medicine name.") {
		t.Errorf("sent %+v", e.mailer.sent)
	}
}

func TestADownMailServerNeverFailsTheDecisionAndTheMailIsSentLater(t *testing.T) {
	e := newEnv(t)
	e.reviewPermit(t, "WEP-NEW01", "PendingFinal", 1, "", "", "2027-01-31")
	c, tok := e.everyStage(t, "final")
	e.mailer.err = errors.New("smtp down")
	if code, _, _ := e.review(t, c, tok, "WEP-NEW01", "final", "approve", ""); code != 303 {
		t.Fatalf("the decision must still succeed, got %d", code)
	}
	if got := e.statusOf(t, "permit", "request_id", "WEP-NEW01"); got != "Approved" {
		t.Errorf("status %s", got)
	}
	st, n, last := e.outboxRow(t, mail.KindWEPApproved)
	if st != "Pending" || n != 1 || last != "smtp down" {
		t.Errorf("row %s attempts %d err %q", st, n, last)
	}
	// The server comes back; the drain delivers it.
	e.mailer.err = nil
	e.clock.now = e.clock.now.AddMinutes(60)
	res, err := (mail.Outbox{DB: e.db}).Drain(context.Background(), e.mailer, e.clock.now, mail.Retry{MaxAttempts: 14, BaseMinutes: 15})
	if err != nil || res.Sent != 1 || len(e.mailer.sent) != 1 {
		t.Errorf("drain: %+v err=%v sent=%d", res, err, len(e.mailer.sent))
	}
}

func TestWithoutAMailerTheMailWaitsInTheOutbox(t *testing.T) {
	e := newEnv(t)
	e.app.Mailer = nil
	e.reviewPermit(t, "WEP-NEW01", "PendingFinal", 1, "", "", "2027-01-31")
	c, tok := e.everyStage(t, "final")
	if code, _, _ := e.review(t, c, tok, "WEP-NEW01", "final", "approve", ""); code != 303 {
		t.Fatalf("approve: %d", code)
	}
	if st, n, _ := e.outboxRow(t, mail.KindWEPApproved); st != "Pending" || n != 0 {
		t.Errorf("row %s attempts %d: with no mail server configured it just waits", st, n)
	}
}

func TestABadRequesterAddressStillLetsTheDecisionThrough(t *testing.T) {
	e := newEnv(t)
	e.reviewPermit(t, "WEP-NEW01", "PendingFinal", 1, "", "", "2027-01-31")
	if _, err := e.db.Exec(`UPDATE permit SET requester_email = 'not an address'`); err != nil {
		t.Fatal(err)
	}
	c, tok := e.everyStage(t, "final")
	if code, _, _ := e.review(t, c, tok, "WEP-NEW01", "final", "approve", ""); code != 303 {
		t.Fatalf("approve: %d", code)
	}
	if e.outboxRows(t) != 0 || len(e.mailer.sent) != 0 {
		t.Error("an address that cannot be mailed is logged and skipped, not stored")
	}
}

// An approval goes to both addresses the requester gave and copies the active administrators; a
// rejection concerns the requester only, so nobody is copied.
func TestApprovalGoesToBothAddressesAndCopiesTheAdmin(t *testing.T) {
	e := newEnv(t)
	if _, err := e.store.Create(context.Background(), "boss", "Boss Admin", "Admin@Example.com", "1234", []staff.Role{staff.Admin}); err != nil {
		t.Fatal(err)
	}
	e.reviewPermit(t, "WEP-NEW01", "PendingFinal", 1, "", "", "2027-01-31")
	e.reviewPermit(t, "WEP-NEW02", "PendingFinal", 1, "", "", "2027-01-31")
	if _, err := e.db.Exec(`UPDATE permit SET requester_email = 'rina@example.com,backup@example.com' WHERE request_id IN ('WEP-NEW01', 'WEP-NEW02')`); err != nil {
		t.Fatal(err)
	}
	c, tok := e.everyStage(t, "final")
	e.review(t, c, tok, "WEP-NEW01", "final", "approve", "")
	if len(e.mailer.sent) != 1 {
		t.Fatalf("sent %d mails", len(e.mailer.sent))
	}
	m := e.mailer.sent[0]
	if strings.Join(m.To, ",") != "rina@example.com,backup@example.com" || strings.Join(m.CC, ",") != "admin@example.com" {
		t.Errorf("To %v CC %v", m.To, m.CC)
	}
	e.review(t, c, tok, "WEP-NEW02", "final", "reject", "The photo is not clear enough, please send it again.")
	if r := e.mailer.sent[1]; r.Kind != mail.KindWEPRejected || len(r.CC) != 0 {
		t.Errorf("rejection %+v", r)
	}
}
