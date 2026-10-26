package emr

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func (f *fixture) listNames(t *testing.T, query string) []string {
	t.Helper()
	rec := f.do(t, f.api.List, http.MethodGet, "/api/malaria-screenings"+query, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d: %s", query, rec.Code, rec.Body.String())
	}
	var rows []struct {
		PatientName string `json:"patientName"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, r := range rows {
		names = append(names, r.PatientName)
	}
	return names
}

func TestListSearchesByPatientName(t *testing.T) {
	f := newFixture(t)
	f.seedSummary(t) // three for Budi Santoso, one for Siti Aminah
	cases := map[string]int{
		"?q=siti":                               1,
		"?q=BUDI":                               3,
		"?q=" + url.QueryEscape("santoso budi"): 3, // every word, any order
		"?q=" + url.QueryEscape("  budi  "):     3,
		"?q=zzz":                                0,
		"?q=%25":                                0, // a literal % is not a wildcard
		"?q=b_di":                               0, // nor is _
		"?q=budi&from=2026-10-15":               2, // together with the date filter
		"":                                      4,
	}
	for query, want := range cases {
		if got := f.listNames(t, query); len(got) != want {
			t.Errorf("%q: %d rows %v, want %d", query, len(got), got, want)
		}
	}
}

func (f *fixture) export(t *testing.T, query string) [][]string {
	t.Helper()
	rec := f.do(t, f.api.Export, http.MethodGet, "/api/malaria-screenings.xlsx"+query, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Fatalf("content type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, `attachment; filename="malaria screening - `) || !strings.HasSuffix(cd, `.xlsx"`) {
		t.Fatalf("content disposition = %q", cd)
	}
	book, err := excelize.OpenReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("not a readable workbook: %v", err)
	}
	defer book.Close()
	rows, err := book.GetRows(book.GetSheetList()[0])
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestExportHasEveryColumnNewestFirst(t *testing.T) {
	f := newFixture(t)
	f.seedSummary(t)
	rows := f.export(t, "")
	wantHeader := []string{"Test ID", "Test date", "Week", "Slide initial", "Name", "Age", "Weight (kg)", "Head of household", "Head is the patient",
		"Regency", "District", "Village", "Regency code", "District code", "Village code",
		"Work position", "Company", "Examiner", "Screening purpose", "Exam type", "Result", "Recorded at (UTC)"}
	if strings.Join(rows[0], "|") != strings.Join(wantHeader, "|") {
		t.Fatalf("header = %v", rows[0])
	}
	if len(rows) != 5 {
		t.Fatalf("rows = %d, want header + 4", len(rows))
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[h] = i
	}
	// Newest test date first, then the later record: ids 4, 3, 1 on the 20th, then 2 (the 10th).
	var ids []string
	for _, r := range rows[1:] {
		ids = append(ids, r[col["Test ID"]])
	}
	if strings.Join(ids, " ") != "MAL-00004 MAL-00003 MAL-00001 MAL-00002" {
		t.Fatalf("test ids = %v", ids)
	}
	last := rows[4]
	if last[col["Test date"]] != "2026-10-10" || last[col["Result"]] != "Pf" || last[col["Week"]] != "2026-W41" {
		t.Fatalf("oldest row = %v", last)
	}
	var city []string
	for _, r := range rows[1:] {
		if r[col["Regency code"]] == "94.71" {
			city = r
		}
	}
	if city == nil || city[col["Name"]] != "Siti Aminah" || city[col["Village"]] != "Asano" || city[col["District code"]] != "94.71.01" ||
		city[col["Head of household"]] != "Siti Aminah" || city[col["Weight (kg)"]] != "61.5" || city[col["Recorded at (UTC)"]] != "2026-10-20T03:00:00" {
		t.Fatalf("city row = %v", city)
	}
	pending := 0
	for _, r := range rows[1:] {
		if r[col["Result"]] == "Pending" {
			pending++
		}
	}
	if pending != 1 {
		t.Fatalf("pending rows = %d, want 1 (shown as the word Pending)", pending)
	}
}

func TestExportFollowsTheSearchAndTheDates(t *testing.T) {
	f := newFixture(t)
	f.seedSummary(t)
	if rows := f.export(t, "?q=siti"); len(rows) != 2 {
		t.Fatalf("q=siti: %d rows, want header + 1", len(rows))
	}
	if rows := f.export(t, "?q=budi&from=2026-10-15"); len(rows) != 3 {
		t.Fatalf("q=budi from 15th: %d rows, want header + 2", len(rows))
	}
	if rows := f.export(t, "?q=zzz"); len(rows) != 1 {
		t.Fatalf("no match: %d rows, want just the header", len(rows))
	}
}

func TestExportRejectsBadDates(t *testing.T) {
	f := newFixture(t)
	rec := f.do(t, f.api.Export, http.MethodGet, "/api/malaria-screenings.xlsx?from=garbage", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}
