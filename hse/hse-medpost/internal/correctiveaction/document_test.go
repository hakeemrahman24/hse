package correctiveaction

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
)

// minimalPDF is enough for the "%PDF-" sniff; the server never parses a document, it only stores it.
var minimalPDF = []byte("%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF\n")

func (f apiFixture) uploadDocs(t *testing.T, id int64, files map[string][]byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for name, data := range files {
		part, err := w.CreateFormFile("documents", name)
		if err != nil {
			t.Fatal(err)
		}
		part.Write(data)
	}
	w.Close()
	req := httptest.NewRequest("POST", "/api/corrective-actions/"+strconv.FormatInt(id, 10)+"/documents", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func TestAPIDocumentUploadServeAndDelete(t *testing.T) {
	f := newAPIFixture(t)
	action := f.createViaAPI(t)

	rec := f.uploadDocs(t, action.ID, map[string][]byte{"Work Order.pdf": minimalPDF})
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d: %s", rec.Code, rec.Body.String())
	}
	got := decode[Action](t, rec)
	if len(got.Documents) != 1 || got.Documents[0].FileName != "Work Order.pdf" || got.Documents[0].URL == "" {
		t.Fatalf("documents = %+v", got.Documents)
	}

	served := f.do(t, "GET", got.Documents[0].URL, nil)
	if served.Code != 200 || served.Header().Get("Content-Type") != "application/pdf" ||
		served.Header().Get("Content-Security-Policy") != "" || !bytes.HasPrefix(served.Body.Bytes(), []byte("%PDF-")) {
		t.Fatalf("serve = %d %v", served.Code, served.Header())
	}

	path, _, err := f.store.DocumentPath(got.Documents[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if del := f.do(t, "DELETE", got.Documents[0].URL, nil); del.Code != 200 {
		t.Fatalf("delete = %d: %s", del.Code, del.Body.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the PDF file should be gone, stat err = %v", err)
	}
}

func TestAPIDocumentRejectsNonPDFAndTooMany(t *testing.T) {
	f := newAPIFixture(t)
	action := f.createViaAPI(t)

	if rec := f.uploadDocs(t, action.ID, map[string][]byte{"a.pdf": []byte("<html>not a pdf</html>")}); rec.Code != http.StatusBadRequest {
		t.Fatalf("non-PDF = %d, want 400", rec.Code)
	}
	for i := 0; i < MaxDocumentsPerAction; i++ {
		if rec := f.uploadDocs(t, action.ID, map[string][]byte{"d.pdf": minimalPDF}); rec.Code != http.StatusCreated {
			t.Fatalf("document %d = %d: %s", i+1, rec.Code, rec.Body.String())
		}
	}
	if rec := f.uploadDocs(t, action.ID, map[string][]byte{"d.pdf": minimalPDF}); rec.Code != http.StatusBadRequest {
		t.Fatalf("over the limit = %d, want 400", rec.Code)
	}
}

func TestDeletingAFindingReturnsItsDocumentPaths(t *testing.T) {
	f := newAPIFixture(t)
	action := f.createViaAPI(t)
	if rec := f.uploadDocs(t, action.ID, map[string][]byte{"d.pdf": minimalPDF}); rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	paths, err := f.store.Delete(action.ID)
	if err != nil || len(paths) != 1 || !strings.HasSuffix(paths[0], ".pdf") {
		t.Fatalf("paths = %v, err = %v", paths, err)
	}
}
