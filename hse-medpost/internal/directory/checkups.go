package directory

import (
	"database/sql"
	"net/http"
	"time"

	"hse-medpost/internal/entrymcu"
)

// Fitness lists every Fit and Fit with note checkup whose standing is not
// ReEvaluate / Overdue and not Archived / Exit. The standing is the one a
// reader sees, including a date that has already lapsed.
func (a *API) Fitness(w http.ResponseWriter, r *http.Request) {
	if !a.authorize(w, r) {
		return
	}
	rows, err := a.DB.Query(`
		SELECT p.PatientID, COALESCE(p.EmployeeID,''), p.Name, COALESCE(p.Gender,''), COALESCE(p.DateOfBirth,''),
		       COALESCE(n.Name,''), COALESCE(m.BloodType,''),
		       COALESCE(c.CompanyID, 0), COALESCE(c.Name,''), COALESCE(e.Name,''),
		       COALESCE(NULLIF(p.WorkPosition,''), NULLIF(m.WorkPosition,''), ''),
		       m.CheckUpID, COALESCE(m.DateCheckUp,''), COALESCE(m.ResultCriteria,''),
		       COALESCE(m.ResultValidUntil,''), COALESCE(m.ResultNotes,''),
		       COALESCE(m.CheckUpStatus,''), COALESCE(m.HcValidityStatus,'')
		FROM Patient p
		JOIN McuRecord m ON m.FK_PatientID = p.PatientID
		LEFT JOIN NationalityTable n ON n.NationalityID = p.FK_NationalityID
		LEFT JOIN Company c ON c.CompanyID = COALESCE(p.FK_CompanyID, m.FK_CompanyID)
		LEFT JOIN EntityType e ON e.EntityTypeID = c.FK_EntityTypeID
		WHERE m.ResultCriteria IN ('Fit', 'FitWithNote')
		ORDER BY p.Name, m.CheckUpID`)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	today := a.now()
	out := []FitnessReview{}
	companyIDs := []int64{}
	seenCompany := map[int64]bool{}
	for rows.Next() {
		var review FitnessReview
		var status, stored string
		if err := rows.Scan(
			&review.PatientID, &review.EmployeeID, &review.Name, &review.Gender, &review.DateOfBirth,
			&review.Nationality, &review.BloodType,
			&review.companyID, &review.Company, &review.CompanyEntity, &review.WorkPosition,
			&review.CheckUpID, &review.DateCheckUp, &review.ResultCriteria, &review.ResultValidUntil, &review.ResultNotes,
			&status, &stored,
		); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		review.HcValidityStatus = entrymcu.ShownValidity(stored, status, review.ResultValidUntil, review.ResultCriteria, today)
		if review.HcValidityStatus == "ReEvaluate / Overdue" || review.HcValidityStatus == "Archived / Exit" {
			continue
		}
		review.Age = ageYears(review.DateOfBirth, today)
		review.ResultCriteria = reviewLabel(review.ResultCriteria)
		review.Parents = []Parent{}
		out = append(out, review)
		if review.companyID > 0 && !seenCompany[review.companyID] {
			seenCompany[review.companyID] = true
			companyIDs = append(companyIDs, review.companyID)
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

// FitnessReview is one Fit or Fit with note checkup that is still a clearance.
type FitnessReview struct {
	Employee
	HcValidityStatus string `json:"hcValidityStatus"`
}

// Checkups returns every medical checkup, including exam sections. The
// caller is another database using the same bearer token as the employee search.
func (a *API) Checkups(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeFull(w, r) {
		return
	}
	rows, err := a.DB.Query(checkupQuery)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	records, err := scanCheckups(rows)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	relations, err := a.relationLabels()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	today := a.now()
	for i := range records {
		company, _ := records[i]["company"].(string)
		if rel, ok := relations[company]; ok {
			records[i]["parent"] = rel.parent
			records[i]["project"] = rel.project
			records[i]["depth"] = rel.depth
		}
		stored, _ := records[i]["hcValidityStatus"].(string)
		status, _ := records[i]["checkUpStatus"].(string)
		until, _ := records[i]["resultValidUntil"].(string)
		result, _ := records[i]["resultCriteria"].(string)
		records[i]["hcValidityStatus"] = entrymcu.ShownValidity(stored, status, until, result, today)
		// WHY after ShownValidity: that call needs the stored code (FitWithNote), and the reader
		// gets the words, the same as the employee endpoints.
		records[i]["resultCriteria"] = reviewLabel(result)
	}
	writeJSON(w, http.StatusOK, records)
}

const checkupQuery = `
	SELECT m.FK_PatientID, m.CheckUpID, p.Name, COALESCE(p.DateOfBirth,''),
	       COALESCE(p.Gender,''), COALESCE(n.Name,''), COALESCE(m.BloodType,''), COALESCE(p.EmployeeID,''),
	       COALESCE(m.PersonalID,''),
	       COALESCE(co.Name,''), COALESCE(m.WorkPosition, p.WorkPosition, ''),
	       COALESCE(c.Name,''), COALESCE(m.TaskCategoryNames,''),
	       m.ReviewDate, COALESCE(m.DateCheckUp,''), COALESCE(m.Reviewer,''),
	       m.Age, m.HeightCm, m.WeightKg, m.IsShortProject, m.ValidityPeriodMonths,
	       m.BmiValue, COALESCE(m.BmiCriteria,''), COALESCE(m.BmiNotes,''), COALESCE(m.Smoking,''), COALESCE(m.OtherPhysicalExamNotes,''),
	       COALESCE(m.BpCriteria,''), COALESCE(m.BpNotes,''), COALESCE(m.VisionCriteria,''), COALESCE(m.VisionNotes,''),
	       COALESCE(m.CbcCriteria,''), COALESCE(m.CbcNotes,''), COALESCE(m.LipidCriteria,''), COALESCE(m.LipidNotes,''),
	       COALESCE(m.GlucoseCriteria,''), COALESCE(m.GlucoseNotes,''), COALESCE(m.LiverCriteria,''), COALESCE(m.LiverNotes,''),
	       COALESCE(m.KidneyCriteria,''), COALESCE(m.KidneyNotes,''), COALESCE(m.UrinalysisCriteria,''), COALESCE(m.UrinalysisNotes,''),
	       COALESCE(m.RadiologyResult,''), COALESCE(m.RadiologyNotes,''),
	       COALESCE(m.EchocardiographyCriteria,''), COALESCE(m.EchocardiographyNotes,''), COALESCE(m.CtScanCriteria,''), COALESCE(m.CtScanNotes,''),
	       COALESCE(m.UsgAbdomenCriteria,''), COALESCE(m.UsgAbdomenNotes,''),
	       COALESCE(m.EcgResult,''), COALESCE(m.TreadmillResult,''), COALESCE(m.EcgTreadmillNotes,''),
	       COALESCE(m.HerniaHemorrhoidCriteria,''), COALESCE(m.HerniaHemorrhoidNotes,''), COALESCE(m.SpirometryCriteria,''), COALESCE(m.SpirometryNotes,''),
	       COALESCE(m.AudiometryCriteria,''), COALESCE(m.AudiometryNotes,''), COALESCE(m.HepBCriteria,''), COALESCE(m.HepBNotes,''), COALESCE(m.HepACriteria,''), COALESCE(m.HepANotes,''),
	       COALESCE(m.RectalSwabCriteria,''), COALESCE(m.RectalSwabNotes,''), COALESCE(m.HivCriteria,''),
	       COALESCE(m.ResultCriteria,''), COALESCE(m.ResultValidUntil,''), COALESCE(m.ResultNotes,''), COALESCE(m.Recommendation,''), COALESCE(m.AdditionalNotes,''), COALESCE(m.CheckUpStatus,''),
	       COALESCE(parent.CheckUpID,''), CASE WHEN m.HealthCertificateFileName IS NOT NULL THEN 'Yes' ELSE 'No' END,
	       COALESCE(m.HcValidityStatus,''), m.IsTerminal
	FROM McuRecord m
	JOIN Patient p ON p.PatientID = m.FK_PatientID
	LEFT JOIN NationalityTable n ON n.NationalityID = p.FK_NationalityID
	LEFT JOIN Company co ON co.CompanyID = COALESCE(m.FK_CompanyID, p.FK_CompanyID)
	LEFT JOIN CheckUpCriteriaTable c ON c.CheckUpCriteriaID = m.FK_CheckUpCriteriaID
	LEFT JOIN McuRecord parent ON parent.McuRecordID = m.FK_ParentRecordID
	ORDER BY m.ReviewDate, m.CheckUpID`

var checkupKeys = []string{
	"patientId", "checkUpId", "patientName", "dateOfBirth", "gender", "nationality", "bloodType", "employeeId",
	"personalId", "company", "workPosition", "checkUpCriteria", "taskCategory",
	"reviewDate", "dateCheckUp", "reviewer", "age", "heightCm", "weightKg",
	"isShortProject", "validityPeriodMonths",
	"bmiValue", "bmiCriteria", "bmiNotes", "smoking", "otherPhysicalExamNotes",
	"bpCriteria", "bpNotes", "visionCriteria", "visionNotes",
	"cbcCriteria", "cbcNotes", "lipidCriteria", "lipidNotes",
	"glucoseCriteria", "glucoseNotes", "liverCriteria", "liverNotes",
	"kidneyCriteria", "kidneyNotes", "urinalysisCriteria", "urinalysisNotes",
	"radiologyResult", "radiologyNotes",
	"echocardiographyCriteria", "echocardiographyNotes", "ctScanCriteria", "ctScanNotes",
	"usgAbdomenCriteria", "usgAbdomenNotes",
	"ecgResult", "treadmillResult", "ecgTreadmillNotes",
	"herniaHemorrhoidCriteria", "herniaHemorrhoidNotes", "spirometryCriteria", "spirometryNotes",
	"audiometryCriteria", "audiometryNotes", "hepBCriteria", "hepBNotes", "hepACriteria", "hepANotes",
	"rectalSwabCriteria", "rectalSwabNotes", "hivCriteria",
	"resultCriteria", "resultValidUntil", "resultNotes", "recommendation", "additionalNotes", "checkUpStatus",
	"parentCheckUpId", "hasCertificate", "hcValidityStatus", "isTerminal",
}

func scanCheckups(rows *sql.Rows) ([]map[string]any, error) {
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		vals := make([]any, len(checkupKeys))
		dest := make([]any, len(checkupKeys))
		for i := range vals {
			dest[i] = &vals[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(checkupKeys)+3)
		for i, key := range checkupKeys {
			row[key] = jsonCell(vals[i])
		}
		row["parent"] = ""
		row["project"] = ""
		row["depth"] = ""
		out = append(out, row)
	}
	return out, rows.Err()
}

func jsonCell(v any) any {
	switch t := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(t)
	case time.Time:
		return t.Format("2006-01-02")
	default:
		return t
	}
}

type relationLabel struct {
	parent  string
	project string
	depth   string
}

func (a *API) relationLabels() (map[string]relationLabel, error) {
	rows, err := a.DB.Query(`
		SELECT c.Name, COALESCE(parent.Name,''), COALESCE(cr.Project,''), cr.Depth
		FROM CompanyRelation cr
		JOIN Company c ON c.CompanyID = cr.FK_CompanyID
		LEFT JOIN Company parent ON parent.CompanyID = cr.FK_ParentID
		ORDER BY c.Name, COALESCE(cr.Project,'')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	grouped := map[string][]relationLabel{}
	for rows.Next() {
		var name, parent, project string
		var depth sql.NullInt64
		if err := rows.Scan(&name, &parent, &project, &depth); err != nil {
			return nil, err
		}
		depthText := ""
		if depth.Valid {
			depthText = itoa(depth.Int64)
		}
		grouped[name] = append(grouped[name], relationLabel{parent: parent, project: project, depth: depthText})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]relationLabel, len(grouped))
	for name, list := range grouped {
		parents := make([]string, len(list))
		projects := make([]string, len(list))
		depths := make([]string, len(list))
		for i, item := range list {
			parents[i] = item.parent
			projects[i] = item.project
			depths[i] = item.depth
		}
		out[name] = relationLabel{
			parent:  joinSlash(parents),
			project: joinSlash(projects),
			depth:   joinSlash(depths),
		}
	}
	return out, nil
}

func joinSlash(parts []string) string {
	out := ""
	for i, part := range parts {
		if i > 0 {
			out += " / "
		}
		out += part
	}
	return out
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
