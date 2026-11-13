package mcusubmit

import (
	"context"
	"errors"
	"testing"
)

// company adds a company and, when parent is not 0, the CompanyRelation row that makes it that
// parent's subcontractor.
func company(t *testing.T, s *Store, name string, parent int64) int64 {
	t.Helper()
	res, err := s.DB.Exec(`INSERT INTO Company (Name) VALUES (?)`, name)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if parent != 0 {
		if _, err := s.DB.Exec(`INSERT INTO CompanyRelation (RelationCode, FK_CompanyID, FK_ParentID) VALUES (?, ?, ?)`, name+"-rel", id, parent); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func TestCompanyTreeFollowsSubcontractorsDownButNeverUp(t *testing.T) {
	s, _, main := newTestStore(t)
	ctx := context.Background()
	sub := company(t, s, "PT Sub", main)
	subsub := company(t, s, "PT SubSub", sub)
	company(t, s, "PT Sibling", main)

	got, err := s.CompanyTree(ctx, main)
	if err != nil || len(got) != 4 {
		t.Fatalf("main tree = %v, %v; want main, sub, subsub, sibling", got, err)
	}
	if got, _ := s.CompanyTree(ctx, sub); len(got) != 2 || got[0] != sub || got[1] != subsub {
		t.Errorf("sub tree = %v, want itself and its own subcontractor only", got)
	}
	if got, _ := s.CompanyTree(ctx, subsub); len(got) != 1 {
		t.Errorf("a company with no subcontractors sees only itself, got %v", got)
	}
}

func TestAMainContractorSeesSubcontractorDocumentsAndNotTheOtherWayRound(t *testing.T) {
	s, _, main := newTestStore(t)
	ctx := context.Background()
	sub := company(t, s, "PT Sub", main)
	other := company(t, s, "PT Other", 0)
	boss := activeSubmitter(t, s, main, "boss@example.com")
	worker := activeSubmitter(t, s, sub, "sub@example.com")
	stranger := activeSubmitter(t, s, other, "stranger@example.com")
	sentDoc(t, s, worker, "Sub Worker")
	addClean(t, s, worker, "Sub Draft") // unsent work stays private
	sentDoc(t, s, boss, "Boss Worker")
	sentDoc(t, s, stranger, "Stranger Worker")

	names := func(u int64, account int64) map[string]bool {
		me, _ := s.ByID(ctx, u)
		sc, _ := s.ScopeFor(ctx, &me)
		page, err := s.SearchDocs(ctx, DocFilter{OwnerID: u, Scope: &sc, Account: account})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, d := range page.Rows {
			out[d.PatName] = true
		}
		return out
	}
	if got := names(boss, 0); !got["Sub Worker"] || !got["Boss Worker"] || got["Sub Draft"] || got["Stranger Worker"] || len(got) != 2 {
		t.Errorf("the main contractor sees %v", got)
	}
	if got := names(boss, sub); !got["Sub Worker"] || got["Boss Worker"] {
		t.Errorf("the company filter shows %v", got)
	}
	if got := names(worker, 0); !got["Sub Worker"] || !got["Sub Draft"] || got["Boss Worker"] || len(got) != 2 {
		t.Errorf("the subcontractor sees %v, want only its own documents", got)
	}
	if got := names(stranger, 0); len(got) != 1 || !got["Stranger Worker"] {
		t.Errorf("another company sees %v", got)
	}
	me, _ := s.ByID(ctx, stranger)
	sc, _ := s.ScopeFor(ctx, &me)
	if _, err := s.SearchDocs(ctx, DocFilter{OwnerID: stranger, Scope: &sc, Account: main}); !errors.Is(err, ErrBadFilter) {
		t.Errorf("asking for a company outside the scope = %v, want ErrBadFilter", err)
	}
}

func TestOnlyTheOwnerOpensAnUnsentFileButColleaguesOpenSentOnes(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	a := activeSubmitter(t, s, co, "alice.hr@example.com")
	b := activeSubmitter(t, s, co, "bobby.hr@example.com")
	// The draft is added after the batch was sent, so it sits in a new, unsent batch.
	sent := sentDoc(t, s, a, "Sent Worker")
	draft := addClean(t, s, a, "Draft Worker")
	if _, err := s.DB.Exec(`UPDATE McuSubmission SET ScanStatus = ?`, ScanClean); err != nil {
		t.Fatal(err)
	}
	me, _ := s.ByID(ctx, b)
	sc, _ := s.ScopeFor(ctx, &me)
	if _, err := s.OwnFile(ctx, sc, draft); !errors.Is(err, ErrNotFound) {
		t.Errorf("a colleague opened an unsent draft: %v", err)
	}
	if _, err := s.OwnFile(ctx, sc, sent); err != nil {
		t.Errorf("a colleague cannot open a sent document: %v", err)
	}
}

func TestACompanyHasTwoAccountsAndAnArchivedOneFreesAPlace(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	activeSubmitter(t, s, co, "one@example.com")
	two := activeSubmitter(t, s, co, "two@example.com")
	if _, err := s.Register(ctx, "three@example.com", "Third Person", co, "HR"); !errors.Is(err, ErrCompanyFull) {
		t.Fatalf("third account = %v, want ErrCompanyFull", err)
	}
	if err := s.Archive(ctx, two); err != nil {
		t.Fatal(err)
	}
	id, err := s.Register(ctx, "three@example.com", "Third Person", co, "HR")
	if err != nil {
		t.Fatalf("after archiving, a new request = %v", err)
	}
	// The archived account cannot come back while the places are taken again.
	if _, err := s.DB.Exec(`UPDATE Submitter SET Status = ? WHERE SubmitterID = ?`, StatusRequested, id); err != nil {
		t.Fatal(err)
	}
	if err := s.Restore(ctx, two); !errors.Is(err, ErrCompanyFull) {
		t.Errorf("restore into a full company = %v, want ErrCompanyFull", err)
	}
}

func TestTheCompanysOtherAccountGetsACopyAndCoversABouncingMailbox(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	a := activeSubmitter(t, s, co, "hr.a@example.com")
	activeSubmitter(t, s, co, "hr.b@example.com")
	closeFor(t, s, co, a, "Worker One")
	m := &fakeMailer{}
	if mails, docs, err := s.RunClosedNotices(ctx, m, "https://portal.example.com"); err != nil || mails != 1 || docs != 1 {
		t.Fatalf("mails %d docs %d err %v", mails, docs, err)
	}
	if len(m.sent) != 2 || m.sent[0].To != "hr.a@example.com" || m.sent[1].To != "hr.b@example.com" {
		t.Fatalf("recipients = %+v, want the uploader and the other account", m.sent)
	}

	// A's mailbox bounces: B's copy is now the mail, and the document is still told.
	closeFor(t, s, co, a, "Worker Two")
	if _, err := s.DB.Exec(`UPDATE Submitter SET EmailBouncedAt = ? WHERE SubmitterID = ?`, "2026-10-28T09:00:00", a); err != nil {
		t.Fatal(err)
	}
	m2 := &fakeMailer{}
	if mails, docs, err := s.RunClosedNotices(ctx, m2, "https://portal.example.com"); err != nil || mails != 1 || docs != 1 {
		t.Fatalf("with a bouncing mailbox: mails %d docs %d err %v", mails, docs, err)
	}
	if len(m2.sent) != 1 || m2.sent[0].To != "hr.b@example.com" {
		t.Fatalf("recipients = %+v, want only the other account", m2.sent)
	}
}

func TestChangeUsernameKeepsTheRulesAndRefusesATakenName(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	a := activeSubmitter(t, s, co, "alice.hr@example.com")
	activeSubmitter(t, s, co2(t, s), "bobby.hr@example.com")
	if err := s.ChangeUsername(ctx, a, "New.Name_1"); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.ByID(ctx, a); u.Username != "new.name_1" {
		t.Errorf("username = %q, want it lower-cased", u.Username)
	}
	if err := s.ChangeUsername(ctx, a, "ab"); err == nil {
		t.Error("a two-letter username was accepted")
	}
	if err := s.ChangeUsername(ctx, a, "admin"); err == nil {
		t.Error("the reserved name was accepted")
	}
	if err := s.ChangeUsername(ctx, a, "bobby.hr"); !errors.Is(err, ErrUsernameTaken) {
		t.Errorf("a taken name = %v, want ErrUsernameTaken", err)
	}
	// Keeping the name it already has is not "taken".
	if err := s.ChangeUsername(ctx, a, "new.name_1"); err != nil {
		t.Errorf("re-saving its own name = %v", err)
	}
}

// co2 adds a second company for tests that need accounts that do not share one.
func co2(t *testing.T, s *Store) int64 { return company(t, s, "PT Second", 0) }

func TestAColleagueMayWithdrawAPendingDocumentAndAParentMayNot(t *testing.T) {
	s, _, main := newTestStore(t)
	ctx := context.Background()
	sub := company(t, s, "PT Sub", main)
	first := activeSubmitter(t, s, sub, "first.hr@example.com")
	second := activeSubmitter(t, s, sub, "second.hr@example.com")
	boss := activeSubmitter(t, s, main, "boss.hr@example.com")
	id := sentDoc(t, s, first, "Pending Worker")
	if _, err := s.Withdraw(ctx, boss, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the main contractor withdrew a subcontractor's document: %v", err)
	}
	if _, err := s.Withdraw(ctx, second, id); err != nil {
		t.Fatalf("the colleague could not withdraw: %v", err)
	}
	if !hasEvent(t, s, EntitySubmission, id, "Withdrawn") {
		t.Error("no Withdrawn event")
	}
}

// "Needs action" (owner, 2026-11-05): a rejected document, or one with a follow-up result the
// submitter was already mailed about (CertNotifiedAt). Only Fit and Fit with note close a document (owner,
// 2026-11-10), so those follow-up documents are Reviewed with a notice, not Closed. A Fit one, one
// not yet told, and one a follow-up checkup already renewed need nothing.
func TestNeedsActionListsRejectedAndFollowUpResultsOnly(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hr.needs@example.com")
	followUp := func(name, result string, notified bool, renewed string) {
		t.Helper()
		id := closeFor(t, s, co, sub, name) // links a Fit record and closes it
		if _, err := s.DB.Exec(`UPDATE McuRecord SET ResultCriteria = ?, HcValidityStatus = NULLIF(?, '') WHERE CheckUpID = ?`, result, renewed, "MedC-"+itoa(id)); err != nil {
			t.Fatal(err)
		}
		notice := any(nil)
		if notified {
			notice = "2026-11-09T08:00:00"
		}
		if _, err := s.DB.Exec(`UPDATE McuSubmission SET Status = ?, CertNotifiedAt = ? WHERE SubmissionID = ?`, SubReviewed, notice, id); err != nil {
			t.Fatal(err)
		}
	}
	followUp("Unfit Told", "Unfit", true, "")
	followUp("Unfit Not Told", "Unfit", false, "")
	followUp("Unfit Renewed", "Unfit", true, "Renewed by MedC-NEW")
	closeFor(t, s, co, sub, "Fit Worker") // Closed with a Fit result
	sentDoc(t, s, sub, "Pending Worker")
	rejected := sentDoc(t, s, sub, "Rejected Worker")
	if _, err := s.DB.Exec(`UPDATE McuSubmission SET Status = ? WHERE SubmissionID = ?`, SubRejected, rejected); err != nil {
		t.Fatal(err)
	}
	me, _ := s.ByID(ctx, sub)
	sc, _ := s.ScopeFor(ctx, &me)
	page, err := s.SearchDocs(ctx, DocFilter{OwnerID: sub, Scope: &sc, Needs: true})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, d := range page.Rows {
		got[d.PatName] = true
	}
	if len(got) != 2 || !got["Unfit Told"] || !got["Rejected Worker"] {
		t.Errorf("needs action = %v, want the told Unfit one and the rejected one", got)
	}
}

// A worker whose Fit certificate already exists on the MCU tab (sent by another route, for example
// WhatsApp) has nothing left to upload, so their rejected document stops needing action.
func TestNeedsActionSkipsWorkerWhoAlreadyHasAFitCertificate(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hr.cert@example.com")
	covered := sentDoc(t, s, sub, "Has Certificate")
	addRecord(t, s, seedWorker(t, s, "Has Certificate", "1990-04-12", co), "MedC-ELSEWHERE", "Fit", "2027-01-01", "Close", 1)
	bare := sentDoc(t, s, sub, "No Certificate")
	for _, id := range []int64{covered, bare} {
		if _, err := s.DB.Exec(`UPDATE McuSubmission SET Status = ? WHERE SubmissionID = ?`, SubRejected, id); err != nil {
			t.Fatal(err)
		}
	}
	me, _ := s.ByID(ctx, sub)
	sc, _ := s.ScopeFor(ctx, &me)
	page, err := s.SearchDocs(ctx, DocFilter{OwnerID: sub, Scope: &sc, Needs: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 1 || page.Rows[0].PatName != "No Certificate" || page.Total != 1 {
		t.Errorf("needs action = %+v, want only the worker without a certificate", page.Rows)
	}
}
