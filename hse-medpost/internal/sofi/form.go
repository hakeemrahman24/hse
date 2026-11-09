package sofi

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
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
	// The window the person signed in under must not have been ended by an admin (Close now /
	// Cancel). A window that merely ran out of time still counts: nobody is cut off mid-form.
	winID, err := a.sessionWindowID(s)
	if err != nil {
		log.Printf("sofi: submit window: %v", err)
		writeError(w, r, http.StatusInternalServerError, "Something went wrong.")
		return
	}
	if winID == 0 {
		writeError(w, r, http.StatusForbidden, msgClosed)
		return
	}
	var in submission
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil ||
		in.WeeksOnSite == nil || !validWeeks(*in.WeeksOnSite) {
		writeError(w, r, http.StatusBadRequest, "Enter the weeks on site and answer every item.")
		return
	}
	res, err := Score(in.Answers)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "Enter the weeks on site and answer every item.")
		return
	}
	res = named(res, a.dimensions()) // the result page shows the dimension names as set in Settings
	// Preview: score it and show it, save nothing and take no lock.
	if winID == previewWindowID {
		writeJSON(w, http.StatusOK, struct {
			Result
			Preview bool `json:"preview"`
		}{res, true})
		return
	}
	p, err := a.profile(s.UserID)
	if err != nil {
		log.Printf("sofi: submit profile: %v", err)
		writeError(w, r, http.StatusInternalServerError, "Something went wrong.")
		return
	}
	// One questionnaire per person per period. The check and the insert run under one lock, so two
	// taps (or two tabs) arriving together cannot both pass the check; one server process serves
	// the site, so a lock in memory is enough.
	a.submitMu.Lock()
	defer a.submitMu.Unlock()
	done, err := a.hasSubmitted(s.UserID, winID)
	if err != nil {
		log.Printf("sofi: submit check: %v", err)
		writeError(w, r, http.StatusInternalServerError, "Something went wrong.")
		return
	}
	if done {
		writeError(w, r, http.StatusConflict, msgAlreadyDone)
		return
	}
	if err := a.save(s.UserID, p, *in.WeeksOnSite, in.Answers, res, winID); err != nil {
		if isDuplicateKey(err) { // another server process saved it between the check and the insert
			writeError(w, r, http.StatusConflict, msgAlreadyDone)
			return
		}
		log.Printf("sofi: submit save: %v", err)
		writeError(w, r, http.StatusInternalServerError, "Something went wrong.")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *API) save(patientID int64, p Profile, weeks int, answers []int, res Result, windowID int64) error {
	return a.saveAt(patientID, p, weeks, answers, res, a.now().UTC().Format(timeLayoutDB), windowID)
}

// saveAt stores one questionnaire with the given submit time (UTC, YYYY-MM-DDTHH:MM:SS) and the
// window it was submitted in (0 stores NULL, "before windows existed").
func (a *API) saveAt(patientID int64, p Profile, weeks int, answers []int, res Result, submittedAt string, windowID int64) error {
	raw, err := json.Marshal(answers)
	if err != nil {
		return fmt.Errorf("encode answers: %w", err)
	}
	var age any // NULL when the date of birth could not be read
	if p.Age > 0 {
		age = p.Age
	}
	var win any
	if windowID > 0 {
		win = windowID
	}
	score := func(i int) int { return res.Dimensions[i].Score }
	_, err = a.DB.Exec(`
		INSERT INTO SofiScreening
		  (FK_PatientID, SubmittedAt, WeeksOnSite, AgeYears, WorkPosition, Company, McuReviewStatus, Answers,
		   ScoreEnergy, ScoreExertion, ScoreDiscomfort, ScoreMotivation, ScoreSleepiness, ScoreTotal, ResultLevel, FK_WindowID)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		patientID, submittedAt, weeks, age, p.WorkPosition, p.Company, p.MCUReviewStatus, string(raw),
		score(0), score(1), score(2), score(3), score(4), res.Total, res.Level, win)
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
	out, total, qerr := a.staffRecords(r, listLimit)
	if qerr != nil {
		writeError(w, r, qerr.status, qerr.msg)
		return
	}
	// The table shows at most listLimit rows; the total is sent in a header so the page can say
	// "showing the first 500 of N" instead of silently disagreeing with the dashboard's count.
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	writeJSON(w, http.StatusOK, out)
}

// recordsError is a failed list query: the HTTP status and the message to show.
type recordsError struct {
	status int
	msg    string
}

// staffRecords runs the query behind the SOFI table and its Excel export, so both follow the
// same filters (q, from, to, window) and the same scoring. It returns at most limit rows, newest
// first, and how many rows matched in all. Details of a failure are logged here, not shown.
func (a *API) staffRecords(r *http.Request, limit int) ([]Record, int, *recordsError) {
	readFailed := &recordsError{http.StatusInternalServerError, "Could not read the SOFI records."}
	where := []string{"1 = 1"}
	var args []any
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		where = append(where, `LOWER(p.Name) LIKE ? ESCAPE '!'`)
		args = append(args, "%"+likeEscape(strings.ToLower(q))+"%")
	}
	dayWhere, dayArgs, derr := siteDayFilter(r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if derr != nil {
		return nil, 0, &recordsError{http.StatusBadRequest, derr.Error()}
	}
	where, args = append(where, dayWhere...), append(args, dayArgs...)
	clause, wargs, werr := windowFilter(r.URL.Query().Get("window"))
	if werr != nil {
		return nil, 0, &recordsError{http.StatusBadRequest, werr.Error()}
	}
	if clause != "" {
		where, args = append(where, clause), append(args, wargs...)
	}
	var total int
	if err := a.DB.QueryRow(`
		SELECT COUNT(*) FROM SofiScreening s JOIN Patient p ON p.PatientID = s.FK_PatientID
		WHERE `+strings.Join(where, " AND "), args...).Scan(&total); err != nil {
		log.Printf("sofi: list count: %v", err)
		return nil, 0, readFailed
	}
	args = append(args, limit)
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
		return nil, 0, readFailed
	}
	defer rows.Close()
	dims := a.dimensions()
	out := []Record{}
	for rows.Next() {
		var rec Record
		var raw string
		if err := rows.Scan(&rec.ID, &rec.Name, &rec.SubmittedAt, &rec.WeeksOnSite, &rec.Age, &rec.WorkPosition,
			&rec.Company, &rec.MCUReviewStatus, &raw); err != nil {
			log.Printf("sofi: list scan: %v", err)
			return nil, 0, readFailed
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
		res = named(res, dims)
		rec.Dimensions, rec.Total, rec.TotalMax, rec.Level = res.Dimensions, res.Total, res.TotalMax, res.Level
		rec.WeeksOverYear = rec.WeeksOnSite == WeeksOverYear
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		log.Printf("sofi: list rows: %v", err)
		return nil, 0, readFailed
	}
	return out, total, nil
}

// hasSubmitted tells whether the person already sent a questionnaire in this window.
func (a *API) hasSubmitted(patientID, windowID int64) (bool, error) {
	var n int
	err := a.DB.QueryRow(`SELECT COUNT(*) FROM SofiScreening WHERE FK_PatientID = ? AND FK_WindowID = ?`, patientID, windowID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("count submissions: %w", err)
	}
	return n > 0, nil
}
