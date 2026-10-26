package sofi

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

const (
	// The weeks on site are chosen from a list: minWeeksOnSite to maxWeeksOnSite, or "more than
	// 1 year". That last answer is stored as the number WeeksOverYear in the same WeeksOnSite
	// column (a column of whole weeks has no other room for it), so 54 never means 54 weeks:
	// the staff list shows it as "> 1 year".
	minWeeksOnSite = 2
	maxWeeksOnSite = 53
	WeeksOverYear  = maxWeeksOnSite + 1
	// submitMax completed questionnaires an hour from one person; a retry after a slow network
	// is fine, a script is not.
	submitMax    = 5
	submitWindow = time.Hour

	noMcuStatus  = "No checkup result on record"
	listLimit    = 500
	timeLayoutDB = "2006-01-02T15:04:05"
)

// mcuStatusLabel turns the stored ResultCriteria into the words staff see.
var mcuStatusLabel = map[string]string{
	"Fit":            "Fit",
	"FitWithNote":    "Fit with note",
	"Unfit":          "Unfit",
	"TemporaryUnfit": "Temporary unfit",
	"NeedFollowUp":   "Need follow up",
}

// Profile is the identity block at the top of the form. All of it comes from the database except
// the weeks on site, which the person types.
type Profile struct {
	Name            string
	DateOfBirth     string // dd/mm/yyyy, as shown
	WorkPosition    string
	Company         string
	Age             int
	MCUReviewStatus string
}

// latestResult picks one column from a patient's newest checkup that already has a result. McuRecord
// is aliased m; the outer Patient must be aliased p.
const latestResult = `
	SELECT %s FROM McuRecord m
	WHERE m.FK_PatientID = p.PatientID AND COALESCE(m.ResultCriteria, '') <> ''
	ORDER BY COALESCE(m.DateCheckUp, '') DESC, m.McuRecordID DESC
	LIMIT 1`

func (a *API) profile(patientID int64) (Profile, error) {
	var p Profile
	var dob, criteria string
	err := a.DB.QueryRow(`
		SELECT p.Name, COALESCE(p.DateOfBirth, ''),
		       COALESCE(NULLIF(p.WorkPosition, ''), (`+fmt.Sprintf(latestResult, "m.WorkPosition")+`), ''),
		       COALESCE(c.Name, ''),
		       COALESCE((`+fmt.Sprintf(latestResult, "m.ResultCriteria")+`), '')
		FROM Patient p
		LEFT JOIN Company c ON c.CompanyID = p.FK_CompanyID
		WHERE p.PatientID = ?`, patientID).Scan(&p.Name, &dob, &p.WorkPosition, &p.Company, &criteria)
	if err != nil {
		return Profile{}, fmt.Errorf("read patient %d: %w", patientID, err)
	}
	p.Age, p.DateOfBirth = ageAndDisplay(dob, a.now())
	p.MCUReviewStatus = noMcuStatus
	if label, ok := mcuStatusLabel[criteria]; ok {
		p.MCUReviewStatus = label
	}
	return p, nil
}

// ageAndDisplay returns whole years and dd/mm/yyyy for a stored yyyy-MM-dd; zero and "" when the
// stored date cannot be read.
func ageAndDisplay(stored string, today time.Time) (int, string) {
	if len(stored) > 10 {
		stored = stored[:10]
	}
	born, err := time.Parse("2006-01-02", stored)
	if err != nil {
		return 0, ""
	}
	years := today.Year() - born.Year()
	if today.Month() < born.Month() || (today.Month() == born.Month() && today.Day() < born.Day()) {
		years--
	}
	if years < 0 {
		years = 0
	}
	return years, born.Format("02/01/2006")
}

// validWeeks accepts 2 to 53 weeks, or WeeksOverYear for "more than 1 year".
func validWeeks(n int) bool {
	return (n >= minWeeksOnSite && n <= maxWeeksOnSite) || n == WeeksOverYear
}

// weekChoices are the numbers offered in the list, in order.
var weekChoices = func() []int {
	out := make([]int, 0, maxWeeksOnSite-minWeeksOnSite+1)
	for n := minWeeksOnSite; n <= maxWeeksOnSite; n++ {
		out = append(out, n)
	}
	return out
}()

type submission struct {
	WeeksOnSite *int  `json:"weeksOnSite"`
	Answers     []int `json:"answers"`
}

// Submit scores a completed questionnaire and saves it. Only the weeks and the twenty answers
// are read from the browser: the person comes from the sign-in cookie, and every score and level
// is worked out here.
func (a *API) Submit(w http.ResponseWriter, r *http.Request) {
	s := a.session(r)
	if s == nil {
		writeError(w, r, http.StatusUnauthorized, "Please sign in first.")
		return
	}
	var in submission
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil ||
		in.WeeksOnSite == nil || !validWeeks(*in.WeeksOnSite) {
		writeError(w, r, http.StatusBadRequest, "Enter the weeks on site and answer every word.")
		return
	}
	res, err := Score(in.Answers)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "Enter the weeks on site and answer every word.")
		return
	}
	key := fmt.Sprintf("sub:%d", s.UserID)
	if a.lim.count(key, submitWindow) >= submitMax {
		writeError(w, r, http.StatusTooManyRequests, "Too many submissions, please try again later.")
		return
	}
	p, err := a.profile(s.UserID)
	if err != nil {
		log.Printf("sofi: submit profile: %v", err)
		writeError(w, r, http.StatusInternalServerError, "Something went wrong.")
		return
	}
	if err := a.save(s.UserID, p, *in.WeeksOnSite, in.Answers, res); err != nil {
		log.Printf("sofi: submit save: %v", err)
		writeError(w, r, http.StatusInternalServerError, "Something went wrong.")
		return
	}
	a.lim.add(key)
	writeJSON(w, http.StatusOK, res)
}

func (a *API) save(patientID int64, p Profile, weeks int, answers []int, res Result) error {
	return a.saveAt(patientID, p, weeks, answers, res, a.now().UTC().Format(timeLayoutDB))
}

// saveAt stores one questionnaire with the given submit time (UTC, YYYY-MM-DDTHH:MM:SS).
func (a *API) saveAt(patientID int64, p Profile, weeks int, answers []int, res Result, submittedAt string) error {
	raw, err := json.Marshal(answers)
	if err != nil {
		return fmt.Errorf("encode answers: %w", err)
	}
	var age any // NULL when the date of birth could not be read
	if p.Age > 0 {
		age = p.Age
	}
	score := func(i int) int { return res.Dimensions[i].Score }
	_, err = a.DB.Exec(`
		INSERT INTO SofiScreening
		  (FK_PatientID, SubmittedAt, WeeksOnSite, AgeYears, WorkPosition, Company, McuReviewStatus, Answers,
		   ScoreEnergy, ScoreExertion, ScoreDiscomfort, ScoreMotivation, ScoreSleepiness, ScoreTotal, ResultLevel)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		patientID, submittedAt, weeks, age, p.WorkPosition, p.Company, p.MCUReviewStatus, string(raw),
		score(0), score(1), score(2), score(3), score(4), res.Total, res.Level)
	if err != nil {
		return fmt.Errorf("insert SofiScreening: %w", err)
	}
	return nil
}

// Record is one saved questionnaire as staff see it.
type Record struct {
	ID              int64             `json:"id"`
	Name            string            `json:"name"`
	SubmittedAt     string            `json:"submittedAt"`
	WeeksOnSite     int               `json:"weeksOnSite"`
	WeeksOverYear   bool              `json:"weeksOverYear"` // the person chose "more than 1 year"; WeeksOnSite then holds the marker, not a number of weeks
	Age             int               `json:"age"`
	WorkPosition    string            `json:"workPosition"`
	Company         string            `json:"company"`
	MCUReviewStatus string            `json:"mcuReviewStatus"`
	Dimensions      []DimensionResult `json:"dimensions"`
	Total           int               `json:"total"`
	TotalMax        int               `json:"totalMax"`
	Level           string            `json:"level"`
}

// StaffList lists saved questionnaires for the EMR Fatigue > SOFI tab. It is registered behind the
// staff sign-in. Filters: q (part of the name), from and to (yyyy-MM-dd, by submit date).
// Scores and levels are worked out again from the stored answers, so a change to the criteria in
// scoring.go applies to old records too.
func (a *API) StaffList(w http.ResponseWriter, r *http.Request) {
	where := []string{"1 = 1"}
	var args []any
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		where = append(where, `LOWER(p.Name) LIKE ? ESCAPE '!'`)
		args = append(args, "%"+likeEscape(strings.ToLower(q))+"%")
	}
	if from := r.URL.Query().Get("from"); from != "" {
		where = append(where, `SUBSTR(s.SubmittedAt, 1, 10) >= ?`)
		args = append(args, from)
	}
	if to := r.URL.Query().Get("to"); to != "" {
		where = append(where, `SUBSTR(s.SubmittedAt, 1, 10) <= ?`)
		args = append(args, to)
	}
	args = append(args, listLimit)
	rows, err := a.DB.Query(`
		SELECT s.SofiID, p.Name, s.SubmittedAt, s.WeeksOnSite, COALESCE(s.AgeYears, 0), s.WorkPosition, s.Company,
		       s.McuReviewStatus, s.Answers
		FROM SofiScreening s
		JOIN Patient p ON p.PatientID = s.FK_PatientID
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY s.SubmittedAt DESC, s.SofiID DESC
		LIMIT ?`, args...)
	if err != nil {
		log.Printf("sofi: list: %v", err)
		writeError(w, r, http.StatusInternalServerError, "Could not read the SOFI records.")
		return
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		var rec Record
		var raw string
		if err := rows.Scan(&rec.ID, &rec.Name, &rec.SubmittedAt, &rec.WeeksOnSite, &rec.Age, &rec.WorkPosition,
			&rec.Company, &rec.MCUReviewStatus, &raw); err != nil {
			log.Printf("sofi: list scan: %v", err)
			writeError(w, r, http.StatusInternalServerError, "Could not read the SOFI records.")
			return
		}
		var answers []int
		if err := json.Unmarshal([]byte(raw), &answers); err != nil {
			log.Printf("sofi: record %d has unreadable answers: %v", rec.ID, err)
			continue
		}
		res, err := Score(answers)
		if err != nil {
			log.Printf("sofi: record %d: %v", rec.ID, err)
			continue
		}
		rec.Dimensions, rec.Total, rec.TotalMax, rec.Level = res.Dimensions, res.Total, res.TotalMax, res.Level
		rec.WeeksOverYear = rec.WeeksOnSite == WeeksOverYear
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		log.Printf("sofi: list rows: %v", err)
		writeError(w, r, http.StatusInternalServerError, "Could not read the SOFI records.")
		return
	}
	writeJSON(w, http.StatusOK, out)
}
