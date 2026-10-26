package emr

import (
	"net/http"
	"strings"

	"hse-medpost/internal/dates"
)

const summaryRegionLimit = 15

// regionColumns maps the ?by= choice to the MalariaScreening columns holding the
// code and the name. The SQL only ever uses these constants, never the request text.
var regionColumns = map[string][2]string{
	"regency":  {"RegencyCode", "RegencyName"},
	"district": {"DistrictCode", "DistrictName"},
	"village":  {"VillageCode", "VillageName"},
}

// dateBounds turns ?from= and ?to= (YYYY-MM-DD, inclusive) into SQL conditions on
// m.TestDate, for the list and the summary alike.
func dateBounds(r *http.Request) (string, []any, error) {
	clause := ""
	var args []any
	for _, bound := range []struct{ param, op string }{{"from", ">="}, {"to", "<="}} {
		v, err := dates.Normalize(r.URL.Query().Get(bound.param))
		if err != nil {
			return "", nil, invalidInput(bound.param + ": " + err.Error())
		}
		if v != "" {
			clause += ` AND m.TestDate ` + bound.op + ` ?`
			args = append(args, v)
		}
	}
	return clause, args, nil
}

const (
	maxNameWords     = 6
	maxNameWordRunes = 40
	// likeEscape is the escape character in LIKE patterns. "!" is used because a
	// backslash means different things to MariaDB and SQLite string literals.
	likeEscape = "!"
)

// nameBounds turns ?q= into conditions on the patient's name (alias p): every
// word typed must appear in it, in any order and any case. %, _ and the escape
// character in the typed text match only themselves.
func nameBounds(r *http.Request) (string, []any) {
	clause := ""
	var args []any
	words := strings.Fields(r.URL.Query().Get("q"))
	if len(words) > maxNameWords {
		words = words[:maxNameWords]
	}
	replacer := strings.NewReplacer(likeEscape, likeEscape+likeEscape, "%", likeEscape+"%", "_", likeEscape+"_")
	for _, word := range words {
		if runes := []rune(word); len(runes) > maxNameWordRunes {
			word = string(runes[:maxNameWordRunes])
		}
		clause += ` AND p.Name LIKE ? ESCAPE '` + likeEscape + `'`
		args = append(args, "%"+replacer.Replace(word)+"%")
	}
	return clause, args
}

// filters is the date range and the name search together, for the list and the export.
func filters(r *http.Request) (string, []any, error) {
	where, args, err := dateBounds(r)
	if err != nil {
		return "", nil, err
	}
	nameWhere, nameArgs := nameBounds(r)
	return where + nameWhere, append(args, nameArgs...), nil
}

type regionCount struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Count    int    `json:"count"`
	Positive int    `json:"positive"`
	// Species splits Positive by result; the keys are always speciesResults.
	Species map[string]int `json:"species"`
}

type summary struct {
	Total       int            `json:"total"`
	Patients    int            `json:"patients"`
	Positive    int            `json:"positive"`
	Negative    int            `json:"negative"`
	Pending     int            `json:"pending"`
	Species     map[string]int `json:"species"`
	ByExamType  map[string]int `json:"byExamType"`
	By          string         `json:"by"`
	Regions     []regionCount  `json:"regions"`
	MoreRegions int            `json:"moreRegions"`
}

// speciesResults are the positive results, in the order the dashboard shows them.
var speciesResults = []string{"Pv", "Pf", "Po", "Mixed"}

const positiveResults = `m.Result IN ('Pv','Pf','Po','Mixed')`

// speciesSums is one SUM column per positive result, in speciesResults order.
// The names are constants, never request text.
func speciesSums() string {
	cols := ""
	for _, result := range speciesResults {
		cols += `, COALESCE(SUM(CASE WHEN m.Result = '` + result + `' THEN 1 ELSE 0 END), 0)`
	}
	return cols
}

// speciesTargets returns a zeroed map and the scan destinations that fill it.
func speciesTargets() (map[string]int, []any) {
	counts := make(map[string]int, len(speciesResults))
	dests := make([]any, len(speciesResults))
	for i, result := range speciesResults {
		counts[result] = 0
		dests[i] = new(int)
	}
	return counts, dests
}

func fillSpecies(counts map[string]int, dests []any) {
	for i, result := range speciesResults {
		counts[result] = *dests[i].(*int)
	}
}

// Summary backs the dashboard tab: how many screenings were done (and for how
// many patients), how they came out, and the busiest regions. ?by= is regency
// (default), district or village; ?from= and ?to= limit the test dates. Only the
// biggest summaryRegionLimit regions are listed; moreRegions counts the rest.
func (a *API) Summary(w http.ResponseWriter, r *http.Request) {
	by := r.URL.Query().Get("by")
	if by == "" {
		by = "regency"
	}
	columns, ok := regionColumns[by]
	if !ok {
		writeError(w, invalidInput("by must be regency, district or village"))
		return
	}
	where, args, err := dateBounds(r)
	if err != nil {
		writeError(w, err)
		return
	}

	species, speciesDests := speciesTargets()
	out := summary{By: by, Species: species, ByExamType: map[string]int{}, Regions: []regionCount{}}
	err = a.DB.QueryRow(`
		SELECT COUNT(*), COUNT(DISTINCT v.FK_PatientID),
		       COALESCE(SUM(CASE WHEN `+positiveResults+` THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN m.Result = 'Negative' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN m.Result IS NULL THEN 1 ELSE 0 END), 0)`+speciesSums()+`
		FROM MalariaScreening m JOIN Visit v ON v.VisitID = m.FK_VisitID
		WHERE 1=1`+where, args...).Scan(append([]any{&out.Total, &out.Patients, &out.Positive, &out.Negative, &out.Pending}, speciesDests...)...)
	if err != nil {
		writeError(w, err)
		return
	}
	fillSpecies(out.Species, speciesDests)

	examRows, err := a.DB.Query(`SELECT m.ExamType, COUNT(*) FROM MalariaScreening m WHERE 1=1`+where+` GROUP BY m.ExamType`, args...)
	if err != nil {
		writeError(w, err)
		return
	}
	defer examRows.Close()
	for examRows.Next() {
		var exam string
		var n int
		if err := examRows.Scan(&exam, &n); err != nil {
			writeError(w, err)
			return
		}
		out.ByExamType[exam] = n
	}
	if err := examRows.Err(); err != nil {
		writeError(w, err)
		return
	}

	code, name := "m."+columns[0], "m."+columns[1]
	regionRows, err := a.DB.Query(`
		SELECT `+code+`, `+name+`, COUNT(*), SUM(CASE WHEN `+positiveResults+` THEN 1 ELSE 0 END)`+speciesSums()+`
		FROM MalariaScreening m WHERE 1=1`+where+`
		GROUP BY `+code+`, `+name+`
		ORDER BY COUNT(*) DESC, `+name+`, `+code, args...)
	if err != nil {
		writeError(w, err)
		return
	}
	defer regionRows.Close()
	for regionRows.Next() {
		var rc regionCount
		var dests []any
		rc.Species, dests = speciesTargets()
		if err := regionRows.Scan(append([]any{&rc.Code, &rc.Name, &rc.Count, &rc.Positive}, dests...)...); err != nil {
			writeError(w, err)
			return
		}
		fillSpecies(rc.Species, dests)
		if len(out.Regions) < summaryRegionLimit {
			out.Regions = append(out.Regions, rc)
		} else {
			out.MoreRegions++
		}
	}
	if err := regionRows.Err(); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
