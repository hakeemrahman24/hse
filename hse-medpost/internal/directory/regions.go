package directory

import (
	"log"
	"net/http"
)

// Region is one regency or city (kabupaten / kota) with its province name.
type Region struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Province string `json:"province"`
}

// Regions answers GET /api/directory/regions with every regency and city and
// the province it belongs to, from the local Region table (the same data the
// staff app's cascading dropdowns use). WHY a token route: other databases
// (entry-permit) must read the official names, not copy them, so a name they
// store always matches the one medpost shows. Read-only.
func (a *API) Regions(w http.ResponseWriter, r *http.Request) {
	if !a.authorize(w, r) {
		return
	}
	rows, err := a.DB.Query(`SELECT c.Code, c.Name, p.Name
		FROM Region c JOIN Region p ON p.Code = c.ParentCode
		WHERE c.Level = 2 ORDER BY p.Name, c.Name`)
	if err != nil {
		log.Printf("directory regions: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()
	out := []Region{}
	for rows.Next() {
		var rg Region
		if err := rows.Scan(&rg.Code, &rg.Name, &rg.Province); err != nil {
			log.Printf("directory regions scan: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		out = append(out, rg)
	}
	if err := rows.Err(); err != nil {
		log.Printf("directory regions rows: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}
