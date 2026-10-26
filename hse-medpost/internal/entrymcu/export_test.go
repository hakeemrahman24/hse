package entrymcu

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"hse-medpost/internal/db"
	"hse-medpost/internal/staff"
)

func TestExportWorkbookHasPatientAndMcuSheets(t *testing.T) {
	database := openExportTestDB(t)
	if _, err := database.Exec(`INSERT INTO NationalityTable (Name) VALUES ('Indonesia')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO Patient (Name, DateOfBirth, Gender, FK_NationalityID, PersonalID, WorkPosition) VALUES ('Ani Test', '1990-01-02', 'F', 1, '1234567890123456', 'Nurse')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO McuRecord (FK_PatientID, Timestamp, ReviewDate, CheckUpID, PersonalID, WorkPosition, BloodType) VALUES (1, '2026-01-01T00:00:00', '2026-01-01', 'MedC1', '1234567890123456', 'Medic', 'O')`); err != nil {
		t.Fatal(err)
	}

	api := &API{DB: database}
	rec := httptest.NewRecorder()
	api.DownloadAllEntries(rec, httptest.NewRequest(http.MethodGet, "/api/mcu-records/export.xlsx", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	f, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if got := f.GetSheetList(); len(got) != 2 || got[0] != patientSheetName || got[1] != mcuSheetName {
		t.Fatalf("sheets = %v", got)
	}
	patients, err := f.GetRows(patientSheetName)
	if err != nil {
		t.Fatal(err)
	}
	if patients[0][5] != "BloodType" || patients[0][7] != "PersonalID" {
		t.Fatalf("patient header = %v", patients[0])
	}
	if patients[1][1] != "Ani Test" || patients[1][5] != "O" {
		t.Fatalf("patient row = %v", patients[1])
	}
	mcuRows, err := f.GetRows(mcuSheetName)
	if err != nil {
		t.Fatal(err)
	}
	header := map[string]int{}
	for i, h := range mcuRows[0] {
		header[h] = i
	}
	if _, ok := header["Gender"]; ok {
		t.Fatal("gender belongs on the Patients sheet")
	}
	if mcuRows[1][header["CheckUpID"]] != "MedC1" || mcuRows[1][header["PersonalID"]] != "1234567890123456" || mcuRows[1][header["WorkPosition"]] != "Medic" {
		t.Fatalf("mcu row = %v", mcuRows[1])
	}

	body, contentType := multipartXLSX(t, rec.Body.Bytes())
	importRec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/mcu-records/import", body)
	req.Header.Set("Content-Type", contentType)
	api.ImportMcuRecords(importRec, req)
	if importRec.Code != http.StatusOK {
		t.Fatalf("import status %d: %s", importRec.Code, importRec.Body.String())
	}

	var blood, patientPosition, visitPersonal, visitPosition string
	if err := database.QueryRow(`SELECT COALESCE(MAX(m.BloodType),''), p.WorkPosition FROM Patient p JOIN McuRecord m ON m.FK_PatientID = p.PatientID WHERE p.Name = 'Ani Test'`).Scan(&blood, &patientPosition); err != nil {
		t.Fatal(err)
	}
	// Import syncs the patient's position from their latest visit ("Medic"), not
	// the stored patient value ("Nurse").
	if blood != "O" || patientPosition != "Medic" {
		t.Fatalf("patient identity blood=%q position=%q", blood, patientPosition)
	}
	if err := database.QueryRow(`SELECT PersonalID, WorkPosition FROM McuRecord WHERE CheckUpID = 'MedC1'`).Scan(&visitPersonal, &visitPosition); err != nil {
		t.Fatal(err)
	}
	if visitPersonal != "1234567890123456" || visitPosition != "Medic" {
		t.Fatalf("visit personal=%q position=%q", visitPersonal, visitPosition)
	}
}

func TestFlatExportIsOneSheetWithEmployeeDetails(t *testing.T) {
	database := openExportTestDB(t)
	if _, err := database.Exec(`INSERT INTO NationalityTable (Name) VALUES ('Indonesia')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO Company (Name) VALUES ('Starway'), ('Indocater'), ('Client A'), ('Client B'), ('Dual Co')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO CompanyRelation (RelationCode, FK_CompanyID, FK_ParentID, Project, Depth) VALUES
		('INDO-LNG', (SELECT CompanyID FROM Company WHERE Name='Indocater'), (SELECT CompanyID FROM Company WHERE Name='Starway'), 'LNG', 2),
		('DUAL-GOKPL', (SELECT CompanyID FROM Company WHERE Name='Dual Co'), (SELECT CompanyID FROM Company WHERE Name='Client A'), 'GOKPL', 2),
		('DUAL-LNG', (SELECT CompanyID FROM Company WHERE Name='Dual Co'), (SELECT CompanyID FROM Company WHERE Name='Client B'), 'LNG', 3)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO Patient (Name, DateOfBirth, Gender, FK_NationalityID, EmployeeID, PersonalID, WorkPosition) VALUES ('Ani Test', '1990-01-02', 'F', 1, 'EMP-9', '1234567890123456', 'Nurse'), ('Budi Test', '1991-03-04', 'M', 1, 'EMP-2', '1234567890123457', 'Welder')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO McuRecord (FK_PatientID, Timestamp, ReviewDate, CheckUpID, PersonalID, WorkPosition, FK_CompanyID, BloodType) VALUES
		(1, '2026-01-01T00:00:00', '2026-01-01', 'MedC1', '1234567890123456', 'Medic', (SELECT CompanyID FROM Company WHERE Name='Indocater'), 'O'),
		(2, '2026-01-02T00:00:00', '2026-01-02', 'MedC2', '1234567890123457', 'Welder', (SELECT CompanyID FROM Company WHERE Name='Dual Co'), 'A'),
		(2, '2026-01-03T00:00:00', '2026-01-03', 'MedC3', '1234567890123457', 'Welder', (SELECT CompanyID FROM Company WHERE Name='Dual Co'), 'A')`); err != nil {
		t.Fatal(err)
	}

	if _, err := database.Exec(`UPDATE McuRecord SET FK_CompanyRelationID=(SELECT CompanyRelationID FROM CompanyRelation WHERE RelationCode='DUAL-LNG') WHERE CheckUpID='MedC2'`); err != nil {
		t.Fatal(err)
	}

	api := &API{DB: database}
	rec := httptest.NewRecorder()
	api.DownloadMcuDataSheet(rec, httptest.NewRequest(http.MethodGet, "/api/mcu-records/mcu-data.xlsx", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	f, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if got := f.GetSheetList(); len(got) != 1 || got[0] != mcuDataSheetName {
		t.Fatalf("sheets = %v", got)
	}
	rows, err := f.GetRows(mcuDataSheetName)
	if err != nil {
		t.Fatal(err)
	}
	header := map[string]int{}
	for i, h := range rows[0] {
		header[h] = i
	}
	for _, col := range []string{"CheckUpID", "PatientName", "Gender", "Nationality", "BloodType", "EmployeeID", "WorkPosition", "Company", "Parent", "Project", "Depth"} {
		if _, ok := header[col]; !ok {
			t.Fatalf("missing %s in %v", col, rows[0])
		}
	}
	if len(rows) != 4 {
		t.Fatalf("data rows = %d, want one row per checkup", len(rows)-1)
	}
	row := rows[1]
	if row[header["CheckUpID"]] != "MedC1" || row[header["PatientName"]] != "Ani Test" || row[header["Gender"]] != "F" || row[header["Nationality"]] != "Indonesia" || row[header["BloodType"]] != "O" || row[header["EmployeeID"]] != "EMP-9" || row[header["WorkPosition"]] != "Medic" || row[header["Company"]] != "Indocater" || row[header["Parent"]] != "Starway" || row[header["Project"]] != "LNG" || row[header["Depth"]] != "2" {
		t.Fatalf("row = %v", row)
	}
	// A checkup tied to one relation exports that relation's single parent.
	dual := rows[2]
	if dual[header["CheckUpID"]] != "MedC2" || dual[header["Parent"]] != "Client B" || dual[header["Project"]] != "LNG" || dual[header["Depth"]] != "3" {
		t.Fatalf("dual relation row = %v", dual)
	}
	// With no relation on the checkup, a multi-parent company still gets one
	// parent (first by project), never "A / B".
	unlinked := rows[3]
	if unlinked[header["CheckUpID"]] != "MedC3" || unlinked[header["Parent"]] != "Client A" || unlinked[header["Project"]] != "GOKPL" || unlinked[header["Depth"]] != "2" {
		t.Fatalf("unlinked dual company row = %v", unlinked)
	}
}

func TestMcuDataSheetLimitKeepsLatestRows(t *testing.T) {
	database := openExportTestDB(t)
	if _, err := database.Exec(`INSERT INTO Patient (Name) VALUES ('Ani Test')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO McuRecord (FK_PatientID, Timestamp, ReviewDate, CheckUpID, PersonalID) VALUES
		(1, '2026-01-01T00:00:00', '2026-01-01', 'MedC1', '1234567890123456'),
		(1, '2026-01-02T00:00:00', '2026-01-02', 'MedC2', '1234567890123456'),
		(1, '2026-01-03T00:00:00', '2026-01-03', 'MedC3', '1234567890123456')`); err != nil {
		t.Fatal(err)
	}
	api := &API{DB: database}

	checkupIDs := func(query string) ([]string, int) {
		t.Helper()
		rec := httptest.NewRecorder()
		api.DownloadMcuDataSheet(rec, httptest.NewRequest(http.MethodGet, "/api/mcu-records/mcu-data.xlsx"+query, nil))
		if rec.Code != http.StatusOK {
			return nil, rec.Code
		}
		f, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		rows, err := f.GetRows(mcuDataSheetName)
		if err != nil {
			t.Fatal(err)
		}
		col := -1
		for i, h := range rows[0] {
			if h == "CheckUpID" {
				col = i
			}
		}
		ids := []string{}
		for _, row := range rows[1:] {
			ids = append(ids, row[col])
		}
		return ids, rec.Code
	}

	if ids, _ := checkupIDs("?limit=2"); strings.Join(ids, ",") != "MedC2,MedC3" {
		t.Fatalf("limit=2 gave %v, want the two latest rows oldest first", ids)
	}
	if ids, _ := checkupIDs("?limit=10"); len(ids) != 3 {
		t.Fatalf("limit above row count gave %v, want all rows", ids)
	}
	if ids, _ := checkupIDs(""); len(ids) != 3 {
		t.Fatalf("no limit gave %v, want all rows", ids)
	}
	if ids, _ := checkupIDs("?ids=1,3"); strings.Join(ids, ",") != "MedC1,MedC3" {
		t.Fatalf("ids=1,3 gave %v, want just those checkups", ids)
	}
	if ids, _ := checkupIDs("?ids=3,1,2"); strings.Join(ids, ",") != "MedC3,MedC1,MedC2" {
		t.Fatalf("ids=3,1,2 gave %v, want the order given", ids)
	}
	if ids, _ := checkupIDs("?ids=1,3&limit=1"); len(ids) != 2 {
		t.Fatalf("ids with limit gave %v, want ids to win", ids)
	}
	if _, code := checkupIDs("?ids=999"); code != http.StatusNotFound {
		t.Fatalf("ids matching nothing status = %d, want 404", code)
	}
	for _, bad := range []string{"?ids=1,x", "?ids=0", "?ids=1,,2"} {
		if _, code := checkupIDs(bad); code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400", bad, code)
		}
	}
	for _, bad := range []string{"?limit=0", "?limit=-1", "?limit=abc"} {
		if _, code := checkupIDs(bad); code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400", bad, code)
		}
	}
}

func TestDownloadLatestEligibleCertificates(t *testing.T) {
	database := openExportTestDB(t)
	validUntil := time.Now().AddDate(1, 0, 0).Format(dateOnly)
	yesterday := time.Now().AddDate(0, 0, -1).Format(dateOnly)
	insertPatient := func(name string) int64 {
		t.Helper()
		res, err := database.Exec(`INSERT INTO Patient (Name) VALUES (?)`, name)
		if err != nil {
			t.Fatal(err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	insertMCU := func(patientID int64, reviewDate, checkUpID, result, valid, status string) {
		t.Helper()
		_, err := database.Exec(`
			INSERT INTO McuRecord (FK_PatientID, Timestamp, ReviewDate, CheckUpID, Reviewer, ResultCriteria, ResultValidUntil, CheckUpStatus)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			patientID, reviewDate+"T00:00:00", reviewDate, checkUpID, "Site Doctor Example", result, valid, status)
		if err != nil {
			t.Fatal(err)
		}
	}

	a := insertPatient("Ahmad Older")
	insertMCU(a, "2026-01-01", "MedC-A-old", "Fit", validUntil, "Close")
	insertMCU(a, "2026-09-01", "MedC-A-new", "Fit", validUntil, "Close")
	b := insertPatient("Budi Recent")
	insertMCU(b, "2026-09-20", "MedC-B", "Fit", validUntil, "Close")
	c := insertPatient("Cici Overdue")
	insertMCU(c, "2026-09-25", "MedC-C", "Fit", yesterday, "Close")
	d := insertPatient("Dedi Open")
	insertMCU(d, "2026-09-26", "MedC-D", "NeedFollowUp", validUntil, "Open")
	e := insertPatient("Eka Unfit")
	insertMCU(e, "2026-09-26", "MedC-E", "Unfit", validUntil, "Close")
	f := insertPatient("Fajar Earlier")
	insertMCU(f, "2026-08-01", "MedC-F", "Fit", validUntil, "Close")

	signDir := filepath.Join(t.TempDir(), "signing")
	if _, err := database.Exec(`INSERT INTO MedpostStaffTable (StaffName, Position, Status) VALUES ('Site Doctor Example', 'Site Doctor', 'Active')`); err != nil {
		t.Fatal(err)
	}
	var signerID int64
	if err := database.QueryRow(`SELECT StaffID FROM MedpostStaffTable WHERE StaffName = ?`, "Site Doctor Example").Scan(&signerID); err != nil {
		t.Fatal(err)
	}
	signerAPI := &staff.API{DB: database, SigningDir: signDir}
	signerRec := httptest.NewRecorder()
	signerReq := httptest.NewRequest(http.MethodPost, "/api/staff/"+strconv.FormatInt(signerID, 10)+"/signing-certificate", nil)
	signerReq.SetPathValue("id", strconv.FormatInt(signerID, 10))
	signerAPI.CreateSigningCertificate(signerRec, signerReq)
	if signerRec.Code != http.StatusOK {
		t.Fatalf("signer cert %d %s", signerRec.Code, signerRec.Body.String())
	}

	api := &API{DB: database}
	cfg := CertConfig{Secret: "test-secret", UploadDir: t.TempDir(), SigningDir: signDir}
	handler := api.DownloadLatestEligibleCertificates(cfg)

	bad := httptest.NewRecorder()
	handler(bad, httptest.NewRequest(http.MethodGet, "/api/mcu-records/latest-certificates.zip?n=21", nil))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("n=21 status %d: %s", bad.Code, bad.Body.String())
	}

	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodGet, "/api/mcu-records/latest-certificates.zip?n=2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(body) < 4 || string(body[:4]) != "%PDF" {
			t.Fatalf("%s is not a pdf", f.Name)
		}
		if !bytes.Contains(body, []byte("/ByteRange")) || !bytes.Contains(body, []byte("Site Doctor Example")) {
			t.Fatalf("%s is not signed by the reviewer", f.Name)
		}
		names = append(names, f.Name)
	}
	joined := strings.Join(names, "\n")
	want := []string{
		certificateOpenFilename("MedC-B", "", "Budi Recent", validUntil),
		certificateOpenFilename("MedC-A-new", "", "Ahmad Older", validUntil),
	}
	if len(names) != 2 || !strings.Contains(joined, want[0]) || !strings.Contains(joined, want[1]) {
		t.Fatalf("zip names = %v, want %v", names, want)
	}
	for _, excluded := range []string{"MedC-A-old", "MedC-C", "MedC-D", "MedC-E", "MedC-F"} {
		if strings.Contains(joined, excluded) {
			t.Fatalf("zip included %s: %v", excluded, names)
		}
	}
}

func multipartXLSX(t *testing.T, xlsx []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "mcu.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(xlsx); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

func openExportTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	for _, name := range []string{
		"001_sqlite.sql", "002_position_role.sql", "003_subheaders.sql", "004_company.sql",
		"005_patient.sql", "006_entrymcu.sql", "007_mcu_certificate_log.sql",
		"008_mcu_email_notification.sql", "009_patient_name_index.sql", "010_project.sql",
	} {
		sqlText, err := db.LoadMigrationFile(filepath.Join("..", "..", "migrations", "sqlite", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(database, sqlText); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for _, step := range []func(*sql.DB) error{
		EnsureCheckUpStatusConstraint,
		EnsureTaskCategoryNamesColumn,
		EnsureRadiologyColumns,
		EnsureCtScanColumnsRenamed,
		EnsurePhysicalExamNotesColumn,
		EnsureBloodTypeColumn,
		EnsurePatientPersonalIDColumn,
		EnsureMcuRecordPersonalIDColumn,
		EnsureWorkLocationColumn,
		EnsureSchemaIndexes,
	} {
		if err := step(database); err != nil {
			t.Fatal(err)
		}
	}
	return database
}
