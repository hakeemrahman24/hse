package middleware

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	"hse-medpost/internal/auth"
)

type ctxKey int

const sessionKey ctxKey = 1

func WithSession(store *auth.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if sess, err := store.Get(r); err == nil {
				ctx := context.WithValue(r.Context(), sessionKey, sess)
				r = r.WithContext(ctx)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if Session(r) == nil {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		// A signed-in page must not be replayed from the browser cache after
		// logout (back button), or it looks live while every action fails.
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAdmin restricts to the admin role.
func RequireAdmin(next http.Handler) http.Handler {
	return requireRole(next, "admin")
}

// RequireProgramManager allows admin only (the manager role is removed).
func RequireProgramManager(next http.Handler) http.Handler {
	return requireRole(next, "admin")
}

func requireRole(next http.Handler, allowed ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := Session(r)
		if sess == nil {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		ok := false
		for _, role := range allowed {
			if sess.Role == role {
				ok = true
				break
			}
		}
		if !ok {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			http.Redirect(w, r, "/programs", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// IsDoctorOrCMO is true when the job title is a doctor or the Chief Medical Officer.
// A nurse and the built-in administrator are not included.
func IsDoctorOrCMO(position string) bool {
	p := strings.ToLower(strings.Join(strings.Fields(position), " "))
	if p == "chief medical officer" || p == "cmo" {
		return true
	}
	for _, word := range strings.Fields(p) {
		if word == "doctor" {
			return true
		}
	}
	return false
}

// MayOpenMedicalCheckup is true for an active doctor, Chief Medical Officer,
// or the Administrator superuser. The administrator is not a clinician, so
// signing stays with IsDoctorOrCMO, but the admin role can open every module.
func MayOpenMedicalCheckup(db *sql.DB, staffID int64) bool {
	if db == nil || staffID == 0 {
		return false
	}
	var position, role string
	err := db.QueryRow(`
		SELECT COALESCE(s.Position,''), COALESCE(u.Role,'')
		FROM MedpostStaffTable s
		LEFT JOIN AppUserTable u ON u.FK_StaffID = s.StaffID
		WHERE s.StaffID = ? AND s.Status = 'Active'`,
		staffID,
	).Scan(&position, &role)
	if err != nil {
		return false
	}
	return role == "admin" || isAdministratorPosition(position) || IsDoctorOrCMO(position)
}

func isAdministratorPosition(position string) bool {
	switch strings.ToLower(strings.Join(strings.Fields(position), " ")) {
	case "administrator", "admin":
		return true
	default:
		return false
	}
}

// RequireDoctorOrCMO limits medical checkup pages and records to a doctor,
// the Chief Medical Officer, or the Administrator superuser.
func RequireDoctorOrCMO(db *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sess := Session(r)
			if sess == nil || !MayOpenMedicalCheckup(db, sess.StaffID) {
				if strings.HasPrefix(r.URL.Path, "/api/") {
					if sess == nil {
						http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
						return
					}
					http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
					return
				}
				http.Redirect(w, r, "/start", http.StatusSeeOther)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func Session(r *http.Request) *auth.Session {
	v, _ := r.Context().Value(sessionKey).(*auth.Session)
	return v
}

// ContextWithSession attaches a session for tests and other callers that
// already know who is signed in.
func ContextWithSession(ctx context.Context, sess *auth.Session) context.Context {
	return context.WithValue(ctx, sessionKey, sess)
}
