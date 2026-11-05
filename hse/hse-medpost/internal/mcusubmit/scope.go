package mcusubmit

import (
	"context"
	"errors"
	"strings"
)

// Who sees which documents (owner, 2026-11-05):
//   - the accounts of one company see that company's documents together (at most two accounts per
//     company, so the second is also the backup if the first mailbox has trouble);
//   - a main contractor also sees the documents of its subcontractors, and their subcontractors in
//     turn, read only; nobody sees a parent, a sibling or another company;
//   - unsent work (Draft, Review) stays with the account that made it.
//
// Writing (adding to a batch, withdrawing) stays with the account that uploaded the document.

// maxAccountsPerCompany is the owner's limit. Accounts that are Requested, Approved, Active or
// Disabled use a place; Rejected, Archived and erased (blanked) ones do not, so a leaver's place can be freed by
// archiving or deleting that account (or by the person changing their own email or username).
const maxAccountsPerCompany = 2

// ErrCompanyFull means the company already has its two accounts.
var ErrCompanyFull = errors.New("mcusubmit: the company already has the most accounts it may have")

// companyDepthLimit stops a loop in bad reference data from running for ever; real contractor
// chains are three or four levels deep.
const companyDepthLimit = 10

// CompanyTree returns the company and every company below it in CompanyRelation (its
// subcontractors, theirs in turn), the company itself first. A company with no relations is just
// itself. The walk is done here, level by level, instead of with a recursive SQL query, so it runs
// the same on SQLite and MariaDB.
func (s *Store) CompanyTree(ctx context.Context, companyID int64) ([]int64, error) {
	if companyID == 0 {
		return nil, nil
	}
	out, seen, level := []int64{companyID}, map[int64]bool{companyID: true}, []int64{companyID}
	for depth := 0; depth < companyDepthLimit && len(level) > 0; depth++ {
		marks, args := make([]string, len(level)), make([]any, len(level))
		for i, id := range level {
			marks[i], args[i] = "?", id
		}
		rows, err := s.DB.QueryContext(ctx,
			`SELECT DISTINCT FK_CompanyID FROM CompanyRelation WHERE FK_ParentID IN (`+strings.Join(marks, ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
		var next []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
				next = append(next, id)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		level = next
	}
	return out, nil
}

// Scope is what one signed-in submitter may read: their own documents plus the sent documents of
// every account in CompanyIDs. It is built from the session's account, never from the request.
type Scope struct {
	SubmitterID int64
	CompanyIDs  []int64
}

// ScopeFor builds the scope of an account.
func (s *Store) ScopeFor(ctx context.Context, u *Submitter) (Scope, error) {
	ids, err := s.CompanyTree(ctx, u.CompanyID)
	return Scope{SubmitterID: u.ID, CompanyIDs: ids}, err
}

// Covers reports whether a company is inside the scope (the filter may only name such a company).
func (sc Scope) Covers(companyID int64) bool {
	for _, id := range sc.CompanyIDs {
		if id == companyID {
			return true
		}
	}
	return false
}

// clause is the SQL condition, on the table alias s, for "this document may be read": it is mine,
// or it was sent (not Draft or Review) by an account of a company in scope.
func (sc Scope) clause() (string, []any) {
	if len(sc.CompanyIDs) == 0 {
		return `s.FK_SubmitterID = ?`, []any{sc.SubmitterID}
	}
	marks, args := make([]string, len(sc.CompanyIDs)), []any{sc.SubmitterID, SubDraft, SubReview}
	for i, id := range sc.CompanyIDs {
		marks[i] = "?"
		args = append(args, id)
	}
	return `(s.FK_SubmitterID = ? OR (s.Status NOT IN (?, ?) AND s.FK_SubmitterID IN
		(SELECT SubmitterID FROM Submitter WHERE FK_CompanyID IN (` + strings.Join(marks, ",") + `))))`, args
}

// peerWhere is the condition for "a document of mine or of the other account of my company": the
// accounts that may act for each other (owner, 2026-11-05: so the backup can withdraw a document when
// the first account is away). A subcontractor's documents are not included: a main contractor only
// reads them. Two parameters, from peerArgs.
const peerWhere = `(FK_SubmitterID = ? OR FK_SubmitterID IN (SELECT SubmitterID FROM Submitter
	WHERE FK_CompanyID IS NOT NULL AND FK_CompanyID = (SELECT FK_CompanyID FROM Submitter WHERE SubmitterID = ?)))`

func peerArgs(submitterID int64) []any { return []any{submitterID, submitterID} }

// accountsInCompany counts the accounts that use one of the company's places.
func (s *Store) accountsInCompany(ctx context.Context, companyID int64) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM Submitter WHERE FK_CompanyID = ? AND IsBuiltIn = 0 AND ErasedAt IS NULL AND Status IN (?, ?, ?, ?)`,
		companyID, StatusRequested, StatusApproved, StatusActive, StatusDisabled).Scan(&n)
	return n, err
}

// CompanyHasRoom reports whether one more account fits in the company.
func (s *Store) CompanyHasRoom(ctx context.Context, companyID int64) (bool, error) {
	n, err := s.accountsInCompany(ctx, companyID)
	return n < maxAccountsPerCompany, err
}

// CompanyRef is a company the submitter may filter by.
type CompanyRef struct {
	ID   int64
	Name string
}

// ScopeCompanies lists the companies in scope with their names, the submitter's own first, for the
// filter above My documents.
func (s *Store) ScopeCompanies(ctx context.Context, sc Scope) []CompanyRef {
	var out []CompanyRef
	for _, id := range sc.CompanyIDs {
		var name string
		if err := s.DB.QueryRowContext(ctx, `SELECT Name FROM Company WHERE CompanyID = ?`, id).Scan(&name); err != nil {
			continue // a company row that vanished only drops out of the filter
		}
		out = append(out, CompanyRef{ID: id, Name: name})
	}
	return out
}

// BackupRecipients are the other accounts of the submitter's company that can receive mail: active,
// mailbox not bouncing, not erased. They get a copy of the notices (owner, 2026-11-05), so a
// problem with the first mailbox does not stop the company hearing about its documents.
func (s *Store) BackupRecipients(ctx context.Context, submitterID, companyID int64) ([]string, error) {
	if companyID == 0 {
		return nil, nil
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT Email FROM Submitter WHERE FK_CompanyID = ? AND SubmitterID <> ? AND IsBuiltIn = 0 AND Status = ?
		   AND EmailBouncedAt IS NULL AND ErasedAt IS NULL ORDER BY SubmitterID`, companyID, submitterID, StatusActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
