package directory

import (
	"net/http"

	"hse-medpost/internal/db"
	"hse-medpost/internal/entrymcu"
)

// LatestCheckup is one employee with their latest checkup, whatever its result. The employee
// endpoints skip open, incomplete and renewed rows when they pick "latest" (they answer "is this
// person cleared"); this one answers "what is the newest thing on file", so it shows exactly that,
// including Unfit, Temporarily Unfit, Need follow up, Incomplete, overdue and archived.
type LatestCheckup struct {
	Employee
	ReviewDate       string `json:"reviewDate"`
	Recommendation   string `json:"recommendation"`
	CheckUpStatus    string `json:"checkUpStatus"`
	HcValidityStatus string `json:"hcValidityStatus"`
}

// LatestCheckups returns one row per employee that has any checkup. "Latest" is the newest by
// checkup date (else review date), then the newest record. Filters (see filter.go) apply to that
// latest checkup, so result=Unfit means "people whose LATEST checkup is Unfit". Only the all-checkups
// token opens it. There are no exam sections and no ID number: it is a status feed, not the record.
func (a *API) LatestCheckups(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeFull(w, r) {
		return
	}
	f, err := parseFilter(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	where, args := f.conditions("p", "m")
	rows, err := a.DB.Query(`
		SELECT p.PatientID, COALESCE(p.EmployeeID,''), p.Name, COALESCE(p.Gender,''), COALESCE(p.DateOfBirth,''),
		       COALESCE(n.Name,''), COALESCE(`+db.LatestBloodTypeSQL("p")+`,''),
		       COALESCE(c.CompanyID, 0), COALESCE(c.Name,''), COALESCE(e.Name,''),
		       COALESCE(NULLIF(p.WorkPosition,''), NULLIF(m.WorkPosition,''), ''),
		       m.CheckUpID, m.ReviewDate, COALESCE(m.DateCheckUp,''), COALESCE(m.ResultCriteria,''),
		       COALESCE(m.ResultValidUntil,''), COALESCE(m.ResultNotes,''), COALESCE(m.Recommendation,''),
		       COALESCE(m.CheckUpStatus,''), COALESCE(m.HcValidityStatus,'')
		FROM Patient p
		JOIN McuRecord m ON m.McuRecordID = (
		    SELECT x.McuRecordID FROM McuRecord x WHERE x.FK_PatientID = p.PatientID
		    ORDER BY `+checkupDate("x")+` DESC, x.McuRecordID DESC LIMIT 1)
		LEFT JOIN NationalityTable n ON n.NationalityID = p.FK_NationalityID
		LEFT JOIN Company c ON c.CompanyID = COALESCE(p.FK_CompanyID, m.FK_CompanyID)
		LEFT JOIN EntityType e ON e.EntityTypeID = c.FK_EntityTypeID
		WHERE 1 = 1`+where+` ORDER BY p.Name, p.PatientID`, args...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	today := a.now()
	out := []LatestCheckup{}
	companyIDs := []int64{}
	seenCompany := map[int64]bool{}
	for rows.Next() {
		var l LatestCheckup
		var stored string
		if err := rows.Scan(
			&l.PatientID, &l.EmployeeID, &l.Name, &l.Gender, &l.DateOfBirth,
			&l.Nationality, &l.BloodType,
			&l.companyID, &l.Company, &l.CompanyEntity, &l.WorkPosition,
			&l.CheckUpID, &l.ReviewDate, &l.DateCheckUp, &l.ResultCriteria,
			&l.ResultValidUntil, &l.ResultNotes, &l.Recommendation,
			&l.CheckUpStatus, &stored,
		); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		// The standing needs the stored code (FitWithNote), so it is computed before the words.
		l.HcValidityStatus = entrymcu.ShownValidity(stored, l.CheckUpStatus, l.ResultValidUntil, l.ResultCriteria, today)
		l.ResultCriteria = reviewLabel(l.ResultCriteria)
		l.Age = ageYears(l.DateOfBirth, today)
		l.Parents = []Parent{}
		out = append(out, l)
		if l.companyID > 0 && !seenCompany[l.companyID] {
			seenCompany[l.companyID] = true
			companyIDs = append(companyIDs, l.companyID)
		}
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	parents, err := a.parentsByCompany(companyIDs)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	for i := range out {
		if list := parents[out[i].companyID]; list != nil {
			out[i].Parents = list
		}
	}
	writeJSON(w, http.StatusOK, out)
}
