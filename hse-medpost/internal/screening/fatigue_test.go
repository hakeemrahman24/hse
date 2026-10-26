package screening

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestSubmitFatigueStoresOnceAndAcceptsNumbers(t *testing.T) {
	database, staffID := openScreeningTestDB(t)
	api := newAPI(database)
	token := login(t, api)
	body := map[string]any{
		"form":        formFatigue,
		"formVersion": "20271006",
		"records": []any{
			// The form sends scores as numbers and blanks as null.
			map[string]any{"id": "f-1", "name": "Budi", "score_24h": 3, "score_48h": nil, "score_total": 7, "risk_level": "medium"},
			map[string]any{"id": ""},                              // invalid: missing id
			map[string]any{"id": "f-2", "name": map[string]any{}}, // invalid: object where text is expected
		},
	}

	first := postJSON(t, api.SubmitFatigue, "/api/screening/fatigue", body, token)
	if first.Code != http.StatusOK {
		t.Fatalf("submit status = %d body = %s", first.Code, first.Body.String())
	}
	var out struct {
		Accepted []string `json:"accepted"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Accepted) != 1 || out.Accepted[0] != "f-1" {
		t.Fatalf("accepted = %v, want [f-1]", out.Accepted)
	}

	if rec := postJSON(t, api.SubmitFatigue, "/api/screening/fatigue", body, token); rec.Code != http.StatusOK {
		t.Fatalf("retry status = %d body = %s", rec.Code, rec.Body.String())
	}
	var count int
	var owner int64
	var score24h, score48h, total string
	err := database.QueryRow(`SELECT COUNT(*), MAX(FK_StaffID), MAX(Score24h), MAX(Score48h), MAX(ScoreTotal)
		FROM FatigueScreening WHERE ClientID = 'f-1'`).Scan(&count, &owner, &score24h, &score48h, &total)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("rows for f-1 = %d, want 1 (retry must not duplicate)", count)
	}
	if owner != staffID {
		t.Fatalf("FK_StaffID = %d, want %d", owner, staffID)
	}
	if score24h != "3" || score48h != "" || total != "7" {
		t.Fatalf("scores = %q %q %q, want \"3\" \"\" \"7\"", score24h, score48h, total)
	}
}

func TestSubmitFatigueRejectsWrongForm(t *testing.T) {
	database, _ := openScreeningTestDB(t)
	api := newAPI(database)
	token := login(t, api)
	body := map[string]any{"form": formDrugAlcohol, "formVersion": "20270228", "records": []any{map[string]string{"id": "x"}}}
	rec := postJSON(t, api.SubmitFatigue, "/api/screening/fatigue", body, token)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
