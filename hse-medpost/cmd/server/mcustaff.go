package main

import (
	"database/sql"
	"net/http"

	"hse-medpost/internal/mcusubmit"
)

// registerMcuStaff adds the staff side of the medical checkup portal to the staff app: the review
// list API (the page is the Submitted tab on /mcu). It uses the staff login, never the portal cookie, and the same
// doctor-or-CMO check as /mcu, because a submitted checkup result is the same health record.
// (Plan section 7 said "staff or admin"; the stricter rule is the default until the owner says
// who else may review. Loosen it here by passing another guard.)
func registerMcuStaff(mux *http.ServeMux, guard func(http.Handler) http.Handler, database *sql.DB, files mcusubmit.FileStore) {
	api := &mcusubmit.StaffAPI{Store: mcusubmit.NewStore(database), Files: files}

	// The review list is the Submitted tab on /mcu now. The old address stays, because the reviewer
	// digest mail links to it.
	mux.Handle("GET /mcu-submissions", guard(http.RedirectHandler("/mcu?view=submitted", http.StatusSeeOther)))
	mux.Handle("GET /api/mcu-submissions", guard(http.HandlerFunc(api.List)))
	mux.Handle("GET /api/mcu-submissions/{id}", guard(http.HandlerFunc(api.Get)))
	mux.Handle("GET /api/mcu-submissions/{id}/file", guard(http.HandlerFunc(api.File)))
	mux.Handle("GET /api/mcu-submissions/{id}/records", guard(http.HandlerFunc(api.Records)))
	mux.Handle("POST /api/mcu-submissions/{id}/reject", guard(http.HandlerFunc(api.Reject)))
	mux.Handle("POST /api/mcu-submissions/{id}/link", guard(http.HandlerFunc(api.Link)))
	mux.Handle("POST /api/mcu-submissions/{id}/unlink", guard(http.HandlerFunc(api.Unlink)))
	mux.Handle("POST /api/mcu-submissions/{id}/close", guard(http.HandlerFunc(api.Close)))
}
