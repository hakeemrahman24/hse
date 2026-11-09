package sofi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xuri/excelize/v2"
)

// The export follows the table's filters, shows times in GMT+9, and has the five dimension
// scores as their own columns.
func TestStaffExportHasOneRowPerMatchingSurvey(t *testing.T) {
	api := newTestAPI(t)
	andi := patientID(t, api, "Andi Pratama")
	budi := patientID(t, api, "Andini Putri")
	addSurvey(t, api, andi, "PT A", "Welder", "2026-10-05T01:30:00", 2) // 10:30 GMT+9
	addSurvey(t, api, budi, "PT B", "Cook", "2026-10-06T01:30:00", 5)

	get := func(query string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		api.StaffExport(rec, httptest.NewRequest(http.MethodGet, "/api/sofi-log.xlsx?"+query, nil))
		return rec
	}
	rec := get("q=pratama")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Errorf("content type %q", ct)
	}
	f, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := f.GetRows(exportSheet)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d (header + 1 expected): %v", len(rows), rows)
	}
	if rows[1][0] != "2026-10-05 10:30" || rows[1][1] != "Andi Pratama" || rows[1][3] != "PT A" {
		t.Errorf("row = %v", rows[1])
	}
	if want := 7 + len(Dimensions) + 2; len(rows[0]) != want {
		t.Errorf("header has %d columns, want %d", len(rows[0]), want)
	}

	all, _ := excelize.OpenReader(bytes.NewReader(get("").Body.Bytes()))
	defer all.Close()
	if rows, _ := all.GetRows(exportSheet); len(rows) != 3 {
		t.Errorf("unfiltered export has %d rows, want 3", len(rows))
	}
	if code := get("window=bogus").Code; code != http.StatusBadRequest {
		t.Errorf("bad window = %d, want 400", code)
	}
}
