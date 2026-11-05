package mcusubmit

import (
	"context"
	"database/sql"
	"fmt"
)

// Account housekeeping for the portal admin (owner, 2026-11-02): Disable already existed; this
// adds Enable, Archive, Restore and a permanent removal that cascades through the person's data.
//
// StatusArchived hides an account from the admin's list and keeps it from signing in, but keeps
// every document. It needs no schema change: Status is free text checked in Go (VARCHAR(12) on
// MariaDB, and "Archived" is 8 characters).
const StatusArchived = "Archived"

// Enable lets a Disabled account sign in again. Only an account that already chose its username can
// come back as Active; one that was disabled before it finished setup would need a new setup link,
// so it is refused (ErrBadState) and the admin asks that person to register again.
func (s *Store) Enable(ctx context.Context, id int64) error {
	return s.transition(ctx, id, `Status = ?, SessionVersion = SessionVersion + 1`, []any{StatusActive},
		` AND Username IS NOT NULL AND PasswordHash IS NOT NULL`, StatusDisabled)
}

// Archive hides an account and blocks it from signing in, without touching its data. Any state but
// Archived can be archived, so "archive" is one step whatever the account was doing.
func (s *Store) Archive(ctx context.Context, id int64) error {
	return s.transition(ctx, id, `Status = ?, SessionVersion = SessionVersion + 1`, []any{StatusArchived},
		``, StatusRequested, StatusApproved, StatusActive, StatusRejected, StatusDisabled)
}

// Restore brings an Archived account back as Disabled, never straight to Active: the admin looks at
// it first and then presses Enable.
func (s *Store) Restore(ctx context.Context, id int64) error {
	// Coming back uses a place in the company again, so it needs one.
	if u, err := s.ByID(ctx, id); err == nil && u.Status == StatusArchived && u.CompanyID != 0 {
		if room, err := s.CompanyHasRoom(ctx, u.CompanyID); err != nil {
			return err
		} else if !room {
			return ErrCompanyFull
		}
	}
	return s.transition(ctx, id, `Status = ?`, []any{StatusDisabled}, ``, StatusArchived)
}

// AccountFootprint counts what removing an account would take with it, for the warning the admin
// reads first.
type AccountFootprint struct {
	Batches, Documents, Files int
}

// Footprint counts one account's batches, documents and stored PDFs.
func (s *Store) Footprint(ctx context.Context, id int64) (AccountFootprint, error) {
	var f AccountFootprint
	err := s.DB.QueryRowContext(ctx,
		`SELECT (SELECT COUNT(*) FROM McuBatch WHERE FK_SubmitterID = ?),
		        (SELECT COUNT(*) FROM McuSubmission WHERE FK_SubmitterID = ?),
		        (SELECT COUNT(*) FROM McuSubmission WHERE FK_SubmitterID = ? AND FilePath IS NOT NULL)`,
		id, id, id).Scan(&f.Batches, &f.Documents, &f.Files)
	return f, err
}

// DocumentCounts returns how many documents each account has, for the admin list.
func (s *Store) DocumentCounts(ctx context.Context) (map[int64]int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT FK_SubmitterID, COUNT(*) FROM McuSubmission GROUP BY FK_SubmitterID`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// RemoveAccount permanently removes an account and everything that hangs on it, in one transaction:
// its documents, its batches, its sign-in and email tokens, and the account row itself. The tables
// point at the account with plain foreign keys (no ON DELETE CASCADE), so the order matters and the
// children go first. The stored PDFs cannot be removed inside the transaction, so their references
// are returned and the caller removes them afterwards. What stays: the checkup records staff made
// from those documents (medical data, not the portal's), and the event log, which holds ids and
// counts but no name or email. The built-in admin cannot be removed (ErrNotFound, like everywhere
// else that hides it), and an admin cannot remove the account they are signed in with.
func (s *Store) RemoveAccount(ctx context.Context, id, adminID int64) (fileRefs []string, foot AccountFootprint, err error) {
	u, err := s.ByID(ctx, id)
	if err != nil || u.IsBuiltIn {
		return nil, foot, ErrNotFound
	}
	if id == adminID {
		return nil, foot, ErrBadState
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, foot, err
	}
	defer func() { _ = tx.Rollback() }() // a no-op once Commit succeeded

	rows, err := tx.QueryContext(ctx, `SELECT FilePath FROM McuSubmission WHERE FK_SubmitterID = ? AND FilePath IS NOT NULL`, id)
	if err != nil {
		return nil, foot, err
	}
	for rows.Next() {
		var ref sql.NullString
		if err := rows.Scan(&ref); err != nil {
			rows.Close()
			return nil, foot, err
		}
		if ref.Valid && ref.String != "" {
			fileRefs = append(fileRefs, ref.String)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, foot, err
	}
	rows.Close()

	for _, q := range []struct {
		sql  string
		into *int
	}{
		{`DELETE FROM McuSubmission WHERE FK_SubmitterID = ?`, &foot.Documents},
		{`DELETE FROM McuBatch WHERE FK_SubmitterID = ?`, &foot.Batches},
		{`DELETE FROM SubmitterToken WHERE FK_SubmitterID = ?`, nil},
		// Rows that merely name this account as the one who approved or last edited something are
		// kept, with the name cleared.
		{`UPDATE Submitter SET FK_ApprovedBySubmitterID = NULL WHERE FK_ApprovedBySubmitterID = ?`, nil},
		{`UPDATE McuPortalSetting SET FK_UpdatedBySubmitterID = NULL WHERE FK_UpdatedBySubmitterID = ?`, nil},
	} {
		res, err := tx.ExecContext(ctx, q.sql, id)
		if err != nil {
			return nil, foot, fmt.Errorf("remove account %d: %w", id, err)
		}
		if q.into != nil {
			n, _ := res.RowsAffected()
			*q.into = int(n)
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM Submitter WHERE SubmitterID = ? AND IsBuiltIn = 0`, id)
	if err != nil {
		return nil, foot, fmt.Errorf("remove account %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil, foot, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, foot, err
	}
	foot.Files = len(fileRefs)
	// Counts only, never a name or an email address (plan section 3).
	_ = s.LogEvent(ctx, Event{ActorType: ActorSubmitter, ActorID: adminID, EntityType: EntitySubmitter, EntityID: id, Action: "AccountRemoved",
		Detail: fmt.Sprintf("was %s; %d documents, %d batches, %d files", u.Status, foot.Documents, foot.Batches, foot.Files)})
	return fileRefs, foot, nil
}
