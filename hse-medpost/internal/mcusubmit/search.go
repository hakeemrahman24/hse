package mcusubmit

import (
	"context"
	"errors"
	"strconv"
	"time"
)

// docPageSize is how many documents one page of either list shows.
const docPageSize = 25

// ErrBadFilter means a status or date that is not one the lists know.
var ErrBadFilter = errors.New("mcusubmit: unknown status or date in the filter")

// DocFilter is what a submitter or a staff member typed above a document list. Empty fields mean
// "no filter". With OwnerID set it is a submitter's own list (the id comes from the session, never
// from the request); with OwnerID zero it is the staff list, which never shows unsent work.
type DocFilter struct {
	OwnerID   int64
	Scope     *Scope // with OwnerID set: also the sent documents of the company and its subcontractors
	Account   int64  // with Scope: only documents of accounts of this company (must be inside Scope)
	Needs     bool   // own view: only documents that need the submitter to do something (see needsActionSQL)
	Status    string
	Worker    string // words of the worker's name, any order
	Company   string // words of the company text
	Submitter string // words of the submitter's name (staff list)
	From, To  string // YYYY-MM-DD, the day the document was uploaded
	Page      int    // 1-based; out-of-range values are moved to the nearest real page
}

// DocPage is one page of results with what a pager needs.
type DocPage struct {
	Rows            []Submission
	Total, PageSize int
	Page, Pages     int
}

var ownStatuses = map[string]bool{"": true, SubDraft: true, SubReview: true, SubPending: true, SubReviewed: true, SubClosed: true, SubRejected: true, SubWithdrawn: true}

// followUpResults are the results after which the submitter has something to do or read: the
// reviewer's recommendation or notes (see Submission.ResultMessage).
var followUpResults = []string{"NeedFollowUp", "IncompleteResult", "TemporaryUnfit", "Unfit"}

// needsActionSQL is "Needs action" (owner, 2026-11-05): a rejected document (upload a corrected one)
// or a Closed one with a follow-up result. Fixed text; its values are bound parameters.
const needsActionSQL = ` AND (s.Status = ? OR (s.Status = ? AND rec.ResultCriteria IN (?, ?, ?, ?)))`

const dateOnlyLayout = "2006-01-02"

// SearchDocs returns one page of documents. Every typed value goes in as a bound parameter; the
// only text added to the query is fixed in this function, so a filter cannot change the query.
func (s *Store) SearchDocs(ctx context.Context, f DocFilter) (DocPage, error) {
	known := reviewStatuses
	if f.OwnerID != 0 {
		known = ownStatuses
	}
	if !known[f.Status] {
		return DocPage{}, ErrBadFilter
	}
	from, to, err := dayBounds(f.From, f.To)
	if err != nil {
		return DocPage{}, err
	}

	where, args := ` WHERE 1 = 1`, []any{}
	if f.OwnerID != 0 {
		if f.Scope != nil {
			if f.Account != 0 && !f.Scope.Covers(f.Account) {
				return DocPage{}, ErrBadFilter // a company outside the scope cannot be asked for
			}
			clause, scopeArgs := f.Scope.clause()
			where += ` AND ` + clause
			args = append(args, scopeArgs...)
			if f.Account != 0 {
				where += ` AND s.FK_SubmitterID IN (SELECT SubmitterID FROM Submitter WHERE FK_CompanyID = ?)`
				args = append(args, f.Account)
			}
		} else {
			where += ` AND s.FK_SubmitterID = ?`
			args = append(args, f.OwnerID)
		}
	} else {
		// Staff never see unsent work (Draft and Review are not valid staff filters either); withdrawn
		// documents only when asked for by name.
		where += ` AND s.Status NOT IN (?, ?)`
		args = append(args, SubReview, SubDraft)
		if f.Status != SubWithdrawn {
			where += ` AND s.Status <> ?`
			args = append(args, SubWithdrawn)
		}
	}
	if f.Needs && f.OwnerID != 0 {
		where += needsActionSQL
		args = append(args, SubRejected, SubClosed)
		for _, r := range followUpResults {
			args = append(args, r)
		}
	}
	if f.Status != "" {
		where += ` AND s.Status = ?`
		args = append(args, f.Status)
	}
	for column, typed := range map[string]string{"s.PatName": f.Worker, "s.PatCompanyText": f.Company, "u.Name": f.Submitter} {
		for _, w := range splitWords(typed) {
			where += ` AND ` + column + ` LIKE ?`
			args = append(args, "%"+w+"%")
		}
	}
	if from != "" {
		where += ` AND s.SubmittedAt >= ?`
		args = append(args, from)
	}
	if to != "" {
		where += ` AND s.SubmittedAt <= ?`
		args = append(args, to)
	}

	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*)`+submissionFrom+where, args...).Scan(&total); err != nil {
		return DocPage{}, err
	}
	pages := (total + docPageSize - 1) / docPageSize
	if pages < 1 {
		pages = 1
	}
	page := f.Page
	if page < 1 {
		page = 1
	}
	if page > pages {
		page = pages
	}

	// The waiting queue reads oldest first, so nothing sits unanswered at the bottom; every other
	// list is newest first. The ids are tie-breakers, which also keep pages from overlapping.
	order := ` ORDER BY s.SubmissionID DESC`
	if f.OwnerID == 0 && f.Status == SubPending {
		order = ` ORDER BY b.SentAt ASC, s.SubmissionID ASC`
	} else if f.OwnerID == 0 {
		order = ` ORDER BY b.SentAt DESC, s.SubmissionID DESC`
	}
	// Limit and offset are numbers computed above, not typed text.
	rows, err := s.querySubmissions(ctx, where+order+` LIMIT `+strconv.Itoa(docPageSize)+` OFFSET `+strconv.Itoa((page-1)*docPageSize), args...)
	if err != nil {
		return DocPage{}, err
	}
	return DocPage{Rows: rows, Total: total, PageSize: docPageSize, Page: page, Pages: pages}, nil
}

// dayBounds turns two dates into the first and last stored timestamp of those days, or "" for none.
func dayBounds(fromDay, toDay string) (from, to string, err error) {
	var a, b time.Time
	if fromDay != "" {
		if a, err = time.Parse(dateOnlyLayout, fromDay); err != nil {
			return "", "", ErrBadFilter
		}
		from = fromDay + "T00:00:00"
	}
	if toDay != "" {
		if b, err = time.Parse(dateOnlyLayout, toDay); err != nil {
			return "", "", ErrBadFilter
		}
		to = toDay + "T23:59:59"
	}
	if fromDay != "" && toDay != "" && a.After(b) {
		return "", "", ErrBadFilter
	}
	return from, to, nil
}
