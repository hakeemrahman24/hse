package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"entry-permit/internal/audit"
	"entry-permit/internal/gateofficer"
	"entry-permit/internal/malaria"
	"entry-permit/internal/reflist"
	"entry-permit/internal/region"
	"entry-permit/internal/settings"
	"entry-permit/internal/staff"
)

const (
	auditPageSize = 200
	maxFlashLen   = 200 // longest message accepted back from the query string
)

// AdminData is what the admin templates receive, inside View.
type AdminData struct {
	Defs         []reflist.Def
	Super        bool // full admin; false = Gate admin (gate lists and officers only)
	Officers     []gateofficer.Officer
	Def          reflist.Def
	Rows         []reflist.Item
	Span         int // table columns, for the "no items" row
	Q            string
	ShowArchived bool
	Flash, Err   string
	Audit        []audit.Entry
	// StatusWarnings counts the things the status page says need attention (overview tile).
	StatusWarnings int
	// MalariaOrphans counts listed regencies that medpost no longer has (overview warning).
	MalariaOrphans int
}

func (a *App) adminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /staff/admin", a.adminHome)
	mux.HandleFunc("GET /staff/admin/lists/{list}", a.adminList)
	mux.HandleFunc("POST /staff/admin/lists/{list}", a.adminListAdd)
	mux.HandleFunc("POST /staff/admin/lists/{list}/{id}", a.adminListItem)
	mux.HandleFunc("GET /staff/admin/audit", a.adminAudit)
	mux.HandleFunc("GET /staff/admin/status", a.adminStatus)
	mux.HandleFunc("POST /staff/admin/status/run/{job}", a.adminStatusRun)
	mux.HandleFunc("POST /staff/admin/status/mail/{id}/retry", a.adminStatusRetry)
	mux.HandleFunc("GET /staff/gate-admin", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/staff/admin", http.StatusSeeOther) })
	mux.HandleFunc("GET /staff/admin/gate-officers", a.adminOfficers)
	mux.HandleFunc("POST /staff/admin/gate-officers", a.adminOfficerAdd)
	mux.HandleFunc("GET /staff/admin/employee-names", a.adminEmployeeNames)
	mux.HandleFunc("POST /staff/admin/gate-officers/{id}", a.adminOfficerAction)
	mux.HandleFunc("GET /staff/admin/settings", a.adminSettings)
	mux.HandleFunc("POST /staff/admin/settings", a.adminSettingsSave)
	mux.HandleFunc("GET /staff/admin/companies", a.adminCompanies)
	mux.HandleFunc("POST /staff/admin/companies/refresh", a.adminCompaniesRefresh)
	mux.HandleFunc("GET /staff/admin/malaria", a.adminMalaria)
	mux.HandleFunc("POST /staff/admin/malaria", a.adminMalariaSet)
	mux.HandleFunc("POST /staff/admin/malaria/orphan/{id}", a.adminMalariaOrphan)
}

// csrfToken is an HMAC of the session cookie, so a page from another site
// (which cannot read our cookie) cannot build a valid form. It is checked on
// every admin POST, on top of the Origin check and SameSite=Lax.
func (a *App) csrfToken(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	h := hmac.New(sha256.New, []byte(a.Secret))
	h.Write([]byte("csrf|" + c.Value))
	return hex.EncodeToString(h.Sum(nil))
}

func (a *App) csrfOK(r *http.Request) bool {
	want := a.csrfToken(r)
	return want != "" && hmac.Equal([]byte(want), []byte(r.PostFormValue("_csrf")))
}

// adminUser returns the signed-in admin, or answers the request itself
// (login redirect or 403) and returns nil. Called first in every admin
// handler, so the role is re-checked on the server each time.
func (a *App) adminUser(w http.ResponseWriter, r *http.Request) *staff.User {
	u := a.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return nil
	}
	if !u.HasRole(staff.Admin) {
		a.render(w, "staff", http.StatusForbidden, View{Title: "No access", User: u, Links: menu(*u, ""), Message: "You do not have access to this page."})
		return nil
	}
	return u
}

// scopedUser is like adminUser but also lets a Gate admin in when gateOK is
// true. The check runs on the server in every handler.
func (a *App) scopedUser(w http.ResponseWriter, r *http.Request, gateOK bool) *staff.User {
	u := a.currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return nil
	}
	if !u.HasRole(staff.Admin) && !(gateOK && u.HasRole(staff.GateAdmin)) {
		a.render(w, "staff", http.StatusForbidden, View{Title: "No access", User: u, Links: menu(*u, ""), Message: "You do not have access to this page."})
		return nil
	}
	return u
}

// scopedPost is scopedUser plus form parsing and the CSRF check.
func (a *App) scopedPost(w http.ResponseWriter, r *http.Request, gateOK bool) *staff.User {
	u := a.scopedUser(w, r, gateOK)
	if u == nil {
		return nil
	}
	if err := r.ParseForm(); err != nil || !a.csrfOK(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return nil
	}
	return u
}

// adminPost is adminUser plus form parsing and the CSRF check.
func (a *App) adminPost(w http.ResponseWriter, r *http.Request) *staff.User {
	u := a.adminUser(w, r)
	if u == nil {
		return nil
	}
	if err := r.ParseForm(); err != nil || !a.csrfOK(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return nil
	}
	return u
}

func (a *App) adminView(r *http.Request, u *staff.User, title string, data AdminData) View {
	data.Super = u.HasRole(staff.Admin)
	data.Defs = reflist.Defs
	if !data.Super {
		data.Defs = nil
		for _, d := range reflist.Defs {
			if d.Gate {
				data.Defs = append(data.Defs, d)
			}
		}
	}
	return View{Title: title, User: u, Links: menu(*u, "admin"), CSRF: a.csrfToken(r), Admin: data}
}

func (a *App) adminHome(w http.ResponseWriter, r *http.Request) {
	u := a.scopedUser(w, r, true)
	if u == nil {
		return
	}
	data := AdminData{}
	if a.Malaria != nil && u.HasRole(staff.Admin) {
		// A region list that cannot be read just means no count is shown.
		if n, err := a.Malaria.OrphanCount(r.Context()); err == nil {
			data.MalariaOrphans = n
		}
	}
	if u.HasRole(staff.Admin) {
		// A status that cannot be read just means no count is shown on the tile.
		if d, err := a.statusData(r.Context()); err == nil {
			data.StatusWarnings = len(d.Warnings)
		}
	}
	a.render(w, "admin_home", http.StatusOK, a.adminView(r, u, "Administration", data))
}

func flash(r *http.Request, key string) string {
	v := r.URL.Query().Get(key)
	if len(v) > maxFlashLen {
		return ""
	}
	return v
}

// listDef finds the list in the path and checks the caller may use it: a full
// admin any list, a Gate admin only the gate lists. It answers the request
// itself and returns false when it refuses.
func (a *App) listDef(w http.ResponseWriter, r *http.Request, post bool) (*staff.User, reflist.Def, bool) {
	def, ok := reflist.Find(r.PathValue("list"))
	if !ok {
		// Not signed in or not allowed: say so before revealing which lists exist.
		if u := a.scopedUser(w, r, true); u != nil {
			http.NotFound(w, r)
		}
		return nil, def, false
	}
	var u *staff.User
	if post {
		u = a.scopedPost(w, r, def.Gate)
	} else {
		u = a.scopedUser(w, r, def.Gate)
	}
	return u, def, u != nil
}

func (a *App) adminList(w http.ResponseWriter, r *http.Request) {
	u, def, ok := a.listDef(w, r, false)
	if !ok {
		return
	}
	if def.CustomPath != "" {
		http.Redirect(w, r, def.CustomPath, http.StatusSeeOther)
		return
	}
	showArchived := r.URL.Query().Get("archived") == "1"
	items, err := a.Lists.Items(r.Context(), def, showArchived)
	if err != nil {
		log.Printf("admin list %s: %v", def.Key, err)
		http.Error(w, "Something went wrong", http.StatusInternalServerError)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	data := AdminData{
		Def: def, Rows: filterItems(items, q), Span: len(def.Columns) + 2, Q: q,
		ShowArchived: showArchived, Flash: flash(r, "ok"), Err: flash(r, "err"),
	}
	a.render(w, "admin_list", http.StatusOK, a.adminView(r, u, def.Label, data))
}

// filterItems keeps items where any value contains q (case-insensitive).
func filterItems(items []reflist.Item, q string) []reflist.Item {
	if q == "" {
		return items
	}
	q = strings.ToLower(q)
	var out []reflist.Item
	for _, it := range items {
		for _, v := range it.Values {
			if strings.Contains(strings.ToLower(v), q) {
				out = append(out, it)
				break
			}
		}
	}
	return out
}

// formValues reads v0..vN in the list's column order.
func formValues(r *http.Request, def reflist.Def) []string {
	values := make([]string, len(def.Columns))
	for i := range values {
		values[i] = r.PostFormValue("v" + strconv.Itoa(i))
	}
	return values
}

// back redirects to the list page with a message in the query string. The
// message always comes from our own code, never from request data.
func back(w http.ResponseWriter, r *http.Request, def reflist.Def, key, msg string) {
	http.Redirect(w, r, "/staff/admin/lists/"+def.Key+"?"+url.Values{key: {msg}}.Encode(), http.StatusSeeOther)
}

// saveError turns a store error into a message the admin can read. Unknown
// errors are logged in detail and shown as a generic line.
func saveError(err error) string {
	var ve reflist.ValidationError
	switch {
	case errors.As(err, &ve):
		return ve.Msg
	case errors.Is(err, reflist.ErrDuplicate), errors.Is(err, reflist.ErrNotFound):
		return err.Error()
	}
	log.Printf("admin list save: %v", err)
	return "Could not save. Please try again."
}

func (a *App) adminListAdd(w http.ResponseWriter, r *http.Request) {
	u, def, ok := a.listDef(w, r, true)
	if !ok {
		return
	}
	if def.CustomPath != "" { // free-text editing is off for lists with their own page
		http.NotFound(w, r)
		return
	}
	if _, err := a.Lists.Add(r.Context(), u.Username, def, formValues(r, def)); err != nil {
		back(w, r, def, "err", saveError(err))
		return
	}
	back(w, r, def, "ok", "Added.")
}

func (a *App) adminListItem(w http.ResponseWriter, r *http.Request) {
	u, def, ok := a.listDef(w, r, true)
	if !ok {
		return
	}
	id, idErr := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if idErr != nil || def.CustomPath != "" {
		http.NotFound(w, r)
		return
	}
	var err error
	var done string
	switch r.PostFormValue("action") {
	case "save":
		err, done = a.Lists.Update(r.Context(), u.Username, def, id, formValues(r, def)), "Saved."
	case "archive":
		err, done = a.Lists.SetArchived(r.Context(), u.Username, def, id, true), "Archived."
	case "restore":
		err, done = a.Lists.SetArchived(r.Context(), u.Username, def, id, false), "Restored."
	default:
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	if err != nil {
		back(w, r, def, "err", saveError(err))
		return
	}
	back(w, r, def, "ok", done)
}

func (a *App) adminAudit(w http.ResponseWriter, r *http.Request) {
	u := a.adminUser(w, r)
	if u == nil {
		return
	}
	entries, err := audit.Recent(r.Context(), a.SQL, auditPageSize)
	if err != nil {
		log.Printf("admin audit: %v", err)
		http.Error(w, "Something went wrong", http.StatusInternalServerError)
		return
	}
	a.render(w, "admin_audit", http.StatusOK, a.adminView(r, u, "Audit log", AdminData{Audit: entries}))
}

// MalariaData is what the malaria page template receives.
type MalariaData struct {
	Rows       []malaria.Row
	Provinces  []string
	Orphans    []malaria.Orphan
	Regencies  []region.Regency // targets for re-linking an orphan
	Levels     []string
	Province   string
	Q          string
	All        bool // show every regency, not only the listed ones
	Flash, Err string
	Unavail    string // set when the region list cannot be read
}

func (a *App) malariaFilter(r *http.Request, get func(string) string) malaria.Filter {
	return malaria.Filter{Province: get("province"), Q: strings.TrimSpace(get("q")), OnlyListed: get("all") != "1"}
}

func (a *App) adminMalaria(w http.ResponseWriter, r *http.Request) {
	u := a.adminUser(w, r)
	if u == nil {
		return
	}
	get := r.URL.Query().Get
	f := a.malariaFilter(r, get)
	data := MalariaData{Levels: malaria.Endemicities, Province: f.Province, Q: f.Q, All: !f.OnlyListed, Flash: flash(r, "ok"), Err: flash(r, "err")}
	if a.Malaria == nil {
		data.Unavail = malaria.ErrNoSource.Error()
	} else if pg, err := a.Malaria.Page(r.Context(), f); err != nil {
		log.Printf("admin malaria: %v", err)
		data.Unavail = malaria.ErrNoSource.Error()
	} else {
		data.Rows, data.Provinces, data.Orphans, data.Regencies = pg.Rows, pg.Provinces, pg.Orphans, pg.Regencies
	}
	def, _ := reflist.Find("malaria")
	v := a.adminView(r, u, def.Label, AdminData{Def: def})
	v.Malaria = data
	a.render(w, "admin_malaria", http.StatusOK, v)
}

func (a *App) adminMalariaSet(w http.ResponseWriter, r *http.Request) {
	u := a.adminPost(w, r)
	if u == nil {
		return
	}
	// Keep the filter the admin was using, so saving does not lose their place.
	keep := url.Values{}
	for _, k := range []string{"province", "q", "all"} {
		if v := r.PostFormValue("f_" + k); v != "" {
			keep.Set(k, v)
		}
	}
	if a.Malaria == nil {
		keep.Set("err", malaria.ErrNoSource.Error())
	} else if err := a.Malaria.Set(r.Context(), u.Username, r.PostFormValue("code"), r.PostFormValue("endemicity")); err != nil {
		keep.Set("err", malariaError(err))
	} else {
		keep.Set("ok", "Saved.")
	}
	http.Redirect(w, r, "/staff/admin/malaria?"+keep.Encode(), http.StatusSeeOther)
}

// adminMalariaOrphan re-links or archives a stored item that is no longer in
// the region list. The item is addressed by its id and re-link targets by API
// code, so nothing typed by hand reaches the list.
func (a *App) adminMalariaOrphan(w http.ResponseWriter, r *http.Request) {
	u := a.adminPost(w, r)
	if u == nil {
		return
	}
	id, idErr := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if idErr != nil {
		http.NotFound(w, r)
		return
	}
	keep := url.Values{}
	switch {
	case a.Malaria == nil:
		keep.Set("err", malaria.ErrNoSource.Error())
	case r.PostFormValue("action") == "archive":
		if err := a.Malaria.ArchiveOrphan(r.Context(), u.Username, id); err != nil {
			keep.Set("err", malariaError(err))
		} else {
			keep.Set("ok", "Archived.")
		}
	case r.PostFormValue("action") == "relink":
		if err := a.Malaria.Relink(r.Context(), u.Username, id, r.PostFormValue("code")); err != nil {
			keep.Set("err", malariaError(err))
		} else {
			keep.Set("ok", "Linked to the current regency.")
		}
	default:
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/staff/admin/malaria?"+keep.Encode(), http.StatusSeeOther)
}

func malariaError(err error) string {
	for _, known := range []error{malaria.ErrNoSource, malaria.ErrUnknownRegion, malaria.ErrBadEndemicity, malaria.ErrNotOrphan, malaria.ErrAlreadyListed} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	var ve reflist.ValidationError
	if errors.As(err, &ve) {
		return ve.Msg
	}
	log.Printf("admin malaria save: %v", err)
	return "Could not save. Please try again."
}

// SettingsData is what the settings page template receives.
type SettingsData struct {
	Groups     []settings.Group
	Flash, Err string
}

func (a *App) adminSettings(w http.ResponseWriter, r *http.Request) {
	u := a.adminUser(w, r)
	if u == nil {
		return
	}
	if a.Settings == nil {
		http.NotFound(w, r)
		return
	}
	groups, err := a.Settings.Groups(r.Context())
	if err != nil {
		log.Printf("admin settings: %v", err)
		http.Error(w, "Something went wrong", http.StatusInternalServerError)
		return
	}
	v := a.adminView(r, u, "Settings", AdminData{})
	v.Settings = SettingsData{Groups: groups, Flash: flash(r, "ok"), Err: flash(r, "err")}
	a.render(w, "admin_settings", http.StatusOK, v)
}

// adminSettingsSave saves one setting per POST ("save" or "reset"), so one
// bad value never blocks the others and each change is its own audit row.
func (a *App) adminSettingsSave(w http.ResponseWriter, r *http.Request) {
	u := a.adminPost(w, r)
	if u == nil {
		return
	}
	if a.Settings == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PostFormValue("key")
	var err error
	var done string
	switch r.PostFormValue("action") {
	case "save":
		err, done = a.Settings.Set(r.Context(), u.Username, key, r.PostFormValue("value")), "Saved."
	case "reset":
		err, done = a.Settings.Reset(r.Context(), u.Username, key), "Reset to the default."
	default:
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	q := url.Values{"ok": {done}}
	if err != nil {
		q = url.Values{"err": {settingsError(err)}}
	}
	http.Redirect(w, r, "/staff/admin/settings?"+q.Encode(), http.StatusSeeOther)
}

func settingsError(err error) string {
	var ve settings.ValidationError
	if errors.As(err, &ve) {
		return ve.Msg
	}
	log.Printf("admin settings save: %v", err)
	return "Could not save. Please try again."
}

func officerError(err error) string {
	for _, known := range []error{gateofficer.ErrName, gateofficer.ErrPIN, gateofficer.ErrUsername, gateofficer.ErrDuplicateUsername, gateofficer.ErrDuplicateName, gateofficer.ErrNotFound} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	log.Printf("gate officer save: %v", err)
	return "Could not save. Please try again."
}

func (a *App) adminOfficers(w http.ResponseWriter, r *http.Request) {
	u := a.scopedUser(w, r, true)
	if u == nil {
		return
	}
	if a.Officers == nil {
		http.NotFound(w, r)
		return
	}
	showArchived := r.URL.Query().Get("archived") == "1"
	list, err := a.Officers.List(r.Context(), showArchived)
	if err != nil {
		log.Printf("admin officers: %v", err)
		http.Error(w, "Something went wrong", http.StatusInternalServerError)
		return
	}
	v := a.adminView(r, u, "Gate officers", AdminData{Officers: list, ShowArchived: showArchived, Flash: flash(r, "ok"), Err: flash(r, "err")})
	a.render(w, "admin_officers", http.StatusOK, v)
}

func (a *App) adminOfficerAdd(w http.ResponseWriter, r *http.Request) {
	u := a.scopedPost(w, r, true)
	if u == nil || a.Officers == nil {
		return
	}
	// The name must be an eligible worker from medpost, never free text.
	name := strings.Join(strings.Fields(r.PostFormValue("name")), " ")
	if a.Workers == nil {
		officerRedirect(w, r, errNoEmployees, "")
		return
	}
	ok, err := a.Workers.Eligible(r.Context(), name)
	if err != nil {
		log.Printf("officer eligibility: %v", err)
		officerRedirect(w, r, errNoEmployees, "")
		return
	}
	if !ok {
		officerRedirect(w, r, errNotEligible, "")
		return
	}
	_, err = a.Officers.Add(r.Context(), u.Username, name, r.PostFormValue("username"), r.PostFormValue("pin"))
	officerRedirect(w, r, err, "Added.")
}

func (a *App) adminOfficerAction(w http.ResponseWriter, r *http.Request) {
	u := a.scopedPost(w, r, true)
	if u == nil || a.Officers == nil {
		return
	}
	id, idErr := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if idErr != nil {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	var err error
	var done string
	switch r.PostFormValue("action") {
	case "username":
		err, done = a.Officers.SetUsername(ctx, u.Username, id, r.PostFormValue("username")), "Saved."
	case "pin":
		err, done = a.Officers.SetPIN(ctx, u.Username, id, r.PostFormValue("pin")), "New PIN set."
	case "archive":
		err, done = a.Officers.SetArchived(ctx, u.Username, id, true), "Archived."
	case "restore":
		err, done = a.Officers.SetArchived(ctx, u.Username, id, false), "Restored."
	case "remove":
		// Removing cannot be undone, so it needs the tick box as well as the button.
		if r.PostFormValue("confirm") != "yes" {
			officerRedirect(w, r, errConfirm, "")
			return
		}
		err, done = a.Officers.Remove(ctx, u.Username, id), "Removed."
	default:
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	officerRedirect(w, r, err, done)
}

var (
	errConfirm     = errors.New("Tick \"Confirm\" to remove an officer for good.")
	errNotEligible = errors.New("Pick the name from the list: only workers with a valid MCU result of Fit or Fit with note can be gate officers.")
	errNoEmployees = errors.New("The employee list from hse-medpost is not available right now. Try again later.")
)

func officerRedirect(w http.ResponseWriter, r *http.Request, err error, done string) {
	q := url.Values{"ok": {done}}
	if err != nil {
		msg := officerError(err)
		for _, own := range []error{errConfirm, errNotEligible, errNoEmployees} {
			if errors.Is(err, own) {
				msg = own.Error()
			}
		}
		q = url.Values{"err": {msg}}
	}
	http.Redirect(w, r, "/staff/admin/gate-officers?"+q.Encode(), http.StatusSeeOther)
}

// adminEmployeeNames feeds the name picker on the gate officers page: the same
// eligible workers as the public search, but only for a signed-in admin or
// Gate admin, and limited per account rather than per address.
func (a *App) adminEmployeeNames(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	if u == nil || !(u.HasRole(staff.Admin) || u.HasRole(staff.GateAdmin)) {
		writeJSON(w, http.StatusForbidden, map[string][]string{"names": {}})
		return
	}
	a.searchNames(w, r, "a:"+u.Username)
}
