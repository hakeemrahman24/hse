package screening

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"hse-medpost/internal/dates"
)

const (
	maxFieldLength = 500
	maxIDLength    = 64
)

// drugAlcoholField maps one key in the form's upload row to its column in
// DrugAlcoholScreening. Keys match the form's toRow(). A field added to the
// form needs a row here, a column in drugAlcoholAddedColumns (and the MariaDB
// migration), and a column in logSheets.
type drugAlcoholField struct {
	key    string
	column string
}

var drugAlcoholFields = []drugAlcoholField{
	{"id", "ClientID"},
	{"created_at", "CreatedAt"},
	{"donor_name", "DonorName"},
	{"donor_nik", "DonorNik"},
	{"donor_company", "DonorCompany"},
	{"donor_position", "DonorPosition"},
	{"consent_at", "ConsentAt"},
	{"donor_sign_name", "DonorSignName"},
	{"witness_name", "WitnessName"},
	{"test_type", "TestType"},
	{"med_none", "MedNone"},
	{"med1_name", "Med1Name"},
	{"med1_dose", "Med1Dose"},
	{"med1_reason", "Med1Reason"},
	{"med1_prescribed", "Med1Prescribed"},
	{"med2_name", "Med2Name"},
	{"med2_dose", "Med2Dose"},
	{"med2_reason", "Med2Reason"},
	{"med2_prescribed", "Med2Prescribed"},
	{"breath_device", "BreathDevice"},
	{"breath_calibration_until", "BreathCalibrationUntil"},
	{"breath1_time", "Breath1Time"},
	{"breath1_reading", "Breath1Reading"},
	{"breath1_mouthpiece_sealed", "Breath1MouthpieceSealed"},
	{"breath1_result", "Breath1Result"},
	{"breath2_time", "Breath2Time"},
	{"breath2_reading", "Breath2Reading"},
	{"breath2_mouthpiece_sealed", "Breath2MouthpieceSealed"},
	{"breath2_result", "Breath2Result"},
	{"specimen_temp_ok", "SpecimenTempOk"},
	{"specimen_colour_ok", "SpecimenColourOk"},
	{"specimen_temp_value", "SpecimenTempValue"},
	{"panel_negative", "PanelNegative"},
	{"panel_nonnegative", "PanelNonnegative"},
	{"final_result", "FinalResult"},
	{"remarks", "Remarks"},
	{"collector_name", "CollectorName"},
	{"security_name", "SecurityName"},
	{"ack_name", "AckName"},
}

// drugAlcoholAddedColumns are the columns for form fields that were not in the
// first release. Every one is text, like the existing columns.
// migrations/mysql/015_alcohol_fields.sql creates the same list for MariaDB.
var drugAlcoholAddedColumns = []string{
	"DonorSignName", "WitnessName", "MedNone",
	"Med1Name", "Med1Dose", "Med1Reason", "Med1Prescribed",
	"Med2Name", "Med2Dose", "Med2Reason", "Med2Prescribed",
	"BreathDevice", "BreathCalibrationUntil",
	"Breath1Time", "Breath1MouthpieceSealed",
	"Breath2Time", "Breath2MouthpieceSealed",
	"SpecimenTempOk", "SpecimenColourOk", "SpecimenTempValue",
	"PanelNegative", "PanelNonnegative",
	"CollectorName", "SecurityName", "AckName",
}

var errInvalidRecord = errors.New("invalid record")

// fieldText turns one JSON value into the text stored in its column. A string
// is unquoted, a number or boolean is written as it is ("true" or "false"),
// a missing or null value is "", and an object or array (the panel results)
// is stored as compact JSON.
func fieldText(v json.RawMessage) (string, error) {
	s := bytes.TrimSpace(v)
	switch {
	case len(s) == 0 || string(s) == "null":
		return "", nil
	case s[0] == '"':
		var out string
		if err := json.Unmarshal(s, &out); err != nil {
			return "", errInvalidRecord
		}
		return out, nil
	case s[0] == '{' || s[0] == '[':
		var buf bytes.Buffer
		if err := json.Compact(&buf, s); err != nil {
			return "", errInvalidRecord
		}
		return buf.String(), nil
	default:
		return string(s), nil
	}
}

// parseRecord turns one upload row into column values, keyed by the form's
// key. It rejects a row with a missing or oversized id, or any value longer
// than maxFieldLength. Rejected rows are not stored and are not reported as
// accepted.
func parseRecord(raw json.RawMessage) (map[string]string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, errInvalidRecord
	}
	values := make(map[string]string, len(drugAlcoholFields))
	for _, f := range drugAlcoholFields {
		text, err := fieldText(fields[f.key])
		if err != nil {
			return nil, err
		}
		if utf8.RuneCountInString(text) > maxFieldLength {
			return nil, errInvalidRecord
		}
		values[f.key] = text
	}
	// The browser sends toISOString() (24 characters, milliseconds, "Z").
	// Store the shared 19-character UTC layout instead, so every timestamp
	// column reads the same; RawJSON keeps the original. A value that is not a
	// timestamp makes the record invalid, like any other bad field.
	createdAt, err := dates.NormalizeTimestamp(values["created_at"])
	if err != nil {
		return nil, errInvalidRecord
	}
	values["created_at"] = createdAt
	if values["id"] == "" || utf8.RuneCountInString(values["id"]) > maxIDLength {
		return nil, errInvalidRecord
	}
	return values, nil
}

// insert stores one record unless its ClientID is already present. A repeat
// is reported as success, so the form can clear it from the queue. Rows are
// attributed to the uploading staff member through FK_StaffID.
func (a *API) insert(staffID int64, formVersion string, values map[string]string, raw json.RawMessage) error {
	var existing int
	if err := a.DB.QueryRow(`SELECT COUNT(*) FROM DrugAlcoholScreening WHERE ClientID = ?`, values["id"]).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	columns := []string{"FK_StaffID", "FormVersion", "SubmittedAt", "RawJSON"}
	args := []any{staffID, formVersion, dates.Stamp(a.now()), string(raw)}
	for _, f := range drugAlcoholFields {
		columns = append(columns, f.column)
		args = append(args, values[f.key])
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(columns)), ", ")
	query := fmt.Sprintf("INSERT INTO DrugAlcoholScreening (%s) VALUES (%s)",
		strings.Join(columns, ", "), placeholders)
	_, err := a.DB.Exec(query, args...)
	return err
}

// EnsureDrugAlcoholColumns adds the added columns to a SQLite database that
// was created before them. SQLite's ADD COLUMN cannot be repeated, so each
// column is checked first. MariaDB is migrated by hand (see the README).
func EnsureDrugAlcoholColumns(database *sql.DB) error {
	return ensureTextColumns(database, "DrugAlcoholScreening", drugAlcoholAddedColumns)
}
