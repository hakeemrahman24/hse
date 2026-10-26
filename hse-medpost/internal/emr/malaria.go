package emr

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"hse-medpost/internal/dates"
	"hse-medpost/internal/middleware"
)

const (
	maxMalariaBodyBytes = 1 << 14
	maxMalariaRows      = 500
	maxWeightKg         = 150.0
	categoryScreening   = "Screening"
	defaultPurposeID    = 1 // On arrival
)

var (
	examTypes     = map[string]bool{"RDT": true, "BloodSmear": true}
	malariaResult = map[string]bool{"Negative": true, "Pv": true, "Pf": true, "Po": true, "Mixed": true}
)

// API serves the Medical Record page's clinical records.
type API struct {
	DB  *sql.DB
	Now func() time.Time // nil means time.Now; tests override it
}

func (a *API) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// today is the clinic's calendar day for a.now().
func (a *API) today() string {
	return a.now().In(dates.ClinicLocation).Format("2006-01-02")
}

// invalidInput is a mistake in the request, shown to the user as it is.
type invalidInput string

func (e invalidInput) Error() string { return string(e) }

// MalariaInput is what the form sends. Age, company, position and the region
// names are deliberately not here: the server looks them up, so a client cannot
// write a different value.
type MalariaInput struct {
	PatientID       int64   `json:"patientId"`
	PurposeID       int     `json:"purposeId"` // 0 or missing means 1, On arrival
	TestDate        string  `json:"testDate"`
	WeightKg        float64 `json:"weightKg"`
	HeadOfHousehold string  `json:"headOfHousehold"`
	HeadIsPatient   bool    `json:"headIsPatient"`
	RegencyCode     string  `json:"regencyCode"`
	DistrictCode    string  `json:"districtCode"`
	VillageCode     string  `json:"villageCode"`
	ExamType        string  `json:"examType"`
	Result          *string `json:"result"`
}

type malariaRow struct {
	MalariaID       int64   `json:"malariaId"`
	VisitID         int64   `json:"visitId"`
	PurposeID       int     `json:"purposeId"`
	Purpose         string  `json:"purpose"`
	TestID          string  `json:"testId"`
	TestDate        string  `json:"testDate"`
	Week            string  `json:"week"`
	SlideInitial    string  `json:"slideInitial"`
	PatientName     string  `json:"patientName"`
	AgeYears        *int    `json:"ageYears"`
	WeightKg        float64 `json:"weightKg"`
	HeadOfHousehold string  `json:"headOfHousehold"`
	HeadIsPatient   bool    `json:"headIsPatient"`
	Regency         string  `json:"regency"`
	District        string  `json:"district"`
	Village         string  `json:"village"`
	WorkPosition    string  `json:"workPosition"`
	Company         string  `json:"company"`
	Examiner        string  `json:"examiner"`
	ExamType        string  `json:"examType"`
	Result          *string `json:"result"`
	// The codes and the time of recording are not shown in the table; the Excel
	// export carries them.
	RegencyCode  string `json:"regencyCode"`
	DistrictCode string `json:"districtCode"`
	VillageCode  string `json:"villageCode"`
	RecordedAt   string `json:"recordedAt"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	var bad invalidInput
	switch {
	case errors.As(err, &bad):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": bad.Error()})
	case errors.Is(err, sql.ErrNoRows):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	default:
		log.Printf("emr: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
	}
}

// Create saves a Screening visit and its malaria part in one transaction.
func (a *API) Create(w http.ResponseWriter, r *http.Request) {
	sess := middleware.Session(r)
	if sess == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "sign in required"})
		return
	}
	var in MalariaInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxMalariaBodyBytes)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	created, err := a.createMalaria(in, sess.StaffID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (a *API) createMalaria(in MalariaInput, staffID int64) (malariaRow, error) {
	testDate, err := dates.Require(in.TestDate)
	if err != nil {
		return malariaRow{}, invalidInput("test date: " + err.Error())
	}
	if testDate > a.today() {
		return malariaRow{}, invalidInput("test date cannot be in the future")
	}
	weight := math.Round(in.WeightKg*10) / 10
	if !(weight > 0 && weight <= maxWeightKg) {
		return malariaRow{}, invalidInput("weight must be more than 0 and at most 150 kg")
	}
	if in.PurposeID == 0 {
		in.PurposeID = defaultPurposeID
	}
	var purposeFound int
	if err := a.DB.QueryRow(`SELECT PurposeID FROM MalariaPurposeTable WHERE PurposeID = ?`, in.PurposeID).Scan(&purposeFound); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return malariaRow{}, invalidInput("purposeId must be 1 (On arrival) or 2 (Symptomatic)")
		}
		return malariaRow{}, err
	}
	if !examTypes[in.ExamType] {
		return malariaRow{}, invalidInput("exam type must be RDT or BloodSmear")
	}
	if in.Result != nil && !malariaResult[*in.Result] {
		return malariaRow{}, invalidInput("result must be Negative, Pv, Pf, Po or Mixed")
	}

	tx, err := a.DB.Begin()
	if err != nil {
		return malariaRow{}, err
	}
	defer tx.Rollback()

	var name string
	var dob, position, company sql.NullString
	var companyID sql.NullInt64
	err = tx.QueryRow(`
		SELECT p.Name, p.DateOfBirth, p.WorkPosition, c.Name, p.FK_CompanyID
		FROM Patient p LEFT JOIN Company c ON c.CompanyID = p.FK_CompanyID
		WHERE p.PatientID = ?`, in.PatientID).Scan(&name, &dob, &position, &company, &companyID)
	if errors.Is(err, sql.ErrNoRows) {
		return malariaRow{}, invalidInput("patient not found")
	}
	if err != nil {
		return malariaRow{}, err
	}

	head := CleanHeadOfHousehold(in.HeadOfHousehold)
	if in.HeadIsPatient {
		head = CleanHeadOfHousehold(name)
	}
	if head == "" {
		return malariaRow{}, invalidInput("head of household is required (letters only), or choose Sama dengan pasien")
	}
	if len([]rune(head)) > maxHeadOfHouseholdLength {
		return malariaRow{}, invalidInput("head of household is too long")
	}

	regency, district, village, err := lookupRegions(tx, in.RegencyCode, in.DistrictCode, in.VillageCode)
	if err != nil {
		return malariaRow{}, err
	}

	var age *int
	if years, ok := AgeAt(dob.String, testDate); ok {
		age = &years
	}
	initial, err := pickSlideInitial(tx, testDate, in.PatientID, name)
	if err != nil {
		return malariaRow{}, err
	}

	nowStamp := dates.Stamp(a.now())
	res, err := tx.Exec(`
		INSERT INTO Visit (FK_PatientID, FK_CompanyID, FK_StaffID, VisitCategory, VisitAt, ReportDate, Status, CreatedAt)
		VALUES (?, ?, ?, ?, ?, ?, 'Open', ?)`,
		in.PatientID, companyID, staffID, categoryScreening, nowStamp, a.today(), nowStamp)
	if err != nil {
		return malariaRow{}, err
	}
	visitID, err := res.LastInsertId()
	if err != nil {
		return malariaRow{}, err
	}
	saved, err := tx.Exec(`
		INSERT INTO MalariaScreening (FK_VisitID, FK_PurposeID, TestDate, SlideInitial, AgeYears, WeightKg, HeadOfHousehold, HeadIsPatient,
			RegencyCode, RegencyName, DistrictCode, DistrictName, VillageCode, VillageName,
			WorkPosition, Company, ExamType, Result)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		visitID, in.PurposeID, testDate, initial, age, weight, head, in.HeadIsPatient,
		regency.code, regency.name, district.code, district.name, village.code, village.name,
		position.String, company.String, in.ExamType, in.Result)
	if err != nil {
		return malariaRow{}, err
	}
	malariaID, err := saved.LastInsertId()
	if err != nil {
		return malariaRow{}, err
	}
	if err := tx.Commit(); err != nil {
		return malariaRow{}, err
	}
	return a.getRow(malariaID)
}

type area struct{ code, name string }

// lookupRegions checks that the district and village exist, are at the right
// level and form one chain (village inside district), and returns the three
// areas with names from the Region table, never from the client. The regency is
// the district's own parent; a regency code the client sends is only checked
// against it, so people can search by district alone.
func lookupRegions(tx *sql.Tx, regencyCode, districtCode, villageCode string) (regency, district, village area, err error) {
	find := func(code string, level int, label string) (area, string, error) {
		var a area
		var parentCode sql.NullString
		err := tx.QueryRow(`SELECT Code, Name, ParentCode FROM Region WHERE Code = ? AND Level = ?`, code, level).Scan(&a.code, &a.name, &parentCode)
		if errors.Is(err, sql.ErrNoRows) {
			return a, "", invalidInput(label + " not found")
		}
		return a, parentCode.String, err
	}
	var districtParent, villageParent string
	if district, districtParent, err = find(districtCode, 3, "district"); err != nil {
		return
	}
	if regencyCode != "" && regencyCode != districtParent {
		err = invalidInput("district does not belong to the chosen regency")
		return
	}
	if regency, _, err = find(districtParent, 2, "regency"); err != nil {
		return
	}
	if village, villageParent, err = find(villageCode, 4, "village"); err != nil {
		return
	}
	if villageParent != district.code {
		err = invalidInput("village does not belong to the chosen district")
	}
	return
}

// querier is what pickSlideInitial needs; both *sql.DB (the read-only preview)
// and *sql.Tx (saving, so the choice and the insert are one transaction) fit.
type querier interface {
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}

// pickSlideInitial returns the code for the blood slide. A patient who already
// has a screening on this day keeps that day's code; otherwise the first
// candidate made from the name that no one else has used that day.
func pickSlideInitial(tx querier, testDate string, patientID int64, name string) (string, error) {
	var same sql.NullString
	err := tx.QueryRow(`
		SELECT m.SlideInitial FROM MalariaScreening m JOIN Visit v ON v.VisitID = m.FK_VisitID
		WHERE m.TestDate = ? AND v.FK_PatientID = ? ORDER BY m.MalariaID LIMIT 1`, testDate, patientID).Scan(&same)
	if err == nil && same.Valid {
		return same.String, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}

	taken := map[string]bool{}
	rows, err := tx.Query(`SELECT DISTINCT SlideInitial FROM MalariaScreening WHERE TestDate = ?`, testDate)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return "", err
		}
		taken[s] = true
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	for _, c := range SlideInitialCandidates(name) {
		if !taken[c] {
			return c, nil
		}
	}
	return "", errors.New("no free slide initial left for " + testDate)
}

const malariaSelect = `
	SELECT m.MalariaID, m.FK_VisitID, m.FK_PurposeID, pu.PurposeName, m.TestDate, m.SlideInitial, p.Name, m.AgeYears, m.WeightKg, m.HeadOfHousehold, m.HeadIsPatient,
	       m.RegencyName, m.DistrictName, m.VillageName, m.WorkPosition, m.Company,
	       s.StaffName, m.ExamType, m.Result,
	       m.RegencyCode, m.DistrictCode, m.VillageCode, v.VisitAt
	FROM MalariaScreening m
	JOIN Visit v ON v.VisitID = m.FK_VisitID
	JOIN MalariaPurposeTable pu ON pu.PurposeID = m.FK_PurposeID
	JOIN Patient p ON p.PatientID = v.FK_PatientID
	JOIN MedpostStaffTable s ON s.StaffID = v.FK_StaffID`

func scanRow(sc interface{ Scan(...any) error }) (malariaRow, error) {
	var row malariaRow
	var age sql.NullInt64
	var result sql.NullString
	var head int
	err := sc.Scan(&row.MalariaID, &row.VisitID, &row.PurposeID, &row.Purpose, &row.TestDate, &row.SlideInitial, &row.PatientName, &age, &row.WeightKg, &row.HeadOfHousehold, &head,
		&row.Regency, &row.District, &row.Village, &row.WorkPosition, &row.Company,
		&row.Examiner, &row.ExamType, &result,
		&row.RegencyCode, &row.DistrictCode, &row.VillageCode, &row.RecordedAt)
	if err != nil {
		return row, err
	}
	if age.Valid {
		v := int(age.Int64)
		row.AgeYears = &v
	}
	if result.Valid {
		row.Result = &result.String
	}
	row.HeadIsPatient = head != 0
	row.TestID = TestID(row.MalariaID)
	row.Week = ISOWeek(row.TestDate)
	return row, nil
}

func (a *API) getRow(malariaID int64) (malariaRow, error) {
	return scanRow(a.DB.QueryRow(malariaSelect+` WHERE m.MalariaID = ?`, malariaID))
}

// List returns malaria screenings, newest test date first, optionally between
// ?from= and ?to= (YYYY-MM-DD, inclusive) and only for patients whose name
// contains every word of ?q=.
func (a *API) List(w http.ResponseWriter, r *http.Request) {
	where, args, err := filters(r)
	if err != nil {
		writeError(w, err)
		return
	}
	query := malariaSelect + ` WHERE 1=1` + where
	query += ` ORDER BY m.TestDate DESC, m.MalariaID DESC LIMIT ` + strconv.Itoa(maxMalariaRows)
	rows, err := a.DB.Query(query, args...)
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()
	out := []malariaRow{}
	for rows.Next() {
		row, err := scanRow(rows)
		if err != nil {
			writeError(w, err)
			return
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// SetResult records the smear result once it is known (or corrects it).
func (a *API) SetResult(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, invalidInput("invalid id"))
		return
	}
	var body struct {
		Result string `json:"result"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxMalariaBodyBytes)).Decode(&body); err != nil || !malariaResult[body.Result] {
		writeError(w, invalidInput("result must be Negative, Pv, Pf, Po or Mixed"))
		return
	}
	res, err := a.DB.Exec(`UPDATE MalariaScreening SET Result = ? WHERE MalariaID = ?`, body.Result, id)
	if err != nil {
		writeError(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, sql.ErrNoRows)
		return
	}
	updated, err := a.getRow(id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// PatientContext gives the form what it fills in by itself once a patient is
// chosen: age on the test date, and company and position from the latest
// checkup (Patient carries them, kept in step with the latest checkup).
func (a *API) PatientContext(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("patientId"), 10, 64)
	if err != nil {
		writeError(w, invalidInput("invalid patient"))
		return
	}
	testDate := strings.TrimSpace(r.URL.Query().Get("testDate"))
	if testDate == "" {
		testDate = a.today()
	}
	if testDate, err = dates.Require(testDate); err != nil {
		writeError(w, invalidInput("test date: "+err.Error()))
		return
	}
	var name string
	var dob, position, company sql.NullString
	err = a.DB.QueryRow(`
		SELECT p.Name, p.DateOfBirth, p.WorkPosition, c.Name
		FROM Patient p LEFT JOIN Company c ON c.CompanyID = p.FK_CompanyID
		WHERE p.PatientID = ?`, id).Scan(&name, &dob, &position, &company)
	if err != nil {
		writeError(w, err)
		return
	}
	var age *int
	if years, ok := AgeAt(dob.String, testDate); ok {
		age = &years
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name": name, "ageYears": age, "company": company.String, "workPosition": position.String,
	})
}

// SlideInitialPreview answers GET .../slide-initial?patientId=&testDate= with the
// code that saving would give this patient on that day, so the form can show it
// before the slide is written. It writes nothing. The code is final only when
// the record is saved (someone else may take it first); Create returns the real
// one.
func (a *API) SlideInitialPreview(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("patientId"), 10, 64)
	if err != nil {
		writeError(w, invalidInput("invalid patient"))
		return
	}
	testDate := strings.TrimSpace(r.URL.Query().Get("testDate"))
	if testDate == "" {
		testDate = a.today()
	}
	if testDate, err = dates.Require(testDate); err != nil {
		writeError(w, invalidInput("test date: "+err.Error()))
		return
	}
	var name string
	if err := a.DB.QueryRow(`SELECT Name FROM Patient WHERE PatientID = ?`, id).Scan(&name); err != nil {
		writeError(w, err)
		return
	}
	code, err := pickSlideInitial(a.DB, testDate, id, name)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"slideInitial": code})
}
