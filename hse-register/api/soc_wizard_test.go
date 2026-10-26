package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// wizardSnap: hazard topics 1 (high potential, keyword "ketinggian") and 2 (keyword "kabel"), control
// topics 3 PPE and 4 Permit To Work, the picklists of the form spec, and two areas.
func wizardSnap() *refSnapshot {
	s := newSnapshot(
		[]refTopic{
			{ID: 1, Name: "Working at Height", HazardID: 1, HighPotential: true},
			{ID: 2, Name: "Housekeeping", HazardID: 2},
			{ID: 3, Name: "PPE", Control: true, ControlClass: "PPE"},
			{ID: 4, Name: "Permit To Work", Control: true, ControlClass: "Administrative"},
		},
		[]refHazard{{ID: 1, Class: "Physical", Name: "Fall from Height"}, {ID: 2, Class: "Physical", Name: "Slips, Trips & Falls"}},
		[]string{"Camp", "Workshop", "Port"},
		[]keywordRule{{TopicID: 1, Keyword: "ketinggian", Weight: 2}, {TopicID: 2, Keyword: "kabel", Weight: 1}})
	s.SubAreas = map[string][]string{"Port": {"Gate 1", "Gate 2"}} // Camp and Workshop have none
	s.Picklists = map[string][]picklistOption{
		"unsafe_reason": {
			{Label: "PPE tidak ada atau salah", ControlTag: "PPE"},
			{Label: "Bekerja tanpa izin kerja (permit)", ControlTag: "Permit To Work"},
			{Label: "Posisi atau cara kerja tidak aman"},
		},
		"ppe_item":      {{Label: "Helm", ControlTag: "PPE"}, {Label: "Harness", ControlTag: "PPE"}},
		"good_practice": {{Label: "PPE dipakai dengan benar", ControlTag: "PPE"}, {Label: "Berkendara aman"}},
		"stopped_by":    {{Label: "Saya sendiri"}, {Label: "Supervisor"}},
	}
	return s
}

var wizardCompanies = []string{"STARWAY ENGINEERING INDONESIA", "SALAWATI MOTORINDO"}

func pathA() wizardInput {
	return wizardInput{Path: "A", Area: "camp", Company: "STARWAY ENGINEERING INDONESIA", Work: "Kerja di ketinggian",
		UnsafeReason: "3", StopWork: "no", Corrective: "Saya menegur pekerja"}
}

func pathB() wizardInput {
	return wizardInput{Path: "B", Area: "Workshop", AreaOwner: "SALAWATI MOTORINDO", Condition: "Kabel berserakan",
		Danger: "no", Barricaded: "yes", Corrective: "Dirapikan"}
}

func TestWizardPathAGood(t *testing.T) {
	in := pathA()
	in.TopicID = "1"
	sub, errs := validateWizard(in, today, wizardSnap(), wizardCompanies)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if sub.Path != "A" || sub.Category != catUnsafeAction || sub.Area != "Camp" || sub.ResponsibleCompany != in.Company ||
		sub.Description != "Kerja di ketinggian" || sub.ActionTaken != "Saya menegur pekerja" || sub.UnsafeReason != "Posisi atau cara kerja tidak aman" {
		t.Errorf("bad submission: %+v", sub)
	}
	if sub.StopWork == nil || *sub.StopWork || sub.TopicID != 1 || sub.HazardID != 1 || sub.ControlTopicID != 0 {
		t.Errorf("stop work / topic / control tag: %+v", sub)
	}
	if !sub.FindingDate.Equal(today) {
		t.Errorf("a wizard report is dated the day it is sent, got %v", sub.FindingDate)
	}
	if sub.ReviewFlags != "" {
		t.Errorf("flags = %q", sub.ReviewFlags)
	}
}

func TestWizardPathAControlTagAndPPEItem(t *testing.T) {
	in := pathA()
	in.UnsafeReason = "1" // PPE
	if _, errs := validateWizard(in, today, wizardSnap(), wizardCompanies); errs["ppe_item"] == "" {
		t.Errorf("the PPE item is required when the reason is PPE: %v", errs)
	}
	in.PPEItem = "2"
	sub, errs := validateWizard(in, today, wizardSnap(), wizardCompanies)
	if len(errs) != 0 || sub.PPEItem != "Harness" || sub.ControlTopicID != 3 {
		t.Errorf("ppe: %+v %v", sub, errs)
	}
	in.UnsafeReason, in.PPEItem = "2", "" // permit: no PPE item asked, control tag is Permit To Work
	sub, errs = validateWizard(in, today, wizardSnap(), wizardCompanies)
	if len(errs) != 0 || sub.PPEItem != "" || sub.ControlTopicID != 4 {
		t.Errorf("permit: %+v %v", sub, errs)
	}
}

func TestWizardPathAStopWorkRules(t *testing.T) {
	in := pathA()
	in.StopWork = "yes"
	if _, errs := validateWizard(in, today, wizardSnap(), wizardCompanies); errs["stopped_by"] == "" {
		t.Errorf("who stopped it is required: %v", errs)
	}
	in.StoppedBy = "2"
	sub, errs := validateWizard(in, today, wizardSnap(), wizardCompanies)
	if len(errs) != 0 || sub.StopWork == nil || !*sub.StopWork || sub.StoppedBy != "Supervisor" {
		t.Errorf("stopped by: %+v %v", sub, errs)
	}
	in.StopWork = ""
	if _, errs := validateWizard(in, today, wizardSnap(), wizardCompanies); errs["stop_work"] == "" {
		t.Errorf("stop work must be answered: %v", errs)
	}
	// Stopped-by from an earlier "yes" is dropped when the answer is now "no".
	in.StopWork, in.StoppedBy = "no", "2"
	if sub, _ := validateWizard(in, today, wizardSnap(), wizardCompanies); sub.StoppedBy != "" {
		t.Errorf("stopped_by kept after answering no: %q", sub.StoppedBy)
	}
}

func TestWizardHighPotentialNotStoppedFlag(t *testing.T) {
	in := pathA()
	in.TopicID, in.ShouldStop = "1", "yes" // high-potential, not stopped, reporter says it should have been
	sub, errs := validateWizard(in, today, wizardSnap(), wizardCompanies)
	if len(errs) != 0 || !strings.Contains(sub.ReviewFlags, "stop work not used on high-potential work") {
		t.Errorf("flags = %q %v", sub.ReviewFlags, errs)
	}
	in.TopicID = "2" // not high-potential: the prompt is not asked, so a stray answer is ignored
	if sub, _ := validateWizard(in, today, wizardSnap(), wizardCompanies); strings.Contains(sub.ReviewFlags, "high-potential") {
		t.Errorf("flag raised for a topic that is not high-potential: %q", sub.ReviewFlags)
	}
	in.TopicID, in.StopWork = "1", "yes"
	in.StoppedBy = "1"
	if sub, _ := validateWizard(in, today, wizardSnap(), wizardCompanies); strings.Contains(sub.ReviewFlags, "high-potential") {
		t.Errorf("flag raised although the work was stopped: %q", sub.ReviewFlags)
	}
}

func TestWizardReviewFlagsTopicAndCompany(t *testing.T) {
	in := pathA()
	in.Company = "PT Baru Kontraktor"
	sub, errs := validateWizard(in, today, wizardSnap(), wizardCompanies)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	for _, want := range []string{"topic not picked", "company not in list"} {
		if !strings.Contains(sub.ReviewFlags, want) {
			t.Errorf("flags %q miss %q", sub.ReviewFlags, want)
		}
	}
	if sub.TopicID != 0 || sub.TopicPick != topicPickNone {
		t.Errorf("topic must stay empty: %+v", sub)
	}
	// With no company list at all (Medpost unreachable) a typed company is not flagged.
	if sub, _ := validateWizard(in, today, wizardSnap(), nil); strings.Contains(sub.ReviewFlags, "company") {
		t.Errorf("flagged without a list: %q", sub.ReviewFlags)
	}
}

func TestWizardPathAMissingFields(t *testing.T) {
	cases := map[string]struct {
		mutate func(*wizardInput)
		field  string
	}{
		"no area":        {func(i *wizardInput) { i.Area = "" }, "area"},
		"unknown area":   {func(i *wizardInput) { i.Area = "Moon" }, "area"},
		"no company":     {func(i *wizardInput) { i.Company = " " }, "company"},
		"no work":        {func(i *wizardInput) { i.Work = "" }, "work"},
		"no reason":      {func(i *wizardInput) { i.UnsafeReason = "" }, "unsafe_reason"},
		"reason invalid": {func(i *wizardInput) { i.UnsafeReason = "99" }, "unsafe_reason"},
		"no corrective":  {func(i *wizardInput) { i.Corrective = "" }, "corrective"},
		"unknown topic":  {func(i *wizardInput) { i.TopicID = "999" }, "topic"},
		"long work":      {func(i *wizardInput) { i.Work = strings.Repeat("a", maxObsText+1) }, "work"},
	}
	for name, c := range cases {
		in := pathA()
		c.mutate(&in)
		if _, errs := validateWizard(in, today, wizardSnap(), wizardCompanies); errs[c.field] == "" {
			t.Errorf("%s: want an error on %q, got %v", name, c.field, errs)
		}
	}
}

func TestWizardBadPath(t *testing.T) {
	in := pathA()
	in.Path = "E"
	if _, errs := validateWizard(in, today, wizardSnap(), wizardCompanies); errs["path"] == "" {
		t.Errorf("an unknown path was accepted: %v", errs)
	}
}

func TestWizardPathB(t *testing.T) {
	sub, errs := validateWizard(pathB(), today, wizardSnap(), wizardCompanies)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if sub.Category != catUnsafeCondition || sub.ResponsibleCompany != "SALAWATI MOTORINDO" || sub.Description != "Kabel berserakan" ||
		sub.ImmediateDanger == nil || *sub.ImmediateDanger || sub.Barricaded == nil || !*sub.Barricaded {
		t.Errorf("bad submission: %+v", sub)
	}
	if strings.Contains(sub.ReviewFlags, "urgent") {
		t.Errorf("not urgent: %q", sub.ReviewFlags)
	}
	in := pathB()
	in.Danger, in.Barricaded = "yes", "no"
	sub, _ = validateWizard(in, today, wizardSnap(), wizardCompanies)
	if !strings.Contains(sub.ReviewFlags, "urgent") {
		t.Errorf("immediate danger and no barricade must be urgent: %q", sub.ReviewFlags)
	}
	in.Barricaded = "yes"
	if sub, _ = validateWizard(in, today, wizardSnap(), wizardCompanies); strings.Contains(sub.ReviewFlags, "urgent") {
		t.Errorf("a barricaded danger is not urgent: %q", sub.ReviewFlags)
	}
	for _, f := range []func(*wizardInput){func(i *wizardInput) { i.AreaOwner = "" }, func(i *wizardInput) { i.Condition = "" },
		func(i *wizardInput) { i.Danger = "" }, func(i *wizardInput) { i.Barricaded = "" }, func(i *wizardInput) { i.Corrective = "" }} {
		in := pathB()
		f(&in)
		if _, errs := validateWizard(in, today, wizardSnap(), wizardCompanies); len(errs) == 0 {
			t.Errorf("an incomplete path B was accepted: %+v", in)
		}
	}
}

func TestWizardPathC(t *testing.T) {
	in := wizardInput{Path: "C", Area: "Camp", Company: "SALAWATI MOTORINDO", GoodPractice: "1"}
	sub, errs := validateWizard(in, today, wizardSnap(), wizardCompanies)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	// Work is optional on a safe observation: the description falls back to what was done well.
	if sub.Category != catPositive || sub.Description != "PPE dipakai dengan benar" || sub.GoodPractice != "PPE dipakai dengan benar" || sub.ControlTopicID != 3 {
		t.Errorf("bad submission: %+v", sub)
	}
	if strings.Contains(sub.ReviewFlags, "topic not picked") {
		t.Errorf("no topic is asked on a safe observation without work: %q", sub.ReviewFlags)
	}
	in.Work, in.GoodPractice = "Kerja di ketinggian dengan harness", "2"
	sub, _ = validateWizard(in, today, wizardSnap(), wizardCompanies)
	if sub.Description != "Kerja di ketinggian dengan harness" || sub.ControlTopicID != 0 {
		t.Errorf("with work: %+v", sub)
	}
	in.GoodPractice = ""
	if _, errs := validateWizard(in, today, wizardSnap(), wizardCompanies); errs["good_practice"] == "" {
		t.Errorf("what was done well is required: %v", errs)
	}
}

func TestWizardPathD(t *testing.T) {
	in := wizardInput{Path: "D", Area: "Camp", Improve: "Lampu jalan mati", Recommendation: "Ganti lampu"}
	sub, errs := validateWizard(in, today, wizardSnap(), wizardCompanies)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if sub.Category != catUnsafeCondition || sub.Description != "Lampu jalan mati" || sub.Recommendation != "Ganti lampu" || sub.ResponsibleCompany != "" {
		t.Errorf("bad submission: %+v", sub)
	}
	in.Recommendation = ""
	if _, errs := validateWizard(in, today, wizardSnap(), wizardCompanies); errs["recommendation"] == "" {
		t.Errorf("the proposal is required: %v", errs)
	}
}

func TestWizardRecordsTheTopicPick(t *testing.T) {
	in := pathA()
	in.Work = "Kerja di ketinggian dekat kabel"
	in.TopicID = "2" // both topics are suggested for this text
	sub, _ := validateWizard(in, today, wizardSnap(), wizardCompanies)
	if sub.TopicPick != topicPickSuggested {
		t.Errorf("pick = %q, want suggested", sub.TopicPick)
	}
}

// ---- the wizard pages ----

func wizardClient(t *testing.T) (*socClient, *memorySink) {
	t.Helper()
	c, h, sink := signedInWithHandlers(t)
	h.ref = newStaticRef(wizardSnap())
	return c, sink
}

func wizardForm(kv ...string) string {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return v.Encode()
}

const formURLEncoded = "application/x-www-form-urlencoded"

func TestWizardNeedsSignin(t *testing.T) {
	c, _, _ := newSOCTest(t)
	for _, req := range [][2]string{{"GET", "/soc/new"}, {"POST", "/soc/new"}} {
		if rec := c.do(req[0], req[1], formURLEncoded, "path=A"); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/soc" {
			t.Errorf("%s without a session: %d %q", req[0], rec.Code, rec.Header().Get("Location"))
		}
	}
}

func TestSigninLeadsToTheWizard(t *testing.T) {
	c, _, _ := newSOCTest(t)
	c.passCheck()
	rec := c.signIn(c.search("budi")[0].Ref, "1990-05-14")
	if !strings.Contains(rec.Body.String(), `"redirect":"/soc/new"`) {
		t.Errorf("sign-in should lead to the wizard: %s", rec.Body)
	}
}

func TestWizardPageShowsTheScreensAndPicklists(t *testing.T) {
	c, _ := wizardClient(t)
	body := c.do("GET", "/soc/new", "", "").Body.String()
	for _, want := range []string{"What did you see?", "Unsafe action", "Safe observation", `data-path="D"`, `id="wz-area"`,
		"PPE tidak ada atau salah", `data-ppe="1"`, `name="stopped_by" value="2"`, "PPE dipakai dengan benar", "Harness",
		`name="topic_id"`, `data-hp="1"`, `/soc/static/soc-wizard.js?v=`, `data-qr=""`, "attachWizard"} {
		if !strings.Contains(body, want) {
			t.Errorf("wizard page is missing %q", want)
		}
	}
	if strings.Contains(body, `<option value="3"`) {
		t.Error("control topics (PPE) must not be in the wizard's topic list")
	}
}

func TestWizardAreaFromQRCode(t *testing.T) {
	c, _ := wizardClient(t)
	body := c.do("GET", "/soc/new?area=workshop", "", "").Body.String()
	if !strings.Contains(body, `data-qr="1"`) || !strings.Contains(body, `<option value="Workshop" selected>`) {
		t.Errorf("a known area from the QR code fills the area and skips the question")
	}
	if body := c.do("GET", "/soc/new?area=Moon", "", "").Body.String(); !strings.Contains(body, `data-qr=""`) {
		t.Error("an unknown area must be ignored")
	}
}

func TestWizardSubmitSavesPathAWithSessionIdentity(t *testing.T) {
	c, sink := wizardClient(t)
	rec := c.do("POST", "/soc/new", formURLEncoded, wizardForm(
		"path", "A", "area", "Camp", "company", "STARWAY ENGINEERING INDONESIA", "work", "Kerja di ketinggian", "topic_id", "1",
		"unsafe_reason", "1", "ppe_item", "2", "stop_work", "yes", "stopped_by", "2", "should_stop", "yes", "corrective", "Dihentikan",
		"observer_name", "Evil", "employee_id", "E999"))
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/soc/thanks?ref=SOC-") {
		t.Fatalf("got %d %q: %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	if len(sink.rows) != 1 {
		t.Fatalf("saved %d rows", len(sink.rows))
	}
	got := sink.rows[0]
	if got.ReporterName != "Budi Santoso" || got.EmployeeID != "E001" {
		t.Errorf("reporter must come from the session: %+v", got)
	}
	if got.Path != "A" || got.Category != catUnsafeAction || got.UnsafeReason != "PPE tidak ada atau salah" || got.PPEItem != "Harness" ||
		got.ControlTopicID != 3 || got.StopWork == nil || !*got.StopWork || got.StoppedBy != "Supervisor" || got.TopicID != 1 || got.HazardID != 1 {
		t.Errorf("bad row: %+v", got)
	}
	if got.ReviewFlags != "" { // work was stopped, so no stop-work flag
		t.Errorf("flags = %q", got.ReviewFlags)
	}
}

func TestWizardSubmitErrorsKeepWhatWasTyped(t *testing.T) {
	c, sink := wizardClient(t)
	rec := c.do("POST", "/soc/new", formURLEncoded, wizardForm("path", "A", "area", "Camp", "company", "PT X", "work", "Kerja las di atap"))
	if rec.Code != http.StatusUnprocessableEntity || len(sink.rows) != 0 {
		t.Fatalf("got %d, saved %d", rec.Code, len(sink.rows))
	}
	body := rec.Body.String()
	for _, want := range []string{`id="wz-path" value="A"`, "Kerja las di atap", `value="PT X"`, "Choose what was unsafe.", "Answer yes or no.", "Describe what you did on the spot.", "field-error"} {
		if !strings.Contains(body, want) {
			t.Errorf("error page is missing %q", want)
		}
	}
}

func TestWizardPathBUrgentFlagIsSaved(t *testing.T) {
	c, sink := wizardClient(t)
	rec := c.do("POST", "/soc/new", formURLEncoded, wizardForm(
		"path", "B", "area", "Workshop", "area_owner", "SALAWATI MOTORINDO", "condition", "Lubang terbuka",
		"danger", "yes", "barricaded", "no", "corrective", "Menunggu supervisor"))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("got %d: %s", rec.Code, rec.Body)
	}
	got := sink.rows[0]
	if got.Path != "B" || !strings.Contains(got.ReviewFlags, "urgent") || !strings.Contains(got.ReviewFlags, "topic not picked") || got.TopicPick != topicPickNone {
		t.Errorf("row: %+v", got)
	}
}

func TestWizardPathsCAndDSave(t *testing.T) {
	c, sink := wizardClient(t)
	if rec := c.do("POST", "/soc/new", formURLEncoded, wizardForm("path", "C", "area", "Camp", "company", "SALAWATI MOTORINDO", "good_practice", "2")); rec.Code != http.StatusSeeOther {
		t.Fatalf("path C: %d %s", rec.Code, rec.Body)
	}
	if rec := c.do("POST", "/soc/new", formURLEncoded, wizardForm("path", "D", "area", "Camp", "improve", "Lampu mati", "recommendation", "Ganti lampu")); rec.Code != http.StatusSeeOther {
		t.Fatalf("path D: %d %s", rec.Code, rec.Body)
	}
	if len(sink.rows) != 2 || sink.rows[0].Category != catPositive || sink.rows[1].Recommendation != "Ganti lampu" || sink.rows[1].Path != "D" {
		t.Errorf("rows: %+v", sink.rows)
	}
}

// ---- sub-areas (the second level of the area list, edited in Settings) ----

func TestWizardSubAreaIsRequiredWhenTheAreaHasOnes(t *testing.T) {
	in := pathA()
	in.Area = "port" // area "port": Port has sub-areas
	if _, errs := validateWizard(in, today, wizardSnap(), wizardCompanies); errs["sub_area"] == "" {
		t.Errorf("the sub-area must be chosen when the area has some: %v", errs)
	}
	in.SubArea = "gate 2" // any letter case, stored with the list's spelling
	sub, errs := validateWizard(in, today, wizardSnap(), wizardCompanies)
	if len(errs) != 0 || sub.Area != "Port" || sub.SubArea != "Gate 2" {
		t.Errorf("sub-area: %+v %v", sub, errs)
	}
	in.SubArea = "Jetty"
	if _, errs := validateWizard(in, today, wizardSnap(), wizardCompanies); errs["sub_area"] == "" {
		t.Errorf("a sub-area of another area (or not in the list) was accepted: %v", errs)
	}
}

func TestWizardSubAreaIsIgnoredWhenTheAreaHasNone(t *testing.T) {
	in := pathA()
	in.Area, in.SubArea = "Workshop", "Gate 1" // left over from an earlier choice of Port
	sub, errs := validateWizard(in, today, wizardSnap(), wizardCompanies)
	if len(errs) != 0 || sub.SubArea != "" {
		t.Errorf("a stale sub-area must be dropped: %+v %v", sub, errs)
	}
}

func TestWizardSubAreaAppliesToEveryPath(t *testing.T) {
	for _, in := range []wizardInput{
		{Path: "B", Area: "Port", AreaOwner: "SALAWATI MOTORINDO", Condition: "x", Danger: "no", Barricaded: "yes", Corrective: "x"},
		{Path: "C", Area: "Port", Company: "SALAWATI MOTORINDO", GoodPractice: "1"},
		{Path: "D", Area: "Port", Improve: "x", Recommendation: "y"},
	} {
		if _, errs := validateWizard(in, today, wizardSnap(), wizardCompanies); errs["sub_area"] == "" {
			t.Errorf("path %s: the sub-area was not asked: %v", in.Path, errs)
		}
		in.SubArea = "Gate 1"
		if sub, errs := validateWizard(in, today, wizardSnap(), wizardCompanies); len(errs) != 0 || sub.SubArea != "Gate 1" {
			t.Errorf("path %s: %+v %v", in.Path, sub, errs)
		}
	}
}

func TestWizardPageCarriesTheSubAreasAndAQRSubArea(t *testing.T) {
	c, _ := wizardClient(t)
	body := c.do("GET", "/soc/new", "", "").Body.String()
	for _, want := range []string{`data-subareas=`, "Gate 1", "Gate 2", `name="sub_area"`} {
		if !strings.Contains(body, want) {
			t.Errorf("wizard page is missing %q", want)
		}
	}
	body = c.do("GET", "/soc/new?area=Port&sub=gate+1", "", "").Body.String()
	if !strings.Contains(body, `data-qr="1"`) || !strings.Contains(body, `data-qr-sub="1"`) || !strings.Contains(body, `<option value="Gate 1" selected>`) {
		t.Error("a QR code with area and sub-area fills both and skips both questions")
	}
	if body = c.do("GET", "/soc/new?area=Port", "", "").Body.String(); strings.Contains(body, `data-qr-sub="1"`) {
		t.Error("a QR code with only the area must still ask the sub-area")
	}
	if body = c.do("GET", "/soc/new?area=Port&sub=Moon", "", "").Body.String(); strings.Contains(body, `data-qr-sub="1"`) {
		t.Error("an unknown sub-area in the QR code must be ignored")
	}
}

func TestWizardSubmitSavesTheSubArea(t *testing.T) {
	c, sink := wizardClient(t)
	rec := c.do("POST", "/soc/new", formURLEncoded, wizardForm("path", "D", "area", "Port", "sub_area", "Gate 1", "improve", "Lampu mati", "recommendation", "Ganti"))
	if rec.Code != http.StatusSeeOther || len(sink.rows) != 1 || sink.rows[0].SubArea != "Gate 1" {
		t.Fatalf("got %d, rows %+v", rec.Code, sink.rows)
	}
}
