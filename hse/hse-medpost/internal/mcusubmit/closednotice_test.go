package mcusubmit

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// closeFor sends one document for a submitter and closes it against a real record, as staff do.
func closeFor(t *testing.T, s *Store, co, submitter int64, name string) int64 {
	t.Helper()
	id := sentDoc(t, s, submitter, name)
	addRecord(t, s, seedWorker(t, s, name, "1990-04-12", co), "MedC-"+itoa(id), "Fit", "2027-01-01", "Close", 1)
	if err := closeWithRecord(s, context.Background(), id, testStaffID, "MedC-"+itoa(id)); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestOneMailPerSubmitterListsEverythingClosedSinceTheLastOne(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	a := activeSubmitter(t, s, co, "hr.a@example.com")
	// B is at another company: colleagues of one company are copied on each other's mail (see
	// TestTheCompanysOtherAccountGetsACopy), which this test is not about.
	res, err := s.DB.Exec(`INSERT INTO Company (Name) VALUES ('PT Other')`)
	if err != nil {
		t.Fatal(err)
	}
	co2, _ := res.LastInsertId()
	b := activeSubmitter(t, s, co2, "hr.b@example.com")
	closeFor(t, s, co, a, "Worker One")
	closeFor(t, s, co, a, "Worker Two")
	closeFor(t, s, co, b, "Worker Three")
	sentDoc(t, s, a, "Still Pending") // not closed: not in any mail
	m := &fakeMailer{}

	mails, docs, err := s.RunClosedNotices(ctx, m, "https://portal.example.com")
	if err != nil || mails != 2 || docs != 3 || len(m.sent) != 2 {
		t.Fatalf("mails %d, docs %d, sent %d, %v; want 2 mails about 3 documents", mails, docs, len(m.sent), err)
	}
	var toA sentMail
	for _, x := range m.sent {
		if x.To == "hr.a@example.com" {
			toA = x
		}
	}
	if !strings.Contains(toA.Body, "Worker One") || !strings.Contains(toA.Body, "Worker Two") || strings.Contains(toA.Body, "Worker Three") || strings.Contains(toA.Body, "Still Pending") {
		t.Errorf("A's mail lists the wrong documents:\n%s", toA.Body)
	}
	if !strings.Contains(toA.Subject, "2") || !strings.Contains(toA.Body, "https://portal.example.com/mcu-submit/documents") {
		t.Errorf("subject %q or link missing:\n%s", toA.Subject, toA.Body)
	}
	// The mail never carries the result, a birth date or an ID number.
	for _, secret := range []string{"Unfit", "1990-04-12", "3201011204900001"} {
		if strings.Contains(toA.Body, secret) {
			t.Errorf("the mail contains %q", secret)
		}
	}

	// A second run the same day, and the next day, finds nothing new to say.
	if mails, _, _ := s.RunClosedNotices(ctx, m, "https://portal.example.com"); mails != 0 || len(m.sent) != 2 {
		t.Fatalf("second run sent %d more mails", mails)
	}
	// A later closure is mentioned alone.
	closeFor(t, s, co, a, "Worker Four")
	if mails, docs, _ := s.RunClosedNotices(ctx, m, "https://portal.example.com"); mails != 1 || docs != 1 || strings.Contains(m.last(t).Body, "Worker One") {
		t.Fatalf("next day: %d mails about %d documents\n%s", mails, docs, m.last(t).Body)
	}
	if !hasEvent(t, s, EntitySubmitter, a, "ClosedNoticeSent") {
		t.Error("no ClosedNoticeSent event")
	}
}

func TestAFailedMailIsRetriedAndUnreachableAccountsAreSkipped(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	ok := activeSubmitter(t, s, co, "hr.ok@example.com")
	co2 := company(t, s, "PT Bounce", 0) // another company, so no colleague covers the bounce
	bounced := activeSubmitter(t, s, co2, "hr.bounce@example.com")
	closeFor(t, s, co, ok, "Worker One")
	closeFor(t, s, co2, bounced, "Worker Two")
	if _, err := s.DB.Exec(`UPDATE Submitter SET EmailBouncedAt = ? WHERE SubmitterID = ?`, s.stamp(), bounced); err != nil {
		t.Fatal(err)
	}

	down := &fakeMailer{fail: errors.New("smtp down")}
	if mails, _, err := s.RunClosedNotices(ctx, down, "https://p"); mails != 0 || err == nil {
		t.Fatalf("a failing mailer: %d mails, %v; want 0 and an error", mails, err)
	}
	up := &fakeMailer{}
	if mails, docs, err := s.RunClosedNotices(ctx, up, "https://p"); err != nil || mails != 1 || docs != 1 || up.sent[0].To != "hr.ok@example.com" {
		t.Fatalf("retry: %d mails, %d docs, %v, sent %+v; want one to the reachable submitter", mails, docs, err, up.sent)
	}
}

func TestEnsureNoticeColumnDoesNotAnnounceThePast(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	a := activeSubmitter(t, s, co, "hr.a@example.com")
	closeFor(t, s, co, a, "Old Worker")
	// A database from before the column: drop it, then upgrade as startup does.
	if _, err := s.DB.Exec(`ALTER TABLE McuSubmission DROP COLUMN CertNotifiedAt`); err != nil {
		t.Fatal(err)
	}
	if err := EnsureNoticeColumn(s.DB); err != nil {
		t.Fatal(err)
	}
	if err := EnsureNoticeColumn(s.DB); err != nil {
		t.Fatalf("second run: %v", err)
	}
	m := &fakeMailer{}
	if mails, _, err := s.RunClosedNotices(ctx, m, "https://p"); err != nil || mails != 0 {
		t.Fatalf("an old Closed document was announced: %d mails, %v", mails, err)
	}
	closeFor(t, s, co, a, "New Worker")
	if mails, docs, _ := s.RunClosedNotices(ctx, m, "https://p"); mails != 1 || docs != 1 {
		t.Fatalf("a new closure: %d mails about %d documents", mails, docs)
	}
}

func TestRejectedDocumentsAreInTheDailyMailOnceAndWithoutTheReason(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	a := activeSubmitter(t, s, co, "hr.rej@example.com")
	old, fresh := sentDocIn(t, s, a, "Old Rejected", "Fresh Rejected")
	if err := s.RejectSubmission(ctx, old, testStaffID, "Scan is blurry"); err != nil {
		t.Fatal(err)
	}
	// Startup marks the rejections that already exist as told, once.
	if err := s.BackfillRejectNotices(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.RejectSubmission(ctx, fresh, testStaffID, "Secret reason text"); err != nil {
		t.Fatal(err)
	}
	if err := s.BackfillRejectNotices(ctx); err != nil { // a restart must not swallow the new one
		t.Fatal(err)
	}
	closeFor(t, s, co, a, "Done Worker")

	m := &fakeMailer{}
	mails, docs, err := s.RunClosedNotices(ctx, m, "https://p")
	if err != nil || mails != 1 || docs != 2 {
		t.Fatalf("mails %d docs %d err %v", mails, docs, err)
	}
	got := m.sent[0]
	if !strings.Contains(got.Subject, "1 completed, 1 need a new upload") {
		t.Errorf("subject %q", got.Subject)
	}
	for _, want := range []string{"Done Worker", "Fresh Rejected", "corrected document"} {
		if !strings.Contains(got.Body, want) {
			t.Errorf("mail lacks %q:\n%s", want, got.Body)
		}
	}
	for _, bad := range []string{"Old Rejected", "Secret reason text", "Scan is blurry"} {
		if strings.Contains(got.Body, bad) {
			t.Errorf("mail must not contain %q", bad)
		}
	}
	if mails, _, _ := s.RunClosedNotices(ctx, m, "https://p"); mails != 0 {
		t.Errorf("second run sent %d more mails", mails)
	}
}

// The reviewers set in the portal admin's settings get a copy of every notice (owner, 2026-11-10):
// a separate mail each, never a Cc header, only when the submitter side was accepted, and never to
// a reviewer who is switched off or who already received the mail as an addressee.
func TestNoticesAreCopiedToTheActiveReviewers(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	a := activeSubmitter(t, s, co, "hr.copy@example.com")
	mustReviewer(t, s, "doc1@example.com")
	mustReviewer(t, s, "doc2@example.com")
	off := mustReviewer(t, s, "off@example.com")
	if _, err := s.DB.Exec(`UPDATE McuReviewerEmail SET Active = 0 WHERE ReviewerEmailID = ?`, off); err != nil {
		t.Fatal(err)
	}
	mustReviewer(t, s, "hr.copy@example.com") // also the addressee: must not get two mails
	closeFor(t, s, co, a, "Copied Worker")

	// A failing mailer: nobody was told, so no copies either.
	down := &fakeMailer{fail: errors.New("smtp down")}
	if _, _, err := s.RunClosedNotices(ctx, down, "https://p"); err == nil || len(down.sent) != 0 {
		t.Fatalf("failing mailer: %v, %d sent", err, len(down.sent))
	}
	m := &fakeMailer{}
	if mails, docs, err := s.RunClosedNotices(ctx, m, "https://p"); err != nil || mails != 1 || docs != 1 {
		t.Fatalf("run: %d mails, %d documents, %v", mails, docs, err)
	}
	got := map[string]string{}
	for _, msg := range m.sent {
		got[msg.To] = msg.Subject
	}
	if len(m.sent) != 3 || got["hr.copy@example.com"] == "" || got["doc1@example.com"] == "" || got["doc2@example.com"] == "" {
		t.Fatalf("mails went to %v, want the submitter once and the two active reviewers", got)
	}
	if got["off@example.com"] != "" {
		t.Error("a switched-off reviewer got a copy")
	}
	for _, msg := range m.sent {
		isCopy := strings.HasPrefix(msg.Subject, "Copy: ")
		if (msg.To == "hr.copy@example.com") == isCopy {
			t.Errorf("%s: subject %q, only the reviewers' mails are marked as copies", msg.To, msg.Subject)
		}
		if isCopy && !strings.Contains(msg.Body, "hr.copy@example.com") {
			t.Errorf("the copy to %s does not say who it went to:\n%s", msg.To, msg.Body)
		}
		if strings.Contains(strings.ToLower(msg.Body), "cc:") {
			t.Errorf("a Cc line would expose the reviewers' addresses:\n%s", msg.Body)
		}
	}
	// A second run finds nothing to say, so nothing is copied either.
	if mails, _, _ := s.RunClosedNotices(ctx, m, "https://p"); mails != 0 || len(m.sent) != 3 {
		t.Fatalf("second run: %d mails, %d total sent", mails, len(m.sent))
	}
}
