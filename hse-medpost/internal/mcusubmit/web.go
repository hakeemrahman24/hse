package mcusubmit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"hse-medpost/internal/auth"
	"hse-medpost/internal/ratelimit"
	"hse-medpost/internal/turnstile"
)

const (
	basePath    = "/mcu-submit"
	cookieName  = "mcu_submitter"
	sessionRole = "mcu-submitter"

	verifyTTL  = 24 * time.Hour // unverified requests are purged after 48 h, so a link outlives no request
	setupTTL   = 72 * time.Hour
	sessionTTL = 12 * time.Hour

	mailTimeout = 15 * time.Second
	maxFormBody = 16 << 10
)

// Limits are the per-address request caps (plan section 4). They are fields, not constants, so a
// test can lower them instead of sending hundreds of requests.
type Limits struct {
	RegisterPerIP, RegisterPerEmail int // per hour
	SignInPerIP, SignInPerUser      int // per 15 minutes
	WorkerSearchPerUser             int // per minute
	ForgotPerIP, ForgotPerEmail     int // per hour
}

func defaultLimits() Limits {
	return Limits{RegisterPerIP: 5, RegisterPerEmail: 3, SignInPerIP: 20, SignInPerUser: 10, WorkerSearchPerUser: 60, ForgotPerIP: 5, ForgotPerEmail: 3}
}

const (
	registerWindow     = time.Hour
	signInWindow       = 15 * time.Minute
	workerSearchWindow = time.Minute
)

var tokenShape = regexp.MustCompile(`^[0-9a-f]{64}$`)

// API holds what the /mcu-submit pages need. It owns its router, cookie and templates and never
// imports the staff handlers: the portal is a separate world that happens to share the process
// (plan section 1), so it could be moved to its own binary later.
type API struct {
	Store *Store
	// MailDomainOK says whether a mail domain can receive mail. A field so tests can replace the
	// DNS lookup.
	MailDomainOK func(ctx context.Context, domain string) bool

	sessions     *auth.Store
	pages        *template.Template
	lim          *ratelimit.Limiter
	limits       Limits
	verifier     turnstile.Verifier
	sitekey      string
	devCheck     bool
	mailer       Mailer
	baseURL      string
	trustCF      bool
	secureCookie bool
	pipeline     *Pipeline         // nil = uploads are off
	bounceSecret string            // empty = the Brevo webhook is off
	mailCfg      *ConfigMailer     // nil = no mail settings page, sign-up never closed for lack of mail
	certs        CertificateSource // nil = no certificate download for submitters
}

// New builds the handlers. The cookie key is derived from the staff session secret with its own
// label, so a staff token is never a valid portal token and the other way round.
func New(store *Store, sessionSecret, templateDir string) (*API, error) {
	pages, err := template.ParseGlob(filepath.Join(templateDir, "mcusubmit", "*.html"))
	if err != nil {
		return nil, fmt.Errorf("mcusubmit templates: %w", err)
	}
	sum := sha256.Sum256([]byte("mcusubmit|" + sessionSecret))
	return &API{
		Store:        store,
		MailDomainOK: lookupMailDomain,
		sessions:     auth.NewStore(hex.EncodeToString(sum[:])),
		pages:        pages,
		lim:          ratelimit.New(),
		limits:       defaultLimits(),
	}, nil
}

// SetTurnstile sets how the Cloudflare check is shown and verified. Without it nothing can pass the
// check, so register and sign-in stay closed.
func (a *API) SetTurnstile(v turnstile.Verifier, sitekey string, dev bool) {
	a.verifier, a.sitekey, a.devCheck = v, sitekey, dev
}

// SetMailer sets the sender and the public address that mail links point at (no trailing slash).
func (a *API) SetMailer(m Mailer, baseURL string) {
	a.mailer, a.baseURL = m, strings.TrimRight(baseURL, "/")
}

// SetPipeline turns uploads on. The pipeline's FileStore is also where documents are read back
// from and deleted, so this is the only place the web layer learns about storage.
func (a *API) SetPipeline(p *Pipeline) { a.pipeline = p }

// TrustCloudflareHeader makes the rate limits use CF-Connecting-IP and marks the cookie Secure.
// Only turn it on when the port is reachable through the tunnel alone.
func (a *API) TrustCloudflareHeader(on bool) { a.trustCF, a.secureCookie = on, on }

// Register adds the portal routes to mux. Every route sits behind the cross-origin check and the
// security headers.
func (a *API) Register(mux *http.ServeMux) {
	in := http.NewServeMux()
	in.HandleFunc("GET "+basePath, a.Landing)
	in.HandleFunc("GET "+basePath+"/{$}", a.Landing)
	in.HandleFunc("GET "+basePath+"/register", a.RegisterPage)
	in.HandleFunc("POST "+basePath+"/register", a.RegisterSubmit)
	in.HandleFunc("GET "+basePath+"/verify", a.VerifyPage)
	in.HandleFunc("POST "+basePath+"/verify", a.VerifySubmit)
	in.HandleFunc("POST "+basePath+"/lang", a.SetLanguage)
	in.HandleFunc("GET "+basePath+"/signin", a.SignInPage)
	in.HandleFunc("POST "+basePath+"/signin", a.SignInSubmit)
	in.HandleFunc("POST "+basePath+"/signout", a.SignOut)
	in.HandleFunc("GET "+basePath+"/setup", a.SetupPage)
	in.HandleFunc("POST "+basePath+"/setup", a.SetupSubmit)
	in.HandleFunc("GET "+basePath+"/forgot", a.ForgotPage)
	in.HandleFunc("POST "+basePath+"/forgot", a.ForgotSubmit)
	in.HandleFunc("GET "+basePath+"/reset", a.ResetPage)
	in.HandleFunc("POST "+basePath+"/reset", a.ResetSubmit)
	in.HandleFunc("GET "+basePath+"/settings", a.SettingsPage)
	in.HandleFunc("POST "+basePath+"/settings/password", a.ChangePassword)
	in.HandleFunc("POST "+basePath+"/settings/email", a.RequestEmailChange)
	in.HandleFunc("POST "+basePath+"/settings/reminders", a.SaveReminders)
	in.HandleFunc("GET "+basePath+"/email-change", a.EmailChangePage)
	in.HandleFunc("POST "+basePath+"/email-change", a.EmailChangeSubmit)
	in.HandleFunc("GET "+basePath+"/home", a.Home)
	in.HandleFunc("GET "+basePath+"/batch", a.BatchPage)
	in.HandleFunc("POST "+basePath+"/batch/add", a.AddDocument)
	in.HandleFunc("POST "+basePath+"/batch/send", a.SendBatchSubmit)
	in.HandleFunc("GET "+basePath+"/workers", a.WorkerSearch)
	in.HandleFunc("GET "+basePath+"/documents", a.Documents)
	in.HandleFunc("GET "+basePath+"/documents/{id}/review", a.ReviewPage)
	in.HandleFunc("POST "+basePath+"/documents/{id}/confirm", a.ConfirmDocument)
	in.HandleFunc("GET "+basePath+"/documents/{id}/file", a.DocumentFile)
	in.HandleFunc("GET "+basePath+"/documents/{id}/certificate", a.DocumentCertificate)
	in.HandleFunc("POST "+basePath+"/documents/{id}/remove", a.RemoveDocument)
	in.HandleFunc("POST "+basePath+"/documents/{id}/withdraw", a.WithdrawDocument)
	in.HandleFunc("POST "+basePath+"/webhooks/brevo", a.BrevoWebhook)
	in.HandleFunc("GET "+basePath+"/admin", a.AdminPage)
	in.HandleFunc("POST "+basePath+"/admin/approve", a.AdminApprove)
	in.HandleFunc("POST "+basePath+"/admin/reject", a.AdminReject)
	in.HandleFunc("POST "+basePath+"/admin/disable", a.AdminDisable)
	in.HandleFunc("GET "+basePath+"/admin/reminders", a.ReminderPage)
	in.HandleFunc("POST "+basePath+"/admin/reminders", a.ReminderSave)
	in.HandleFunc("POST "+basePath+"/admin/reminders/send", a.ReminderSendNow)
	in.HandleFunc("GET "+basePath+"/admin/mail", a.MailPage)
	in.HandleFunc("POST "+basePath+"/admin/mail", a.MailSave)
	in.HandleFunc("POST "+basePath+"/admin/mail/test", a.MailTest)
	in.HandleFunc("GET "+basePath+"/admin/digest", a.DigestPage)
	in.HandleFunc("POST "+basePath+"/admin/digest", a.DigestSave)
	in.HandleFunc("POST "+basePath+"/admin/reviewers/add", a.ReviewerAdd)
	in.HandleFunc("POST "+basePath+"/admin/reviewers/toggle", a.ReviewerToggle)
	in.HandleFunc("POST "+basePath+"/admin/reviewers/delete", a.ReviewerDelete)

	// CrossOriginProtection refuses a state-changing request that another site caused, by reading
	// Sec-Fetch-Site and Origin (owner's choice, plan section 9). With the SameSite=Lax cookie and
	// POST-only changes it replaces form tokens.
	h := a.secure(a.withLanguage(http.NewCrossOriginProtection().Handler(a.routeOrNotFound(in))))
	mux.Handle(basePath, h)
	mux.Handle(basePath+"/", h)
}

// secure adds the headers every portal page carries. no-referrer matters most: links in mails hold
// a token in the query string, which must never leak in a Referer header to Cloudflare.
func (a *API) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://challenges.cloudflare.com; "+
			"frame-src https://challenges.cloudflare.com; connect-src 'self' https://challenges.cloudflare.com; "+
			"style-src 'self' 'unsafe-inline'; img-src 'self' data:; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
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

// page is everything the templates need.
type page struct {
	Title     string
	Nav       string // which sidebar item is current: the template name without ".html"
	User      *Submitter
	Sitekey   string
	DevCheck  bool
	Error     string
	Form      map[string]string
	Companies []companyOption
	Token     string
	Heading   string
	Message   string
	Counts    map[string]int
	Pending   []adminRow
	Accounts  []adminRow

	// Upload pages.
	Docs          []Submission
	BatchID       int64 // the open batch; 0 when it has no documents
	Nationalities []string
	Caption       string   // table caption
	Notice        string   // a success message on a form page
	Unconfirmed   int      // documents of the open batch still in Review
	Statuses      []string // choices of the status filter
	Result        DocPage  // paging numbers of the list on screen
	PrevURL       string
	NextURL       string

	// Reviewer digest settings page.
	Digest    DigestConfig
	Reviewers []Reviewer
	Intervals []int

	// Reminders: the admin's settings page and the submitter's Expiring soon list.
	Reminder ReminderConfig
	Preview  string
	Expiring []Expiring

	// Mail settings page.
	MailCfg    MailConfig
	MailSource string
}

type companyOption struct {
	ID   int64
	Name string
}

type adminRow struct {
	ID                                                int64
	Name, Email, Company, Position, Status, CreatedAt string
	RejectReason, BouncedAt                           string
}

func (a *API) newPage(title string) page {
	return page{Title: title, Sitekey: a.sitekey, DevCheck: a.devCheck, Form: map[string]string{}}
}

// render executes into a buffer first, so a template error gives a clean 500 and not a half page.
func (a *API) render(w http.ResponseWriter, status int, name string, p page) {
	// The sidebar marks the current item from the page name, so no handler has to remember to say it.
	p.Nav = strings.TrimSuffix(name, ".html")
	var buf bytes.Buffer
	if err := a.pages.ExecuteTemplate(&buf, name, p); err != nil {
		log.Printf("mcusubmit: render %s: %v", name, err)
		http.Error(w, "Something went wrong.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func (a *API) message(w http.ResponseWriter, status int, heading, msg string, user *Submitter) {
	p := a.newPage(heading)
	p.Heading, p.Message, p.User = heading, msg, user
	a.render(w, status, "message.html", p)
}

// routeOrNotFound hands a request to the portal's router, except when no route claims it. Then the
// answer is decided here, because the router's own plain-text 404 is not a portal page: an address
// that exists for another method (a POST to a page that only answers GET) is still the router's
// 405 with its Allow header, and an address nothing claims gets the portal's own "Page not found".
// A catch-all route cannot do this: it would answer for every method and turn each 405 into a 404.
func (a *API) routeOrNotFound(in *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := in.Handler(r); pattern != "" {
			in.ServeHTTP(w, r)
			return
		}
		var allow []string
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			probe := r.Clone(r.Context())
			probe.Method = method
			if _, pattern := in.Handler(probe); pattern != "" {
				allow = append(allow, method)
			}
		}
		if len(allow) > 0 {
			w.Header().Set("Allow", strings.Join(allow, ", "))
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		a.NotFoundPage(w, r)
	})
}

// NotFoundPage answers an address no portal route claims. It shows the sidebar when somebody is
// signed in, so a mistyped link leaves them one click from where they were.
func (a *API) NotFoundPage(w http.ResponseWriter, r *http.Request) {
	a.notFound(w, a.current(r))
}

func (a *API) notFound(w http.ResponseWriter, user *Submitter) {
	a.message(w, http.StatusNotFound, "Page not found", "There is nothing at this address.", user)
}

func (a *API) serverError(w http.ResponseWriter, what string, err error) {
	log.Printf("mcusubmit: %s: %v", what, err)
	http.Error(w, "Something went wrong.", http.StatusInternalServerError)
}

// parseForm limits the body size, then parses it. The portal's forms are all tiny.
func parseForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	return r.ParseForm() == nil
}

// passedCheck verifies the Cloudflare token the widget added to the form.
func (a *API) passedCheck(r *http.Request) bool {
	if a.verifier == nil {
		return false
	}
	ok, err := a.verifier.Verify(r.Context(), r.PostFormValue("cf-turnstile-response"), a.clientIP(r))
	return err == nil && ok
}

// ---- sessions ---------------------------------------------------------------------------------

func (a *API) startSession(w http.ResponseWriter, u Submitter) error {
	token, err := a.sessions.IssueFor(auth.Session{UserID: u.ID, Role: sessionRole, Ver: u.SessionVersion}, sessionTTL)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: token, Path: basePath, HttpOnly: true,
		Secure: a.secureCookie, SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL.Seconds()),
	})
	return nil
}

func (a *API) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: basePath, HttpOnly: true,
		Secure: a.secureCookie, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

// current returns the signed-in submitter, or nil. The cookie only names an account and the
// version it signed in under: the account is read from the database on every request, so Disable
// and a password change take effect at once and a cookie can never raise its own role.
func (a *API) current(r *http.Request) *Submitter {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return nil
	}
	sess, err := a.sessions.Verify(c.Value)
	if err != nil || sess.Role != sessionRole || sess.UserID == 0 {
		return nil
	}
	u, err := a.Store.ByID(r.Context(), sess.UserID)
	if err != nil || u.Status != StatusActive || u.SessionVersion != sess.Ver {
		return nil
	}
	return &u
}

// requireUser redirects to sign-in when there is no valid session.
func (a *API) requireUser(w http.ResponseWriter, r *http.Request) *Submitter {
	u := a.current(r)
	if u == nil {
		http.Redirect(w, r, basePath+"/signin", http.StatusSeeOther)
	}
	return u
}

// requireAdmin is requireUser plus the Admin role. A signed-in non-admin gets the same 404 as a
// path that does not exist, so the admin pages are not advertised.
func (a *API) requireAdmin(w http.ResponseWriter, r *http.Request) *Submitter {
	u := a.requireUser(w, r)
	if u == nil {
		return nil
	}
	if u.Role != RoleAdmin {
		a.notFound(w, u)
		return nil
	}
	return u
}

// ---- public pages -----------------------------------------------------------------------------

// Landing is the public front page; a signed-in visitor goes straight to Home.
func (a *API) Landing(w http.ResponseWriter, r *http.Request) {
	if a.current(r) != nil {
		http.Redirect(w, r, basePath+"/home", http.StatusSeeOther)
		return
	}
	a.render(w, http.StatusOK, "landing.html", a.newPage("Medical checkup submission"))
}

// Home is the signed-in start page: the submitter's own counts by status.
func (a *API) Home(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	docs, err := a.Store.ListOwn(r.Context(), u.ID)
	if err != nil {
		a.serverError(w, "list own", err)
		return
	}
	p := a.newPage("Home")
	p.User, p.Counts = u, map[string]int{}
	for _, d := range docs {
		p.Counts[d.Status]++
	}
	// Shown whether or not reminder mails are on (plan section 8); the admin has no workers of
	// their own, so the list is only for submitters.
	if u.Role != RoleAdmin {
		cfg, err := a.Store.ReminderSettings(r.Context())
		if err == nil {
			p.Expiring, err = a.Store.ExpiringFor(r.Context(), u.ID, cfg.DaysBefore)
		}
		if err != nil {
			log.Printf("mcusubmit: expiring soon: %v", err)
		}
	}
	a.render(w, http.StatusOK, "home.html", p)
}

// SignOut clears the cookie. It needs no session, so a stale tab can always sign out.
func (a *API) SignOut(w http.ResponseWriter, r *http.Request) {
	a.clearSession(w)
	http.Redirect(w, r, basePath, http.StatusSeeOther)
}

// ---- companies --------------------------------------------------------------------------------

// companies are the pick-only choices on the register form (owner, 2026-10-26): no free text, so
// an admin has to add a missing company first.
func (a *API) companies(ctx context.Context) ([]companyOption, error) {
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT CompanyID, Name FROM Company WHERE COALESCE(Archived, 0) = 0 ORDER BY Name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []companyOption
	for rows.Next() {
		var c companyOption
		if err := rows.Scan(&c.ID, &c.Name); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (a *API) companyName(ctx context.Context, id int64) (string, error) {
	var name string
	err := a.Store.DB.QueryRowContext(ctx, `SELECT Name FROM Company WHERE CompanyID = ? AND COALESCE(Archived, 0) = 0`, id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return name, err
}

func (a *API) allCompanyNames(ctx context.Context) map[int64]string {
	names := map[int64]string{}
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT CompanyID, Name FROM Company`)
	if err != nil {
		return names
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var n string
		if rows.Scan(&id, &n) == nil {
			names[id] = n
		}
	}
	return names
}

// lookupMailDomain asks DNS whether a domain can receive mail: an MX record, or failing that an
// address record (RFC 5321 treats a domain with only an A record as its own mail host).
func lookupMailDomain(ctx context.Context, domain string) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if mx, err := net.DefaultResolver.LookupMX(ctx, domain); err == nil && len(mx) > 0 {
		return true
	}
	hosts, err := net.DefaultResolver.LookupHost(ctx, domain)
	return err == nil && len(hosts) > 0
}

func parseID(s string) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return id, err == nil && id > 0
}

// sendMail sends with a timeout and only logs a failure: a mail problem must not change what the
// visitor sees, or the form would reveal which addresses are registered.
func (a *API) sendMail(ctx context.Context, to, subject, body string) {
	if a.mailer == nil {
		log.Printf("mcusubmit: no mailer configured, dropped %q for a submitter", subject)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, mailTimeout)
	defer cancel()
	if err := a.mailer.Send(ctx, to, subject, body); err != nil {
		log.Printf("mcusubmit: send %q failed: %v", subject, err)
	}
}

var dummyHash struct {
	once sync.Once
	hash string
}

// fakeHash is a real bcrypt hash of a throwaway password. Sign-in compares against it when the
// username does not exist, so the response takes as long as for a real account.
func fakeHash() string {
	dummyHash.once.Do(func() { dummyHash.hash, _ = HashPassword("not-a-real-account-password") })
	return dummyHash.hash
}
