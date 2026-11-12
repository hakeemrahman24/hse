package importer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"entry-permit/internal/dates"
	"entry-permit/internal/waiver"
	"entry-permit/internal/waiverlog"
)

// Column positions (0-based) in the WaiverLog sheet, from COL_WAIVER in the old Code.js.
const (
	wTimestamp   = 0
	wRequestNo   = 1
	wWaiverID    = 2
	wGroup       = 4
	wEmail       = 5
	wNationality = 7
	wPersonalID  = 8
	wName        = 9
	wGender      = 10
	wDOB         = 11
	wPosition    = 14
	wDepartment  = 15
	wInstitution = 16
	wLocation    = 17
	wPurpose     = 18
	wDays        = 19
	wFrom        = 20
	wTo          = 21
	wRisk        = 24
	wPrecaution  = 25
	wPrevent     = 26
	wMedicine1   = 27
	wDosage1     = 28
	wMedicine2   = 29
	wDosage2     = 30
	wMedicine3   = 31
	wDosage3     = 32
	wMedNotes    = 33
	wRiskFirst   = 34 // nine Risk Exposure answers, in the order of waiver.RiskQuestions
	wHealthFirst = 45 // fifteen Health Declaration answers, in the order of waiver.HealthQuestions
	wDoctorState = 69
	wDoctorAt    = 70
	wDoctorBy    = 71
	wDoctorNote  = 72
	wStage1State = 73
	wStage1At    = 74
	wStage1By    = 75
	wStage1Note  = 76
	wFinalBy     = 77
	wFinalAt     = 78
	wFinalOnDuty = 79
	wFinalNote   = 80
	wStatus      = 82
	wPassport    = 86
	wVisa        = 87
	wPhone       = 88
)

const maxWaiverID = 12

// MappedWaiver is one WaiverLog row, ready to store.
type MappedWaiver struct {
	Waiver waiverlog.NewWaiver
	Steps  []Step
	Hash   string
}

// MapWaiver turns one WaiverLog row into a waiver and its review steps. Like MapWEP it never
// panics on a short row, and separates what refuses the row (Fatal) from what is only reported.
func MapWaiver(row []string, rowNo int) (MappedWaiver, []Problem) {
	id := firstOf(cell(row, wWaiverID), cell(row, wRequestNo))
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

	status := cell(row, wStatus)
	if id == "" || len(id) > maxWaiverID {
		bad("request_id", fmt.Sprintf("request id %q is missing or longer than %d characters", id, maxWaiverID), true)
	}
	if !knownWaiverStatus(status) {
		bad("status", fmt.Sprintf("status %q is not one the system knows", status), true)
	}
	created, ok := Moment(cell(row, wTimestamp))
	if !ok || created == "" {
		bad("submit_time", fmt.Sprintf("submit time %q cannot be read", cell(row, wTimestamp)), true)
	}
	if text(row, wName) == "" {
		bad("name", "the visitor name is empty", true)
	}
	kind := applicantType(cell(row, wGroup))
	if kind == "" {
		bad("applicant_type", fmt.Sprintf("visitor group %q is not Government Official or Company Survey", cell(row, wGroup)), true)
	}
	from, fromOK := Day(cell(row, wFrom))
	to, toOK := Day(cell(row, wTo))
	if !fromOK || !toOK || from == "" || to == "" {
		bad("visit_dates", fmt.Sprintf("visit dates %q to %q cannot be read", cell(row, wFrom), cell(row, wTo)), true)
	}
	days := 0
	if n, err := strconv.Atoi(text(row, wDays)); err == nil {
		days = n
	} else if from != "" && to != "" {
		days = dates.InclusiveDays(dates.Day(from), dates.Day(to)) // blank in the sheet: work it out, as the form does
	}

	w := waiverlog.NewWaiver{
		RequestID: id, Status: status, ApplicantType: kind,
		Company: text(row, wInstitution), Name: text(row, wName), Gender: text(row, wGender),
		DOB: day(wDOB, "dob"), Nationality: text(row, wNationality), NIK: text(row, wPersonalID),
		Position: text(row, wPosition), Department: text(row, wDepartment), VisitLocation: text(row, wLocation), Purpose: text(row, wPurpose),
		VisitFrom: from, VisitTo: to, Days: days,
		RequesterEmail: strings.ToLower(cell(row, wEmail)),
		Risk:           text(row, wRisk), Precaution: text(row, wPrecaution), Prevent: text(row, wPrevent),
		Medicine1: text(row, wMedicine1), Dosage1: text(row, wDosage1),
		Medicine2: text(row, wMedicine2), Dosage2: text(row, wDosage2), Medicine3: text(row, wMedicine3), Dosage3: text(row, wDosage3),
		MedicineNotes: text(row, wMedNotes),
		PassportUntil: day(wPassport, "passport_until"), VisaUntil: day(wVisa, "visa_until"),
		DeclaredAt: created, CreatedAt: created,
	}
	w.TakesMedicine = w.Medicine1 != ""
	w.RiskAnswers = answersFrom(row, wRiskFirst, waiver.RiskQuestions, "risk", bad)
	w.HealthAnswers = answersFrom(row, wHealthFirst, waiver.HealthQuestions, "health", bad)
	var lossy bool
	if w.Phone, lossy = Phone(cell(row, wPhone)); lossy {
		bad("phone", "phone number was stored as a number and may have lost its last digits", false)
	}
	fit(bad, []textField{
		{"request_id", &w.RequestID, 12}, {"company", &w.Company, 160}, {"visitor_name", &w.Name, 120}, {"nationality", &w.Nationality, 60},
		{"nik", &w.NIK, 20}, {"position", &w.Position, 120}, {"department", &w.Department, 120}, {"visit_location", &w.VisitLocation, 120},
		{"purpose", &w.Purpose, 255}, {"requester_email", &w.RequesterEmail, 160}, {"risk", &w.Risk, 500}, {"precaution", &w.Precaution, 500},
		{"prevent", &w.Prevent, 500}, {"medicine1", &w.Medicine1, 120}, {"dosage1", &w.Dosage1, 120},
		{"medicine2", &w.Medicine2, 120}, {"dosage2", &w.Dosage2, 120}, {"medicine3", &w.Medicine3, 120}, {"dosage3", &w.Dosage3, 120},
		{"medicine_notes", &w.MedicineNotes, 500},
	})

	m := MappedWaiver{Waiver: w, Steps: waiverSteps(row, status, created, bad)}
	m.Hash = hashOf(struct {
		W waiverlog.NewWaiver
		S []Step
	}{w, m.Steps})
	return m, ps
}

// waiverSteps reads the Doctor, Stage 1 and Final approvals. The sheet keeps only the day of
// each (the review time was not stored), so a step is placed at the start of that day.
func waiverSteps(row []string, status, created string, bad func(string, string, bool)) []Step {
	var out []Step
	add := func(stage, reviewer string, at, note int) {
		when, ok := stampOfDay(cell(row, at))
		if !ok || when == "" {
			when = created
			if !ok {
				bad(stage+"_time", fmt.Sprintf("%s review day %q cannot be read; the submit time is used", stage, cell(row, at)), false)
			}
		}
		out = append(out, Step{Stage: stage, Reviewer: firstOf(reviewer, "(unknown)"), Decision: "approved", Note: text(row, note), At: when})
	}
	if strings.EqualFold(cell(row, wDoctorState), "approved") {
		add("doctor", text(row, wDoctorBy), wDoctorAt, wDoctorNote)
	}
	if strings.EqualFold(cell(row, wStage1State), "approved") {
		add("stage1", text(row, wStage1By), wStage1At, wStage1Note)
	}
	// The Final approver's name sits in one of two columns; a waiver that has passed Final has one.
	finished := status == waiver.StatusApproved || status == waiver.StatusCompleted || status == waiver.StatusExpired
	if by := firstOf(text(row, wFinalBy), text(row, wFinalOnDuty)); by != "" && finished {
		add("final", by, wFinalAt, wFinalNote)
	}
	return out
}

func knownWaiverStatus(s string) bool {
	switch s {
	case waiver.StatusPendingDoctor, waiver.StatusPendingStage1, waiver.StatusPendingSiteManager, waiver.StatusApproved,
		waiver.StatusRejected, waiver.StatusCompleted, waiver.StatusCancelled, waiver.StatusExpired:
		return true
	}
	return false
}

// applicantType maps the sheet's visitor group to the form's two applicant types.
func applicantType(group string) string {
	switch strings.ToLower(strings.TrimSpace(group)) {
	case "government official":
		return waiver.TypeGovernment
	case "company survey":
		return waiver.TypeVisitorCompany
	}
	return ""
}

func hashOf(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// answersFrom reads one Yes / No / N/A card (one cell per question, starting at first) into the
// stored one-letter-per-question string.
//
// WHY blank or odd cards become "" and not a guess: an old row may have no answers at all, and a
// half-readable card must never turn into a row of No that a doctor would trust. "" is shown to
// reviewers as "not recorded". An all-blank card is normal and silent; a card with some cells
// unreadable is reported (not fatal), so the row still imports.
func answersFrom(row []string, first int, qs []waiver.Question, name string, bad func(string, string, bool)) string {
	out := make([]byte, len(qs))
	blanks := 0
	for i := range qs {
		raw := cell(row, first+i)
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "yes", "y", "ya":
			out[i] = waiver.AnswerYes
		case "no", "n", "tidak":
			out[i] = waiver.AnswerNo
		case "n/a", "na", "n.a", "t/a":
			out[i] = waiver.AnswerNA
		case "":
			blanks++
		default:
			bad(name+"_answers", fmt.Sprintf("%s answer %q for %q cannot be read; the whole card is left unrecorded", name, raw, qs[i].Label), false)
			return ""
		}
	}
	switch blanks {
	case 0:
		return string(out)
	case len(qs):
		return ""
	}
	bad(name+"_answers", fmt.Sprintf("%s card has %d of %d answers blank; the whole card is left unrecorded", name, blanks, len(qs)), false)
	return ""
}
