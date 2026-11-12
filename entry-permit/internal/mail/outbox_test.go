package mail

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"entry-permit/internal/dates"
	"entry-permit/internal/db"
	"entry-permit/migrations"
)

func newOutbox(t *testing.T) (Outbox, *sql.DB) {
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
	return Outbox{DB: d}, d
}

// fakeMailer records what it was asked to send and can be told to fail.
type fakeMailer struct {
	sent []Message
	err  error
}

func (f *fakeMailer) Send(_ context.Context, m Message) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, m)
	return nil
}

var (
	retry = Retry{MaxAttempts: 3, BaseMinutes: 10}
	t0    = dates.Moment("2026-11-07 08:00:00")
	msg   = Message{Kind: "wep-approved", SourceKey: "WEP-ONE01", To: []string{"rina@example.com"}, Subject: "Approved", Text: "t", HTML: "<p>h</p>"}
)

type outRow struct {
	status, last, next string
	attempts           int
}

func rowOf(t *testing.T, d *sql.DB, id int64) outRow {
	t.Helper()
	var r outRow
	if err := d.QueryRow(`SELECT status, attempts, next_attempt, last_error FROM email_outbox WHERE id = ?`, id).Scan(&r.status, &r.attempts, &r.next, &r.last); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestEnqueueStoresAPendingRowAndRefusesBadRecipients(t *testing.T) {
	o, d := newOutbox(t)
	id, err := o.Enqueue(context.Background(), msg, t0)
	if err != nil {
		t.Fatal(err)
	}
	if r := rowOf(t, d, id); r.status != "Pending" || r.attempts != 0 || r.next != string(t0) {
		t.Errorf("row = %+v", r)
	}
	bad := msg
	bad.To = []string{"not-an-address"}
	if _, err := o.Enqueue(context.Background(), bad, t0); err == nil {
		t.Error("a bad recipient must be refused before it is stored")
	}
}

func TestDeliverSendsOnceAndMarksTheRowSent(t *testing.T) {
	o, d := newOutbox(t)
	id, _ := o.Enqueue(context.Background(), msg, t0)
	f := &fakeMailer{}
	if err := o.Deliver(context.Background(), f, id, t0, retry); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 1 || f.sent[0].Subject != "Approved" || f.sent[0].To[0] != "rina@example.com" {
		t.Fatalf("sent %+v", f.sent)
	}
	if r := rowOf(t, d, id); r.status != "Sent" || r.attempts != 1 {
		t.Errorf("row = %+v", r)
	}
	// A second try (a double click, a second drain) sends nothing more.
	if err := o.Deliver(context.Background(), f, id, t0, retry); err != nil || len(f.sent) != 1 {
		t.Errorf("second deliver: err=%v sent=%d", err, len(f.sent))
	}
}

func TestAFailedSendStaysPendingWithAGrowingDelayThenGivesUp(t *testing.T) {
	o, d := newOutbox(t)
	id, _ := o.Enqueue(context.Background(), msg, t0)
	f := &fakeMailer{err: errors.New("smtp down")}

	if err := o.Deliver(context.Background(), f, id, t0, retry); err == nil {
		t.Error("the failure is reported to the caller")
	}
	r := rowOf(t, d, id)
	if r.status != "Pending" || r.attempts != 1 || r.next != "2026-11-07 08:10:00" || r.last != "smtp down" {
		t.Errorf("after 1st failure: %+v", r)
	}
	// Not due yet: the drain leaves it alone.
	res, _ := o.Drain(context.Background(), f, t0.AddMinutes(5), retry)
	if res.Sent+res.Retried+res.Failed != 0 {
		t.Errorf("drain before the due time did work: %+v", res)
	}
	// Due: second failure waits twice as long.
	res, _ = o.Drain(context.Background(), f, t0.AddMinutes(10), retry)
	if res.Retried != 1 {
		t.Errorf("drain result %+v", res)
	}
	if r := rowOf(t, d, id); r.attempts != 2 || r.next != "2026-11-07 08:30:00" {
		t.Errorf("after 2nd failure: %+v", r)
	}
	// Third failure reaches MaxAttempts: given up.
	res, _ = o.Drain(context.Background(), f, t0.AddMinutes(30), retry)
	if res.Failed != 1 {
		t.Errorf("drain result %+v", res)
	}
	if r := rowOf(t, d, id); r.status != "Failed" || r.attempts != 3 {
		t.Errorf("after giving up: %+v", r)
	}
	// A failed row is never retried by the drain.
	f.err = nil
	res, _ = o.Drain(context.Background(), f, t0.AddMinutes(600), retry)
	if len(f.sent) != 0 || res.Sent != 0 {
		t.Error("a Failed row must stay failed until an admin requeues it")
	}
}

func TestDrainSendsEveryDueRowAndRecoversAfterAnOutage(t *testing.T) {
	o, d := newOutbox(t)
	a, _ := o.Enqueue(context.Background(), msg, t0)
	m2 := msg
	m2.SourceKey = "WEP-TWO02"
	b, _ := o.Enqueue(context.Background(), m2, t0)
	down := &fakeMailer{err: errors.New("down")}
	_, _ = o.Drain(context.Background(), down, t0, retry)
	up := &fakeMailer{}
	res, err := o.Drain(context.Background(), up, t0.AddMinutes(10), retry)
	if err != nil || res.Sent != 2 || len(up.sent) != 2 {
		t.Fatalf("res=%+v err=%v sent=%d", res, err, len(up.sent))
	}
	if rowOf(t, d, a).status != "Sent" || rowOf(t, d, b).status != "Sent" {
		t.Error("both rows are Sent")
	}
}

func TestRequeueGivesAFailedRowAFreshStart(t *testing.T) {
	o, d := newOutbox(t)
	id, _ := o.Enqueue(context.Background(), msg, t0)
	f := &fakeMailer{err: errors.New("x")}
	one := Retry{MaxAttempts: 1, BaseMinutes: 1}
	_ = o.Deliver(context.Background(), f, id, t0, one)
	if rowOf(t, d, id).status != "Failed" {
		t.Fatal("setup: the row should have failed")
	}
	if err := o.Requeue(context.Background(), id, t0); err != nil {
		t.Fatal(err)
	}
	if r := rowOf(t, d, id); r.status != "Pending" || r.attempts != 0 {
		t.Errorf("row = %+v", r)
	}
	if err := o.Requeue(context.Background(), 9999, t0); err == nil {
		t.Error("requeue of an unknown id must say so")
	}
}

func TestFailedListsOnlyFailedRowsNewestFirst(t *testing.T) {
	o, _ := newOutbox(t)
	id, _ := o.Enqueue(context.Background(), msg, t0)
	_, _ = o.Enqueue(context.Background(), msg, t0) // stays Pending
	_ = o.Deliver(context.Background(), &fakeMailer{err: errors.New("boom")}, id, t0, Retry{MaxAttempts: 1, BaseMinutes: 1})
	list, err := o.Failed(context.Background())
	if err != nil || len(list) != 1 || list[0].ID != id || list[0].LastError != "boom" || list[0].SourceKey != "WEP-ONE01" {
		t.Errorf("list=%+v err=%v", list, err)
	}
}

func TestCountsAndPendingShowTheBacklogForTheStatusPage(t *testing.T) {
	o, _ := newOutbox(t)
	ctx := context.Background()
	sentID, _ := o.Enqueue(ctx, msg, t0)
	_ = o.Deliver(ctx, &fakeMailer{}, sentID, t0, retry) // Sent
	failID, _ := o.Enqueue(ctx, msg, t0)
	_ = o.Deliver(ctx, &fakeMailer{err: errors.New("boom")}, failID, t0, Retry{MaxAttempts: 1, BaseMinutes: 1}) // Failed
	waitID, _ := o.Enqueue(ctx, msg, t0)
	_ = o.Deliver(ctx, &fakeMailer{err: errors.New("smtp down")}, waitID, t0, retry) // Pending, attempt 1
	_, _ = o.Enqueue(ctx, msg, t0.AddMinutes(1))                                     // Pending, never tried

	c, err := o.Counts(ctx)
	if err != nil || c.Pending != 2 || c.Failed != 1 || c.Sent != 1 {
		t.Fatalf("counts %+v err %v", c, err)
	}
	list, err := o.Pending(ctx, 10)
	if err != nil || len(list) != 2 {
		t.Fatalf("pending %+v err %v", list, err)
	}
	// oldest first: the one that has waited longest is on top
	if list[0].ID != waitID || list[0].Attempts != 1 || list[0].LastError != "smtp down" || list[0].Next == "" {
		t.Errorf("first pending %+v", list[0])
	}
	if list[0].To != "rina@example.com" {
		t.Errorf("recipient missing: %+v", list[0])
	}
	// the list is capped so a huge backlog cannot make the page huge
	if got, _ := o.Pending(ctx, 1); len(got) != 1 {
		t.Errorf("limit 1 gave %d rows", len(got))
	}
}
