// Package presence tracks who is signed in right now and which section they
// are viewing. It is an in-memory heartbeat store, not a durable record.
// There is no websocket in this app, so the open page posts a heartbeat
// every few seconds and a session stays active only while that heartbeat is
// inside staleAfter.
package presence

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

const staleAfter = 20 * time.Second

const maxSectionRunes = 48

// Person is one other staff member currently signed in.
type Person struct {
	Name    string `json:"name"`
	Section string `json:"section"`
}

type entry struct {
	staffID  int64
	name     string
	username string
	section  string
	lastSeen time.Time
}

// Store keeps one current section per staff member. A later heartbeat
// replaces the earlier one, so a person is listed once.
type Store struct {
	mu   sync.Mutex
	byID map[int64]entry
}

func NewStore() *Store {
	return &Store{byID: map[int64]entry{}}
}

// Touch records that staffID is active in section right now.
func (s *Store) Touch(staffID int64, name, username, section string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[staffID] = entry{
		staffID:  staffID,
		name:     name,
		username: username,
		section:  section,
		lastSeen: time.Now(),
	}
}

// administratorUsername is the built-in login. Its display name is
// "Administrator", an alias for dr. Arif Rahman Hakim, who also signs in
// under his own name. Announcing that login would tell everyone else the
// same person arrived twice. The match is the username, not the admin role,
// so other administrators still appear.
func isAdministratorUsername(username string) bool {
	return strings.EqualFold(strings.TrimSpace(username), "admin")
}

// Active returns everyone except excludeStaffID whose last heartbeat is
// still inside staleAfter, sorted by name. Expired rows are dropped.
func (s *Store) Active(excludeStaffID int64) []Person {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-staleAfter)
	out := make([]Person, 0, len(s.byID))
	for id, e := range s.byID {
		if e.lastSeen.Before(cutoff) {
			delete(s.byID, id)
			continue
		}
		if id == excludeStaffID || isAdministratorUsername(e.username) {
			continue
		}
		out = append(out, Person{Name: e.name, Section: e.section})
	}
	sortPeople(out)
	return out
}

func sortPeople(people []Person) {
	for i := 1; i < len(people); i++ {
		item := people[i]
		j := i
		for j > 0 && strings.ToLower(people[j-1].Name) > strings.ToLower(item.Name) {
			people[j] = people[j-1]
			j--
		}
		people[j] = item
	}
}

// SessionFunc resolves the caller's staff id, display name, and username.
type SessionFunc func(*http.Request) (staffID int64, name, username string, ok bool)

// Heartbeat handles POST /api/presence/heartbeat?section=Data+entry.
func Heartbeat(store *Store, session SessionFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		staffID, name, username, ok := session(r)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		section, ok := cleanSection(r.URL.Query().Get("section"))
		if !ok {
			http.Error(w, "section required", http.StatusBadRequest)
			return
		}
		store.Touch(staffID, name, username, section)
		w.WriteHeader(http.StatusNoContent)
	}
}

// Who handles GET /api/presence/who. The caller is left out of the list.
func Who(store *Store, session SessionFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		staffID, _, _, ok := session(r)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"others": store.Active(staffID)})
	}
}

func cleanSection(raw string) (string, bool) {
	section := strings.Join(strings.Fields(raw), " ")
	if section == "" {
		return "", false
	}
	runes := []rune(section)
	if len(runes) > maxSectionRunes {
		runes = runes[:maxSectionRunes]
	}
	return string(runes), true
}
