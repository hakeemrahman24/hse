package staff

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"hse-medpost/internal/db"
)

func TestBuiltInAdminIsHiddenFromSettings(t *testing.T) {
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
		INSERT INTO MedpostStaffTable (StaffName, Position, Status) VALUES
			('Administrator', 'Admin', 'Active'),
			('Nurse Example', 'Medpost Nurse', 'Active')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO AppUserTable (FK_StaffID, Username, PasswordHash, Role) VALUES
			(1, 'admin', 'hash', 'admin'),
			(2, 'nurse', 'hash', 'staff')
	`); err != nil {
		t.Fatal(err)
	}
	api := &API{DB: database}

	list := httptest.NewRecorder()
	api.ListUsers(list, httptest.NewRequest(http.MethodGet, "/api/users", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list users %d %s", list.Code, list.Body.String())
	}
	if strings.Contains(list.Body.String(), "admin") || strings.Contains(list.Body.String(), "Administrator") {
		t.Fatalf("admin visible in users: %s", list.Body.String())
	}
	if !strings.Contains(list.Body.String(), "nurse") {
		t.Fatalf("nurse missing: %s", list.Body.String())
	}

	staffList := httptest.NewRecorder()
	api.ListStaff(staffList, httptest.NewRequest(http.MethodGet, "/api/staff", nil))
	if staffList.Code != http.StatusOK || strings.Contains(staffList.Body.String(), "Administrator") {
		t.Fatalf("admin visible in staff: %d %s", staffList.Code, staffList.Body.String())
	}

	reset := httptest.NewRecorder()
	resetReq := httptest.NewRequest(http.MethodPut, "/api/users/1/password", strings.NewReader(`{"password":"222222"}`))
	resetReq.SetPathValue("id", "1")
	api.ResetPassword(reset, resetReq)
	if reset.Code != http.StatusNotFound {
		t.Fatalf("reset admin %d %s", reset.Code, reset.Body.String())
	}

	edit := httptest.NewRecorder()
	editReq := httptest.NewRequest(http.MethodPut, "/api/staff/1", strings.NewReader(`{"staffName":"Someone Else"}`))
	editReq.SetPathValue("id", "1")
	api.UpdateStaff(edit, editReq)
	if edit.Code != http.StatusNotFound {
		t.Fatalf("edit admin %d %s", edit.Code, edit.Body.String())
	}

	remove := httptest.NewRecorder()
	removeReq := httptest.NewRequest(http.MethodDelete, "/api/users/1", nil)
	removeReq.SetPathValue("id", "1")
	api.DeactivateUser(remove, removeReq)
	if remove.Code != http.StatusNotFound {
		t.Fatalf("delete admin %d %s", remove.Code, remove.Body.String())
	}
}

func TestAdminEditUpdatesLogin(t *testing.T) {
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
		CREATE TABLE PositionRoleTable (
			PositionID INTEGER PRIMARY KEY AUTOINCREMENT,
			PositionName TEXT NOT NULL,
			Role TEXT NOT NULL
		);
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO MedpostStaffTable (StaffName, Position) VALUES ('Nurse Example', 'Medpost Nurse');
		INSERT INTO AppUserTable (FK_StaffID, Username, PasswordHash, Role) VALUES (1, 'nurse', 'old-hash', 'staff');
		INSERT INTO PositionRoleTable (PositionName, Role) VALUES ('Chief Medical Officer', 'admin');
	`); err != nil {
		t.Fatal(err)
	}
	api := &API{DB: database}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/staff/1", strings.NewReader(`{
		"staffName":"dr. Nurse Example",
		"position":"Chief Medical Officer",
		"username":"nurse.lead",
		"password":"445566"
	}`))
	req.SetPathValue("id", "1")
	api.UpdateStaff(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("edit %d %s", rec.Code, rec.Body.String())
	}
	var name, position, username, role, hash string
	if err := database.QueryRow(`
		SELECT s.StaffName, s.Position, u.Username, u.Role, u.PasswordHash
		FROM MedpostStaffTable s JOIN AppUserTable u ON u.FK_StaffID = s.StaffID
		WHERE s.StaffID = 1`).Scan(&name, &position, &username, &role, &hash); err != nil {
		t.Fatal(err)
	}
	if name != "dr. Nurse Example" || position != "Chief Medical Officer" || username != "nurse.lead" || role != "admin" {
		t.Fatalf("saved name=%s position=%s username=%s role=%s", name, position, username, role)
	}
	if hash == "old-hash" {
		t.Fatal("password was not updated")
	}

	keep := httptest.NewRecorder()
	keepReq := httptest.NewRequest(http.MethodPut, "/api/staff/1", strings.NewReader(`{
		"position":"Chief Medical Officer",
		"role":"staff"
	}`))
	keepReq.SetPathValue("id", "1")
	api.UpdateStaff(keep, keepReq)
	if keep.Code != http.StatusOK {
		t.Fatalf("role edit %d %s", keep.Code, keep.Body.String())
	}
	if err := database.QueryRow(`SELECT Role FROM AppUserTable WHERE FK_StaffID = 1`).Scan(&role); err != nil {
		t.Fatal(err)
	}
	if role != "staff" {
		t.Fatalf("explicit role overwritten: %s", role)
	}
}

func TestAdministratorPositionIsHidden(t *testing.T) {
	database, err := db.Open("sqlite", filepath.Join(t.TempDir(), "positions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if _, err := database.Exec(`
		CREATE TABLE PositionRoleTable (
			PositionID INTEGER PRIMARY KEY AUTOINCREMENT,
			PositionName TEXT NOT NULL UNIQUE,
			Role TEXT NOT NULL
		);
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO PositionRoleTable (PositionName, Role) VALUES
			('Administrator', 'admin'),
			('Chief Medical Officer', 'admin'),
			('Site Doctor', 'staff')
	`); err != nil {
		t.Fatal(err)
	}
	api := &API{DB: database}
	list := httptest.NewRecorder()
	api.ListPositionRoles(list, httptest.NewRequest(http.MethodGet, "/api/position-roles", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list %d %s", list.Code, list.Body.String())
	}
	body := list.Body.String()
	if strings.Contains(body, "Administrator") {
		t.Fatalf("administrator visible: %s", body)
	}
	if !strings.Contains(body, "Chief Medical Officer") || !strings.Contains(body, "Site Doctor") {
		t.Fatalf("other positions missing: %s", body)
	}
	edit := httptest.NewRecorder()
	editReq := httptest.NewRequest(http.MethodPut, "/api/position-roles/1", strings.NewReader(`{"role":"staff"}`))
	editReq.SetPathValue("id", "1")
	api.UpdatePositionRole(edit, editReq)
	if edit.Code != http.StatusNotFound {
		t.Fatalf("edit administrator %d %s", edit.Code, edit.Body.String())
	}
}
