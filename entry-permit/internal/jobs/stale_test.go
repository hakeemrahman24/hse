package jobs

import (
	"context"
	"strings"
	"testing"

	"entry-permit/internal/permit"
	"entry-permit/internal/staff"
)

// pendingPermit stores a work permit waiting at a stage; created is when it was submitted.
func (e *env) pendingPermit(id, status, created string) {
	e.t.Helper()
	e.exec(`INSERT INTO permit (request_id, lep_no, status, patient_id, worker_name, company, nationality, requester_name, requester_email,
		work_location, mcu_valid_until, lep_valid_until, created_at, work_position)
		VALUES (?, '', ?, 1, 'Budi ' || ?, 'PT Maju', 'Indonesia', 'Rina', 'rina@example.com', 'Onshore', '2027-03-31', '', ?, 'Welder')`,
		id, status, id, created)
}

func TestStaleReminderMailsAStageOnlyWhenSomethingIsOverTheLimit(t *testing.T) {
	// Owner decision 2026-11-06: send only when an item has waited the limit (24h) or more.
	e := newEnv(t)
	e.staff[staff.Stage1] = []string{"anna@example.com"}
	e.staff[staff.Final] = []string{"manager@example.com"}
	e.clock.now = today + " 07:00:00"
	e.pendingPermit("WEP-LATE1", permit.StatusPendingStage1, "2026-11-08 07:00:00") // 48h
	e.pendingPermit("WEP-FRESH", permit.StatusPendingStage1, "2026-11-10 05:00:00") // 2h
	e.pendingPermit("WEP-ONLY2", permit.StatusPendingFinal, "2026-11-10 03:00:00")  // Final has only a fresh one
	e.exec(`INSERT INTO review_step (subject_id, stage, reviewer, decision, at) VALUES ('WEP-ONLY2', 'stage1', 'R', 'approved', '2026-11-10 03:00:00')`)

	out, err := e.r.StaleReminder(context.Background())
	if err != nil || out.Changed != 1 || out.Errors != 0 {
		t.Fatalf("out %+v err %v", out, err)
	}
	if len(e.mailer.sent) != 1 {
		t.Fatalf("%d mails sent, want 1 (Stage 1 only)", len(e.mailer.sent))
	}
	m := e.mailer.sent[0]
	if strings.Join(m.To, ",") != "anna@example.com" || m.Subject != "[Entry Permit] 2 pending Stage 1 Reviewer (1 over 24h)" {
		t.Errorf("mail %+v", m)
	}
	for _, want := range []string{"WEP-LATE1", "48h", "WEP-FRESH", "2h", "https://permit.example/staff/stage1"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("text lacks %q", want)
		}
	}
}

func TestStaleReminderGoesToTheRightRoleForEachStage(t *testing.T) {
	e := newEnv(t)
	e.staff[staff.Stage1], e.staff[staff.Doctor], e.staff[staff.Final] = []string{"s1@example.com"}, []string{"doc@example.com"}, []string{"fin@example.com"}
	e.clock.now = today + " 07:00:00"
	e.pendingPermit("WEP-AAAAA", permit.StatusPendingStage1, "2026-11-01 07:00:00")
	e.pendingPermit("WEP-BBBBB", permit.StatusPendingFinal, "2026-11-01 07:00:00")
	e.waiver("WVR-DOCTR", "PendingDoctor", "2026-11-12", "PendingDoctor")
	if out, err := e.r.StaleReminder(context.Background()); err != nil || out.Changed != 3 {
		t.Fatalf("out %+v err %v", out, err)
	}
	got := map[string]string{}
	for _, m := range e.mailer.sent {
		got[m.To[0]] = m.Subject
	}
	for to, stage := range map[string]string{"s1@example.com": "Stage 1 Reviewer", "doc@example.com": "Doctor", "fin@example.com": "Final Approver"} {
		if !strings.Contains(got[to], stage) {
			t.Errorf("%s got %q, want stage %q", to, got[to], stage)
		}
	}
}

func TestStaleReminderWithNoRecipientIsLoggedAndCountedAsSkipped(t *testing.T) {
	e := newEnv(t)
	e.clock.now = today + " 07:00:00"
	e.pendingPermit("WEP-AAAAA", permit.StatusPendingStage1, "2026-11-01 07:00:00")
	out, err := e.r.StaleReminder(context.Background())
	if err != nil || out.Changed != 0 || out.Errors != 1 {
		t.Fatalf("out %+v err %v: a stage with no mailbox must be reported as skipped", out, err)
	}
	if len(e.mailer.sent) != 0 {
		t.Error("a mail was sent to nobody")
	}
}

func TestStaleReminderWithNothingWaitingDoesNothing(t *testing.T) {
	e := newEnv(t)
	out, err := e.r.StaleReminder(context.Background())
	if err != nil || out != (Outcome{}) || len(e.mailer.sent) != 0 {
		t.Fatalf("out %+v err %v mails %d", out, err, len(e.mailer.sent))
	}
}
