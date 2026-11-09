package sofi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type itemsBody struct {
	Items []struct {
		Number  int    `json:"number"`
		EN      string `json:"en"`
		ID      string `json:"id"`
		ZH      string `json:"zh"`
		Custom  bool   `json:"custom"`
		Default struct {
			EN string `json:"en"`
		} `json:"default"`
	} `json:"items"`
	Dimensions []struct {
		Key    string `json:"key"`
		EN     string `json:"en"`
		ID     string `json:"id"`
		Custom bool   `json:"custom"`
		Items  []int  `json:"items"`
	} `json:"dimensions"`
}

func putItem(api *API, number, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/api/sofi-items/"+number, strings.NewReader(body))
	req.SetPathValue("number", number)
	rec := httptest.NewRecorder()
	api.AdminPutItem(rec, req)
	return rec
}

func getItems(t *testing.T, api *API) itemsBody {
	t.Helper()
	rec := httptest.NewRecorder()
	api.AdminItems(rec, httptest.NewRequest(http.MethodGet, "/api/sofi-items", nil))
	var out itemsBody
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("items: %d %s", rec.Code, rec.Body.String())
	}
	return out
}

func TestItemsListHasTwentyItemsWithTheBuiltInWording(t *testing.T) {
	out := getItems(t, newTestAPI(t))
	if len(out.Items) != ItemCount || len(out.Dimensions) != len(Dimensions) {
		t.Fatalf("%d items, %d dimensions", len(out.Items), len(out.Dimensions))
	}
	if first := out.Items[0]; first.EN != "Having too many activities to do" || first.Custom {
		t.Fatalf("first item = %+v", first)
	}
}

// A reworded item shows on the worker's form, scoring is untouched, and blanking brings the
// built-in wording back.
func TestRewordedItemShowsOnTheFormAndCanBeReset(t *testing.T) {
	api := newTestAPI(t)
	rec := putItem(api, "1", `{"en":"Too much work","id":"","zh":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	first := getItems(t, api).Items[0]
	if first.EN != "Too much work" || !first.Custom || first.Default.EN != "Having too many activities to do" || first.ID != Items[0].ID {
		t.Fatalf("after save = %+v", first)
	}
	if body := startPage(t, api, LangEN); !strings.Contains(body, "Too much work") || strings.Contains(body, ">Having too many activities to do<") {
		t.Error("the form does not show the new wording")
	}
	if r, err := Score(answers(3)); err != nil || r.Total != 60 {
		t.Fatalf("score changed: %+v %v", r, err)
	}

	if rec := putItem(api, "1", `{"en":"","id":"","zh":""}`); rec.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body.String())
	}
	if first := getItems(t, api).Items[0]; first.EN != "Having too many activities to do" || first.Custom {
		t.Fatalf("after reset = %+v", first)
	}
	if got := storedItemText(t, api, 1); got != Items[0].EN {
		t.Fatalf("stored English after reset = %q, want the built-in %q", got, Items[0].EN)
	}
}

// A blank language is saved as the built-in text, so a row is always complete.
func TestBlankLanguageIsStoredAsTheBuiltInText(t *testing.T) {
	api := newTestAPI(t)
	if rec := putItem(api, "2", `{"en":"Custom English","id":"","zh":""}`); rec.Code != http.StatusOK {
		t.Fatalf("save: %d", rec.Code)
	}
	var en, id, zh string
	if err := api.DB.QueryRow(`SELECT TextEN, TextID, TextZH FROM SofiItem WHERE ItemNumber = 2`).Scan(&en, &id, &zh); err != nil {
		t.Fatal(err)
	}
	if en != "Custom English" || id != Items[1].ID || zh != Items[1].ZH {
		t.Fatalf("stored = %q / %q / %q", en, id, zh)
	}
}

func storedItemText(t *testing.T, api *API, n int) string {
	t.Helper()
	var en string
	if err := api.DB.QueryRow(`SELECT TextEN FROM SofiItem WHERE ItemNumber = ?`, n).Scan(&en); err != nil {
		t.Fatal(err)
	}
	return en
}

func TestItemEditRefusesBadInput(t *testing.T) {
	api := newTestAPI(t)
	for name, c := range map[string]struct{ number, body string }{
		"no such item": {"21", `{"en":"x"}`},
		"not a number": {"abc", `{"en":"x"}`},
		"too long":     {"1", `{"en":"` + strings.Repeat("a", maxItemTextRunes+1) + `"}`},
		"line break":   {"1", `{"en":"a\nb"}`},
		"not json":     {"1", `nope`},
	} {
		if rec := putItem(api, c.number, c.body); rec.Code < 400 {
			t.Errorf("%s: status %d", name, rec.Code)
		}
	}
	if got := storedItemText(t, api, 1); got != Items[0].EN {
		t.Fatalf("a refused edit changed item 1 to %q", got)
	}
}

// MariaDB migrations are run by hand, so the table can be missing for a while after a deploy: the
// form must keep working with the built-in wording and the editor must say it could not save.
func TestWithoutItsTableTheFormKeepsTheBuiltInWording(t *testing.T) {
	api := newTestAPI(t)
	if _, err := api.DB.Exec(`DROP TABLE SofiItem`); err != nil { // a throwaway test database
		t.Fatal(err)
	}
	if body := startPage(t, api, LangEN); !strings.Contains(body, "Having too many activities to do") {
		t.Fatal("the form lost its items with no SofiItem table")
	}
	if rec := putItem(api, "1", `{"en":"x"}`); rec.Code != http.StatusInternalServerError {
		t.Fatalf("save without the table: %d", rec.Code)
	}
}

func putDimension(api *API, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/api/sofi-dimensions/"+key, strings.NewReader(body))
	req.SetPathValue("key", key)
	rec := httptest.NewRecorder()
	api.AdminPutDimension(rec, req)
	return rec
}

// The dimension names are the clinic's Indonesian names with English translations, and each
// dimension lists its four items.
func TestDimensionsListHasTheClinicNamesAndTheirItems(t *testing.T) {
	out := getItems(t, newTestAPI(t))
	if len(out.Dimensions) != 5 {
		t.Fatalf("%d dimensions", len(out.Dimensions))
	}
	first := out.Dimensions[0]
	if first.Key != "lack_of_energy" || first.EN != "Lack of Energy at Work" || first.ID != "Kekurangan energi pada Pekerjaan" || len(first.Items) != 4 || first.Items[0] != 1 {
		t.Fatalf("first dimension = %+v", first)
	}
}

// A renamed dimension shows wherever a result is shown (staff table, worker's list, summary, export
// header); scores and levels do not move; resetting brings the built-in name back.
func TestRenamedDimensionShowsEverywhereAndCanBeReset(t *testing.T) {
	api := newTestAPI(t)
	andi := patientID(t, api, "Andi Pratama")
	addSurvey(t, api, andi, "PT A", "Welder", "2026-10-01T08:00:00", 3)
	if rec := putDimension(api, "sleepiness", `{"en":"Sleepiness at work","id":"","zh":""}`); rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body.String())
	}

	rec := httptest.NewRecorder()
	api.StaffList(rec, httptest.NewRequest(http.MethodGet, "/api/sofi-log", nil))
	var rows []struct {
		Dimensions []struct {
			EN    string `json:"en"`
			Score int    `json:"score"`
		} `json:"dimensions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("list: %v %s", err, rec.Body.String())
	}
	if last := rows[0].Dimensions[4]; last.EN != "Sleepiness at work" || last.Score != 12 {
		t.Errorf("staff row dimension = %+v", last)
	}
	if _, s := getSummary(t, api, ""); s.Dimensions[4].Key != "sleepiness" {
		t.Errorf("summary dimension = %+v", s.Dimensions[4])
	}
	// The result a worker sees right after submitting carries the new name too.
	if sub := submit(api, signedInCookie(t, api), `{"weeksOnSite":12,"answers":`+answersJSON(2)+`}`); !strings.Contains(sub.Body.String(), "Sleepiness at work") {
		t.Errorf("the submit result lacks the new name: %s", sub.Body.String())
	}
	if rec := httptest.NewRecorder(); true {
		api.StaffExport(rec, httptest.NewRequest(http.MethodGet, "/api/sofi-log.xlsx", nil))
		if rec.Code != http.StatusOK {
			t.Errorf("export %d", rec.Code)
		}
	}

	if rec := putDimension(api, "sleepiness", `{"en":"","id":"","zh":""}`); rec.Code != http.StatusOK {
		t.Fatalf("reset: %d", rec.Code)
	}
	if got := getItems(t, api).Dimensions[4]; got.EN != "Feeling of Sleepiness" || got.Custom {
		t.Fatalf("after reset = %+v", got)
	}
	if rec := putDimension(api, "nope", `{"en":"x"}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown dimension: %d", rec.Code)
	}
}
