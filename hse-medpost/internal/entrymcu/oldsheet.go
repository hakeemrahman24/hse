package entrymcu

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// OldSheetReport counts the adjustments applied while turning the legacy
// spreadsheet into the workbook ImportMcuRecords already accepts.
type OldSheetReport struct {
	Checkups            int
	Patients            int
	ShortProjects       int
	ParentsLinked       int
	ParentDisagreements int
	MultiPredecessor    int
	FitWithRestriction  int
	NoneID              string
	PhilippinesFixed    int
	CompanyValueErrors  int
	YilybangFixed       int
	TerminalCurrent     int
	TerminalSuperseded  int
}

var legacyTaskAliases = map[string]string{
	"heavy equipment operator/crane operator": "Heavy Equipment or Crane Operator",
	"driver/seafarer":                         "Professional Drivers; Seafarer",
	"health professionals":                    "Health care worker",
}

// legacyRelationCodes maps the old sheet's company relation codes (R01–R48)
// to the RelationCode values stored in CompanyRelation. Each pair matches on
// company, parent, and project.
var legacyRelationCodes = map[string]string{
	"R01": "ANUGRAH-LNG", "R02": "APA-LNG", "R03": "ASW-LNG", "R04": "ASI-LNG",
	"R05": "AEA-LNG", "R06": "BMT-LNG", "R07": "CLL-LNG", "R08": "CCC-LNG",
	"R09": "ELECO-LNG", "R10": "CHCI-LNG", "R11": "CEI-LNG", "R12": "COEC-LNG",
	"R13": "ERM-LNG", "R14": "GAP-LNG", "R15": "HAT-LNG", "R16": "HTGI-LNG",
	"R17": "HGEI-LNG", "R18": "IDE-LNG", "R19": "KERUI-LNG", "R20": "KJPP-LNG",
	"R21": "IBTKPN-LNG", "R22": "SIKARS-GOKPL", "R23": "KUS-LNG", "R24": "MAX-LNG",
	"R25": "MCE-LNG", "R26": "N28-LNG", "R27": "NES-LNG", "R28": "PCG-LNG",
	"R29": "PNEP-LNG", "R30": "POWERCHINA-LNG", "R31": "S95-LNG", "R32": "SALA-LNG",
	"R33": "SET-LNG", "R34": "SLL-LNG", "R35": "SEMESTA-LNG", "R36": "SP-LNG",
	"R37": "SINO-LNG", "R38": "SEI-LNG", "R39": "TEN-LNG", "R40": "TMU-LNG",
	"R41": "TRIC-LNG", "R42": "WI-LNG", "R43": "YILKI-LNG", "R44": "YSK-LNG",
	"R45": "ZSCEC-LNG", "R46": "ZIHI-LNG", "R47": "SALA-GOKPL", "R48": "Indocater-LNG",
}

// relationCodeFor returns the CompanyRelation code for a checkup's relation
// cell. Legacy R-codes are translated; anything else (already a database
// code) is returned unchanged.
func relationCodeFor(code string) string {
	if mapped, ok := legacyRelationCodes[strings.ToUpper(strings.TrimSpace(code))]; ok {
		return mapped
	}
	return code
}

var legacyCriteria = map[string]string{
	"acceptable - monitor": "Acceptable - monitor",
	"not acceptable":       "Unacceptable",
	"normal w therapy":     "Normal w/ Therapy",
}

// PrepareOldMCU reads the legacy one-sheet checkup export and returns a
// workbook with a Patients sheet and an MCU entries sheet.
func PrepareOldMCU(r io.Reader) (*excelize.File, OldSheetReport, error) {
	reader := csv.NewReader(r)
	// Spreadsheet exports drop trailing empty cells, so rows can be shorter
	// than the header; missing cells are read as blank below.
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, OldSheetReport{}, fmt.Errorf("read legacy csv: %w", err)
	}
	if len(records) < 2 {
		return nil, OldSheetReport{}, fmt.Errorf("legacy csv has no data rows")
	}
	header := map[string]int{}
	for i, name := range records[0] {
		header[name] = i
	}
	rows := make([]legacyCheckup, 0, len(records)-1)
	var report OldSheetReport
	for _, rec := range records[1:] {
		row := legacyCheckup{cells: map[string]string{}}
		for name, i := range header {
			if i < len(rec) {
				row.cells[name] = cleanLegacyCell(rec[i])
			}
		}
		row = adjustLegacyRow(row, &report)
		rows = append(rows, row)
	}
	report.Checkups = len(rows)
	linkLegacyParents(rows, &report)
	order := orderParentsFirst(rows)

	f := excelize.NewFile()
	f.SetSheetName("Sheet1", patientSheetName)
	if _, err := f.NewSheet(mcuSheetName); err != nil {
		return nil, report, err
	}
	patients := legacyPatientRows(rows)
	report.Patients = len(patients)
	if err := writeImportSheet(f, patientSheetName, patientExportColumns, patients); err != nil {
		return nil, report, err
	}
	checkups := make([][]string, 0, len(order))
	for _, i := range order {
		checkups = append(checkups, legacyMCURow(rows[i]))
	}
	if err := writeImportSheet(f, mcuSheetName, legacyMCUColumns, checkups); err != nil {
		return nil, report, err
	}
	return f, report, nil
}

var legacyMCUColumns = append(append([]string{}, exportColumns...),
	"Timestamp", "HealthCertificateFileName", "ReviewYear", "SubmissionDate", "DaysToReview", "Gender", "Nationality",
	"BloodType",
)

type legacyCheckup struct {
	cells  map[string]string
	parent string
}

func cleanLegacyCell(s string) string {
	s = strings.TrimSpace(s)
	if s == `\N` || s == "_" {
		return ""
	}
	return s
}

func adjustLegacyRow(row legacyCheckup, report *OldSheetReport) legacyCheckup {
	if row.cells["CheckUpID"] == "None" {
		row.cells["CheckUpID"] = ""
		row.cells["_noneID"] = "1"
	}
	switch row.cells["Nationality"] {
	case "Phillipines":
		row.cells["Nationality"] = "Philippines"
		report.PhilippinesFixed++
	}
	switch row.cells["Company"] {
	case "#VALUE!":
		row.cells["Company"] = ""
		report.CompanyValueErrors++
	case "Yilybang Konstruksi Indonesia, YILKI":
		row.cells["Company"] = "Yilybang Konstruksi Indonesia"
		report.YilybangFixed++
	}
	if row.cells["ResultCriteria"] == "FitWithRestriction" {
		row.cells["ResultCriteria"] = "FitWithNote"
		if row.cells["AdditionalNotes"] == "" {
			row.cells["AdditionalNotes"] = "Imported result was Fit with restriction."
		}
		report.FitWithRestriction++
	}
	row.cells["Age"] = roundLegacyAge(strings.ReplaceAll(row.cells["Age"], ",", "."))
	for _, col := range []string{"Height", "Weight", "BMIValue"} {
		row.cells[col] = strings.ReplaceAll(row.cells[col], ",", ".")
	}
	row.cells["TaskType"] = canonicalTaskList(row.cells["TaskType"])
	for _, col := range []string{
		"BMICriteria", "BloodPressureCriteria", "Vision", "CBCCriteria", "LipidProfileCriteria",
		"BloodGlucoseCriteria", "LiverFunctionCriteria", "KidneyFunctionCriteria",
		"UrinalysisIncludingDrugTestCriteria", "Radiology", "HerniaHemorrhoidCriteria",
		"SpirometryCriteria", "AudiometryCriteria", "HepBSerology", "HepASerology",
		"RectalSwabCriteria", "HIVSerology",
	} {
		if mapped, ok := legacyCriteria[strings.ToLower(row.cells[col])]; ok {
			row.cells[col] = mapped
		}
	}
	if strings.EqualFold(row.cells["ValidityForReview"], "IsShortProject") {
		row.cells["_short"] = "yes"
		report.ShortProjects++
	} else {
		row.cells["_short"] = "no"
	}
	switch strings.ToUpper(row.cells["Terminal"]) {
	case "N":
		row.cells["_terminal"] = "no"
		report.TerminalSuperseded++
	default:
		row.cells["_terminal"] = "yes"
		report.TerminalCurrent++
	}
	return row
}

func roundLegacyAge(s string) string {
	if s == "" {
		return ""
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s
	}
	return strconv.FormatInt(int64(math.Round(f)), 10)
}

func canonicalTaskList(raw string) string {
	names := splitTaskCategories(raw)
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, canonicalTaskName(name))
	}
	return strings.Join(out, "; ")
}

func canonicalTaskName(name string) string {
	if alias, ok := legacyTaskAliases[strings.ToLower(name)]; ok {
		return alias
	}
	for _, official := range []string{
		"Business Travelers",
		"Confined space",
		"Confined Space or SCBA/Respirator Users",
		"Fire & Emergency Crew",
		"Food Handler",
		"Health care worker",
		"Heavy Equipment or Crane Operator",
		"Non Remote Site Worker/Jakarta Office",
		"Other Professional (Pilot, Seafarer, Diver)",
		"Professional Drivers",
		"Remote Site Visitor",
		"Remote Site Worker",
		"Respirator or SCBA",
		"Seafarer",
		"Shift Worker",
		"Work at Height",
		"Work in Extreme Temperature",
	} {
		if strings.EqualFold(official, name) {
			return official
		}
	}
	return name
}

func linkLegacyParents(rows []legacyCheckup, report *OldSheetReport) {
	maxMedC := 0
	ids := map[string]struct{}{}
	for _, row := range rows {
		id := row.cells["CheckUpID"]
		if id == "" {
			continue
		}
		ids[id] = struct{}{}
		if n, err := strconv.Atoi(strings.TrimPrefix(id, "MedC")); err == nil && strings.HasPrefix(id, "MedC") && n > maxMedC {
			maxMedC = n
		}
	}
	for i := range rows {
		if rows[i].cells["_noneID"] != "1" {
			continue
		}
		maxMedC++
		id := "MedC" + strconv.Itoa(maxMedC)
		rows[i].cells["CheckUpID"] = id
		ids[id] = struct{}{}
		if report.NoneID == "" {
			report.NoneID = id
		}
	}

	preds := map[string][]int{}
	for i, row := range rows {
		hc := row.cells["HCValidityStatus"]
		const prefix = "renewed by "
		if len(hc) >= len(prefix) && strings.HasPrefix(strings.ToLower(hc), prefix) {
			child := strings.TrimSpace(hc[len(prefix):])
			preds[child] = append(preds[child], i)
		}
	}
	for _, list := range preds {
		if len(list) > 1 {
			report.MultiPredecessor++
		}
	}
	for i := range rows {
		explicit := rows[i].cells["ParentCheckUpID"]
		list := preds[rows[i].cells["CheckUpID"]]
		parent := ""
		if explicit != "" {
			if _, ok := ids[explicit]; ok {
				parent = explicit
			}
		}
		if parent == "" && len(list) > 0 {
			best := list[0]
			for _, cand := range list[1:] {
				if rows[cand].cells["ReviewDate"] > rows[best].cells["ReviewDate"] {
					best = cand
				}
			}
			parent = rows[best].cells["CheckUpID"]
		} else if explicit != "" && len(list) > 0 {
			agrees := false
			for _, cand := range list {
				if rows[cand].cells["CheckUpID"] == explicit {
					agrees = true
				}
			}
			if !agrees {
				report.ParentDisagreements++
			}
		}
		if parent != "" && parent != rows[i].cells["CheckUpID"] {
			rows[i].parent = parent
			report.ParentsLinked++
		}
	}
}

func orderParentsFirst(rows []legacyCheckup) []int {
	index := map[string]int{}
	for i, row := range rows {
		index[row.cells["CheckUpID"]] = i
	}
	children := map[int][]int{}
	indegree := make([]int, len(rows))
	for i, row := range rows {
		p, ok := index[row.parent]
		if !ok || row.parent == "" {
			continue
		}
		children[p] = append(children[p], i)
		indegree[i]++
	}
	ready := []int{}
	for i := range rows {
		if indegree[i] == 0 {
			ready = append(ready, i)
		}
	}
	order := make([]int, 0, len(rows))
	for len(ready) > 0 {
		i := ready[0]
		ready = ready[1:]
		order = append(order, i)
		for _, child := range children[i] {
			indegree[child]--
			if indegree[child] == 0 {
				ready = append(ready, child)
			}
		}
	}
	if len(order) < len(rows) {
		seen := map[int]bool{}
		for _, i := range order {
			seen[i] = true
		}
		for i := range rows {
			if !seen[i] {
				order = append(order, i)
			}
		}
	}
	return order
}

func legacyPatientRows(rows []legacyCheckup) [][]string {
	type person struct {
		review string
		cells  map[string]string
	}
	people := map[string]*person{}
	var keys []string
	for _, row := range rows {
		name := strings.Join(strings.Fields(row.cells["Name"]), " ")
		if name == "" {
			continue
		}
		key := strings.ToLower(name) + "|" + row.cells["DateOfBirth"]
		cur, ok := people[key]
		if !ok {
			people[key] = &person{review: row.cells["ReviewDate"], cells: map[string]string{
				"Name": name, "DateOfBirth": row.cells["DateOfBirth"],
				"Gender": row.cells["Gender"], "Nationality": row.cells["Nationality"],
				"BloodType": row.cells["BloodType"], "Company": row.cells["Company"],
				"WorkPosition": row.cells["WorkPosition"],
			}}
			keys = append(keys, key)
			continue
		}
		newer := row.cells["ReviewDate"] >= cur.review
		for _, col := range []string{"Gender", "Nationality", "BloodType", "Company", "WorkPosition"} {
			if row.cells[col] == "" {
				continue
			}
			if newer || cur.cells[col] == "" {
				cur.cells[col] = row.cells[col]
			}
		}
		if newer {
			cur.review = row.cells["ReviewDate"]
			cur.cells["Name"] = name
			cur.cells["DateOfBirth"] = row.cells["DateOfBirth"]
		}
	}
	sort.Strings(keys)
	out := make([][]string, 0, len(keys))
	for _, key := range keys {
		p := people[key].cells
		out = append(out, []string{
			"", p["Name"], p["DateOfBirth"], p["Gender"], p["Nationality"],
			p["BloodType"], "", "", p["Company"], p["WorkPosition"],
		})
	}
	return out
}

func legacyMCURow(row legacyCheckup) []string {
	c := row.cells
	cert := c["HealthCertificateFileName"]
	hasCert := "No"
	if cert != "" {
		hasCert = "Yes"
	}
	values := map[string]string{
		"PatientName": c["Name"], "DateOfBirth": c["DateOfBirth"], "Company": c["Company"],
		"WorkPosition": c["WorkPosition"], "CheckUpCriteria": c["CheckUpCriteria"],
		"TaskCategory": c["TaskType"], "ReviewDate": c["ReviewDate"], "DateCheckUp": c["DateCheckUp"],
		"Reviewer": c["Reviewer"], "Age": c["Age"], "HeightCm": c["Height"], "WeightKg": c["Weight"],
		"IsShortProject": c["_short"], "ValidityPeriodMonths": c["ResultValidityPeriod"],
		"BmiValue": c["BMIValue"], "BmiCriteria": c["BMICriteria"], "BmiNotes": c["BMINotes"],
		"Smoking": c["SmokingVape"], "BloodType": c["BloodType"],
		"BpCriteria": c["BloodPressureCriteria"], "BpNotes": c["BloodPressureNotes"],
		"VisionCriteria": c["Vision"], "VisionNotes": c["VisionNotes"],
		"CbcCriteria": c["CBCCriteria"], "CbcNotes": c["CBCNotes"],
		"LipidCriteria": c["LipidProfileCriteria"], "LipidNotes": c["LipidProfileNotes"],
		"GlucoseCriteria": c["BloodGlucoseCriteria"], "GlucoseNotes": c["BloodGlucoseNotes"],
		"LiverCriteria": c["LiverFunctionCriteria"], "LiverNotes": c["LiverFunctionNotes"],
		"KidneyCriteria": c["KidneyFunctionCriteria"], "KidneyNotes": c["KidneyFunctionNotes"],
		"UrinalysisCriteria": c["UrinalysisIncludingDrugTestCriteria"], "UrinalysisNotes": c["UrynalisisNotes"],
		"RadiologyResult": c["Radiology"], "RadiologyNotes": c["RadiologyNotes"],
		"EcgResult": c["ECG"], "TreadmillResult": c["TreadmillTest"], "EcgTreadmillNotes": c["ECGTreadmillNotes"],
		"HerniaHemorrhoidCriteria": c["HerniaHemorrhoidCriteria"], "HerniaHemorrhoidNotes": c["HerniaHemorrhoidNotes"],
		"SpirometryCriteria": c["SpirometryCriteria"], "SpirometryNotes": c["SpirometryNotes"],
		"AudiometryCriteria": c["AudiometryCriteria"], "AudiometryNotes": c["AudiometryNotes"],
		"HepBCriteria": c["HepBSerology"], "HepBNotes": c["HepBSerologyNotes"],
		"HepACriteria": c["HepASerology"], "HepANotes": c["HepASErologyNotes"],
		"RectalSwabCriteria": c["RectalSwabCriteria"], "RectalSwabNotes": c["RectalSwabNotes"],
		"HivCriteria":    c["HIVSerology"],
		"ResultCriteria": c["ResultCriteria"], "ResultValidUntil": c["ResultValidUntil"],
		"ResultNotes": c["ResultNotes"], "Recommendation": c["RecommendationForFollowUp"],
		"AdditionalNotes": c["AdditionalNotes"], "CheckUpStatus": c["CheckUpStatus"],
		"ParentCheckUpID": row.parent, "HasCertificate": hasCert, "HcValidityStatus": c["HCValidityStatus"],
		"IsTerminal": c["_terminal"], "CheckUpID": c["CheckUpID"],
		"CompanyRelationCode":       c["Relation"],
		"Timestamp":                 legacyTimestamp(c["Timestamp"]),
		"HealthCertificateFileName": cert, "ReviewYear": c["YearReview"],
		"SubmissionDate": c["SubmissionDate"], "DaysToReview": c["DaysToReview"],
		"Gender": c["Gender"], "Nationality": c["Nationality"],
	}
	out := make([]string, len(legacyMCUColumns))
	for i, name := range legacyMCUColumns {
		out[i] = values[name]
	}
	return out
}

func legacyTimestamp(s string) string {
	if s == "" {
		return ""
	}
	return parseImportTimestamp(s)
}

func writeImportSheet(f *excelize.File, sheet string, header []string, rows [][]string) error {
	if err := f.SetSheetRow(sheet, "A1", &header); err != nil {
		return err
	}
	for i, row := range rows {
		cell, err := excelize.CoordinatesToCellName(1, i+2)
		if err != nil {
			return err
		}
		if err := f.SetSheetRow(sheet, cell, &row); err != nil {
			return err
		}
	}
	return nil
}
