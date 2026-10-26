// Package entrymcu owns the medical-checkup module: McuRecord (one row per
// checkup visit — a patient has many, via FK_PatientID) plus its two
// lookup tables, CheckUpCriteriaTable and TaskCategoryTable. Identity
// (name/DOB/nationality/company) lives in internal/patient's Patient table,
// not here — see plan §2 for the full column-by-column mapping from the
// legacy 71-column Google Sheet header this schema replaces.
package entrymcu

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"hse-medpost/internal/auth"
	"hse-medpost/internal/dates"
	"net/http"
	"strconv"
	"strings"

	"hse-medpost/internal/db"
	"hse-medpost/internal/middleware"
	"hse-medpost/internal/patient"
)

type API struct {
	DB *sql.DB
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// isUniqueViolation reports whether err is a unique/duplicate-key constraint
// failure, across both supported drivers: SQLite says "UNIQUE constraint
// failed", MariaDB/MySQL says "Duplicate entry '...' for key '...'".
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE") || strings.Contains(msg, "Duplicate entry")
}

// --- CheckUpCriteriaTable ----------------------------------------------

type CheckUpCriteria struct {
	CheckUpCriteriaID int64  `json:"checkUpCriteriaId"`
	Name              string `json:"name"`
	Status            string `json:"status"`
}

func (a *API) ListCheckUpCriteria(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.Query(`SELECT CheckUpCriteriaID, Name, Status FROM CheckUpCriteriaTable ORDER BY Name`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	out := []CheckUpCriteria{}
	for rows.Next() {
		var c CheckUpCriteria
		if err := rows.Scan(&c.CheckUpCriteriaID, &c.Name, &c.Status); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) CreateCheckUpCriteria(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	body.Name = strings.Join(strings.Fields(strings.TrimSpace(body.Name)), " ")
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	res, err := a.DB.Exec(`INSERT INTO CheckUpCriteriaTable (Name) VALUES (?)`, body.Name)
	if err != nil {
		if isUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "checkup criteria already exists"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "checkUpCriteriaId": id})
}

func (a *API) DeleteCheckUpCriteria(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	res, err := a.DB.Exec(`DELETE FROM CheckUpCriteriaTable WHERE CheckUpCriteriaID = ?`, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- TaskCategoryTable ---------------------------------------------------

type TaskCategory struct {
	TaskCategoryID int64  `json:"taskCategoryId"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	TriggerWords   string `json:"triggerWords"`
}

func (a *API) ListTaskCategories(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.Query(`SELECT TaskCategoryID, Name, Status, COALESCE(TriggerWords, '') FROM TaskCategoryTable ORDER BY Name`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	out := []TaskCategory{}
	for rows.Next() {
		var t TaskCategory
		if err := rows.Scan(&t.TaskCategoryID, &t.Name, &t.Status, &t.TriggerWords); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) CreateTaskCategory(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name         string `json:"name"`
		TriggerWords string `json:"triggerWords"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	body.Name = strings.Join(strings.Fields(strings.TrimSpace(body.Name)), " ")
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
		return
	}
	triggers := normalizeTriggerWords(body.TriggerWords)
	if triggers == "" {
		triggers = taskCategoryTriggerWords[body.Name]
	}
	res, err := a.DB.Exec(`INSERT INTO TaskCategoryTable (Name, TriggerWords) VALUES (?, ?)`, body.Name, triggers)
	if err != nil {
		if isUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "task category already exists"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "taskCategoryId": id})
}

func (a *API) DeleteTaskCategory(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	res, err := a.DB.Exec(`DELETE FROM TaskCategoryTable WHERE TaskCategoryID = ?`, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- McuRecord ---------------------------------------------------------

// McuRecord mirrors the full checkup-visit row. Nullable columns use
// pointer types (Go's database/sql Scan supports **T destinations directly,
// allocating on non-NULL and leaving nil on NULL — same pattern already
// used by internal/company). Create/Update both take the whole struct: the
// checkup entry form resends every field on save, so there's no partial-
// patch semantics to build here, unlike the smaller lookup-style tables.
type McuRecord struct {
	McuRecordID int64   `json:"mcuRecordId"`
	PatientID   int64   `json:"patientId"`
	Timestamp   string  `json:"timestamp"`
	ReviewDate  string  `json:"reviewDate"`
	CheckUpID   string  `json:"checkUpId"`
	Reviewer    *string `json:"reviewer"`

	CheckUpCriteriaID *int64 `json:"checkUpCriteriaId"`

	// Task category is multi-select — a checkup can be e.g. both "Food
	// Handler" and "Work at Height" — so unlike every other lookup on this
	// record it isn't a single FK. TaskCategoryIDs is what the client sends
	// (the checked boxes); CreateMcuRecord/UpdateMcuRecord resolve those
	// against TaskCategoryTable and persist the joined result into
	// TaskCategoryNames, which is what every read (list/report/certificate)
	// returns. TaskCategoryIDs is never itself stored.
	TaskCategoryIDs   []int64 `json:"taskCategoryIds,omitempty"`
	TaskCategoryNames *string `json:"taskCategoryNames"`
	WorkLocation      *string `json:"workLocation"`

	Age      *int64   `json:"age"`
	HeightCm *float64 `json:"heightCm"`
	WeightKg *float64 `json:"weightKg"`

	CompanyID    *int64  `json:"companyId"`
	WorkPosition *string `json:"workPosition"`
	// PersonalID mirrors Patient.PersonalID the same way CompanyID/
	// WorkPosition do: snapshotted at save time so a later edit to the
	// patient's own NIK/passport ID (or a data-entry correction on just
	// this checkup) doesn't retroactively rewrite a past record's history.
	PersonalID *string `json:"personalId"`

	DateCheckUp          *string `json:"dateCheckUp"`
	IsShortProject       bool    `json:"isShortProject"`
	ValidityPeriodMonths *int64  `json:"validityPeriodMonths"`

	BmiValue    *float64 `json:"bmiValue"`
	BmiCriteria *string  `json:"bmiCriteria"`
	BmiNotes    *string  `json:"bmiNotes"`

	Smoking *string `json:"smoking"`

	// OtherPhysicalExamNotes is the Physical exam section's catch-all free
	// text, positioned last in that section for findings that don't fit BMI,
	// smoking, or hernia/hemorrhoid specifically.
	OtherPhysicalExamNotes *string `json:"otherPhysicalExamNotes"`

	// BloodType is the checkup's recorded blood type. It's a measurement taken
	// at the visit, like height, so it lives here rather than on Patient.
	BloodType *string `json:"bloodType"`

	BpCriteria         *string `json:"bpCriteria"`
	BpNotes            *string `json:"bpNotes"`
	VisionCriteria     *string `json:"visionCriteria"`
	VisionNotes        *string `json:"visionNotes"`
	CbcCriteria        *string `json:"cbcCriteria"`
	CbcNotes           *string `json:"cbcNotes"`
	LipidCriteria      *string `json:"lipidCriteria"`
	LipidNotes         *string `json:"lipidNotes"`
	GlucoseCriteria    *string `json:"glucoseCriteria"`
	GlucoseNotes       *string `json:"glucoseNotes"`
	LiverCriteria      *string `json:"liverCriteria"`
	LiverNotes         *string `json:"liverNotes"`
	KidneyCriteria     *string `json:"kidneyCriteria"`
	KidneyNotes        *string `json:"kidneyNotes"`
	UrinalysisCriteria *string `json:"urinalysisCriteria"`
	UrinalysisNotes    *string `json:"urinalysisNotes"`
	// RadiologyResult/RadiologyNotes hold the Radiology section's Thorax
	// X-ray criteria/notes pair (legacy free-text "result" field revised into
	// the same select-criteria + notes shape as the section's other tests;
	// the column names stayed as-is to avoid a needless migration/data
	// remap since both are already free-form TEXT).
	RadiologyResult          *string `json:"radiologyResult"`
	RadiologyNotes           *string `json:"radiologyNotes"`
	EchocardiographyCriteria *string `json:"echocardiographyCriteria"`
	EchocardiographyNotes    *string `json:"echocardiographyNotes"`
	CtScanCriteria           *string `json:"ctScanCriteria"`
	CtScanNotes              *string `json:"ctScanNotes"`
	UsgAbdomenCriteria       *string `json:"usgAbdomenCriteria"`
	UsgAbdomenNotes          *string `json:"usgAbdomenNotes"`

	// AdditionalNotes is a general free-text field for the whole checkup,
	// not scoped to any one exam section — surfaced in its own "Additional
	// exam" group ("Additional exam notes") rather than inside Radiology or
	// any other specific exam section.
	AdditionalNotes *string `json:"additionalNotes"`

	EcgResult         *string `json:"ecgResult"`
	TreadmillResult   *string `json:"treadmillResult"`
	EcgTreadmillNotes *string `json:"ecgTreadmillNotes"`

	HerniaHemorrhoidCriteria *string `json:"herniaHemorrhoidCriteria"`
	HerniaHemorrhoidNotes    *string `json:"herniaHemorrhoidNotes"`
	SpirometryCriteria       *string `json:"spirometryCriteria"`
	SpirometryNotes          *string `json:"spirometryNotes"`
	AudiometryCriteria       *string `json:"audiometryCriteria"`
	AudiometryNotes          *string `json:"audiometryNotes"`
	HepBCriteria             *string `json:"hepBCriteria"`
	HepBNotes                *string `json:"hepBNotes"`
	HepACriteria             *string `json:"hepACriteria"`
	HepANotes                *string `json:"hepANotes"`
	RectalSwabCriteria       *string `json:"rectalSwabCriteria"`
	RectalSwabNotes          *string `json:"rectalSwabNotes"`
	HivCriteria              *string `json:"hivCriteria"`

	ResultCriteria   *string `json:"resultCriteria"`
	ResultValidUntil *string `json:"resultValidUntil"`
	ResultNotes      *string `json:"resultNotes"`
	Recommendation   *string `json:"recommendation"`
	CheckUpStatus    *string `json:"checkUpStatus"`

	ParentRecordID *int64 `json:"parentRecordId"`

	HealthCertificateFileName *string `json:"healthCertificateFileName"`
	HcValidityStatus          *string `json:"hcValidityStatus"`
	ReviewYear                *int64  `json:"reviewYear"`
	IsTerminal                bool    `json:"isTerminal"`
	SubmissionDate            *string `json:"submissionDate"`
	DaysToReview              *int64  `json:"daysToReview"`

	CompanyRelationID *int64 `json:"companyRelationId"`
}

const mcuRecordColumns = `
	McuRecordID, FK_PatientID, Timestamp, ReviewDate, CheckUpID, Reviewer,
	FK_CheckUpCriteriaID, TaskCategoryNames,
	Age, HeightCm, WeightKg, FK_CompanyID, WorkPosition, PersonalID,
	DateCheckUp, IsShortProject, ValidityPeriodMonths,
	BmiValue, BmiCriteria, BmiNotes, Smoking, OtherPhysicalExamNotes, BloodType,
	BpCriteria, BpNotes, VisionCriteria, VisionNotes,
	CbcCriteria, CbcNotes, LipidCriteria, LipidNotes,
	GlucoseCriteria, GlucoseNotes, LiverCriteria, LiverNotes,
	KidneyCriteria, KidneyNotes, UrinalysisCriteria, UrinalysisNotes,
	RadiologyResult, RadiologyNotes,
	EchocardiographyCriteria, EchocardiographyNotes, CtScanCriteria, CtScanNotes,
	UsgAbdomenCriteria, UsgAbdomenNotes, AdditionalNotes,
	EcgResult, TreadmillResult, EcgTreadmillNotes,
	HerniaHemorrhoidCriteria, HerniaHemorrhoidNotes, SpirometryCriteria, SpirometryNotes,
	AudiometryCriteria, AudiometryNotes, HepBCriteria, HepBNotes, HepACriteria, HepANotes,
	RectalSwabCriteria, RectalSwabNotes, HivCriteria,
	ResultCriteria, ResultValidUntil, ResultNotes, Recommendation, CheckUpStatus,
	FK_ParentRecordID, HealthCertificateFileName, HcValidityStatus, ReviewYear,
	IsTerminal, SubmissionDate, DaysToReview, FK_CompanyRelationID, WorkLocation`

func scanMcuRecord(rows *sql.Rows, m *McuRecord) error {
	return rows.Scan(
		&m.McuRecordID, &m.PatientID, &m.Timestamp, &m.ReviewDate, &m.CheckUpID, &m.Reviewer,
		&m.CheckUpCriteriaID, &m.TaskCategoryNames,
		&m.Age, &m.HeightCm, &m.WeightKg, &m.CompanyID, &m.WorkPosition, &m.PersonalID,
		&m.DateCheckUp, &m.IsShortProject, &m.ValidityPeriodMonths,
		&m.BmiValue, &m.BmiCriteria, &m.BmiNotes, &m.Smoking, &m.OtherPhysicalExamNotes, &m.BloodType,
		&m.BpCriteria, &m.BpNotes, &m.VisionCriteria, &m.VisionNotes,
		&m.CbcCriteria, &m.CbcNotes, &m.LipidCriteria, &m.LipidNotes,
		&m.GlucoseCriteria, &m.GlucoseNotes, &m.LiverCriteria, &m.LiverNotes,
		&m.KidneyCriteria, &m.KidneyNotes, &m.UrinalysisCriteria, &m.UrinalysisNotes,
		&m.RadiologyResult, &m.RadiologyNotes,
		&m.EchocardiographyCriteria, &m.EchocardiographyNotes, &m.CtScanCriteria, &m.CtScanNotes,
		&m.UsgAbdomenCriteria, &m.UsgAbdomenNotes, &m.AdditionalNotes,
		&m.EcgResult, &m.TreadmillResult, &m.EcgTreadmillNotes,
		&m.HerniaHemorrhoidCriteria, &m.HerniaHemorrhoidNotes, &m.SpirometryCriteria, &m.SpirometryNotes,
		&m.AudiometryCriteria, &m.AudiometryNotes, &m.HepBCriteria, &m.HepBNotes, &m.HepACriteria, &m.HepANotes,
		&m.RectalSwabCriteria, &m.RectalSwabNotes, &m.HivCriteria,
		&m.ResultCriteria, &m.ResultValidUntil, &m.ResultNotes, &m.Recommendation, &m.CheckUpStatus,
		&m.ParentRecordID, &m.HealthCertificateFileName, &m.HcValidityStatus, &m.ReviewYear,
		&m.IsTerminal, &m.SubmissionDate, &m.DaysToReview, &m.CompanyRelationID, &m.WorkLocation,
	)
}

// ListMcuRecords supports an optional ?patientId= filter, since the
// checkup-history view for one patient (potentially Pre Employment, Annual,
// Follow Up, Fit for Task, ... all as separate rows) is the common read
// path — every McuRecord shares one FK_PatientID per visit, not a 1:1. The
// filter also matches on Name+DOB (same widening as ListParentCandidates,
// same reason: CreatePatient never dedupes the way findOrCreatePatientID
// does for imports, so the same real person can end up under two Patient
// rows) — otherwise this endpoint's callers (the visible History table and
// suggestNextCriteria's "what was the last checkup" logic in mcu.js) would
// silently miss records sitting under a duplicate row, while the adjacent
// parent-record picker on the same form already finds them. A subquery
// (not a JOIN) keeps this ambiguity-free: Patient and McuRecord both have a
// WorkPosition/FK_CompanyID column, so joining them into one FROM would
// make mcuRecordColumns' unqualified names ambiguous.
func (a *API) ListMcuRecords(w http.ResponseWriter, r *http.Request) {
	query := `SELECT ` + mcuRecordColumns + ` FROM McuRecord`
	args := []any{}
	if pidStr := r.URL.Query().Get("patientId"); pidStr != "" {
		pid, err := strconv.ParseInt(pidStr, 10, 64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad patientId"})
			return
		}
		query += ` WHERE FK_PatientID = ? OR FK_PatientID IN (
			SELECT PatientID FROM Patient
			WHERE DateOfBirth IS NOT NULL
			  AND DateOfBirth = (SELECT DateOfBirth FROM Patient WHERE PatientID = ?)
			  AND LOWER(TRIM(Name)) = (SELECT LOWER(TRIM(Name)) FROM Patient WHERE PatientID = ?)
		)`
		args = append(args, pid, pid, pid)
	}
	query += ` ORDER BY ReviewDate DESC`

	rows, err := a.DB.Query(query, args...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	out := []McuRecord{}
	for rows.Next() {
		var m McuRecord
		if err := scanMcuRecord(rows, &m); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		// Same computed-not-raw treatment report.go's two handlers already
		// give HcValidityStatus (see displayHcValidityStatus, validity.go) —
		// without this, this endpoint would leak the raw stored column
		// (usually empty/"Archived / Exit"/"Renewed by X" only) instead of
		// the live "ReEvaluate / Overdue" etc. ladder the patient-history
		// table's certificate-link gating (mcu.js) now depends on.
		computed := displayHcValidityStatus(m.HcValidityStatus, m.CheckUpStatus, m.ResultValidUntil, m.ResultCriteria, dates.ClinicNow())
		m.HcValidityStatus = &computed
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, out)
}

// adminAliasReviewerName is the reviewer recorded when the built-in admin
// account signs in (its staff name is builtinAdminStaffName). Every other
// login records its own staff name.
const (
	builtinAdminStaffName  = "Administrator"
	adminAliasReviewerName = "dr. Arif Rahman Hakim"
)

// reviewerFromLogin stores the signed-in reviewer name. See ReviewerNameForSession.
func reviewerFromLogin(r *http.Request) *string {
	if sess := middleware.Session(r); sess != nil {
		name := ReviewerNameForSession(sess)
		return trimReviewer(&name)
	}
	return nil
}

// ReviewerNameForSession is the reviewer name for a signed-in session: the
// built-in admin username uses the alias, everyone else uses their staff name.
func ReviewerNameForSession(sess *auth.Session) string {
	if sess == nil {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(sess.Name), builtinAdminStaffName) {
		return adminAliasReviewerName
	}
	return strings.TrimSpace(sess.Name)
}

func trimReviewer(s *string) *string {
	name := strings.TrimSpace(derefOrEmpty(s))
	if name == "" {
		return nil
	}
	return &name
}

func derefOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// resolveLookupName reads a lookup table's Name column by id, returning ""
// for a nil/unresolvable id — used to feed calculateValidityDate the plain
// criteria/task-category names it (like the legacy sheet) reasons about,
// since McuRecord itself only stores the FK ids.
func (a *API) resolveLookupName(table, idColumn string, id *int64) string {
	if id == nil {
		return ""
	}
	var name string
	if err := a.DB.QueryRow(`SELECT Name FROM `+table+` WHERE `+idColumn+` = ?`, *id).Scan(&name); err != nil {
		return ""
	}
	return name
}

// resolveTaskCategoryNames turns the client's checked taskCategoryIds into
// the "; "-joined string TaskCategoryNames actually stores — resolved
// server-side (never trusted as raw text from the client) so the stored
// names always match TaskCategoryTable's canonical spelling. Unresolvable
// ids are silently dropped (same "best effort" behavior resolveLookupName
// already has for a single id); an empty/nil selection stores nil, not "".
func (a *API) resolveTaskCategoryNames(ids []int64) *string {
	if len(ids) == 0 {
		return nil
	}
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		id := id
		if name := a.resolveLookupName("TaskCategoryTable", "TaskCategoryID", &id); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	joined := strings.Join(names, "; ")
	return &joined
}

// validateRequiredCheckupFields mirrors the checkup form's required fields:
// purpose, work location, company and work position at this visit, and result.
// Date of checkup is checked separately by validateCheckupDate.
func validateRequiredCheckupFields(m McuRecord) error {
	switch {
	case m.CheckUpCriteriaID == nil || *m.CheckUpCriteriaID == 0:
		return fmt.Errorf("checkup purpose is required")
	case strings.TrimSpace(derefOrEmpty(m.WorkLocation)) == "":
		return fmt.Errorf("work location is required")
	case m.CompanyID == nil || *m.CompanyID == 0:
		return fmt.Errorf("company is required")
	case strings.TrimSpace(derefOrEmpty(m.WorkPosition)) == "":
		return fmt.Errorf("work position is required")
	case strings.TrimSpace(derefOrEmpty(m.ResultCriteria)) == "":
		return fmt.Errorf("result is required")
	default:
		return nil
	}
}

// validateResultDependentFields mirrors mcu.js's updateResultDependentFields.
// Fit stores neither field. Fit with note, Temporarily unfit, and Unfit need
// result notes and store no follow-up. Need follow up and Incomplete need the
// follow-up and store no result notes. The returned pointers are what should
// be saved: a locked field comes back nil.
func validateResultDependentFields(resultCriteria string, resultNotes, recommendation *string) (*string, *string, error) {
	switch resultCriteria {
	case "Fit":
		return nil, nil, nil
	case "FitWithNote", "Unfit", "TemporaryUnfit":
		if strings.TrimSpace(derefOrEmpty(resultNotes)) == "" {
			return resultNotes, recommendation, fmt.Errorf("result notes are required when result is %s", resultCriteria)
		}
		return resultNotes, nil, nil
	case "NeedFollowUp", "IncompleteResult":
		if strings.TrimSpace(derefOrEmpty(recommendation)) == "" {
			return resultNotes, recommendation, fmt.Errorf("recommendation is required when result is %s", resultCriteria)
		}
		return nil, recommendation, nil
	default:
		return resultNotes, recommendation, nil
	}
}

// validateNewCheckup checks a checkup that is about to be created and fills in
// the fields the server owns (result validity, status, terminal flag). It
// writes nothing. It returns an HTTP status and message on failure, or 0 and
// "" on success. PatientID and PersonalID are the caller's job: a new patient
// doesn't have an ID yet, so the caller sets PersonalID itself.
func (a *API) validateNewCheckup(m *McuRecord) (int, string) {
	m.Timestamp = strings.TrimSpace(m.Timestamp)
	m.ReviewDate = strings.TrimSpace(m.ReviewDate)
	m.CheckUpID = strings.TrimSpace(m.CheckUpID)
	if m.Timestamp == "" || m.ReviewDate == "" || m.CheckUpID == "" {
		return http.StatusBadRequest, "timestamp, reviewDate, and checkUpId are required"
	}
	if err := validateRequiredCheckupFields(*m); err != nil {
		return http.StatusBadRequest, err.Error()
	}
	notes, recommendation, err := validateResultDependentFields(derefOrEmpty(m.ResultCriteria), m.ResultNotes, m.Recommendation)
	if err != nil {
		return http.StatusBadRequest, err.Error()
	}
	m.ResultNotes, m.Recommendation = notes, recommendation
	if err := validateCheckupDate(derefOrEmpty(m.DateCheckUp), m.IsShortProject); err != nil {
		return http.StatusBadRequest, err.Error()
	}

	// SubmissionDate is required on new checkups. Edits of older records
	// aren't checked, so they still save.
	if derefOrEmpty(m.SubmissionDate) == "" {
		return http.StatusBadRequest, "submission date is required"
	}
	if err := validateSubmissionDate(derefOrEmpty(m.SubmissionDate)); err != nil {
		return http.StatusBadRequest, err.Error()
	}

	// HealthCertificateFileName is server-managed once a certificate PDF
	// actually exists (see certificate.go's DownloadCertificate) — a brand
	// new record can't have one yet, so client input here is ignored.
	m.HealthCertificateFileName = nil

	// ResultValidUntil is always computed server-side (never trusted from
	// the client) — same source of truth the legacy sheet's
	// calculateValidityDate provided, just moved off the client-side form
	// preview and onto the one place that can't be tampered with.
	resultCriteria := ""
	if m.ResultCriteria != nil {
		resultCriteria = *m.ResultCriteria
	}
	m.TaskCategoryNames = a.resolveTaskCategoryNames(m.TaskCategoryIDs)
	m.ResultValidUntil = a.calculateValidityDate(validityInput{
		resultCriteria:       resultCriteria,
		criteriaName:         a.resolveLookupName("CheckUpCriteriaTable", "CheckUpCriteriaID", m.CheckUpCriteriaID),
		taskCategoryName:     derefOrEmpty(m.TaskCategoryNames),
		validityPeriodMonths: m.ValidityPeriodMonths,
		dateCheckUp:          derefOrEmpty(m.DateCheckUp),
		parentRecordID:       m.ParentRecordID,
		patientID:            m.PatientID,
	})

	// CheckUpStatus and IsTerminal are likewise always computed server-side
	// (never trusted from the client) — see checkUpStatusFor. Archived/Exit
	// is a separate axis (HcValidityStatus, legacy sheet column BM) and is
	// passed through from the client as-is, untouched here.
	m.IsTerminal = isTerminalResult(resultCriteria)
	status := checkUpStatusFor(m.IsTerminal)
	m.CheckUpStatus = &status

	return 0, ""
}

// insertCheckupTx writes an already-validated checkup inside the caller's
// transaction, and returns its new ID. The caller commits or rolls back, so a
// checkup can be saved together with its patient or not at all.
func (a *API) insertCheckupTx(tx *sql.Tx, m McuRecord) (int64, error) {
	res, err := tx.Exec(`INSERT INTO McuRecord (
		FK_PatientID, Timestamp, ReviewDate, CheckUpID, Reviewer,
		FK_CheckUpCriteriaID, TaskCategoryNames,
		Age, HeightCm, WeightKg, FK_CompanyID, WorkPosition, PersonalID,
		DateCheckUp, IsShortProject, ValidityPeriodMonths,
		BmiValue, BmiCriteria, BmiNotes, Smoking, OtherPhysicalExamNotes, BloodType,
		BpCriteria, BpNotes, VisionCriteria, VisionNotes,
		CbcCriteria, CbcNotes, LipidCriteria, LipidNotes,
		GlucoseCriteria, GlucoseNotes, LiverCriteria, LiverNotes,
		KidneyCriteria, KidneyNotes, UrinalysisCriteria, UrinalysisNotes,
		RadiologyResult, RadiologyNotes,
		EchocardiographyCriteria, EchocardiographyNotes, CtScanCriteria, CtScanNotes,
		UsgAbdomenCriteria, UsgAbdomenNotes, AdditionalNotes,
		EcgResult, TreadmillResult, EcgTreadmillNotes,
		HerniaHemorrhoidCriteria, HerniaHemorrhoidNotes, SpirometryCriteria, SpirometryNotes,
		AudiometryCriteria, AudiometryNotes, HepBCriteria, HepBNotes, HepACriteria, HepANotes,
		RectalSwabCriteria, RectalSwabNotes, HivCriteria,
		ResultCriteria, ResultValidUntil, ResultNotes, Recommendation, CheckUpStatus,
		FK_ParentRecordID, HealthCertificateFileName, HcValidityStatus, ReviewYear,
		IsTerminal, SubmissionDate, DaysToReview, FK_CompanyRelationID, WorkLocation
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.PatientID, m.Timestamp, m.ReviewDate, m.CheckUpID, m.Reviewer,
		m.CheckUpCriteriaID, m.TaskCategoryNames,
		m.Age, m.HeightCm, m.WeightKg, m.CompanyID, m.WorkPosition, m.PersonalID,
		m.DateCheckUp, m.IsShortProject, m.ValidityPeriodMonths,
		m.BmiValue, m.BmiCriteria, m.BmiNotes, m.Smoking, m.OtherPhysicalExamNotes, m.BloodType,
		m.BpCriteria, m.BpNotes, m.VisionCriteria, m.VisionNotes,
		m.CbcCriteria, m.CbcNotes, m.LipidCriteria, m.LipidNotes,
		m.GlucoseCriteria, m.GlucoseNotes, m.LiverCriteria, m.LiverNotes,
		m.KidneyCriteria, m.KidneyNotes, m.UrinalysisCriteria, m.UrinalysisNotes,
		m.RadiologyResult, m.RadiologyNotes,
		m.EchocardiographyCriteria, m.EchocardiographyNotes, m.CtScanCriteria, m.CtScanNotes,
		m.UsgAbdomenCriteria, m.UsgAbdomenNotes, m.AdditionalNotes,
		m.EcgResult, m.TreadmillResult, m.EcgTreadmillNotes,
		m.HerniaHemorrhoidCriteria, m.HerniaHemorrhoidNotes, m.SpirometryCriteria, m.SpirometryNotes,
		m.AudiometryCriteria, m.AudiometryNotes, m.HepBCriteria, m.HepBNotes, m.HepACriteria, m.HepANotes,
		m.RectalSwabCriteria, m.RectalSwabNotes, m.HivCriteria,
		m.ResultCriteria, m.ResultValidUntil, m.ResultNotes, m.Recommendation, m.CheckUpStatus,
		m.ParentRecordID, m.HealthCertificateFileName, m.HcValidityStatus, m.ReviewYear,
		m.IsTerminal, m.SubmissionDate, m.DaysToReview, m.CompanyRelationID, m.WorkLocation,
	)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()

	if m.ParentRecordID != nil {
		if err := closeParentRecord(tx, *m.ParentRecordID, m.CheckUpID, m.HealthCertificateFileName, id); err != nil {
			return 0, err
		}
	}

	// Patient.FK_CompanyID is derived from the latest visit, so it moves with
	// every visit save. It runs in the caller's transaction, so a new patient's
	// row and their first visit are rolled back together.
	if err := db.SyncPatientFromLatestVisit(tx, m.PatientID); err != nil {
		return 0, err
	}

	return id, nil
}

func (a *API) CreateMcuRecord(w http.ResponseWriter, r *http.Request) {
	var m McuRecord
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	m.Reviewer = reviewerFromLogin(r)
	m.WorkPosition = patient.NormalizeOptionalText(m.WorkPosition, patient.NormalizeWorkPosition)
	if m.PatientID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "patientId required"})
		return
	}
	if status, msg := a.validateNewCheckup(&m); status != 0 {
		writeJSON(w, status, map[string]string{"error": msg})
		return
	}

	// Duplicate-submission guard: Name+DOB (findOrCreatePatientID, import.go)
	// already dedupes at the *patient* level, but two reviewers opening "New
	// checkup" for the same patient around the same time each get their own
	// fresh MedC<timestamp> CheckUpID (see mcu.js openCheckupForm) and could
	// otherwise both save — Name+DOB alone doesn't catch that, since it's a
	// different patient already resolved by then. A second McuRecord for the
	// same patient on the same ReviewDate is always that race, never a
	// legitimate second checkup, so it's rejected outright rather than
	// silently allowed to create a duplicate history entry.
	var dupCount int
	if err := a.DB.QueryRow(
		`SELECT COUNT(*) FROM McuRecord WHERE FK_PatientID = ? AND ReviewDate = ?`,
		m.PatientID, m.ReviewDate,
	).Scan(&dupCount); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if dupCount > 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "a checkup for this patient on this review date already exists — check the patient's history before creating another"})
		return
	}

	// PersonalID is a snapshot of Patient.PersonalID at save time (see
	// EnsureMcuRecordPersonalIDColumn) — resolved server-side, never trusted
	// from the client.
	if err := a.DB.QueryRow(`SELECT PersonalID FROM Patient WHERE PatientID = ?`, m.PatientID).Scan(&m.PersonalID); err != nil && err != sql.ErrNoRows {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	tx, err := a.DB.Begin()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer tx.Rollback()

	id, err := a.insertCheckupTx(tx, m)
	if err != nil {
		if isUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "checkup id already exists"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "mcuRecordId": id, "resultValidUntil": m.ResultValidUntil})
}

func (a *API) UpdateMcuRecord(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var m McuRecord
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	m.Reviewer = trimReviewer(m.Reviewer)
	m.WorkPosition = patient.NormalizeOptionalText(m.WorkPosition, patient.NormalizeWorkPosition)
	if err := validateRequiredCheckupFields(m); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := validateCheckupDate(derefOrEmpty(m.DateCheckUp), m.IsShortProject); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	notes, recommendation, err := validateResultDependentFields(derefOrEmpty(m.ResultCriteria), m.ResultNotes, m.Recommendation)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	m.ResultNotes, m.Recommendation = notes, recommendation

	// FK_PatientID is never part of the UPDATE SET below (a checkup's
	// patient doesn't change after the fact), so it's read back from the
	// existing row rather than trusted from the request body — needed here
	// only as an input to the Biannual/Update fallback validity lookup.
	var existingPatientID int64
	if err := a.DB.QueryRow(`SELECT FK_PatientID FROM McuRecord WHERE McuRecordID = ?`, id).Scan(&existingPatientID); err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	// PersonalID is refreshed from Patient.PersonalID on every save, same
	// as CreateMcuRecord — never trusted from the client (no field for it
	// on the checkup form), so it isn't in this record's UPDATE SET list by
	// omission, it's actively re-resolved here.
	if err := a.DB.QueryRow(`SELECT PersonalID FROM Patient WHERE PatientID = ?`, existingPatientID).Scan(&m.PersonalID); err != nil && err != sql.ErrNoRows {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	resultCriteria := ""
	if m.ResultCriteria != nil {
		resultCriteria = *m.ResultCriteria
	}
	m.TaskCategoryNames = a.resolveTaskCategoryNames(m.TaskCategoryIDs)
	m.ResultValidUntil = a.calculateValidityDate(validityInput{
		resultCriteria:       resultCriteria,
		criteriaName:         a.resolveLookupName("CheckUpCriteriaTable", "CheckUpCriteriaID", m.CheckUpCriteriaID),
		taskCategoryName:     derefOrEmpty(m.TaskCategoryNames),
		validityPeriodMonths: m.ValidityPeriodMonths,
		dateCheckUp:          derefOrEmpty(m.DateCheckUp),
		parentRecordID:       m.ParentRecordID,
		patientID:            existingPatientID,
	})

	// CheckUpStatus and IsTerminal are likewise always computed server-side —
	// see checkUpStatusFor. HcValidityStatus (legacy column BM) is excluded
	// from this UPDATE entirely: it's system-derived (displayHcValidityStatus)
	// or set only by closeParentRecord's own statement, never by the client,
	// so leaving it out of the SET clause here prevents an edit from wiping
	// an existing "Archived / Exit" / "Renewed by X" stamp.
	m.IsTerminal = isTerminalResult(resultCriteria)
	status := checkUpStatusFor(m.IsTerminal)
	m.CheckUpStatus = &status

	_, err = a.DB.Exec(`UPDATE McuRecord SET
		Reviewer=?, FK_CheckUpCriteriaID=?, TaskCategoryNames=?,
		Age=?, HeightCm=?, WeightKg=?, FK_CompanyID=?, WorkPosition=?, PersonalID=?,
		DateCheckUp=?, IsShortProject=?, ValidityPeriodMonths=?,
		BmiValue=?, BmiCriteria=?, BmiNotes=?, Smoking=?, OtherPhysicalExamNotes=?, BloodType=?,
		BpCriteria=?, BpNotes=?, VisionCriteria=?, VisionNotes=?,
		CbcCriteria=?, CbcNotes=?, LipidCriteria=?, LipidNotes=?,
		GlucoseCriteria=?, GlucoseNotes=?, LiverCriteria=?, LiverNotes=?,
		KidneyCriteria=?, KidneyNotes=?, UrinalysisCriteria=?, UrinalysisNotes=?,
		RadiologyResult=?, RadiologyNotes=?,
		EchocardiographyCriteria=?, EchocardiographyNotes=?, CtScanCriteria=?, CtScanNotes=?,
		UsgAbdomenCriteria=?, UsgAbdomenNotes=?, AdditionalNotes=?,
		EcgResult=?, TreadmillResult=?, EcgTreadmillNotes=?,
		HerniaHemorrhoidCriteria=?, HerniaHemorrhoidNotes=?, SpirometryCriteria=?, SpirometryNotes=?,
		AudiometryCriteria=?, AudiometryNotes=?, HepBCriteria=?, HepBNotes=?, HepACriteria=?, HepANotes=?,
		RectalSwabCriteria=?, RectalSwabNotes=?, HivCriteria=?,
		ResultCriteria=?, ResultValidUntil=?, ResultNotes=?, Recommendation=?, CheckUpStatus=?,
		FK_ParentRecordID=?, HealthCertificateFileName=NULL, ReviewYear=?,
		IsTerminal=?, SubmissionDate=?, DaysToReview=?, FK_CompanyRelationID=?, WorkLocation=?
	WHERE McuRecordID=?`,
		m.Reviewer, m.CheckUpCriteriaID, m.TaskCategoryNames,
		m.Age, m.HeightCm, m.WeightKg, m.CompanyID, m.WorkPosition, m.PersonalID,
		m.DateCheckUp, m.IsShortProject, m.ValidityPeriodMonths,
		m.BmiValue, m.BmiCriteria, m.BmiNotes, m.Smoking, m.OtherPhysicalExamNotes, m.BloodType,
		m.BpCriteria, m.BpNotes, m.VisionCriteria, m.VisionNotes,
		m.CbcCriteria, m.CbcNotes, m.LipidCriteria, m.LipidNotes,
		m.GlucoseCriteria, m.GlucoseNotes, m.LiverCriteria, m.LiverNotes,
		m.KidneyCriteria, m.KidneyNotes, m.UrinalysisCriteria, m.UrinalysisNotes,
		m.RadiologyResult, m.RadiologyNotes,
		m.EchocardiographyCriteria, m.EchocardiographyNotes, m.CtScanCriteria, m.CtScanNotes,
		m.UsgAbdomenCriteria, m.UsgAbdomenNotes, m.AdditionalNotes,
		m.EcgResult, m.TreadmillResult, m.EcgTreadmillNotes,
		m.HerniaHemorrhoidCriteria, m.HerniaHemorrhoidNotes, m.SpirometryCriteria, m.SpirometryNotes,
		m.AudiometryCriteria, m.AudiometryNotes, m.HepBCriteria, m.HepBNotes, m.HepACriteria, m.HepANotes,
		m.RectalSwabCriteria, m.RectalSwabNotes, m.HivCriteria,
		m.ResultCriteria, m.ResultValidUntil, m.ResultNotes, m.Recommendation, m.CheckUpStatus,
		m.ParentRecordID, m.ReviewYear,
		m.IsTerminal, m.SubmissionDate, m.DaysToReview, m.CompanyRelationID, m.WorkLocation,
		id,
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if err := db.SyncPatientFromLatestVisit(a.DB, existingPatientID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "resultValidUntil": m.ResultValidUntil})
}

// ParentCandidate is the trimmed shape the parent-record picker dropdown
// needs — Code.js's getOpenIDsByName, ported: a patient's still-open
// (CheckUpStatus != 'Close'), non-Follow-Up records, most recent first, for
// linking a new renewal/follow-up to the record it supersedes.
type ParentCandidate struct {
	McuRecordID int64  `json:"mcuRecordId"`
	CheckUpID   string `json:"checkUpId"`
	ReviewDate  string `json:"reviewDate"`
	Criteria    string `json:"criteria"`
}

// ListParentCandidates matches on FK_PatientID *or* the same Name+DOB as
// that patient — CreatePatient (internal/patient/patient.go, the "+ New
// patient" button) never dedupes the way findOrCreatePatientID does for
// imports, so the same real person can end up under two different Patient
// rows (one from the UI, one from a later import, say). Scoping strictly by
// FK_PatientID would then miss a genuine renewal candidate sitting under
// the other row for the same person. DOB must be present and match exactly
// to widen the search — a blank DOB never matches another blank DOB, so
// patients missing a DOB just fall back to today's FK_PatientID-only
// behavior instead of pulling in unrelated same-name people.
func (a *API) ListParentCandidates(w http.ResponseWriter, r *http.Request) {
	pidStr := r.URL.Query().Get("patientId")
	pid, err := strconv.ParseInt(pidStr, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "patientId required"})
		return
	}

	rows, err := a.DB.Query(`
		SELECT m.McuRecordID, m.CheckUpID, m.ReviewDate, COALESCE(c.Name,'')
		FROM McuRecord m
		JOIN Patient mp ON mp.PatientID = m.FK_PatientID
		LEFT JOIN CheckUpCriteriaTable c ON c.CheckUpCriteriaID = m.FK_CheckUpCriteriaID
		WHERE (
		    m.FK_PatientID = ?
		    OR (
		      mp.DateOfBirth IS NOT NULL
		      AND mp.DateOfBirth = (SELECT DateOfBirth FROM Patient WHERE PatientID = ?)
		      AND LOWER(TRIM(mp.Name)) = (SELECT LOWER(TRIM(Name)) FROM Patient WHERE PatientID = ?)
		    )
		  )
		  AND (
		    m.CheckUpStatus IS NULL OR m.CheckUpStatus != 'Close'
		    OR m.ResultCriteria IN ('TemporaryUnfit', 'NeedFollowUp', 'IncompleteResult')
		  )
		  AND (m.HcValidityStatus IS NULL OR m.HcValidityStatus NOT LIKE 'Renewed by %')
		  AND COALESCE(c.Name, '') != 'Follow Up'
		ORDER BY m.ReviewDate DESC`, pid, pid, pid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	out := []ParentCandidate{}
	for rows.Next() {
		var c ParentCandidate
		if err := rows.Scan(&c.McuRecordID, &c.CheckUpID, &c.ReviewDate, &c.Criteria); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// SearchResult is one row of the general ID/name search box — Code.js's
// searchRecords, deduped to the latest record per patient (matches by exact
// CheckUpID bypass the dedup, same as the legacy version).
type SearchResult struct {
	McuRecordID      int64   `json:"mcuRecordId"`
	CheckUpID        string  `json:"checkUpId"`
	PatientName      string  `json:"patientName"`
	Criteria         string  `json:"criteria"`
	CompanyName      string  `json:"companyName"`
	ReviewDate       string  `json:"reviewDate"`
	ResultCriteria   *string `json:"resultCriteria"`
	ResultValidUntil *string `json:"resultValidUntil"`
	CheckUpStatus    *string `json:"checkUpStatus"`
}

func (a *API) SearchMcuRecords(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, []SearchResult{})
		return
	}

	rows, err := a.DB.Query(`
		SELECT m.McuRecordID, m.CheckUpID, p.Name, COALESCE(c.Name,''), COALESCE(co.Name,''),
		       m.ReviewDate, m.ResultCriteria, m.ResultValidUntil, m.CheckUpStatus
		FROM McuRecord m
		JOIN Patient p ON p.PatientID = m.FK_PatientID
		LEFT JOIN CheckUpCriteriaTable c ON c.CheckUpCriteriaID = m.FK_CheckUpCriteriaID
		LEFT JOIN Company co ON co.CompanyID = m.FK_CompanyID
		WHERE m.CheckUpID LIKE ? OR p.Name LIKE ?
		ORDER BY m.ReviewDate DESC`,
		"%"+q+"%", "%"+q+"%")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	// Dedup to the latest row per patient, except an exact CheckUpID match
	// (case-insensitive) which always stands on its own — same rule the
	// legacy searchRecords used.
	seenPatient := map[string]bool{}
	out := []SearchResult{}
	for rows.Next() {
		var sr SearchResult
		if err := rows.Scan(&sr.McuRecordID, &sr.CheckUpID, &sr.PatientName, &sr.Criteria, &sr.CompanyName,
			&sr.ReviewDate, &sr.ResultCriteria, &sr.ResultValidUntil, &sr.CheckUpStatus); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		isExactID := strings.EqualFold(sr.CheckUpID, q)
		key := sr.PatientName
		if !isExactID {
			if seenPatient[key] {
				continue
			}
			seenPatient[key] = true
		}
		out = append(out, sr)
		if len(out) >= 10 {
			break
		}
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) DeleteMcuRecord(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var patientID int64
	if err := a.DB.QueryRow(`SELECT FK_PatientID FROM McuRecord WHERE McuRecordID = ?`, id).Scan(&patientID); err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	res, err := a.DB.Exec(`DELETE FROM McuRecord WHERE McuRecordID = ?`, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err := db.SyncPatientFromLatestVisit(a.DB, patientID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
