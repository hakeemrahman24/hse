package entrymcu

import (
	"bytes"
	"compress/zlib"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDashboardCohortAndFilename(t *testing.T) {
	if got := dashboardPDFFilename("2026-09-01", "2026-09-27"); got != "MCU_dashboard_2026-09-01_2026-09-27.pdf" {
		t.Fatalf("filename = %s", got)
	}
	if got := dashboardPDFFilename("", "  "); got != "MCU_dashboard_na_na.pdf" {
		t.Fatalf("blank filename = %s", got)
	}

	got := buildDashboard([]dashSrc{
		{Company: "Acme", Criteria: "Annual", Status: "Close", Result: "NeedFollowUp"},
		{Company: "Acme", Criteria: "Annual", Status: "Close", Result: "Fit", HC: "Renewed by MedC2"},
		{Company: "Acme", Criteria: "Annual", Status: "Close", Result: "Fit"},
		{Company: "Beta", Criteria: "Pre Employment", Status: "Open"},
		{Company: "Beta", Criteria: "Pre Employment", Status: "Close"},
	})
	if got.ClosedNeedFollowUpExcludedCount != 1 || got.RenewedExcludedCount != 1 || got.Total != 3 {
		t.Fatalf("cohort total=%d nfu=%d renewed=%d", got.Total, got.ClosedNeedFollowUpExcludedCount, got.RenewedExcludedCount)
	}
	if got.ClosedCount != 2 || got.ClosedNoResultCriteriaCount != 1 || got.ResultCriteriaTotal != 1 {
		t.Fatalf("closed=%d noResult=%d results=%d", got.ClosedCount, got.ClosedNoResultCriteriaCount, got.ResultCriteriaTotal)
	}
	if len(got.ResultCriteriaRows) != 1 || got.ResultCriteriaRows[0].Label != "Fit" || got.ResultCriteriaRows[0].Color != "#2ea043" {
		t.Fatalf("result rows = %+v", got.ResultCriteriaRows)
	}
	if len(got.Companies) != 2 || got.Companies[0] != "Beta" || got.Companies[1] != "Acme" {
		t.Fatalf("companies = %v", got.Companies)
	}
	filtered := filterDashboard(got, "Acme")
	if filtered.Total != 1 || len(filtered.CompanyPivot) != 1 {
		t.Fatalf("filtered = %+v", filtered)
	}
}

func TestDashboardResultsCountEveryStatus(t *testing.T) {
	got := buildDashboard([]dashSrc{
		{Company: "Acme", Status: "Open", Result: "NeedFollowUp"},
		{Company: "Acme", Status: "Open", Result: "TemporaryUnfit"},
		{Company: "Acme", Status: "Close", Result: "Unfit"},
		{Company: "Beta", Status: "Open", Result: "Fit"},
		{Company: "Beta", Status: "Close"},
	})
	if got.ResultCriteriaTotal != 4 || got.ClosedNoResultCriteriaCount != 1 {
		t.Fatalf("results=%d noResult=%d", got.ResultCriteriaTotal, got.ClosedNoResultCriteriaCount)
	}
	keys := []string{}
	for _, r := range got.ResultCriteriaRows {
		keys = append(keys, r.Key)
	}
	if strings.Join(keys, ",") != "Fit,NeedFollowUp,TemporaryUnfit,Unfit" {
		t.Fatalf("result keys = %v", keys)
	}
	if f := filterDashboard(got, "Beta"); f.ClosedNoResultCriteriaCount != 1 || f.ResultCriteriaTotal != 1 {
		t.Fatalf("filtered = %+v", f)
	}
}

func TestDashboardSkipsSupersededOpenRecords(t *testing.T) {
	got := buildDashboard([]dashSrc{
		{Company: "Acme", Status: "Open", Result: "NeedFollowUp", Superseded: true},
		{Company: "Acme", Status: "Close", Result: "Fit"},
		{Company: "Acme", Status: "Open", Result: "TemporaryUnfit"},
	})
	if got.Total != 2 || got.ResultCriteriaTotal != 2 || got.SupersededExcludedCount != 1 {
		t.Fatalf("total=%d results=%d superseded=%d", got.Total, got.ResultCriteriaTotal, got.SupersededExcludedCount)
	}
	for _, r := range got.ResultCriteriaRows {
		if r.Key == "NeedFollowUp" {
			t.Fatalf("superseded open record counted: %+v", got.ResultCriteriaRows)
		}
	}
}

func TestDashboardMergesConsultationResultPurposes(t *testing.T) {
	got := buildDashboard([]dashSrc{
		{Company: "Acme", Criteria: "Fit for Task", Status: "Close", Result: "Fit"},
		{Company: "Acme", Criteria: "Follow Up", Status: "Open"},
		{Company: "Acme", Criteria: "FitForTask", Status: "Open"},
		{Company: "Acme", Criteria: "ConsultationResult", Status: "Open"},
		{Company: "Acme", Criteria: "Annual", Status: "Open"},
	})
	counts := map[string]int{}
	for _, r := range got.PurposeRows {
		counts[r.Label] = r.Count
	}
	if counts["Consultation result"] != 4 || counts["Annual"] != 1 || len(counts) != 2 {
		t.Fatalf("purposes = %v", counts)
	}
}

func TestPackTwoColumnCount(t *testing.T) {
	if got := packTwoColumnCount([]float64{10, 10, 10, 10}, 25); got != 4 {
		t.Fatalf("four short blocks = %d", got)
	}
	if got := packTwoColumnCount([]float64{20, 20, 20}, 25); got != 2 {
		t.Fatalf("three tall blocks = %d", got)
	}
	if got := packTwoColumnCount([]float64{100}, 25); got != 1 {
		t.Fatalf("one oversized block = %d", got)
	}
	if got := packTwoColumnCount(nil, 25); got != 0 {
		t.Fatalf("empty = %d", got)
	}
}

func pdfPlainText(body []byte) string {
	var b strings.Builder
	rest := body
	for {
		i := bytes.Index(rest, []byte("stream"))
		if i < 0 {
			break
		}
		rest = rest[i+len("stream"):]
		if len(rest) > 0 && rest[0] == '\r' {
			rest = rest[1:]
		}
		if len(rest) > 0 && rest[0] == '\n' {
			rest = rest[1:]
		}
		j := bytes.Index(rest, []byte("endstream"))
		if j < 0 {
			break
		}
		chunk := bytes.TrimRight(rest[:j], "\r\n")
		rest = rest[j+len("endstream"):]
		zr, err := zlib.NewReader(bytes.NewReader(chunk))
		if err != nil {
			b.Write(chunk)
			continue
		}
		decoded, err := io.ReadAll(zr)
		zr.Close()
		if err != nil {
			continue
		}
		b.Write(decoded)
	}
	return b.String()
}

func TestChartSectionStartsOnNextPage(t *testing.T) {
	analytics := buildDashboard([]dashSrc{
		{Company: "Acme", Criteria: "Annual", Status: "Close", Result: "Fit"},
		{Company: "Beta", Criteria: "Pre Employment", Status: "Open", Result: "FitWithNote"},
	})
	c := newPDFDoc()
	c.y = c.pageH - c.margin - 20
	page := c.pdf.PageNo()
	leftW := c.contentW * 0.47
	rightW := leftW
	rightX := c.margin + leftW + c.contentW*0.06
	c.splitSection("2. Purpose", "", "", analytics.PurposeRows, "pie", false, leftW, rightX, rightW, func(x, y, w float64) float64 {
		return c.drawTitledCountTable(x, y, w, "Purpose", analytics.PurposeRows, sumCounts(analytics.PurposeRows), true)
	}, func(w float64) float64 {
		return c.countTableHeight(w, "Purpose", analytics.PurposeRows, true)
	})
	if c.pdf.PageNo() == page {
		t.Fatal("section stayed on a page where it crosses the bottom margin")
	}
	if c.y > c.pageH-c.margin {
		t.Fatalf("section ends at %.1f, past the margin at %.1f", c.y, c.pageH-c.margin)
	}
}

func TestDashboardPDFIsSmallVector(t *testing.T) {
	analytics := buildDashboard([]dashSrc{
		{Company: "Acme", Criteria: "Annual", Status: "Close", Result: "Fit"},
		{Company: "Beta", Criteria: "Pre Employment", Status: "Open", Result: "FitWithNote"},
	})
	body, err := renderDashboardPDF(analytics, dashPDFOptions{From: "2026-09-01", To: "2026-09-27", Chart: "pie", StatusView: "perCompany"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), "%PDF") {
		t.Fatal("missing pdf header")
	}
	if strings.Contains(string(body), "Chart type") || strings.Contains(string(body), "Left out") {
		t.Fatal("pdf still explains chart type or exclusions")
	}
	text := pdfPlainText(body)
	if strings.Contains(text, "Per company") || strings.Contains(text, "Status view") {
		t.Fatal("pdf followed the on-screen per-company view")
	}
	if !strings.Contains(text, "Share of total") || !strings.Contains(text, "50.0%") {
		t.Fatal("pdf status table or chart is missing the share of the total")
	}
	if strings.Contains(string(body), "/Subtype /Image") || strings.Contains(string(body), "/Subtype/Image") {
		t.Fatal("summary embedded a chart image")
	}
	if len(body) > 200_000 {
		t.Fatalf("pdf is %d bytes", len(body))
	}
}

func TestDashboardPDFHandlerFilename(t *testing.T) {
	database := openExportTestDB(t)
	if _, err := database.Exec(`INSERT INTO NationalityTable (Name) VALUES ('Indonesia')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO Company (Name) VALUES ('Acme')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO CheckUpCriteriaTable (Name) VALUES ('Annual')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO Patient (Name, DateOfBirth, Gender, FK_NationalityID, BloodType, PersonalID, WorkPosition) VALUES ('Ani Test', '1990-01-02', 'F', 1, 'O', '1234567890123456', 'Nurse')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO McuRecord (FK_PatientID, Timestamp, ReviewDate, CheckUpID, FK_CompanyID, FK_CheckUpCriteriaID, ResultCriteria, CheckUpStatus) VALUES (1, '2026-09-15T00:00:00', '2026-09-15', 'MedC1', 1, 1, 'Fit', 'Close')`); err != nil {
		t.Fatal(err)
	}

	api := &API{DB: database}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/mcu-reports/summary.pdf?from=2026-09-01&to=2026-09-27&chart=bar&download=1", nil)
	api.GetMcuDashboardPDF(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="MCU_dashboard_2026-09-01_2026-09-27.pdf"` {
		t.Fatalf("disposition = %s", got)
	}
	if !strings.HasPrefix(rec.Body.String(), "%PDF") {
		t.Fatal("body is not a pdf")
	}

	bad := httptest.NewRecorder()
	api.GetMcuDashboardPDF(bad, httptest.NewRequest(http.MethodGet, "/api/mcu-reports/summary.pdf?from=2026-09-27&to=2026-09-01", nil))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("reversed range status %d", bad.Code)
	}
}
