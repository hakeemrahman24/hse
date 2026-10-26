// Package region serves Indonesia's administrative areas (province, regency/city,
// district, village) for the cascading dropdowns on the screening forms.
// The Region table is loaded by indonesia-region/load.sh, not by the app.
package region

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

const (
	defaultSearchLimit = 50
	maxSearchLimit     = 100
	maxSearchWords     = 6
	maxSearchWordRunes = 40
	// likeEscape is the escape character in LIKE patterns. "!" is used because
	// a backslash means different things to MariaDB and SQLite string literals.
	likeEscape = "!"
)

// codePattern is the shape of every Region code, so a parent that does not match
// is refused before it reaches the query.
var codePattern = regexp.MustCompile(`^\d{2}(\.\d{2}(\.\d{2})?)?$`)

type API struct {
	DB *sql.DB
}

// item is one area. ParentCode and Path are filled only in search mode, so a
// result can say where the area is: "Wanggar" is in "Kabupaten Jayapura, Papua".
type item struct {
	Code       string `json:"code"`
	Name       string `json:"name"`
	ParentCode string `json:"parentCode,omitempty"`
	Path       string `json:"path,omitempty"`
}

// List answers GET /api/regions with either ?parent=CODE (the children of one
// area) or ?level=1..4 (a whole level, default 1). The two cannot be combined.
//
// With ?q=words (together with level) it searches that level instead: every word
// must appear in the area's own name or in the name of one of its three nearest
// parents, so "wanggar jayapura" finds the Wanggar in Kabupaten Jayapura. Each
// result then carries parentCode and path. ?limit= caps the results (default 50,
// at most 100).
func (a *API) List(w http.ResponseWriter, r *http.Request) {
	parent := r.URL.Query().Get("parent")
	levelText := r.URL.Query().Get("level")
	if parent != "" && levelText != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "use parent or level, not both"})
		return
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" || r.URL.Query().Has("q") || r.URL.Query().Has("limit") {
		a.search(w, r, parent, levelText, q)
		return
	}

	var rows *sql.Rows
	var err error
	if parent != "" {
		if !codePattern.MatchString(parent) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid parent code"})
			return
		}
		rows, err = a.DB.Query(`SELECT Code, Name FROM Region WHERE ParentCode = ? ORDER BY Name, Code`, parent)
	} else {
		level := 1
		if levelText != "" {
			level, err = strconv.Atoi(levelText)
			if err != nil || level < 1 || level > 4 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "level must be 1 to 4"})
				return
			}
		}
		rows, err = a.DB.Query(`SELECT Code, Name FROM Region WHERE Level = ? ORDER BY Name, Code`, level)
	}
	if err != nil {
		log.Printf("region list: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()

	out := []item{}
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.Code, &it.Name); err != nil {
			log.Printf("region scan: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		log.Printf("region rows: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// search is List's ?level=&q= mode.
func (a *API) search(w http.ResponseWriter, r *http.Request, parent, levelText, q string) {
	if parent != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "search works on a level, not a parent"})
		return
	}
	level := 1
	if levelText != "" {
		var err error
		if level, err = strconv.Atoi(levelText); err != nil || level < 1 || level > 4 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "level must be 1 to 4"})
			return
		}
	}
	limit := defaultSearchLimit
	if text := r.URL.Query().Get("limit"); text != "" {
		var err error
		if limit, err = strconv.Atoi(text); err != nil || limit < 1 || limit > maxSearchLimit {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be 1 to 100"})
			return
		}
	}
	words := strings.Fields(q)
	if len(words) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "q is empty"})
		return
	}
	if len(words) > maxSearchWords {
		words = words[:maxSearchWords]
	}

	query := `
		SELECT r.Code, r.Name, COALESCE(r.ParentCode,''), COALESCE(p1.Name,''), COALESCE(p2.Name,''), COALESCE(p3.Name,'')
		FROM Region r
		LEFT JOIN Region p1 ON p1.Code = r.ParentCode
		LEFT JOIN Region p2 ON p2.Code = p1.ParentCode
		LEFT JOIN Region p3 ON p3.Code = p2.ParentCode
		WHERE r.Level = ?`
	args := []any{level}
	for _, word := range words {
		runes := []rune(word)
		if len(runes) > maxSearchWordRunes {
			word = string(runes[:maxSearchWordRunes])
		}
		pattern := "%" + escapeLike(word) + "%"
		query += ` AND (r.Name LIKE ? ESCAPE '` + likeEscape + `' OR p1.Name LIKE ? ESCAPE '` + likeEscape +
			`' OR p2.Name LIKE ? ESCAPE '` + likeEscape + `')`
		args = append(args, pattern, pattern, pattern)
	}
	query += ` ORDER BY r.Name, r.Code LIMIT ?`
	args = append(args, limit)

	rows, err := a.DB.Query(query, args...)
	if err != nil {
		log.Printf("region search: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	defer rows.Close()
	out := []item{}
	for rows.Next() {
		var it item
		var p1, p2, p3 string
		if err := rows.Scan(&it.Code, &it.Name, &it.ParentCode, &p1, &p2, &p3); err != nil {
			log.Printf("region search scan: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		var parts []string
		for _, name := range []string{p1, p2, p3} {
			if name != "" {
				parts = append(parts, name)
			}
		}
		it.Path = strings.Join(parts, ", ")
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		log.Printf("region search rows: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// escapeLike makes a typed word safe inside a LIKE pattern: %, _ and the escape
// character itself match only themselves.
func escapeLike(word string) string {
	replacer := strings.NewReplacer(likeEscape, likeEscape+likeEscape, "%", likeEscape+"%", "_", likeEscape+"_")
	return replacer.Replace(word)
}
