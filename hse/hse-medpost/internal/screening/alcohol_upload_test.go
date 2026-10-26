package screening

import (
	"encoding/json"
	"net/http"
	"testing"
)

// A row shaped like the form's toRow(): text, true/false checkboxes, radio
// answers, and the two panel objects.
func fullAlcoholRow() map[string]any {
	return map[string]any{
		"id":                        "a-full-1",
		"created_at":                "2026-10-07T09:00:00.000Z",
		"form_version":              "20270228",
		"donor_name":                "Budi",
		"donor_nik":                 "0123456789",
		"donor_company":             "PT Contoh",
		"donor_position":            "Operator",
		"consent_at":                "2026-10-07 08:55",
		"donor_sign_name":           "Budi",
		"witness_name":              "Sari",
		"test_type":                 "random",
		"med_none":                  false,
		"med1_name":                 "Paracetamol",
		"med1_dose":                 "500 mg, 3x",
		"med1_reason":               "headache",
		"med1_prescribed":           "yes",
		"med2_name":                 "",
		"med2_dose":                 "",
		"med2_reason":               "",
		"med2_prescribed":           "",
		"breath_device":             "Alcolmeter X",
		"breath_calibration_until":  "2027-01-31",
		"breath1_time":              "09:10",
		"breath1_reading":           "0.00",
		"breath1_mouthpiece_sealed": true,
		"breath1_result":            "negative",
		"breath2_time":              "09:12",
		"breath2_reading":           "0.00",
		"breath2_mouthpiece_sealed": true,
		"breath2_result":            "negative",
		"specimen_temp_ok":          true,
		"specimen_colour_ok":        true,
		"specimen_temp_value":       "36.5",
		"panel_negative":            map[string]any{"amp": true, "met": true, "thc": true, "coc": true, "mop": true, "bzo": true},
		"panel_nonnegative":         map[string]any{"amp": false, "met": false, "thc": false, "coc": false, "mop": false, "bzo": false},
		"final_result":              "negative",
		"remarks":                   "",
		"collector_name":            "Dewi",
		"security_name":             "Agus",
		"ack_name":                  "Rina",
	}
}

func TestSubmitAlcoholStoresEveryFormField(t *testing.T) {
	database, staffID := openScreeningTestDB(t)
	api := newAPI(database)
	token := login(t, api)
	body := map[string]any{
		"form":        formDrugAlcohol,
		"formVersion": "20270228",
		"records":     []any{fullAlcoholRow()},
	}

	rec := postJSON(t, api.SubmitDrugAlcohol, "/api/screening/drug-alcohol", body, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit status = %d body = %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Accepted []string `json:"accepted"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Accepted) != 1 || out.Accepted[0] != "a-full-1" {
		t.Fatalf("accepted = %v, want [a-full-1]", out.Accepted)
	}

	checks := map[string]string{
		"DonorSignName":           "Budi",
		"WitnessName":             "Sari",
		"MedNone":                 "false",
		"Med1Name":                "Paracetamol",
		"Med1Dose":                "500 mg, 3x",
		"Med1Prescribed":          "yes",
		"BreathDevice":            "Alcolmeter X",
		"BreathCalibrationUntil":  "2027-01-31",
		"Breath1MouthpieceSealed": "true",
		"Breath1Result":           "negative",
		"Breath2MouthpieceSealed": "true",
		"SpecimenTempOk":          "true",
		"SpecimenTempValue":       "36.5",
		"CollectorName":           "Dewi",
		"SecurityName":            "Agus",
		"AckName":                 "Rina",
		"FinalResult":             "negative",
		"ClientID":                "a-full-1",
	}
	for column, want := range checks {
		var got string
		if err := database.QueryRow(`SELECT ` + column + ` FROM DrugAlcoholScreening WHERE ClientID = 'a-full-1'`).Scan(&got); err != nil {
			t.Fatalf("%s: %v", column, err)
		}
		if got != want {
			t.Errorf("%s = %q, want %q", column, got, want)
		}
	}

	var panelNeg, panelNonneg string
	if err := database.QueryRow(`SELECT PanelNegative, PanelNonnegative FROM DrugAlcoholScreening WHERE ClientID = 'a-full-1'`).Scan(&panelNeg, &panelNonneg); err != nil {
		t.Fatal(err)
	}
	var negMap, nonnegMap map[string]bool
	if err := json.Unmarshal([]byte(panelNeg), &negMap); err != nil || !negMap["amp"] || !negMap["bzo"] {
		t.Fatalf("PanelNegative = %q (err %v), want all six drugs true", panelNeg, err)
	}
	if err := json.Unmarshal([]byte(panelNonneg), &nonnegMap); err != nil || nonnegMap["amp"] {
		t.Fatalf("PanelNonnegative = %q (err %v), want all six drugs false", panelNonneg, err)
	}

	var owner int64
	if err := database.QueryRow(`SELECT FK_StaffID FROM DrugAlcoholScreening WHERE ClientID = 'a-full-1'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != staffID {
		t.Fatalf("FK_StaffID = %d, want %d", owner, staffID)
	}
}

func TestSubmitAlcoholRejectsOversizedField(t *testing.T) {
	database, _ := openScreeningTestDB(t)
	api := newAPI(database)
	token := login(t, api)
	row := fullAlcoholRow()
	row["remarks"] = string(make([]rune, maxFieldLength+1))
	body := map[string]any{"form": formDrugAlcohol, "formVersion": "20270228", "records": []any{row}}

	rec := postJSON(t, api.SubmitDrugAlcohol, "/api/screening/drug-alcohol", body, token)
	var out struct {
		Accepted []string `json:"accepted"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Accepted) != 0 {
		t.Fatalf("accepted = %v, want none for an oversized remarks field", out.Accepted)
	}
}
