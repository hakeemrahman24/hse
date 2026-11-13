package web

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"entry-permit/internal/staff"
	"entry-permit/internal/worker"
)

// Limits on the public lookup (owner decision 2026-11-12): 5 failed tries, then a 15 minute wait,
// the same as the staff PIN and the working ticket. WHY: a worker mistypes a date a couple of
// times at most, while guessing a date of birth needs thousands of tries.
const (
	nameSearchesPerMinute = 30
	failedLookupsPerIP    = 5
	failedLookupsPerName  = 5
	ipWindow              = 15 * time.Minute
	nameWindow            = 15 * time.Minute
	searchWindow          = time.Minute

	turnstileField = "cf-turnstile-response"
	maxNameLen     = 120
)

// turnstileCSP lets the Cloudflare widget load. It is used only on the pages
// that show the widget; every other page keeps the strict policy.
const turnstileCSP = "default-src 'self'; script-src 'self' https://challenges.cloudflare.com; " +
	"frame-src https://challenges.cloudflare.com; connect-src 'self' https://challenges.cloudflare.com; " +
	"style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; form-action 'self'; base-uri 'none'"

// MyData is what the home page and the result page receive.
type MyData struct {
	Available  bool   // lookup switched on (medpost and Turnstile configured)
	SiteKey    string // Turnstile site key
	Dev        bool   // development: no widget, the token is filled in
	Name, DOB  string // kept so a failed try does not retype everything
	Err        string
	Person     *worker.Person
	Permits    []MyPermit
	PhotoToken string // signed proof for the "change my photo" form; empty when photos cannot be saved
	SignedIn   bool   // the page is the worker's own: it shows the sign out button
}

// PhotoURL is where the worker's current photo is served: the newest permit that has one. It is
// the card photo address, which only the worker and staff can open. Blank when there is none.
func (m MyData) PhotoURL() string {
	for _, p := range m.Permits {
		if p.HasPhoto && p.LEP != "" {
			return "/c/" + p.LEP + "/photo"
		}
	}
	return ""
}

// TicketPermits are the worker's permits that can take a working ticket (Valid ones), for the
// "My working tickets" option of the worker page.
func (m MyData) TicketPermits() []MyPermit {
	var out []MyPermit
	for _, p := range m.Permits {
		if p.CanTicket {
			out = append(out, p)
		}
	}
	return out
}

// lookupNotFound is the one answer for a wrong date or an unknown name, so nothing can be
// learned from which one it was.
const lookupNotFound = "We could not find that name and date of birth together. Check both and try again. Only workers with a valid MCU result of Fit or Fit with note can be looked up."

// permitEnded is said only after the name and date of birth were both proven, so it tells a
// worker nothing they did not already know about themselves.
const permitEnded = "Your permit validity has ended. Your medical checkup is no longer valid, so please do a new medical checkup first."

// loginTargets are the pages the home tiles can ask for. Anything else is ignored.
// WHY "review" and "admin": the Review tile does not know which review stage a person holds, so
// "review" means "your own review page"; "admin" is the Setting (gear) tile.
var loginTargets = map[string]bool{"stage1": true, "doctor": true, "final": true, "dashboard": true, "gate": true, "review": true, "admin": true}

// reviewTileRoles is the order the "review" tile looks for a role, last stage first.
var reviewTileRoles = []staff.Role{staff.Final, staff.Stage1, staff.Doctor}

func (a *App) lookupAvailable() bool {
	return a.Workers != nil && a.Turnstile != nil && (a.TurnstileDev || a.TurnstileSite != "")
}

func (a *App) myData() MyData {
	return MyData{Available: a.lookupAvailable(), SiteKey: a.TurnstileSite, Dev: a.TurnstileDev}
}

func (a *App) home(w http.ResponseWriter, r *http.Request) {
	// Cards printed by the old system point at "/?page=ticket&lep=...".
	if to := a.scanTarget(r); to != "" {
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	d := a.myData()
	_, _, d.SignedIn = a.workerFromCookie(r)
	v := View{Title: "Home", User: a.currentUser(r), My: d}
	if v.User != nil {
		v.Links = menu(*v.User, "")
	}
	a.render(w, "home", http.StatusOK, v)
}

// mySignIn is the worker sign in: name, date of birth and the Turnstile check in one form.
func (a *App) mySignIn(w http.ResponseWriter, r *http.Request) {
	d := a.myData()
	_, _, d.SignedIn = a.workerFromCookie(r)
	a.render(w, "my_signin", http.StatusOK, View{Title: "My Entry Permit", User: a.currentUser(r), My: d})
}

// requestPage asks which kind of entry permit to request: work or visitor. No sign in needed.
func (a *App) requestPage(w http.ResponseWriter, r *http.Request) {
	a.render(w, "request", http.StatusOK, View{Title: "Request Entry Permit", User: a.currentUser(r)})
}

func (a *App) clientIP(r *http.Request) string {
	if a.TrustCFIP {
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// workerNames feeds the searchable name list. It answers nothing under four
// characters, at most ten names, and only names (no dates, no companies).
func (a *App) workerNames(w http.ResponseWriter, r *http.Request) {
	a.searchNames(w, r, "s:"+a.clientIP(r))
}

// searchNames answers a name search, limited per limiter key.
func (a *App) searchNames(w http.ResponseWriter, r *http.Request, key string) {
	empty := map[string][]string{"names": {}}
	if a.Limit.Count(key, searchWindow) >= nameSearchesPerMinute {
		writeJSON(w, http.StatusTooManyRequests, empty)
		return
	}
	a.Limit.Add(key)
	if a.Workers == nil {
		writeJSON(w, http.StatusServiceUnavailable, empty)
		return
	}
	names, err := a.Workers.Names(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		log.Printf("worker names: %v", err)
		writeJSON(w, http.StatusBadGateway, empty)
		return
	}
	if names == nil {
		names = []string{}
	}
	writeJSON(w, http.StatusOK, map[string][]string{"names": names})
}

// myLookup proves a worker is who they say (name plus their own date of birth,
// behind Turnstile) and then shows what is held about them.
func (a *App) myLookup(w http.ResponseWriter, r *http.Request) {
	d := a.myData()
	// A failed try shows the date of birth step again for that name, not the home page, so the
	// person fixes the date without picking the name again.
	fail := func(status int, msg string) {
		a.renderSignIn(w, r, status, d, SignInData{Step: 2, Name: d.Name, Err: msg})
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	d.Name = strings.Join(strings.Fields(r.PostFormValue("name")), " ")
	d.DOB = strings.TrimSpace(r.PostFormValue("dob"))
	e, status, msg := a.verifyWorker(r, d.Name, d.DOB, true)
	if status != 0 {
		fail(status, msg)
		return
	}
	// WHY checked here as well as in medpost: a permit lasts as long as the medical checkup, so a
	// worker whose checkup has run out is not let in, even if the lookup source still returns them.
	// The last valid day itself still works (days are yyyy-mm-dd text and sort in date order).
	if e.McuValidUntil != "" && e.McuValidUntil < string(a.Clock.Today()) {
		fail(http.StatusForbidden, permitEnded)
		return
	}
	token := a.photoToken(e)
	a.setWorkerCookie(w, token)
	a.renderMy(w, r, token, e)
}

// workerCookie holds the signed proof (photoClaims) of a worker who looked themselves up. WHY a
// cookie: the sidebar entries of the worker's page (my permit, my working tickets, photos) and the
// pages they lead to must open again without asking for the name and birth date a second time.
// It carries no date of birth, only what the page already showed, and it expires with the proof.
const workerCookie = "ep_worker"

func (a *App) setWorkerCookie(w http.ResponseWriter, token string) {
	if token == "" {
		return // photos and permits are off, so there is nothing to stay signed in to
	}
	http.SetCookie(w, &http.Cookie{
		Name: workerCookie, Value: token, Path: "/", MaxAge: photoTokenHours * 3600,
		HttpOnly: true, Secure: a.Secure, SameSite: http.SameSiteLaxMode,
	})
}

// workerFromCookie is the proven worker of this browser, or false when there is none or it ran out.
func (a *App) workerFromCookie(r *http.Request) (photoClaims, string, bool) {
	c, err := r.Cookie(workerCookie)
	if err != nil {
		return photoClaims{}, "", false
	}
	claims, ok := verifyPhoto(a.Secret, c.Value, a.Clock.Now())
	return claims, c.Value, ok
}

// renderMy shows the worker's own page for a record medpost just confirmed or the cookie carries.
func (a *App) renderMy(w http.ResponseWriter, r *http.Request, token string, e worker.Employee) {
	p := worker.Person{Name: e.Name, DOB: e.DOB, Company: e.Company, Position: e.Position, McuValidUntil: e.McuValidUntil, McuResult: e.McuResult, McuNote: e.McuNote}
	my := MyData{Person: &p, PhotoToken: token, Permits: a.myPermits(r, e), SignedIn: true}
	w.Header().Set("Cache-Control", "no-store")
	a.render(w, "my", http.StatusOK, View{Title: "My Layar Entry Permit", User: a.currentUser(r), My: my, Links: my.Links()})
}

// Links are the worker's options, shown in the blue column. WHY the working tickets option is a
// link to the schedule itself when a permit is Valid: the option is the action, so the page needs
// no second "propose" button; the schedule opens without a birth date for the signed-in owner.
// Without a Valid permit it points at a short explanation on the same page.
func (m MyData) Links() []Link {
	tickets := "#my-tickets"
	if valid := m.TicketPermits(); len(valid) > 0 {
		tickets = "/ticket?lep=" + valid[0].LEP
	}
	return []Link{
		{Label: "My permit", Href: "#my-permit"},
		{Label: "My working tickets", Href: tickets},
		{Label: "Photos", Href: "#my-photos"},
	}
}

// myPage reopens the worker's page for a browser that is still signed in. Without a valid cookie
// the worker goes to the sign-in page, the same place a lookup starts from.
func (a *App) myPage(w http.ResponseWriter, r *http.Request) {
	claims, token, ok := a.workerFromCookie(r)
	if !ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	a.renderMy(w, r, token, worker.Employee{
		PatientID: claims.PatientID, PersonalID: claims.PersonalID, Name: claims.Name, Company: claims.Company,
		Position: claims.Position, McuResult: claims.McuResult, McuNote: claims.McuNote, McuValidUntil: claims.McuValidUntil,
	})
}

// myLogout ends the worker's sign-in on this device.
func (a *App) myLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: workerCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.Secure, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// verifyWorker is the proof every public page needs before it shows a worker's
// record: the name was picked, the date of birth typed (dd/mm/yyyy), Turnstile
// passed, and medpost holds exactly one eligible worker with that name and date.
// It returns the record, or a non-zero HTTP status and the message to show.
//
// WHY one function: /my and /permit must apply identical limits, so a tightened
// rule can never reach one page and miss the other. Order matters: locks are
// checked before medpost is asked, Turnstile before the lookup.
func (a *App) verifyWorker(r *http.Request, name, dob string, includeExpired bool) (worker.Employee, int, string) {
	ip := a.clientIP(r)
	nameKey := "fn:" + strings.ToLower(name)
	switch {
	case !a.lookupAvailable():
		return worker.Employee{}, http.StatusServiceUnavailable, "This lookup is not available right now. Please try again later."
	case a.Limit.Count("f:"+ip, ipWindow) >= failedLookupsPerIP || a.Limit.Count(nameKey, nameWindow) >= failedLookupsPerName:
		// Checked before medpost is asked, so a locked address costs nothing.
		return worker.Employee{}, http.StatusTooManyRequests, "Too many attempts. Please try again in 15 minutes."
	case len([]rune(name)) < worker.MinQuery || len([]rune(name)) > maxNameLen:
		return worker.Employee{}, http.StatusBadRequest, "Pick the name from the list (type at least 4 letters)."
	}
	dobISO, err := worker.ParseDOB(dob, string(a.Clock.Today()))
	if err != nil {
		return worker.Employee{}, http.StatusBadRequest, err.Error()
	}
	ok, err := a.Turnstile.Verify(r.Context(), r.PostFormValue(turnstileField), ip)
	if err != nil {
		log.Printf("turnstile: %v", err)
		return worker.Employee{}, http.StatusServiceUnavailable, "The security check could not be completed. Please try again."
	}
	if !ok {
		a.Limit.Add("f:" + ip)
		return worker.Employee{}, http.StatusBadRequest, "The security check failed. Please try again."
	}
	pick := a.Workers.Pick
	if includeExpired { // only the "My Layar Entry Permit" page, which tells such a worker their checkup ended
		pick = a.Workers.PickIncludingExpired
	}
	e, found, err := pick(r.Context(), name, dobISO)
	if err != nil {
		log.Printf("worker lookup: %v", err)
		return worker.Employee{}, http.StatusServiceUnavailable, "This lookup is not available right now. Please try again later."
	}
	if !found {
		a.Limit.Add("f:" + ip)
		a.Limit.Add(nameKey)
		// One message for a wrong date and an unknown name, so nothing can be enumerated.
		return worker.Employee{}, http.StatusUnauthorized, lookupNotFound
	}
	// Medpost has confirmed this person by name and birth date: link any earlier permit that is
	// still unlinked (the spreadsheet import has no patient id). A failure here must not stop the
	// worker, so it is logged without names or ids and the page carries on.
	if a.Permits != nil {
		if n, err := a.Permits.Adopt(r.Context(), e.PatientID, e.PersonalID, e.Name, dobISO); err != nil {
			log.Printf("link earlier permits: %v", err)
		} else if n > 0 {
			log.Printf("linked %d earlier permit(s) to a confirmed worker", n)
		}
	}
	return e, 0, ""
}

// loginTarget turns the requested page into the path to open after sign-in: the page itself when this
// person may open it, else the shared landing page "/" (owner decision 2026-11-13: staff use the same
// landing page as everyone, where a door lists their own pages).
func (a *App) loginTarget(u staff.User, next string) string {
	if next == "review" {
		for _, r := range reviewTileRoles {
			if u.HasRole(r) {
				return "/staff/" + r.Slug()
			}
		}
		return "/"
	}
	if loginTargets[next] {
		if role, ok := roleForSlug(next); ok && (role == staff.Dashboard || u.HasRole(role)) {
			return "/staff/" + role.Slug()
		}
	}
	return "/"
}

// cleanNext returns next only when it is a known page name.
func cleanNext(next string) string {
	if loginTargets[next] {
		return next
	}
	return ""
}
