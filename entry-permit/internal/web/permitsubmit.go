package web

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"entry-permit/internal/textclean"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"entry-permit/internal/dates"
	"entry-permit/internal/filestore"
	"entry-permit/internal/permit"
	"entry-permit/internal/permitlog"
	"entry-permit/internal/phone"
	"entry-permit/internal/policy"
	"entry-permit/internal/position"
	"entry-permit/internal/region"
	"entry-permit/internal/worker"
)

const (
	// stepTokenHours: how long part 2 may take. Generous, because the worker's
	// documents may have to be found and scanned before the form is sent.
	stepTokenHours = 2
	submitsPerHour = 10
	submitWindow   = time.Hour
	maxFieldLen    = 240
	multipartMemKB = 1024
	idAttempts     = 10
	maxFiles       = 6
)

// stepClaims is what part 1 proved. It is signed, not stored, so no server
// state is needed between the two parts. Only the name and date of birth are
// kept: at submit the worker is looked up in medpost again, so the permit
// always holds the freshest MCU and company.
type stepClaims struct {
	Name, DOB, RequesterName, RequesterEmail, Location string
	Expires                                            dates.Moment
}

var tokenEnc = base64.RawURLEncoding

func stepMAC(secret, payload string) []byte {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte("wep-step|")) // keeps these tokens apart from login cookies
	h.Write([]byte(payload))
	return h.Sum(nil)
}

func signStep(secret string, c stepClaims) string {
	b, _ := json.Marshal(c)
	return tokenEnc.EncodeToString(b) + "." + tokenEnc.EncodeToString(stepMAC(secret, string(b)))
}

// verifyStep returns the claims when the token is genuine and not expired.
func verifyStep(secret, tok string, now dates.Moment) (stepClaims, bool) {
	var c stepClaims
	parts := strings.Split(tok, ".")
	if len(parts) != 2 {
		return c, false
	}
	payload, err1 := tokenEnc.DecodeString(parts[0])
	sig, err2 := tokenEnc.DecodeString(parts[1])
	if err1 != nil || err2 != nil || !hmac.Equal(sig, stepMAC(secret, string(payload))) {
		return c, false
	}
	if json.Unmarshal(payload, &c) != nil || c.Name == "" || now >= c.Expires {
		return c, false
	}
	return c, true
}

// regencies returns the address list, or nil when the region source is not wired.
func (a *App) regencies(ctx context.Context) []region.Regency {
	if a.Malaria == nil || a.Malaria.Regions == nil {
		return nil
	}
	list, err := a.Malaria.Regions.Regencies(ctx)
	if err != nil {
		log.Printf("regencies: %v", err)
		return nil
	}
	return list
}

func findRegency(list []region.Regency, code string) (region.Regency, bool) {
	for _, r := range list {
		if r.Code == code {
			return r, true
		}
	}
	return region.Regency{}, false
}

var dayInput = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})/(\d{4})$`)

var errDayFormat = errors.New("Enter dates as dd/mm/yyyy, for example 31/12/2027.")

// parseFormDay turns dd/mm/yyyy into a Day. Blank is allowed and means "not given".
func parseFormDay(s string) (dates.Day, error) {
	s = dates.CleanTyped(s)
	if s == "" {
		return "", nil
	}
	m := dayInput.FindStringSubmatch(s)
	if m == nil {
		return "", errDayFormat
	}
	d, err := dates.ParseDay(fmt.Sprintf("%04d-%02d-%02d", atoi(m[3]), atoi(m[2]), atoi(m[1])))
	if err != nil {
		return "", errDayFormat
	}
	return d, nil
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

// partTwoFields are the text boxes whose values are kept when a try fails.
var partTwoFields = []string{"phone", "emergency_name", "emergency_phone", "regency", "district", "village", "street",
	"visa_type", "visa_code", "visa_issued", "visa_issued_by", "visa_expiry", "rptka_issued", "rptka_days", "rptka_expiry",
	"immigration_until", "police_until", "passport_until", "contract_until", "bpjs_kes", "bpjs_tk", "cert_until"}

// fileKinds maps the upload box name to the label shown in a message.
var fileKinds = []struct{ field, label string }{
	{"id_doc", "ID document"}, {"id_photo", "ID photo"}, {"visa_doc", "Work visa document"},
	{"rptka_doc", "RPTKA document"}, {"immigration_doc", "Immigration letter"}, {"police_doc", "Police letter"},
	{"contract_doc", "Contract"}, {"bpjs_kes_doc", "BPJS Kesehatan document"}, {"bpjs_tk_doc", "BPJS Ketenagakerjaan document"},
	{"cert_doc", "Certificate of competence"},
}

// Sections says which optional parts of part 2 apply to this worker. It is
// decided on the server from Settings, the worker's nationality and the position
// list, and the same answer drives both what is shown and what is accepted.
type Sections struct {
	ContractShown, ContractRequired bool
	BpjsKesShown, BpjsKesRequired   bool
	BpjsTkShown, BpjsTkRequired     bool
	CertNeeded                      bool
	CertName                        string
	CertFixedUntil                  dates.Day
	Position                        position.Result
}

// sections works out the optional parts for a worker.
func (a *App) sections(ctx context.Context, e worker.Employee, pol policy.Policy) Sections {
	sec := Sections{ContractShown: pol.ContractShown, ContractRequired: pol.ContractRequired}
	if !e.IsForeign() { // BPJS is an Indonesian scheme
		sec.BpjsKesShown, sec.BpjsKesRequired = pol.BpjsKesShown, pol.BpjsKesRequired
		sec.BpjsTkShown, sec.BpjsTkRequired = pol.BpjsTkShown, pol.BpjsTkRequired
	}
	if a.Positions != nil {
		if r, err := a.Positions.Resolver(ctx); err != nil {
			log.Printf("positions: %v", err)
		} else {
			sec.Position = r.Resolve(e.Position, pol)
		}
	}
	if sec.Position.Canonical == "" {
		sec.Position.Canonical = e.Position
	}
	if pol.CertificateRequired && sec.Position.CertRequired == "Y" {
		sec.CertNeeded, sec.CertName, sec.CertFixedUntil = true, sec.Position.Certificate, sec.Position.CertValidUntilFixed
	}
	return sec
}

// accepts reports whether an upload box belongs to this worker's form. A file
// posted for a section that is not shown is ignored, never stored.
func (sec Sections) accepts(field string, foreign bool) bool {
	switch field {
	case "visa_doc", "rptka_doc", "immigration_doc", "police_doc":
		return foreign
	case "contract_doc":
		return sec.ContractShown
	case "bpjs_kes_doc":
		return sec.BpjsKesShown
	case "bpjs_tk_doc":
		return sec.BpjsTkShown
	case "cert_doc":
		return sec.CertNeeded
	}
	return true
}

// uploaded returns the file header for a box, or nil when nothing was chosen.
func uploaded(r *http.Request, field string) *multipart.FileHeader {
	if hs := r.MultipartForm.File[field]; len(hs) > 0 && hs[0].Size > 0 {
		return hs[0]
	}
	return nil
}

// permitSubmit is part 2: it re-proves the worker from the signed token, applies
// every rule, stores the files and the permit, and shows the request id.
func (a *App) permitSubmit(w http.ResponseWriter, r *http.Request) {
	pol := a.policy()
	maxBytes := pol.UploadMaxKB * 1024
	r.Body = http.MaxBytesReader(w, r.Body, int64(maxFiles*maxBytes+1<<20))
	if err := r.ParseMultipartForm(multipartMemKB << 10); err != nil {
		a.permitStartAgain(w, r, http.StatusRequestEntityTooLarge, fmt.Sprintf("The upload was too large. Each file may be at most %d KB.", pol.UploadMaxKB))
		return
	}
	defer r.MultipartForm.RemoveAll()

	vals := map[string]string{}
	for _, f := range partTwoFields {
		vals[f] = strings.TrimSpace(r.PostFormValue(f))
		if len(vals[f]) > maxFieldLen {
			vals[f] = vals[f][:maxFieldLen]
		}
	}
	limitKey := "sub:" + a.clientIP(r)
	if a.Limit.Count(limitKey, submitWindow) >= submitsPerHour {
		a.permitStartAgain(w, r, http.StatusTooManyRequests, "Too many attempts. Please try again later.")
		return
	}
	a.Limit.Add(limitKey)

	claims, ok := verifyStep(a.Secret, r.PostFormValue("token"), a.Clock.Now())
	if !ok || a.Permits == nil || a.Workers == nil {
		a.permitStartAgain(w, r, http.StatusBadRequest, "Your session has expired. Please start the request again.")
		return
	}
	e, found, err := a.Workers.Pick(r.Context(), claims.Name, claims.DOB)
	if err != nil {
		log.Printf("submit worker lookup: %v", err)
		a.permitStartAgain(w, r, http.StatusServiceUnavailable, "This form is not available right now. Please try again later.")
		return
	}
	if !found {
		a.permitStartAgain(w, r, http.StatusBadRequest, "The worker could not be found any more. Please start the request again.")
		return
	}
	check, status, msg := a.checkPermit(r, e, "")
	if status != 0 {
		a.permitStartAgain(w, r, status, msg)
		return
	}
	check.RequesterName, check.RequesterMail, check.WorkLocation = claims.RequesterName, claims.RequesterEmail, claims.Location
	check.Token, check.Regencies, check.Vals = r.PostFormValue("token"), a.regencies(r.Context()), vals
	sec := a.sections(r.Context(), e, pol)
	check.Sec = sec

	// fail shows the form again with a message. The optional field is the input the message is about, so the
	// step-by-step form can open the step that holds it.
	fail := func(status int, msg string, field ...string) {
		f := ""
		if len(field) > 0 {
			f = field[0]
		}
		a.render(w, "permit", status, View{Title: "Work Entry Permit", User: a.currentUser(r), My: a.myData(),
			Permit: PermitData{Available: true, Err: msg, ErrField: f, Result: &check}})
	}
	if check.Blocked {
		fail(http.StatusConflict, check.BlockedText)
		return
	}
	if msg := a.checkMailDomains(pol, claims.RequesterEmail); msg != "" {
		fail(http.StatusBadRequest, msg)
		return
	}

	form, perr, perrField := a.readForm(r, e, vals, sec)
	if perr != "" {
		fail(http.StatusBadRequest, perr, perrField)
		return
	}
	if err := permit.ValidateForm(form, a.Clock.Today()); err != nil {
		fail(http.StatusBadRequest, err.Error(), permit.FieldOf(err))
		return
	}
	if check.Foreign { // the duplicate gate again, now that the new visa date is known
		if again, st, m := a.checkPermit(r, e, form.VisaExpiry); st != 0 || again.Blocked {
			if m == "" {
				m = again.BlockedText
			}
			fail(http.StatusConflict, m)
			return
		}
	}
	valid, reason, err := permit.ResolveValidity(pol, form.ValidityInput(dates.Day(e.McuValidUntil)))
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	a.storePermit(w, r, e, form, sec, claims, valid, reason, maxBytes, fail)
}

// readForm turns the posted values into the rules' Form. The second result is a
// message when a date cannot be read.
func (a *App) readForm(r *http.Request, e worker.Employee, v map[string]string, sec Sections) (permit.Form, string, string) {
	f := permit.Form{
		WorkerName: e.Name, Foreign: e.IsForeign(), Declaration: r.PostFormValue("declaration") != "",
		Phone: v["phone"], EmergencyName: textclean.Name(v["emergency_name"]), EmergencyPhone: v["emergency_phone"],
		District: v["district"], Village: v["village"], Street: v["street"],
		VisaType: v["visa_type"], VisaCode: v["visa_code"], VisaIssuedBy: v["visa_issued_by"],
		HasIDDoc: uploaded(r, "id_doc") != nil, HasIDPhoto: uploaded(r, "id_photo") != nil,
		HasVisaDoc: uploaded(r, "visa_doc") != nil, HasRptkaDoc: uploaded(r, "rptka_doc") != nil,
		HasImmigrationDoc: uploaded(r, "immigration_doc") != nil, HasPoliceDoc: uploaded(r, "police_doc") != nil,
		ContractShown: sec.ContractShown, ContractRequired: sec.ContractRequired, HasContractDoc: uploaded(r, "contract_doc") != nil,
		BpjsKesShown: sec.BpjsKesShown, BpjsKesRequired: sec.BpjsKesRequired, BpjsKes: v["bpjs_kes"],
		BpjsTkShown: sec.BpjsTkShown, BpjsTkRequired: sec.BpjsTkRequired, BpjsTk: v["bpjs_tk"],
		CertNeeded: sec.CertNeeded, CertFixedUntil: sec.CertFixedUntil, HasCertDoc: uploaded(r, "cert_doc") != nil,
	}
	if rg, ok := findRegency(a.regencies(r.Context()), v["regency"]); ok {
		f.Province, f.Regency = rg.Province, rg.Name
	}
	var err error
	if sec.ContractShown {
		if f.ContractUntil, err = parseFormDay(v["contract_until"]); err != nil {
			return f, err.Error(), "contract_until"
		}
	}
	if sec.CertNeeded && sec.CertFixedUntil == "" {
		if f.CertUntil, err = parseFormDay(v["cert_until"]); err != nil {
			return f, err.Error(), "cert_until"
		}
	}
	if !f.Foreign {
		return f, "", "" // an Indonesian's foreign fields are never read
	}
	targets := []struct {
		in  string
		out *dates.Day
	}{{"visa_issued", &f.VisaIssued}, {"visa_expiry", &f.VisaExpiry}, {"rptka_issued", &f.RptkaIssued}, {"rptka_expiry", &f.RptkaExpiry},
		{"immigration_until", &f.ImmigrationUntil}, {"police_until", &f.PoliceUntil}, {"passport_until", &f.PassportUntil}}
	for _, t := range targets {
		d, err := parseFormDay(v[t.in])
		if err != nil {
			return f, err.Error(), t.in
		}
		*t.out = d
	}
	f.RptkaDays = atoi(v["rptka_days"])
	return f, "", ""
}

// storePermit saves the files and then the row. A failure after the files are
// written removes them again, so a refused request leaves nothing on disk.
func (a *App) storePermit(w http.ResponseWriter, r *http.Request, e worker.Employee, f permit.Form, sec Sections, c stepClaims,
	valid dates.Day, reason permit.Reason, maxBytes int, fail func(int, string, ...string)) {
	id, err := permit.UniqueID("WEP-", idAttempts, func(id string) (bool, error) { return a.Permits.Exists(r.Context(), id) })
	if err != nil {
		log.Printf("request id: %v", err)
		fail(http.StatusServiceUnavailable, "This form is not available right now. Please try again later.")
		return
	}
	paths := map[string]string{}
	for _, k := range fileKinds {
		h := uploaded(r, k.field)
		if h == nil || !sec.accepts(k.field, f.Foreign) {
			continue
		}
		data, err := readUpload(h, maxBytes)
		if err == nil {
			paths[k.field], err = a.Uploads.Save(id, k.field, data, maxBytes)
		}
		if err != nil {
			_ = a.Uploads.Remove(id)
			fail(http.StatusBadRequest, uploadMessage(k.label, err, maxBytes), k.field)
			return
		}
	}
	now := string(a.Clock.Now())
	p := permitlog.NewPermit{
		RequestID: id, Status: permit.StatusPendingStage1, PatientID: e.PatientID, PersonalID: e.PersonalID,
		WorkerName: e.Name, Company: e.Company, Entity: e.Entity, WorkPosition: e.Position, Parents: parentsText(e.Parents),
		Nationality: e.Nationality, RequesterName: c.RequesterName, RequesterEmail: c.RequesterEmail, WorkLocation: c.Location,
		McuCheckupID: e.CheckUpID, McuResult: e.McuResult, McuValidUntil: e.McuValidUntil, LepValidUntil: string(valid),
		Phone: phone.Normalize(f.Phone), EmergencyName: f.EmergencyName, EmergencyPhone: phone.Normalize(f.EmergencyPhone),
		Province: f.Province, Regency: f.Regency, District: f.District, Village: f.Village, Street: f.Street,
		IDDoc: paths["id_doc"], IDPhoto: paths["id_photo"], VisaDoc: paths["visa_doc"], RptkaDoc: paths["rptka_doc"],
		ImmigrationDoc: paths["immigration_doc"], PoliceDoc: paths["police_doc"],
		ContractDoc: paths["contract_doc"], BpjsKesDoc: paths["bpjs_kes_doc"], BpjsTkDoc: paths["bpjs_tk_doc"], CertDoc: paths["cert_doc"],
		PositionCanonical: sec.Position.Canonical, PositionRisk: sec.Position.RiskTier, CertRequired: sec.Position.CertRequired,
		DOB: e.DOB, DeclaredAt: now, CreatedAt: now,
	}
	if sec.ContractShown {
		p.ContractUntil = string(f.ContractUntil)
	}
	if sec.BpjsKesShown {
		p.BpjsKes = digitsOnly(f.BpjsKes)
	}
	if sec.BpjsTkShown {
		p.BpjsTk = digitsOnly(f.BpjsTk)
	}
	if sec.CertNeeded {
		p.CertName, p.CertUntil = sec.CertName, string(f.CertUntil)
		if sec.CertFixedUntil != "" {
			p.CertUntil = string(sec.CertFixedUntil)
		}
	}
	if f.Foreign {
		p.VisaType, p.VisaCode, p.VisaIssued, p.VisaIssuedBy, p.VisaExpiry = f.VisaType, f.VisaCode, string(f.VisaIssued), f.VisaIssuedBy, string(f.VisaExpiry)
		p.RptkaIssued, p.RptkaExpiry, p.RptkaDays = string(f.RptkaIssued), string(f.RptkaExpiry), f.RptkaDays
		p.ImmigrationUntil, p.PoliceUntil, p.PassportUntil = string(f.ImmigrationUntil), string(f.PoliceUntil), string(f.PassportUntil)
	}
	if err := a.Permits.Create(r.Context(), p); err != nil {
		log.Printf("store permit: %v", err)
		_ = a.Uploads.Remove(id)
		fail(http.StatusServiceUnavailable, "The request could not be saved. Please try again.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	a.render(w, "permit_done", http.StatusOK, View{Title: "Request sent", User: a.currentUser(r), My: a.myData(),
		Permit: PermitData{Done: &PermitDone{RequestID: id, Worker: e.Name, Company: e.Company, ValidUntil: valid, Reason: string(reason), Email: c.RequesterEmail}}})
}

// readUpload reads one uploaded file, never more than the limit plus one byte,
// so an oversize file is reported as such without loading all of it.
func readUpload(h *multipart.FileHeader, maxBytes int) ([]byte, error) {
	f, err := h.Open()
	if err != nil {
		return nil, fmt.Errorf("open upload: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(maxBytes)+1))
	if err != nil {
		return nil, fmt.Errorf("read upload: %w", err)
	}
	return data, nil
}

func uploadMessage(label string, err error, maxBytes int) string {
	switch {
	case errors.Is(err, filestore.ErrTooBig):
		return fmt.Sprintf("%s: the file is too large (at most %d KB).", label, maxBytes/1024)
	case errors.Is(err, filestore.ErrType), errors.Is(err, filestore.ErrEmpty), errors.Is(err, filestore.ErrImage):
		return fmt.Sprintf("%s: %s.", label, err.Error())
	}
	log.Printf("upload %s: %v", label, err)
	return "The files could not be stored. Please try again."
}

func parentsText(ps []worker.Parent) string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, fmt.Sprintf("%s (%d)", p.Name, p.Depth))
	}
	return strings.Join(out, "; ")
}

// permitStartAgain shows the part-1 form with a message, for a request that
// cannot continue (token gone, too many tries, medpost down).
func (a *App) permitStartAgain(w http.ResponseWriter, r *http.Request, status int, msg string) {
	d := a.permitData()
	d.Err = msg
	a.render(w, "permit", status, View{Title: "Work Entry Permit", User: a.currentUser(r), My: a.myData(), Permit: d})
}

// digitsOnly is a BPJS number as stored: the digits, without spaces or dashes.
func digitsOnly(s string) string { return phone.Digits(s) }
