package web

import (
	"html/template"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"

	"entry-permit/internal/card"
	"entry-permit/internal/staff"
)

// cardPattern is the shape of a work-permit number. Only these have a card.
var cardPattern = regexp.MustCompile(`^LEP-[A-Z0-9]{5}$`)

// OG is the link-preview data for one page. Only the card page sets it.
type OG struct {
	Title, Description, URL string
}

// CardData is the public card, already masked: the template cannot show more
// than this struct holds.
type CardData struct {
	LEP, Name, Company, Parent, Position string
	ValidUntil, StateLabel, StateClass   string
	Theme                                string
	PhotoURL                             string // blank unless the viewer may see the photo and one is stored
	QR                                   template.HTML
}

// cardPhotoOf finds the stored photo path for a viewer who may see it: the permit's own signed-in
// worker (owner) or a signed-in member of staff. ok is false for everyone else, so the public card
// never carries a picture, and the path may be blank when the permit has no photo yet.
// WHY: the ID photo is personal data, while the card page itself is open to anyone holding the number.
func (a *App) cardPhotoOf(r *http.Request, lep string) (rel string, owner, ok bool) {
	if claims, _, signedIn := a.workerFromCookie(r); signedIn {
		own, found, err := a.Permits.OwnedCard(r.Context(), claims.PatientID, claims.PersonalID, lep)
		if err != nil {
			log.Printf("card photo owner %s: %v", lep, err)
		} else if found {
			return own.IDPhoto, true, true
		}
	}
	if a.currentUser(r) != nil {
		rel, found, err := a.Permits.IDPhoto(r.Context(), lep)
		if err != nil {
			log.Printf("card photo staff %s: %v", lep, err)
			return "", false, false
		}
		return rel, false, found
	}
	return "", false, false
}

// photoOnDisk says whether a stored photo path points at a file, so the page never shows a broken picture.
func (a *App) photoOnDisk(rel string) bool {
	if rel == "" {
		return false
	}
	path, _, err := a.Uploads.Path(rel)
	if err != nil {
		return false
	}
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

// cardPhotoFile serves the ID photo to the permit's own worker and to staff, and to nobody else.
// Every refusal is the same plain 404, so it cannot be used to find out which numbers exist.
func (a *App) cardPhotoFile(w http.ResponseWriter, r *http.Request) {
	lep := strings.ToUpper(r.PathValue("lep"))
	if !cardPattern.MatchString(lep) || a.Permits == nil {
		http.NotFound(w, r)
		return
	}
	rel, _, ok := a.cardPhotoOf(r, lep)
	data := a.readPhoto(rel)
	kind := http.DetectContentType(data)
	if !ok || len(data) == 0 || (kind != "image/jpeg" && kind != "image/png" && kind != "image/gif" && kind != "image/webp") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

// gateScanPage is where a signed-in gate officer lands after scanning a card.
// The page itself comes in slice 5; the redirect is wired now so printed cards
// never have to change.
func gateScanPage(lep string) string { return "/staff/gate?lep=" + lep }

// scanTarget is where a scan of an old-style address should go, or "" when the
// address is not one we recognise.
//
// WHY only a strictly shaped LEP is ever put into a redirect: the number comes
// from the address bar, so anything else (a full URL, "//host", "../") would
// turn this into an open redirect. A bad address just falls through to the home page.
func (a *App) scanTarget(r *http.Request) string {
	q := r.URL.Query()
	switch q.Get("page") {
	case "ticket":
		lep := strings.ToUpper(strings.TrimSpace(q.Get("lep")))
		if !lepPattern.MatchString(lep) {
			return ""
		}
		if a.isGateOfficer(r) {
			return gateScanPage(lep)
		}
		return "/ticket?lep=" + lep
	case "card":
		id := strings.ToUpper(strings.TrimSpace(q.Get("id")))
		if !cardPattern.MatchString(id) {
			return ""
		}
		return "/c/" + id
	}
	return ""
}

func (a *App) isGateOfficer(r *http.Request) bool {
	u := a.currentUser(r)
	return u != nil && u.HasRole(staff.GateOfficer)
}

// cardPage is the public card for one LEP number. Every number that is not a
// work permit with a number (unknown, waiting, wrong shape) gets the same 404.
func (a *App) cardPage(w http.ResponseWriter, r *http.Request) {
	lep := strings.ToUpper(r.PathValue("lep"))
	if !cardPattern.MatchString(lep) || a.Permits == nil {
		a.cardNotFound(w, r)
		return
	}
	if a.isGateOfficer(r) {
		http.Redirect(w, r, gateScanPage(lep), http.StatusSeeOther)
		return
	}
	info, found, err := a.Permits.Card(r.Context(), lep)
	if err != nil {
		log.Printf("card %s: %v", lep, err)
		http.Error(w, "Something went wrong", http.StatusInternalServerError)
		return
	}
	if !found {
		a.cardNotFound(w, r)
		return
	}
	svg, err := card.QRSVG(card.Payload(a.BaseURL, lep))
	if err != nil {
		log.Printf("card %s QR: %v", lep, err)
		http.Error(w, "Something went wrong", http.StatusInternalServerError)
		return
	}
	state := card.StateOf(info.Status, info.ValidUntil, a.Clock.Today())
	name, photoURL := card.MaskName(info.WorkerName), ""
	if rel, owner, ok := a.cardPhotoOf(r, lep); ok {
		if owner {
			name = info.WorkerName // a worker sees their own full name, as on the downloaded file
		}
		if a.photoOnDisk(rel) {
			photoURL = "/c/" + lep + "/photo"
		}
	}
	d := CardData{
		LEP: lep, Name: name, Company: info.Company, PhotoURL: photoURL,
		Parent: card.LowestParent(info.Parents), Position: info.Position,
		ValidUntil: card.ShortDate(info.ValidUntil),
		StateLabel: state.Label(), StateClass: stateClass(state),
		Theme: card.ThemeFor(info.Entity),
		// The SVG is built only from integers by card.QRSVG, never from request text.
		QR: template.HTML(svg), //nolint:gosec
	}
	a.render(w, "card", http.StatusOK, View{
		Title: "Entry Permit " + lep, User: a.currentUser(r), Card: d,
		OG: OG{Title: "Entry Permit " + lep, Description: cardDescription(d, state), URL: strings.TrimRight(a.BaseURL, "/") + "/c/" + lep},
	})
}

func (a *App) cardNotFound(w http.ResponseWriter, r *http.Request) {
	a.render(w, "card_missing", http.StatusNotFound, View{Title: "Permit not found", User: a.currentUser(r)})
}

func stateClass(s card.State) string {
	if s == card.Valid {
		return "state-valid"
	}
	return "state-bad"
}

// cardDescription is the link-preview line: masked name, company, validity.
func cardDescription(d CardData, s card.State) string {
	parts := []string{d.Name, d.Company}
	if s == card.Valid {
		parts = append(parts, "Valid until "+d.ValidUntil)
	} else {
		parts = append(parts, s.Label())
	}
	return strings.Join(parts, " · ")
}
