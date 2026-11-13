package mcusubmit

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"hse-medpost/internal/db"
)

// fixedClock lets a test move time forward without sleeping.
type fixedClock struct{ t time.Time }

func (c *fixedClock) now() time.Time          { return c.t }
func (c *fixedClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestStore(t *testing.T) (*Store, *fixedClock, int64) {
	t.Helper()
	database, err := db.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	for _, name := range []string{"004_company.sql", "005_patient.sql", "006_entrymcu.sql", "016_mcu_submission.sql", "017_mcu_submission_batches.sql", "020_mcu_portal_digest.sql", "021_mcu_mail_setting.sql", "022_mcu_submission_record_unique.sql", "029_mcu_notice_setting.sql", "032_mcu_reminder_sent.sql"} {
		sqlText, err := db.LoadMigrationFile(filepath.Join("..", "..", "migrations", "sqlite", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(database, sqlText); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// NotifyRequestedAt is added by the startup, as for an older SQLite database.
	if err := EnsureNotifyRequestedColumn(database); err != nil {
		t.Fatal(err)
	}
	if err := EnsureMustChangePasswordColumn(database); err != nil {
		t.Fatal(err)
	}
	// Patient.PersonalID (the NIK or passport) is added to Patient by the entrymcu startup, not by 005.
	if _, err := database.Exec(`ALTER TABLE Patient ADD COLUMN PersonalID TEXT NULL`); err != nil {
		t.Fatal(err)
	}
	// The reviewer's name is read from the staff table (001, not loaded here): a minimal copy.
	if _, err := database.Exec(`CREATE TABLE MedpostStaffTable (StaffID INTEGER PRIMARY KEY, StaffName TEXT NOT NULL);
		INSERT INTO MedpostStaffTable (StaffID, StaffName) VALUES (7, 'Dr Test')`); err != nil {
		t.Fatal(err)
	}
	res, err := database.Exec(`INSERT INTO Company (Name) VALUES ('PT Example')`)
	if err != nil {
		t.Fatal(err)
	}
	companyID, _ := res.LastInsertId()
	clock := &fixedClock{t: time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)}
	s := NewStore(database)
	s.now = clock.now
	return s, clock, companyID
}

func register(t *testing.T, s *Store, companyID int64, email string) int64 {
	t.Helper()
	id, err := s.Register(context.Background(), email, "Budi Santoso", companyID, "HR Officer")
	if err != nil {
		t.Fatalf("Register(%q): %v", email, err)
	}
	return id
}

// approved walks a submitter through verify and approve, ready for CompleteSetup.
func approved(t *testing.T, s *Store, companyID int64, email string) int64 {
	t.Helper()
	ctx := context.Background()
	admin := mustAdmin(t, s)
	id := register(t, s, companyID, email)
	if err := s.MarkVerified(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.Approve(ctx, id, admin); err != nil {
		t.Fatal(err)
	}
	return id
}

func mustAdmin(t *testing.T, s *Store) int64 {
	t.Helper()
	if _, err := s.EnsureBuiltInAdmin(context.Background(), "pelican-orbit-77"); err != nil {
		t.Fatal(err)
	}
	a, err := s.ByUsername(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	return a.ID
}

func TestRegisterLowerCasesEmailAndStartsRequested(t *testing.T) {
	s, _, co := newTestStore(t)
	id := register(t, s, co, "  Budi@Example.COM ")

	got, err := s.ByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != "budi@example.com" || got.Status != StatusRequested || got.Role != RoleSubmitter {
		t.Fatalf("unexpected submitter: %+v", got)
	}
	if got.EmailVerifiedAt != "" {
		t.Fatal("a new request must not be verified")
	}
}

func TestRegisterRefusesADuplicateEmail(t *testing.T) {
	s, _, co := newTestStore(t)
	register(t, s, co, "budi@example.com")
	_, err := s.Register(context.Background(), "BUDI@example.com", "Other", co, "x")
	if !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("error = %v, want ErrEmailTaken", err)
	}
}

func TestApproveNeedsAVerifiedEmail(t *testing.T) {
	s, _, co := newTestStore(t)
	admin := mustAdmin(t, s)
	id := register(t, s, co, "budi@example.com")

	if err := s.Approve(context.Background(), id, admin); !errors.Is(err, ErrBadState) {
		t.Fatalf("Approve before verification = %v, want ErrBadState", err)
	}
}

func TestApproveRejectAndDisableFollowTheStatusRules(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	id := approved(t, s, co, "budi@example.com")

	if err := s.Approve(ctx, id, 1); !errors.Is(err, ErrBadState) {
		t.Fatalf("approving twice = %v, want ErrBadState", err)
	}
	other := register(t, s, co, "siti@example.com")
	if err := s.MarkVerified(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := s.Reject(ctx, other, "Company not recognised"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ByID(ctx, other)
	if got.Status != StatusRejected || got.RejectReason != "Company not recognised" {
		t.Fatalf("after reject: %+v", got)
	}
	if err := s.Reject(ctx, id, "late"); !errors.Is(err, ErrBadState) {
		t.Fatalf("rejecting an approved account = %v, want ErrBadState", err)
	}
}

func TestCompleteSetupMakesTheAccountActive(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	id := approved(t, s, co, "budi@example.com")
	hash, _ := HashPassword("pelican-orbit-77")

	if err := s.CompleteSetup(ctx, id, "budi.s", hash); err != nil {
		t.Fatal(err)
	}
	got, err := s.ByUsername(ctx, "budi.s")
	if err != nil || got.ID != id || got.Status != StatusActive {
		t.Fatalf("ByUsername = %+v, %v", got, err)
	}
}

func TestCompleteSetupRefusesAnUnapprovedAccountAndATakenUsername(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	hash, _ := HashPassword("pelican-orbit-77")

	requested := register(t, s, co, "a@example.com")
	if err := s.CompleteSetup(ctx, requested, "user_a", hash); !errors.Is(err, ErrBadState) {
		t.Fatalf("setup before approval = %v, want ErrBadState", err)
	}
	first := approved(t, s, co, "b@example.com")
	second := approved(t, s, co2(t, s), "c@example.com") // another company: two accounts per company
	if err := s.CompleteSetup(ctx, first, "shared", hash); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteSetup(ctx, second, "SHARED", hash); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("duplicate username = %v, want ErrUsernameTaken", err)
	}
}

func TestTokenWorksOnceForItsPurposeOnly(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	id := register(t, s, co, "budi@example.com")

	raw, err := s.IssueToken(ctx, id, PurposeVerify, "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConsumeToken(ctx, raw, PurposeReset); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("wrong purpose = %v, want ErrTokenInvalid", err)
	}
	tok, err := s.ConsumeToken(ctx, raw, PurposeVerify)
	if err != nil || tok.SubmitterID != id {
		t.Fatalf("first use = %+v, %v", tok, err)
	}
	if _, err := s.ConsumeToken(ctx, raw, PurposeVerify); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("second use = %v, want ErrTokenInvalid", err)
	}
}

// A mail scanner or a link preview opens links before the person does. Looking at a link to show a
// confirm button must therefore not use it up.
func TestPeekTokenDoesNotConsumeTheLink(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	id := register(t, s, co, "budi@example.com")
	raw, _ := s.IssueToken(ctx, id, PurposeVerify, "", time.Hour)

	for i := 0; i < 3; i++ {
		tok, err := s.PeekToken(ctx, raw, PurposeVerify)
		if err != nil || tok.SubmitterID != id {
			t.Fatalf("peek %d = %+v, %v", i+1, tok, err)
		}
	}
	if _, err := s.PeekToken(ctx, raw, PurposeReset); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("peek with the wrong purpose = %v, want ErrTokenInvalid", err)
	}
	if _, err := s.ConsumeToken(ctx, raw, PurposeVerify); err != nil {
		t.Fatalf("the link must still work after peeking: %v", err)
	}
	if _, err := s.PeekToken(ctx, raw, PurposeVerify); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("peek after use = %v, want ErrTokenInvalid", err)
	}
}

func TestTokenExpires(t *testing.T) {
	s, clock, co := newTestStore(t)
	ctx := context.Background()
	id := register(t, s, co, "budi@example.com")
	raw, _ := s.IssueToken(ctx, id, PurposeSetup, "", 72*time.Hour)

	clock.advance(73 * time.Hour)
	if _, err := s.ConsumeToken(ctx, raw, PurposeSetup); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("expired token = %v, want ErrTokenInvalid", err)
	}
}

func TestTokenIsStoredHashedAndUnknownTokensFail(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	id := register(t, s, co, "budi@example.com")
	raw, _ := s.IssueToken(ctx, id, PurposeVerify, "", time.Hour)

	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM SubmitterToken WHERE TokenHash = ?`, raw).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("the raw token must never be stored")
	}
	if _, err := s.ConsumeToken(ctx, "not-a-real-token", PurposeVerify); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("unknown token = %v, want ErrTokenInvalid", err)
	}
}

func TestEmailChangeTokenCarriesTheNewAddress(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	id := register(t, s, co, "budi@example.com")
	raw, _ := s.IssueToken(ctx, id, PurposeEmailChange, "new@example.com", time.Hour)

	tok, err := s.ConsumeToken(ctx, raw, PurposeEmailChange)
	if err != nil || tok.NewEmail != "new@example.com" {
		t.Fatalf("ConsumeToken = %+v, %v", tok, err)
	}
}

func TestFiveWrongPasswordsLockTheAccountForFifteenMinutes(t *testing.T) {
	s, clock, co := newTestStore(t)
	ctx := context.Background()
	id := approved(t, s, co, "budi@example.com")

	for i := 0; i < 4; i++ {
		if err := s.RecordLoginFailure(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if locked, _ := s.IsLocked(ctx, id); locked {
		t.Fatal("locked after only 4 failures")
	}
	if err := s.RecordLoginFailure(ctx, id); err != nil {
		t.Fatal(err)
	}
	if locked, _ := s.IsLocked(ctx, id); !locked {
		t.Fatal("not locked after 5 failures")
	}
	clock.advance(16 * time.Minute)
	if locked, _ := s.IsLocked(ctx, id); locked {
		t.Fatal("still locked after 16 minutes")
	}
}

func TestLoginSuccessClearsTheFailureCount(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	id := approved(t, s, co, "budi@example.com")
	for i := 0; i < 4; i++ {
		_ = s.RecordLoginFailure(ctx, id)
	}
	if err := s.RecordLoginSuccess(ctx, id); err != nil {
		t.Fatal(err)
	}
	_ = s.RecordLoginFailure(ctx, id)
	if locked, _ := s.IsLocked(ctx, id); locked {
		t.Fatal("a success must reset the count, one later failure must not lock")
	}
}

func TestDisableAndPasswordChangeEndOlderSessions(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	id := approved(t, s, co, "budi@example.com")
	before, _ := s.ByID(ctx, id)

	hash, _ := HashPassword("pelican-orbit-88")
	if err := s.SetPassword(ctx, id, hash); err != nil {
		t.Fatal(err)
	}
	afterPassword, _ := s.ByID(ctx, id)
	if afterPassword.SessionVersion <= before.SessionVersion {
		t.Fatal("changing the password must bump SessionVersion")
	}
	if err := s.Disable(ctx, id); err != nil {
		t.Fatal(err)
	}
	afterDisable, _ := s.ByID(ctx, id)
	if afterDisable.Status != StatusDisabled || afterDisable.SessionVersion <= afterPassword.SessionVersion {
		t.Fatalf("Disable must set Disabled and bump SessionVersion: %+v", afterDisable)
	}
}

func TestPurgeUnverifiedRemovesOnlyStaleUnverifiedRequests(t *testing.T) {
	s, clock, co := newTestStore(t)
	ctx := context.Background()
	stale := register(t, s, co, "stale@example.com")
	_, _ = s.IssueToken(ctx, stale, PurposeVerify, "", time.Hour)
	verified := register(t, s, co, "verified@example.com")
	_ = s.MarkVerified(ctx, verified)

	clock.advance(49 * time.Hour)
	fresh := register(t, s, co2(t, s), "fresh@example.com") // another company: two accounts per company

	n, err := s.PurgeUnverified(ctx, 48*time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("PurgeUnverified = %d, %v; want 1, nil", n, err)
	}
	if _, err := s.ByID(ctx, stale); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale request still there: %v", err)
	}
	for _, id := range []int64{verified, fresh} {
		if _, err := s.ByID(ctx, id); err != nil {
			t.Fatalf("submitter %d was purged: %v", id, err)
		}
	}
	var tokens int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM SubmitterToken WHERE FK_SubmitterID = ?`, stale).Scan(&tokens)
	if tokens != 0 {
		t.Fatal("the purged request's token was left behind")
	}
}

func TestBuiltInAdminIsCreatedOnceAndNeverOverwritten(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()

	created, err := s.EnsureBuiltInAdmin(ctx, "pelican-orbit-77")
	if err != nil || !created {
		t.Fatalf("first call = %v, %v; want true, nil", created, err)
	}
	admin, _ := s.ByUsername(ctx, "admin")
	if admin.Role != RoleAdmin || !admin.IsBuiltIn || admin.Status != StatusActive {
		t.Fatalf("unexpected admin: %+v", admin)
	}
	hash, _ := s.passwordHash(ctx, admin.ID)

	created, err = s.EnsureBuiltInAdmin(ctx, "a-different-password-9")
	if err != nil || created {
		t.Fatalf("second call = %v, %v; want false, nil", created, err)
	}
	again, _ := s.passwordHash(ctx, admin.ID)
	if again != hash {
		t.Fatal("a restart with another env password must not overwrite the stored one")
	}
}

func TestBuiltInAdminNeedsAStrongPasswordFromTheEnvironment(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()

	if created, err := s.EnsureBuiltInAdmin(ctx, ""); err != nil || created {
		t.Fatalf("no password = %v, %v; want false, nil (admin simply not created)", created, err)
	}
	// Owner decision 2026-10-29: the admin may use a common password such as 101112, but not a short one.
	if _, err := s.EnsureBuiltInAdmin(ctx, "12345"); err == nil {
		t.Fatal("a five character password must be refused here")
	}
	if _, err := s.ByUsername(ctx, "admin"); !errors.Is(err, ErrNotFound) {
		t.Fatal("no admin should exist after refused passwords")
	}
}

func TestResetBuiltInAdminReplacesThePasswordAndClearsTheLock(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	id := mustAdmin(t, s)
	for i := 0; i < 5; i++ {
		_ = s.RecordLoginFailure(ctx, id)
	}
	if err := s.ResetBuiltInAdmin(ctx, "new-pelican-orbit-1"); err != nil {
		t.Fatal(err)
	}
	if locked, _ := s.IsLocked(ctx, id); locked {
		t.Fatal("reset must clear the lock")
	}
	hash, _ := s.passwordHash(ctx, id)
	if !CheckPassword(hash, "new-pelican-orbit-1") {
		t.Fatal("reset did not set the new password")
	}
}

func TestListSubmittersHidesTheBuiltInAdmin(t *testing.T) {
	s, _, co := newTestStore(t)
	mustAdmin(t, s)
	register(t, s, co, "budi@example.com")

	list, err := s.List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Email != "budi@example.com" {
		t.Fatalf("List = %+v, want only budi", list)
	}
}

func TestOperationsOnTheBuiltInAdminReportNotFound(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	id := mustAdmin(t, s)

	if err := s.Disable(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Disable(admin) = %v, want ErrNotFound", err)
	}
	if err := s.Reject(ctx, id, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Reject(admin) = %v, want ErrNotFound", err)
	}
}

func TestSettingsDefaultToOffAndValidateOnSave(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	admin := mustAdmin(t, s)

	got, err := s.Settings(ctx)
	if err != nil || got.RemindersEnabled || got.DaysBefore != 60 || got.RepeatDays != 30 {
		t.Fatalf("defaults = %+v, %v", got, err)
	}
	if err := s.SaveSettings(ctx, Settings{RemindersEnabled: true, DaysBefore: 0, RepeatDays: 30}, admin); err == nil {
		t.Fatal("0 days before expiry must be refused")
	}
	if err := s.SaveSettings(ctx, Settings{RemindersEnabled: true, DaysBefore: 90, RepeatDays: 366}, admin); err == nil {
		t.Fatal("a repeat period over a year must be refused")
	}
	if err := s.SaveSettings(ctx, Settings{RemindersEnabled: true, DaysBefore: 90, RepeatDays: 14}, admin); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Settings(ctx)
	if !got.RemindersEnabled || got.DaysBefore != 90 || got.RepeatDays != 14 {
		t.Fatalf("saved settings = %+v", got)
	}
}

func TestLogEventIsAppendOnlyAndKeepsTheActor(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()

	err := s.LogEvent(ctx, Event{ActorType: ActorSubmitter, ActorID: 7, EntityType: EntitySubmission, EntityID: 42, Action: "Withdrawn"})
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.Events(ctx, EntitySubmission, 42)
	if err != nil || len(events) != 1 {
		t.Fatalf("Events = %+v, %v", events, err)
	}
	if events[0].ActorType != ActorSubmitter || events[0].ActorID != 7 || events[0].At != "2026-10-28T09:00:00" {
		t.Fatalf("event = %+v", events[0])
	}
	if err := s.LogEvent(ctx, Event{ActorType: "Robot", EntityType: EntitySubmission, EntityID: 1, Action: "X"}); err == nil {
		t.Fatal("an unknown actor type must be refused")
	}
}
