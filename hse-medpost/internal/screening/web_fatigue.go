package screening

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"hse-medpost/internal/middleware"
)

// CreateFatigueEntry: POST /api/screening-log/fatigue. The web version of the
// offline fatigue form's upload: one record in its row shape plus
// "form_version", from a signed-in staff member (session cookie) and
// attributed to them. Saved once per id: 201 new, 200 already stored.
func (a *API) CreateFatigueEntry(w http.ResponseWriter, r *http.Request) {
	sess := middleware.Session(r)
	if sess == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "sign in required"})
		return
	}
	var raw json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&raw); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	rec, err := parseFatigueRecord(raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid record"})
		return
	}
	var version struct {
		FormVersion string `json:"form_version"`
	}
	if json.Unmarshal(raw, &version) != nil || version.FormVersion == "" || len(version.FormVersion) > maxFieldLength {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "form_version is required"})
		return
	}
	required := map[string]flexText{
		"examiner": rec.Examiner, "name": rec.Name, "company": rec.Company, "position": rec.Position,
		"sleep_24h": rec.Sleep24h, "sleep_48h": rec.Sleep48h, "awake_hours": rec.AwakeHours,
		"score_total": rec.ScoreTotal, "risk_level": rec.RiskLevel,
	}
	for key, value := range required {
		if strings.TrimSpace(string(value)) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": key + " is required"})
			return
		}
	}
	var existing string
	err = a.DB.QueryRow(`SELECT ClientID FROM FatigueScreening WHERE ClientID = ?`, string(rec.ID)).Scan(&existing)
	if err == nil {
		writeJSON(w, http.StatusOK, map[string]string{"id": string(rec.ID)})
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		log.Printf("screening: fatigue entry lookup failed (client id %s): %v", rec.ID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if err := a.insertFatigue(sess.StaffID, version.FormVersion, rec, raw); err != nil {
		log.Printf("screening: fatigue entry insert failed (client id %s, staff %d): %v", rec.ID, sess.StaffID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": string(rec.ID)})
}
