package web

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"entry-permit/internal/company"
	"entry-permit/internal/dates"
	"entry-permit/internal/filestore"
	"entry-permit/internal/gateofficer"
	"entry-permit/internal/mail"
	"entry-permit/internal/malaria"
	"entry-permit/internal/permitlog"
	"entry-permit/internal/policy"
	"entry-permit/internal/position"
	"entry-permit/internal/ratelimit"
	"entry-permit/internal/reflist"
	"entry-permit/internal/reviewlog"
	"entry-permit/internal/session"
	"entry-permit/internal/settings"
	"entry-permit/internal/staff"
	"entry-permit/internal/ticketlog"
	"entry-permit/internal/turnstile"
	"entry-permit/internal/waiverlog"
	"entry-permit/internal/worker"
)

const sessionCookie = "ep_session"

// App is the web application: one home page, one login, one page per role.
type App struct {
	Store    *staff.Store
	Clock    dates.Clock
	Pol      policy.Policy
	PolicyFn func() policy.Policy // when set, wins over Pol (live Settings)
	Settings *settings.Service
	Officers *gateofficer.Store
	Permits  *permitlog.Store
	Waivers  *waiverlog.Store
	Tickets  *ticketlog.Store
	Reviews  *reviewlog.Store
	// Uploads keeps the permit documents; MXCheck says whether an email domain can
	// receive mail (nil skips the check, as in tests and development).
	Uploads filestore.Store
	// Positions resolves a worker's job title to a canonical position (does it need a certificate?).
	Positions *position.Store
	MXCheck   func(domain string) error
	// Companies is the read-only mirror of medpost's company list; CompanySource refreshes it.
	Companies     *company.Store
	CompanySource company.Source

	// Public lookup ("My Layar Entry Permit"). Nil Workers or Turnstile switches it off.
	Workers       worker.Source
	Turnstile     turnstile.Verifier
	TurnstileSite string
	TurnstileDev  bool
	TrustCFIP     bool
	Limit         *ratelimit.Limiter
	Secret        string
	Secure        bool         // set the cookie Secure flag (true when BASE_URL is https)
	BaseURL       string       // public address, used inside QR codes and link previews
	McuPortalURL  string       // medical checkup portal address for the landing page; empty hides its links
	Jobs          JobControl   // the scheduler, for the status page; nil when none runs
	Outbox        *mail.Outbox // every e-mail is stored here first; nil switches e-mail off
	Mailer        mail.Mailer  // nil when no SMTP server is configured: mail waits in the outbox
	GateKick      func()       // asks the gate sync to run now; nil when no gate Worker is configured
	DB            Pinger
	SQL           *sql.DB        // for the audit log pages
	Lists         *reflist.Store // admin-editable reference lists
	Malaria       *malaria.Service
	StaticDir     string
	tmpl          *template.Template
}

// NewApp parses the templates in webDir/templates once, at start.
func NewApp(webDir string, a App) (*App, error) {
	t, err := template.ParseGlob(filepath.Join(webDir, "templates", "*.html"))
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	a.tmpl = t
	a.StaticDir = filepath.Join(webDir, "static")
	return &a, nil
}

// policy is the current Policy: live from Settings when wired, else the fixed one.
func (a *App) policy() policy.Policy {
	if a.PolicyFn != nil {
		return a.PolicyFn()
	}
	return a.Pol
}

// Link is one entry in the header menu.
type Link struct {
	Label, Href string
	Current     bool
}

// View is the data every page template receives.
type View struct {
	Title         string
	User          *staff.User
	Photo         PhotoData
	Links         []Link
	Names         []staff.NameEntry
	Username      string // pre-selected name after a failed login
	Error         string
	Message       string
	CSRF          string
	Admin         AdminData
	Malaria       MalariaData
	Settings      SettingsData
	Users         UsersData
	My            MyData
	McuURL        string // the medical checkup portal address, for the side menu and the landing page
	Nav           string // which side menu item is current (see navKey); set by render
	Permit        PermitData
	Waiver        WaiverData
	Ticket        TicketData
	Review        ReviewData
	Card          CardData
	OG            OG // link-preview tags; the head template prints them only when Title is set
	CompaniesPage CompaniesData
	Dash          DashData
	Status        StatusData
	Next          string // page requested from the home page, carried through sign-in
	SignIn        SignInData
}

// Routes returns the full handler tree.
func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", Health(a.DB))
	mux.HandleFunc("GET /{$}", a.home)
	mux.HandleFunc("GET /request", a.requestPage)
	mux.HandleFunc("GET /my/signin", a.mySignIn)
	mux.HandleFunc("POST /my", a.myLookup)
	mux.HandleFunc("GET /my", a.myPage)
	mux.HandleFunc("POST /my/logout", a.myLogout)
	mux.HandleFunc("POST /my/photo", a.myPhoto)
	mux.HandleFunc("POST /my/ticket", a.myTicket)
	mux.HandleFunc("POST /my/permit.pdf", a.myPermitPDF)
	mux.HandleFunc("GET /api/worker-names", a.workerNames)
	mux.HandleFunc("GET /login", a.loginPage)
	mux.HandleFunc("POST /login", a.loginSubmit)
	mux.HandleFunc("POST /logout", a.logout)
	mux.HandleFunc("GET /staff", a.staffLanding)
	mux.HandleFunc("GET /staff/dashboard.pdf", a.dashboardPDF)
	mux.HandleFunc("GET /staff/{page}", a.staffPage)
	mux.HandleFunc("POST /staff/review", a.reviewSubmit)
	mux.HandleFunc("GET /staff/file", a.reviewFile)
	a.adminRoutes(mux)
	a.userRoutes(mux)
	mux.HandleFunc("GET /permit", a.permitPage)
	mux.HandleFunc("POST /permit", a.permitStart)
	mux.HandleFunc("POST /permit/submit", a.permitSubmit)
	mux.HandleFunc("GET /waiver", a.waiverPage)
	mux.HandleFunc("POST /waiver", a.waiverSubmit)
	mux.HandleFunc("GET /c/{lep}", a.cardPage)
	mux.HandleFunc("GET /c/{lep}/photo", a.cardPhotoFile)
	mux.HandleFunc("GET /ticket", a.ticketPage)
	mux.HandleFunc("POST /ticket", a.ticketAccess)
	mux.HandleFunc("POST /ticket/propose", a.ticketPropose)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(a.StaticDir))))
	return secure(sameOrigin(mux))
}

// secure adds headers. WHY the strict CSP: pages use no inline script or
// style, so "self" only is enough and blocks injected script.
func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		csp := "default-src 'self'; frame-ancestors 'none'; form-action 'self'"
		if r.URL.Path == "/my" || r.URL.Path == "/my/signin" || r.URL.Path == "/permit" || r.URL.Path == "/permit/submit" || r.URL.Path == "/waiver" || r.URL.Path == "/ticket" || r.URL.Path == "/ticket/propose" {
			csp = turnstileCSP // the Turnstile widget lives only on these pages
		}
		h.Set("Content-Security-Policy", csp)
		next.ServeHTTP(w, r)
	})
}

// sameOrigin refuses a POST whose Origin header names another site (CSRF).
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host {
					http.Error(w, "Forbidden", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) render(w http.ResponseWriter, name string, status int, v View) {
	// WHY: the address is an admin Setting (mcu_portal_url); the MCU_PORTAL_URL environment value is only the fallback
	// for a Settings row that does not exist yet.
	v.McuURL = a.policy().McuPortalURL
	if v.McuURL == "" {
		v.McuURL = a.McuPortalURL
	}
	if v.Nav == "" {
		v.Nav = navKey(name)
	}
	var buf bytes.Buffer // render fully first so an error never leaves half a page
	if err := a.tmpl.ExecuteTemplate(&buf, name, v); err != nil {
		http.Error(w, "Something went wrong", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// currentUser returns the logged-in active user, or nil.
func (a *App) currentUser(r *http.Request) *staff.User {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	id, err := session.Verify(a.Secret, c.Value, a.Clock.Now())
	if err != nil {
		return nil
	}
	u, err := a.Store.ByID(r.Context(), id)
	if err != nil || !u.Active {
		return nil
	}
	return &u
}

func (a *App) loginPage(w http.ResponseWriter, r *http.Request) {
	next := cleanNext(r.URL.Query().Get("next"))
	if u := a.currentUser(r); u != nil {
		http.Redirect(w, r, a.loginTarget(*u, next), http.StatusSeeOther)
		return
	}
	a.renderSignIn(w, r, http.StatusOK, a.myData(), SignInData{Step: 1, Next: next})
}

// loginRetry shows the sign-in form again after a failed attempt, keeping the username so only
// the PIN is retyped. An unknown username gets the same message as a wrong PIN.
func (a *App) loginRetry(w http.ResponseWriter, r *http.Request, status int, username, msg, next string) {
	a.renderSignIn(w, r, status, a.myData(), SignInData{Step: 1, Username: username, Next: next, Err: msg})
}

func (a *App) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(r.PostFormValue("username"))
	next := cleanNext(r.PostFormValue("next"))
	u, err := a.Store.Authenticate(r.Context(), username, r.PostFormValue("pin"))
	switch {
	case errors.Is(err, staff.ErrLocked):
		msg := fmt.Sprintf("Too many attempts. Please try again in %d minutes.", a.policy().LoginLockMinutes)
		a.loginRetry(w, r, http.StatusTooManyRequests, username, msg, next)
		return
	case errors.Is(err, staff.ErrBadLogin):
		a.loginRetry(w, r, http.StatusUnauthorized, username, "Username or PIN is incorrect.", next)
		return
	case err != nil:
		http.Error(w, "Something went wrong", http.StatusInternalServerError)
		return
	}
	expires := a.Clock.Now().AddHours(a.policy().SessionHours)
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: session.Sign(a.Secret, u.ID, expires), Path: "/",
		MaxAge: a.policy().SessionHours * 3600, HttpOnly: true, Secure: a.Secure, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, a.loginTarget(u, next), http.StatusSeeOther)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.Secure, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// staffLanding sends the user to their own page: this is the "just log in"
// behaviour the owner asked for.
func (a *App) staffLanding(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	path := staff.LandingPath(u.Roles)
	if path == "" {
		a.render(w, "staff", http.StatusForbidden, View{Title: "No access", User: u, Message: "Your account has no role yet. Ask an administrator."})
		return
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}

// pageMessage is the placeholder body until each role's page is built.
var pageMessage = map[staff.Role]string{
	staff.Admin:       "Settings, users and the reference lists will be here (slices 1c and 1d).",
	staff.GateOfficer: "The gate view of the dashboard will be here (slice 5c).",
	staff.Dashboard:   "The dashboard will be here (slice 5c).",
}

func (a *App) staffPage(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	role, ok := roleForSlug(r.PathValue("page"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	// Every staff member may open the dashboard; each other page, review queues included, needs
	// its own role. A review page only announces how many requests wait at the other stages.
	if role != staff.Dashboard && !u.HasRole(role) {
		a.render(w, "staff", http.StatusForbidden, View{Title: "No access", User: u, Links: menu(*u, ""), Message: "You do not have access to this page."})
		return
	}
	if isReviewPage(role) {
		a.reviewPage(w, r, u, role, http.StatusOK, "")
		return
	}
	switch role {
	case staff.Dashboard:
		a.dashboardPage(w, r, u)
		return
	case staff.GateOfficer:
		a.gatePage(w, r, u)
		return
	}
	a.render(w, "staff", http.StatusOK, View{Title: role.Label(), User: u, Links: menu(*u, role.Slug()), Message: pageMessage[role]})
}

func roleForSlug(slug string) (staff.Role, bool) {
	for _, r := range staff.Priority {
		if r.Slug() == slug {
			return r, true
		}
	}
	return "", false
}

// menu lists the user's own pages in priority order, then the dashboard
// (open to everyone, so reviewers can check status without leaving).
func menu(u staff.User, current string) []Link {
	var links []Link
	for _, r := range staff.Priority {
		if u.HasRole(r) {
			links = append(links, Link{Label: r.Label(), Href: "/staff/" + r.Slug(), Current: r.Slug() == current})
		}
	}
	if !u.HasRole(staff.Dashboard) {
		links = append(links, Link{Label: staff.Dashboard.Label(), Href: "/staff/dashboard", Current: current == "dashboard"})
	}
	// Reviewers and gate officers are site personnel (a gate officer must be a medpost worker with
	// a valid checkup), so they have a permit of their own: this opens the date of birth step for
	// their name. Viewer-only, admin and gate admin accounts are office roles (HR, settings) with
	// no permit, so they get no such link.
	if u.HasRole(staff.Doctor) || u.HasRole(staff.Stage1) || u.HasRole(staff.Final) || u.HasRole(staff.GateOfficer) {
		links = append(links, Link{Label: "My permit", Href: "/my/signin"})
	}
	return links
}

// navKey says which side menu item a template belongs to. Staff pages are marked through their own
// Links (menu), so they return "".
func navKey(template string) string {
	switch template {
	case "home":
		return "home"
	case "request", "permit", "permit_done", "waiver", "waiver_done":
		return "request"
	case "my", "my_photo", "my_signin", "ticket":
		return "my"
	case "signin":
		return "login"
	case "account":
		return "account"
	}
	return ""
}
