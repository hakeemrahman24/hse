package middleware

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"hse-medpost/internal/auth"
	"hse-medpost/internal/db"
)

func TestMayOpenMedicalCheckup(t *testing.T) {
	if !IsDoctorOrCMO("Site Doctor") || !IsDoctorOrCMO("Chief Medical Officer") || !IsDoctorOrCMO("CMO") {
		t.Fatal("doctor and CMO should be allowed")
	}
	if IsDoctorOrCMO("Medpost Nurse") || IsDoctorOrCMO("Administrator") || IsDoctorOrCMO("") {
		t.Fatal("nurse and administrator should not be allowed")
	}

	database, err := db.Open("sqlite", filepath.Join(t.TempDir(), "mcu-access.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if _, err := database.Exec(`
		CREATE TABLE MedpostStaffTable (
			StaffID INTEGER PRIMARY KEY,
			Position TEXT,
			Status TEXT NOT NULL DEFAULT 'Active'
		);
		CREATE TABLE AppUserTable (
			UserID INTEGER PRIMARY KEY,
			FK_StaffID INTEGER NOT NULL,
			Username TEXT NOT NULL,
			Role TEXT NOT NULL
		);
		INSERT INTO MedpostStaffTable (StaffID, Position, Status) VALUES
			(1, 'Site Doctor', 'Active'),
			(2, 'Chief Medical Officer', 'Active'),
			(3, 'Medpost Nurse', 'Active'),
			(4, 'Site Doctor', 'NotActive'),
			(5, 'Admin', 'Active'),
			(6, 'Administrator', 'NotActive');
		INSERT INTO AppUserTable (UserID, FK_StaffID, Username, Role) VALUES
			(1, 1, 'doctor', 'staff'),
			(2, 2, 'cmo', 'admin'),
			(3, 3, 'nurse', 'staff'),
			(5, 5, 'admin', 'admin'),
			(6, 6, 'oldadmin', 'admin');
	`); err != nil {
		t.Fatal(err)
	}

	if !MayOpenMedicalCheckup(database, 1) || !MayOpenMedicalCheckup(database, 2) || !MayOpenMedicalCheckup(database, 5) {
		t.Fatal("active doctor, CMO, and administrator should open medical checkup")
	}
	if MayOpenMedicalCheckup(database, 3) || MayOpenMedicalCheckup(database, 4) || MayOpenMedicalCheckup(database, 6) || MayOpenMedicalCheckup(database, 9) {
		t.Fatal("nurse, inactive doctor, inactive administrator, and unknown staff should not open medical checkup")
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	gate := RequireDoctorOrCMO(database)(next)

	nurse := httptest.NewRequest(http.MethodGet, "/api/mcu-records", nil)
	nurse = nurse.WithContext(ContextWithSession(nurse.Context(), &auth.Session{StaffID: 3, Role: "staff"}))
	rec := httptest.NewRecorder()
	gate.ServeHTTP(rec, nurse)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("nurse api status %d", rec.Code)
	}

	doctor := httptest.NewRequest(http.MethodGet, "/mcu", nil)
	doctor = doctor.WithContext(ContextWithSession(doctor.Context(), &auth.Session{StaffID: 1, Role: "staff"}))
	rec = httptest.NewRecorder()
	gate.ServeHTTP(rec, doctor)
	if rec.Code != http.StatusOK {
		t.Fatalf("doctor page status %d", rec.Code)
	}

	admin := httptest.NewRequest(http.MethodGet, "/mcu", nil)
	admin = admin.WithContext(ContextWithSession(admin.Context(), &auth.Session{StaffID: 5, Role: "admin"}))
	rec = httptest.NewRecorder()
	gate.ServeHTTP(rec, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("administrator page status %d", rec.Code)
	}
}
