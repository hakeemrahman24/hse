// Package sofi serves the public fatigue screening pages at /sofi: any employee on file signs in
// with their name and date of birth, the same way as the SOC page in hse-register. These routes
// have no staff session; they use their own cookie and signing key.
package sofi

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"hse-medpost/internal/auth"
	"hse-medpost/internal/ratelimit"
	"hse-medpost/internal/turnstile"
)

const (
	// CookieName is deliberately not "session": that name belongs to staff.
	CookieName = "sofi_session"

	// CheckCookieName holds the proof that the Cloudflare check passed.
	CheckCookieName = "sofi_check"

	roleSession = "sofi"
	rolePick    = "sofi-pick"
	roleCheck   = "sofi-check"

	sessionTTL = 2 * time.Hour
	pickTTL    = 10 * time.Minute
	checkTTL   = 10 * time.Minute
	// checkMax Cloudflare checks per address inside checkTTL.
	checkMax = 20

	minQueryLetters = 3
	maxNames        = 10

	// failMax wrong sign-ins inside failWindow lock that address (and that person) for the rest of it.
	failMax    = 5
	failWindow = 15 * time.Minute
	// searchMax name searches a minute from one address.
	searchMax    = 60
	searchWindow = time.Minute
)

const msgNotFound = "Details not found, please check and try again."

// API holds what the /sofi handlers need.
type API struct {
	DB            *sql.DB
	store         *auth.Store
	pages         *template.Template
	lim           *ratelimit.Limiter
	trustCF       bool // read the visitor address from CF-Connecting-IP (only when the port is reachable through the tunnel alone)
	secureCookie  bool
	verifier      turnstile.Verifier
	now           func() time.Time // replaced in tests
	sitekey       string
	devCheck      bool        // show a stub button instead of the Cloudflare box
	submitMu      sync.Mutex  // makes "already submitted?" and the insert one step
	previewWarned atomic.Bool // the missing-preview-table warning is logged once, not on every request
}

// New builds the handlers. The signing key is derived from the staff session secret, so a staff
// token is never a valid SOFI token and the other way round.
//
// The templates live in web/templates/sofi/, not beside the staff pages: the staff server parses
// every web/templates/*.html with one set of functions, and these need the "tr" function.
func New(database *sql.DB, sessionSecret, templateDir string) (*API, error) {
	pages, err := template.New("sofi").Funcs(template.FuncMap{"tr": tr, "lower": strings.ToLower}).ParseFiles(
		filepath.Join(templateDir, "sofi", "sofi_signin.html"),
		filepath.Join(templateDir, "sofi", "sofi_home.html"),
		filepath.Join(templateDir, "sofi", "sofi_closed.html"),
	)
	if err != nil {
		return nil, fmt.Errorf("sofi templates: %w", err)
	}
	sum := sha256.Sum256([]byte("sofi|" + sessionSecret))
	return &API{
		DB:    database,
		store: auth.NewStore(hex.EncodeToString(sum[:])),
		pages: pages,
		lim:   ratelimit.New(),
		now:   time.Now,
	}, nil
}

// TrustCloudflareHeader makes the rate limits use CF-Connecting-IP and marks the cookie Secure.
func (a *API) TrustCloudflareHeader(on bool) {
	a.trustCF = on
	a.secureCookie = on
}

// SetTurnstile sets how the Cloudflare check is shown and verified. Without it nothing can pass
// the check, so name search and sign-in stay closed.
func (a *API) SetTurnstile(v turnstile.Verifier, sitekey string, dev bool) {
	a.verifier, a.sitekey, a.devCheck = v, sitekey, dev
}

// Register adds the /sofi routes to mux.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /sofi/api/check", a.Check)
	mux.HandleFunc("GET /sofi/lang", a.SetLang)
	mux.HandleFunc("GET /sofi", a.SignInPage)
	mux.HandleFunc("GET /sofi/start", a.Start)
	mux.HandleFunc("GET /sofi/api/names", a.Names)
	mux.HandleFunc("POST /sofi/api/signin", a.SignIn)
	mux.HandleFunc("POST /sofi/api/submit", a.Submit)
	mux.HandleFunc("POST /sofi/signout", a.SignOut)
}

func noStore(w http.ResponseWriter) { w.Header().Set("Cache-Control", "no-store") }

func writeJSON(w http.ResponseWriter, status int, v any) {
	noStore(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError answers in the visitor's language; a message with no Indonesian text stays English.
func writeError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": tr(langOf(r), msg)})
}

func (a *API) clientIP(r *http.Request) string {
	if a.trustCF {
		if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// session returns the signed-in employee, or nil.
func (a *API) session(r *http.Request) *auth.Session {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return nil
	}
	s, err := a.store.Verify(c.Value)
	if err != nil || s.Role != roleSession || s.UserID == 0 {
		return nil
	}
	return s
}

// pageData is everything the two templates need. Lang is the language shown; Path is where the
// language switch returns to.
type pageData struct {
	Lang      string
	Langs     []Language
	Path      string
	Name      string
	Sitekey   string
	DevCheck  bool
	Profile   Profile
	Items     []Item
	Scale     []int
	LevelHelp map[string]string
	// The choices for "weeks on site": WeekChoices, then one more option worth WeeksOverYear.
	WeekChoices   []int
	WeeksOverYear int
	// The person's own earlier results, newest first (empty the first time).
	Past []PastResult
	// Done: they already sent the questionnaire in the period they signed in under, so no form.
	Done bool
	// Preview: the administrator switched preview on, so nothing sent here is saved.
	Preview bool
}

func (a *API) pageData(r *http.Request, path string) pageData {
	return pageData{Lang: langOf(r), Langs: languages, Path: path, Preview: a.previewActive()}
}

var scalePoints = func() []int {
	s := make([]int, MaxAnswer+1)
	for i := range s {
		s[i] = i
	}
	return s
}()

// levelHelp is the sentence shown under each overall level.
var levelHelp = map[string]string{
	LevelLow:      "Your fatigue is low. Keep resting well.",
	LevelModerate: "Your fatigue is moderate. Rest and drink water, and tell your supervisor if it gets worse.",
	LevelHigh:     "Your fatigue is high. Tell your supervisor and the Medical Team before doing high-risk work.",
}

// checked reports whether this browser has passed the Cloudflare check recently.
func (a *API) checked(r *http.Request) bool {
	c, err := r.Cookie(CheckCookieName)
	if err != nil {
		return false
	}
	s, err := a.store.Verify(c.Value)
	return err == nil && s.Role == roleCheck
}

// Check verifies the Cloudflare token. Until it passes, name search and sign-in stay closed.
func (a *API) Check(w http.ResponseWriter, r *http.Request) {
	const failed = "Check failed, please try again."
	if _, ok := a.requireOpen(w, r); !ok {
		return
	}
	ip := a.clientIP(r)
	if a.lim.Count("c:"+ip, checkTTL) >= checkMax {
		writeError(w, r, http.StatusTooManyRequests, "Please wait a moment and try again.")
		return
	}
	a.lim.Add("c:" + ip)
	var in struct {
		Token string `json:"token"`
	}
	if a.verifier == nil || json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil {
		writeError(w, r, http.StatusBadRequest, failed)
		return
	}
	ok, err := a.verifier.Verify(r.Context(), in.Token, ip)
	if err != nil || !ok {
		writeError(w, r, http.StatusBadRequest, failed)
		return
	}
	token, err := a.store.IssueFor(auth.Session{UserID: 1, Role: roleCheck}, checkTTL)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "Something went wrong.")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CheckCookieName,
		Value:    token,
		Path:     "/sofi",
		HttpOnly: true,
		Secure:   a.secureCookie,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(checkTTL.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// SignInPage shows the sign-in form, or goes straight on when already signed in.
func (a *API) SignInPage(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if a.session(r) != nil {
		http.Redirect(w, r, "/sofi/start", http.StatusSeeOther)
		return
	}
	if !a.pageOpen(w, r, "/sofi") {
		return
	}
	d := a.pageData(r, "/sofi")
	d.Sitekey, d.DevCheck = a.sitekey, a.devCheck
	_ = a.pages.ExecuteTemplate(w, "sofi_signin.html", d)
}

// Start is the questionnaire, shown after sign-in.
func (a *API) Start(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	s := a.session(r)
	if s == nil {
		http.Redirect(w, r, "/sofi", http.StatusSeeOther)
		return
	}
	if !a.startAllowed(w, r, s) {
		return
	}
	p, err := a.profile(s.UserID)
	if err != nil {
		log.Printf("sofi: start profile: %v", err)
		http.Error(w, tr(langOf(r), "Something went wrong."), http.StatusInternalServerError)
		return
	}
	d := a.pageData(r, "/sofi/start")
	d.Name, d.Profile, d.Items, d.Scale, d.LevelHelp = s.Name, p, Items, scalePoints, levelHelp
	d.WeekChoices, d.WeeksOverYear = weekChoices, WeeksOverYear
	if winID, werr := a.sessionWindowID(s); werr == nil && winID > 0 {
		d.Done, err = a.hasSubmitted(s.UserID, winID)
		if err != nil {
			log.Printf("sofi: start submitted check: %v", err)
			http.Error(w, tr(langOf(r), "Something went wrong."), http.StatusInternalServerError)
			return
		}
	}
	if d.Past, err = a.pastResults(s.UserID); err != nil {
		log.Printf("sofi: start past results: %v", err)
		http.Error(w, tr(langOf(r), "Something went wrong."), http.StatusInternalServerError)
		return
	}
	_ = a.pages.ExecuteTemplate(w, "sofi_home.html", d)
}

type namePick struct {
	Ref     string `json:"ref"`
	Label   string `json:"label"`
	Company string `json:"company"`
}

// Names lists employees whose name contains the typed text. Only name and company leave the
// server; the ref is a signed, short-lived pointer to the person, not the patient number.
func (a *API) Names(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireOpen(w, r); !ok {
		return
	}
	if !a.checked(r) {
		writeError(w, r, http.StatusForbidden, "Please complete the check first.")
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) < minQueryLetters {
		writeJSON(w, http.StatusOK, []namePick{})
		return
	}
	ip := a.clientIP(r)
	if a.lim.Count("s:"+ip, searchWindow) >= searchMax {
		writeError(w, r, http.StatusTooManyRequests, "Too many searches, please wait a minute.")
		return
	}
	a.lim.Add("s:" + ip)

	out, err := a.findNames(q)
	if err != nil {
		log.Printf("sofi: name search: %v", err)
		writeError(w, r, http.StatusInternalServerError, "Name search is not available right now.")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) findNames(q string) ([]namePick, error) {
	rows, err := a.DB.Query(`
		SELECT p.PatientID, p.Name, COALESCE(c.Name, '')
		FROM Patient p
		LEFT JOIN Company c ON c.CompanyID = p.FK_CompanyID
		WHERE LOWER(p.Name) LIKE ? ESCAPE '!'
		  AND TRIM(COALESCE(p.DateOfBirth, '')) <> ''
		ORDER BY p.Name
		LIMIT ?`, "%"+likeEscape(strings.ToLower(q))+"%", maxNames)
	if err != nil {
		return nil, fmt.Errorf("search patients: %w", err)
	}
	defer rows.Close()
	out := []namePick{}
	for rows.Next() {
		var id int64
		var p namePick
		if err := rows.Scan(&id, &p.Label, &p.Company); err != nil {
			return nil, fmt.Errorf("scan patient: %w", err)
		}
		if p.Ref, err = a.store.IssueFor(auth.Session{UserID: id, Role: rolePick}, pickTTL); err != nil {
			return nil, fmt.Errorf("issue ref: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// likeEscape makes s match literally inside LIKE ... ESCAPE '!'. The escape character is "!" and
// not a backslash because MariaDB reads a backslash inside a string literal as an escape itself.
func likeEscape(s string) string {
	return strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(s)
}

// SignIn checks the chosen person's date of birth. Every failure gives the same answer, and the
// date of birth is compared and dropped: it is never stored, logged or echoed back.
func (a *API) SignIn(w http.ResponseWriter, r *http.Request) {
	fail := func() { writeError(w, r, http.StatusUnauthorized, msgNotFound) }
	// The window is recorded in the session so the person can finish even if it closes meanwhile.
	win, ok := a.requireOpen(w, r)
	if !ok {
		return
	}
	if !a.checked(r) {
		writeError(w, r, http.StatusForbidden, "Please complete the check first.")
		return
	}
	ip := a.clientIP(r)
	if a.lim.Count("ip:"+ip, failWindow) >= failMax {
		fail()
		return
	}
	var in struct {
		Ref string `json:"ref"`
		DOB string `json:"dob"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		fail()
		return
	}
	pick, err := a.store.Verify(in.Ref)
	if err != nil || pick.Role != rolePick || pick.UserID == 0 {
		a.lim.Add("ip:" + ip)
		fail()
		return
	}
	person := fmt.Sprintf("p:%d", pick.UserID)
	if a.lim.Count(person, failWindow) >= failMax {
		fail()
		return
	}
	var name, stored string
	err = a.DB.QueryRow(`SELECT Name, COALESCE(DateOfBirth, '') FROM Patient WHERE PatientID = ?`, pick.UserID).Scan(&name, &stored)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeError(w, r, http.StatusServiceUnavailable, "Sign-in is not available right now. Please try again later.")
		return
	}
	if !sameDate(stored, in.DOB) {
		a.lim.Add("ip:" + ip)
		a.lim.Add(person)
		fail()
		return
	}
	token, err := a.store.IssueFor(auth.Session{UserID: pick.UserID, Name: name, Role: roleSession, WindowID: win.ID}, sessionTTL)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "Something went wrong.")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/sofi",
		HttpOnly: true,
		Secure:   a.secureCookie,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]string{"redirect": "/sofi/start"})
}

// sameDate compares the stored yyyy-MM-dd with the typed one. An empty stored date never matches.
func sameDate(stored, given string) bool {
	stored = strings.TrimSpace(stored)
	if len(stored) > 10 {
		stored = stored[:10]
	}
	if stored == "" {
		return false
	}
	if _, err := time.Parse("2006-01-02", given); err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(given)) == 1
}

// SignOut clears the cookie and returns to the sign-in page.
func (a *API) SignOut(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/sofi", HttpOnly: true, MaxAge: -1})
	http.Redirect(w, r, "/sofi", http.StatusSeeOther)
}
