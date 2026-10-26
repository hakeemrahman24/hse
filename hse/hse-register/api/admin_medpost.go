package main

import (
	"errors"
	"log"
	"net/http"
	"strings"
)

// medpostHandlers is Settings > Medpost API (administrators): the address and token used to talk
// to hse-medpost, a Test connection button, and a way back to the server's own settings.
type medpostHandlers struct {
	auth     *authContext
	conn     *medpostConn
	onChange func() // drops the caches that were filled from the old connection
}

func (h *medpostHandlers) page(w http.ResponseWriter, r *http.Request, status int, formURL, msg, kind string) {
	st := h.conn.Status()
	if formURL == "" {
		formURL = st.URL
	}
	renderStatus(w, status, "settings_medpost", PageData{
		Title: "Medpost API", Heading: "Medpost API", Active: "set-medpost",
		Subheading: "How this register talks to HSE Medpost: the employee list for the SOC form, the company list, and the Medical Activities page.",
		Session:    h.auth.session(r), Medpost: st, MedpostURL: formURL, Flash: msg, FlashKind: kind,
	})
}

func (h *medpostHandlers) show(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("done") == "cleared" {
		h.page(w, r, http.StatusOK, "", "Saved settings removed.", "success")
		return
	}
	h.page(w, r, http.StatusOK, "", "", "")
}

// submit handles both buttons of the form: action=save stores the settings and then tests them,
// action=test only tests what is typed (an empty token means the one already in use).
func (h *medpostHandlers) submit(w http.ResponseWriter, r *http.Request) {
	rawURL := r.FormValue("url")
	token := strings.TrimSpace(r.FormValue("token"))

	if r.FormValue("action") == "test" {
		u, err := validateMedpostURL(rawURL)
		if err != nil {
			h.page(w, r, http.StatusUnprocessableEntity, rawURL, err.Error(), "error")
			return
		}
		if token == "" {
			token = h.conn.savedToken()
		}
		if err := validateMedpostToken(token); err != nil {
			h.page(w, r, http.StatusUnprocessableEntity, rawURL, err.Error(), "error")
			return
		}
		res := testMedpost(r.Context(), u, token)
		kind := "error"
		if res.OK {
			kind = "success"
		}
		h.page(w, r, http.StatusOK, rawURL, "Test: "+res.Message+" (nothing was saved)", kind)
		return
	}

	if err := h.conn.Save(rawURL, token); err != nil {
		var fe formError
		if errors.As(err, &fe) {
			h.page(w, r, http.StatusUnprocessableEntity, rawURL, fe.Error(), "error")
			return
		}
		log.Printf("medpost settings save: %v", err)
		http.Error(w, "Could not save the settings.", http.StatusInternalServerError)
		return
	}
	h.onChange()
	st := h.conn.Status()
	res := testMedpost(r.Context(), st.URL, h.conn.savedToken())
	kind, msg := "success", "Saved. "+res.Message
	if !res.OK {
		kind, msg = "error", "Saved, but "+lowerFirst(res.Message)
	}
	h.page(w, r, http.StatusOK, "", msg, kind)
}

func (h *medpostHandlers) clear(w http.ResponseWriter, r *http.Request) {
	if err := h.conn.Clear(); err != nil {
		log.Printf("medpost settings clear: %v", err)
		http.Error(w, "Could not remove the settings.", http.StatusInternalServerError)
		return
	}
	h.onChange()
	http.Redirect(w, r, "/admin/settings/medpost-api?done=cleared", http.StatusSeeOther)
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
