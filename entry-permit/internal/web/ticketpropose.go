package web

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"entry-permit/internal/permit"
	"entry-permit/internal/permitlog"
	"entry-permit/internal/ticket"
	"entry-permit/internal/ticketlog"
	"entry-permit/internal/worker"
)

const maxMedicationText = 255

// ticketPropose proposes the next working ticket for a permit whose birth date
// was accepted. The signed token carries which permit; nothing else is trusted.
func (a *App) ticketPropose(w http.ResponseWriter, r *http.Request) {
	pol := a.policy()
	maxBytes := pol.UploadMaxKB * 1024
	vals := map[string]string{}
	token := ""
	lep := ""
	// show renders the opened page again, with a message, and keeps what was typed.
	show := func(status int, msg, notice string, keep map[string]string) {
		d, st, m := a.ticketOpen(r.Context(), lep, token)
		if st != 0 {
			a.ticketRender(w, r, st, TicketData{LEP: lep, Err: m})
			return
		}
		d.Err, d.Notice, d.Vals = msg, notice, keep
		w.Header().Set("Cache-Control", "no-store")
		a.ticketRender(w, r, status, d)
	}
	gone := func(status int, msg string) { a.ticketRender(w, r, status, TicketData{Err: msg}) }

	r.Body = http.MaxBytesReader(w, r.Body, int64(maxBytes+1<<20))
	if err := r.ParseMultipartForm(multipartMemKB << 10); err != nil {
		gone(http.StatusRequestEntityTooLarge, fmt.Sprintf("The upload was too large. The photo may be at most %d KB.", pol.UploadMaxKB))
		return
	}
	defer r.MultipartForm.RemoveAll()
	token = r.PostFormValue("token")
	for _, f := range []string{"arrival", "departure", "location", "medication_answer"} {
		vals[f] = clip(strings.TrimSpace(r.PostFormValue(f)), 12)
	}
	vals["medication_details"] = clip(strings.TrimSpace(r.PostFormValue("medication_details")), maxMedicationText)
	vals["fatigue_consent"], vals["mcu_note_agreement"] = r.PostFormValue("fatigue_consent"), r.PostFormValue("mcu_note_agreement")

	ip := a.clientIP(r)
	if a.Limit.Count("tkp:"+ip, submitWindow) >= submitsPerHour {
		gone(http.StatusTooManyRequests, "Too many attempts. Please try again later.")
		return
	}
	a.Limit.Add("tkp:" + ip)
	if !a.ticketAvailable() {
		gone(http.StatusServiceUnavailable, "This page is not available right now. Please try again later.")
		return
	}
	claims, ok := verifyAccess(a.Secret, token, a.Clock.Now())
	if !ok {
		gone(http.StatusBadRequest, sessionGone)
		return
	}
	lep = claims.LEP
	if strings.HasPrefix(lep, "WVR-") {
		show(http.StatusBadRequest, ticket.ErrWaiverTicket.Error()+": a waiver's ticket is made with the waiver.", "", nil)
		return
	}
	in, status, msg := a.readProposal(r, lep, vals)
	if status != 0 {
		show(status, msg, "", vals)
		return
	}
	prop, err := ticket.Propose(pol, in.input)
	if err != nil {
		show(http.StatusBadRequest, err.Error(), "", vals)
		return
	}
	id, status, msg := a.storeTicket(r, in, prop, maxBytes)
	if status != 0 {
		show(status, msg, "", vals)
		return
	}
	show(http.StatusOK, "", fmt.Sprintf("Ticket %s was saved and approved. A confirmation email is not sent yet (the email service is built in a later step).", id), nil)
}

// proposal is what ticketPropose hands on after reading the request.
type proposal struct {
	input ticket.ProposalInput
	info  permitlog.AccessInfo
}

// readProposal loads the permit and its tickets and turns the form into the
// rules' input. A non-zero status is the error page to show.
func (a *App) readProposal(r *http.Request, lep string, v map[string]string) (proposal, int, string) {
	var p proposal
	info, found, err := a.Permits.Access(r.Context(), lep)
	if err != nil {
		log.Printf("ticket permit: %v", err)
		return p, http.StatusServiceUnavailable, "This page is not available right now. Please try again later."
	}
	tickets, err := a.Tickets.ForLEP(r.Context(), lep)
	if err != nil {
		log.Printf("ticket list: %v", err)
		return p, http.StatusServiceUnavailable, "This page is not available right now. Please try again later."
	}
	arrival, err1 := parseFormDay(v["arrival"])
	departure, err2 := parseFormDay(v["departure"])
	if err := firstErr(err1, err2); err != nil {
		return p, http.StatusBadRequest, err.Error()
	}
	p.info = info
	p.input = ticket.ProposalInput{
		LEP: lep, Today: a.Clock.Today(), Arrival: string(arrival), Departure: string(departure),
		PermitFound: found, PermitApproved: info.Status == permit.StatusApproved,
		PermitLocation: info.WorkLocation, FormLocation: v["location"],
		Wni:       !worker.Employee{Nationality: info.Nationality}.IsForeign(),
		McuResult: info.McuResult, LepValidUntil: info.LepValidUntil, Tickets: tickets,
		FatigueConsent: v["fatigue_consent"] != "", McuNoteAgreement: v["mcu_note_agreement"] != "",
		MedicationAnswer: v["medication_answer"], MedicationDetails: v["medication_details"],
		MedicinePhoto: uploaded(r, "medicine_photo") != nil,
	}
	return p, 0, ""
}

// storeTicket saves the medicine photo (only when the declaration needs it) and
// then the row. A failure after the photo is written removes it again.
func (a *App) storeTicket(r *http.Request, p proposal, prop ticket.Proposal, maxBytes int) (string, int, string) {
	const unavailable = "This page is not available right now. Please try again later."
	id, err := permit.UniqueID("TKT-", idAttempts, func(id string) (bool, error) { return a.Tickets.Exists(r.Context(), id) })
	if err != nil {
		log.Printf("ticket id: %v", err)
		return "", http.StatusServiceUnavailable, unavailable
	}
	n := ticketlog.NewTicket{
		ID: id, LEP: p.input.LEP, Work: true, Status: string(prop.Status),
		Arrival: string(prop.Arrival), Departure: string(prop.Departure),
		Location: prop.Analysis.Location, Pattern: prop.Analysis.Pattern, RotationNo: prop.RotationNo,
		FatigueDue: string(prop.FatigueDue), GapDays: prop.Analysis.GapDays,
		ShortBreak: prop.Analysis.ShortBreak, RotationExtension: prop.Analysis.RotationExtension,
		FatigueAgree: p.input.FatigueConsent, DoctorReview: string(ticket.StatusApproved), DoctorNote: prop.DoctorNote,
		McuResult: p.info.McuResult, RequesterEmail: p.info.RequesterEmail, SubmittedAt: string(a.Clock.Now()),
	}
	if prop.FitWithNote {
		n.MedAnswer = strings.ToUpper(strings.TrimSpace(p.input.MedicationAnswer))
		if n.MedAnswer == "Y" {
			n.MedDetails = p.input.MedicationDetails
			path, status, msg := a.saveMedicinePhoto(r, id, maxBytes)
			if status != 0 {
				return "", status, msg
			}
			n.MedPhoto = path
		}
	}
	if err := a.Tickets.Create(r.Context(), n); err != nil {
		log.Printf("store ticket: %v", err)
		_ = a.Uploads.Remove(id)
		return "", http.StatusServiceUnavailable, unavailable
	}
	return id, 0, ""
}

// saveMedicinePhoto stores the photo posted with a "Fit with note" declaration.
func (a *App) saveMedicinePhoto(r *http.Request, id string, maxBytes int) (string, int, string) {
	h := uploaded(r, "medicine_photo")
	if h == nil { // Propose already refused a missing photo; this is only a guard
		return "", http.StatusBadRequest, "Upload a photo of the medicine."
	}
	data, err := readUpload(h, maxBytes)
	if err == nil {
		var path string
		if path, err = a.Uploads.Save(id, "med_photo", data, maxBytes); err == nil {
			return path, 0, ""
		}
	}
	_ = a.Uploads.Remove(id)
	return "", http.StatusBadRequest, uploadMessage("Medicine photo", err, maxBytes)
}
