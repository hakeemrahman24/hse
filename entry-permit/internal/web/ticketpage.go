package web

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"entry-permit/internal/dates"
	"entry-permit/internal/permit"
	"entry-permit/internal/phone"
	"entry-permit/internal/ticket"
	"entry-permit/internal/waiver"
)

const (
	ticketTitle      = "Working schedule"
	ticketTriesPerIP = 30 // access tries per address per hour, on top of the per-permit lock
	accessError      = "The permit number or date of birth is incorrect."
	sessionGone      = "Your session has expired. Please open your permit again."
)

// lepPattern is the shape of a permit number: LEP- for a work permit, WVR- for a waiver.
var lepPattern = regexp.MustCompile(`^(LEP|WVR)-[A-Z0-9]{5}$`)

// accessClaims is what the birth-date check proved: this permit may be opened
// until Expires. It is signed, not stored, like the permit form's step token.
type accessClaims struct {
	LEP     string
	Expires dates.Moment
}

func accessMAC(secret, payload string) []byte {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte("ticket-access|")) // keeps these tokens apart from login cookies and the permit step token
	h.Write([]byte(payload))
	return h.Sum(nil)
}

func signAccess(secret string, c accessClaims) string {
	b, _ := json.Marshal(c)
	return tokenEnc.EncodeToString(b) + "." + tokenEnc.EncodeToString(accessMAC(secret, string(b)))
}

// verifyAccess returns the claims when the token is genuine and not expired.
func verifyAccess(secret, tok string, now dates.Moment) (accessClaims, bool) {
	var c accessClaims
	parts := strings.Split(tok, ".")
	if len(parts) != 2 {
		return c, false
	}
	payload, err1 := tokenEnc.DecodeString(parts[0])
	sig, err2 := tokenEnc.DecodeString(parts[1])
	if err1 != nil || err2 != nil || !hmac.Equal(sig, accessMAC(secret, string(payload))) {
		return c, false
	}
	if json.Unmarshal(payload, &c) != nil || c.LEP == "" || now >= c.Expires {
		return c, false
	}
	return c, true
}

// TicketData feeds the working schedule page.
type TicketData struct {
	Available  bool
	Err        string
	Notice     string
	LEP        string
	Open       bool // the birth date was accepted and the permit is shown
	Token      string
	Name       string
	Waiver     bool // a waiver's ticket is shown read-only
	Visit      string
	Tickets    []TicketRow
	CanPropose bool
	FitNote    bool
	Vals       map[string]string
}

// TicketRow is one ticket as listed on the page.
type TicketRow struct {
	ID, Arrival, Departure, Location, Status, Pattern string
	Days                                              int
}

func (a *App) ticketAvailable() bool {
	return a.Permits != nil && a.Waivers != nil && a.Tickets != nil && a.Turnstile != nil && (a.TurnstileDev || a.TurnstileSite != "")
}

func (a *App) ticketRender(w http.ResponseWriter, r *http.Request, status int, d TicketData) {
	d.Available = a.ticketAvailable()
	a.render(w, "ticket", status, View{Title: ticketTitle, User: a.currentUser(r), My: a.myData(), Ticket: d})
}

func (a *App) ticketPage(w http.ResponseWriter, r *http.Request) {
	lep := clip(strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("lep"))), 12)
	// A worker who is already signed in and owns this valid permit is not asked for the birth date
	// again: the same proof the worker page uses already holds. Anyone else gets the birth-date step.
	if claims, _, ok := a.workerFromCookie(r); ok && a.Permits != nil && a.ticketAvailable() && a.ownsValidPermit(r, claims, lep) {
		a.openTicketFor(w, r, lep)
		return
	}
	a.ticketRender(w, r, http.StatusOK, TicketData{LEP: lep})
}

// birthFromDigits reads DDMMYYYY out of whatever was typed: only the digits count,
// so 05/03/1988 and 05031988 are the same answer.
func birthFromDigits(s string) (dates.Day, bool) {
	d := phone.Digits(s)
	if len(d) != 8 {
		return "", false
	}
	day, err := dates.ParseDay(d[4:] + "-" + d[2:4] + "-" + d[:2])
	return day, err == nil
}

// ticketAccess is the birth-date check. Every way it can fail gives the same
// message, so it never says which half was wrong (tickets doc section 6).
func (a *App) ticketAccess(w http.ResponseWriter, r *http.Request) {
	pol := a.policy()
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	lep := clip(strings.ToUpper(strings.TrimSpace(r.PostFormValue("lep"))), 12)
	fail := func(status int, msg string) { a.ticketRender(w, r, status, TicketData{LEP: lep, Err: msg}) }

	ip := a.clientIP(r)
	if a.Limit.Count("tka:"+ip, submitWindow) >= ticketTriesPerIP {
		fail(http.StatusTooManyRequests, "Too many attempts. Please try again later.")
		return
	}
	a.Limit.Add("tka:" + ip)
	if !a.ticketAvailable() {
		fail(http.StatusServiceUnavailable, "This page is not available right now. Please try again later.")
		return
	}
	ok, err := a.Turnstile.Verify(r.Context(), r.PostFormValue(turnstileField), ip)
	if err != nil {
		log.Printf("turnstile: %v", err)
		fail(http.StatusServiceUnavailable, "The security check could not be completed. Please try again.")
		return
	}
	if !ok {
		fail(http.StatusBadRequest, "The security check failed. Please try again.")
		return
	}
	if !lepPattern.MatchString(lep) {
		fail(http.StatusBadRequest, accessError)
		return
	}
	lockKey, lockFor := "tk:"+lep, time.Duration(pol.TicketLockMinutes)*time.Minute
	if a.Limit.Count(lockKey, lockFor) >= pol.TicketMaxTries {
		fail(http.StatusTooManyRequests, fmt.Sprintf("Too many attempts. Please try again in %d minutes.", pol.TicketLockMinutes))
		return
	}
	dob, valid := birthFromDigits(r.PostFormValue("dob"))
	matched := false
	if valid {
		var err error
		if matched, err = a.accessMatches(r.Context(), lep, dob); err != nil {
			log.Printf("ticket access: %v", err)
			fail(http.StatusServiceUnavailable, "This page is not available right now. Please try again later.")
			return
		}
	}
	if !matched {
		a.Limit.Add(lockKey)
		fail(http.StatusBadRequest, accessError)
		return
	}
	a.Limit.Forget(lockKey) // a correct entry clears the failed tries
	token := signAccess(a.Secret, accessClaims{LEP: lep, Expires: a.Clock.Now().AddMinutes(pol.TicketSessionMinutes)})
	d, status, msg := a.ticketOpen(r.Context(), lep, token)
	if status != 0 {
		fail(status, msg)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	a.ticketRender(w, r, http.StatusOK, d)
}

// accessMatches is true when the permit exists, is Approved, and holds this birth date.
func (a *App) accessMatches(ctx context.Context, lep string, dob dates.Day) (bool, error) {
	if strings.HasPrefix(lep, "WVR-") {
		info, found, err := a.Waivers.Access(ctx, lep)
		return found && info.Status == waiver.StatusApproved && info.DOB == string(dob), err
	}
	info, found, err := a.Permits.Access(ctx, lep)
	return found && info.Status == permit.StatusApproved && info.DOB == string(dob), err
}

// ticketOpen builds the opened page: the permit's name and every ticket on it.
// A non-zero status is an error page.
func (a *App) ticketOpen(ctx context.Context, lep, token string) (TicketData, int, string) {
	d := TicketData{LEP: lep, Open: true, Token: token}
	rows, err := a.Tickets.ForLEP(ctx, lep)
	if err != nil {
		log.Printf("tickets of %s: %v", lep, err)
		return d, http.StatusServiceUnavailable, "This page is not available right now. Please try again later."
	}
	for _, t := range rows {
		d.Tickets = append(d.Tickets, TicketRow{ID: t.ID, Arrival: string(t.Arrival), Departure: string(t.Departure),
			Location: t.Location, Status: string(t.Status), Pattern: t.Pattern, Days: t.Days()})
	}
	if strings.HasPrefix(lep, "WVR-") {
		info, found, err := a.Waivers.Access(ctx, lep)
		if err != nil || !found {
			return d, http.StatusServiceUnavailable, "This page is not available right now. Please try again later."
		}
		d.Name, d.Waiver, d.Visit = info.Name, true, fmt.Sprintf("%s to %s", info.From, info.To)
		return d, 0, ""
	}
	info, found, err := a.Permits.Access(ctx, lep)
	if err != nil || !found {
		return d, http.StatusServiceUnavailable, "This page is not available right now. Please try again later."
	}
	d.Name, d.CanPropose, d.FitNote = info.WorkerName, true, ticket.IsMCUFitWithNote(info.McuResult)
	return d, 0, ""
}
