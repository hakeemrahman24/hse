package entrymcu

import (
	"bytes"
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrepareOldMCUAdjustsLegacySheet(t *testing.T) {
	header := strings.Split("Timestamp,ReviewDate,CheckUpID,Reviewer,CheckUpCriteria,TaskType,Name,Gender,DateOfBirth,Age,Nationality,BloodType,Height,Company,WorkPosition,DateCheckUp,ValidityForReview,ResultValidityPeriod,Weight,BMIValue,BMICriteria,BMINotes,SmokingVape,BloodPressureCriteria,BloodPressureNotes,Vision,VisionNotes,CBCCriteria,CBCNotes,LipidProfileCriteria,LipidProfileNotes,BloodGlucoseCriteria,BloodGlucoseNotes,LiverFunctionCriteria,LiverFunctionNotes,KidneyFunctionCriteria,KidneyFunctionNotes,UrinalysisIncludingDrugTestCriteria,UrynalisisNotes,Radiology,RadiologyNotes,ECG,TreadmillTest,ECGTreadmillNotes,HerniaHemorrhoidCriteria,HerniaHemorrhoidNotes,SpirometryCriteria,SpirometryNotes,AudiometryCriteria,AudiometryNotes,HepBSerology,HepBSerologyNotes,HepASerology,HepASErologyNotes,RectalSwabCriteria,RectalSwabNotes,HIVSerology,ResultCriteria,ResultValidUntil,ResultNotes,RecommendationForFollowUp,CheckUpStatus,ParentCheckUpID,HealthCertificateFileName,HCValidityStatus,YearReview,Terminal,SubmissionDate,DaysToReview,Relation,Depth", ",")
	blank := func() map[string]string {
		row := map[string]string{}
		for _, name := range header {
			row[name] = "_"
		}
		return row
	}
	older := blank()
	older["Timestamp"] = "2024-11-01 08:00:00"
	older["ReviewDate"] = "2024-11-01"
	older["CheckUpID"] = "OLD1"
	older["Reviewer"] = "dr. Ana, MKK"
	older["CheckUpCriteria"] = "Pre Employment"
	older["TaskType"] = "Remote Site Worker, Other Professional (Pilot, Seafarer, Diver)"
	older["Name"] = "Ana Example"
	older["Gender"] = "Male"
	older["DateOfBirth"] = "1990-01-02"
	older["Age"] = "40.9"
	older["Nationality"] = "Phillipines"
	older["BloodType"] = "O+"
	older["Height"] = "170"
	older["Company"] = "Yilybang Konstruksi Indonesia, YILKI"
	older["WorkPosition"] = "QA/QC lead"
	older["DateCheckUp"] = "2024-11-01"
	older["ValidityForReview"] = "IsShortProject"
	older["ResultValidityPeriod"] = "12"
	older["Weight"] = "70"
	older["BMIValue"] = "24.2"
	older["BMICriteria"] = "Acceptable - Monitor"
	older["Vision"] = "Normal w Therapy"
	older["CBCCriteria"] = "Not Acceptable"
	older["ResultCriteria"] = "FitWithRestriction"
	older["ResultValidUntil"] = "2025-11-01"
	older["CheckUpStatus"] = "Close"
	older["HealthCertificateFileName"] = "cert-old1"
	older["HCValidityStatus"] = "Renewed by NEW1"
	older["YearReview"] = "2024"
	older["Terminal"] = "N"
	older["SubmissionDate"] = "2024-11-01"
	older["DaysToReview"] = `\N`
	older["Relation"] = "R32"
	renewed := blank()
	renewed["Timestamp"] = "2025-11-01 08:00:00"
	renewed["ReviewDate"] = "2025-11-01"
	renewed["CheckUpID"] = "NEW1"
	renewed["Reviewer"] = "dr. Ana"
	renewed["CheckUpCriteria"] = "Annual"
	renewed["TaskType"] = "Remote site worker"
	renewed["Name"] = "Ana Example"
	renewed["DateOfBirth"] = "1990-01-02"
	renewed["Age"] = "41"
	renewed["Nationality"] = "Philippines"
	renewed["Height"] = `\N`
	renewed["Company"] = "#VALUE!"
	renewed["WorkPosition"] = "QA/QC lead"
	renewed["DateCheckUp"] = "2025-10-01"
	renewed["ValidityForReview"] = "≤ 180 days"
	renewed["ResultValidityPeriod"] = "12"
	renewed["BMICriteria"] = "Normal"
	renewed["ResultCriteria"] = "Fit"
	renewed["ResultValidUntil"] = "2026-11-01"
	renewed["CheckUpStatus"] = "Close"
	renewed["ParentCheckUpID"] = "OLD1"
	renewed["HCValidityStatus"] = "Valid"
	renewed["YearReview"] = "2025"
	renewed["Terminal"] = "T"
	renewed["SubmissionDate"] = "2025-11-01"
	renewed["Relation"] = "R1"
	other := blank()
	other["Timestamp"] = "2025-07-01 08:00:00"
	other["ReviewDate"] = "2025-07-01"
	other["CheckUpID"] = "None"
	other["Reviewer"] = "dr. Ana"
	other["CheckUpCriteria"] = "Pre Employment"
	other["Name"] = "Other Person"
	other["Gender"] = "Female"
	other["DateOfBirth"] = "1980-02-03"
	other["Age"] = "45.2"
	other["Nationality"] = "Indonesia"
	other["DateCheckUp"] = "2025-07-01"
	other["BMICriteria"] = "Normal"
	other["ResultCriteria"] = "Unfit"
	other["ResultValidUntil"] = "2026-07-01"
	other["CheckUpStatus"] = "Close"
	other["YearReview"] = "2025"
	other["Terminal"] = "T"
	other["SubmissionDate"] = "2025-07-01"
	var raw bytes.Buffer
	writer := csv.NewWriter(&raw)
	if err := writer.Write(header); err != nil {
		t.Fatal(err)
	}
	write := func(row map[string]string) {
		fields := make([]string, len(header))
		for i, name := range header {
			fields[i] = row[name]
		}
		if err := writer.Write(fields); err != nil {
			t.Fatal(err)
		}
	}
	write(older)
	write(renewed)
	write(other)
	writer.Flush()
	csvText := raw.String()

	file, report, err := PrepareOldMCU(strings.NewReader(csvText))
	if err != nil {
		t.Fatal(err)
	}
	if report.PhilippinesFixed != 1 || report.YilybangFixed != 1 || report.CompanyValueErrors != 1 || report.FitWithRestriction != 1 || report.ShortProjects != 1 || report.NoneID == "" {
		t.Fatalf("report = %+v", report)
	}
	if report.Patients != 2 || report.Checkups != 3 || report.ParentsLinked != 1 {
		t.Fatalf("counts patients=%d checkups=%d parents=%d", report.Patients, report.Checkups, report.ParentsLinked)
	}

	mcuRows, err := file.GetRows(mcuSheetName)
	if err != nil {
		t.Fatal(err)
	}
	col := map[string]int{}
	for i, name := range mcuRows[0] {
		col[name] = i
	}
	byID := map[string][]string{}
	for _, row := range mcuRows[1:] {
		byID[row[col["CheckUpID"]]] = row
	}
	old := byID["OLD1"]
	if old[col["IsTerminal"]] != "no" || old[col["Nationality"]] != "Philippines" || old[col["Age"]] != "41" {
		t.Fatalf("old row terminal=%s nationality=%s age=%s", old[col["IsTerminal"]], old[col["Nationality"]], old[col["Age"]])
	}
	if old[col["Company"]] != "Yilybang Konstruksi Indonesia" || old[col["IsShortProject"]] != "yes" {
		t.Fatalf("old company=%s short=%s", old[col["Company"]], old[col["IsShortProject"]])
	}
	if old[col["TaskCategory"]] != "Remote Site Worker; Other Professional (Pilot, Seafarer, Diver)" {
		t.Fatalf("tasks = %s", old[col["TaskCategory"]])
	}
	if old[col["VisionCriteria"]] != "Normal w/ Therapy" || old[col["ResultCriteria"]] != "FitWithNote" {
		t.Fatalf("vision=%s result=%s", old[col["VisionCriteria"]], old[col["ResultCriteria"]])
	}
	if old[col["Timestamp"]] != "2024-11-01T08:00:00" || old[col["HealthCertificateFileName"]] != "cert-old1" {
		t.Fatalf("timestamp=%s cert=%s", old[col["Timestamp"]], old[col["HealthCertificateFileName"]])
	}
	if old[col["Reviewer"]] != "dr. Ana, MKK" || old[col["WorkPosition"]] != "QA/QC lead" {
		t.Fatalf("reviewer=%s position=%s", old[col["Reviewer"]], old[col["WorkPosition"]])
	}
	next := byID["NEW1"]
	if next[col["ParentCheckUpID"]] != "OLD1" || next[col["Company"]] != "" || next[col["IsTerminal"]] != "yes" {
		t.Fatalf("new parent=%s company=%q terminal=%s", next[col["ParentCheckUpID"]], next[col["Company"]], next[col["IsTerminal"]])
	}
	if _, ok := byID["None"]; ok {
		t.Fatal("literal None id was kept")
	}
	if byID[report.NoneID][col["PatientName"]] != "Other Person" {
		t.Fatalf("replacement id %s did not keep the row", report.NoneID)
	}

	var buf bytes.Buffer
	if err := file.Write(&buf); err != nil {
		t.Fatal(err)
	}
	database := openExportTestDB(t)
	body, contentType := multipartXLSX(t, buf.Bytes())
	importRec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/mcu-records/import", body)
	req.Header.Set("Content-Type", contentType)
	(&API{DB: database}).ImportMcuRecords(importRec, req)
	if importRec.Code != http.StatusOK || !strings.Contains(importRec.Body.String(), `"created":3`) {
		t.Fatalf("import %d %s", importRec.Code, importRec.Body.String())
	}
	var timestamp, tasks string
	var terminal int
	if err := database.QueryRow(`SELECT Timestamp, TaskCategoryNames, IsTerminal FROM McuRecord WHERE CheckUpID = 'OLD1'`).Scan(&timestamp, &tasks, &terminal); err != nil {
		t.Fatal(err)
	}
	if timestamp != "2024-11-01T08:00:00" || terminal != 0 || !strings.Contains(tasks, "Other Professional (Pilot, Seafarer, Diver)") {
		t.Fatalf("stored timestamp=%s terminal=%d tasks=%s", timestamp, terminal, tasks)
	}
	var parentID string
	if err := database.QueryRow(`SELECT p.CheckUpID FROM McuRecord c JOIN McuRecord p ON p.McuRecordID = c.FK_ParentRecordID WHERE c.CheckUpID = 'NEW1'`).Scan(&parentID); err != nil || parentID != "OLD1" {
		t.Fatalf("parent = %s err=%v", parentID, err)
	}
	var blood, position string
	if err := database.QueryRow(`SELECT COALESCE(MAX(m.BloodType),'') FROM McuRecord m JOIN Patient p ON p.PatientID = m.FK_PatientID WHERE p.Name = 'Ana Example'`).Scan(&blood); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT WorkPosition FROM Patient WHERE Name = 'Ana Example'`).Scan(&position); err != nil {
		t.Fatal(err)
	}
	if blood != "O+" || position != "QA/QC lead" {
		t.Fatalf("blood=%s position=%s", blood, position)
	}
}

func TestCanonicalTaskNamesFollowRiskList(t *testing.T) {
	if got := canonicalTaskList("driver/seafarer"); got != "Professional Drivers; Seafarer" {
		t.Fatalf("driver/seafarer = %q", got)
	}
	if got := canonicalTaskName("Health Professionals"); got != "Health care worker" {
		t.Fatalf("health = %q", got)
	}
	if got := canonicalTaskName("Other Professional (Pilot, Seafarer, Diver)"); got != "Other Professional (Pilot, Seafarer, Diver)" {
		t.Fatalf("bundled name = %q", got)
	}
}

func TestRelationCodeForTranslatesLegacyCodes(t *testing.T) {
	cases := map[string]string{
		"R38":     "SEI-LNG",
		"r47":     "SALA-GOKPL",
		"R10":     "CHCI-LNG",
		"SEI-LNG": "SEI-LNG",
		"":        "",
	}
	for in, want := range cases {
		if got := relationCodeFor(in); got != want {
			t.Errorf("relationCodeFor(%q) = %q, want %q", in, got, want)
		}
	}
	if len(legacyRelationCodes) != 48 {
		t.Fatalf("legacyRelationCodes has %d entries, want 48", len(legacyRelationCodes))
	}
}
