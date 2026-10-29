package main

import (
	"database/sql"
	"html/template"
	"net/http"

	"hse-medpost/internal/mcusubmit"
	"hse-medpost/internal/middleware"
)

// registerMcuStaff adds the staff side of the medical checkup portal to the staff app: the review
// list page and its API. It uses the staff login, never the portal cookie, and the same
// doctor-or-CMO check as /mcu, because a submitted checkup result is the same health record.
// (Plan section 7 said "staff or admin"; the stricter rule is the default until the owner says
// who else may review. Loosen it here by passing another guard.)
func registerMcuStaff(mux *http.ServeMux, guard func(http.Handler) http.Handler, templates *template.Template, database *sql.DB, files mcusubmit.FileStore) {
	api := &mcusubmit.StaffAPI{Store: mcusubmit.NewStore(database), Files: files}

	mux.Handle("GET /mcu-submissions", guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := ""
		if sess := middleware.Session(r); sess != nil {
			name = sess.Name
		}
		w.Header().Set("Cache-Control", "no-store")
		if err := templates.ExecuteTemplate(w, "mcu-submissions.html", map[string]any{"Name": name}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})))
	mux.Handle("GET /api/mcu-submissions", guard(http.HandlerFunc(api.List)))
	mux.Handle("GET /api/mcu-submissions/{id}/file", guard(http.HandlerFunc(api.File)))
	mux.Handle("POST /api/mcu-submissions/{id}/review", guard(http.HandlerFunc(api.MarkReviewed)))
	mux.Handle("POST /api/mcu-submissions/{id}/reject", guard(http.HandlerFunc(api.Reject)))
	mux.Handle("POST /api/mcu-submissions/{id}/close", guard(http.HandlerFunc(api.Close)))
}
