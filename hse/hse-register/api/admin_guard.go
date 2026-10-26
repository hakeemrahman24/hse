package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The typo guard list (plan v8, section 2 rule 4 and section 5): real words that look like a keyword
// and must never be "corrected" by typo tolerance. HSE adds to it from the pilot data. It is stored
// as one line per word in app_setting, so no migration is needed beyond 014.
const (
	settingGuardWords = "typo_guard_words"
	guardPath         = "/admin/settings/guard-words"
	maxGuardWords     = 2000
	maxGuardWordLen   = 40
)

// parseGuardWords turns the text of the page into a sorted list of distinct lower-case words.
// Words are split on spaces, commas and new lines; anything that is not a letter is dropped from
// the edges and a word with other characters inside, or one shorter than the typo length, is
// ignored. It reports how many entries were ignored.
func parseGuardWords(text string) (words []string, ignored int) {
	seen := map[string]bool{}
	for _, f := range strings.FieldsFunc(text, func(r rune) bool { return unicode.IsSpace(r) || r == ',' || r == ';' }) {
		w := strings.ToLower(strings.TrimFunc(f, func(r rune) bool { return !unicode.IsLetter(r) }))
		n := utf8.RuneCountInString(w)
		if n < typoMinWord || n > maxGuardWordLen || strings.IndexFunc(w, func(r rune) bool { return !unicode.IsLetter(r) }) >= 0 {
			ignored++
			continue
		}
		if !seen[w] {
			seen[w] = true
			words = append(words, w)
		}
	}
	sort.Strings(words)
	if len(words) > maxGuardWords {
		ignored += len(words) - maxGuardWords
		words = words[:maxGuardWords]
	}
	return words, ignored
}

// loadGuardWords reads the saved list. A missing table or setting gives an empty list, so the
// suggestion keeps working before migration 014 is run.
func loadGuardWords(ctx context.Context, db *sql.DB) map[string]bool {
	var text string
	err := db.QueryRowContext(ctx, `SELECT value FROM app_setting WHERE name = ?`, settingGuardWords).Scan(&text)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("guard words: %v", err)
		}
		return nil
	}
	words, _ := parseGuardWords(text)
	out := make(map[string]bool, len(words))
	for _, w := range words {
		out[w] = true
	}
	return out
}

type guardHandlers struct {
	db   *sql.DB
	auth *authContext
	ref  *refData
}

func (h *guardHandlers) page(w http.ResponseWriter, r *http.Request, status int, text, message, kind string) {
	renderStatus(w, status, "settings_guard", PageData{
		Title: "Typo guard words", Heading: "Typo guard words", Active: "set-guard",
		Subheading: "Real words that look like a keyword. Typo tolerance never changes them. Add a word here when the pilot shows it was wrongly corrected.",
		Session:    h.auth.session(r), GuardText: text, Flash: message, FlashKind: kind,
	})
}

func (h *guardHandlers) show(w http.ResponseWriter, r *http.Request) {
	var text string
	if err := h.db.QueryRow(`SELECT value FROM app_setting WHERE name = ?`, settingGuardWords).Scan(&text); err != nil && err != sql.ErrNoRows {
		log.Printf("guard words: %v", err)
		http.Error(w, "Could not load the list. Did you run migration 014?", http.StatusInternalServerError)
		return
	}
	msg, kind := "", ""
	if r.URL.Query().Get("done") == "saved" {
		msg, kind = "Saved. The suggestions use the new list within a moment.", "success"
	}
	h.page(w, r, http.StatusOK, text, msg, kind)
}

func (h *guardHandlers) save(w http.ResponseWriter, r *http.Request) {
	words, ignored := parseGuardWords(r.FormValue("words"))
	text := strings.Join(words, "\n")
	if _, err := h.db.Exec(`INSERT INTO app_setting (name, value) VALUES (?, ?)
		ON DUPLICATE KEY UPDATE value = VALUES(value)`, settingGuardWords, text); err != nil {
		log.Printf("guard words save: %v", err)
		http.Error(w, "Could not save the list. Did you run migration 014?", http.StatusInternalServerError)
		return
	}
	h.ref.Invalidate()
	if ignored > 0 {
		h.page(w, r, http.StatusOK, text, "Saved. Ignored "+strconv.Itoa(ignored)+" entries (a word needs 4 to 40 letters and nothing else).", "success")
		return
	}
	http.Redirect(w, r, guardPath+"?done=saved", http.StatusSeeOther)
}
