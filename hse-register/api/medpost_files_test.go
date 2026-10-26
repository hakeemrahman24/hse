package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// filesMedpost answers like hse-medpost's activities, evidence and documents routes.
func filesMedpost(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/directory/activities":
			if r.URL.Query().Get("files") != "1" || r.URL.Query().Get("subheaders") != "1" {
				t.Error("the page must ask for the files and the sub-header entries with the list")
			}
			_, _ = w.Write([]byte(`[{"activityId":7,"program":"Health Screening","type":"Socialization","title":"Toolbox talk","summary":"Hand hygiene","date":"2026-10-01","location":"Camp",
				"photos":[{"evidenceId":11,"type":"Photos","name":"a b.jpg","date":"2026-10-01"}],"proof":[{"evidenceId":12,"type":"AttendanceList","name":"list.pdf","date":"2026-10-01"}]},
				{"activityId":9,"program":"Health Screening","type":"Socialization","title":"Sept resume","summary":"Monthly numbers","date":"2026-09-30","location":"","subHeaderId":5},
				{"activityId":8,"program":"Nutrition","type":"Monitoring/Inspection","title":"Kitchen check","summary":"","date":"2026-09-01","location":""}]`))
		case "/api/directory/subheaders":
			_, _ = w.Write([]byte(`[{"subHeaderId":5,"program":"Health Screening","name":"Monthly Resume"}]`))
		case "/api/directory/evidence/11":
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Content-Disposition", `inline; filename="a b.jpg"`)
			w.Header().Set("Set-Cookie", "leak=1")
			_, _ = w.Write([]byte("JPEG"))
		case "/api/directory/documents":
			_, _ = w.Write([]byte(`[{"documentId":3,"program":"Health Screening","type":"Procedure","title":"SOP","notes":"v2","formLink":"javascript:alert(1)","fileName":"sop.pdf"},{"documentId":4,"program":"Nutrition","type":"Forms","title":"Menu form","notes":"","formLink":"https://example.com/f","fileName":""}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func filesHandlers(t *testing.T, connected bool) *activitiesHandlers {
	t.Helper()
	loadTestTemplates()
	h := &activitiesHandlers{auth: &authContext{}, conn: &medpostConn{}}
	if connected {
		h.conn.dir = newMedpostDirectory(filesMedpost(t).URL, "tok")
	}
	return h
}

func get(h http.HandlerFunc, pattern, path string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+pattern, func(w http.ResponseWriter, r *http.Request) {
		h(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, &Session{Username: "budi", Role: roleUser})))
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestActivitiesPageShowsPhotosAndProofInline(t *testing.T) {
	h := filesHandlers(t, true)
	rec := get(h.show, "/activities", "/activities")
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, body)
	}
	for _, want := range []string{"Toolbox talk", "Kitchen check", "Health programs", `data-full="/activities/files/11/a%20b.jpg"`,
		`href="/activities/files/12/list.pdf"`, `download="list.pdf"`, "Socialization", "Monitoring/Inspection", "1 photo<", "0 photos"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, "Bearer") || strings.Contains(body, "127.0.0.1") {
		t.Error("page leaks the token or the Medpost address")
	}
}

func TestActivitiesPageFiltersByProgram(t *testing.T) {
	h := filesHandlers(t, true)
	body := get(h.show, "/activities", "/activities?program=Nutrition").Body.String()
	if !strings.Contains(body, "Kitchen check") || strings.Contains(body, "Toolbox talk") {
		t.Error("the program filter must keep only that program's activities")
	}
}

func TestReferenceTabListsDocumentsAndDropsUnsafeLinks(t *testing.T) {
	h := filesHandlers(t, true)
	body := get(h.show, "/activities", "/activities?tab=reference").Body.String()
	for _, want := range []string{"SOP", "Menu form", `href="/activities/documents/3/sop.pdf"`, `href="https://example.com/f"`, "Reference"} {
		if !strings.Contains(body, want) {
			t.Errorf("reference tab lacks %q", want)
		}
	}
	if strings.Contains(body, "javascript:") {
		t.Error("a javascript: form link was rendered")
	}
	one := get(h.show, "/activities", "/activities?tab=reference&program=Nutrition").Body.String()
	if strings.Contains(one, "SOP") || !strings.Contains(one, "Menu form") {
		t.Error("the reference tab must follow the program filter")
	}
}

func TestOldDocumentsAddressRedirectsToReferenceTab(t *testing.T) {
	rec := get(filesHandlers(t, false).documents, "/activities/documents", "/activities/documents")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/activities?tab=reference" {
		t.Errorf("%d %v", rec.Code, rec.Header())
	}
}

func TestActivitiesPageNotConnected(t *testing.T) {
	rec := get(filesHandlers(t, false).show, "/activities", "/activities")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "not connected to HSE Medpost") {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

func TestStreamPassesTheFileButNotCookiesOrToken(t *testing.T) {
	h := filesHandlers(t, true)
	rec := get(h.evidenceFile, "/activities/files/{eid}/{name}", "/activities/files/11/a%20b.jpg")
	if rec.Code != http.StatusOK || rec.Body.String() != "JPEG" {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
	hd := rec.Header()
	if hd.Get("Content-Type") != "image/jpeg" || !strings.HasPrefix(hd.Get("Content-Disposition"), "inline") {
		t.Errorf("headers %v", hd)
	}
	if hd.Get("Set-Cookie") != "" || hd.Get("X-Content-Type-Options") != "nosniff" || hd.Get("Content-Security-Policy") != "sandbox" {
		t.Errorf("unsafe headers %v", hd)
	}
	if rec := get(h.evidenceFile, "/activities/files/{eid}/{name}", "/activities/files/99/x"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown file: %d, want 404", rec.Code)
	}
	if rec := get(h.evidenceFile, "/activities/files/{eid}/{name}", "/activities/files/x/x"); rec.Code != http.StatusNotFound {
		t.Errorf("bad id: %d, want 404", rec.Code)
	}
	off := filesHandlers(t, false)
	if rec := get(off.evidenceFile, "/activities/files/{eid}/{name}", "/activities/files/11/x"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("not connected: %d, want 503", rec.Code)
	}
}

func TestSidebarShowsSubHeadersUnderTheirProgramAndKeepsEntriesOutOfTheMainList(t *testing.T) {
	h := filesHandlers(t, true)
	body := get(h.show, "/activities", "/activities").Body.String()
	for _, want := range []string{`class="obs-toggle" aria-expanded="false"`, `class="obs-subs" hidden`, `href="/activities?program=Health%20Screening&amp;sub=5"`, "Monthly Resume"} {
		if !strings.Contains(body, want) {
			t.Errorf("sidebar lacks %q", want)
		}
	}
	if strings.Contains(body, "Sept resume") {
		t.Error("a sub-header entry must not appear in the program list")
	}
	if strings.Count(body, `class="obs-toggle"`) != 1 || !strings.Contains(body, `class="obs-toggle-gap"`) {
		t.Error("only the program with sub-headers gets a triangle; the other keeps the gap")
	}
}

func TestSubHeaderPageShowsOnlyItsEntriesAndOpensItsProgram(t *testing.T) {
	h := filesHandlers(t, true)
	body := get(h.show, "/activities", "/activities?program=Health+Screening&sub=5").Body.String()
	if !strings.Contains(body, "Sept resume") || !strings.Contains(body, "Monthly numbers") {
		t.Error("the sub-header's entry is missing")
	}
	if strings.Contains(body, "Toolbox talk") || strings.Contains(body, "Kitchen check") {
		t.Error("only the sub-header's entries belong on its page")
	}
	if !strings.Contains(body, `aria-expanded="true"`) || !strings.Contains(body, `class="obs-sub active"`) {
		t.Error("the open sub-header's program must be expanded and the sub-header marked")
	}
	if !strings.Contains(body, "Monthly Resume") {
		t.Error("the page heading should be the sub-header name")
	}
}
