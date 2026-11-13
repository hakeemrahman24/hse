package mcusubmit

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

const (
	maxFailedLogins = 5
	lockDuration    = 15 * time.Minute
)

// transition runs one guarded UPDATE on a submitter. The WHERE clause carries the rule (allowed
// statuses, and IsBuiltIn = 0 so the hidden admin cannot be touched), so the check and the change
// are one statement and two admins clicking at once cannot both win.
//
// When nothing changed, the account either does not exist or is the built-in admin (ErrNotFound,
// the same answer as for a missing id, so the hidden admin is not revealed) or it exists in the
// wrong status (ErrBadState).
func (s *Store) transition(ctx context.Context, id int64, set string, setArgs []any, extraWhere string, statuses ...string) error {
	marks := strings.TrimSuffix(strings.Repeat("?,", len(statuses)), ",")
	args := append(append([]any{}, setArgs...), id)
	for _, st := range statuses {
		args = append(args, st)
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE Submitter SET `+set+` WHERE SubmitterID = ? AND IsBuiltIn = 0 AND Status IN (`+marks+`)`+extraWhere, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	u, err := s.ByID(ctx, id)
	if err != nil || u.IsBuiltIn {
		return ErrNotFound
	}
	return ErrBadState
}

// MarkVerified records that the verification link was opened. Idempotent.
func (s *Store) MarkVerified(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE Submitter SET EmailVerifiedAt = COALESCE(EmailVerifiedAt, ?) WHERE SubmitterID = ?`, s.stamp(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Approve moves a verified request to Approved. An unverified request cannot be approved: it is
// not in the admin queue yet (plan section 4, step 1).
func (s *Store) Approve(ctx context.Context, id, byAdminID int64) error {
	return s.transition(ctx, id, `Status = ?, ApprovedAt = ?, FK_ApprovedBySubmitterID = ?`,
		[]any{StatusApproved, s.stamp(), byAdminID}, ` AND EmailVerifiedAt IS NOT NULL`, StatusRequested)
}

// Reject refuses a request that is still waiting, with a reason the submitter is emailed.
func (s *Store) Reject(ctx context.Context, id int64, reason string) error {
	return s.transition(ctx, id, `Status = ?, RejectReason = ?`, []any{StatusRejected, reason}, ``, StatusRequested)
}

// Disable blocks an account at any time and ends its open sessions: bumping SessionVersion makes
// every signed cookie issued before now fail on its next request.
func (s *Store) Disable(ctx context.Context, id int64) error {
	return s.transition(ctx, id, `Status = ?, SessionVersion = SessionVersion + 1`, []any{StatusDisabled}, ``,
		StatusRequested, StatusApproved, StatusActive)
}

// CompleteSetup stores the chosen username and password hash of an Approved account and makes it
// Active. The caller has already run ValidatePassword; usernames are stored lower-cased so
// "Budi" and "budi" cannot both exist.
func (s *Store) CompleteSetup(ctx context.Context, id int64, username, passwordHash string) error {
	if err := ValidUsername(username); err != nil {
		return err
	}
	username = strings.ToLower(username)
	if _, err := s.ByUsername(ctx, username); err == nil {
		return ErrUsernameTaken
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	err := s.transition(ctx, id, `Username = ?, PasswordHash = ?, Status = ?`,
		[]any{username, passwordHash, StatusActive}, ``, StatusApproved)
	if isUniqueViolation(err) {
		return ErrUsernameTaken
	}
	return err
}

// SetPassword replaces the hash and signs out every other session. It also works for the built-in
// admin, who changes their own password from Settings like anyone else.
func (s *Store) SetPassword(ctx context.Context, id int64, passwordHash string) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE Submitter SET PasswordHash = ?, SessionVersion = SessionVersion + 1, FailedLogins = 0, LockedUntil = NULL,
		 MustChangePassword = 0 WHERE SubmitterID = ?`, passwordHash, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordLoginFailure counts a wrong password and locks the account for 15 minutes from the fifth
// in a row. The count is only reset by a successful sign-in or a password change.
func (s *Store) RecordLoginFailure(ctx context.Context, id int64) error {
	if _, err := s.DB.ExecContext(ctx, `UPDATE Submitter SET FailedLogins = FailedLogins + 1 WHERE SubmitterID = ?`, id); err != nil {
		return err
	}
	var failed int
	if err := s.DB.QueryRowContext(ctx, `SELECT FailedLogins FROM Submitter WHERE SubmitterID = ?`, id).Scan(&failed); err != nil {
		return err
	}
	if failed < maxFailedLogins {
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE Submitter SET LockedUntil = ? WHERE SubmitterID = ?`,
		stampOf(s.now().Add(lockDuration)), id)
	return err
}

// RecordLoginSuccess clears the failure count and the lock and notes the sign-in time.
func (s *Store) RecordLoginSuccess(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE Submitter SET FailedLogins = 0, LockedUntil = NULL, LastLoginAt = ? WHERE SubmitterID = ?`, s.stamp(), id)
	return err
}

// IsLocked reports whether the account is inside a lockout. Text timestamps in one fixed UTC
// format compare correctly as strings.
func (s *Store) IsLocked(ctx context.Context, id int64) (bool, error) {
	var until sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT LockedUntil FROM Submitter WHERE SubmitterID = ?`, id).Scan(&until)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	return until.Valid && until.String > s.stamp(), err
}

// PurgeUnverified deletes requests whose email link was never opened within olderThan, with their
// tokens. Without it someone could park another person's address on the UNIQUE email and lock the
// real owner out (plan section 4). Returns how many requests were removed.
func (s *Store) PurgeUnverified(ctx context.Context, olderThan time.Duration) (int, error) {
	cutoff := stampOf(s.now().Add(-olderThan))
	stale := `SELECT SubmitterID FROM Submitter
		WHERE Status = ? AND EmailVerifiedAt IS NULL AND IsBuiltIn = 0 AND CreatedAt < ?`
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM SubmitterToken WHERE FK_SubmitterID IN (`+stale+`)`, StatusRequested, cutoff); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM Submitter WHERE Status = ? AND EmailVerifiedAt IS NULL AND IsBuiltIn = 0 AND CreatedAt < ?`,
		StatusRequested, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), tx.Commit()
}

// EnsureBuiltInAdmin creates the hidden portal admin once, from the password given by the
// environment (MCU_PORTAL_ADMIN_PASSWORD). Unlike the staff seed, no password is fixed in the
// source: this login faces the internet. An empty password creates nothing (the caller logs
// that); a weak one is refused. An existing admin is never touched, so a restart with another
// value cannot overwrite a password the admin has since changed.
func (s *Store) EnsureBuiltInAdmin(ctx context.Context, password string) (bool, error) {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM Submitter WHERE IsBuiltIn = 1`).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 || password == "" {
		return false, nil
	}
	if err := ValidateOperatorPassword(password, builtInUsername, builtInEmail); err != nil {
		return false, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return false, err
	}
	now := s.stamp()
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO Submitter (Email, Username, PasswordHash, Name, Role, IsBuiltIn, Status, EmailVerifiedAt, CreatedAt)
		 VALUES (?, ?, ?, 'Portal administrator', ?, 1, ?, ?, ?)`,
		builtInEmail, builtInUsername, hash, RoleAdmin, StatusActive, now, now)
	return err == nil, err
}

// ResetBuiltInAdmin is the recovery path (MCU_PORTAL_ADMIN_RESET=1 for one start): the hidden
// admin has no real email, so "forgot password" cannot work for it.
func (s *Store) ResetBuiltInAdmin(ctx context.Context, password string) error {
	if err := ValidateOperatorPassword(password, builtInUsername, builtInEmail); err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx,
		`UPDATE Submitter SET PasswordHash = ?, SessionVersion = SessionVersion + 1, FailedLogins = 0, LockedUntil = NULL
		 WHERE IsBuiltIn = 1`, hash)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ChangeEmail replaces the login email after the person opened the link sent to the new address.
// A bounce flag belonged to the old address, so it is cleared. Two accounts can never share an
// address: a duplicate gives ErrEmailTaken.
func (s *Store) ChangeEmail(ctx context.Context, id int64, newEmail string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE Submitter SET Email = ?, EmailBouncedAt = NULL WHERE SubmitterID = ? AND IsBuiltIn = 0`,
		normalizeEmail(newEmail), id)
	if isUniqueViolation(err) {
		return ErrEmailTaken
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ChangeUsername lets a signed-in account pick another username (owner, 2026-11-05: a company's
// two accounts are handed over by changing the details, not by adding a third account). The new name
// follows the same rules as at set-up and may not be taken by another account.
func (s *Store) ChangeUsername(ctx context.Context, id int64, username string) error {
	if err := ValidUsername(username); err != nil {
		return err
	}
	username = strings.ToLower(strings.TrimSpace(username))
	if other, err := s.ByUsername(ctx, username); err == nil && other.ID != id {
		return ErrUsernameTaken
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE Submitter SET Username = ? WHERE SubmitterID = ? AND IsBuiltIn = 0 AND Status = ?`, username, id, StatusActive)
	if isUniqueViolation(err) {
		return ErrUsernameTaken
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetReminders switches the expiry reminder mails on or off for one submitter.
func (s *Store) SetReminders(ctx context.Context, id int64, on bool) error {
	v := 0
	if on {
		v = 1
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE Submitter SET RemindersEnabled = ? WHERE SubmitterID = ?`, v, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
