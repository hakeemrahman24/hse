// Package patient owns the shared patient identity extracted out of
// McuRecord's old inlined demographics (name, DOB, gender, nationality,
// blood type, employer) — entrymcu, and later emr/screening, all resolve
// FK_PatientID against this package's Patient table instead of re-entering
// the same identity on every record. NationalityTable lives here too since
// Patient is its only reference (see internal/README.md's ownership index).
package patient

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"hse-medpost/internal/db"
)

type API struct {
	DB *sql.DB

	// InvalidateCertificates is wired from main.go to
	// entrymcu.API.InvalidateCertificatesForPatient — kept as an injected
	// callback rather than an import so patient/ doesn't have to depend on
	// entrymcu/ (only entrymcu depends on patient today). Nil-safe: unset
	// in tests or if entrymcu isn't wired up.
	InvalidateCertificates func(patientID int64) error
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

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

var (
	nikPattern      = regexp.MustCompile(`^[0-9]{16}$`)
	passportPattern = regexp.MustCompile(`^[A-Z0-9]{7,9}$`)
)

// validatePersonalID enforces PersonalID's two formats: a 16-digit NIK for
// Indonesian nationality, or a 7-9 character alphanumeric passport number
// otherwise (ICAO passport numbers commonly run 6-9 characters; 7-9 covers
// the vast majority without being so wide it stops catching typos). It's
// optional — an empty value is left alone, since PersonalID only becomes a
// dedup key (idx_patient_personal_id) once someone actually fills it in.
// Returns the normalized (trimmed, uppercased) value to store.
func validatePersonalID(personalID, nationalityName string) (string, error) {
	personalID = strings.ToUpper(strings.TrimSpace(personalID))
	if personalID == "" {
		return "", nil
	}
	if strings.EqualFold(strings.TrimSpace(nationalityName), "Indonesia") {
		if !nikPattern.MatchString(personalID) {
			return "", errInvalidNIK
		}
		return personalID, nil
	}
	if !passportPattern.MatchString(personalID) {
		return "", errInvalidPassport
	}
	return personalID, nil
}

// NormalizePersonalID is the bulk-import entry point for the same NIK and
// passport rules the patient form uses.
func NormalizePersonalID(personalID, nationalityName string) (string, error) {
	return validatePersonalID(personalID, nationalityName)
}

var (
	nonNameChars     = regexp.MustCompile(`[^\p{L}\p{M}\s'.\-]+`)
	nonPositionChars = regexp.MustCompile(`[^\p{L}\p{N}\s]+`)
	repeatedSpace    = regexp.MustCompile(`\s{2,}`)
	asciiWord        = regexp.MustCompile(`[A-Za-z]+`)
)

// NormalizePersonName keeps letters, combining marks, space, apostrophe, hyphen
// and dot, collapses spaces, and title-cases: as'adul becomes As'adul, o'connor
// O'Connor, JOHN DOE John Doe, nur-aini Nur-Aini. Curly apostrophes become the
// plain one. This is the same rule as the staff form (case-guard.js) and
// SanitizeName in internal/mcusubmit.
func NormalizePersonName(s string) string {
	s = strings.NewReplacer("‘", "'", "’", "'").Replace(norm.NFC.String(s))
	return titlePersonName(collapseSpaces(nonNameChars.ReplaceAllString(s, "")))
}

// titlePersonName starts a word after a space, hyphen or dot. After an
// apostrophe the next letter is capital only behind a one-letter start
// (O'Neil, D'Angelo), so Ma'ruf and As'adul keep their lower case.
func titlePersonName(s string) string {
	var b strings.Builder
	upperNext := true
	lettersInWord := 0
	for _, r := range s {
		switch {
		case unicode.IsLetter(r):
			if upperNext {
				b.WriteRune(unicode.ToUpper(r))
			} else {
				b.WriteRune(unicode.ToLower(r))
			}
			upperNext = false
			lettersInWord++
		case r == '\'':
			b.WriteRune(r)
			upperNext = lettersInWord == 1
		case unicode.IsMark(r):
			b.WriteRune(r)
		default: // space, hyphen, dot
			b.WriteRune(r)
			upperNext = true
			lettersInWord = 0
		}
	}
	return b.String()
}

// NormalizeWorkPosition is the same styling, and digits stay.
// welder 3g becomes Welder 3G.
func NormalizeWorkPosition(s string) string {
	return titleASCIIWords(collapseSpaces(nonPositionChars.ReplaceAllString(s, "")))
}

// NormalizeOptionalText applies a normalizer to a nullable field and stores
// nil when the result is empty.
func NormalizeOptionalText(s *string, norm func(string) string) *string {
	if s == nil {
		return nil
	}
	v := norm(*s)
	if v == "" {
		return nil
	}
	return &v
}

func collapseSpaces(s string) string {
	return strings.TrimSpace(repeatedSpace.ReplaceAllString(s, " "))
}

func titleASCIIWords(s string) string {
	return asciiWord.ReplaceAllStringFunc(s, func(word string) string {
		return strings.ToUpper(word[:1]) + strings.ToLower(word[1:])
	})
}

var (
	errInvalidNIK      = &validationError{"NIK must be exactly 16 digits"}
	errInvalidPassport = &validationError{"passport ID must be 7-9 alphanumeric characters (A-Z, 0-9)"}
)

type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }

// nationalityName resolves a NationalityID to its Name, used to decide
// which PersonalID format applies (NIK vs passport). Returns "" for a nil
// id, matching validatePersonalID's "no nationality yet" fallback to the
// passport branch (the more permissive of the two).
func (a *API) nationalityName(id *int64) (string, error) {
	if id == nil {
		return "", nil
	}
	var name string
	err := a.DB.QueryRow(`SELECT Name FROM NationalityTable WHERE NationalityID = ?`, *id).Scan(&name)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return name, err
}

// --- NationalityTable ------------------------------------------------------

type Nationality struct {
	NationalityID int64  `json:"nationalityId"`
	Name          string `json:"name"`
	Status        string `json:"status"`
}

func (a *API) ListNationalities(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.Query(`SELECT NationalityID, Name, Status FROM NationalityTable ORDER BY Name`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	out := []Nationality{}
	for rows.Next() {
		var n Nationality
		if err := rows.Scan(&n.NationalityID, &n.Name, &n.Status); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) CreateNationality(w http.ResponseWriter, r *http.Request) {
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
	res, err := a.DB.Exec(`INSERT INTO NationalityTable (Name) VALUES (?)`, body.Name)
	if err != nil {
		if isUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "nationality already exists"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "nationalityId": id})
}

func (a *API) UpdateNationality(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
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
	res, err := a.DB.Exec(`UPDATE NationalityTable SET Name = ? WHERE NationalityID = ?`, body.Name, id)
	if err != nil {
		if isUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "nationality already exists"})
			return
		}
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

func (a *API) SetNationalityStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	status := strings.TrimSpace(body.Status)
	if status != "Active" && status != "Archived" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "status must be Active or Archived"})
		return
	}
	res, err := a.DB.Exec(`UPDATE NationalityTable SET Status = ? WHERE NationalityID = ?`, status, id)
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

func (a *API) DeleteNationality(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	res, err := a.DB.Exec(`DELETE FROM NationalityTable WHERE NationalityID = ?`, id)
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

// --- Patient ---------------------------------------------------------------

type Patient struct {
	PatientID       int64  `json:"patientId"`
	Name            string `json:"name"`
	Gender          string `json:"gender"`
	DateOfBirth     string `json:"dateOfBirth"`
	NationalityID   *int64 `json:"nationalityId"`
	NationalityName string `json:"nationalityName"`
	BloodType       string `json:"bloodType"`
	EmployeeID      string `json:"employeeId"`
	CompanyID       *int64 `json:"companyId"`
	CompanyName     string `json:"companyName"`
	WorkPosition    string `json:"workPosition"`
	PersonalID      string `json:"personalId"`
	// MCUDate and MCUValidUntil come from the person's LATEST checkup (its checkup date, else its
	// review date; ties go to the newest record). Both are "" for someone with no checkup, and
	// MCUValidUntil is "" while that checkup is still open (no result, so nothing to expire).
	MCUDate       string `json:"mcuDate"`
	MCUValidUntil string `json:"mcuValidUntil"`
}

// latestCheckupDate is the date a checkup counts under: the date it was done, else the day it was
// reviewed. The same expression orders the records and is shown, so the two can never disagree.
func checkupDateSQL(alias string) string {
	return "COALESCE(NULLIF(" + alias + ".DateCheckUp, ''), " + alias + ".ReviewDate)"
}

// ListPatients supports an optional ?q= substring filter against Name, for
// the checkup-entry form's patient picker (search-as-you-type against a
// roster that will eventually be much larger than a single dropdown page).
func (a *API) ListPatients(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	query := `
		SELECT p.PatientID, p.Name, COALESCE(p.Gender,''), COALESCE(p.DateOfBirth,''),
		       p.FK_NationalityID, COALESCE(n.Name,''), COALESCE(` + db.LatestBloodTypeSQL("p") + `,''),
		       COALESCE(p.EmployeeID,''), p.FK_CompanyID, COALESCE(c.Name,''), COALESCE(p.WorkPosition,''),
		       COALESCE(p.PersonalID,''),
		       COALESCE(` + checkupDateSQL("lm") + `, ''), COALESCE(lm.ResultValidUntil, '')
		FROM Patient p
		LEFT JOIN NationalityTable n ON n.NationalityID = p.FK_NationalityID
		LEFT JOIN Company c ON c.CompanyID = p.FK_CompanyID
		LEFT JOIN McuRecord lm ON lm.McuRecordID = (
		    SELECT m.McuRecordID FROM McuRecord m WHERE m.FK_PatientID = p.PatientID
		    ORDER BY ` + checkupDateSQL("m") + ` DESC, m.McuRecordID DESC LIMIT 1)`
	args := []any{}
	if q != "" {
		query += ` WHERE ` + db.NameLikeSQL("p.Name")
		args = append(args, db.NameLikeArg(q))
	}
	query += ` ORDER BY p.Name`

	rows, err := a.DB.Query(query, args...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	out := []Patient{}
	for rows.Next() {
		var p Patient
		if err := rows.Scan(&p.PatientID, &p.Name, &p.Gender, &p.DateOfBirth,
			&p.NationalityID, &p.NationalityName, &p.BloodType,
			&p.EmployeeID, &p.CompanyID, &p.CompanyName, &p.WorkPosition,
			&p.PersonalID, &p.MCUDate, &p.MCUValidUntil); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) CreatePatient(w http.ResponseWriter, r *http.Request) {
	var body NewPatient
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	clean, err := a.ValidateNew(body)
	if err != nil {
		if errors.Is(err, ErrDatabase) {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	id, err := InsertPatient(a.DB, clean)
	if err != nil {
		if IsDuplicate(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": DuplicateMessage(err)})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "patientId": id})
}

func (a *API) UpdatePatient(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	var body struct {
		Name          *string `json:"name"`
		Gender        *string `json:"gender"`
		DateOfBirth   *string `json:"dateOfBirth"`
		NationalityID *int64  `json:"nationalityId"`
		EmployeeID    *string `json:"employeeId"`
		PersonalID    *string `json:"personalId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	var exists int
	if err := a.DB.QueryRow(`SELECT 1 FROM Patient WHERE PatientID = ?`, id).Scan(&exists); err == sql.ErrNoRows {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	// certRelevantChanged tracks whether this edit touches a field the
	// health-certificate PDF actually embeds (Name/DateOfBirth) —
	// Gender/NationalityID/EmployeeID never
	// appear on the certificate, so editing only those doesn't need to
	// invalidate anything already generated.
	certRelevantChanged := false

	if body.Name != nil {
		name := NormalizePersonName(*body.Name)
		if name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "patient name required"})
			return
		}
		if _, err := a.DB.Exec(`UPDATE Patient SET Name = ? WHERE PatientID = ?`, name, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		certRelevantChanged = true
	}
	if body.Gender != nil {
		if _, err := a.DB.Exec(`UPDATE Patient SET Gender = ? WHERE PatientID = ?`, nullIfEmpty(strings.TrimSpace(*body.Gender)), id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if body.DateOfBirth != nil {
		if _, err := a.DB.Exec(`UPDATE Patient SET DateOfBirth = ? WHERE PatientID = ?`, nullIfEmpty(strings.TrimSpace(*body.DateOfBirth)), id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		certRelevantChanged = true
	}
	if body.NationalityID != nil {
		if _, err := a.DB.Exec(`UPDATE Patient SET FK_NationalityID = ? WHERE PatientID = ?`, *body.NationalityID, id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if body.PersonalID != nil {
		// Which format applies (NIK vs passport) depends on nationality —
		// use the value this same request just set above if given,
		// otherwise fall back to whatever's already on file, so editing
		// PersonalID alone (the common case) still validates against the
		// patient's real nationality instead of treating them as
		// nationality-less.
		nationalityID := body.NationalityID
		if nationalityID == nil {
			if err := a.DB.QueryRow(`SELECT FK_NationalityID FROM Patient WHERE PatientID = ?`, id).Scan(&nationalityID); err != nil && err != sql.ErrNoRows {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
				return
			}
		}
		natName, err := a.nationalityName(nationalityID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		personalID, verr := validatePersonalID(*body.PersonalID, natName)
		if verr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": verr.Error()})
			return
		}
		if _, err := a.DB.Exec(`UPDATE Patient SET PersonalID = ? WHERE PatientID = ?`, nullIfEmpty(personalID), id); err != nil {
			if isUniqueViolation(err) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "a patient with this NIK/passport ID already exists"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}
	if body.EmployeeID != nil {
		if _, err := a.DB.Exec(`UPDATE Patient SET EmployeeID = ? WHERE PatientID = ?`, nullIfEmpty(strings.TrimSpace(*body.EmployeeID)), id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
	}

	// Best-effort, same as certificate.go's own audit logging: the patient
	// edit itself already succeeded above, so a failure clearing stale
	// certificates shouldn't turn that success into an error response.
	if certRelevantChanged && a.InvalidateCertificates != nil {
		_ = a.InvalidateCertificates(id)
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *API) DeletePatient(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	res, err := a.DB.Exec(`DELETE FROM Patient WHERE PatientID = ?`, id)
	if err != nil {
		// McuRecord.FK_PatientID has no ON DELETE clause on SQLite by
		// default, so a patient with existing checkups surfaces as a plain
		// db error here rather than a friendlier message — acceptable until
		// that's actually hit in practice (same call already made for
		// company.DeleteCompany).
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
