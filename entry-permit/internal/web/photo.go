package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"entry-permit/internal/audit"
	"entry-permit/internal/card"
	"entry-permit/internal/dates"
	"entry-permit/internal/filestore"
	"entry-permit/internal/permit"
	"entry-permit/internal/ticket"
	"entry-permit/internal/worker"
)

const (
	// photoTokenHours: how long the proof lasts, both in the page's forms and in the worker cookie
	// (workerCookie), so the worker is not asked for the name and birth date again while using their
	// own pages. WHY four: a shift's worth of use on a phone, short enough for a shared device.
	photoTokenHours = 4
	// photoTriesPerHour limits uploads per address: a real person changes a photo once or twice,
	// and every try decodes an image, which is the costly thing on a small machine.
	photoTriesPerHour = 10
	photoKind         = "id_photo"
	// photoMaxUploadBytes is what a photo may be before it is shrunk. WHY not the permit forms'
	// limit (700 KB by default): a phone picture is 2-5 MB, and the point of this page is that the
	// server makes it small, so the worker should not have to. The pixel count is limited too.
	photoMaxUploadBytes = 8 << 20
)

// photoClaims is what the lookup proved. It is signed, not stored, and holds no date of birth: the
// result page is shown once and must not carry it. The patient id and personal id are what find
// the worker's permits, the same keys the renewal check uses.
type photoClaims struct {
	PatientID  int64
	PersonalID string
	Name       string
	Expires    dates.Moment
	// What the worker's own page shows, signed in so the page can open again without asking
	// medpost for a name and birth date it deliberately does not keep.
	Company, Position, McuResult, McuNote, McuValidUntil string
}

func photoMAC(secret, payload string) []byte {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte("photo|")) // keeps these tokens apart from the permit-step and login tokens
	h.Write([]byte(payload))
	return h.Sum(nil)
}

func signPhoto(secret string, c photoClaims) string {
	b, _ := json.Marshal(c)
	return tokenEnc.EncodeToString(b) + "." + tokenEnc.EncodeToString(photoMAC(secret, string(b)))
}

func verifyPhoto(secret, tok string, now dates.Moment) (photoClaims, bool) {
	var c photoClaims
	parts := strings.Split(tok, ".")
	if len(parts) != 2 {
		return c, false
	}
	payload, err1 := tokenEnc.DecodeString(parts[0])
	sig, err2 := tokenEnc.DecodeString(parts[1])
	if err1 != nil || err2 != nil || !hmac.Equal(sig, photoMAC(secret, string(payload))) {
		return c, false
	}
	if json.Unmarshal(payload, &c) != nil || c.Name == "" || now >= c.Expires {
		return c, false
	}
	return c, true
}

// photoToken is the token the result page's photo form carries, or "" when photos cannot be saved.
func (a *App) photoToken(e worker.Employee) string {
	if a.Permits == nil {
		return ""
	}
	return signPhoto(a.Secret, photoClaims{PatientID: e.PatientID, PersonalID: e.PersonalID, Name: e.Name,
		Expires: a.Clock.Now().AddHours(photoTokenHours),
		Company: e.Company, Position: e.Position, McuResult: e.McuResult, McuNote: e.McuNote, McuValidUntil: e.McuValidUntil})
}

// PhotoData is what the photo page shows.
type PhotoData struct {
	Token, Done, Err string
}

// myPhoto lets a proven worker replace their ID photo. The picture is shrunk once, then stored
// over the old one on each of the worker's permits that is still in use, so reviewers and the gate
// always see the newest photo and the old file does not stay on disk.
func (a *App) myPhoto(w http.ResponseWriter, r *http.Request) {
	maxBytes := photoMaxUploadBytes
	show := func(status int, p PhotoData) {
		w.Header().Set("Cache-Control", "no-store")
		a.render(w, "my_photo", status, View{Title: "My photo", User: a.currentUser(r), Photo: p})
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64(maxBytes+1<<20))
	if err := r.ParseMultipartForm(multipartMemKB << 10); err != nil {
		show(http.StatusRequestEntityTooLarge, PhotoData{Err: fmt.Sprintf("The upload was too large. The photo may be at most %d MB.", photoMaxUploadBytes>>20)})
		return
	}
	defer r.MultipartForm.RemoveAll()

	key := "ph:" + a.clientIP(r)
	if a.Limit.Count(key, submitWindow) >= photoTriesPerHour {
		show(http.StatusTooManyRequests, PhotoData{Err: "Too many attempts. Please try again later."})
		return
	}
	a.Limit.Add(key)

	tok := r.PostFormValue("token")
	claims, ok := verifyPhoto(a.Secret, tok, a.Clock.Now())
	if !ok || a.Permits == nil {
		show(http.StatusBadRequest, PhotoData{Err: "Your session has expired. Please look yourself up again on the home page."})
		return
	}
	again := func(status int, msg string) { show(status, PhotoData{Token: tok, Err: msg}) }

	h := uploaded(r, "photo")
	if h == nil {
		again(http.StatusBadRequest, "Choose a photo first.")
		return
	}
	data, err := readUpload(h, maxBytes)
	if err != nil {
		again(http.StatusBadRequest, uploadMessage("Photo", err, maxBytes))
		return
	}
	if len(data) > maxBytes { // readUpload reads one byte past the limit so an oversize file is noticed
		again(http.StatusRequestEntityTooLarge, fmt.Sprintf("Photo: the file is too large (at most %d MB).", photoMaxUploadBytes>>20))
		return
	}
	small, err := filestore.Shrink(data)
	if err != nil {
		again(http.StatusBadRequest, uploadMessage("Photo", err, maxBytes))
		return
	}
	ids, err := a.Permits.PhotoTargets(r.Context(), claims.PatientID, claims.PersonalID)
	if err != nil {
		log.Printf("photo targets: %v", err)
		again(http.StatusServiceUnavailable, "The photo could not be saved right now. Please try again later.")
		return
	}
	if len(ids) == 0 {
		again(http.StatusNotFound, "You have no current permit to put this photo on. Request a permit first.")
		return
	}
	for _, id := range ids {
		if err := a.replacePhoto(r, id, small); err != nil {
			log.Printf("replace photo %s: %v", id, err)
			again(http.StatusServiceUnavailable, "The photo could not be saved right now. Please try again later.")
			return
		}
	}
	noun := "permits"
	if len(ids) == 1 {
		noun = "permit"
	}
	show(http.StatusOK, PhotoData{Done: fmt.Sprintf("Photo updated on %d %s.", len(ids), noun)})
}

// replacePhoto puts the photo in storage over the old one, records the new path, and writes the
// audit row. The file goes first: if the database step fails, the permit still points at a file
// that exists (the new one), never at one that was removed.
func (a *App) replacePhoto(r *http.Request, id string, small []byte) error {
	rel, err := a.Uploads.PlacePhoto(id, photoKind, small)
	if err != nil {
		return err
	}
	old, err := a.Permits.SetIDPhoto(r.Context(), id, rel)
	if err != nil {
		return err
	}
	if err := audit.Write(r.Context(), a.SQL, a.Clock.Now(), "worker", "photo.change", "permit:"+id, old, rel); err != nil {
		// The photo is already saved, so a failed log row is reported but does not undo it.
		log.Printf("audit photo.change %s: %v", id, err)
	}
	return nil
}

// MyPermit is one line of the worker's permit list.
type MyPermit struct {
	LEP, Label, Class, ValidUntil string
	CardURL                       string // blank when the permit has no card
	CanTicket                     bool   // a Valid permit: the worker may open the ticket page
	HasPhoto                      bool   // a photo file is stored for this permit
	TicketText                    string // the newest live working ticket ("2026-11-20 to 2026-11-27, Approved"); blank when there is none
}

// maxMyPermits keeps the list short: the newest few are what a worker needs.
const maxMyPermits = 5

// myPermits builds the list on the result page. A failure shows an empty list and is logged: the
// worker's record above it is still right, and the page is shown once.
func (a *App) myPermits(r *http.Request, e worker.Employee) []MyPermit {
	if a.Permits == nil {
		return nil
	}
	list, err := a.Permits.ForPerson(r.Context(), e.PatientID, e.PersonalID, maxMyPermits)
	if err != nil {
		log.Printf("my permits: %v", err)
		return nil
	}
	today := a.Clock.Today()
	var out []MyPermit
	for _, p := range list {
		m := MyPermit{LEP: p.LEP, ValidUntil: p.ValidUntil}
		switch {
		case p.LEP == "" && p.Status == permit.StatusRejected:
			m.Label, m.Class = "Rejected", "state-bad"
		case p.LEP == "":
			m.Label, m.Class = "Waiting for review", "state-wait"
		default:
			state := card.StateOf(p.Status, dates.Day(p.ValidUntil), today)
			m.Label, m.Class, m.CardURL = state.Label(), stateClass(state), "/c/"+p.LEP
			m.CanTicket = state == card.Valid
			if m.CanTicket {
				m.TicketText = a.liveTicketText(r, p.LEP)
			}
			m.HasPhoto = a.photoOnDisk(p.IDPhoto)
		}
		out = append(out, m)
	}
	return out
}

// liveTicketText describes the newest working ticket on a permit that is still alive (waiting for the
// doctor, or approved), or "" when the permit has none, so the worker page can offer "Apply" instead.
// WHY a failure shows "": the worker can still open the schedule page, where the real list is shown.
func (a *App) liveTicketText(r *http.Request, lep string) string {
	if a.Tickets == nil {
		return ""
	}
	rows, err := a.Tickets.ForLEP(r.Context(), lep)
	if err != nil {
		log.Printf("my permits: tickets of %s: %v", lep, err)
		return ""
	}
	best, found := ticket.Ticket{}, false
	for _, t := range rows {
		st := ticket.NormalizeStatus(string(t.Status))
		if (st == ticket.StatusPendingDoctor || st == ticket.StatusApproved) && (!found || t.Arrival > best.Arrival) {
			best, found = t, true
		}
	}
	if !found {
		return ""
	}
	label := "Approved"
	if ticket.NormalizeStatus(string(best.Status)) == ticket.StatusPendingDoctor {
		label = "Waiting for the doctor"
	}
	return fmt.Sprintf("%s to %s, %s", best.Arrival, best.Departure, label)
}

// myTicket opens the working-ticket page for a permit of the worker who just proved who they
// are, so they are not asked for the birth date a second time. It accepts only a genuine
// token and only a Valid permit that belongs to that worker; every other case gets the same
// plain refusal, so it cannot be used to find out which permit numbers exist.
func (a *App) myTicket(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	lep := clip(strings.ToUpper(strings.TrimSpace(r.PostFormValue("lep"))), 12)
	fail := func(status int, msg string) { a.ticketRender(w, r, status, TicketData{LEP: lep, Err: msg}) }

	key := "mytk:" + a.clientIP(r)
	if a.Limit.Count(key, submitWindow) >= ticketTriesPerIP {
		fail(http.StatusTooManyRequests, "Too many attempts. Please try again later.")
		return
	}
	a.Limit.Add(key)
	claims, ok := verifyPhoto(a.Secret, r.PostFormValue("token"), a.Clock.Now())
	if !ok || a.Permits == nil || !a.ticketAvailable() {
		fail(http.StatusBadRequest, "Your session has expired. Please look yourself up again on the home page.")
		return
	}
	if !a.ownsValidPermit(r, claims, lep) {
		fail(http.StatusNotFound, "That permit cannot take a working ticket.")
		return
	}
	a.openTicketFor(w, r, lep)
}

// openTicketFor opens the working-ticket page for a permit whose ownership the caller has already
// proven (the token on the worker page, or the worker cookie). It never asks for a birth date.
func (a *App) openTicketFor(w http.ResponseWriter, r *http.Request, lep string) {
	token := signAccess(a.Secret, accessClaims{LEP: lep, Expires: a.Clock.Now().AddMinutes(a.policy().TicketSessionMinutes)})
	d, status, msg := a.ticketOpen(r.Context(), lep, token)
	if status != 0 {
		a.ticketRender(w, r, status, TicketData{LEP: lep, Err: msg})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	a.ticketRender(w, r, http.StatusOK, d)
}

// ownsValidPermit is true when lep is a Valid permit of the worker the token names.
func (a *App) ownsValidPermit(r *http.Request, c photoClaims, lep string) bool {
	if !lepPattern.MatchString(lep) {
		return false
	}
	list, err := a.Permits.ForPerson(r.Context(), c.PatientID, c.PersonalID, 50)
	if err != nil {
		log.Printf("my ticket permits: %v", err)
		return false
	}
	today := a.Clock.Today()
	for _, p := range list {
		if p.LEP == lep && card.StateOf(p.Status, dates.Day(p.ValidUntil), today) == card.Valid {
			return true
		}
	}
	return false
}
