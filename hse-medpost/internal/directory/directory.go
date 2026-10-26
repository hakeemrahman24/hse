package directory

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"hse-medpost/internal/db"
)

// API serves employee identity plus the latest MCU review result to other
// databases. Exam sections stay in McuRecord and are not part of this payload.
type API struct {
	DB        *sql.DB
	Token     string
	FullToken string
	Now       func() time.Time
}

type Parent struct {
	Name  string `json:"name"`
	Depth int    `json:"depth"`
}

type Employee struct {
	PatientID        int64    `json:"patientId"`
	EmployeeID       string   `json:"employeeId"`
	Name             string   `json:"name"`
	Gender           string   `json:"gender"`
	DateOfBirth      string   `json:"dateOfBirth"`
	Age              int      `json:"age,omitempty"`
	Nationality      string   `json:"nationality"`
	BloodType        string   `json:"bloodType"`
	Company          string   `json:"company"`
	CompanyEntity    string   `json:"companyEntity"`
	Parents          []Parent `json:"parents"`
	WorkPosition     string   `json:"workPosition"`
	CheckUpID        string   `json:"checkUpId"`
	DateCheckUp      string   `json:"dateCheckUp"`
	ResultCriteria   string   `json:"resultCriteria"`
	ResultValidUntil string   `json:"resultValidUntil"`
	ResultNotes      string   `json:"resultNotes"`
	companyID        int64
}

func (a *API) List(w http.ResponseWriter, r *http.Request) {
	if !a.authorize(w, r) {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) < 2 {
		q = ""
	}
	checkUpID := strings.TrimSpace(r.URL.Query().Get("checkUpId"))
	patientID, err := parsePatientID(r.URL.Query().Get("patientId"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid patientId"})
		return
	}
	dob := strings.TrimSpace(r.URL.Query().Get("dob"))
	if q == "" && checkUpID == "" && patientID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "q, patientId, or checkUpId required"})
		return
	}

	rows, err := a.listEmployees(q, dob, checkUpID, patientID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (a *API) authorize(w http.ResponseWriter, r *http.Request) bool {
	return a.authorizeAccess(w, r, false)
}

func (a *API) authorizeFull(w http.ResponseWriter, r *http.Request) bool {
	return a.authorizeAccess(w, r, true)
}

// authorizeAccess checks the bearer token. Eligible routes accept the
// fitness token or the all-checkups token. The full checkup route accepts
// only the all-checkups token, so a database that was given the fitness
// token cannot read every exam.
func (a *API) authorizeAccess(w http.ResponseWriter, r *http.Request, fullOnly bool) bool {
	eligible := a.currentToken()
	full := a.currentFullToken()
	if fullOnly {
		if full == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "directory api is not configured"})
			return false
		}
	} else if eligible == "" && full == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "directory api is not configured"})
		return false
	}
	got := bearerToken(r)
	if fullOnly {
		if !tokenMatches(got, full) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return false
		}
		return true
	}
	if tokenMatches(got, eligible) || tokenMatches(got, full) {
		return true
	}
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	return false
}

func tokenMatches(got, want string) bool {
	if got == "" || want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func bearerToken(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

func parsePatientID(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseInt(raw, 10, 64)
}

func (a *API) listEmployees(q, dob, checkUpID string, patientID int64) ([]Employee, error) {
	query, args := employeeQuery(q, dob, checkUpID, patientID, a.today())
	rows, err := a.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Employee{}
	companyIDs := []int64{}
	seenCompany := map[int64]bool{}
	for rows.Next() {
		emp, err := scanEmployee(rows)
		if err != nil {
			return nil, err
		}
		emp.Age = ageYears(emp.DateOfBirth, a.now())
		emp.ResultCriteria = reviewLabel(emp.ResultCriteria)
		emp.Parents = []Parent{}
		out = append(out, emp)
		if emp.companyID > 0 && !seenCompany[emp.companyID] {
			seenCompany[emp.companyID] = true
			companyIDs = append(companyIDs, emp.companyID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	parents, err := a.parentsByCompany(companyIDs)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if list := parents[out[i].companyID]; list != nil {
			out[i].Parents = list
		}
	}
	return out, nil
}

func employeeQuery(q, dob, checkUpID string, patientID int64, today string) (string, []any) {
	query := `
		SELECT p.PatientID, COALESCE(p.EmployeeID,''), p.Name, COALESCE(p.Gender,''), COALESCE(p.DateOfBirth,''),
		       COALESCE(n.Name,''), COALESCE(` + db.LatestBloodTypeSQL("p") + `,''),
		       COALESCE(c.CompanyID, 0), COALESCE(c.Name,''), COALESCE(e.Name,''),
		       COALESCE(NULLIF(p.WorkPosition,''), NULLIF(m.WorkPosition,''), ''),
		       m.CheckUpID, COALESCE(m.DateCheckUp,''), COALESCE(m.ResultCriteria,''),
		       COALESCE(m.ResultValidUntil,''), COALESCE(m.ResultNotes,'')
		FROM Patient p
		JOIN McuRecord m ON m.FK_PatientID = p.PatientID
		LEFT JOIN NationalityTable n ON n.NationalityID = p.FK_NationalityID
		LEFT JOIN Company c ON c.CompanyID = COALESCE(p.FK_CompanyID, m.FK_CompanyID)
		LEFT JOIN EntityType e ON e.EntityTypeID = c.FK_EntityTypeID
		WHERE m.ResultCriteria IN ('Fit', 'FitWithNote')
		  AND m.CheckUpStatus = 'Close'
		  AND COALESCE(m.ResultValidUntil, '') >= ?
		  AND NOT EXISTS (
		    SELECT 1 FROM McuRecord newer
		    WHERE newer.FK_PatientID = m.FK_PatientID
		      AND (
		        COALESCE(newer.DateCheckUp, '') > COALESCE(m.DateCheckUp, '')
		        OR (COALESCE(newer.DateCheckUp, '') = COALESCE(m.DateCheckUp, '') AND newer.McuRecordID > m.McuRecordID)
		      )
		  )`
	args := []any{today}
	if q != "" {
		query += ` AND LOWER(p.Name) LIKE ?`
		args = append(args, "%"+strings.ToLower(q)+"%")
	}
	if dob != "" {
		query += ` AND p.DateOfBirth = ?`
		args = append(args, dob)
	}
	if checkUpID != "" {
		query += ` AND m.CheckUpID = ?`
		args = append(args, checkUpID)
	}
	if patientID != 0 {
		query += ` AND p.PatientID = ?`
		args = append(args, patientID)
	}
	query += ` ORDER BY p.Name LIMIT 20`
	return query, args
}

func scanEmployee(rows *sql.Rows) (Employee, error) {
	var emp Employee
	err := rows.Scan(
		&emp.PatientID, &emp.EmployeeID, &emp.Name, &emp.Gender, &emp.DateOfBirth,
		&emp.Nationality, &emp.BloodType,
		&emp.companyID, &emp.Company, &emp.CompanyEntity, &emp.WorkPosition,
		&emp.CheckUpID, &emp.DateCheckUp, &emp.ResultCriteria, &emp.ResultValidUntil, &emp.ResultNotes,
	)
	return emp, err
}

func (a *API) parentsByCompany(companyIDs []int64) (map[int64][]Parent, error) {
	out := map[int64][]Parent{}
	if len(companyIDs) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(companyIDs))
	args := make([]any, len(companyIDs))
	for i, id := range companyIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	query := `
		SELECT cr.FK_CompanyID, COALESCE(parent.Name,''), COALESCE(cr.Depth, 0)
		FROM CompanyRelation cr
		LEFT JOIN Company parent ON parent.CompanyID = cr.FK_ParentID
		WHERE cr.FK_CompanyID IN (` + strings.Join(placeholders, ",") + `)
		ORDER BY cr.Depth, parent.Name`
	rows, err := a.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var companyID int64
		var parent Parent
		if err := rows.Scan(&companyID, &parent.Name, &parent.Depth); err != nil {
			return nil, err
		}
		if parent.Name == "" {
			continue
		}
		out[companyID] = append(out[companyID], parent)
	}
	return out, rows.Err()
}

func (a *API) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *API) today() string {
	return a.now().Format("2006-01-02")
}

func reviewLabel(stored string) string {
	if stored == "FitWithNote" {
		return "Fit with note"
	}
	return stored
}

func ageYears(dob string, today time.Time) int {
	born, err := time.Parse("2006-01-02", dob)
	if err != nil {
		return 0
	}
	years := today.Year() - born.Year()
	if today.Month() < born.Month() || (today.Month() == born.Month() && today.Day() < born.Day()) {
		years--
	}
	if years < 0 {
		return 0
	}
	return years
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
