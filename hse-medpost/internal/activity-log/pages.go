package activitylog

import (
	"database/sql"
	"html/template"
	"net/http"
	"path/filepath"

	"hse-medpost/internal/middleware"
)

type Pages struct {
	Templates *template.Template
	DB        *sql.DB
}

func LoadPages(dir string) (*Pages, error) {
	t, err := template.ParseGlob(filepath.Join(dir, "*.html"))
	if err != nil {
		return nil, err
	}
	return &Pages{Templates: t}, nil
}

// Every page here is session-dependent (role, name) and can change between
// deploys, so browsers must never serve a cached copy — a stale Settings
// page silently hiding role-gated sections has bitten us more than once.
func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}

func (p *Pages) Login(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if middleware.Session(r) != nil {
		http.Redirect(w, r, "/start", http.StatusSeeOther)
		return
	}
	_ = p.Templates.ExecuteTemplate(w, "login.html", nil)
}

// Start is the post-login landing page (also what clicking the "medpost"
// logo returns to from any module) — pick which of EMR/Inventory/Activity
// Log/Settings to open. Replaces the old always-visible top-bar module
// switcher, which got cramped once there were both page links and module
// links in one nav.
func (p *Pages) Start(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	sess := middleware.Session(r)
	_ = p.Templates.ExecuteTemplate(w, "start.html", map[string]any{
		"Name":           sess.Name,
		"Role":           sess.Role,
		"MedicalCheckup": middleware.MayOpenMedicalCheckup(p.DB, sess.StaffID),
	})
}

func (p *Pages) Programs(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	sess := middleware.Session(r)
	_ = p.Templates.ExecuteTemplate(w, "programs.html", map[string]any{
		"Name":    sess.Name,
		"Role":    sess.Role,
		"StaffID": sess.StaffID,
		"ProcID":  "",
		"Initial": initial(sess.Name),
	})
}

func (p *Pages) ProgramDetail(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	sess := middleware.Session(r)
	_ = p.Templates.ExecuteTemplate(w, "programs.html", map[string]any{
		"Name":    sess.Name,
		"Role":    sess.Role,
		"StaffID": sess.StaffID,
		"ProcID":  r.PathValue("id"),
		"Initial": initial(sess.Name),
	})
}

func (p *Pages) Settings(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	sess := middleware.Session(r)
	_ = p.Templates.ExecuteTemplate(w, "settings.html", map[string]any{
		"Name":    sess.Name,
		"Role":    sess.Role,
		"Initial": initial(sess.Name),
	})
}

func initial(name string) string {
	for _, r := range name {
		return string(r)
	}
	return "?"
}
