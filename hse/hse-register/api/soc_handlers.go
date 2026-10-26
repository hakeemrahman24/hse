package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Lifetimes and limits from plan section 7.1 (all placeholders to be tuned).
const (
	socCheckTTL      = 10 * time.Minute // after Turnstile passes: how long the name search stays open
	socSessionTTL    = 30 * time.Minute // after a date-of-birth match
	socMinQueryRunes = 3
	socMaxResults    = 5
	socSearchMax     = 30  // name searches per device per 10 minutes
	socSuggestMax    = 120 // topic suggestions per employee per 10 minutes
	socFailMax       = 5   // failed verifications per employee record or device per 15 minutes
	socFailWindow    = 15 * time.Minute
	socLockFor       = 15 * time.Minute

	cookieSOCCheck   = "soc_check"
	cookieSOCSession = "soc_session"

	// One generic message for every sign-in failure: never say which part was wrong.
	msgNotFound = "Details not found, please check and try again."
)

type socCheck struct {
	ID string `json:"id"`
}

// socSession carries the verified reporter. It never contains the date of birth.
type socSession struct {
	EmployeeID  string `json:"eid"`
	Name        string `json:"name"`
	Designation string `json:"des"`
	Company     string `json:"co"`
}

type socRefEntry struct {
	key     string
	checkID string
	exp     time.Time
}

type socHandlers struct {
	store        *SessionStore
	tv           turnstileVerifier
	dir          employeeDirectory
	sink         findingSink
	lim          *limiter
	sitekey      string
	devStubs     bool
	trustCF      bool // read the visitor address from Cf-Connecting-Ip (public listener only)
	secureCookie bool
	evidenceDir  string // where compressed photos are stored
	ref          *refData
	companies    *companyCache

	mu   sync.Mutex
	refs map[string]socRefEntry // one-time name references -> employee key
}

func newSOCHandlers(store *SessionStore, tv turnstileVerifier, dir employeeDirectory, sink findingSink) *socHandlers {
	return &socHandlers{store: store, tv: tv, dir: dir, sink: sink, lim: newLimiter(), refs: map[string]socRefEntry{}}
}

// clientIP is the base of the per-device limits. Behind the Cloudflare tunnel every request
// comes from the board itself, so the real visitor is in Cf-Connecting-Ip. That header is only
// trusted on the public listener (trustCF); anywhere else anyone could forge it.
func (h *socHandlers) clientIP(r *http.Request) string {
	if h.trustCF {
		if ip := strings.TrimSpace(r.Header.Get("Cf-Connecting-Ip")); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (h *socHandlers) check(r *http.Request) (*socCheck, bool) {
	var c socCheck
	if err := h.store.GetToken(r, cookieSOCCheck, &c); err != nil || c.ID == "" {
		return nil, false
	}
	return &c, true
}

func (h *socHandlers) session(r *http.Request) (*socSession, bool) {
	var s socSession
	if err := h.store.GetToken(r, cookieSOCSession, &s); err != nil || s.EmployeeID == "" {
		return nil, false
	}
	return &s, true
}

// apiSuggest proposes the topic and hazard for the description being typed. It needs the
// employee sign-in and is rate-limited per employee, so it cannot be used as an open service.
func (h *socHandlers) apiSuggest(w http.ResponseWriter, r *http.Request) {
	sess, ok := h.session(r)
	if !ok {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": tr(langOf(r), "Please sign in first.")})
		return
	}
	if !h.lim.allow("suggest:"+sess.EmployeeID, socSuggestMax, socCheckTTL) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": tr(langOf(r), "Please wait a moment and try again.")})
		return
	}
	serveSuggestionTop(w, r, h.ref)
}

// apiCheck verifies the Turnstile token. Until it passes, name search and sign-in stay closed.
func (h *socHandlers) apiCheck(w http.ResponseWriter, r *http.Request) {
	ip := h.clientIP(r)
	if !h.lim.allow("check:"+ip, 20, socCheckTTL) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": tr(langOf(r), "Please wait a moment and try again.")})
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": tr(langOf(r), "Check failed, please try again.")})
		return
	}
	ok, err := h.tv.Verify(r.Context(), in.Token, ip)
	if err != nil || !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": tr(langOf(r), "Check failed, please try again.")})
		return
	}
	if err := h.store.SetToken(w, cookieSOCCheck, socCheckTTL, h.secureCookie, socCheck{ID: randomID()}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": tr(langOf(r), "Something went wrong.")})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// apiNames is the searchable dropdown. It returns at most 5 entries of {ref, label}: no
// employee ID, designation or date of birth (the date of birth is the secret that proves identity), and never the full list.
func (h *socHandlers) apiNames(w http.ResponseWriter, r *http.Request) {
	chk, ok := h.check(r)
	if !ok {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": tr(langOf(r), "Please complete the check first.")})
		return
	}
	if !h.lim.allow("search:"+h.clientIP(r), socSearchMax, socCheckTTL) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": tr(langOf(r), "Please wait a moment and try again.")})
		return
	}
	type entry struct {
		Ref     string `json:"ref"`
		Label   string `json:"label"`
		Company string `json:"company"` // shown under the name so people with similar names can be told apart
	}
	out := []entry{}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if utf8.RuneCountInString(q) >= socMinQueryRunes && utf8.RuneCountInString(q) <= 60 {
		matches, err := h.dir.Search(r.Context(), q, socMaxResults)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": tr(langOf(r), "Name search is not available right now.")})
			return
		}
		for _, m := range matches {
			out = append(out, entry{Ref: h.newRef(m.Key, chk.ID), Label: m.Name, Company: m.Company})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *socHandlers) newRef(key, checkID string) string {
	ref := randomID()
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	for k, e := range h.refs { // drop expired references so the map cannot grow without limit
		if now.After(e.exp) {
			delete(h.refs, k)
		}
	}
	h.refs[ref] = socRefEntry{key: key, checkID: checkID, exp: now.Add(socCheckTTL)}
	return ref
}

// lookupRef finds a reference that belongs to this visitor's check and has not expired.
func (h *socHandlers) lookupRef(ref, checkID string) (socRefEntry, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.refs[ref]
	if !ok || e.checkID != checkID || time.Now().After(e.exp) {
		return socRefEntry{}, false
	}
	return e, true
}

func (h *socHandlers) consumeRef(ref string) {
	h.mu.Lock()
	delete(h.refs, ref)
	h.mu.Unlock()
}

// apiSignin verifies the chosen name and the date of birth with the directory (Medpost).
// The date of birth is read, compared and dropped: it is not stored, logged or echoed back.
func (h *socHandlers) apiSignin(w http.ResponseWriter, r *http.Request) {
	fail := func(status int) { writeJSON(w, status, map[string]string{"error": tr(langOf(r), msgNotFound)}) }
	chk, ok := h.check(r)
	if !ok {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": tr(langOf(r), "Please complete the check first.")})
		return
	}
	ip := h.clientIP(r)
	if h.lim.locked("ip:" + ip) {
		fail(http.StatusUnauthorized) // same message as a wrong date: do not reveal which limit was hit
		return
	}
	var in struct {
		Ref string `json:"ref"`
		DOB string `json:"dob"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		fail(http.StatusUnauthorized)
		return
	}
	entry, ok := h.lookupRef(in.Ref, chk.ID)
	dob, perr := time.Parse("2006-01-02", in.DOB)
	if !ok || perr != nil {
		h.lim.fail("ip:"+ip, socFailMax, socFailWindow, socLockFor)
		fail(http.StatusUnauthorized)
		return
	}
	if h.lim.locked("emp:" + entry.key) {
		fail(http.StatusUnauthorized)
		return
	}
	emp, err := h.dir.Verify(r.Context(), entry.key, dob)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": tr(langOf(r), "Sign-in is not available right now. Please try again later.")})
		return
	}
	if emp == nil {
		h.lim.fail("ip:"+ip, socFailMax, socFailWindow, socLockFor)
		h.lim.fail("emp:"+entry.key, socFailMax, socFailWindow, socLockFor)
		fail(http.StatusUnauthorized)
		return
	}
	h.consumeRef(in.Ref)
	sess := socSession{EmployeeID: emp.Key, Name: emp.Name, Designation: emp.Designation, Company: emp.Company}
	if err := h.store.SetToken(w, cookieSOCSession, socSessionTTL, h.secureCookie, sess); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": tr(langOf(r), "Something went wrong.")})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"redirect": "/soc/new"})
}
