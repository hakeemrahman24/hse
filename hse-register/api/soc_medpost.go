package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// medpostDirectory is the real employeeDirectory. It calls hse-medpost's existing
// GET /api/directory/employees (built for entry permits) with the directory bearer token.
//
// Two things to know about that endpoint:
//   - It only returns people with a current Fit / Fit-with-note medical result, so only they can
//     sign in to the SOC form. Someone with an expired or missing certificate is "not found". An
//     employee ID is not needed: most patients have none (see ownerKey).
//   - Every answer carries the date of birth and medical notes. The struct below decodes only the
//     fields needed, so everything else is dropped as soon as the response is parsed and is never
//     stored or logged (plan section 7.2).
type medpostDirectory struct {
	baseURL string
	token   string
	client  *http.Client
}

func newMedpostDirectory(baseURL, token string) *medpostDirectory {
	return &medpostDirectory{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		client:  &http.Client{Timeout: 5 * time.Second},
	}
}

// medpostEmployee lists the only fields read from Medpost's answer.
type medpostEmployee struct {
	PatientID    int64  `json:"patientId"`
	EmployeeID   string `json:"employeeId"`
	Name         string `json:"name"`
	DateOfBirth  string `json:"dateOfBirth"`
	WorkPosition string `json:"workPosition"`
	Company      string `json:"company"`
}

// get calls the employees endpoint with the given query and decodes the list.
func (m *medpostDirectory) get(ctx context.Context, params url.Values) ([]medpostEmployee, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.baseURL+"/api/directory/employees?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+m.token)
	req.Header.Set("Accept", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("medpost request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// The body is not read into the error: it could contain details we must not log.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil, fmt.Errorf("medpost answered %d", resp.StatusCode)
	}
	var rows []medpostEmployee
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rows); err != nil {
		return nil, fmt.Errorf("medpost answer could not be read: %w", err)
	}
	return rows, nil
}

// Search finds employees by part of the name. Medpost returns up to 20 rows; at most max are kept.
// Key is the Medpost patient ID, which stays on the server.
func (m *medpostDirectory) Search(ctx context.Context, q string, max int) ([]nameMatch, error) {
	rows, err := m.get(ctx, url.Values{"q": {q}})
	if err != nil {
		return nil, err
	}
	// Count names first so two people with the identical name are flagged. The count covers this
	// result set only (Medpost does not return a global count), which is enough because identical
	// names always match the same search text.
	count := map[string]int{}
	for _, r := range rows {
		count[strings.ToLower(r.Name)]++
	}
	out := []nameMatch{}
	for _, r := range rows {
		out = append(out, nameMatch{
			Key: strconv.FormatInt(r.PatientID, 10), Name: r.Name, Company: r.Company,
			Duplicate: count[strings.ToLower(r.Name)] > 1,
		})
		if len(out) == max {
			break
		}
	}
	return out, nil
}

// ownerKey is what a SOC post's owner is stored as (observations.employee_id). Most Medpost
// patients have no employee ID, so the Medpost patient ID stands in for them, prefixed "P" so it
// can never equal a real employee ID.
func ownerKey(r medpostEmployee) string {
	if r.EmployeeID != "" {
		return r.EmployeeID
	}
	return "P" + strconv.FormatInt(r.PatientID, 10)
}

// Verify looks the person up by patient ID and compares the date of birth here, in memory.
//
// WHY here and not with Medpost's own dob= filter: that filter would put the typed date in the
// request URL, and URLs end up in proxy and access logs (this call goes through a public
// hostname). The stored date comes back inside an HTTPS answer body instead, is compared, and is
// dropped. This is a deliberate change from plan section 7.2, which wanted the comparison inside
// the API.
func (m *medpostDirectory) Verify(ctx context.Context, key string, dob time.Time) (*employee, error) {
	id, err := strconv.ParseInt(key, 10, 64)
	if err != nil || id <= 0 {
		return nil, nil
	}
	rows, err := m.get(ctx, url.Values{"patientId": {strconv.FormatInt(id, 10)}})
	if err != nil {
		return nil, err
	}
	want := dob.Format("2006-01-02")
	for _, r := range rows {
		// An empty stored date never matches, even against a zero time.
		if r.PatientID != id || r.DateOfBirth == "" || r.DateOfBirth != want {
			continue
		}
		return &employee{Key: ownerKey(r), Name: r.Name, Designation: r.WorkPosition, Company: r.Company}, nil
	}
	return nil, nil
}
