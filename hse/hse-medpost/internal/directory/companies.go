package directory

import (
	"log"
	"net/http"
	"strings"
)

// governmentEntity is the EntityType name medpost gives government offices.
const governmentEntity = "Government"

// Company is one company in medpost's reference list, for databases that keep
// a read-only mirror (entry-permit).
type Company struct {
	CompanyID    int64    `json:"companyId"`
	Name         string   `json:"name"`
	Abbreviation string   `json:"abbreviation"`
	Entity       string   `json:"entity"`
	Gov          bool     `json:"gov"`
	Archived     bool     `json:"archived"`
	Parents      []Parent `json:"parents"`
}

// isGov: a government office has the Government entity or the GOV abbreviation
// (medpost's seed gives every government office both). Decided here, next to
// the data, so every reader of the API gets the same answer.
func isGov(entity, abbreviation string) bool {
	return strings.EqualFold(strings.TrimSpace(entity), governmentEntity) ||
		strings.EqualFold(strings.TrimSpace(abbreviation), "GOV")
}

// Companies answers GET /api/directory/companies with every company, archived
// ones included and flagged, so a mirror can hide them from pickers yet keep
// the name on permits already issued. Parents come ordered by depth (nearest
// first). WHY a token route and a mirror: medpost is the one place where the
// list is edited; the other app copies it nightly instead of calling live, so
// its forms keep working while medpost restarts. Read-only.
func (a *API) Companies(w http.ResponseWriter, r *http.Request) {
	if !a.authorize(w, r) {
		return
	}
	rows, err := a.DB.Query(`SELECT c.CompanyID, c.Name, COALESCE(c.Abbreviation,''), COALESCE(e.Name,''), COALESCE(c.Archived,0)
		FROM Company c LEFT JOIN EntityType e ON e.EntityTypeID = c.FK_EntityTypeID
		ORDER BY c.Name`)
	if err != nil {
		log.Printf("directory companies: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()
	out := []Company{}
	var ids []int64
	for rows.Next() {
		var c Company
		var archived int
		if err := rows.Scan(&c.CompanyID, &c.Name, &c.Abbreviation, &c.Entity, &archived); err != nil {
			log.Printf("directory companies scan: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		c.Archived, c.Gov, c.Parents = archived == 1, isGov(c.Entity, c.Abbreviation), []Parent{}
		out = append(out, c)
		ids = append(ids, c.CompanyID)
	}
	if err := rows.Err(); err != nil {
		log.Printf("directory companies rows: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	parents, err := a.parentsByCompanyAll(ids)
	if err != nil {
		log.Printf("directory companies parents: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	for i := range out {
		if p := parents[out[i].CompanyID]; len(p) > 0 {
			out[i].Parents = p
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// parentsByCompanyAll reads the parents of many companies in batches, because
// parentsByCompany builds one placeholder per id and SQLite refuses more than
// a few hundred (the list is every company, not one page of employees).
func (a *API) parentsByCompanyAll(ids []int64) (map[int64][]Parent, error) {
	const batch = 200
	out := map[int64][]Parent{}
	for start := 0; start < len(ids); start += batch {
		end := min(start+batch, len(ids))
		got, err := a.parentsByCompany(ids[start:end])
		if err != nil {
			return nil, err
		}
		for id, p := range got {
			out[id] = p
		}
	}
	return out, nil
}
