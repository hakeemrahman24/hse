// Package worker finds a worker's identity in hse-medpost's employee directory.
//
// WHY here and not a copy: worker identity, company and position are owned by
// hse-medpost (plan 4.1). entry-permit asks over loopback HTTP each time and
// keeps no copy, so a correction made in medpost is seen at once.
//
// Only eligible people are visible. medpost's employee route returns a worker
// only when their latest decided checkup is Fit or Fit with note and its
// valid-until date has not passed (see employeeQuery in hse-medpost). So an
// unfit, expired or still-open record is neither listed in the name search nor
// found by the date-of-birth check; this package adds no filter of its own and
// must keep calling that route, not the all-checkups one.
//
// Two public questions are answered: "which names contain this?" (the
// searchable dropdown, at least MinQuery characters so the list cannot be
// dumped) and "is there exactly one person with this name and date of
// birth?" (the proof a worker gives before seeing their own page).
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"entry-permit/internal/dates"
)

const (
	// MinQuery is the fewest characters a name search accepts.
	MinQuery = 4
	// MaxNames caps one search answer.
	MaxNames = 10

	httpTimeout  = 10 * time.Second
	maxBodyBytes = 4 << 20
	oldestBirth  = 1900
)

// ErrBadDOB is shown to the worker when the date does not fit dd/mm/yyyy.
var ErrBadDOB = errors.New("Enter your date of birth as dd/mm/yyyy, for example 05/03/1988.")

var dobRe = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})/(\d{4})$`)

// ParseDOB turns what the worker typed (dd/mm/yyyy) into yyyy-mm-dd. today is
// yyyy-mm-dd and lets the check refuse a date in the future.
func ParseDOB(s, today string) (string, error) {
	m := dobRe.FindStringSubmatch(dates.CleanTyped(s))
	if m == nil {
		return "", ErrBadDOB
	}
	var d, mo, y int
	if _, err := fmt.Sscanf(m[1]+" "+m[2]+" "+m[3], "%d %d %d", &d, &mo, &y); err != nil {
		return "", ErrBadDOB
	}
	t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
	// time.Date rolls 31 April over to 1 May: a mismatch means the day is not real.
	if t.Day() != d || int(t.Month()) != mo || t.Year() != y || y < oldestBirth {
		return "", ErrBadDOB
	}
	iso := t.Format("2006-01-02")
	if iso > today {
		return "", ErrBadDOB
	}
	return iso, nil
}

// Person is what a worker sees about themselves.
type Person struct {
	Name, DOB, Company, Position, McuValidUntil string
	McuResult                                   string // Fit or Fit with note
	McuNote                                     string
}

// Parent is one company above the worker's company, with its depth.
type Parent struct {
	Name  string `json:"name"`
	Depth int    `json:"depth"`
}

// Employee is the full medpost record the permit form needs. It is read-only
// here: a wrong company or position is corrected in medpost, never retyped.
type Employee struct {
	PatientID        int64
	PersonalID       string // NIK or passport; empty until medpost adds it to this route
	Name, Gender     string
	DOB, Nationality string
	Company, Entity  string
	Parents          []Parent
	Position         string
	CheckUpID        string
	McuResult        string // Fit or Fit with note (the route returns nobody else)
	McuValidUntil    string
	McuNote          string // the doctor's note on a "Fit with note" result; blank otherwise
}

// IsForeign: only nationality "indonesia" is WNI, everyone else is foreign
// (business-rules-tickets.md section 0). A blank nationality counts as foreign
// so a missing value never skips the foreign-document checks.
func (e Employee) IsForeign() bool { return norm(e.Nationality) != "indonesia" }

// Source answers the public questions. Defined here, where it is used.
type Source interface {
	Names(ctx context.Context, q string) ([]string, error)
	// Pick returns the one worker with exactly this name and date of birth.
	Pick(ctx context.Context, name, dobISO string) (Employee, bool, error)
	// PickIncludingExpired is Pick, but also returns a worker whose medical checkup has run out,
	// so the "My Layar Entry Permit" page can tell them. Nothing else may use it: a permit must
	// never be requested for a worker with no valid checkup.
	PickIncludingExpired(ctx context.Context, name, dobISO string) (Employee, bool, error)
	// Eligible reports whether exactly this full name is an eligible worker
	// (used to check a name picked for a gate officer).
	Eligible(ctx context.Context, name string) (bool, error)
}

// HTTPSource reads hse-medpost's token-guarded employee route.
type HTTPSource struct {
	BaseURL, Token string
	Client         *http.Client // nil uses a client with a 10 second timeout
}

type employee struct {
	PatientID        int64    `json:"patientId"`
	PersonalID       string   `json:"personalId"`
	Name             string   `json:"name"`
	Gender           string   `json:"gender"`
	DateOfBirth      string   `json:"dateOfBirth"`
	Nationality      string   `json:"nationality"`
	Company          string   `json:"company"`
	CompanyEntity    string   `json:"companyEntity"`
	Parents          []Parent `json:"parents"`
	WorkPosition     string   `json:"workPosition"`
	CheckUpID        string   `json:"checkUpId"`
	ResultCriteria   string   `json:"resultCriteria"`
	ResultValidUntil string   `json:"resultValidUntil"`
	ResultNotes      string   `json:"resultNotes"`
}

// norm makes names comparable: lower case, single spaces.
func norm(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

func (s HTTPSource) fetch(ctx context.Context, q, dob string, includeExpired bool) ([]employee, error) {
	if s.BaseURL == "" || s.Token == "" {
		return nil, errors.New("medpost employee API is not configured (MEDPOST_API_URL / MEDPOST_API_TOKEN)")
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: httpTimeout}
	}
	v := url.Values{"q": {q}}
	if dob != "" {
		v.Set("dob", dob)
	}
	if includeExpired {
		v.Set("includeExpired", "1")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.BaseURL, "/")+"/api/directory/employees?"+v.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build employee request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.Token) // sent only to BaseURL, never logged
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("medpost employee API unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("medpost employee API answered %d", resp.StatusCode)
	}
	var rows []employee
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&rows); err != nil {
		return nil, fmt.Errorf("medpost employee API sent bad data: %w", err)
	}
	return rows, nil
}

// Names returns up to MaxNames distinct names containing q (case-insensitive).
// A query shorter than MinQuery returns nothing and never reaches medpost.
func (s HTTPSource) Names(ctx context.Context, q string) ([]string, error) {
	q = strings.Join(strings.Fields(q), " ")
	if len([]rune(q)) < MinQuery {
		return nil, nil
	}
	rows, err := s.fetch(ctx, q, "", false)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range rows {
		name := strings.Join(strings.Fields(r.Name), " ")
		if name == "" || seen[norm(name)] {
			continue
		}
		seen[norm(name)] = true
		out = append(out, name)
	}
	sort.Strings(out)
	if len(out) > MaxNames {
		out = out[:MaxNames]
	}
	return out, nil
}

// Pick returns the worker whose full name and date of birth both match
// exactly. medpost does a partial name match, so the exact check is repeated
// here. If two records match (it should not happen), none is returned: a
// permit must never be attached to a guessed person.
func (s HTTPSource) Pick(ctx context.Context, name, dobISO string) (Employee, bool, error) {
	return s.pick(ctx, name, dobISO, false)
}

// PickIncludingExpired is Pick that also finds a worker whose checkup has expired.
func (s HTTPSource) PickIncludingExpired(ctx context.Context, name, dobISO string) (Employee, bool, error) {
	return s.pick(ctx, name, dobISO, true)
}

func (s HTTPSource) pick(ctx context.Context, name, dobISO string, includeExpired bool) (Employee, bool, error) {
	rows, err := s.fetch(ctx, strings.Join(strings.Fields(name), " "), dobISO, includeExpired)
	if err != nil {
		return Employee{}, false, err
	}
	var hit []employee
	for _, r := range rows {
		if norm(r.Name) == norm(name) && r.DateOfBirth == dobISO {
			hit = append(hit, r)
		}
	}
	if len(hit) != 1 {
		return Employee{}, false, nil
	}
	r := hit[0]
	return Employee{
		PatientID: r.PatientID, PersonalID: strings.TrimSpace(r.PersonalID),
		Name: strings.Join(strings.Fields(r.Name), " "), Gender: r.Gender, DOB: r.DateOfBirth,
		Nationality: r.Nationality, Company: r.Company, Entity: r.CompanyEntity, Parents: r.Parents,
		Position: r.WorkPosition, CheckUpID: r.CheckUpID, McuResult: r.ResultCriteria, McuValidUntil: r.ResultValidUntil,
		McuNote: strings.TrimSpace(r.ResultNotes),
	}, true, nil
}

// Find is Pick reduced to what the worker sees about themselves.
func (s HTTPSource) Find(ctx context.Context, name, dobISO string) (Person, bool, error) {
	e, ok, err := s.Pick(ctx, name, dobISO)
	if err != nil || !ok {
		return Person{}, false, err
	}
	return Person{Name: e.Name, DOB: e.DOB, Company: e.Company, Position: e.Position, McuValidUntil: e.McuValidUntil}, true, nil
}

// Eligible reports whether a worker with exactly this name is listed. medpost
// matches names partially, so the exact comparison is repeated here.
func (s HTTPSource) Eligible(ctx context.Context, name string) (bool, error) {
	name = strings.Join(strings.Fields(name), " ")
	if len([]rune(name)) < MinQuery {
		return false, nil
	}
	rows, err := s.fetch(ctx, name, "", false)
	if err != nil {
		return false, err
	}
	for _, r := range rows {
		if norm(r.Name) == norm(name) {
			return true, nil
		}
	}
	return false, nil
}
