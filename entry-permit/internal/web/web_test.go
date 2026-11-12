package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakePinger struct{ err error }

func (f fakePinger) PingContext(context.Context) error { return f.err }

func TestHealth(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
		body string
	}{
		{"up", nil, http.StatusOK, `"status":"ok"`},
		{"down", errors.New("boom"), http.StatusServiceUnavailable, "database unavailable"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		Health(fakePinger{c.err}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.body) {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: health must not be cached", c.name)
		}
		// The error text must never reach the client.
		if c.err != nil && strings.Contains(rec.Body.String(), "boom") {
			t.Error("internal error leaked")
		}
	}
}
