package web

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"entry-permit/internal/company"
	"entry-permit/internal/dates"
	"entry-permit/internal/permit"
	"entry-permit/internal/phone"
	"entry-permit/internal/policy"
	"entry-permit/internal/reflist"
	"entry-permit/internal/ticket"
	"entry-permit/internal/ticketlog"
	"entry-permit/internal/waiver"
	"entry-permit/internal/waiverlog"
	"entry-permit/internal/worker"
)

const (
	waiverTitle = "Visitor Entry Permit"
	waiverFiles = 4 // ID, photo, appointment letter, visa
)

// waiverGenders are the two choices on the form.
var waiverGenders = []string{"Male", "Female"}

// waiverLimits caps each text box to the width of its column, so a long paste
// is cut here and never becomes a database error.
var waiverLimits = map[string]int{
	"applicant_type": 20, "company_id": 12, "name": 120, "gender": 10, "dob": 10, "nationality": 60, "nik": 40,
	"position": 120, "department": 120, "visit_location": 120, "purpose": 255, "from": 10, "to": 10,
	"requester_name": 120, "requester_email": 160, "spare_email": 160, "phone": 20, "risk": 500, "precaution": 500, "prevent": 500,
	"medicine1": 120, "dosage1": 120, "medicine2": 120, "dosage2": 120, "medicine3": 120, "dosage3": 120, "medicine_notes": 500, "passport_until": 10, "visa_until": 10,
}

// waiverFileKinds maps the upload box to the label shown in a message.
var waiverFileKinds = []struct{ field, label string }{
	{"id_doc", "ID document"}, {"id_photo", "ID photo"}, {"letter_doc", "Appointment letter"}, {"visa_doc", "Visa document"},
}

// WaiverData feeds the Visitor Entry Permit page (the visiting waiver, WVR-).
type WaiverData struct {
	Available      bool
	Err            string
	Vals           map[string]string // what the boxes hold after a refused try
	Companies      []company.Company
	Nationalities  []string // empty when the admin list has no entries: the box is then free text
	Types          []string
	Genders        []string
	Risk, Health   []waiver.Question
	Nonconformance string
	Done           *WaiverDone
}

// WaiverDone is the page shown after a waiver is saved.
type WaiverDone struct {
	RequestID, Name, Company, Email string
	From, To                        dates.Day
	Days                            int
}

// waiverInput is what a request holds once it has been read and the company found.
type waiverInput struct {
	Form     waiver.Form
	Company  company.Company
	DOB      dates.Day
	From, To dates.Day
	Spare    string // optional second email, cleaned; joined to the main one once both are checked
}

func (a *App) waiverAvailable() bool {
	return a.Waivers != nil && a.Tickets != nil && a.Companies != nil && a.Turnstile != nil && (a.TurnstileDev || a.TurnstileSite != "")
}

// nationalities reads the admin-managed list. An empty answer means "no list".
func (a *App) nationalities(ctx context.Context) []string {
	def, ok := reflist.Find("nationality")
	if !ok || a.Lists == nil {
		return nil
	}
	items, err := a.Lists.Items(ctx, def, false)
	if err != nil {
		log.Printf("nationality list: %v", err)
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if len(it.Values) > 0 && strings.TrimSpace(it.Values[0]) != "" {
			out = append(out, it.Values[0])
		}
	}
	return out
}

func (a *App) waiverData(ctx context.Context, pol policy.Policy, vals map[string]string, msg string) WaiverData {
	d := WaiverData{Available: a.waiverAvailable(), Err: msg, Vals: vals, Genders: waiverGenders,
		Risk: waiver.RiskQuestions, Health: waiver.HealthQuestions, Nonconformance: waiver.NonconformanceText}
	d.Types = []string{waiver.TypeGovernment}
	if pol.WaiverVisitorEnabled {
		d.Types = []string{waiver.TypeVisitorCompany, waiver.TypeGovernment}
	}
	if !d.Available {
		return d
	}
	list, err := a.Companies.List(ctx, false) // never offer an archived company
	if err != nil {
		log.Printf("waiver companies: %v", err)
		d.Available = false
		return d
	}
	d.Companies, d.Nationalities = list, a.nationalities(ctx)
	return d
}

func (a *App) waiverPage(w http.ResponseWriter, r *http.Request) {
	a.render(w, "waiver", http.StatusOK, View{Title: waiverTitle, User: a.currentUser(r), My: a.myData(),
		Waiver: a.waiverData(r.Context(), a.policy(), nil, "")})
}

// clip cuts s to n characters (not bytes, so a name is never cut mid-letter).
func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// waiverSubmit is the whole waiver request in one step: there is no worker
// record to prove, so Turnstile and the rate limit are what stop abuse.
func (a *App) waiverSubmit(w http.ResponseWriter, r *http.Request) {
	pol := a.policy()
	maxBytes := pol.UploadMaxKB * 1024
	vals := map[string]string{}
	fail := func(status int, msg string) {
		a.render(w, "waiver", status, View{Title: waiverTitle, User: a.currentUser(r), My: a.myData(),
			Waiver: a.waiverData(r.Context(), pol, vals, msg)})
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64(waiverFiles*maxBytes+1<<20))
	if err := r.ParseMultipartForm(multipartMemKB << 10); err != nil {
		fail(http.StatusRequestEntityTooLarge, fmt.Sprintf("The upload was too large. Each file may be at most %d KB.", pol.UploadMaxKB))
		return
	}
	defer r.MultipartForm.RemoveAll()
	for f, n := range waiverLimits {
		vals[f] = clip(strings.TrimSpace(r.PostFormValue(f)), n)
	}
	vals["takes_medicine"], vals["declaration"] = r.PostFormValue("takes_medicine"), r.PostFormValue("declaration")
	// The answers are kept as the posted word so a refused try shows the same radio buttons ticked.
	for _, q := range waiver.RiskQuestions {
		vals["risk_"+q.Key] = clip(r.PostFormValue("risk_"+q.Key), 3)
	}
	for _, q := range waiver.HealthQuestions {
		vals["health_"+q.Key] = clip(r.PostFormValue("health_"+q.Key), 3)
	}

	ip := a.clientIP(r)
	if a.Limit.Count("wvr:"+ip, submitWindow) >= submitsPerHour {
		fail(http.StatusTooManyRequests, "Too many attempts. Please try again later.")
		return
	}
	a.Limit.Add("wvr:" + ip)
	if !a.waiverAvailable() {
		fail(http.StatusServiceUnavailable, "This form is not available right now. Please try again later.")
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

	in, status, msg := a.readWaiver(r, vals)
	if status != 0 {
		fail(status, msg)
		return
	}
	checked, err := waiver.ValidateForm(pol, in.Form, a.Clock.Today())
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	// The spare email is optional: the same address twice is one address, a bad one is refused,
	// and both addresses must fit the 160 character column as "main,spare".
	if in.Spare == in.Form.RequesterEmail {
		in.Spare = ""
	}
	switch {
	case in.Spare != "" && !validEmail(in.Spare):
		fail(http.StatusBadRequest, "The spare email address is not valid. Fix it or leave it empty.")
		return
	case len(joinEmails(in.Form.RequesterEmail, in.Spare)) > maxEmailLength:
		fail(http.StatusBadRequest, "The two email addresses are too long together (at most 160 characters). Use shorter addresses.")
		return
	}
	if msg := a.checkMailDomains(pol, in.Form.RequesterEmail, in.Spare); msg != "" {
		fail(http.StatusBadRequest, msg)
		return
	}
	in.Form.RequesterEmail = joinEmails(in.Form.RequesterEmail, in.Spare)
	if status, msg := a.waiverRulesNeedingStorage(r.Context(), pol, in, checked); status != 0 {
		fail(status, msg)
		return
	}
	a.storeWaiver(w, r, in, checked, pol, fail)
}

// readWaiver turns the posted values into the rules' Form. A non-zero status is
// the error page to show.
func (a *App) readWaiver(r *http.Request, v map[string]string) (waiverInput, int, string) {
	var in waiverInput
	if id, err := strconv.ParseInt(v["company_id"], 10, 64); err == nil {
		c, found, err := a.Companies.Find(r.Context(), id)
		if err != nil {
			log.Printf("waiver company: %v", err)
			return in, http.StatusServiceUnavailable, "This form is not available right now. Please try again later."
		}
		// An archived company is kept for old permits but is not a choice any more.
		if found && !c.Archived {
			in.Company = c
		}
	}
	days := map[string]*dates.Day{"dob": &in.DOB, "from": &in.From, "to": &in.To}
	for field, out := range days {
		d, err := parseFormDay(v[field])
		if err != nil {
			return in, http.StatusBadRequest, err.Error()
		}
		*out = d
	}
	passport, err1 := parseFormDay(v["passport_until"])
	visa, err2 := parseFormDay(v["visa_until"])
	if err := firstErr(err1, err2); err != nil {
		return in, http.StatusBadRequest, err.Error()
	}
	nat, msg := a.pickNationality(r.Context(), v["nationality"])
	if msg != "" {
		return in, http.StatusBadRequest, msg
	}
	foreign := nat != "" && worker.Employee{Nationality: nat}.IsForeign()
	in.Spare = cleanEmail(v["spare_email"])
	// The two Yes / No / N/A cards. A missing answer is refused in readWaiver's caller as a plain
	// message, so the form is shown again with what the person already typed.
	risk, rerr := waiver.ParseAnswers("risk", waiver.RiskQuestions, func(f string) string { return v[f] })
	health, herr := waiver.ParseAnswers("health", waiver.HealthQuestions, func(f string) string { return v[f] })
	if err := firstErr(rerr, herr); err != nil {
		return in, http.StatusBadRequest, err.Error()
	}
	in.Form = waiver.Form{
		ApplicantType: v["applicant_type"], CompanyChosen: in.Company.ID != 0, CompanyIsGov: in.Company.Gov,
		Name: v["name"], Gender: pickOne(v["gender"], waiverGenders), DOB: string(in.DOB), Nationality: nat, NIK: v["nik"],
		Position: v["position"], Department: v["department"], VisitLocation: v["visit_location"], Purpose: v["purpose"],
		From: string(in.From), To: string(in.To),
		RequesterName: v["requester_name"], RequesterEmail: cleanEmail(v["requester_email"]), Phone: v["phone"],
		Risk: v["risk"], Precaution: v["precaution"], Prevent: v["prevent"],
		TakesMedicine: v["takes_medicine"] != "", Medicine1: v["medicine1"], Dosage1: v["dosage1"],
		Medicine2: v["medicine2"], Dosage2: v["dosage2"], Medicine3: v["medicine3"], Dosage3: v["dosage3"],
		MedicineNotes: v["medicine_notes"], RiskAnswers: risk, HealthAnswers: health,
		Declaration: v["declaration"] != "",
		HasID:       uploaded(r, "id_doc") != nil, HasPhoto: uploaded(r, "id_photo") != nil, HasLetter: uploaded(r, "letter_doc") != nil,
		Foreign: foreign, PassportUntil: passport, VisaUntil: visa, HasVisa: uploaded(r, "visa_doc") != nil,
	}
	return in, 0, ""
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// pickOne returns the choice that equals v (any case), else "".
func pickOne(v string, choices []string) string {
	for _, c := range choices {
		if strings.EqualFold(strings.TrimSpace(v), c) {
			return c
		}
	}
	return ""
}

// pickNationality checks the typed nationality against the admin list when
// there is one, and returns the listed spelling.
func (a *App) pickNationality(ctx context.Context, typed string) (string, string) {
	typed = strings.TrimSpace(typed)
	list := a.nationalities(ctx)
	if typed == "" || len(list) == 0 {
		return typed, ""
	}
	if got := pickOne(typed, list); got != "" {
		return got, ""
	}
	return "", "Choose the nationality from the list."
}

// waiverRulesNeedingStorage applies the two checks that look at earlier waivers:
// the blocking waiver and the yearly company cap.
func (a *App) waiverRulesNeedingStorage(ctx context.Context, pol policy.Policy, in waiverInput, c waiver.Checked) (int, string) {
	today := a.Clock.Today()
	open, err := a.Waivers.Open(ctx, today)
	if err != nil {
		log.Printf("open waivers: %v", err)
		return http.StatusServiceUnavailable, "This form is not available right now. Please try again later."
	}
	typ := waiver.NormalizeType(in.Form.ApplicantType)
	cand := waiver.Waiver{NIK: c.NIK, Name: in.Form.Name, DOB: string(in.DOB), Company: in.Company.Name, Type: typ, From: in.From, To: in.To}
	if ex := waiver.Blocking(cand, open, today); ex != nil {
		return http.StatusConflict, fmt.Sprintf("This visitor already has a waiver that is open (Request %s, visit %s to %s). Please wait for its decision, or ask for it to be cancelled, instead of submitting a new one.", ex.ID, ex.From, ex.To)
	}
	year := string(in.From)[:4]
	rows, err := a.Waivers.CapRows(ctx, year)
	if err != nil {
		log.Printf("waiver cap rows: %v", err)
		return http.StatusServiceUnavailable, "This form is not available right now. Please try again later."
	}
	if err := waiver.CheckCompanyCap(pol, typ, waiver.CapUsed(rows, in.Company.Name, year), c.Days); err != nil {
		return http.StatusBadRequest, err.Error()
	}
	return 0, ""
}

// storeWaiver saves the files and then the row. A failure after the files are
// written removes them again, so a refused request leaves nothing on disk.
func (a *App) storeWaiver(w http.ResponseWriter, r *http.Request, in waiverInput, c waiver.Checked, pol policy.Policy, fail func(int, string)) {
	maxBytes := pol.UploadMaxKB * 1024
	id, err := permit.UniqueID("WVR-", idAttempts, func(id string) (bool, error) { return a.Waivers.Exists(r.Context(), id) })
	if err != nil {
		log.Printf("waiver id: %v", err)
		fail(http.StatusServiceUnavailable, "This form is not available right now. Please try again later.")
		return
	}
	paths := map[string]string{}
	for _, k := range waiverFileKinds {
		h := uploaded(r, k.field)
		if h == nil || (k.field == "visa_doc" && !in.Form.Foreign) { // a visa file posted for an Indonesian is ignored
			continue
		}
		data, err := readUpload(h, maxBytes)
		if err == nil {
			paths[k.field], err = a.Uploads.Save(id, k.field, data, maxBytes)
		}
		if err != nil {
			_ = a.Uploads.Remove(id)
			fail(http.StatusBadRequest, uploadMessage(k.label, err, maxBytes))
			return
		}
	}
	f, now := in.Form, string(a.Clock.Now())
	row := waiverlog.NewWaiver{
		RequestID: id, Status: waiver.StatusPendingDoctor, ApplicantType: waiver.NormalizeType(f.ApplicantType),
		CompanyID: in.Company.ID, Company: in.Company.Name, Name: f.Name, Gender: f.Gender, DOB: f.DOB,
		Nationality: f.Nationality, NIK: c.NIK, Position: f.Position, Department: f.Department,
		VisitLocation: f.VisitLocation, Purpose: f.Purpose, VisitFrom: f.From, VisitTo: f.To, Days: c.Days,
		RequesterName: f.RequesterName, RequesterEmail: f.RequesterEmail, Phone: phone.Normalize(f.Phone),
		Risk: f.Risk, Precaution: f.Precaution, Prevent: f.Prevent,
		Medicine2: f.Medicine2, Dosage2: f.Dosage2, Medicine3: f.Medicine3, Dosage3: f.Dosage3, MedicineNotes: f.MedicineNotes,
		RiskAnswers: f.RiskAnswers, HealthAnswers: f.HealthAnswers,
		IDDoc: paths["id_doc"], IDPhoto: paths["id_photo"], LetterDoc: paths["letter_doc"], VisaDoc: paths["visa_doc"],
		DeclaredAt: now, CreatedAt: now,
	}
	if f.TakesMedicine {
		row.TakesMedicine, row.Medicine1, row.Dosage1 = true, f.Medicine1, f.Dosage1
	}
	if f.Foreign {
		row.PassportUntil, row.VisaUntil = string(f.PassportUntil), string(f.VisaUntil)
	}
	// The visitor's ticket exists from submit (permits doc 8): PendingDoctor, rotation 1.
	// Final approval activates it (slice 4a).
	tid, err := permit.UniqueID("TKT-", idAttempts, func(id string) (bool, error) { return a.Tickets.Exists(r.Context(), id) })
	if err != nil {
		log.Printf("waiver ticket id: %v", err)
		_ = a.Uploads.Remove(id)
		fail(http.StatusServiceUnavailable, "This form is not available right now. Please try again later.")
		return
	}
	tk := ticketlog.NewTicket{
		ID: tid, LEP: id, Work: false, Status: string(ticket.StatusPendingDoctor),
		Arrival: f.From, Departure: f.To, Location: ticket.NormalizeLocation(f.VisitLocation),
		Pattern: ticket.PatternNormal, RotationNo: 1, RequesterEmail: f.RequesterEmail, SubmittedAt: now,
	}
	if err := a.Waivers.CreateWithTicket(r.Context(), row, tk); err != nil {
		log.Printf("store waiver: %v", err)
		_ = a.Uploads.Remove(id)
		fail(http.StatusServiceUnavailable, "The request could not be saved. Please try again.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	a.render(w, "waiver_done", http.StatusOK, View{Title: "Request sent", User: a.currentUser(r), My: a.myData(),
		Waiver: WaiverData{Done: &WaiverDone{RequestID: id, Name: f.Name, Company: in.Company.Name, Email: f.RequesterEmail,
			From: in.From, To: in.To, Days: c.Days}}})
}
