package sofi

import (
	"encoding/json"
	"log"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

// summaryGroupLimit is how many company or position bars the dashboard shows.
const summaryGroupLimit = 20

// levelKey is the lower-case name of a level in the JSON (low / moderate / high), the same
// spelling the dashboard's colour classes use.
func levelKey(level string) string { return strings.ToLower(level) }

func newLevelCounts() map[string]int {
	return map[string]int{levelKey(LevelLow): 0, levelKey(LevelModerate): 0, levelKey(LevelHigh): 0}
}

type dimensionSummary struct {
	Key     string         `json:"key"`
	EN      string         `json:"en"`
	ID      string         `json:"id"`
	Average float64        `json:"average"`
	Max     int            `json:"max"`
	Results map[string]int `json:"results"`
}

type groupSummary struct {
	Name    string         `json:"name"`
	Count   int            `json:"count"`
	Results map[string]int `json:"results"`
}

type summary struct {
	Total      int                `json:"total"`
	People     int                `json:"people"`
	Results    map[string]int     `json:"results"`
	Dimensions []dimensionSummary `json:"dimensions"`
	By         string             `json:"by"`
	Groups     []groupSummary     `json:"groups"`
	MoreGroups int                `json:"moreGroups"`
}

// StaffSummary counts the saved questionnaires for the Fatigue dashboard (staff sign-in only).
// Filters: from and to (yyyy-MM-dd, by submit date) and by (company or position, default company).
// Levels are worked out again from the stored answers, so the provisional bands in scoring.go
// apply to every record, old ones included.
func (a *API) StaffSummary(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	by := q.Get("by")
	if by == "" {
		by = "company"
	}
	column := map[string]string{"company": "s.Company", "position": "s.WorkPosition"}[by]
	if column == "" {
		writeError(w, r, http.StatusBadRequest, "by must be company or position")
		return
	}
	where, args := []string{"1 = 1"}, []any{}
	for _, f := range []struct{ param, op string }{{"from", ">="}, {"to", "<="}} {
		v := q.Get(f.param)
		if v == "" {
			continue
		}
		if _, err := time.Parse("2006-01-02", v); err != nil {
			writeError(w, r, http.StatusBadRequest, f.param+" must be yyyy-mm-dd")
			return
		}
		where = append(where, "SUBSTR(s.SubmittedAt, 1, 10) "+f.op+" ?")
		args = append(args, v)
	}

	rows, err := a.DB.Query(`SELECT s.FK_PatientID, TRIM(`+column+`), s.Answers FROM SofiScreening s WHERE `+strings.Join(where, " AND "), args...)
	if err != nil {
		log.Printf("sofi: summary: %v", err)
		writeError(w, r, http.StatusInternalServerError, "Could not read the SOFI records.")
		return
	}
	defer rows.Close()

	out := summary{Results: newLevelCounts(), By: by, Groups: []groupSummary{}}
	sums := make([]int, len(Dimensions))
	out.Dimensions = make([]dimensionSummary, len(Dimensions))
	for i, d := range Dimensions {
		out.Dimensions[i] = dimensionSummary{Key: d.Key, EN: d.EN, ID: d.ID, Max: itemsPerDimension * MaxAnswer, Results: newLevelCounts()}
	}
	people := map[int64]bool{}
	groups := map[string]*groupSummary{}
	for rows.Next() {
		var patient int64
		var group, raw string
		if err := rows.Scan(&patient, &group, &raw); err != nil {
			log.Printf("sofi: summary scan: %v", err)
			writeError(w, r, http.StatusInternalServerError, "Could not read the SOFI records.")
			return
		}
		var ans []int
		if err := json.Unmarshal([]byte(raw), &ans); err != nil {
			log.Printf("sofi: summary: unreadable answers: %v", err)
			continue
		}
		res, err := Score(ans)
		if err != nil {
			log.Printf("sofi: summary: %v", err)
			continue
		}
		out.Total++
		people[patient] = true
		out.Results[levelKey(res.Level)]++
		for i, d := range res.Dimensions {
			sums[i] += d.Score
			out.Dimensions[i].Results[levelKey(d.Level)]++
		}
		g := groups[group]
		if g == nil {
			g = &groupSummary{Name: group, Results: newLevelCounts()}
			groups[group] = g
		}
		g.Count++
		g.Results[levelKey(res.Level)]++
	}
	if err := rows.Err(); err != nil {
		log.Printf("sofi: summary rows: %v", err)
		writeError(w, r, http.StatusInternalServerError, "Could not read the SOFI records.")
		return
	}

	out.People = len(people)
	if out.Total > 0 {
		for i := range out.Dimensions {
			out.Dimensions[i].Average = math.Round(float64(sums[i])/float64(out.Total)*10) / 10
		}
	}
	all := make([]groupSummary, 0, len(groups))
	for _, g := range groups {
		all = append(all, *g)
	}
	// Busiest first; a blank name (not stated) goes last among equals.
	sort.Slice(all, func(i, j int) bool {
		if all[i].Count != all[j].Count {
			return all[i].Count > all[j].Count
		}
		if (all[i].Name == "") != (all[j].Name == "") {
			return all[j].Name == ""
		}
		return all[i].Name < all[j].Name
	})
	if len(all) > summaryGroupLimit {
		out.MoreGroups = len(all) - summaryGroupLimit
		all = all[:summaryGroupLimit]
	}
	out.Groups = append(out.Groups, all...)
	writeJSON(w, http.StatusOK, out)
}
