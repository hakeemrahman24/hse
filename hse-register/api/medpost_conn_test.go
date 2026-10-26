package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSealedTokenRoundTripsAndIsNotPlain(t *testing.T) {
	c := newMedpostConn(nil, "secret-one")
	sealed, err := c.seal("tok-123")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "tok-123") || !strings.HasPrefix(sealed, sealPrefix) {
		t.Errorf("sealed token = %q", sealed)
	}
	if got, err := c.open(sealed); err != nil || got != "tok-123" {
		t.Errorf("open = %q, %v", got, err)
	}
	again, _ := c.seal("tok-123")
	if again == sealed {
		t.Error("two seals of the same token must differ (random nonce)")
	}
	other := newMedpostConn(nil, "secret-two")
	if _, err := other.open(sealed); err == nil {
		t.Error("a different session secret must not open the token")
	}
	for _, bad := range []string{"", "plain", sealPrefix + "!!!", sealPrefix + "AAAA"} {
		if _, err := c.open(bad); err == nil {
			t.Errorf("open(%q) accepted", bad)
		}
	}
}

func TestValidateMedpostURL(t *testing.T) {
	good := map[string]string{
		"https://medpost.example.com":      "https://medpost.example.com",
		" https://medpost.example.com/ ":   "https://medpost.example.com",
		"https://medpost.example.com/app/": "https://medpost.example.com/app",
		"http://127.0.0.1:8113":            "http://127.0.0.1:8113",
		"http://localhost:8113":            "http://localhost:8113",
		"http://[::1]:8113":                "http://[::1]:8113",
	}
	for in, want := range good {
		if got, err := validateMedpostURL(in); err != nil || got != want {
			t.Errorf("validate(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{
		"", "   ", "medpost.example.com", "ftp://x.example", "http://medpost.example.com", "http://192.168.1.5:8113",
		"https://user:pw@medpost.example.com", "https://medpost.example.com?x=1", "https://medpost.example.com#f",
		"https://", "javascript:alert(1)", "https://" + strings.Repeat("a", 250) + ".com",
	} {
		if got, err := validateMedpostURL(bad); err == nil {
			t.Errorf("validate(%q) accepted as %q", bad, got)
		}
	}
}

func TestValidateMedpostToken(t *testing.T) {
	if err := validateMedpostToken("abc123-_XYZ"); err != nil {
		t.Errorf("good token refused: %v", err)
	}
	for _, bad := range []string{"", "has space", "tab\there", "new\nline", strings.Repeat("x", 257)} {
		if validateMedpostToken(bad) == nil {
			t.Errorf("token %q accepted", bad)
		}
	}
}

func TestConnectionFallsBackToTheEnvironment(t *testing.T) {
	t.Setenv("MEDPOST_URL", "")
	t.Setenv("MEDPOST_DIRECTORY_TOKEN", "")
	c := newMedpostConn(nil, "s")
	if c.Get() != nil || c.Status().Configured {
		t.Error("nothing set: must be not configured")
	}
	if _, err := (liveDirectory{conn: c}).Search(context.Background(), "ab", 5); err != errMedpostOff {
		t.Errorf("unconfigured directory error = %v", err)
	}
	if _, err := (liveCompanies{conn: c}).Companies(context.Background()); err != errMedpostOff {
		t.Errorf("unconfigured companies error = %v", err)
	}
	if got, err := (liveCompanies{conn: c, fallback: devCompanies{}}).Companies(context.Background()); err != nil || len(got) == 0 {
		t.Errorf("fallback list = %v, %v", got, err)
	}

	t.Setenv("MEDPOST_URL", "https://medpost.example.com/")
	t.Setenv("MEDPOST_DIRECTORY_TOKEN", "envtoken")
	c = newMedpostConn(nil, "s")
	st := c.Status()
	if !st.Configured || st.Source != medpostSourceEnv || st.URL != "https://medpost.example.com" || !st.EnvSet {
		t.Errorf("status = %+v", st)
	}
	if c.savedToken() != "envtoken" {
		t.Error("an empty token field should reuse the environment token")
	}
}

// fakeMedpost answers like hse-medpost: the directory with the right token, the activities list
// only when withActivities is set.
func fakeMedpostAPI(t *testing.T, token string, withActivities bool, rows []medpostActivity) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/directory/employees":
			_, _ = w.Write([]byte("[]"))
		case "/api/directory/activities":
			if !withActivities {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(rows)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestTestMedpostReportsEachOutcome(t *testing.T) {
	ctx := context.Background()
	full := fakeMedpostAPI(t, "good", true, nil)
	defer full.Close()
	if r := testMedpost(ctx, full.URL, "good"); !r.OK || !strings.Contains(r.Message, "both work") {
		t.Errorf("full: %+v", r)
	}
	if r := testMedpost(ctx, full.URL, "bad"); r.OK || !strings.Contains(r.Message, "did not accept the token") {
		t.Errorf("wrong token: %+v", r)
	}
	old := fakeMedpostAPI(t, "good", false, nil)
	defer old.Close()
	if r := testMedpost(ctx, old.URL, "good"); !r.OK || !strings.Contains(r.Message, "no activities list yet") {
		t.Errorf("old medpost: %+v", r)
	}
	if r := testMedpost(ctx, "http://127.0.0.1:1", "good"); r.OK || !strings.Contains(r.Message, "Could not reach") {
		t.Errorf("unreachable: %+v", r)
	}
	if r := testMedpost(ctx, full.URL, "good"); strings.Contains(r.Message, "good") && strings.Contains(r.Message, "Bearer") {
		t.Error("the message must never echo the token")
	}
}

func TestTestMedpostDoesNotFollowRedirectsWithTheToken(t *testing.T) {
	var leaked bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("Authorization") != ""
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()
	if r := testMedpost(context.Background(), redirector.URL, "tok"); r.OK {
		t.Errorf("a redirect must not count as connected: %+v", r)
	}
	if leaked {
		t.Error("the token was forwarded to the redirect target")
	}
}

func TestActivitiesClient(t *testing.T) {
	rows := []medpostActivity{{ActivityID: 1, Program: "Health Screening", Title: "Toolbox talk", Date: "2026-10-01"}}
	srv := fakeMedpostAPI(t, "tok", true, rows)
	defer srv.Close()
	got, err := newMedpostDirectory(srv.URL, "tok").Activities(context.Background(), 10)
	if err != nil || len(got) != 1 || got[0].Title != "Toolbox talk" {
		t.Errorf("got %+v, %v", got, err)
	}
	if _, err := newMedpostDirectory(srv.URL, "wrong").Activities(context.Background(), 10); err == nil || strings.Contains(err.Error(), "wrong") {
		t.Errorf("wrong token error = %v", err)
	}
	old := fakeMedpostAPI(t, "tok", false, nil)
	defer old.Close()
	if _, err := newMedpostDirectory(old.URL, "tok").Activities(context.Background(), 10); err != errNoActivitiesRoute {
		t.Errorf("missing route error = %v", err)
	}
}

func TestFilterActivities(t *testing.T) {
	rows := []medpostActivity{
		{Title: "a", Program: "P1", Date: "2026-10-05"},
		{Title: "b", Program: "P2", Date: "2026-10-01"},
		{Title: "c", Program: "P1", Date: "2026-01-02"},
	}
	titles := func(rs []medpostActivity) string {
		var s []string
		for _, r := range rs {
			s = append(s, r.Title)
		}
		return strings.Join(s, "")
	}
	for _, tc := range []struct{ from, to, program, want string }{
		{"", "", "", "abc"}, {"2026-10-01", "", "", "ab"}, {"", "2026-10-01", "", "bc"},
		{"2026-10-01", "2026-10-04", "", "b"}, {"", "", "P1", "ac"}, {"2026-10-01", "", "P2", "b"}, {"", "", "Nope", ""},
	} {
		if got := titles(filterActivities(rows, tc.from, tc.to, tc.program)); got != tc.want {
			t.Errorf("filter %+v = %q, want %q", tc, got, tc.want)
		}
	}
	if validDay("2026-10-01") == "" || validDay("yesterday") != "" || validDay("2026-13-40") != "" {
		t.Error("validDay wrong")
	}
	if p := programsOf(rows); len(p) != 2 || p[0] != "P1" {
		t.Errorf("programs = %v", p)
	}
}

func TestActivitiesCacheServesStaleOnFailure(t *testing.T) {
	rows := []medpostActivity{{ActivityID: 1, Title: "kept"}}
	srv := fakeMedpostAPI(t, "tok", true, rows)
	h := &activitiesHandlers{}
	d := newMedpostDirectory(srv.URL, "tok")
	if got, err := h.list(context.Background(), d); err != nil || len(got) != 1 {
		t.Fatalf("first fetch: %v %v", got, err)
	}
	srv.Close()
	h.at = time.Now().Add(-time.Hour) // expired, so it tries again and fails
	if got, err := h.list(context.Background(), d); err != nil || len(got) != 1 {
		t.Errorf("a failed refresh must serve the older rows: %v %v", got, err)
	}
	h.Reset()
	if _, err := h.list(context.Background(), d); err == nil {
		t.Error("after Reset there is nothing stale to serve")
	}
}
