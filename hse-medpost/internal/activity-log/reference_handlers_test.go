package activitylog

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"hse-medpost/internal/auth"
	"hse-medpost/internal/middleware"
)

// referenceServer wires the real Reference routes (with their real permission
// wrappers) and signs every request in as the given role, so these tests check
// the admin rule the way the running server applies it.
func referenceServer(t *testing.T, a *API, role string) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	a.RegisterReferenceRoutes(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if role != "" {
			r = r.WithContext(middleware.ContextWithSession(r.Context(), &auth.Session{StaffID: 1, Role: role}))
		}
		mux.ServeHTTP(w, r)
	})
}

func do(t *testing.T, h http.Handler, method, path string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func multipartBody(t *testing.T, fields map[string]string, fileName string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if fileName != "" {
		part, err := w.CreateFormFile("file", fileName)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return out
}

func addFileViaAPI(t *testing.T, h http.Handler, section, title, content string) *httptest.ResponseRecorder {
	t.Helper()
	body, ct := multipartBody(t, map[string]string{
		"kind": "file", "documentsType": "Forms", "procId": "1", "documentTitle": title,
	}, title+".pdf", []byte(content))
	return do(t, h, http.MethodPost, "/api/reference/"+section+"/documents", body, ct)
}

func TestReferenceRoutesNeedSignInAndAdminForChanges(t *testing.T) {
	a := newReferenceAPI(t)
	id := mustAddDoc(t, a, "SOP", "sop")
	_ = a.AddDocumentToSection(id, "malaria", 1)

	if rec := do(t, referenceServer(t, a, ""), http.MethodGet, "/api/reference/malaria", nil, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("signed out list: %d", rec.Code)
	}

	staff := referenceServer(t, a, "staff")
	rec := do(t, staff, http.MethodGet, "/api/reference/malaria", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("staff list: %d %s", rec.Code, rec.Body)
	}
	if got := decode(t, rec); got["canManage"] != false || len(got["items"].([]any)) != 1 {
		t.Fatalf("staff view = %v", got)
	}

	body, ct := multipartBody(t, map[string]string{"kind": "link", "documentTitle": "x"}, "", nil)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/reference/malaria/library"},
		{http.MethodPost, "/api/reference/malaria/documents"},
		{http.MethodPost, "/api/reference/malaria/pick"},
		{http.MethodDelete, "/api/reference/malaria/documents/1"},
		{http.MethodDelete, "/api/reference/documents/1"},
		{http.MethodPost, "/api/reference/documents/1/file"},
		{http.MethodGet, "/api/reference/documents/1/versions"},
		{http.MethodGet, "/api/reference/documents/1/versions/1/file"},
	} {
		rec := do(t, staff, c.method, c.path, bytes.NewReader(body.Bytes()), ct)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("staff %s %s = %d, want 403", c.method, c.path, rec.Code)
		}
	}
	var n int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM DocumentTable`).Scan(&n)
	if n != 1 {
		t.Fatal("a refused staff request changed the library")
	}
}

func TestAdminAddsAFileToATabAndItAppearsThere(t *testing.T) {
	a := newReferenceAPI(t)
	admin := referenceServer(t, a, "admin")

	rec := addFileViaAPI(t, admin, "malaria", "Malaria SOP", "sop bytes")
	if rec.Code != http.StatusCreated {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}

	got := decode(t, do(t, admin, http.MethodGet, "/api/reference/malaria", nil, ""))
	items := got["items"].([]any)
	if got["canManage"] != true || len(items) != 1 || items[0].(map[string]any)["title"] != "Malaria SOP" {
		t.Fatalf("tab = %v", got)
	}
	if _, leaked := items[0].(map[string]any)["fileLink"]; leaked {
		t.Fatal("the stored path must never reach the browser")
	}
	// The same file again, in another section, is the duplicate the owner wants refused.
	dup := addFileViaAPI(t, admin, "fatigue", "Another name", "sop bytes")
	if dup.Code != http.StatusConflict {
		t.Fatalf("duplicate: %d %s", dup.Code, dup.Body)
	}
	if body := decode(t, dup); !strings.Contains(body["error"].(string), "already in Reference") ||
		body["duplicateOf"].(map[string]any)["title"] != "Malaria SOP" {
		t.Fatalf("duplicate body = %v", body)
	}
}

func TestAdminAddsALinkAndBadLinksAreRefused(t *testing.T) {
	a := newReferenceAPI(t)
	admin := referenceServer(t, a, "admin")
	add := func(link string) int {
		body, ct := multipartBody(t, map[string]string{
			"kind": "link", "documentsType": "Procedure", "procId": "1", "documentTitle": "WHO", "formLink": link,
		}, "", nil)
		return do(t, admin, http.MethodPost, "/api/reference/malaria/documents", body, ct).Code
	}
	if code := add("https://example.org/who"); code != http.StatusCreated {
		t.Fatalf("good link: %d", code)
	}
	if code := add("javascript:alert(1)"); code != http.StatusBadRequest {
		t.Fatalf("javascript link: %d", code)
	}
}

func TestAddRejectsBadInputWithoutCreatingAnything(t *testing.T) {
	a := newReferenceAPI(t)
	admin := referenceServer(t, a, "admin")

	cases := []struct {
		name    string
		section string
		fields  map[string]string
		file    string
		want    int
	}{
		{"unknown section", "dentistry", map[string]string{"kind": "file", "documentsType": "Forms", "procId": "1", "documentTitle": "A"}, "a.pdf", http.StatusNotFound},
		{"unknown kind", "malaria", map[string]string{"kind": "zip", "documentsType": "Forms", "procId": "1", "documentTitle": "A"}, "a.pdf", http.StatusBadRequest},
		{"file kind without a file", "malaria", map[string]string{"kind": "file", "documentsType": "Forms", "procId": "1", "documentTitle": "A"}, "", http.StatusBadRequest},
		{"no program for Forms", "malaria", map[string]string{"kind": "file", "documentsType": "Forms", "documentTitle": "A"}, "a.pdf", http.StatusBadRequest},
		{"bad program id", "malaria", map[string]string{"kind": "file", "documentsType": "Forms", "procId": "abc", "documentTitle": "A"}, "a.pdf", http.StatusBadRequest},
		{"wrong file type", "malaria", map[string]string{"kind": "file", "documentsType": "Forms", "procId": "1", "documentTitle": "A"}, "run.exe", http.StatusBadRequest},
		{"blank title", "malaria", map[string]string{"kind": "file", "documentsType": "Forms", "procId": "1", "documentTitle": " "}, "a.pdf", http.StatusBadRequest},
	}
	for _, c := range cases {
		body, ct := multipartBody(t, c.fields, c.file, []byte("bytes"))
		if rec := do(t, admin, http.MethodPost, "/api/reference/"+c.section+"/documents", body, ct); rec.Code != c.want {
			t.Fatalf("%s: %d %s, want %d", c.name, rec.Code, rec.Body, c.want)
		}
	}
	var n int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM DocumentTable`).Scan(&n)
	if n != 0 || len(filesUnder(t, a.UploadDir)) != 0 {
		t.Fatalf("rejected requests left %d documents and %v", n, filesUnder(t, a.UploadDir))
	}
}

func TestUploadBiggerThanTheRequestLimitIsRefusedWith413(t *testing.T) {
	a := newReferenceAPI(t)
	admin := referenceServer(t, a, "admin")
	big := bytes.Repeat([]byte{0}, MaxReferenceFileBytes+2<<20) // well past the cap plus form overhead
	body, ct := multipartBody(t, map[string]string{
		"kind": "file", "documentsType": "Forms", "procId": "1", "documentTitle": "Huge",
	}, "huge.pdf", big)

	rec := do(t, admin, http.MethodPost, "/api/reference/malaria/documents", body, ct)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413", rec.Code)
	}
	if n := len(filesUnder(t, a.UploadDir)); n != 0 {
		t.Fatalf("an oversize upload left %d files", n)
	}
}

func TestAdminPicksAnExistingDocumentInsteadOfUploadingAgain(t *testing.T) {
	a := newReferenceAPI(t)
	admin := referenceServer(t, a, "admin")
	id := mustAddDoc(t, a, "Shared SOP", "shared")

	lib := decode(t, do(t, admin, http.MethodGet, "/api/reference/malaria/library", nil, ""))
	items := lib["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["inSection"] != false {
		t.Fatalf("library = %v", lib)
	}

	pick := func() int {
		b, _ := json.Marshal(map[string]int64{"documentId": id})
		return do(t, admin, http.MethodPost, "/api/reference/malaria/pick", bytes.NewReader(b), "application/json").Code
	}
	if code := pick(); code != http.StatusCreated {
		t.Fatalf("pick: %d", code)
	}
	if code := pick(); code != http.StatusConflict {
		t.Fatalf("pick twice: %d, want 409", code)
	}
	var docs int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM DocumentTable`).Scan(&docs)
	if docs != 1 || len(filesUnder(t, a.UploadDir)) != 1 {
		t.Fatal("picking must not copy the document or its file")
	}
}

func TestReplaceFileAndDownloadAnOldVersion(t *testing.T) {
	a := newReferenceAPI(t)
	admin := referenceServer(t, a, "admin")
	id := mustAddDoc(t, a, "Intake form", "first")

	body, ct := multipartBody(t, nil, "intake-v2.pdf", []byte("second"))
	rec := do(t, admin, http.MethodPost, "/api/reference/documents/"+itoa(id)+"/file", body, ct)
	if rec.Code != http.StatusCreated || decode(t, rec)["versionNo"] != float64(2) {
		t.Fatalf("replace: %d %s", rec.Code, rec.Body)
	}
	dup, dct := multipartBody(t, nil, "again.pdf", []byte("first"))
	if rec := do(t, admin, http.MethodPost, "/api/reference/documents/"+itoa(id)+"/file", dup, dct); rec.Code != http.StatusConflict {
		t.Fatalf("replace with old bytes: %d, want 409", rec.Code)
	}

	hist := decode(t, do(t, admin, http.MethodGet, "/api/reference/documents/"+itoa(id)+"/versions", nil, ""))
	if len(hist["versions"].([]any)) != 2 {
		t.Fatalf("history = %v", hist)
	}
	old := do(t, admin, http.MethodGet, "/api/reference/documents/"+itoa(id)+"/versions/1/file", nil, "")
	if old.Code != http.StatusOK || old.Body.String() != "first" {
		t.Fatalf("old version: %d %q", old.Code, old.Body)
	}
	if cd := old.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Fatalf("Content-Disposition = %q, want attachment", cd)
	}
	if old.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing nosniff")
	}
	if rec := do(t, admin, http.MethodGet, "/api/reference/documents/"+itoa(id)+"/versions/9/file", nil, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown version: %d", rec.Code)
	}
}

func TestRemoveFromTabAndDeleteRespectSharing(t *testing.T) {
	a := newReferenceAPI(t)
	admin := referenceServer(t, a, "admin")
	id := mustAddDoc(t, a, "Shared SOP", "shared")
	_ = a.AddDocumentToSection(id, "malaria", 1)
	_ = a.AddDocumentToSection(id, "fatigue", 1)

	rec := do(t, admin, http.MethodDelete, "/api/reference/documents/"+itoa(id)+"?section=malaria", nil, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete while shared: %d %s", rec.Code, rec.Body)
	}
	if got := decode(t, rec)["sections"].([]any); len(got) != 1 || got[0] != "fatigue" {
		t.Fatalf("sections = %v", got)
	}

	if rec := do(t, admin, http.MethodDelete, "/api/reference/malaria/documents/"+itoa(id), nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("remove from tab: %d", rec.Code)
	}
	if rec := do(t, admin, http.MethodDelete, "/api/reference/malaria/documents/"+itoa(id), nil, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("remove twice: %d", rec.Code)
	}
	if rec := do(t, admin, http.MethodDelete, "/api/reference/documents/"+itoa(id)+"?section=fatigue", nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("delete once only fatigue shows it: %d", rec.Code)
	}
	if files := filesUnder(t, a.UploadDir); len(files) != 0 {
		t.Fatalf("files left: %v", files)
	}
	if _, err := os.Stat(a.UploadDir); err != nil {
		t.Fatal(err)
	}
}
