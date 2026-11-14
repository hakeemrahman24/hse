package web

import (
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"regexp"
	"strings"

	"entry-permit/internal/dates"
	"entry-permit/internal/permit"
	"entry-permit/internal/permitlog"
	"entry-permit/internal/policy"
	"entry-permit/internal/region"
	"entry-permit/internal/worker"
)

const (
	maxPersonName  = 120
	maxEmailLength = 160
)

// workLocations are the two choices on the form. The ticket step picks its own
// location later (ticket rules), so this is only stored on the permit.
var workLocations = []string{"Onshore", "Offshore"}

// PermitData feeds the Work Entry Permit page (slice 3b: part 1, who is the
// permit for and may a new one be requested).
type PermitData struct {
	Available                    bool
	Err                          string
	ErrField                     string // the input the message is about (its name), so the step-by-step form opens that step
	RequesterName, RequesterMail string // kept so a failed try does not retype everything
	SpareMail                    string // optional second address, kept the same way
	WorkLocation, Name, DOB      string
	Locations                    []string
	Result                       *PermitCheck
	Done                         *PermitDone
}

// PermitDone is the page shown after a request is saved.
type PermitDone struct {
	RequestID, Worker, Company, Reason, Email string
	ValidUntil                                dates.Day
}

// PermitCheck is what part 1 shows once the worker is proven.
type PermitCheck struct {
	Worker        worker.Employee
	Foreign       bool
	ValidUntil    dates.Day // earliest date known so far
	Reason        string
	Blocked       bool
	BlockedText   string
	Reuse         bool // an earlier permit exists and its details will be offered again
	McuRenewal    bool
	RequesterName string
	RequesterMail string
	WorkLocation  string

	// Part 2 of the form.
	Token     string
	Regencies []region.Regency
	Vals      map[string]string // what the boxes hold (the earlier permit's details, or what was typed)
	Prefill   permitlog.Prefill
	Sec       Sections
}

func (a *App) permitData() PermitData {
	return PermitData{Available: a.lookupAvailable() && a.Permits != nil, Locations: workLocations}
}

func (a *App) permitPage(w http.ResponseWriter, r *http.Request) {
	a.render(w, "permit", http.StatusOK, View{Title: "Work Entry Permit", User: a.currentUser(r), My: a.myData(), Permit: a.permitData()})
}

// permitStart is part 1 of the form. It saves nothing: it proves who the worker
// is, checks the MCU, shows the earliest date the permit could be valid until,
// and says whether the duplicate gate lets a new request through.
func (a *App) permitStart(w http.ResponseWriter, r *http.Request) {
	d := a.permitData()
	fail := func(status int, msg string) {
		d.Err = msg
		a.render(w, "permit", status, View{Title: "Work Entry Permit", User: a.currentUser(r), My: a.myData(), Permit: d})
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	d.RequesterName = strings.Join(strings.Fields(r.PostFormValue("requester_name")), " ")
	d.RequesterMail = cleanEmail(r.PostFormValue("requester_email"))
	d.SpareMail = cleanEmail(r.PostFormValue("spare_email"))
	if d.SpareMail == d.RequesterMail {
		d.SpareMail = "" // the same address twice is one address (the old system dropped repeats too)
	}
	d.WorkLocation = strings.TrimSpace(r.PostFormValue("work_location"))
	d.Name = strings.Join(strings.Fields(r.PostFormValue("name")), " ")
	d.DOB = strings.TrimSpace(r.PostFormValue("dob"))

	// Cheap checks on what the requester typed come first and do not count as
	// failed lookups: a typo in an email must not lock anyone out.
	if msg := checkRequester(d.RequesterName, d.RequesterMail, d.SpareMail, d.WorkLocation); msg != "" {
		fail(http.StatusBadRequest, msg)
		return
	}
	// WHY here and not only at submit: a mistyped domain should be caught before the requester
	// fills in the long part 2 (the old system checked on the first page too).
	if msg := a.checkMailDomains(a.policy(), d.RequesterMail, d.SpareMail); msg != "" {
		fail(http.StatusBadRequest, msg)
		return
	}
	// WHY one comma list: the stored requester email, the signed token and the mail sender all
	// already read "primary,spare" (mail.CleanAddresses), as the old system stored it.
	both := joinEmails(d.RequesterMail, d.SpareMail)
	if a.Permits == nil {
		fail(http.StatusServiceUnavailable, "This form is not available right now. Please try again later.")
		return
	}
	e, status, msg := a.verifyWorker(r, d.Name, d.DOB, false)
	if status != 0 {
		fail(status, msg)
		return
	}
	pol := a.policy()
	check, status, msg := a.checkPermit(r, e, "")
	if status != 0 {
		fail(status, msg)
		return
	}
	check.RequesterName, check.RequesterMail, check.WorkLocation = d.RequesterName, both, d.WorkLocation
	if !check.Blocked {
		// Part 2 carries this proof forward. WHY signed: the worker's date of
		// birth was typed once, and the token lets part 2 look the worker up again.
		check.Token = signStep(a.Secret, stepClaims{Name: e.Name, DOB: string(dates.Day(e.DOB)), RequesterName: d.RequesterName,
			RequesterEmail: both, Location: d.WorkLocation, Expires: a.Clock.Now().AddHours(stepTokenHours)})
		check.Regencies = a.regencies(r.Context())
		check.Vals = prefillValues(check.Prefill, check.Regencies)
		check.Sec = a.sections(r.Context(), e, pol)
	}
	d.Result = &check
	a.render(w, "permit", http.StatusOK, View{Title: "Work Entry Permit", User: a.currentUser(r), My: a.myData(), Permit: d})
}

// checkRequester returns a message for the first problem, or "". The mail
// domain is checked for a mailbox only at submit (slice 3c, setting email_mx_check).
func checkRequester(name, email, spare, location string) string {
	switch {
	case len([]rune(name)) < 2 || len([]rune(name)) > maxPersonName:
		return "Enter your own name (the person making this request)."
	case len(email) > maxEmailLength || !validEmail(email):
		return "Enter a valid email address. The decision will be sent there."
	case spare != "" && !validEmail(spare):
		return "The spare email address is not valid. Fix it or leave it empty."
	case len(joinEmails(email, spare)) > maxEmailLength:
		return "The two email addresses are too long together (at most 160 characters). Use shorter addresses."
	case location != workLocations[0] && location != workLocations[1]:
		return "Choose Onshore or Offshore."
	}
	return ""
}

// emailShape is the old system's rule: something@something.xx (a dot and at least two letters
// after it, no spaces).
var emailShape = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]{2,}$`)

// cleanEmail is the old system's clean-up: every space removed, lower case.
func cleanEmail(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), ""))
}

// joinEmails makes the stored "primary,spare" list; an empty spare leaves just the primary.
func joinEmails(primary, spare string) string {
	if spare == "" {
		return primary
	}
	return primary + "," + spare
}

// validEmail accepts a plain address only: "Name <a@b.c>" and lists are refused.
func validEmail(s string) bool {
	addr, err := mail.ParseAddress(s)
	return err == nil && addr.Address == s && addr.Name == "" && emailShape.MatchString(s)
}

// checkMailDomains asks, for each address, whether its domain can receive mail (an MX record).
// It returns the message for the first address that cannot, or "". A lookup that fails is
// treated as fine by the checker itself, as in the old system, so a DNS hiccup blocks nobody.
func (a *App) checkMailDomains(pol policy.Policy, emails ...string) string {
	if !pol.EmailMXCheck || a.MXCheck == nil {
		return ""
	}
	seen := map[string]bool{}
	for _, list := range emails {
		for _, e := range strings.Split(list, ",") {
			if e == "" {
				continue
			}
			domain := e[strings.LastIndex(e, "@")+1:]
			if seen[domain] {
				continue
			}
			seen[domain] = true
			if err := a.MXCheck(domain); err != nil {
				return "The email domain of " + e + " cannot receive mail. Check the spelling, then try again."
			}
		}
	}
	return ""
}

// checkPermit applies the MCU check, the validity preview and the duplicate gate
// (business-rules-permits.md sections 3, 4, 7.10). A non-zero status is an error page.
func (a *App) checkPermit(r *http.Request, e worker.Employee, visaExpiry dates.Day) (PermitCheck, int, string) {
	today := a.Clock.Today()
	c := PermitCheck{Worker: e, Foreign: e.IsForeign()}
	mcu := dates.Day(e.McuValidUntil)

	// medpost only lists Fit / Fit with note with a date still ahead; checked
	// again here because a permit must never rest on one trusted answer.
	if res := strings.ToLower(strings.TrimSpace(e.McuResult)); (res != "fit" && res != "fit with note") || mcu == "" || mcu < today {
		c.Blocked, c.BlockedText = true, "This worker has no valid MCU (a medical checkup result of Fit or Fit with note that has not expired). A permit cannot be requested yet. The worker's company must first send the result through the medical checkup portal (ask the medical team for its address if you do not have it); then come back and check again."
		return c, 0, ""
	}
	pol := a.policy()
	valid, reason, err := permit.ResolveValidity(pol, permit.ValidityInput{Foreign: c.Foreign, McuValidUntil: mcu})
	if err != nil {
		c.Blocked, c.BlockedText = true, err.Error()
		return c, 0, ""
	}
	c.ValidUntil, c.Reason = valid, string(reason)

	found, err := a.Permits.Latest(r.Context(), e.PatientID, e.PersonalID)
	if err != nil {
		log.Printf("latest permit: %v", err)
		return PermitCheck{}, http.StatusServiceUnavailable, "This form is not available right now. Please try again later."
	}
	dec := permit.Decide(found.Previous, permit.Current{Company: e.Company, McuValidUntil: mcu, Foreign: c.Foreign, VisaExpiry: visaExpiry}, today)
	switch dec.Kind {
	case permit.KindActive:
		c.Blocked, c.BlockedText = true, blockedText(found)
	case permit.KindReuse:
		c.Reuse, c.McuRenewal, c.Prefill = true, dec.McuRenewal, found.Prefill
	}
	return c, 0, ""
}

// blockedText is the message the duplicate gate gives (business rules section 4).
func blockedText(f permitlog.Found) string {
	switch {
	case f.LepNo != "":
		return fmt.Sprintf("This employee already has an active permit (Request %s, LEP %s, valid until %s). Please use the existing permit instead of submitting a new one.",
			f.RequestID, f.LepNo, f.Previous.LepValidUntil)
	default:
		return fmt.Sprintf("This employee already has a request in progress (Request %s). Please wait for its decision instead of submitting a new one.", f.RequestID)
	}
}

// prefillValues turns an earlier permit's details into the part-2 box values.
// The regency is matched by name to the current list, so a renamed regency
// simply starts empty instead of failing.
func prefillValues(p permitlog.Prefill, list []region.Regency) map[string]string {
	v := map[string]string{"phone": p.Phone, "emergency_name": p.EmergencyName, "emergency_phone": p.EmergencyPhone,
		"district": p.District, "village": p.Village, "street": p.Street}
	for _, rg := range list {
		if p.Regency != "" && rg.Name == p.Regency {
			v["regency"] = rg.Code
		}
	}
	return v
}

// emailChecksPerMinute limits the check-as-you-leave-the-box lookups, which cost a DNS query each.
const emailChecksPerMinute = 30

// checkEmailAPI answers "can this address receive mail?" while the person fills in the form, as soon as they
// leave the email box, so a typo is caught there and not after the whole form is sent. It uses the same rule as
// the submit (checkMailDomains), so the two can never disagree. It says nothing about whether the mailbox exists.
func (a *App) checkEmailAPI(w http.ResponseWriter, r *http.Request) {
	key := "mx:" + a.clientIP(r)
	if a.Limit.Count(key, searchWindow) >= emailChecksPerMinute {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"ok": true}) // never block the person because of the limit
		return
	}
	a.Limit.Add(key)
	email := cleanEmail(r.URL.Query().Get("email"))
	if len(email) > 160 || !validEmail(email) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": "Enter a valid email address, like name@company.com."})
		return
	}
	if msg := a.checkMailDomains(a.policy(), email); msg != "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
