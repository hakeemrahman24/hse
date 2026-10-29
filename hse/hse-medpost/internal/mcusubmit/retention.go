package mcusubmit

import (
	"context"
	"errors"
	"fmt"
	"log"
)

// Retention (plan section 5): a stored PDF is deleted 2 years after upload, and an account that has
// been inactive for 2 years and holds no files is blanked. Both jobs are safe to repeat: each one
// only picks rows that still need the work.

// retentionRunLimit bounds one run, so a first run after a long outage cannot hold the database
// for minutes. The next daily run takes the rest.
const retentionRunLimit = 500

// ExpireFiles deletes every stored file past its FileExpiresAt and clears FilePath, keeping the
// row (status, dates, hash) as an audit stub. The file goes first and the column second: if the
// delete fails the path stays, so the next run tries again. The other order would forget the
// file for good and leave it on disk. It returns how many files were expired.
func (s *Store) ExpireFiles(ctx context.Context, files FileStore) (int, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT SubmissionID, FilePath FROM McuSubmission
		 WHERE FilePath IS NOT NULL AND FileExpiresAt IS NOT NULL AND FileExpiresAt <= ?
		 ORDER BY SubmissionID LIMIT ?`, s.stamp(), retentionRunLimit)
	if err != nil {
		return 0, err
	}
	type due struct {
		id  int64
		ref string
	}
	var list []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.ref); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, d)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close() // closed before the writes below: a SQLite connection serves one statement at a time

	var failures []error
	done := 0
	for _, d := range list {
		if err := files.Delete(ctx, d.ref); err != nil {
			failures = append(failures, fmt.Errorf("submission %d: %w", d.id, err))
			continue
		}
		// FilePath = ? makes a concurrent withdraw (which clears it too) count once.
		if _, err := s.DB.ExecContext(ctx,
			`UPDATE McuSubmission SET FilePath = NULL WHERE SubmissionID = ? AND FilePath = ?`, d.id, d.ref); err != nil {
			failures = append(failures, err)
			continue
		}
		_ = s.LogEvent(ctx, Event{ActorType: ActorSystem, EntityType: EntitySubmission, EntityID: d.id, Action: "FileExpired"})
		done++
	}
	return done, errors.Join(failures...)
}

// erasedName replaces the worker's name on a document whose retention period is over.
const erasedName = "(erased)"

// ErasePersonalData blanks the worker's name, date of birth and ID numbers on documents past their
// retention date whose file is already gone. The row stays as an audit stub (status, dates, hash,
// position, company), but it no longer says WHO the worker was: the clinic's own checkup record
// holds the clinical identity, so a submission stub that keeps names and ID numbers forever would
// only be an unneeded copy. Withdrawn documents lose their file at once, so they reach this step
// by age alone. It needs FilePath IS NULL, which makes the file deletion always come first.
// One set-based UPDATE; it returns how many rows were blanked.
func (s *Store) ErasePersonalData(ctx context.Context) (int, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE McuSubmission SET PatName = ?, PatDateOfBirth = '', PatNik = NULL, PatPassportNo = NULL
		 WHERE FileExpiresAt IS NOT NULL AND FileExpiresAt <= ? AND FilePath IS NULL AND PatName <> ?`,
		erasedName, s.stamp(), erasedName)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// inactiveCondition picks accounts whose last sign-in (or sign-up, if they never signed in) is
// older than the cutoff, that are not the built-in admin, not yet erased, and own no stored file.
const inactiveCondition = `IsBuiltIn = 0 AND ErasedAt IS NULL AND COALESCE(LastLoginAt, CreatedAt) <= ?
	AND NOT EXISTS (SELECT 1 FROM McuSubmission m WHERE m.FK_SubmitterID = Submitter.SubmitterID AND m.FilePath IS NOT NULL)`

// EraseInactiveAccounts blanks the personal data of accounts inactive for the retention period.
// The row stays, because submissions and events point at it: the trail then shows that an account
// existed and what it did, without who it was. The email becomes a placeholder that can never
// receive mail, which also frees the real address for a new registration. It returns how many
// accounts were erased.
func (s *Store) EraseInactiveAccounts(ctx context.Context) (int, error) {
	cutoff := stampOf(s.now().AddDate(-retentionYears, 0, 0))
	rows, err := s.DB.QueryContext(ctx,
		`SELECT SubmitterID FROM Submitter WHERE `+inactiveCondition+` ORDER BY SubmitterID LIMIT ?`, cutoff, retentionRunLimit)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	var failures []error
	done := 0
	for _, id := range ids {
		// The same condition again, so an account that signed in or got a file since the list
		// was read is left alone. SessionVersion ends any session that is somehow still open.
		res, err := s.DB.ExecContext(ctx,
			`UPDATE Submitter SET Name = '', PasswordHash = NULL, PositionText = '', Username = NULL,
			   Email = ?, Status = ?, ErasedAt = ?, SessionVersion = SessionVersion + 1
			 WHERE SubmitterID = ? AND `+inactiveCondition,
			fmt.Sprintf("erased-%d@invalid", id), StatusDisabled, s.stamp(), id, cutoff)
		if err != nil {
			failures = append(failures, fmt.Errorf("submitter %d: %w", id, err))
			continue
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM SubmitterToken WHERE FK_SubmitterID = ?`, id); err != nil {
			log.Printf("mcusubmit: erase: clear tokens of submitter %d: %v", id, err)
		}
		_ = s.LogEvent(ctx, Event{ActorType: ActorSystem, EntityType: EntitySubmitter, EntityID: id, Action: "Erased"})
		done++
	}
	return done, errors.Join(failures...)
}

// RunRetention is the daily job: expire files, blank the identity on those documents, then erase
// accounts, because an account only qualifies once its last file is gone.
func (s *Store) RunRetention(ctx context.Context, files FileStore) (expired, erased int, err error) {
	var errs []error
	if files != nil {
		var e error
		if expired, e = s.ExpireFiles(ctx, files); e != nil {
			errs = append(errs, e)
		}
	}
	var e error
	if n, e := s.ErasePersonalData(ctx); e != nil {
		errs = append(errs, e)
	} else if n > 0 {
		log.Printf("mcusubmit: retention blanked the worker identity on %d documents", n)
	}
	if erased, e = s.EraseInactiveAccounts(ctx); e != nil {
		errs = append(errs, e)
	}
	return expired, erased, errors.Join(errs...)
}
