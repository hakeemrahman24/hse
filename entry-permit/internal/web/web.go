// Package web holds HTTP handlers. Slice 1a only needs the health check.
package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"
)

const healthTimeout = 2 * time.Second

// Pinger is the part of *sql.DB the health check needs.
type Pinger interface {
	PingContext(ctx context.Context) error
}

var _ Pinger = (*sql.DB)(nil)

// Health answers 200 when the database responds, 503 otherwise. WHY: the
// deploy script and systemd both need a cheap "is it really working" URL.
func Health(db Pinger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), healthTimeout)
		defer cancel()
		status, code := "ok", http.StatusOK
		if err := db.PingContext(ctx); err != nil {
			status, code = "database unavailable", http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
	})
}
