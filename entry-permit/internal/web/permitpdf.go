package web

import (
	"bytes"
	"log"
	"net/http"
	"os"
	"strings"

	"entry-permit/internal/audit"
	"entry-permit/internal/card"
)

// pdfTriesPerHour limits downloads per address. A person downloads their card once or twice;
// every download draws a PDF in memory, which is the costly thing on a small machine.
const pdfTriesPerHour = 10

// myPermitPDF gives a proven worker their own permit as a PDF: full name, photo, validity,
// state, and the QR code of the card. It needs the token from the result page and a permit
// number that belongs to that worker; every other case gets the same plain refusal.
//
// WHY POST and not a link: the token proves who is asking and must not sit in the address bar,
// browser history or server logs.
func (a *App) myPermitPDF(w http.ResponseWriter, r *http.Request) {
	refuse := func(status int, msg string) {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, msg, status)
	}
	if err := r.ParseForm(); err != nil {
		refuse(http.StatusBadRequest, "Bad request")
		return
	}
	key := "pdf:" + a.clientIP(r)
	if a.Limit.Count(key, submitWindow) >= pdfTriesPerHour {
		refuse(http.StatusTooManyRequests, "Too many attempts. Please try again later.")
		return
	}
	a.Limit.Add(key)
	claims, ok := verifyPhoto(a.Secret, r.PostFormValue("token"), a.Clock.Now())
	if !ok || a.Permits == nil {
		refuse(http.StatusBadRequest, "Your session has expired. Please look yourself up again on the home page.")
		return
	}
	lep := strings.ToUpper(strings.TrimSpace(r.PostFormValue("lep")))
	if !cardPattern.MatchString(lep) {
		refuse(http.StatusNotFound, "That permit cannot be downloaded.")
		return
	}
	own, found, err := a.Permits.OwnedCard(r.Context(), claims.PatientID, claims.PersonalID, lep)
	if err != nil {
		log.Printf("own card %s: %v", lep, err)
		refuse(http.StatusServiceUnavailable, "The file could not be made right now. Please try again later.")
		return
	}
	if !found {
		refuse(http.StatusNotFound, "That permit cannot be downloaded.")
		return
	}
	c := card.PDFCard{
		LEP: lep, Name: own.WorkerName, Company: own.Company, Parent: card.LowestParent(own.Parents), Position: own.Position,
		ValidUntil: card.ShortDate(own.ValidUntil), State: card.StateOf(own.Status, own.ValidUntil, a.Clock.Today()),
		Entity: own.Entity, Payload: card.Payload(a.BaseURL, lep), Photo: a.readPhoto(own.IDPhoto), GeneratedAt: string(a.Clock.Now()),
	}
	var buf bytes.Buffer
	if err := card.PDF(&buf, c); err != nil {
		log.Printf("card pdf %s: %v", lep, err)
		refuse(http.StatusInternalServerError, "The file could not be made right now. Please try again later.")
		return
	}
	// Logged before the file is sent, like the dashboard export; a failed log row is reported
	// but does not stop a worker getting their own card.
	if err := audit.Write(r.Context(), a.SQL, a.Clock.Now(), "worker", "permit.download", "permit:"+lep, "", string(a.Clock.Today())); err != nil {
		log.Printf("audit permit.download %s: %v", lep, err)
	}
	h := w.Header()
	h.Set("Content-Type", "application/pdf")
	h.Set("Content-Disposition", `attachment; filename="`+lep+`.pdf"`)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(buf.Bytes())
}

// readPhoto returns the stored photo bytes, or nil when there is none or it cannot be read: the
// card is made without a picture rather than not at all.
func (a *App) readPhoto(rel string) []byte {
	if rel == "" {
		return nil
	}
	path, _, err := a.Uploads.Path(rel)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}
