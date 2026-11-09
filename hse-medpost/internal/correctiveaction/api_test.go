package correctiveaction

import (
	"bytes"
	"encoding/json"
	"image"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hse-medpost/internal/auth"
	"hse-medpost/internal/middleware"
)

// apiFixture is the store fixture plus a real mux wired the way cmd/server does it, with a fake sign-in:
// the wrapper attaches whichever session the test sets, and the admin wrapper rejects non-admins.
type apiFixture struct {
	fixture
	api  *API
	mux  *http.ServeMux
	role *string
}

func newAPIFixture(t *testing.T) apiFixture {
	t.Helper()
	f := newFixture(t)
	role := "staff"
	api := &API{Store: f.store, UploadDir: t.TempDir()}
	sign := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sess := &auth.Session{StaffID: f.staffID, Name: "Nurse A", Role: role}
			h.ServeHTTP(w, r.WithContext(middleware.ContextWithSession(r.Context(), sess)))
		})
	}
	admin := func(h http.Handler) http.Handler {
		return sign(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if middleware.Session(r).Role != "admin" {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			h.ServeHTTP(w, r)
		}))
	}
	mux := http.NewServeMux()
	api.Register(mux, sign, admin)
	return apiFixture{fixture: f, api: api, mux: mux, role: &role}
}

func (f apiFixture) do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, httptest.NewRequest(method, path, reader))
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("response is not the expected JSON (%v): %s", err, rec.Body.String())
	}
	return v
}

func (f apiFixture) createBody() map[string]any {
	return map[string]any{
		"procId": f.procID, "inspectionDate": "2026-11-01", "location": "Mess hall",
		"finding": "Food stored on the floor", "recommendation": "Use shelves",
		"picId": f.patientID, "dueDate": "2026-11-20", "severity": "Major",
	}
}

func (f apiFixture) createViaAPI(t *testing.T) Action {
	t.Helper()
	rec := f.do(t, "POST", "/api/corrective-actions", f.createBody())
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	return decode[Action](t, rec)
}

func tinyJPEG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 32, 24)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func (f apiFixture) upload(t *testing.T, id int64, phase string, files ...[]byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if phase != "" {
		w.WriteField("phase", phase)
	}
	for i, data := range files {
		part, err := w.CreateFormFile("photos", "p"+string(rune('a'+i))+".jpg")
		if err != nil {
			t.Fatal(err)
		}
		part.Write(data)
	}
	w.Close()
	req := httptest.NewRequest("POST", "/api/corrective-actions/"+itoa(id)+"/photos", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func itoa(id int64) string { return idText(id) }

func TestAPICreateGetAndList(t *testing.T) {
	f := newAPIFixture(t)
	created := f.createViaAPI(t)
	if created.PicName != "Budi Santoso" || created.Status != "Open" || created.CreatedByName != "Nurse A" {
		t.Fatalf("created = %+v", created)
	}

	if rec := f.do(t, "GET", "/api/corrective-actions/"+itoa(created.ID), nil); rec.Code != 200 || decode[Action](t, rec).Finding != "Food stored on the floor" {
		t.Fatalf("get = %d %s", rec.Code, rec.Body.String())
	}
	rec := f.do(t, "GET", "/api/corrective-actions?status=Open&q=floor&procId="+itoa(f.procID), nil)
	if list := decode[[]Action](t, rec); rec.Code != 200 || len(list) != 1 {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do(t, "GET", "/api/corrective-actions?status=Closed", nil); len(decode[[]Action](t, rec)) != 0 {
		t.Fatalf("closed filter returned rows: %s", rec.Body.String())
	}
	if rec := f.do(t, "GET", "/api/corrective-actions/9999", nil); rec.Code != 404 {
		t.Fatalf("unknown id = %d", rec.Code)
	}
}

func TestAPIRejectsBadRequests(t *testing.T) {
	f := newAPIFixture(t)
	body := f.createBody()
	body["picId"] = 9999
	rec := f.do(t, "POST", "/api/corrective-actions", body)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "employee list") {
		t.Fatalf("unlisted PIC = %d %s, want a 400 that says to pick from the list", rec.Code, rec.Body.String())
	}
	req := httptest.NewRequest("POST", "/api/corrective-actions", strings.NewReader("{not json"))
	bad := httptest.NewRecorder()
	f.mux.ServeHTTP(bad, req)
	if bad.Code != 400 {
		t.Fatalf("bad JSON = %d", bad.Code)
	}
	if rec := f.do(t, "GET", "/api/corrective-actions/abc", nil); rec.Code != 400 {
		t.Fatalf("non-numeric id = %d", rec.Code)
	}
	if rec := f.do(t, "GET", "/api/corrective-actions?procId=x", nil); rec.Code != 400 {
		t.Fatalf("bad procId = %d", rec.Code)
	}
}

func TestAPIUpdateKeepsPicWhenNotResent(t *testing.T) {
	f := newAPIFixture(t)
	created := f.createViaAPI(t)
	body := f.createBody()
	delete(body, "picId")
	body["finding"] = "Food and chemicals on the floor"
	rec := f.do(t, "PUT", "/api/corrective-actions/"+itoa(created.ID), body)
	if got := decode[Action](t, rec); rec.Code != 200 || got.PicName != "Budi Santoso" || got.Finding != "Food and chemicals on the floor" {
		t.Fatalf("update = %d %+v", rec.Code, got)
	}
	hist := decode[[]HistoryEntry](t, f.do(t, "GET", "/api/corrective-actions/"+itoa(created.ID)+"/history", nil))
	if len(hist) != 2 || hist[1].Field != "finding" {
		t.Fatalf("history = %+v", hist)
	}
}

func TestAPIEmployeeSearch(t *testing.T) {
	f := newAPIFixture(t)
	rec := f.do(t, "GET", "/api/corrective-actions/employees?q=budi", nil)
	got := decode[[]Employee](t, rec)
	if rec.Code != 200 || len(got) != 2 {
		t.Fatalf("employees = %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "dateOfBirth") || strings.Contains(rec.Body.String(), "bloodType") {
		t.Fatalf("the PIC search must not leak medical fields: %s", rec.Body.String())
	}
}

func TestAPIPhotoUploadCloseAndServe(t *testing.T) {
	f := newAPIFixture(t)
	created := f.createViaAPI(t)
	closeBody := map[string]any{"closingDate": "2026-11-10", "actionTaken": "Shelves installed"}

	if rec := f.do(t, "POST", "/api/corrective-actions/"+itoa(created.ID)+"/close", closeBody); rec.Code != 400 {
		t.Fatalf("closing without an After photo = %d, want 400", rec.Code)
	}
	rec := f.upload(t, created.ID, "After", tinyJPEG(t))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d %s", rec.Code, rec.Body.String())
	}
	withPhoto := decode[Action](t, rec)
	if len(withPhoto.AfterPhotos) != 1 || !strings.HasPrefix(withPhoto.AfterPhotos[0].URL, "/api/corrective-action-photos/") {
		t.Fatalf("after photos = %+v", withPhoto.AfterPhotos)
	}
	if strings.Contains(rec.Body.String(), f.api.UploadDir) {
		t.Fatalf("the response leaks a server path: %s", rec.Body.String())
	}
	if rec := f.do(t, "POST", "/api/corrective-actions/"+itoa(created.ID)+"/close", closeBody); rec.Code != 200 || decode[Action](t, rec).Status != "Closed" {
		t.Fatalf("close = %d %s", rec.Code, rec.Body.String())
	}

	served := f.do(t, "GET", withPhoto.AfterPhotos[0].URL, nil)
	if served.Code != 200 || served.Header().Get("Content-Type") != "image/jpeg" || served.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("serve = %d %v", served.Code, served.Header())
	}
	if _, err := jpeg.Decode(bytes.NewReader(served.Body.Bytes())); err != nil {
		t.Fatalf("served bytes are not a JPEG: %v", err)
	}
	if rec := f.do(t, "GET", "/api/corrective-action-photos/9999", nil); rec.Code != 404 {
		t.Fatalf("unknown photo = %d", rec.Code)
	}
}

func TestAPIShowsTheFriendlyProgramNameButFoldersByTheStoredOne(t *testing.T) {
	f := newAPIFixture(t)
	res, err := f.store.DB.Exec(`INSERT INTO HealthProcedureTable (HealthProcedure) VALUES ('DrugAndAlcoholTesting')`)
	if err != nil {
		t.Fatal(err)
	}
	procID, _ := res.LastInsertId()
	body := f.createBody()
	body["procId"] = procID
	rec := f.do(t, "POST", "/api/corrective-actions", body)
	created := decode[Action](t, rec)
	if created.ProgramName != "Drug And Alcohol Testing" {
		t.Fatalf("program name = %q, want the friendly title", created.ProgramName)
	}
	if strings.Contains(rec.Body.String(), "DrugAndAlcoholTesting") {
		t.Fatalf("the stored program key must not be sent to the browser: %s", rec.Body.String())
	}
	f.upload(t, created.ID, "Before", tinyJPEG(t))
	found := false
	filepath.WalkDir(f.api.UploadDir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.Contains(path, "DrugAndAlcoholTesting") && strings.Contains(path, "corrective-actions") {
			found = true
		}
		return nil
	})
	if !found {
		t.Fatal("the photo must be stored under the program's stored-name folder, beside its other evidence")
	}
}

func TestAPIThumbnailIsSmallerThanTheOriginal(t *testing.T) {
	f := newAPIFixture(t)
	created := f.createViaAPI(t)
	big := jpegBytes(t, noisyImage(900, 600), 80)
	if len(big) > MaxPhotoBytes {
		t.Fatalf("test setup: %d bytes would be re-encoded on upload", len(big))
	}
	withPhoto := decode[Action](t, f.upload(t, created.ID, "Before", big))
	url := withPhoto.BeforePhotos[0].URL

	full := f.do(t, "GET", url, nil)
	thumb := f.do(t, "GET", url+"?thumb=1", nil)
	if thumb.Code != 200 || thumb.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("thumbnail = %d %v", thumb.Code, thumb.Header())
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(thumb.Body.Bytes()))
	if err != nil || cfg.Width != ThumbnailSide || cfg.Height != ThumbnailSide*600/900 {
		t.Fatalf("thumbnail is %dx%d (%v), want %dx%d", cfg.Width, cfg.Height, err, ThumbnailSide, ThumbnailSide*600/900)
	}
	if thumb.Body.Len() >= full.Body.Len() {
		t.Fatalf("thumbnail %d bytes is not smaller than the original %d", thumb.Body.Len(), full.Body.Len())
	}
}

func TestAPIUploadRejectsBadPhotos(t *testing.T) {
	f := newAPIFixture(t)
	created := f.createViaAPI(t)
	cases := []struct {
		name  string
		phase string
		files [][]byte
	}{
		{"not an image", "Before", [][]byte{[]byte("hello")}},
		{"no files", "Before", nil},
		{"bad phase", "Sideways", [][]byte{tinyJPEG(t)}},
		{"missing phase", "", [][]byte{tinyJPEG(t)}},
		{"four files at once", "Before", [][]byte{tinyJPEG(t), tinyJPEG(t), tinyJPEG(t), tinyJPEG(t)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if rec := f.upload(t, created.ID, c.phase, c.files...); rec.Code != 400 {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
	if rec := f.upload(t, 9999, "Before", tinyJPEG(t)); rec.Code != 404 {
		t.Fatalf("unknown finding = %d", rec.Code)
	}
}

func TestAPIFourthPhotoIsRefusedAndLeavesNoFileBehind(t *testing.T) {
	f := newAPIFixture(t)
	created := f.createViaAPI(t)
	if rec := f.upload(t, created.ID, "Before", tinyJPEG(t), tinyJPEG(t), tinyJPEG(t)); rec.Code != http.StatusCreated {
		t.Fatalf("first three = %d %s", rec.Code, rec.Body.String())
	}
	filesBefore := countFiles(t, f.api.UploadDir)
	if rec := f.upload(t, created.ID, "Before", tinyJPEG(t)); rec.Code != 400 {
		t.Fatalf("fourth photo = %d, want 400", rec.Code)
	}
	if after := countFiles(t, f.api.UploadDir); after != filesBefore {
		t.Fatalf("files on disk = %d, want %d: a refused photo must not stay behind", after, filesBefore)
	}
}

func countFiles(t *testing.T, root string) int {
	t.Helper()
	n := 0
	filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

func TestAPIDeletePhotoRemovesTheFile(t *testing.T) {
	f := newAPIFixture(t)
	created := f.createViaAPI(t)
	withPhoto := decode[Action](t, f.upload(t, created.ID, "Before", tinyJPEG(t)))
	photoURL := withPhoto.BeforePhotos[0].URL
	if countFiles(t, f.api.UploadDir) != 2 {
		t.Fatal("expected one stored photo and its small copy")
	}
	if rec := f.do(t, "DELETE", photoURL, nil); rec.Code != 200 {
		t.Fatalf("delete photo = %d %s", rec.Code, rec.Body.String())
	}
	if countFiles(t, f.api.UploadDir) != 0 {
		t.Fatal("the photo file was not removed from disk")
	}
}

func TestAPIDeleteFindingIsAdminOnly(t *testing.T) {
	f := newAPIFixture(t)
	created := f.createViaAPI(t)
	f.upload(t, created.ID, "Before", tinyJPEG(t))

	if rec := f.do(t, "DELETE", "/api/corrective-actions/"+itoa(created.ID), nil); rec.Code != http.StatusForbidden {
		t.Fatalf("staff delete = %d, want 403", rec.Code)
	}
	if rec := f.do(t, "GET", "/api/corrective-actions/"+itoa(created.ID), nil); rec.Code != 200 {
		t.Fatal("the finding must still exist after a refused delete")
	}
	*f.role = "admin"
	if rec := f.do(t, "DELETE", "/api/corrective-actions/"+itoa(created.ID), nil); rec.Code != 200 {
		t.Fatalf("admin delete = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do(t, "GET", "/api/corrective-actions/"+itoa(created.ID), nil); rec.Code != 404 {
		t.Fatalf("after delete = %d, want 404", rec.Code)
	}
	if n := countFiles(t, f.api.UploadDir); n != 0 {
		t.Fatalf("%d photo files left on disk after delete", n)
	}
}

func TestAPIRefusesToServeOrDeleteFilesOutsideTheUploadFolder(t *testing.T) {
	f := newAPIFixture(t)
	created := f.createViaAPI(t)
	outside := filepath.Join(t.TempDir(), "secret.jpg")
	if err := os.WriteFile(outside, []byte("top secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	photoID, err := f.store.AddPhoto(created.ID, "Before", outside, f.staffID)
	if err != nil {
		t.Fatal(err)
	}
	if rec := f.do(t, "GET", "/api/corrective-action-photos/"+itoa(photoID), nil); rec.Code != 404 || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("serve outside file = %d %s", rec.Code, rec.Body.String())
	}
	f.do(t, "DELETE", "/api/corrective-action-photos/"+itoa(photoID), nil)
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("a file outside the upload folder was deleted")
	}
}

func TestAPIFollowUpFlow(t *testing.T) {
	f := newAPIFixture(t)
	created := f.createViaAPI(t)
	base := "/api/corrective-actions/" + itoa(created.ID)

	rec := f.do(t, "POST", base+"/follow-ups", map[string]any{"description": "Buy shelves", "picId": f.patient2ID, "dueDate": "2026-11-15"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("add follow-up = %d %s", rec.Code, rec.Body.String())
	}
	got := decode[Action](t, rec)
	if len(got.FollowUps) != 1 || got.FollowUps[0].PicName != "Budiman Hakim" {
		t.Fatalf("follow-ups = %+v", got.FollowUps)
	}
	fuPath := "/api/corrective-action-follow-ups/" + itoa(got.FollowUps[0].ID)

	if rec := f.do(t, "POST", fuPath+"/done", map[string]any{"doneDate": "2026-11-05", "actionTaken": ""}); rec.Code != 400 {
		t.Fatalf("done without action taken = %d", rec.Code)
	}
	if rec := f.do(t, "POST", fuPath+"/done", map[string]any{"doneDate": "2026-11-05", "actionTaken": "Bought"}); rec.Code != 200 {
		t.Fatalf("done = %d %s", rec.Code, rec.Body.String())
	}
	if status := decode[Action](t, f.do(t, "GET", base, nil)).Status; status != "In Progress" {
		t.Fatalf("finding status = %s, want In Progress", status)
	}
	if rec := f.do(t, "POST", fuPath+"/reopen", nil); rec.Code != 200 {
		t.Fatalf("reopen follow-up = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do(t, "PUT", fuPath, map[string]any{"description": "Buy steel shelves", "dueDate": "2026-11-18"}); rec.Code != 200 {
		t.Fatalf("update follow-up = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do(t, "DELETE", fuPath, nil); rec.Code != 200 {
		t.Fatalf("delete follow-up = %d", rec.Code)
	}
	if rec := f.do(t, "DELETE", fuPath, nil); rec.Code != 404 {
		t.Fatalf("second delete = %d, want 404", rec.Code)
	}
}

func TestAPIReopenFinding(t *testing.T) {
	f := newAPIFixture(t)
	created := f.createViaAPI(t)
	base := "/api/corrective-actions/" + itoa(created.ID)
	if rec := f.do(t, "POST", base+"/reopen", nil); rec.Code != 400 {
		t.Fatalf("reopening an open finding = %d, want 400", rec.Code)
	}
	f.upload(t, created.ID, "After", tinyJPEG(t))
	f.do(t, "POST", base+"/close", map[string]any{"closingDate": "2026-11-10", "actionTaken": "Done"})
	if rec := f.do(t, "POST", base+"/reopen", nil); rec.Code != 200 || decode[Action](t, rec).Status != "Open" {
		t.Fatalf("reopen = %d %s", rec.Code, rec.Body.String())
	}
}
