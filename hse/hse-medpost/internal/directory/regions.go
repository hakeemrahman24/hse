package directory

import (
	"context"
	"log"
	"math"
	"net/http"

	"hse-medpost/internal/datasync"
)

// Region is one regency or city (kabupaten / kota) with its province name and what the Kemenkes
// malaria map says about it.
type Region struct {
	Code       string     `json:"code"`
	Name       string     `json:"name"`
	Province   string     `json:"province"`
	Endemicity Endemicity `json:"endemicity"`
}

// Endemicity is the malaria endemicity of a regency, from the applied Kemenkes list. Status is
// free, low, medium, high or unknown; "unknown" means medpost has NO data for the regency and
// must never be read as free. Label is the map's own wording (Endemis Tinggi I/II/III included),
// DataYear the map year the row came from (an older year for regencies the newest map lacks), and
// AppliedAt the date the list was applied. The map's case data is not validated by Kemenkes, and
// Per1000 is cases per 1,000 of the total population, which can disagree with the label.
type Endemicity struct {
	Status    string  `json:"status"`
	Label     string  `json:"label,omitempty"`
	Free      bool    `json:"free"`
	DataYear  int     `json:"dataYear,omitempty"`
	Per1000   float64 `json:"per1000,omitempty"`
	AppliedAt string  `json:"appliedAt,omitempty"`
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
	a.addEndemicity(r.Context(), out)
	writeJSON(w, http.StatusOK, out)
}

// addEndemicity fills each regency's endemicity from the applied list. WHY it never fails the
// request: the regency list is what other databases depend on. If the endemicity tables are
// missing (migration 031/047 not run yet) or unreadable, every regency says "unknown" and the
// reason is logged, so a problem here can only hide the extra field, never break the names.
func (a *API) addEndemicity(ctx context.Context, regions []Region) {
	rows, appliedAt, err := (&datasync.Store{DB: a.DB}).Snapshot(ctx)
	if err != nil {
		log.Printf("directory regions: endemicity unavailable, sending unknown: %v", err)
		rows = nil
	}
	for i := range regions {
		regions[i].Endemicity = Endemicity{Status: datasync.StatusUnknown}
		row, ok := rows[regions[i].Code]
		if !ok {
			continue
		}
		regions[i].Endemicity = Endemicity{
			Status: datasync.Status(row.Label), Label: row.Label, Free: row.Label == "Eliminasi",
			DataYear: row.DataYear, Per1000: math.Round(row.Per1000()*100) / 100, AppliedAt: appliedAt,
		}
	}
}
