package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// employee is what the Medpost API returns after a successful date-of-birth match. Only these
// fields are used; anything else is discarded and never logged (plan section 7.2).
type employee struct {
	Key         string // employee ID
	Name        string
	Designation string
	Company     string
}

// nameMatch is one search hit. Key stays on the server; the browser only ever gets a random reference.
type nameMatch struct {
	Key, Name, Company string
	Duplicate          bool // another active employee has the identical name, so show the company
}

// employeeDirectory is the Medpost API (plan section 7.2). Verify returns nil, nil when the
// reference and date of birth do not match. The real client is not written yet: it needs the
// Medpost endpoints, so only the development stub in soc_dev.go implements this today.
type employeeDirectory interface {
	Search(ctx context.Context, q string, max int) ([]nameMatch, error)
	Verify(ctx context.Context, key string, dob time.Time) (*employee, error)
}

// turnstileVerifier checks the Cloudflare Turnstile token on the server.
type turnstileVerifier interface {
	Verify(ctx context.Context, token, ip string) (bool, error)
}

// socListItem is one row of "My submissions": reference, date, category and status only.
type socListItem struct {
	ID           int64
	Ref          string
	Date         time.Time
	Category     string
	Status       string
	Verification string  // "Not yet" or "Verified"
	Photos       []int64 // ids of this post's photos, for the thumbnails on the visitor's own pages
	Editable     bool    // the employee can still edit it: not verified and not closed
	CanClose     bool    // the employee can still add After photos and close it: not closed
	obsID        int64
}

// photoRecord is one stored photo (already compressed and written to the evidence folder).
type photoRecord struct {
	Kind       string // "before" or "after"
	StoredName string
	Size       int
}

// findingSink saves SOC submissions into the single observation table.
type findingSink interface {
	Save(ctx context.Context, s socSubmission, photos []photoRecord) (int64, error)
	ListByEmployee(ctx context.Context, employeeID string) ([]socListItem, error)
	// OwnPhoto returns the stored file name of a photo only if it belongs to a SOC post of this
	// employee. Anyone else gets ok = false, so a photo id alone never opens someone else's picture.
	OwnPhoto(ctx context.Context, photoID int64, employeeID string) (name string, ok bool, err error)
	// GetOwn returns the employee's own post for editing, or nil, nil when it is not theirs.
	GetOwn(ctx context.Context, id int64, employeeID string) (*socPost, error)
	// UpdateOwn saves the employee's edit; it returns errNotEditable once the post is verified or
	// closed. It returns the names of removed photos so the caller can delete the files.
	UpdateOwn(ctx context.Context, id int64, employeeID string, sub socSubmission, remove []int64, add []photoRecord) ([]string, error)
	// AddAfterOwn stores After photos on the employee's own post and optionally closes it. It
	// returns errNotEditable once the post is closed and errNeedsAfter for a close without After photos.
	// remarks, when not nil, replaces the post's remarks (an empty string clears them).
	AddAfterOwn(ctx context.Context, id int64, employeeID string, add []photoRecord, remarks *string, closeIt bool) error
}

// ---- real Cloudflare Turnstile verifier ----

type cloudflareTurnstile struct {
	secret string
	client *http.Client
}

func newCloudflareTurnstile(secret string) *cloudflareTurnstile {
	return &cloudflareTurnstile{secret: secret, client: &http.Client{Timeout: 5 * time.Second}}
}

func (c *cloudflareTurnstile) Verify(ctx context.Context, token, ip string) (bool, error) {
	if strings.TrimSpace(token) == "" {
		return false, nil
	}
	form := url.Values{"secret": {c.secret}, "response": {token}}
	if ip != "" {
		form.Set("remoteip", ip)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://challenges.cloudflare.com/turnstile/v0/siteverify", strings.NewReader(form.Encode()))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	var out struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, err
	}
	return out.Success, nil
}

// ---- observation table sink ----

// observationSink stores SOC posts in the observation tables. dir is the evidence folder: after a
// post is saved its photos are moved to their readable names there (evidence_names.go).
type observationSink struct {
	db  *sql.DB
	dir string
}

// organize names the files of one saved post; it never fails the request (see organizeQuietly).
func (s *observationSink) organize(ctx context.Context, id int64) {
	if s.dir != "" {
		organizeQuietly(ctx, s.db, s.dir, id)
	}
}

func (s *observationSink) Save(ctx context.Context, sub socSubmission, photos []photoRecord) (int64, error) {
	// One transaction: the observation and its photo rows are saved together or not at all.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("save SOC observation: %w", err)
	}
	defer tx.Rollback() // no-op after a successful Commit

	// location holds the area. A SOC post is always source 'SOC' and starts with verification
	// 'Not yet'; only HSE staff set it to 'Verified'.
	res, err := tx.ExecContext(ctx, `INSERT INTO observations_observation
		(source, observer_name, employee_id, designation, company, location, sub_area, responsible_company,
		 category, description, action_taken, recommendation, topic_id, hazard_id, topic_pick, suggestions_shown,
		 path, unsafe_reason, ppe_item, stop_work, stopped_by, immediate_danger, barricaded, good_practice,
		 control_topic_id, review_flags, status, verification, observed_at)
		VALUES ('SOC', ?, ?, ?, ?, ?, NULLIF(?,''), NULLIF(?,''), ?, ?, NULLIF(?,''), NULLIF(?,''), NULLIF(?,0), NULLIF(?,0), NULLIF(?,''), NULLIF(?,''),
		 NULLIF(?,''), NULLIF(?,''), NULLIF(?,''), ?, NULLIF(?,''), ?, ?, NULLIF(?,''), NULLIF(?,0), NULLIF(?,''), ?, 'Not yet', ?)`,
		sub.ReporterName, sub.EmployeeID, sub.Designation, sub.Company, sub.Area, sub.SubArea, sub.ResponsibleCompany,
		sub.Category, sub.Description, sub.ActionTaken, sub.Recommendation, sub.TopicID, sub.HazardID, sub.TopicPick, sub.SuggestionsShown,
		sub.Path, sub.UnsafeReason, sub.PPEItem, nullBool(sub.StopWork), sub.StoppedBy, nullBool(sub.ImmediateDanger), nullBool(sub.Barricaded), sub.GoodPractice,
		sub.ControlTopicID, sub.ReviewFlags, sub.Status, sub.FindingDate)
	if err != nil {
		return 0, fmt.Errorf("save SOC observation: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, p := range photos {
		if _, err := tx.ExecContext(ctx, `INSERT INTO observation_files (observation_id, kind, file_type, stored_name, size_bytes) VALUES (?, ?, 'photo', ?, ?)`,
			id, p.Kind, p.StoredName, p.Size); err != nil {
			return 0, fmt.Errorf("save SOC photo: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("save SOC observation: %w", err)
	}
	s.countKeywords(ctx, sub)
	s.organize(ctx, id)
	return id, nil
}

func (s *observationSink) ListByEmployee(ctx context.Context, employeeID string) ([]socListItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, observed_at, category, status, COALESCE(verification, 'Not yet') FROM observations_observation
		WHERE source = 'SOC' AND employee_id = ? ORDER BY created_at DESC LIMIT 100`, employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []socListItem
	for rows.Next() {
		var id int64
		var it socListItem
		if err := rows.Scan(&id, &it.Date, &it.Category, &it.Status, &it.Verification); err != nil {
			return nil, err
		}
		it.ID, it.Ref, it.obsID = id, socRef(id), id
		it.Editable = socOwnerCanEdit(it.Status, it.Verification)
		it.CanClose = socOwnerCanAddAfter(it.Status)
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Attach photo ids with one more query (only this employee's SOC posts).
	prows, err := s.db.QueryContext(ctx, `SELECT p.id, p.observation_id FROM observation_files p
		JOIN observations_observation o ON o.id = p.observation_id
		WHERE o.source = 'SOC' AND o.employee_id = ? AND p.file_type = 'photo' ORDER BY p.id`, employeeID)
	if err != nil {
		return nil, err
	}
	defer prows.Close()
	byObs := map[int64][]int64{}
	for prows.Next() {
		var pid, oid int64
		if err := prows.Scan(&pid, &oid); err != nil {
			return nil, err
		}
		byObs[oid] = append(byObs[oid], pid)
	}
	for i := range out {
		out[i].Photos = byObs[out[i].obsID]
	}
	return out, prows.Err()
}

func (s *observationSink) OwnPhoto(ctx context.Context, photoID int64, employeeID string) (string, bool, error) {
	var name string
	err := s.db.QueryRowContext(ctx, `SELECT p.stored_name FROM observation_files p
		JOIN observations_observation o ON o.id = p.observation_id
		WHERE p.id = ? AND p.file_type = 'photo' AND o.source = 'SOC' AND o.employee_id = ?`, photoID, employeeID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return name, true, nil
}

// nullBool is a yes/no answer for a TINYINT column: NULL when the question was not asked.
func nullBool(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}

// countKeywords adds one to suggested_count / picked_count of the keywords that helped suggest the
// topics the form showed (plan v8, change 6). It runs once, when the post is first sent (an edit
// does not count again). It is best effort: a failure is logged and never fails the report.
func (s *observationSink) countKeywords(ctx context.Context, sub socSubmission) {
	for col, ids := range map[string][]int64{"suggested_count": sub.KeywordsSuggested, "picked_count": sub.KeywordsPicked} {
		if len(ids) == 0 {
			continue
		}
		marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		args := make([]any, len(ids))
		for i, id := range ids {
			args[i] = id
		}
		// col is one of the two constant column names above, never user input.
		if _, err := s.db.ExecContext(ctx, "UPDATE ref_keyword SET "+col+" = "+col+" + 1 WHERE id IN ("+marks+")", args...); err != nil {
			log.Printf("SOC keyword counters (%s): %v", col, err)
		}
	}
}
