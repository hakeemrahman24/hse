// Reporting/analytics, viewable by any clinician role (same gate as the
// Employee/MCU data tabs) — only the PDF/Excel exports stay RequireAdmin.
// One endpoint backs both the full MCU data-table grid and the chart
// summaries on /mcu's "Dashboard" tab, since both read the same
// date-filtered row set.
package entrymcu

import (
	"hse-medpost/internal/dates"
	"net/http"
	"strings"
	"time"
)

type ReportRow struct {
	McuRecordID int64 `json:"mcuRecordId"`
	// PatientID is set only on Employee-tab rows for patients with no
	// checkup yet (McuRecordID is 0 for those).
	PatientID           int64   `json:"patientId,omitempty"`
	CheckUpID           string  `json:"checkUpId"`
	PatientName         string  `json:"patientName"`
	DateOfBirth         *string `json:"dateOfBirth"`
	PersonalID          *string `json:"personalId"`
	CompanyName         string  `json:"companyName"`
	CompanyAbbreviation string  `json:"companyAbbreviation"`
	Criteria            string  `json:"criteria"`
	TaskCategory        string  `json:"taskCategory"`
	ReviewDate          string  `json:"reviewDate"`
	ResultCriteria      *string `json:"resultCriteria"`
	ResultValidUntil    *string `json:"resultValidUntil"`
	CheckUpStatus       *string `json:"checkUpStatus"`
	HcValidityStatus    string  `json:"hcValidityStatus"`
	WorkPosition        string  `json:"workPosition"`
	DateCheckUp         string  `json:"dateCheckUp"`
}

type ReportCount struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

type ExpiringSoon struct {
	CheckUpID        string `json:"checkUpId"`
	PatientName      string `json:"patientName"`
	CompanyName      string `json:"companyName"`
	ResultValidUntil string `json:"resultValidUntil"`
	DaysLeft         int    `json:"daysLeft"`
}

type Report struct {
	Rows         []ReportRow        `json:"rows"`
	ByResult     []ReportCount      `json:"byResult"`
	ByCriteria   []ReportCount      `json:"byCriteria"`
	ByCompany    []ReportCount      `json:"byCompany"`
	ExpiringSoon []ExpiringSoon     `json:"expiringSoon"`
	Dashboard    DashboardAnalytics `json:"dashboard"`
}

const reportBaseQuery = `
	SELECT m.McuRecordID, m.CheckUpID, p.Name, p.DateOfBirth, m.PersonalID, COALESCE(co.Name,''), COALESCE(co.Abbreviation,''),
	       COALESCE(c.Name,''), COALESCE(m.TaskCategoryNames,''), m.ReviewDate,
	       m.ResultCriteria, m.ResultValidUntil, m.CheckUpStatus, m.HcValidityStatus
	FROM McuRecord m
	JOIN Patient p ON p.PatientID = m.FK_PatientID
	LEFT JOIN Company co ON co.CompanyID = m.FK_CompanyID
	LEFT JOIN CheckUpCriteriaTable c ON c.CheckUpCriteriaID = m.FK_CheckUpCriteriaID`

// GetMcuReport backs the Dashboard tab, viewable by any clinician role:
// ?from= and ?to= (both YYYY-MM-DD, both optional) filter Rows/ByResult/ByCriteria/
// ByCompany against ReviewDate; ExpiringSoon is always forward-looking from
// today regardless of the from/to filter, since "what's about to lapse" is
// a different question than "what happened in this date range" — windowed
// by ?expiryDays= (default 90).
func (a *API) GetMcuReport(w http.ResponseWriter, r *http.Request) {
	from := strings.TrimSpace(r.URL.Query().Get("from"))
	to := strings.TrimSpace(r.URL.Query().Get("to"))

	supSQL, supArgs := dashSupersededSQL(from, to)
	query := strings.Replace(reportBaseQuery, "m.HcValidityStatus\n", "m.HcValidityStatus, m.ResultNotes, m.Recommendation, "+supSQL+"\n", 1)
	args := supArgs
	var conds []string
	if from != "" {
		conds = append(conds, "m.ReviewDate >= ?")
		args = append(args, from)
	}
	if to != "" {
		conds = append(conds, "m.ReviewDate <= ?")
		args = append(args, to)
	}
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	query += " ORDER BY m.ReviewDate DESC"

	rows, err := a.DB.Query(query, args...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	resultCounts := map[string]int{}
	criteriaCounts := map[string]int{}
	companyCounts := map[string]int{}
	reportRows := []ReportRow{}
	dash := newDashAccum()
	for rows.Next() {
		var rr ReportRow
		var storedHcValidity, resultNotes, recommendation *string
		var superseded bool
		if err := rows.Scan(&rr.McuRecordID, &rr.CheckUpID, &rr.PatientName, &rr.DateOfBirth, &rr.PersonalID, &rr.CompanyName, &rr.CompanyAbbreviation,
			&rr.Criteria, &rr.TaskCategory, &rr.ReviewDate,
			&rr.ResultCriteria, &rr.ResultValidUntil, &rr.CheckUpStatus, &storedHcValidity, &resultNotes, &recommendation, &superseded); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		rr.HcValidityStatus = displayHcValidityStatus(storedHcValidity, rr.CheckUpStatus, rr.ResultValidUntil, rr.ResultCriteria, dates.ClinicNow())
		reportRows = append(reportRows, rr)
		dash.add(dashSrc{
			Company:  rr.CompanyName,
			Criteria: rr.Criteria,
			Result:   derefStr(rr.ResultCriteria),
			Status:   derefStr(rr.CheckUpStatus),
			HC:       rr.HcValidityStatus,

			CheckUpID:      rr.CheckUpID,
			Name:           rr.PatientName,
			ReviewDate:     rr.ReviewDate,
			ResultNotes:    derefStr(resultNotes),
			Recommendation: derefStr(recommendation),

			Superseded: superseded,
		})

		// The legacy dashboard excludes Close+NeedFollowUp rows from every
		// chart cohort (a closed record that still says "needs follow up"
		// is a dead end — the patient left, the case was archived without
		// resolution — so counting it alongside real Fit/Unfit outcomes
		// would skew the distribution). The raw data-table grid (reportRows
		// above) keeps these rows — this exclusion is chart-only.
		isClose := rr.CheckUpStatus != nil && *rr.CheckUpStatus == "Close"
		isNeedFollowUp := rr.ResultCriteria != nil && *rr.ResultCriteria == "NeedFollowUp"
		if isClose && isNeedFollowUp {
			continue
		}

		// A record that's been superseded by a follow-up/renewal (see
		// closeParentRecord in validity.go, the only writer of "Renewed by
		// <id>") is stale history, not a current outcome — its renewal is
		// its own separate row and will be counted on its own merits. Without
		// this, a patient with e.g. Fit -> renewed -> Fit again would count
		// twice under "Fit" for one person, inflating every chart cohort by
		// however many renewal generations exist in the date range.
		if strings.HasPrefix(rr.HcValidityStatus, "Renewed by ") {
			continue
		}

		result := "(none)"
		if rr.ResultCriteria != nil && *rr.ResultCriteria != "" {
			result = *rr.ResultCriteria
		}
		resultCounts[result]++

		criteria := "(none)"
		if rr.Criteria != "" {
			criteria = rr.Criteria
		}
		criteriaCounts[criteria]++

		company := "(none)"
		if rr.CompanyName != "" {
			company = rr.CompanyName
		}
		companyCounts[company]++
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	expiryDays := 90
	if d := r.URL.Query().Get("expiryDays"); d != "" {
		if parsed, ok := parsePositiveInt(d); ok {
			expiryDays = parsed
		}
	}
	expiring, err := a.fetchExpiringSoon(expiryDays, false)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	writeJSON(w, http.StatusOK, Report{
		Rows:         reportRows,
		ByResult:     countsToSorted(resultCounts),
		ByCriteria:   countsToSorted(criteriaCounts),
		ByCompany:    countsToSorted(companyCounts),
		ExpiringSoon: expiring,
		Dashboard:    dash.finish(),
	})
}

// GetMcuRecordsBrowse backs the Employee and MCU data tabs on /mcu — same
// clinician-only gate as GetMcuReport, since every staff member should be
// able to search all checkups and pull certificates, not just admins.
// ?latestOnly=1 (Employee tab) collapses to one row per patient — their
// single latest checkup (highest ReviewDate, ties broken by McuRecordID).
// Without it (MCU data tab), every checkup is returned.
// ?name= (Patient.Name substring, case-insensitive — SQLite's LIKE already
// is for ASCII) and ?companyId= (McuRecord.FK_CompanyID) narrow
// reportBaseQuery's existing row shape. No server-side sort: the frontend
// sorts client-side, same "load everything, sort/render in JS" convention
// already used by the programs and company lists.
func (a *API) GetMcuRecordsBrowse(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	companyID := strings.TrimSpace(r.URL.Query().Get("companyId"))
	latestOnly := r.URL.Query().Get("latestOnly") == "1"

	if latestOnly {
		a.writeEmployeeRows(w, name, companyID)
		return
	}

	query := strings.Replace(reportBaseQuery, "m.HcValidityStatus\n", "m.HcValidityStatus, COALESCE(NULLIF(TRIM(m.WorkPosition), ''), p.WorkPosition, ''), COALESCE(m.DateCheckUp, '')\n", 1)
	args := []any{}
	var conds []string
	if name != "" {
		conds = append(conds, "p.Name LIKE ?")
		args = append(args, "%"+name+"%")
	}
	if companyID != "" {
		conds = append(conds, "m.FK_CompanyID = ?")
		args = append(args, companyID)
	}
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	query += " ORDER BY m.ReviewDate DESC"

	rows, err := a.DB.Query(query, args...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	out := []ReportRow{}
	for rows.Next() {
		var rr ReportRow
		var storedHcValidity *string
		if err := rows.Scan(&rr.McuRecordID, &rr.CheckUpID, &rr.PatientName, &rr.DateOfBirth, &rr.PersonalID, &rr.CompanyName, &rr.CompanyAbbreviation,
			&rr.Criteria, &rr.TaskCategory, &rr.ReviewDate,
			&rr.ResultCriteria, &rr.ResultValidUntil, &rr.CheckUpStatus, &storedHcValidity, &rr.WorkPosition, &rr.DateCheckUp); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		rr.HcValidityStatus = displayHcValidityStatus(storedHcValidity, rr.CheckUpStatus, rr.ResultValidUntil, rr.ResultCriteria, dates.ClinicNow())
		out = append(out, rr)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// employeeRowsQuery starts from the Patient table, so every employee is
// listed. Each one's latest checkup (highest ReviewDate, ties broken by
// McuRecordID) is joined in when they have one; otherwise the checkup
// columns are empty and McuRecordID is 0.
const employeeRowsQuery = `
	SELECT COALESCE(m.McuRecordID, 0), COALESCE(m.CheckUpID, ''), p.PatientID, p.Name, p.DateOfBirth,
	       COALESCE(m.PersonalID, p.PersonalID), COALESCE(pco.Name, co.Name, ''), COALESCE(pco.Abbreviation, co.Abbreviation, ''),
	       COALESCE(c.Name, ''), COALESCE(m.TaskCategoryNames, ''), COALESCE(m.ReviewDate, ''),
	       COALESCE(m.ResultCriteria, ''), COALESCE(m.ResultValidUntil, ''), COALESCE(m.CheckUpStatus, ''), m.HcValidityStatus,
	       COALESCE(NULLIF(TRIM(m.WorkPosition), ''), p.WorkPosition, '')
	FROM Patient p
	LEFT JOIN Company pco ON pco.CompanyID = p.FK_CompanyID
	LEFT JOIN McuRecord m ON m.McuRecordID = (
		SELECT l.McuRecordID FROM McuRecord l
		WHERE l.FK_PatientID = p.PatientID
		ORDER BY l.ReviewDate DESC, l.McuRecordID DESC
		LIMIT 1
	)
	LEFT JOIN Company co ON co.CompanyID = m.FK_CompanyID
	LEFT JOIN CheckUpCriteriaTable c ON c.CheckUpCriteriaID = m.FK_CheckUpCriteriaID`

// writeEmployeeRows answers the Employee tab: one row per patient, with the
// latest checkup when there is one. ?name= and ?companyId= filter the
// patients, companyId against whichever company (checkup or patient
// fallback) applies to that row.
func (a *API) writeEmployeeRows(w http.ResponseWriter, name, companyID string) {
	query := employeeRowsQuery
	args := []any{}
	var conds []string
	if name != "" {
		conds = append(conds, "p.Name LIKE ?")
		args = append(args, "%"+name+"%")
	}
	if companyID != "" {
		conds = append(conds, "COALESCE(m.FK_CompanyID, p.FK_CompanyID) = ?")
		args = append(args, companyID)
	}
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	query += " ORDER BY p.Name"

	rows, err := a.DB.Query(query, args...)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	out := []ReportRow{}
	for rows.Next() {
		var rr ReportRow
		var storedHcValidity *string
		if err := rows.Scan(&rr.McuRecordID, &rr.CheckUpID, &rr.PatientID, &rr.PatientName, &rr.DateOfBirth,
			&rr.PersonalID, &rr.CompanyName, &rr.CompanyAbbreviation,
			&rr.Criteria, &rr.TaskCategory, &rr.ReviewDate,
			&rr.ResultCriteria, &rr.ResultValidUntil, &rr.CheckUpStatus, &storedHcValidity, &rr.WorkPosition); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		if rr.McuRecordID != 0 {
			rr.HcValidityStatus = displayHcValidityStatus(storedHcValidity, rr.CheckUpStatus, rr.ResultValidUntil, rr.ResultCriteria, dates.ClinicNow())
		}
		out = append(out, rr)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func parsePositiveInt(s string) (int, bool) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, n > 0
}

func countsToSorted(m map[string]int) []ReportCount {
	out := make([]ReportCount, 0, len(m))
	for label, count := range m {
		out = append(out, ReportCount{Label: label, Count: count})
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Count > out[j-1].Count; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// fetchExpiringSoon finds current (IsTerminal) records whose certificate
// lapses within the window — the renewal-planning list a CMO would use to
// chase people down before their fitness lapses, not just a historical
// report. activeOnly additionally excludes Close/Archived-Exit records —
// used by the email notifier (see emailnotify.go) so a patient who has
// already left/been archived doesn't get chased for a renewal; the admin
// report table keeps activeOnly=false since it shows the full picture.
func (a *API) fetchExpiringSoon(windowDays int, activeOnly bool) ([]ExpiringSoon, error) {
	today := dates.Today()
	until := dates.ClinicNow().AddDate(0, 0, windowDays).Format(dateOnly)

	query := `
		SELECT m.CheckUpID, p.Name, COALESCE(co.Name,''), m.ResultValidUntil
		FROM McuRecord m
		JOIN Patient p ON p.PatientID = m.FK_PatientID
		LEFT JOIN Company co ON co.CompanyID = m.FK_CompanyID
		WHERE m.IsTerminal = 1 AND m.ResultCriteria IN ('Fit', 'FitWithNote') AND m.ResultValidUntil IS NOT NULL
		  AND m.ResultValidUntil >= ? AND m.ResultValidUntil <= ?`
	if activeOnly {
		query += ` AND (m.CheckUpStatus IS NULL OR m.CheckUpStatus NOT IN ('Close', 'Archived / Exit'))`
	}
	query += ` ORDER BY m.ResultValidUntil ASC`

	rows, err := a.DB.Query(query, today, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	todayT, _ := time.Parse(dateOnly, today)
	out := []ExpiringSoon{}
	for rows.Next() {
		var e ExpiringSoon
		if err := rows.Scan(&e.CheckUpID, &e.PatientName, &e.CompanyName, &e.ResultValidUntil); err != nil {
			return nil, err
		}
		if t, err := time.Parse(dateOnly, e.ResultValidUntil); err == nil {
			e.DaysLeft = int(t.Sub(todayT).Hours() / 24)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
