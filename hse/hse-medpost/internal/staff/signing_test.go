package staff

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"software.sslmate.com/src/go-pkcs12"

	"hse-medpost/internal/db"
)

func TestIsSigningPosition(t *testing.T) {
	if !isSigningPosition("Site Doctor") || !isSigningPosition("Chief Medical Officer") || !isSigningPosition("CMO") {
		t.Fatal("doctor and CMO should be allowed")
	}
	if isSigningPosition("Medpost Nurse") || isSigningPosition("Administrator") || isSigningPosition("") {
		t.Fatal("nurse and administrator should not be allowed")
	}
}

func TestCreateSigningCertificateStoresP12(t *testing.T) {
	database, err := db.Open("sqlite", filepath.Join(t.TempDir(), "staff.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if _, err := database.Exec(`
		CREATE TABLE MedpostStaffTable (
			StaffID INTEGER PRIMARY KEY AUTOINCREMENT,
			StaffName TEXT NOT NULL,
			Position TEXT,
			Status TEXT NOT NULL DEFAULT 'Active'
		);
		CREATE TABLE AppUserTable (
			UserID INTEGER PRIMARY KEY AUTOINCREMENT,
			FK_StaffID INTEGER NOT NULL UNIQUE,
			Username TEXT NOT NULL UNIQUE,
			PasswordHash TEXT NOT NULL,
			Role TEXT NOT NULL
		);
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO MedpostStaffTable (StaffName, Position) VALUES
			('Administrator', 'Admin'),
			('Nurse Example', 'Medpost Nurse'),
			('Site Doctor Example', 'Site Doctor'),
			('CMO Example', 'Chief Medical Officer')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO AppUserTable (FK_StaffID, Username, PasswordHash, Role) VALUES
			(1, 'admin', 'hash', 'admin'),
			(2, 'nurse', 'hash', 'staff'),
			(3, 'doctor', 'hash', 'staff'),
			(4, 'cmo', 'hash', 'admin')
	`); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "signing")
	api := &API{DB: database, SigningDir: dir}

	nurse := httptest.NewRecorder()
	nurseReq := httptest.NewRequest(http.MethodPost, "/api/staff/2/signing-certificate", nil)
	nurseReq.SetPathValue("id", "2")
	api.CreateSigningCertificate(nurse, nurseReq)
	if nurse.Code != http.StatusForbidden {
		t.Fatalf("nurse status %d %s", nurse.Code, nurse.Body.String())
	}

	admin := httptest.NewRecorder()
	adminReq := httptest.NewRequest(http.MethodPost, "/api/staff/1/signing-certificate", nil)
	adminReq.SetPathValue("id", "1")
	api.CreateSigningCertificate(admin, adminReq)
	if admin.Code != http.StatusNotFound {
		t.Fatalf("admin status %d %s", admin.Code, admin.Body.String())
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/staff/3/signing-certificate", nil)
	req.SetPathValue("id", "3")
	api.CreateSigningCertificate(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("doctor status %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"file":"staff-3.p12"`) || !strings.Contains(rec.Body.String(), `"password":"`) {
		t.Fatalf("response %s", rec.Body.String())
	}
	p12, err := os.ReadFile(filepath.Join(dir, "staff-3.p12"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "staff-3.p12"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("p12 mode %o", info.Mode().Perm())
	}
	pass, err := os.ReadFile(filepath.Join(dir, "staff-3.pass"))
	if err != nil {
		t.Fatal(err)
	}
	key, cert, _, err := pkcs12.DecodeChain(p12, strings.TrimSpace(string(pass)))
	if err != nil {
		t.Fatal(err)
	}
	if key == nil || cert.Subject.CommonName != "Site Doctor Example" {
		t.Fatalf("cert %v", cert.Subject)
	}
	if _, err := os.Stat(filepath.Join(dir, "ca.key")); err != nil {
		t.Fatal(err)
	}

	list := httptest.NewRecorder()
	api.ListUsers(list, httptest.NewRequest(http.MethodGet, "/api/users", nil))
	if !strings.Contains(list.Body.String(), `"signingCertificate":true`) {
		t.Fatalf("list %s", list.Body.String())
	}

	nurseDel := httptest.NewRecorder()
	nurseDelReq := httptest.NewRequest(http.MethodDelete, "/api/staff/2/signing-certificate", nil)
	nurseDelReq.SetPathValue("id", "2")
	api.DeleteSigningCertificate(nurseDel, nurseDelReq)
	if nurseDel.Code != http.StatusForbidden {
		t.Fatalf("nurse delete %d %s", nurseDel.Code, nurseDel.Body.String())
	}

	del := httptest.NewRecorder()
	delReq := httptest.NewRequest(http.MethodDelete, "/api/staff/3/signing-certificate", nil)
	delReq.SetPathValue("id", "3")
	api.DeleteSigningCertificate(del, delReq)
	if del.Code != http.StatusOK {
		t.Fatalf("delete %d %s", del.Code, del.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "staff-3.p12")); !os.IsNotExist(err) {
		t.Fatal("p12 still present")
	}
	if _, err := os.Stat(filepath.Join(dir, "staff-3.pass")); !os.IsNotExist(err) {
		t.Fatal("password file still present")
	}
	if _, err := os.Stat(filepath.Join(dir, "ca.key")); err != nil {
		t.Fatal("shared certificate authority was removed")
	}
	again := httptest.NewRecorder()
	againReq := httptest.NewRequest(http.MethodDelete, "/api/staff/3/signing-certificate", nil)
	againReq.SetPathValue("id", "3")
	api.DeleteSigningCertificate(again, againReq)
	if again.Code != http.StatusNotFound {
		t.Fatalf("second delete %d %s", again.Code, again.Body.String())
	}
}
