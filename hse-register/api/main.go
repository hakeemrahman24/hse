package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

func main() {
	for _, f := range []string{".env", "db.env", "../.env", "../db.env"} {
		_ = godotenv.Load(f)
	}

	createAdminUser := flag.String("createadmin", "", "create an admin user: -createadmin username:password")
	organizeEvidence := flag.Bool("organize-evidence", false, "give old evidence files (random names) their readable names and folders, then exit")
	flag.Parse()

	db, err := openDB()
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer db.Close()

	if *createAdminUser != "" {
		runCreateAdmin(db, *createAdminUser)
		return
	}

	if *organizeEvidence {
		dir := envOrDefault("EVIDENCE_DIR", "data/evidence")
		n, err := organizeAll(context.Background(), db, dir)
		log.Printf("organized the evidence of %d observation(s) in %s", n, dir)
		if err != nil {
			log.Fatalf("some files could not be renamed: %v", err)
		}
		return
	}

	secret := os.Getenv("SESSION_SECRET")
	if secret == "" {
		log.Fatal("SESSION_SECRET is not set")
	}
	store := NewSessionStore(secret)
	auth := &authContext{store: store, db: db}

	loadPages("web/templates")
	loadHomePages("web/templates")
	loadSOCPages("web/templates")
	loadLoginPage("web/templates")

	workPermitRegisters := &workPermitRegisterHandlers{db: db}
	evidenceDir := envOrDefault("EVIDENCE_DIR", "data/evidence")

	// Lookup lists (hazards, topics, keywords, areas) are filled from the seed CSVs on the first
	// start after migration 007, then read from the database.
	if err := seedReference(db, envOrDefault("REF_SEED_DIR", "migrations/seed")); err != nil {
		log.Fatalf("lookup lists: %v", err)
	}
	ref := newRefData(db)
	medpost := newMedpostConn(db, secret)
	companies := newCompanyCache(liveCompanies{conn: medpost, fallback: devCompanySource()})

	observations := &observationHandlers{db: db, ref: ref, evidenceDir: evidenceDir}
	adminObservations := &adminObservationHandlers{db: db, auth: auth, evidenceDir: evidenceDir, ref: ref, companies: companies}
	obsViews := &obsViewHandlers{db: db, auth: auth, ref: ref}
	files := &fileHandlers{db: db, auth: auth, dir: evidenceDir}
	areas := &areaHandlers{db: db, auth: auth, ref: ref}
	hazards := &hazardHandlers{db: db, auth: auth, ref: ref}
	guard := &guardHandlers{db: db, auth: auth, ref: ref}
	adminUsers := &adminUserHandlers{db: db, auth: auth}
	login := &loginHandlers{db: db, store: store}
	home := &homeHandlers{auth: auth}
	activities := &activitiesHandlers{auth: auth, conn: medpost}
	medpostAdmin := &medpostHandlers{auth: auth, conn: medpost, onChange: func() { companies.Reset(); activities.Reset() }}
	account := &accountHandlers{db: db, auth: auth, lim: newLimiter()}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("GET /api/workpermitregister", workPermitRegisters.list)
	mux.HandleFunc("POST /api/workpermitregister", workPermitRegisters.create)
	mux.HandleFunc("GET /api/workpermitregister/{id}", workPermitRegisters.get)
	mux.HandleFunc("PUT /api/workpermitregister/{id}", workPermitRegisters.update)
	mux.HandleFunc("DELETE /api/workpermitregister/{id}", workPermitRegisters.delete)

	mux.HandleFunc("GET /api/observations", observations.list)
	mux.HandleFunc("POST /api/observations", observations.create)
	mux.HandleFunc("GET /api/observations/{id}", observations.get)
	mux.HandleFunc("PUT /api/observations/{id}", observations.update)
	mux.HandleFunc("DELETE /api/observations/{id}", observations.delete)

	// no-cache makes the browser ask "has this changed?" every time (a cheap 304 when it has not), so a
	// new stylesheet or script reaches people at once instead of after their cached copy goes stale.
	static := http.StripPrefix("/static/", http.FileServer(http.Dir("web/static")))
	mux.Handle("GET /static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		static.ServeHTTP(w, r)
	}))

	// "/" is the login page (as in hse-medpost); the old /login path still works.
	mux.HandleFunc("GET /{$}", login.show)
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
	mux.HandleFunc("POST /login", login.submit)
	mux.HandleFunc("POST /logout", login.logout)

	mux.HandleFunc("GET /start", auth.requireAuth(home.start))
	mux.HandleFunc("GET /activities", auth.requireAuth(activities.show))
	mux.HandleFunc("GET /activities/documents", auth.requireAuth(activities.documents))
	mux.HandleFunc("GET /activities/documents/{id}/{name}", auth.requireAuth(activities.documentFile))
	mux.HandleFunc("GET /activities/files/{eid}/{name}", auth.requireAuth(activities.evidenceFile))
	mux.HandleFunc("GET /admin/settings", auth.requireAdmin(home.settings))
	mux.HandleFunc("GET /account", auth.requireAuth(account.show))
	mux.HandleFunc("POST /account", auth.requireAuth(account.submit))

	// The Work Permit Register screens are off while the module is under development.
	mux.HandleFunc("GET /workpermitregister", auth.requireAuth(home.workPermit))

	mux.HandleFunc("POST /observations/suggest", auth.requireAuth(func(w http.ResponseWriter, r *http.Request) { serveSuggestion(w, r, ref) }))
	mux.HandleFunc("GET /observations", auth.requireAuth(obsViews.dashboard))
	mux.HandleFunc("GET /observations/list", auth.requireAuth(obsViews.list))
	mux.HandleFunc("GET /observations/{id}", auth.requireAuth(obsViews.detail))
	mux.HandleFunc("POST /observations/{id}/status", auth.requireAuth(obsViews.setStatus))
	mux.HandleFunc("POST /observations/{id}/verify", auth.requireAuth(obsViews.verify))
	mux.HandleFunc("GET /observations/new", auth.requireAuth(adminObservations.newForm))
	mux.HandleFunc("POST /observations/new", auth.requireAuth(adminObservations.create))
	mux.HandleFunc("GET /observations/{id}/edit", auth.requireAuth(adminObservations.editForm))
	mux.HandleFunc("POST /observations/{id}/edit", auth.requireAuth(adminObservations.update))
	mux.HandleFunc("POST /observations/{id}/delete", auth.requireAdmin(adminObservations.delete))

	// Evidence and after files: photos (4 per kind, 500 KB) and PDFs (2 per kind, 5 MB). Staff only.
	mux.HandleFunc("GET /evidence/{id}", auth.requireAuth(files.serve))
	mux.HandleFunc("POST /observations/{id}/files", auth.requireAuth(files.upload))
	mux.HandleFunc("POST /observations/{id}/files/{fileId}/delete", auth.requireAuth(files.remove))

	// The Area list is edited in Settings (admin only).
	mux.HandleFunc("GET /admin/settings/areas", auth.requireAdmin(areas.list))
	mux.HandleFunc("POST /admin/settings/areas", auth.requireAdmin(areas.add))
	mux.HandleFunc("POST /admin/settings/areas/{id}/rename", auth.requireAdmin(areas.rename))
	mux.HandleFunc("POST /admin/settings/areas/{id}/delete", auth.requireAdmin(areas.remove))
	mux.HandleFunc("POST /admin/settings/areas/{id}/subareas", auth.requireAdmin(areas.subAdd))
	mux.HandleFunc("POST /admin/settings/subareas/{id}/rename", auth.requireAdmin(areas.subRename))
	mux.HandleFunc("POST /admin/settings/subareas/{id}/delete", auth.requireAdmin(areas.subRemove))

	// Hazard classes, hazard categories and sources: add, rename, archive, restore, delete.
	mux.HandleFunc("GET /admin/settings/hazard-classes", auth.requireAdmin(hazards.classList))
	mux.HandleFunc("POST /admin/settings/hazard-classes", auth.requireAdmin(hazards.classAdd))
	mux.HandleFunc("POST /admin/settings/hazard-classes/{id}/rename", auth.requireAdmin(hazards.classRename))
	mux.HandleFunc("POST /admin/settings/hazard-classes/{id}/archive", auth.requireAdmin(hazards.classArchive))
	mux.HandleFunc("POST /admin/settings/hazard-classes/{id}/restore", auth.requireAdmin(hazards.classRestore))
	mux.HandleFunc("POST /admin/settings/hazard-classes/{id}/delete", auth.requireAdmin(hazards.classDelete))
	mux.HandleFunc("GET /admin/settings/hazard-categories", auth.requireAdmin(hazards.categoryList))
	mux.HandleFunc("POST /admin/settings/hazard-categories", auth.requireAdmin(hazards.categoryAdd))
	mux.HandleFunc("POST /admin/settings/hazard-categories/{id}/save", auth.requireAdmin(hazards.categorySave))
	mux.HandleFunc("POST /admin/settings/hazard-categories/{id}/archive", auth.requireAdmin(hazards.categoryArchive))
	mux.HandleFunc("POST /admin/settings/hazard-categories/{id}/restore", auth.requireAdmin(hazards.categoryRestore))
	mux.HandleFunc("POST /admin/settings/hazard-categories/{id}/delete", auth.requireAdmin(hazards.categoryDelete))
	mux.HandleFunc("GET /admin/settings/medpost-api", auth.requireAdmin(medpostAdmin.show))
	mux.HandleFunc("POST /admin/settings/medpost-api", auth.requireAdmin(medpostAdmin.submit))
	mux.HandleFunc("POST /admin/settings/medpost-api/clear", auth.requireAdmin(medpostAdmin.clear))
	mux.HandleFunc("GET /admin/settings/source-types", auth.requireAdmin(hazards.typeList))
	mux.HandleFunc("POST /admin/settings/source-types", auth.requireAdmin(hazards.typeAdd))
	mux.HandleFunc("POST /admin/settings/source-types/{id}/rename", auth.requireAdmin(hazards.typeRename))
	mux.HandleFunc("POST /admin/settings/source-types/{id}/archive", auth.requireAdmin(hazards.typeArchive))
	mux.HandleFunc("POST /admin/settings/source-types/{id}/restore", auth.requireAdmin(hazards.typeRestore))
	mux.HandleFunc("POST /admin/settings/source-types/{id}/delete", auth.requireAdmin(hazards.typeDelete))
	mux.HandleFunc("GET /admin/settings/keywords", auth.requireAdmin(hazards.keywordList))
	mux.HandleFunc("GET /admin/settings/guard-words", auth.requireAdmin(guard.show))
	mux.HandleFunc("POST /admin/settings/guard-words", auth.requireAdmin(guard.save))
	mux.HandleFunc("POST /admin/settings/keywords", auth.requireAdmin(hazards.keywordAdd))
	mux.HandleFunc("POST /admin/settings/keywords/{id}/save", auth.requireAdmin(hazards.keywordSave))
	mux.HandleFunc("POST /admin/settings/keywords/{id}/on", auth.requireAdmin(hazards.keywordOn))
	mux.HandleFunc("POST /admin/settings/keywords/{id}/off", auth.requireAdmin(hazards.keywordOff))
	mux.HandleFunc("POST /admin/settings/keywords/{id}/delete", auth.requireAdmin(hazards.keywordDelete))
	mux.HandleFunc("GET /admin/settings/sources", auth.requireAdmin(hazards.sourceList))
	mux.HandleFunc("POST /admin/settings/sources", auth.requireAdmin(hazards.sourceAdd))
	mux.HandleFunc("POST /admin/settings/sources/{id}/rename", auth.requireAdmin(hazards.sourceRename))
	mux.HandleFunc("POST /admin/settings/sources/{id}/archive", auth.requireAdmin(hazards.sourceArchive))
	mux.HandleFunc("POST /admin/settings/sources/{id}/restore", auth.requireAdmin(hazards.sourceRestore))
	mux.HandleFunc("POST /admin/settings/sources/{id}/delete", auth.requireAdmin(hazards.sourceDelete))

	// User management is admin-only (it is reached from the Settings card).
	mux.HandleFunc("GET /users", auth.requireAdmin(adminUsers.list))
	mux.HandleFunc("GET /users/new", auth.requireAdmin(adminUsers.newForm))
	mux.HandleFunc("POST /users/new", auth.requireAdmin(adminUsers.create))
	mux.HandleFunc("GET /users/{id}/edit", auth.requireAdmin(adminUsers.editForm))
	mux.HandleFunc("POST /users/{id}/edit", auth.requireAdmin(adminUsers.update))
	mux.HandleFunc("POST /users/{id}/archive", auth.requireAdmin(adminUsers.archive))
	mux.HandleFunc("POST /users/{id}/restore", auth.requireAdmin(adminUsers.restore))
	mux.HandleFunc("POST /users/{id}/reset-password", auth.requireAdmin(adminUsers.resetPassword))
	mux.HandleFunc("GET /users/{id}/delete", auth.requireAdmin(adminUsers.confirmDelete))
	mux.HandleFunc("POST /users/{id}/delete", auth.requireAdmin(adminUsers.delete))

	startSOCListener(db, store, evidenceDir, ref, companies, medpost)

	port := envOrDefault("GO_API_PORT", "8111")
	addr := "127.0.0.1:" + port
	log.Printf("hse-register API listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func runCreateAdmin(db *sql.DB, arg string) {
	parts := strings.SplitN(arg, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		log.Fatal("usage: -createadmin username:password")
	}
	hash, err := HashPassword(parts[1])
	if err != nil {
		log.Fatalf("failed to hash password: %v", err)
	}
	res, err := db.Exec(
		`INSERT INTO admin_users (username, password_hash, role) VALUES (?, ?, 'admin')
		 ON DUPLICATE KEY UPDATE password_hash = VALUES(password_hash), role = 'admin'`,
		parts[0], hash,
	)
	if err != nil {
		log.Fatalf("failed to create/update admin user: %v", err)
	}
	if affected, _ := res.RowsAffected(); affected == 2 {
		fmt.Printf("updated password for existing admin user %q\n", parts[0])
	} else {
		fmt.Printf("created admin user %q\n", parts[0])
	}
}
