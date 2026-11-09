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
	Dimensions []map[string]string `json:"dimensions"`
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

func countItemRows(t *testing.T, api *API) int {
	t.Helper()
	var n int
	if err := api.DB.QueryRow(`SELECT COUNT(*) FROM SofiItem`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
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
	if n := countItemRows(t, api); n != 0 {
		t.Fatalf("rows left after reset: %d", n)
	}
}

func TestSavingTheBuiltInWordingLeavesNoOverride(t *testing.T) {
	api := newTestAPI(t)
	if rec := putItem(api, "2", `{"en":"`+Items[1].EN+`","id":"`+Items[1].ID+`","zh":""}`); rec.Code != http.StatusOK {
		t.Fatalf("save: %d", rec.Code)
	}
	if n := countItemRows(t, api); n != 0 {
		t.Fatalf("rows = %d, want none", n)
	}
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
	if n := countItemRows(t, api); n != 0 {
		t.Fatalf("a refused edit saved a row: %d", n)
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
