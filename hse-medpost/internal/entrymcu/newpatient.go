package entrymcu

import (
	"encoding/json"
	"errors"
	"net/http"

	"hse-medpost/internal/patient"
)

// newPatientWithCheckupBody is a new patient and their first checkup, sent as
// one request so the server can save both or neither.
type newPatientWithCheckupBody struct {
	Patient patient.NewPatient `json:"patient"`
	Checkup McuRecord          `json:"checkup"`
}

// CreateNewPatientWithCheckup saves a new patient and their first checkup in a
// single transaction. Both halves are validated before the first write, so a
// bad field in either one leaves both tables untouched. A duplicate patient
// (name + date of birth, or NIK/passport ID) answers 409 and writes nothing.
func (a *API) CreateNewPatientWithCheckup(w http.ResponseWriter, r *http.Request) {
	var body newPatientWithCheckupBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	p, err := (&patient.API{DB: a.DB}).ValidateNew(body.Patient)
	if err != nil {
		if errors.Is(err, patient.ErrDatabase) {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	m := body.Checkup
	m.Reviewer = reviewerFromLogin(r)
	m.WorkPosition = patient.NormalizeOptionalText(m.WorkPosition, patient.NormalizeWorkPosition)
	if p.PersonalID != "" {
		personalID := p.PersonalID
		m.PersonalID = &personalID
	}
	if status, msg := a.validateNewCheckup(&m); status != 0 {
		writeJSON(w, status, map[string]string{"error": msg})
		return
	}

	tx, err := a.DB.Begin()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer tx.Rollback()

	patientID, err := patient.InsertPatient(tx, p)
	if err != nil {
		if patient.IsDuplicate(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": patient.DuplicateMessage(err)})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	m.PatientID = patientID

	mcuRecordID, err := a.insertCheckupTx(tx, m)
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
	writeJSON(w, http.StatusCreated, map[string]any{
		"ok":               true,
		"patientId":        patientID,
		"mcuRecordId":      mcuRecordID,
		"resultValidUntil": m.ResultValidUntil,
	})
}
