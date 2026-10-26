package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Observation is one row of observations_observation. Optional text columns are empty strings
// and optional ids are 0 when the column is NULL.
type Observation struct {
	ID                 int64     `json:"id"`
	Source             string    `json:"source"`
	SourceDetail       string    `json:"source_detail"`
	ObserverName       string    `json:"observer_name"`
	EmployeeID         string    `json:"employee_id"`
	Designation        string    `json:"designation"`
	Company            string    `json:"company"`
	Category           string    `json:"category"`
	Description        string    `json:"description"`
	ActionTaken        string    `json:"action_taken"`
	Recommendation     string    `json:"recommendation"`
	TopicID            int64     `json:"topic_id"`
	HazardID           int64     `json:"hazard_id"`
	Location           string    `json:"location"` // the area
	ResponsibleCompany string    `json:"responsible_company"`
	Status             string    `json:"status"`
	Verification       string    `json:"verification"`
	CreatedBy          string    `json:"created_by"`
	ObservedAt         time.Time `json:"observed_at"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`

	// What the SOC wizard captured (migration 018); empty for staff posts and older reports.
	Path            string `json:"path,omitempty"` // A unsafe action, B unsafe condition, C safe observation, D suggestion
	UnsafeReason    string `json:"unsafe_reason,omitempty"`
	PPEItem         string `json:"ppe_item,omitempty"`
	StopWork        string `json:"stop_work,omitempty"` // "Yes" / "No" / ""
	StoppedBy       string `json:"stopped_by,omitempty"`
	ImmediateDanger string `json:"immediate_danger,omitempty"`
	Barricaded      string `json:"barricaded,omitempty"`
	GoodPractice    string `json:"good_practice,omitempty"`
	ControlTopicID  int64  `json:"control_topic_id,omitempty"`
	ReviewFlags     string `json:"review_flags,omitempty"` // reasons HSE should look at the report, separated by "; "
	TopicPick       string `json:"topic_pick,omitempty"`   // suggested / list / none
	SubArea         string `json:"sub_area,omitempty"`     // the second level of the area
}

// Urgent reports whether the reporter said there is immediate danger that is not barricaded.
func (o *Observation) Urgent() bool { return strings.Contains(o.ReviewFlags, flagUrgent) }

const observationColumns = `id, COALESCE(source,''), COALESCE(source_detail,''), observer_name, COALESCE(employee_id,''),
	COALESCE(designation,''), COALESCE(company,''), category, description, COALESCE(action_taken,''),
	COALESCE(recommendation,''), COALESCE(topic_id,0), COALESCE(hazard_id,0), location,
	COALESCE(responsible_company,''), status, COALESCE(verification,''), COALESCE(created_by,''), observed_at, created_at, updated_at,
	COALESCE(path,''), COALESCE(unsafe_reason,''), COALESCE(ppe_item,''), CASE stop_work WHEN 1 THEN 'Yes' WHEN 0 THEN 'No' ELSE '' END,
	COALESCE(stopped_by,''), CASE immediate_danger WHEN 1 THEN 'Yes' WHEN 0 THEN 'No' ELSE '' END,
	CASE barricaded WHEN 1 THEN 'Yes' WHEN 0 THEN 'No' ELSE '' END, COALESCE(good_practice,''), COALESCE(control_topic_id,0),
	COALESCE(review_flags,''), COALESCE(topic_pick,''), COALESCE(sub_area,'')`

func scanObservation(row interface{ Scan(...any) error }) (*Observation, error) {
	var o Observation
	err := row.Scan(
		&o.ID, &o.Source, &o.SourceDetail, &o.ObserverName, &o.EmployeeID,
		&o.Designation, &o.Company, &o.Category, &o.Description, &o.ActionTaken,
		&o.Recommendation, &o.TopicID, &o.HazardID, &o.Location,
		&o.ResponsibleCompany, &o.Status, &o.Verification, &o.CreatedBy, &o.ObservedAt, &o.CreatedAt, &o.UpdatedAt,
		&o.Path, &o.UnsafeReason, &o.PPEItem, &o.StopWork, &o.StoppedBy, &o.ImmediateDanger, &o.Barricaded, &o.GoodPractice,
		&o.ControlTopicID, &o.ReviewFlags, &o.TopicPick, &o.SubArea,
	)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

func getObservation(ctx context.Context, db *sql.DB, id int64) (*Observation, error) {
	return scanObservation(db.QueryRowContext(ctx, "SELECT "+observationColumns+" FROM observations_observation WHERE id = ?", id))
}

// countAfterFiles is how many "after" photos and PDFs an observation has.
func countAfterFiles(ctx context.Context, db *sql.DB, id int64) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM observation_files WHERE observation_id = ? AND kind = 'after'`, id).Scan(&n)
	return n, err
}

// The two functions below are the only place staff entries are written, so the form and the JSON
// API cannot drift apart. The values were already checked by validateObservation.

func insertObservation(ctx context.Context, db *sql.DB, o *Observation) (int64, error) {
	res, err := db.ExecContext(ctx, `INSERT INTO observations_observation
		(source, source_detail, observer_name, employee_id, designation, company, category, description,
		 action_taken, recommendation, topic_id, hazard_id, location, responsible_company, status,
		 verification, created_by, observed_at, created_at, updated_at)
		VALUES (NULLIF(?,''), NULLIF(?,''), ?, NULLIF(?,''), NULLIF(?,''), NULLIF(?,''), ?, ?,
		 NULLIF(?,''), NULLIF(?,''), NULLIF(?,0), NULLIF(?,0), ?, NULLIF(?,''), ?,
		 NULLIF(?,''), NULLIF(?,''), ?, NOW(), NOW())`,
		o.Source, o.SourceDetail, o.ObserverName, o.EmployeeID, o.Designation, o.Company, o.Category, o.Description,
		o.ActionTaken, o.Recommendation, o.TopicID, o.HazardID, o.Location, o.ResponsibleCompany, o.Status,
		o.Verification, o.CreatedBy, o.ObservedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func updateObservation(ctx context.Context, db *sql.DB, o *Observation) error {
	_, err := db.ExecContext(ctx, `UPDATE observations_observation SET
		source = NULLIF(?,''), source_detail = NULLIF(?,''), observer_name = ?, employee_id = NULLIF(?,''),
		designation = NULLIF(?,''), company = NULLIF(?,''), category = ?, description = ?,
		action_taken = NULLIF(?,''), recommendation = NULLIF(?,''), topic_id = NULLIF(?,0),
		hazard_id = NULLIF(?,0), location = ?, responsible_company = NULLIF(?,''), status = ?,
		verification = NULLIF(?,''), observed_at = ?, updated_at = NOW()
		WHERE id = ?`,
		o.Source, o.SourceDetail, o.ObserverName, o.EmployeeID, o.Designation, o.Company, o.Category, o.Description,
		o.ActionTaken, o.Recommendation, o.TopicID, o.HazardID, o.Location, o.ResponsibleCompany, o.Status,
		o.Verification, o.ObservedAt, o.ID)
	return err
}

// formFromObservation fills the form values of an existing observation (for the edit page, and for
// the JSON API, which reuses the same validation).
func formFromObservation(o *Observation) obsForm {
	idText := func(n int64) string {
		if n == 0 {
			return ""
		}
		return strconv.FormatInt(n, 10)
	}
	return obsForm{
		Source: o.Source, SourceDetail: o.SourceDetail,
		ObserverName: o.ObserverName, Designation: o.Designation, Company: o.Company,
		Category: o.Category, Description: o.Description,
		TopicID: idText(o.TopicID), HazardID: idText(o.HazardID),
		Area: o.Location, ResponsibleCompany: o.ResponsibleCompany,
		Verification: o.Verification, Status: o.Status, ActionTaken: o.ActionTaken,
		ObservedAt: o.ObservedAt.Format(formDateTimeLayout),
	}
}

type observationHandlers struct {
	db          *sql.DB
	ref         *refData
	evidenceDir string
}

func (h *observationHandlers) list(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query("SELECT " + observationColumns + " FROM observations_observation ORDER BY created_at DESC")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	observations := []*Observation{}
	for rows.Next() {
		o, err := scanObservation(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		observations = append(observations, o)
	}
	writeJSON(w, http.StatusOK, observations)
}

func (h *observationHandlers) get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	o, err := getObservation(r.Context(), h.db, id)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

// formFromJSON maps a JSON body onto the staff form so it goes through the same checks.
func formFromJSON(o Observation) obsForm {
	f := formFromObservation(&o)
	if o.ObservedAt.IsZero() {
		f.ObservedAt = ""
	}
	if f.Status == "" {
		f.Status = statusOpen
	}
	return f
}

func writeInvalid(w http.ResponseWriter, errs map[string]string) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid observation", "fields": errs})
}

func (h *observationHandlers) create(w http.ResponseWriter, r *http.Request) {
	var in Observation
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	snap, err := h.ref.Get(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	o, errs := validateObservation(formFromJSON(in), obsCheck{Ref: snap})
	if errs != nil {
		writeInvalid(w, errs)
		return
	}
	id, err := insertObservation(r.Context(), h.db, o)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	created, err := getObservation(r.Context(), h.db, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *observationHandlers) update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	existing, err := getObservation(r.Context(), h.db, id)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var in Observation
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	snap, err := h.ref.Get(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	after, err := countAfterFiles(r.Context(), h.db, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if in.ActionTaken == "" {
		in.ActionTaken = existing.ActionTaken // omitted by the client: keep what is stored
	}
	o, errs := validateObservation(formFromJSON(in), obsCheck{Ref: snap, Existing: existing, AfterFiles: after})
	if errs != nil {
		writeInvalid(w, errs)
		return
	}
	if err := updateObservation(r.Context(), h.db, o); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	updated, err := getObservation(r.Context(), h.db, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *observationHandlers) delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	files := fileNames(h.db, id) // the database cascade removes the rows but not the files
	if _, err := h.db.Exec("DELETE FROM observations_observation WHERE id = ?", id); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	removePhotoFiles(h.evidenceDir, files)
	w.WriteHeader(http.StatusNoContent)
}
