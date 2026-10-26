package directory

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// filesFixture seeds activity 1 with a real photo, a real PDF proof, a demo placeholder photo
// and one program document, and returns the API.
func filesFixture(t *testing.T) *API {
	t.Helper()
	database := openActivitiesTestDB(t)
	dir := t.TempDir()
	photo := filepath.Join(dir, "2026-10-01_photo.jpg")
	proof := filepath.Join(dir, "2026-10-01_list.pdf")
	doc := filepath.Join(dir, "2026-10-01_sop.docx")
	for path, body := range map[string]string{photo: "JPEGDATA", proof: "%PDF-1", doc: "DOCX"} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, stmt := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO EvidenceTable (EvidenceID, FK_IDActivity, FK_StaffID, EvidenceType, FileLink, UploadDate) VALUES (1,1,1,'Photos',?,'2026-10-01')`, []any{photo}},
		{`INSERT INTO EvidenceTable (EvidenceID, FK_IDActivity, FK_StaffID, EvidenceType, FileLink, UploadDate) VALUES (2,1,1,'AttendanceList',?,'2026-10-01')`, []any{proof}},
		{`INSERT INTO EvidenceTable (EvidenceID, FK_IDActivity, FK_StaffID, EvidenceType, FileLink, UploadDate) VALUES (3,1,1,'Photos','placeholder:demo','2026-10-01')`, nil},
		{`INSERT INTO DocumentTable (DocumentID, FK_ProcID, DocumentsType, DocumentTitle, FileLink, Notes) VALUES (1,1,'Procedure','Hand hygiene SOP',?,'v2')`, []any{doc}},
	} {
		if _, err := database.Exec(stmt.q, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	return &API{DB: database, Token: "secret", Now: fixedNow}
}

func serve(h http.HandlerFunc, path, pattern, token string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+pattern, h)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestFilesNeedTheToken(t *testing.T) {
	api := filesFixture(t)
	for _, c := range []struct {
		h             http.HandlerFunc
		path, pattern string
	}{
		{api.ActivityFiles, "/a/1/files", "/a/{id}/files"},
		{api.EvidenceFile, "/e/1", "/e/{id}"},
		{api.Documents, "/d", "/d"},
		{api.DocumentFile, "/d/1", "/d/{id}"},
	} {
		if rec := serve(c.h, c.path, c.pattern, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without token: %d, want 401", c.path, rec.Code)
		}
		if rec := serve(c.h, c.path, c.pattern, "wrong"); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s wrong token: %d, want 401", c.path, rec.Code)
		}
	}
}

func TestActivityFilesSplitsPhotosFromProofAndSkipsPlaceholders(t *testing.T) {
	api := filesFixture(t)
	rec := serve(api.ActivityFiles, "/a/1/files", "/a/{id}/files", "secret")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got ActivityFiles
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Photos) != 1 || got.Photos[0].Name != "2026-10-01_photo.jpg" || len(got.Proof) != 1 || got.Proof[0].Type != "AttendanceList" {
		t.Errorf("got %+v", got)
	}
	if strings.Contains(rec.Body.String(), "Nurse Secret") || strings.Contains(rec.Body.String(), os.TempDir()) {
		t.Errorf("answer leaks staff name or server path: %s", rec.Body)
	}
	if rec := serve(api.ActivityFiles, "/a/99/files", "/a/{id}/files", "secret"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown activity: %d, want 404", rec.Code)
	}
	if rec := serve(api.ActivityFiles, "/a/x/files", "/a/{id}/files", "secret"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id: %d, want 400", rec.Code)
	}
}

func TestEvidenceFileStreamsWithSafeHeaders(t *testing.T) {
	api := filesFixture(t)
	rec := serve(api.EvidenceFile, "/e/1", "/e/{id}", "secret")
	if rec.Code != http.StatusOK || rec.Body.String() != "JPEGDATA" {
		t.Fatalf("status %d body %q", rec.Code, rec.Body)
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Disposition"), "inline") ||
		rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Content-Security-Policy") != "sandbox" {
		t.Errorf("headers %v", rec.Header())
	}
	if rec := serve(api.EvidenceFile, "/e/3", "/e/{id}", "secret"); rec.Code != http.StatusNotFound {
		t.Errorf("placeholder: %d, want 404", rec.Code)
	}
	if rec := serve(api.EvidenceFile, "/e/99", "/e/{id}", "secret"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown: %d, want 404", rec.Code)
	}
}

func TestDocumentsListAndDownload(t *testing.T) {
	api := filesFixture(t)
	rec := serve(api.Documents, "/d", "/d", "secret")
	var docs []Document
	if err := json.Unmarshal(rec.Body.Bytes(), &docs); err != nil || len(docs) != 1 {
		t.Fatalf("docs %v err %v body %s", docs, err, rec.Body)
	}
	if docs[0].Title != "Hand hygiene SOP" || docs[0].Program != "Health Screening" || docs[0].FileName != "2026-10-01_sop.docx" {
		t.Errorf("doc %+v", docs[0])
	}
	if strings.Contains(rec.Body.String(), os.TempDir()) {
		t.Errorf("answer leaks a server path: %s", rec.Body)
	}
	file := serve(api.DocumentFile, "/d/1", "/d/{id}", "secret")
	if file.Code != http.StatusOK || !strings.HasPrefix(file.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("docx: %d %v", file.Code, file.Header())
	}
}

func TestActivitiesWithFilesAttachesPhotosAndProofOnlyWhenAsked(t *testing.T) {
	api := filesFixture(t)
	code, plain, _ := getActivities(t, api, "", "secret")
	if code != 200 || len(plain[0].Photos) != 0 || len(plain[0].Proof) != 0 {
		t.Fatalf("without ?files=1 no files expected: %d %+v", code, plain)
	}
	code, rows, body := getActivities(t, api, "?files=1", "secret")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var toolbox Activity
	for _, r := range rows {
		if r.ActivityID == 1 {
			toolbox = r
		}
	}
	if len(toolbox.Photos) != 1 || toolbox.Photos[0].Name != "2026-10-01_photo.jpg" || len(toolbox.Proof) != 1 {
		t.Errorf("activity 1 files = %+v", toolbox)
	}
	if strings.Contains(body, os.TempDir()) || strings.Contains(body, "Nurse Secret") {
		t.Errorf("answer leaks a path or staff name: %s", body)
	}
}

func TestSubHeadersAndTheirEntries(t *testing.T) {
	api := filesFixture(t)
	for _, q := range []string{
		`INSERT INTO SubHeaderTable (SubHeaderID, FK_ProcID, SubHeaderName, Status) VALUES (1, 1, 'Monthly Resume', 'Active')`,
		`INSERT INTO SubHeaderTable (SubHeaderID, FK_ProcID, SubHeaderName, Status) VALUES (2, 1, 'Old Resume', 'Archived')`,
		`INSERT INTO ActivityTable (ActivityID, FK_ProcID, FK_StaffID, ActivityType, ActivityTitle, ActivityDate, FK_SubHeaderID) VALUES (10, 1, 1, 'Socialization', 'Sept resume', '2026-09-30', 1)`,
		`INSERT INTO ActivityTable (ActivityID, FK_ProcID, FK_StaffID, ActivityType, ActivityTitle, ActivityDate, FK_SubHeaderID) VALUES (11, 1, 1, 'Socialization', 'Archived entry', '2026-09-01', 2)`,
	} {
		if _, err := api.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	rec := serve(api.SubHeaders, "/s", "/s", "secret")
	var subs []SubHeader
	if err := json.Unmarshal(rec.Body.Bytes(), &subs); err != nil || len(subs) != 1 || subs[0].Name != "Monthly Resume" || subs[0].Program != "Health Screening" {
		t.Fatalf("sub-headers %v err %v body %s", subs, err, rec.Body)
	}
	if rec := serve(api.SubHeaders, "/s", "/s", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", rec.Code)
	}
	_, plain, _ := getActivities(t, api, "", "secret")
	for _, a := range plain {
		if a.SubHeaderID != 0 {
			t.Errorf("default list must not carry sub-header entries: %+v", a)
		}
	}
	_, rows, _ := getActivities(t, api, "?subheaders=1", "secret")
	var got []string
	for _, a := range rows {
		if a.SubHeaderID != 0 {
			got = append(got, a.Title)
		}
	}
	if len(got) != 1 || got[0] != "Sept resume" {
		t.Errorf("sub-header entries = %v, want only the active sub-header's entry", got)
	}
}
