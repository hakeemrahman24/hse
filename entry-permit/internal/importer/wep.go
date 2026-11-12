package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"entry-permit/internal/dates"
	"entry-permit/internal/permit"
	"entry-permit/internal/permitlog"
)

// Column positions (0-based) in the WorkEntryPermitLog sheet, from COL_WEP in the old
// Code.js. WHY positions and not header names: 16 of the 98 columns have no header text.
const (
	colTimestamp      = 0
	colRequestID      = 1
	colRequesterName  = 2
	colRequesterEmail = 3
	colName           = 5
	colDOB            = 7
	colNationality    = 9
	colResult         = 13
	colResultUntil    = 14
	colPosition       = 12
	colCurrentPos     = 16
	colCompany        = 11
	colCurrentCompany = 17
	colEntity         = 18
	colParent         = 19
	colWorkLocation   = 20
	colPersonalID     = 23
	colPassportUntil  = 24
	colVisaIssued     = 26
	colVisaExpiry     = 27
	colItasCode       = 29
	colItasExpiry     = 30
	colItapCode       = 32
	colItapIssuedBy   = 33
	colItapExpiry     = 34
	colRptkaIssued    = 36
	colRptkaDays      = 37
	colRptkaExpiry    = 38
	colImmigration    = 41
	colPolice         = 43
	colCanonical      = 50
	colCertName       = 51
	colCertUntil      = 52
	colCertRequired   = 54
	colBpjsKes        = 56
	colBpjsTk         = 60
	colContractUntil  = 65
	colProvince       = 68
	colRegency        = 69
	colDistrict       = 70
	colVillage        = 71
	colAddress        = 72
	colPhone          = 73
	colEmergencyName  = 77
	colEmergencyPhone = 78
	colStage1Name     = 81
	colStage1At       = 82
	colStage1Note     = 83
	colStage1Status   = 84
	colFinalName      = 86
	colFinalAt        = 87
	colFinalNote      = 88
	colFinalStatus    = 89
	colStatus         = 91
	colLepNumber      = 92
	colLepUntil       = 93
)

// Lengths of the columns the ids are stored in.
const (
	maxRequestID = 12
	maxLepNo     = 12
)

// reviewSlack is how far a review may be from the submit time before the row is flagged as
// a probable day/month mix-up.
const (
	submitAfterReview = 24 * 60 * 60      // seconds: a request cannot be submitted after it was reviewed
	reviewLongAfter   = 30 * 24 * 60 * 60 // seconds: a review a month after the request is suspicious
)

// Problem is something found in one sheet row. A Fatal problem means the row is not
// imported; the rest are reported and the row is imported without that value.
type Problem struct {
	Row   int // sheet row number, 1-based
	ID    string
	Field string
	Msg   string
	Fatal bool
}

// Step is one recorded review decision.
type Step struct{ Stage, Reviewer, Decision, Note, At string }

// Mapped is one sheet row, ready to store.
type Mapped struct {
	Permit permitlog.NewPermit
	Steps  []Step
	Hash   string // of the content, so a re-run can tell whether the sheet row changed
}

func cell(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[i])
}

// text is a cell with the sheet's placeholders ("_", "#N/A") read as nothing.
func text(row []string, i int) string {
	if v := cell(row, i); !Blank(v) {
		return strings.Join(strings.Fields(v), " ")
	}
	return ""
}

// MapWEP turns one WorkEntryPermitLog row (rowNo is its 1-based sheet row, for the report)
// into a permit and its review steps. It never panics on a short or odd row.
func MapWEP(row []string, rowNo int) (Mapped, []Problem) {
	id := cell(row, colRequestID)
	var ps []Problem
	bad := func(field, msg string, fatal bool) {
		ps = append(ps, Problem{Row: rowNo, ID: id, Field: field, Msg: msg, Fatal: fatal})
	}
	day := func(col int, field string) string {
		d, ok := Day(cell(row, col))
		if !ok {
			bad(field, fmt.Sprintf("%s %q cannot be read; left blank", field, cell(row, col)), false)
		}
		return d
	}
	moment := func(col int, field string) (string, bool) {
		m, ok := Moment(cell(row, col))
		if !ok {
			bad(field, fmt.Sprintf("%s %q cannot be read", field, cell(row, col)), false)
		}
		return m, ok
	}

	status := strings.TrimSpace(cell(row, colStatus))
	lep := cell(row, colLepNumber)
	created, createdOK := Moment(cell(row, colTimestamp))

	// What makes a row impossible to import safely.
	switch {
	case id == "" || len(id) > maxRequestID:
		bad("request_id", fmt.Sprintf("request id %q is missing or longer than %d characters", id, maxRequestID), true)
	}
	if !knownStatus(status) {
		bad("status", fmt.Sprintf("status %q is not one the system knows", status), true)
	}
	if !createdOK || created == "" {
		bad("submit_time", fmt.Sprintf("submit time %q cannot be read", cell(row, colTimestamp)), true)
	}
	if text(row, colName) == "" {
		bad("name", "the worker name is empty", true)
	}
	location := normaliseLocation(cell(row, colWorkLocation))
	if location == "" {
		bad("work_location", fmt.Sprintf("work location %q is not Onshore or Offshore", cell(row, colWorkLocation)), true)
	}
	if len(lep) > maxLepNo {
		bad("lep_no", fmt.Sprintf("LEP number %q is longer than %d characters", lep, maxLepNo), true)
	}
	if needsLEP(status) && lep == "" {
		bad("lep_no", "this status needs an LEP number and the row has none", true)
	}
	lepUntil, lepUntilOK := Day(cell(row, colLepUntil))
	if mustHaveEnd(status) && (!lepUntilOK || lepUntil == "") {
		bad("lep_valid_until", fmt.Sprintf("valid-until date %q is missing or cannot be read; the daily expiry check needs it", cell(row, colLepUntil)), true)
	} else if !lepUntilOK {
		bad("lep_valid_until", fmt.Sprintf("valid-until date %q cannot be read; left blank", cell(row, colLepUntil)), false)
	}

	p := permitlog.NewPermit{
		RequestID: id, LepNo: lep, Status: status, PatientID: 0, // the sheet has no medpost id: the permit is linked by its personal id
		PersonalID: text(row, colPersonalID), WorkerName: text(row, colName),
		Company:      firstOf(text(row, colCurrentCompany), text(row, colCompany)),
		Entity:       text(row, colEntity),
		WorkPosition: firstOf(text(row, colCurrentPos), text(row, colPosition)),
		Parents:      parents(text(row, colParent)), Nationality: nationality(text(row, colNationality)),
		RequesterName: text(row, colRequesterName), RequesterEmail: strings.ToLower(cell(row, colRequesterEmail)), WorkLocation: location,
		McuResult: mcuResult(text(row, colResult)), McuValidUntil: day(colResultUntil, "mcu_valid_until"), LepValidUntil: lepUntil,
		Province: text(row, colProvince), Regency: text(row, colRegency), District: text(row, colDistrict), Village: text(row, colVillage), Street: text(row, colAddress),
		EmergencyName: text(row, colEmergencyName),
		RptkaIssued:   day(colRptkaIssued, "rptka_issued"), RptkaExpiry: day(colRptkaExpiry, "rptka_expiry"),
		ImmigrationUntil: day(colImmigration, "immigration_until"), PoliceUntil: day(colPolice, "police_until"), PassportUntil: day(colPassportUntil, "passport_until"),
		ContractUntil: day(colContractUntil, "contract_until"),
		BpjsKes:       text(row, colBpjsKes), BpjsTk: text(row, colBpjsTk),
		CertName: text(row, colCertName), CertUntil: day(colCertUntil, "cert_until"),
		PositionCanonical: text(row, colCanonical), CertRequired: certRequired(cell(row, colCertRequired)),
		DOB: day(colDOB, "dob"), CreatedAt: created, DeclaredAt: created,
	}
	if n := text(row, colRptkaDays); n != "" {
		if days, err := strconv.Atoi(n); err == nil {
			p.RptkaDays = days
		} else {
			bad("rptka_days", fmt.Sprintf("RPTKA duration %q is not a number; left as 0", n), false)
		}
	}
	visa(row, &p, day)
	var lossy bool
	if p.Phone, lossy = Phone(cell(row, colPhone)); lossy {
		bad("phone", "phone number was stored as a number and may have lost its last digits", false)
	}
	if p.EmergencyPhone, lossy = Phone(cell(row, colEmergencyPhone)); lossy {
		bad("emergency_phone", "emergency phone was stored as a number and may have lost its last digits", false)
	}
	fit(bad, []textField{
		{"personal_id", &p.PersonalID, 30}, {"worker_name", &p.WorkerName, 120}, {"company", &p.Company, 160}, {"nationality", &p.Nationality, 60},
		{"requester_name", &p.RequesterName, 120}, {"requester_email", &p.RequesterEmail, 160}, {"mcu_result", &p.McuResult, 20},
		{"entity", &p.Entity, 60}, {"work_position", &p.WorkPosition, 120}, {"parents", &p.Parents, 400},
		{"emergency_name", &p.EmergencyName, 120}, {"province", &p.Province, 80}, {"regency", &p.Regency, 120}, {"district", &p.District, 120},
		{"village", &p.Village, 120}, {"street", &p.Street, 240}, {"visa_code", &p.VisaCode, 60}, {"visa_issued_by", &p.VisaIssuedBy, 120},
		{"bpjs_kes", &p.BpjsKes, 30}, {"bpjs_tk", &p.BpjsTk, 30}, {"cert_name", &p.CertName, 200}, {"position_canonical", &p.PositionCanonical, 120},
	})
	if status == permit.StatusApproved && p.DOB == "" {
		bad("dob", "Approved permit with no readable date of birth: the worker cannot open the working-schedule page", false)
	}

	steps := reviewSteps(row, created, moment)
	if len(steps) > 0 && createdOK && created != "" {
		if h, ok := dates.HoursBetween(dates.Moment(created), dates.Moment(steps[0].At)); ok && h*3600 > reviewLongAfter {
			bad("submit_time", "the first review is over a month after the request was submitted: check that the day and month were not swapped", false)
		} else if early, ok := dates.HoursBetween(dates.Moment(steps[0].At), dates.Moment(created)); ok && early*3600 > submitAfterReview {
			bad("submit_time", "the request was submitted after its first review: check that the day and month were not swapped", false)
		}
	}
	m := Mapped{Permit: p, Steps: steps}
	m.Hash = hash(m)
	return m, ps
}

// reviewSteps reads the two review blocks. A block whose status is not Approved or Rejected
// (Pending, blank) records no decision.
func reviewSteps(row []string, created string, moment func(int, string) (string, bool)) []Step {
	var out []Step
	for _, b := range []struct {
		stage                  string
		name, at, note, status int
	}{
		{"stage1", colStage1Name, colStage1At, colStage1Note, colStage1Status},
		{"final", colFinalName, colFinalAt, colFinalNote, colFinalStatus},
	} {
		var decision string
		switch strings.ToLower(cell(row, b.status)) {
		case "approved":
			decision = "approved"
		case "rejected":
			decision = "rejected"
		default:
			continue
		}
		at, ok := moment(b.at, b.stage+"_time")
		if !ok || at == "" {
			at = created // the decision is kept, with the submit time as its moment
		}
		reviewer := text(row, b.name)
		if reviewer == "" {
			reviewer = "(unknown)"
		}
		out = append(out, Step{Stage: b.stage, Reviewer: reviewer, Decision: decision, Note: text(row, b.note), At: at})
	}
	return out
}

// visa fills the visa fields from whichever block the worker has. ITAP wins over ITAS, ITAS
// over a plain visa, because a worker who holds a permanent permit does not rely on the older one.
func visa(row []string, p *permitlog.NewPermit, day func(int, string) string) {
	switch {
	case text(row, colItapCode) != "" || text(row, colItapExpiry) != "":
		p.VisaType, p.VisaCode, p.VisaIssuedBy, p.VisaExpiry = permit.VisaKindItap, text(row, colItapCode), text(row, colItapIssuedBy), day(colItapExpiry, "visa_expiry")
	case text(row, colItasCode) != "" || text(row, colItasExpiry) != "":
		p.VisaType, p.VisaCode, p.VisaExpiry = permit.VisaKindItas, text(row, colItasCode), day(colItasExpiry, "visa_expiry")
	case text(row, colVisaIssued) != "" || text(row, colVisaExpiry) != "":
		p.VisaType, p.VisaIssued, p.VisaExpiry = permit.VisaKindVisa, day(colVisaIssued, "visa_issued"), day(colVisaExpiry, "visa_expiry")
	}
}

// knownStatus accepts the statuses the app uses, kept exactly as the sheet writes them.
func knownStatus(s string) bool {
	switch s {
	case permit.StatusPendingStage1, permit.StatusPendingFinal, permit.StatusApproved, permit.StatusRejected,
		permit.StatusResigned, permit.StatusExpiredGrace, permit.StatusExpired:
		return true
	}
	return permit.IsRenewed(s)
}

// needsLEP: every status after Final approval has an LEP number.
func needsLEP(s string) bool {
	return s != permit.StatusPendingStage1 && s != permit.StatusPendingFinal && s != permit.StatusRejected
}

// mustHaveEnd: the daily expiry job reads the valid-until day of these, so it must be readable.
func mustHaveEnd(s string) bool { return s == permit.StatusApproved || s == permit.StatusExpiredGrace }

func normaliseLocation(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "onshore":
		return "Onshore"
	case "offshore":
		return "Offshore"
	}
	return ""
}

func firstOf(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// nationality corrects the one known misspelling, so old permits match the corrected list.
func nationality(s string) string {
	if strings.EqualFold(s, "Phillipines") {
		return "Philippines"
	}
	return s
}

// mcuResult writes the two spellings in the sheet ("FitWithNote", "Fit with note") one way.
func mcuResult(s string) string {
	if strings.EqualFold(strings.ReplaceAll(s, " ", ""), "fitwithnote") {
		return "Fit with note"
	}
	return s
}

// certRequired keeps Y and N and drops anything else (the sheet holds an Excel error in some rows).
func certRequired(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "Y":
		return "Y"
	case "N":
		return "N"
	}
	return ""
}

// parents turns the sheet's plain parent text ("A / B") into the stored form ("A (1); B (1)").
// WHY depth 1: the sheet names the direct parent and keeps no depth for it.
func parents(s string) string {
	var out []string
	for _, name := range strings.Split(s, "/") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name+" (1)")
		}
	}
	return strings.Join(out, "; ")
}

func hash(m Mapped) string {
	b, _ := json.Marshal(struct {
		P permitlog.NewPermit
		S []Step
	}{m.Permit, m.Steps})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
