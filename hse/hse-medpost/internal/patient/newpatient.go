package patient

import (
	"database/sql"
	"errors"
	"strings"
)

// ErrDatabase marks a failure that isn't the caller's fault, so HTTP handlers
// can answer 500 instead of 400.
var ErrDatabase = errors.New("db error")

// NewPatient is the patient half of a new-patient save. The JSON names match
// what the patient form sends.
type NewPatient struct {
	Name          string `json:"name"`
	Gender        string `json:"gender"`
	DateOfBirth   string `json:"dateOfBirth"`
	NationalityID *int64 `json:"nationalityId"`
	EmployeeID    string `json:"employeeId"`
	CompanyID     *int64 `json:"companyId"`
	WorkPosition  string `json:"workPosition"`
	PersonalID    string `json:"personalId"`
}

// ValidateNew normalizes and checks a new patient, and returns the cleaned copy
// that should be inserted. It writes nothing, so it's safe to call before the
// rest of a save has been validated.
func (a *API) ValidateNew(in NewPatient) (NewPatient, error) {
	in.Name = NormalizePersonName(in.Name)
	in.Gender = strings.TrimSpace(in.Gender)
	in.DateOfBirth = strings.TrimSpace(in.DateOfBirth)
	in.WorkPosition = NormalizeWorkPosition(in.WorkPosition)
	// Name/DoB/Nationality/Gender are mandatory — the
	// client already enforces this via required attributes, but that's
	// bypassable (devtools, a direct API call), so it's re-checked here.
	// Company and work position are optional here: Patient.FK_CompanyID and
	// Patient.WorkPosition are derived from the latest visit
	// (db.SyncPatientFromLatestVisit), so a new patient's first visit sets them
	// and overwrites anything sent now.
	switch {
	case in.Name == "":
		return in, errors.New("patient name required")
	case in.Gender == "":
		return in, errors.New("gender required")
	case in.DateOfBirth == "":
		return in, errors.New("date of birth required")
	case in.NationalityID == nil:
		return in, errors.New("nationality required")
	}

	natName, err := a.nationalityName(in.NationalityID)
	if err != nil {
		return in, ErrDatabase
	}
	personalID, err := validatePersonalID(in.PersonalID, natName)
	if err != nil {
		return in, err
	}
	in.PersonalID = personalID
	return in, nil
}

// execer is the part of *sql.DB and *sql.Tx that an insert needs, so the same
// insert can run inside a transaction or on its own.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// InsertPatient writes an already-validated patient and returns its ID. A
// duplicate name+DOB or NIK/passport ID comes back as a unique-violation error;
// use IsDuplicate and DuplicateMessage to handle it.
func InsertPatient(e execer, p NewPatient) (int64, error) {
	res, err := e.Exec(
		`INSERT INTO Patient (Name, Gender, DateOfBirth, FK_NationalityID, EmployeeID, FK_CompanyID, WorkPosition, PersonalID)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Name, nullIfEmpty(p.Gender), nullIfEmpty(p.DateOfBirth),
		p.NationalityID, nullIfEmpty(strings.TrimSpace(p.EmployeeID)),
		p.CompanyID, nullIfEmpty(p.WorkPosition), nullIfEmpty(p.PersonalID),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// IsDuplicate reports whether err is one of the patient UNIQUE constraints.
func IsDuplicate(err error) bool {
	return isUniqueViolation(err)
}

// DuplicateMessage says which patient field collided, given an error that
// IsDuplicate accepted. The two UNIQUE constraints are idx_patient_name_dob and
// idx_patient_personal_id (migrate.go's EnsureSchemaIndexes).
func DuplicateMessage(err error) string {
	if strings.Contains(err.Error(), "PersonalID") {
		return "a patient with this NIK/passport ID already exists"
	}
	return "a patient with this name and date of birth already exists"
}
