package sofi

import (
	"encoding/json"
	"fmt"
	"log"
)

// maxPastResults caps the "earlier results" list on /sofi/start. Someone would have to take the
// questionnaire every week for years to reach it; the cap only keeps the page small.
const maxPastResults = 20

// PastResult is one earlier questionnaire as its owner sees it on /sofi/start.
type PastResult struct {
	SubmittedAt string // GMT+9, yyyy-MM-dd HH:mm
	Dimensions  []DimensionResult
	Total       int
	TotalMax    int
	Level       string
}

// pastResults lists the person's own saved questionnaires, newest first. The person comes from the
// sign-in session, never from the request, so nobody can ask for someone else's. Scores are worked
// out again from the stored answers (as StaffList does), so a change to the criteria in scoring.go
// applies to old results too. A row that cannot be read is logged and skipped, not shown as zero.
func (a *API) pastResults(patientID int64) ([]PastResult, error) {
	rows, err := a.DB.Query(`
		SELECT SubmittedAt, Answers
		FROM SofiScreening
		WHERE FK_PatientID = ?
		ORDER BY SubmittedAt DESC, SofiID DESC
		LIMIT ?`, patientID, maxPastResults)
	if err != nil {
		return nil, fmt.Errorf("list past results: %w", err)
	}
	defer rows.Close()
	out := []PastResult{}
	for rows.Next() {
		var at, raw string
		if err := rows.Scan(&at, &raw); err != nil {
			return nil, fmt.Errorf("scan past result: %w", err)
		}
		var answers []int
		if err := json.Unmarshal([]byte(raw), &answers); err != nil {
			log.Printf("sofi: past result for patient %d has unreadable answers: %v", patientID, err)
			continue
		}
		res, err := Score(answers)
		if err != nil {
			log.Printf("sofi: past result for patient %d: %v", patientID, err)
			continue
		}
		out = append(out, PastResult{SubmittedAt: siteFromDB(at), Dimensions: res.Dimensions, Total: res.Total, TotalMax: res.TotalMax, Level: res.Level})
	}
	return out, rows.Err()
}
