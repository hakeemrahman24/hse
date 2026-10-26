package activitylog

import (
	"bytes"
	"strings"
	"testing"
)

// render runs one template from web/templates with the data the real handlers
// pass, so these tests see exactly the HTML a signed-in user gets.
func render(t *testing.T, name string, data any) string {
	t.Helper()
	pages, err := LoadPages("../../web/templates")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := pages.Templates.ExecuteTemplate(&out, name, data); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out.String()
}

// The home page card says "Medical Record", not "Electronic Medical Records":
// the module holds plain records, not an electronic system.
func TestStartPageCallsTheModuleMedicalRecord(t *testing.T) {
	html := render(t, "start.html", map[string]any{"Name": "Test User", "Role": "staff", "MedicalCheckup": false})
	if !strings.Contains(html, `href="/emr"`) {
		t.Fatal("start page lost its link to /emr")
	}
	if !strings.Contains(html, "Medical Record") {
		t.Error("start page card should be called Medical Record")
	}
	if strings.Contains(html, "Electronic") {
		t.Error("start page still says Electronic")
	}
}

func TestMedicalRecordPageHasFourSectionsAndScreeningSubHeaders(t *testing.T) {
	html := render(t, "emr.html", map[string]any{"Name": "Test Examiner"})
	if strings.Contains(html, "Electronic") {
		t.Error("medical record page still says Electronic")
	}
	// The four sections, in the order the project owner listed them.
	last := -1
	for _, section := range []string{"outpatient", "inpatient", "screening", "wellness"} {
		i := strings.Index(html, `data-section="`+section+`"`)
		if i < 0 {
			t.Fatalf("no sidebar entry for %s", section)
		}
		if i < last {
			t.Errorf("section %s is out of order", section)
		}
		last = i
	}
	// Screening opens to alcohol, fatigue and malaria.
	for _, sub := range []string{"alcohol", "fatigue", "malaria"} {
		if !strings.Contains(html, `data-sub="`+sub+`"`) {
			t.Errorf("no screening sub-header for %s", sub)
		}
	}
	// The alcohol sub-header is labelled with what the form covers, drugs too.
	if !strings.Contains(html, `data-sub="alcohol">Drug and Alcohol</button>`) {
		t.Error("the alcohol sub-header should be labelled Drug and Alcohol")
	}
}

// Alcohol and Fatigue share one panel with a Table tab and a Dashboard tab.
// screening-log.js looks these ids up when the page loads, so the page must carry
// every one and load the script after malaria.js (whose helpers it uses) and
// before emr.js (which calls it). The old Activity Log test-log.js no longer exists.
func TestMedicalRecordPageCarriesTheScreeningPanel(t *testing.T) {
	html := render(t, "emr.html", map[string]any{"Name": "Test Examiner"})
	ids := []string{"mr-panel-screening", "scr-tab-table", "scr-tab-dashboard", "scr-view-table", "scr-view-dashboard",
		"scr-toolbar", "scr-search", "scr-from", "scr-to", "scr-filter-reset", "scr-export", "scr-head", "scr-body", "scr-empty", "scr-error",
		"scr-dash-filter", "scr-dash-from", "scr-dash-to", "scr-dash-by", "scr-dash-reset", "scr-dash-error", "scr-dash-board",
		"scr-stats", "scr-chart-title", "scr-chart-hint", "scr-bars", "scr-chart-empty", "scr-legend"}
	for _, id := range ids {
		if !strings.Contains(html, `id="`+id+`"`) {
			t.Errorf("missing #%s, which screening-log.js needs", id)
		}
	}
	if strings.Contains(html, "/static/test-log.js") {
		t.Error("the Medical Record page should not load the Activity Log's test-log.js")
	}
	malaria := strings.Index(html, `src="/static/malaria.js"`)
	screening := strings.Index(html, `src="/static/screening-log.js"`)
	emr := strings.Index(html, `src="/static/emr.js"`)
	if malaria < 0 || screening < 0 || emr < 0 || !(malaria < screening && screening < emr) {
		t.Errorf("scripts must load malaria.js, then screening-log.js, then emr.js (positions %d, %d, %d)", malaria, screening, emr)
	}
}

// The Drug and Alcohol entry form (da-form.js) looks its boxes up by id when the
// page loads, so the page must carry every one, and its three scripts must come
// after screening-log.js (whose loadScreening they wrap) and before emr.js.
func TestMedicalRecordPageCarriesTheDrugAlcoholForm(t *testing.T) {
	html := render(t, "emr.html", map[string]any{"Name": "Test Examiner"})
	ids := []string{"scr-new", "da-form", "da-cancel", "da-cancel-top", "da-save", "da-pdf", "da-save-pdf", "da-reset", "da-msg", "da-docid",
		"da-consent", "da-panel-body", "da-photo-input", "da-photo-add", "da-photo-list", "da-testType", "da-finalRes",
		"da-donorName", "da-donorName-list", "da-witnessName", "da-witnessName-list", "da-collectorName", "da-collectorName-list",
		"da-securityName", "da-securityName-list", "da-ackName", "da-ackName-list",
		"da-donorNik", "da-donorCompany", "da-donorPosition", "da-consentDt", "da-medNone",
		"da-med1Name", "da-med1Dose", "da-med1Reason", "da-med2Name", "da-med2Dose", "da-med2Reason",
		"da-breathDevice", "da-breathCalib", "da-b1Time", "da-b1Read", "da-b1Mouth", "da-b2Time", "da-b2Read", "da-b2Mouth",
		"da-specTemp", "da-specColour", "da-specTempVal", "da-remarks"}
	for _, id := range ids {
		if !strings.Contains(html, `id="`+id+`"`) {
			t.Errorf("missing #%s, which da-form.js needs", id)
		}
	}
	for _, name := range []string{"testType", "med1Presc", "med2Presc", "b1Res", "b2Res", "finalRes"} {
		if !strings.Contains(html, `name="da-`+name+`"`) {
			t.Errorf("missing the da-%s radio buttons", name)
		}
	}
	for _, key := range []string{"donor", "witness", "collector", "security", "ack"} {
		if !strings.Contains(html, `class="da-sign-btn" data-key="`+key+`"`) {
			t.Errorf("no signature button for %s", key)
		}
	}
	order := []string{"/static/malaria.js", "/static/screening-log.js", "/static/da-form-core.js", "/static/da-form-pdf.js", "/static/da-form.js", "/static/emr.js"}
	last := -1
	for _, src := range order {
		at := strings.Index(html, `src="`+src+`"`)
		if at <= last {
			t.Errorf("%s must load after the script before it (positions %d, %d)", src, at, last)
		}
		last = at
	}
}

// The Fatigue entry form (fatigue-form.js) looks its boxes up by id when the page
// loads, so the page must carry every one, and its scripts must come after
// da-form.js (whose helpers they use) and before emr.js.
func TestMedicalRecordPageCarriesTheFatigueForm(t *testing.T) {
	html := render(t, "emr.html", map[string]any{"Name": "Test Examiner"})
	ids := []string{"scr-new-fatigue", "fa-form", "fa-cancel", "fa-cancel-top", "fa-save", "fa-pdf", "fa-save-pdf", "fa-reset", "fa-msg", "fa-docid",
		"fa-examiner", "fa-examiner-list", "fa-name", "fa-name-list", "fa-company", "fa-position", "fa-date", "fa-time",
		"fa-sys", "fa-dia", "fa-pulse", "fa-medicineName", "fa-x", "fa-y", "fa-z",
		"fa-score-x", "fa-score-y", "fa-score-z", "fa-score-total", "fa-band-body", "fa-band-label", "fa-recommendation",
		"fa-remarks", "fa-photo-input", "fa-photo-add", "fa-photo-list"}
	for _, id := range ids {
		if !strings.Contains(html, `id="`+id+`"`) {
			t.Errorf("missing #%s, which fatigue-form.js needs", id)
		}
	}
	for _, name := range []string{"healthy", "medicine"} {
		if !strings.Contains(html, `name="fa-`+name+`"`) {
			t.Errorf("missing the fa-%s radio buttons", name)
		}
	}
	for _, key := range []string{"worker", "assessor"} {
		if !strings.Contains(html, `data-sign="`+key+`"`) {
			t.Errorf("no signature block for %s", key)
		}
	}
	order := []string{"/static/da-form.js", "/static/fatigue-form-core.js", "/static/fatigue-form-pdf.js", "/static/fatigue-form.js", "/static/emr.js"}
	last := -1
	for _, src := range order {
		at := strings.Index(html, `src="`+src+`"`)
		if at <= last {
			t.Errorf("%s must load after the script before it (positions %d, %d)", src, at, last)
		}
		last = at
	}
}

// Every section has a small triangle that expands and collapses it, like the
// programs in the Activity Log's sidebar, even where there is nothing inside yet.
func TestMedicalRecordSidebarSectionsAllExpandAndCollapse(t *testing.T) {
	html := render(t, "emr.html", map[string]any{"Name": "Test Examiner"})
	for _, section := range []string{"outpatient", "inpatient", "screening", "wellness"} {
		if !strings.Contains(html, `data-toggle="`+section+`"`) {
			t.Errorf("no expand/collapse triangle for %s", section)
		}
		if !strings.Contains(html, `id="mr-children-`+section+`"`) {
			t.Errorf("no collapsible area for %s", section)
		}
	}
	if strings.Count(html, "No sub-headers yet") != 3 {
		t.Errorf("the three sections without sub-headers should each say so, got %d", strings.Count(html, "No sub-headers yet"))
	}
}

// The sidebar header uses the same id-based style hook as the Activity Log's
// "Health programs" header, so it gets the same larger, tinted look.
func TestMedicalRecordSidebarHasAStyledHeader(t *testing.T) {
	html := render(t, "emr.html", map[string]any{"Name": "Test Examiner"})
	if !strings.Contains(html, `id="mr-label"`) {
		t.Error("sidebar header is missing #mr-label")
	}
}

// malaria.js looks these ids up when the page loads; a missing one would break
// the whole form, so the page must carry every one and load the script.
func TestMedicalRecordPageCarriesTheMalariaForm(t *testing.T) {
	html := render(t, "emr.html", map[string]any{"Name": "Test Examiner"})
	ids := []string{"mr-panel-malaria", "malaria-form", "mal-date", "mal-week", "mal-patient", "mal-patient-list", "mal-age", "mal-position", "mal-company", "mal-weight",
		"mal-head", "mal-head-same", "mal-regency", "mal-regency-list", "mal-district", "mal-district-list", "mal-village", "mal-village-list", "mal-purpose", "mal-exam", "mal-result",
		"mal-slide-preview", "mal-actions", "mal-message", "mal-new", "mal-cancel", "mal-cancel-top", "mal-toolbar", "mal-search", "mal-from", "mal-to", "mal-filter-reset", "mal-export", "mal-view-table", "mal-view-dashboard",
		"mal-dash-filter", "mal-dash-from", "mal-dash-to", "mal-dash-by", "mal-dash-reset", "mal-dash-error", "mal-dash-board",
		"mal-stat-total", "mal-stat-patients", "mal-stat-positive", "mal-stat-pending", "mal-chart-title", "mal-chart-hint", "mal-bars", "mal-chart-empty", "mal-slide", "mal-slide-code", "mal-body", "mal-empty"}
	for _, id := range ids {
		if !strings.Contains(html, `id="`+id+`"`) {
			t.Errorf("missing #%s, which malaria.js needs", id)
		}
	}
	if !strings.Contains(html, "Test Examiner") {
		t.Error("the signed-in examiner's name is not shown")
	}
	// The same dd/mm/yyyy date box as the checkup form, from the shared helper.
	if !strings.Contains(html, `class="date-dmy"`) || !strings.Contains(html, `src="/static/date-dmy.js"`) {
		t.Error("the test date should be a dd/mm/yyyy date box with date-dmy.js loaded")
	}
	if !strings.Contains(html, `src="/static/malaria.js"`) {
		t.Error("page does not load /static/malaria.js")
	}
	if strings.Index(html, `src="/static/malaria.js"`) > strings.Index(html, `src="/static/emr.js"`) {
		t.Error("malaria.js must load before emr.js, which calls window.loadMalaria")
	}
}
