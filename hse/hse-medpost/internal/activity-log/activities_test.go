package activitylog

import (
	"bytes"
	"database/sql"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"hse-medpost/internal/auth"
	"hse-medpost/internal/db"
	"hse-medpost/internal/middleware"
)

func TestUpdateActivityFormAddsPhotoAndDocument(t *testing.T) {
	database := openActivityTestDB(t)
	if _, err := database.Exec(`INSERT INTO HealthProcedureTable (HealthProcedure) VALUES ('Malaria Program')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO MedpostStaffTable (StaffName) VALUES ('Logger'), ('Other')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO ActivityTable (FK_ProcID, FK_StaffID, ActivityType, ActivityTitle, ActivityDate)
		VALUES (1, 1, 'Monitoring/Inspection', 'Gate check', '2026-09-01')`); err != nil {
		t.Fatal(err)
	}

	api := &API{DB: database, UploadDir: t.TempDir()}
	body, contentType := activityEditForm(t, map[string]string{
		"activityType":    "Socialization",
		"activityTitle":   "Updated gate check",
		"activitySummary": "Walked the fence.",
		"location":        "Camp A",
		"activityDate":    "2026-09-02",
		"notes":           "Bring gloves",
	}, true)

	rec := callUpdate(t, api, 1, "staff", body, contentType)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	var title, atype, notes string
	if err := database.QueryRow(`SELECT ActivityTitle, ActivityType, Notes FROM ActivityTable WHERE ActivityID = 1`).Scan(&title, &atype, &notes); err != nil {
		t.Fatal(err)
	}
	if title != "Updated gate check" || atype != "Socialization" || notes != "Bring gloves" {
		t.Fatalf("saved %q %q %q", title, atype, notes)
	}
	var photos, reports int
	if err := database.QueryRow(`SELECT COUNT(*) FROM EvidenceTable WHERE EvidenceType = 'Photos'`).Scan(&photos); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM EvidenceTable WHERE EvidenceType = 'ReportAttachment'`).Scan(&reports); err != nil {
		t.Fatal(err)
	}
	if photos != 1 || reports != 1 {
		t.Fatalf("photos=%d reports=%d", photos, reports)
	}

	deniedBody, deniedType := activityEditForm(t, map[string]string{
		"activityType":  "Socialization",
		"activityTitle": "Should not stick",
		"activityDate":  "2026-09-02",
	}, false)
	denied := callUpdate(t, api, 2, "staff", deniedBody, deniedType)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("other staff status %d: %s", denied.Code, denied.Body.String())
	}
}

func TestDeleteEvidenceRemovesPhoto(t *testing.T) {
	database := openActivityTestDB(t)
	if _, err := database.Exec(`INSERT INTO HealthProcedureTable (HealthProcedure) VALUES ('Malaria Program')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO MedpostStaffTable (StaffName) VALUES ('Logger'), ('Other')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO ActivityTable (FK_ProcID, FK_StaffID, ActivityType, ActivityTitle, ActivityDate)
		VALUES (1, 1, 'Monitoring/Inspection', 'Gate check', '2026-09-01')`); err != nil {
		t.Fatal(err)
	}
	api := &API{DB: database, UploadDir: t.TempDir()}
	body, contentType := activityEditForm(t, map[string]string{
		"activityType":  "Monitoring/Inspection",
		"activityTitle": "Gate check",
		"activityDate":  "2026-09-01",
	}, true)
	if rec := callUpdate(t, api, 1, "staff", body, contentType); rec.Code != http.StatusOK {
		t.Fatalf("save %d: %s", rec.Code, rec.Body.String())
	}
	var evidenceID int64
	if err := database.QueryRow(`SELECT EvidenceID FROM EvidenceTable WHERE EvidenceType = 'Photos'`).Scan(&evidenceID); err != nil {
		t.Fatal(err)
	}

	denied := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/uploads/1", nil)
	req.SetPathValue("evidenceId", strconv.FormatInt(evidenceID, 10))
	req = req.WithContext(middleware.ContextWithSession(req.Context(), &auth.Session{StaffID: 2, Role: "staff"}))
	api.DeleteEvidence(denied, req)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("other staff %d: %s", denied.Code, denied.Body.String())
	}

	ok := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/uploads/1", nil)
	req.SetPathValue("evidenceId", strconv.FormatInt(evidenceID, 10))
	req = req.WithContext(middleware.ContextWithSession(req.Context(), &auth.Session{StaffID: 1, Role: "staff"}))
	api.DeleteEvidence(ok, req)
	if ok.Code != http.StatusOK {
		t.Fatalf("delete %d: %s", ok.Code, ok.Body.String())
	}
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM EvidenceTable WHERE EvidenceID = ?`, evidenceID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("evidence row still present")
	}
}

func callUpdate(t *testing.T, api *API, staffID int64, role string, body *bytes.Buffer, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/activities/1", body)
	req.Header.Set("Content-Type", contentType)
	req.SetPathValue("id", "1")
	req = req.WithContext(middleware.ContextWithSession(req.Context(), &auth.Session{StaffID: staffID, Role: role}))
	rec := httptest.NewRecorder()
	api.UpdateActivity(rec, req)
	return rec
}

func activityEditForm(t *testing.T, fields map[string]string, withFiles bool) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if withFiles {
		photo, err := w.CreateFormFile("photos", "gate.jpg")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := photo.Write([]byte{0xff, 0xd8, 0xff, 0xd9}); err != nil {
			t.Fatal(err)
		}
		report, err := w.CreateFormFile("report", "note.pdf")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := report.Write([]byte("%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF\n")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

func openActivityTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	for _, name := range []string{"001_sqlite.sql", "003_subheaders.sql"} {
		sqlText, err := db.LoadMigrationFile(filepath.Join("..", "..", "migrations", "sqlite", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(database, sqlText); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	return database
}

func TestOpenFileURLUsesStoredName(t *testing.T) {
	got := openFileURL("/api/documents", 12, "/data/uploads/2026-08-22_proc1_Procedure_sop-malaria_a1b2c3.pdf")
	want := "/api/documents/12/2026-08-22_proc1_Procedure_sop-malaria_a1b2c3.pdf"
	if got != want {
		t.Fatalf("document url = %s", got)
	}
	got = openFileURL("/api/uploads", 4, "/data/My Report.pdf")
	want = "/api/uploads/4/My%20Report.pdf"
	if got != want {
		t.Fatalf("upload url = %s", got)
	}
}
