package main

import (
	"encoding/json"
	"net/http"
	"unicode/utf8"
)

// serveSuggestion answers a POST {"text": "..."} with the topic and hazard suggested for that
// description (classify.go). It is used by the staff form and, behind the employee sign-in and a
// rate limit, by the public SOC form. The text is only read, never stored or logged.
func serveSuggestion(w http.ResponseWriter, r *http.Request, ref *refData) {
	serveTopics(w, r, ref, func(snap *refSnapshot, text string) any { return snap.Suggest(text) })
}

// serveSuggestionTop answers the public SOC form with up to three hazard topics, best first:
// {"suggestions": [...]}. The reporter picks one; nothing is chosen for them (plan v8).
func serveSuggestionTop(w http.ResponseWriter, r *http.Request, ref *refData) {
	serveTopics(w, r, ref, func(snap *refSnapshot, text string) any {
		return map[string]any{"suggestions": snap.SuggestTop(text, socTopicSuggestions, true)}
	})
}

func serveTopics(w http.ResponseWriter, r *http.Request, ref *refData, answer func(*refSnapshot, string) any) {
	var in struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if utf8.RuneCountInString(in.Text) > maxObsText {
		in.Text = string([]rune(in.Text)[:maxObsText])
	}
	snap, err := ref.Get(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "lists not available"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, answer(snap, in.Text))
}
