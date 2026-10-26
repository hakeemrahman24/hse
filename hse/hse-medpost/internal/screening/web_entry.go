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

// requiredWebFields are the answers the web form insists on, the same ones
// the offline form will not save without (its donor signature is a click in
// the browser, so it is not sent).
var requiredWebFields = []string{"donor_name", "donor_company", "donor_position", "consent_at", "test_type"}

// CreateAlcoholEntry: POST /api/screening-log/alcohol. The web version of the
// offline form's upload: one record in toRow() shape plus "form_version",
// sent by a signed-in staff member (session cookie, not the offline bearer
// token) and attributed to them. Saved once per id, so a retry cannot
// duplicate it: 201 for a new record, 200 for one already stored.
func (a *API) CreateAlcoholEntry(w http.ResponseWriter, r *http.Request) {
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
	values, err := parseRecord(raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid record"})
		return
	}
	for _, key := range requiredWebFields {
		if strings.TrimSpace(values[key]) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": key + " is required"})
			return
		}
	}
	var version struct {
		FormVersion string `json:"form_version"`
	}
	if json.Unmarshal(raw, &version) != nil || version.FormVersion == "" || len(version.FormVersion) > maxFieldLength {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "form_version is required"})
		return
	}
	var existing string
	err = a.DB.QueryRow(`SELECT ClientID FROM DrugAlcoholScreening WHERE ClientID = ?`, values["id"]).Scan(&existing)
	if err == nil {
		writeJSON(w, http.StatusOK, map[string]string{"id": values["id"]})
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		log.Printf("screening: web entry lookup failed (client id %s): %v", values["id"], err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if err := a.insert(sess.StaffID, version.FormVersion, values, raw); err != nil {
		log.Printf("screening: web entry insert failed (client id %s, staff %d): %v", values["id"], sess.StaffID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": values["id"]})
}
