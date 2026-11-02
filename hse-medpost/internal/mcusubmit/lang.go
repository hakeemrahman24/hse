package mcusubmit

import (
	"bytes"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Language switcher (owner, 2026-10-29): English and Indonesian, chosen with a button in the
// header and remembered in a cookie.
//
// The templates stay in English and are not full of lookups. Instead the finished page is passed
// through the dictionary in lang_id.go: a piece of visible text is replaced only when the WHOLE
// piece equals a dictionary entry, so anything a person typed, a name or an email address is left
// alone unless it happens to be exactly a dictionary phrase. Text that is not in the dictionary
// simply stays English, so a missing translation is a small blemish and never a broken page.

const (
	langCookie  = "mcu_lang"
	langEnglish = "en"
	langIndo    = "id"

	// nextPlaceholder is written by the header template where the switcher needs the current
	// address; withLanguage fills it in. Handlers and templates therefore never need the request.
	nextPlaceholder = "@@NEXT@@"
)

var (
	textNode = regexp.MustCompile(`>([^<>]+)<`)
	// title/placeholder/aria-label/alt carry words a reader sees or hears; data-confirm is the question
	// a confirm box asks (mcu-portal-cards.js).
	visibleAttr = regexp.MustCompile(`\b(title|placeholder|aria-label|alt|data-confirm)="([^"]*)"`)
)

// languageOf reads the choice; anything but a known value means English.
func languageOf(r *http.Request) string {
	if c, err := r.Cookie(langCookie); err == nil && c.Value == langIndo {
		return langIndo
	}
	return langEnglish
}

// SetLanguage stores the chosen language and returns to the page the person was on. The return
// address is accepted only when it is a path inside the portal, so this cannot be used to send
// someone to another site.
func (a *API) SetLanguage(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	lang := r.PostFormValue("lang")
	if lang != langEnglish && lang != langIndo {
		http.Error(w, "unknown language", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: langCookie, Value: lang, Path: basePath, HttpOnly: true,
		Secure: a.secureCookie, SameSite: http.SameSiteLaxMode, MaxAge: 365 * 24 * 60 * 60,
	})
	http.Redirect(w, r, safeNext(r.PostFormValue("next")), http.StatusSeeOther)
}

// safeNext returns next when it is an address inside the portal, otherwise the portal's front page.
func safeNext(next string) string {
	if next != basePath && !strings.HasPrefix(next, basePath+"/") && !strings.HasPrefix(next, basePath+"?") {
		return basePath
	}
	if strings.ContainsAny(next, "\\\r\n") || strings.Contains(next, "..") || strings.HasPrefix(next, basePath+"//") {
		return basePath
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return basePath
	}
	return next
}

// langWriter holds a page back until the handler is done, so it can be translated whole.
type langWriter struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
}

func (l *langWriter) WriteHeader(code int) {
	if l.status == 0 {
		l.status = code
	}
}

func (l *langWriter) Write(p []byte) (int, error) {
	if l.status == 0 {
		l.status = http.StatusOK
	}
	return l.buf.Write(p)
}

// withLanguage fills in the switcher's return address and, for Indonesian, translates every HTML
// page. File downloads are streamed untouched.
func (a *API) withLanguage(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/file") {
			next.ServeHTTP(w, r)
			return
		}
		lw := &langWriter{ResponseWriter: w}
		next.ServeHTTP(lw, r)
		if lw.status == 0 {
			lw.status = http.StatusOK
		}
		body := lw.buf.Bytes()
		if strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
			page := string(body)
			if languageOf(r) == langIndo {
				page = translateHTML(page)
			}
			// The page's own address, escaped for an attribute, so the choice returns here.
			page = strings.ReplaceAll(page, nextPlaceholder, html.EscapeString(r.URL.RequestURI()))
			body = []byte(page)
		}
		w.Header().Del("Content-Length")
		w.WriteHeader(lw.status)
		_, _ = w.Write(body)
	})
}

// translateHTML swaps dictionary phrases for Indonesian in visible text and attributes.
func translateHTML(page string) string {
	page = strings.Replace(page, `<html lang="en">`, `<html lang="id">`, 1)
	page = textNode.ReplaceAllStringFunc(page, func(m string) string {
		inner := m[1 : len(m)-1]
		lead := inner[:len(inner)-len(strings.TrimLeft(inner, " \t\r\n"))]
		trail := inner[len(strings.TrimRight(inner, " \t\r\n")):]
		core := strings.TrimSpace(inner)
		if core == "" {
			return m
		}
		return ">" + lead + translatePhrase(core) + trail + "<"
	})
	return visibleAttr.ReplaceAllStringFunc(page, func(m string) string {
		sub := visibleAttr.FindStringSubmatch(m)
		return sub[1] + `="` + translatePhrase(sub[2]) + `"`
	})
}

// dictionary is indonesianText with its keys and values trimmed. The dictionary file keeps the
// trailing space of a split heading ("Sign " + "in") so it reads like the template, but
// translateHTML looks up the trimmed text of a node and puts the surrounding whitespace back itself.
var dictionary = func() map[string]string {
	m := make(map[string]string, len(indonesianText))
	for k, v := range indonesianText {
		m[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return m
}()

// translatePhrase returns the Indonesian text for an exact English phrase (as it appears in the
// HTML, so entities are decoded first and encoded again), or the phrase unchanged. A page title
// is "<page> · <portal name>", so its two halves are looked up separately.
func translatePhrase(escaped string) string {
	plain := html.UnescapeString(escaped)
	if t, ok := dictionary[plain]; ok {
		return html.EscapeString(t)
	}
	if left, right, ok := strings.Cut(plain, " · "); ok {
		l, lok := dictionary[left]
		r, rok := dictionary[right]
		if lok || rok {
			if !lok {
				l = left
			}
			if !rok {
				r = right
			}
			return html.EscapeString(l + " · " + r)
		}
	}
	return escaped
}
