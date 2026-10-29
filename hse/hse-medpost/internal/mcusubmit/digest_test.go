package mcusubmit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// partialMailer fails for chosen recipients, like a mail server that refuses one address.
type partialMailer struct {
	sent []sentMail
	bad  map[string]bool
}

func (p *partialMailer) Send(_ context.Context, to, subject, body string) error {
	if p.bad[to] {
		return errors.New("refused")
	}
	p.sent = append(p.sent, sentMail{to, subject, body})
	return nil
}

func digestStore(t *testing.T) (*Store, *fixedClock, int64) {
	t.Helper()
	s, clock, co := newTestStore(t)
	sub := activeSubmitter(t, s, co, "hrone@example.com")
	return s, clock, sub
}

func mustReviewer(t *testing.T, s *Store, email string) int64 {
	t.Helper()
	id, err := s.AddReviewer(context.Background(), email, "Dr "+email)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDigestListsOnlyPendingDocumentsAndNoSensitiveData(t *testing.T) {
	s, _, sub := digestStore(t)
	ctx := context.Background()
	mustReviewer(t, s, "doc@example.com")
	sentDoc(t, s, sub, "Worker Pending")
	reviewed := sentDoc(t, s, sub, "Worker Reviewed")
	_ = s.MarkReviewed(ctx, reviewed, 1)
	addClean(t, s, sub, "Worker Draft") // unsent

	m := &fakeMailer{}
	n, err := s.RunDigest(ctx, m, "https://staff.example/mcu-submissions")
	if err != nil || n != 1 || len(m.sent) != 1 {
		t.Fatalf("sent %d, %v, mails %d", n, err, len(m.sent))
	}
	body := m.sent[0].Body
	for _, want := range []string{"Worker Pending", "Welder", "PT Example", "Budi Santoso", "https://staff.example/mcu-submissions"} {
		if !strings.Contains(body, want) {
			t.Errorf("digest lacks %q\n%s", want, body)
		}
	}
	for _, bad := range []string{"Worker Reviewed", "Worker Draft", "3201011204900001", "1990-04-12"} {
		if strings.Contains(body, bad) {
			t.Errorf("digest must not contain %q", bad)
		}
	}
	if !strings.Contains(m.sent[0].Subject, "1") {
		t.Errorf("subject %q should carry the count", m.sent[0].Subject)
	}
	if !hasEvent(t, s, EntitySystem, 0, "DigestSent") {
		t.Error("the digest left no DigestSent event under the System entity")
	}
}

func TestDigestRespectsTheIntervalAndOnlyMarksWhenSent(t *testing.T) {
	s, clock, sub := digestStore(t)
	ctx := context.Background()
	mustReviewer(t, s, "doc@example.com")
	sentDoc(t, s, sub, "Worker One")
	if err := s.SaveDigestInterval(ctx, 36); err != nil {
		t.Fatal(err)
	}
	m := &fakeMailer{}
	if n, _ := s.RunDigest(ctx, m, ""); n != 1 {
		t.Fatalf("the first digest should go at once, sent %d", n)
	}
	clock.advance(35 * time.Hour)
	if n, _ := s.RunDigest(ctx, m, ""); n != 0 {
		t.Fatalf("a digest went after 35 h with a 36 h interval")
	}
	clock.advance(time.Hour)
	if n, _ := s.RunDigest(ctx, m, ""); n != 1 {
		t.Fatalf("the digest should be due after 36 h, sent %d", n)
	}
	if len(m.sent) != 2 {
		t.Fatalf("%d mails, want 2", len(m.sent))
	}
}

func TestDigestSendsNothingAndKeepsTheClockWhenNothingIsWaiting(t *testing.T) {
	s, _, _ := digestStore(t)
	ctx := context.Background()
	mustReviewer(t, s, "doc@example.com")
	m := &fakeMailer{}
	if n, err := s.RunDigest(ctx, m, ""); n != 0 || err != nil || len(m.sent) != 0 {
		t.Fatalf("n %d err %v mails %d, want nothing", n, err, len(m.sent))
	}
	cfg, _ := s.DigestSettings(ctx)
	if cfg.LastSentAt != "" {
		t.Fatalf("LastSentAt = %q: an empty digest must not start the interval", cfg.LastSentAt)
	}
}

func TestDigestSkipsInactiveAndBouncedReviewersAndRetriesWhenNobodyGotIt(t *testing.T) {
	s, _, sub := digestStore(t)
	ctx := context.Background()
	mustReviewer(t, s, "active@example.com")
	off := mustReviewer(t, s, "off@example.com")
	mustReviewer(t, s, "bounced@example.com")
	_ = s.SetReviewerActive(ctx, off, false)
	if _, err := s.MarkEmailBounced(ctx, "bounced@example.com", "hard_bounce"); err != nil {
		t.Fatal(err)
	}
	sentDoc(t, s, sub, "Worker One")

	m := &partialMailer{bad: map[string]bool{"active@example.com": true}}
	if n, err := s.RunDigest(ctx, m, ""); n != 0 || err == nil {
		t.Fatalf("n %d err %v; a digest nobody received must report an error", n, err)
	}
	if cfg, _ := s.DigestSettings(ctx); cfg.LastSentAt != "" {
		t.Fatal("a failed digest was marked as sent, so it would never be retried")
	}
	m.bad = nil
	if n, err := s.RunDigest(ctx, m, ""); n != 1 || err != nil {
		t.Fatalf("retry: %d, %v", n, err)
	}
	if len(m.sent) != 1 || m.sent[0].To != "active@example.com" {
		t.Fatalf("recipients = %+v, want only the active, unbounced reviewer", m.sent)
	}
}

func TestDigestWithoutAnyReviewerSendsNothing(t *testing.T) {
	s, _, sub := digestStore(t)
	sentDoc(t, s, sub, "Worker One")
	m := &fakeMailer{}
	if n, _ := s.RunDigest(context.Background(), m, ""); n != 0 || len(m.sent) != 0 {
		t.Fatal("a digest went out with no reviewers")
	}
}

func TestDigestCapsTheListAndSaysHowManyMore(t *testing.T) {
	s, _, sub := digestStore(t)
	ctx := context.Background()
	mustReviewer(t, s, "doc@example.com")
	// The daily upload quota would stop 105 real uploads, so one sent document is copied in SQL.
	sentDoc(t, s, sub, "Worker 000")
	for i := 1; i < digestMaxLines+5; i++ {
		_, err := s.DB.ExecContext(ctx,
			`INSERT INTO McuSubmission (FK_SubmitterID, FK_BatchID, PatName, PatDateOfBirth, PatPosition, PatCompanyText, PatNationality, PatNik, ScanStatus, Status, SubmittedAt)
			 SELECT FK_SubmitterID, FK_BatchID, ?, PatDateOfBirth, PatPosition, PatCompanyText, PatNationality, PatNik, ScanStatus, Status, SubmittedAt
			 FROM McuSubmission WHERE PatName = 'Worker 000'`, fmt.Sprintf("Worker %03d", i))
		if err != nil {
			t.Fatal(err)
		}
	}
	m := &fakeMailer{}
	if _, err := s.RunDigest(ctx, m, ""); err != nil {
		t.Fatal(err)
	}
	body := m.sent[0].Body
	if strings.Count(body, "Worker ") != digestMaxLines || !strings.Contains(body, "and 5 more") {
		t.Fatalf("lines %d, want %d plus a 'and 5 more' note", strings.Count(body, "Worker "), digestMaxLines)
	}
}

func TestDigestIntervalOnlyAllowsTheThreeChoices(t *testing.T) {
	s, _, _ := digestStore(t)
	ctx := context.Background()
	for _, h := range []int{24, 36, 48} {
		if err := s.SaveDigestInterval(ctx, h); err != nil {
			t.Errorf("%d h refused: %v", h, err)
		}
	}
	for _, h := range []int{0, 1, 12, 25, 72, -24} {
		if err := s.SaveDigestInterval(ctx, h); !errors.Is(err, ErrBadInterval) {
			t.Errorf("%d h = %v, want ErrBadInterval", h, err)
		}
	}
	if cfg, _ := s.DigestSettings(ctx); cfg.IntervalHours != 48 {
		t.Errorf("interval = %d, want the last valid choice, 48", cfg.IntervalHours)
	}
}

func TestReviewerListRules(t *testing.T) {
	s, _, _ := digestStore(t)
	ctx := context.Background()
	if _, err := s.AddReviewer(ctx, "not an email", ""); err == nil {
		t.Error("a bad address was accepted")
	}
	id := mustReviewer(t, s, "Doc@Example.com")
	if _, err := s.AddReviewer(ctx, "doc@example.com", "again"); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("a duplicate (any case) = %v, want ErrEmailTaken", err)
	}
	list, _ := s.Reviewers(ctx)
	if len(list) != 1 || list[0].Email != "doc@example.com" || !list[0].Active {
		t.Fatalf("list = %+v", list)
	}
	_ = s.SetReviewerActive(ctx, id, false)
	if list, _ = s.Reviewers(ctx); list[0].Active {
		t.Error("still active after switching off")
	}
	if err := s.DeleteReviewer(ctx, id); err != nil {
		t.Fatal(err)
	}
	if list, _ = s.Reviewers(ctx); len(list) != 0 {
		t.Error("the reviewer is still listed after delete")
	}
	if err := s.DeleteReviewer(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting twice = %v, want ErrNotFound", err)
	}
}
