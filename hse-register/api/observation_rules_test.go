package main

import (
	"strings"
	"testing"
)

func goodForm() obsForm {
	return obsForm{
		Source: "Internal Audit", SourceDetail: "Site Audit",
		ObserverName: "Siti Rahma", Designation: "Safety Officer", Company: "STARWAY ENGINEERING INDONESIA",
		Category: catUnsafeCondition, Description: "Kabel berserakan di area camp",
		Area: "Camp", ResponsibleCompany: "SALAWATI MOTORINDO",
		Status: statusOpen, ObservedAt: "2026-10-15T08:30",
	}
}

func check(f obsForm, c obsCheck) (*Observation, map[string]string) {
	if c.Ref == nil {
		c.Ref = testSnap()
	}
	return validateObservation(f, c)
}

func TestValidateObservationAcceptsAGoodStaffEntry(t *testing.T) {
	o, errs := check(goodForm(), obsCheck{})
	if errs != nil {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if o.Source != "Internal Audit" || o.SourceDetail != "Site Audit" || o.Location != "Camp" || o.Verification != "" {
		t.Errorf("bad observation: %+v", o)
	}
	if o.TopicID != 2 || o.HazardID != 2 {
		t.Errorf("topic/hazard should be suggested from the description, got %d/%d", o.TopicID, o.HazardID)
	}
	if o.ObservedAt.Format("2006-01-02 15:04") != "2026-10-15 08:30" {
		t.Errorf("observed at = %v", o.ObservedAt)
	}
}

func TestSourceAndDetailRules(t *testing.T) {
	cases := []struct {
		name           string
		source, detail string
		wantErr        string // field expected in the error map, "" for none
		wantDetail     string
	}{
		{"internal audit with site audit", "Internal Audit", "Site Audit", "", "Site Audit"},
		{"internal audit with HO audit", "Internal Audit", "HO Audit", "", "HO Audit"},
		{"internal audit with Corporate audit", "Internal Audit", "Corporate Audit", "", "Corporate Audit"},
		{"external with third party", "External Audit", "3rd Party Company", "", "3rd Party Company"},
		{"external with government", "External Audit", "Government", "", "Government"},
		{"internal needs a type", "Internal Audit", "", "source_detail", ""},
		{"internal with an external type", "Internal Audit", "Government", "source_detail", ""},
		{"external with an internal type", "External Audit", "Site Audit", "source_detail", ""},
		{"walkthrough needs no type", "Management Walkthrough", "", "", ""},
		{"a stray type is dropped", "Management Walkthrough", "Site Audit", "", ""},
		{"project monitoring", "Project Monitoring Action Report", "", "", ""},
		{"staff cannot pick SOC", "SOC", "", "source", ""},
		{"unknown source", "Rumour", "", "source", ""},
		{"no source on a new entry", "", "", "source", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := goodForm()
			f.Source, f.SourceDetail = c.source, c.detail
			o, errs := check(f, obsCheck{})
			if c.wantErr == "" {
				if errs != nil {
					t.Fatalf("unexpected errors: %v", errs)
				}
				if o.SourceDetail != c.wantDetail {
					t.Errorf("detail = %q, want %q", o.SourceDetail, c.wantDetail)
				}
				return
			}
			if errs[c.wantErr] == "" {
				t.Errorf("want an error on %q, got %v", c.wantErr, errs)
			}
		})
	}
}

func TestOldRowWithoutSourceCanStayWithoutOne(t *testing.T) {
	f := goodForm()
	f.Source, f.SourceDetail = "", ""
	old := &Observation{ID: 9, Location: "Camp"}
	if _, errs := check(f, obsCheck{Existing: old}); errs != nil {
		t.Errorf("an old row saved without touching the source must be accepted: %v", errs)
	}
}

func TestAreaRules(t *testing.T) {
	f := goodForm()
	f.Area = "  pcc / tcf "
	if o, errs := check(f, obsCheck{}); errs != nil || o.Location != "PCC / TCF" {
		t.Errorf("area should match the list ignoring case: %v %+v", errs, o)
	}
	f.Area = "Moon base"
	if _, errs := check(f, obsCheck{}); errs["area"] == "" {
		t.Error("an area that is not in the list must be refused for a new entry")
	}
	// An old row keeps its old free-text area as long as it is left alone...
	old := &Observation{ID: 5, Location: "Moon base"}
	if _, errs := check(f, obsCheck{Existing: old}); errs != nil {
		t.Errorf("unchanged old area must be accepted: %v", errs)
	}
	// ...but changing it to another unlisted value is refused.
	f.Area = "Mars base"
	if _, errs := check(f, obsCheck{Existing: old}); errs["area"] == "" {
		t.Error("a new unlisted area must be refused on edit too")
	}
}

func TestRequiredFields(t *testing.T) {
	cases := map[string]struct {
		mutate func(*obsForm)
		field  string
	}{
		"name":            {func(f *obsForm) { f.ObserverName = " " }, "observer_name"},
		"category":        {func(f *obsForm) { f.Category = "near_miss" }, "category"},
		"description":     {func(f *obsForm) { f.Description = "" }, "description"},
		"description len": {func(f *obsForm) { f.Description = strings.Repeat("x", 5001) }, "description"},
		"area":            {func(f *obsForm) { f.Area = "" }, "area"},
		"company":         {func(f *obsForm) { f.ResponsibleCompany = "" }, "responsible_company"},
		"date":            {func(f *obsForm) { f.ObservedAt = "yesterday" }, "observed_at"},
		"status":          {func(f *obsForm) { f.Status = "done" }, "status"},
		"topic":           {func(f *obsForm) { f.TopicID = "77" }, "topic"},
		"hazard":          {func(f *obsForm) { f.HazardID = "x" }, "hazard"},
	}
	for name, c := range cases {
		f := goodForm()
		c.mutate(&f)
		if _, errs := check(f, obsCheck{}); errs[c.field] == "" {
			t.Errorf("%s: want an error on %q, got %v", name, c.field, errs)
		}
	}
}

func TestClosingNeedsAnAfterFile(t *testing.T) {
	f := goodForm()
	f.Status = statusClosed
	if _, errs := check(f, obsCheck{AfterFiles: 0}); errs["status"] != msgCloseNeedsAfter {
		t.Errorf("closing without After files must be refused, got %v", errs)
	}
	if _, errs := check(f, obsCheck{AfterFiles: 1}); errs != nil {
		t.Errorf("closing with one After file must work: %v", errs)
	}
	// In Progress never needs files.
	f.Status = statusInProgress
	if _, errs := check(f, obsCheck{}); errs != nil {
		t.Errorf("in progress must not need files: %v", errs)
	}
	// A positive finding has nothing to follow up.
	f.Status, f.Category = statusClosed, catPositive
	if _, errs := check(f, obsCheck{}); errs != nil {
		t.Errorf("a positive finding can be closed without After files: %v", errs)
	}
}

func TestCloseBlocked(t *testing.T) {
	cases := []struct {
		category, status string
		after            int
		want             bool
	}{
		{catUnsafeAction, statusClosed, 0, true},
		{catUnsafeCondition, statusClosed, 0, true},
		{catUnsafeAction, statusClosed, 1, false},
		{catUnsafeAction, statusOpen, 0, false},
		{catUnsafeAction, statusInProgress, 0, false},
		{catPositive, statusClosed, 0, false},
	}
	for _, c := range cases {
		if got := closeBlocked(c.category, c.status, c.after); got != c.want {
			t.Errorf("closeBlocked(%s,%s,%d) = %v, want %v", c.category, c.status, c.after, got, c.want)
		}
	}
}

func sampleSOCPost() *Observation {
	return &Observation{
		ID: 3, Source: sourceSOC, ObserverName: "Budi Santoso", EmployeeID: "E001", Designation: "Operator",
		Company: "SALAWATI MOTORINDO", Location: "Camp", Verification: verifNotYet, Recommendation: "kept",
	}
}

func TestSOCPostKeepsItsSubmitterWhateverTheFormSays(t *testing.T) {
	f := goodForm()
	f.Source, f.SourceDetail = "External Audit", "Government" // an attempt to relabel the post
	f.ObserverName, f.Designation, f.Company = "Someone Else", "Boss", "Other Co"
	o, errs := check(f, obsCheck{Existing: sampleSOCPost()})
	if errs != nil {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if o.Source != sourceSOC || o.SourceDetail != "" {
		t.Errorf("a SOC post stays SOC, got %q/%q", o.Source, o.SourceDetail)
	}
	if o.ObserverName != "Budi Santoso" || o.EmployeeID != "E001" || o.Designation != "Operator" || o.Company != "SALAWATI MOTORINDO" {
		t.Errorf("submitter must stay as the employee sent it: %+v", o)
	}
	if o.Recommendation != "kept" {
		t.Errorf("fields the form does not show must be kept, got %q", o.Recommendation)
	}
}

func TestActionTakenRemarksAreTrimmedAndLimited(t *testing.T) {
	f := goodForm()
	f.ActionTaken = "  Cables tied and rerouted.  "
	o, errs := check(f, obsCheck{})
	if errs != nil {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if o.ActionTaken != "Cables tied and rerouted." {
		t.Errorf("remarks should be trimmed, got %q", o.ActionTaken)
	}
	f.ActionTaken = strings.Repeat("x", maxObsText+1)
	if _, errs := check(f, obsCheck{}); errs["action_taken"] == "" {
		t.Error("remarks over the limit must be refused")
	}
	f.ActionTaken = ""
	if o, errs := check(f, obsCheck{}); errs != nil || o.ActionTaken != "" {
		t.Errorf("remarks are optional, got %v / %q", errs, o.ActionTaken)
	}
}

func TestVerificationRules(t *testing.T) {
	post := sampleSOCPost()
	f := goodForm()
	f.Verification = verifVerified
	if o, errs := check(f, obsCheck{Existing: post}); errs != nil || o.Verification != verifVerified {
		t.Errorf("staff can verify a SOC post: %v %+v", errs, o)
	}
	f.Verification = verifNotYet
	if o, _ := check(f, obsCheck{Existing: post}); o.Verification != verifNotYet {
		t.Errorf("staff can set it back to Not yet, got %q", o.Verification)
	}
	f.Verification = "Maybe"
	if _, errs := check(f, obsCheck{Existing: post}); errs["verification"] == "" {
		t.Error("an unknown verification value must be refused")
	}
	// A form that does not send the field leaves the stored value alone.
	post.Verification = verifVerified
	f.Verification = ""
	if o, _ := check(f, obsCheck{Existing: post}); o.Verification != verifVerified {
		t.Errorf("a missing field must not reset Verified, got %q", o.Verification)
	}
	// Posts that did not come from SOC have no verification, even if the form sends one.
	g := goodForm()
	g.Verification = verifVerified
	if o, _ := check(g, obsCheck{}); o.Verification != "" {
		t.Errorf("non-SOC entries have no verification, got %q", o.Verification)
	}
}

func TestSourceDetailChoicesAreInFixedOrder(t *testing.T) {
	got := newSnapshot(nil, nil, nil, nil).SourceDetailChoices("", "")
	if len(got) != 5 || got[0] != (sourceDetailChoice{"Internal Audit", "Site Audit"}) || got[4] != (sourceDetailChoice{"External Audit", "Government"}) {
		t.Errorf("choices = %+v", got)
	}
}

func snapWithTypes() *refSnapshot {
	s := testSnap()
	s.Sources = []refSource{
		{ID: 1, Name: "Internal Audit", Details: []refDetail{{ID: 1, Name: "Site Audit"}, {ID: 2, Name: "KL Audit", Archived: true}}},
		{ID: 2, Name: "Regulator Inspection", Details: []refDetail{{ID: 3, Name: "Old type", Archived: true}}},
		{ID: 3, Name: "Custom with types", Details: []refDetail{{ID: 4, Name: "Type A"}}},
	}
	return s
}

func TestArchivedSourceTypeIsRefusedUnlessThePostAlreadyHasIt(t *testing.T) {
	f := goodForm()
	f.Source, f.SourceDetail = "Internal Audit", "KL Audit"
	if _, errs := check(f, obsCheck{Ref: snapWithTypes()}); errs["source_detail"] == "" {
		t.Error("an archived type was accepted on a new post")
	}
	old := &Observation{ID: 4, Location: "Camp", Source: "Internal Audit", SourceDetail: "KL Audit"}
	if o, errs := check(f, obsCheck{Ref: snapWithTypes(), Existing: old}); errs != nil || o.SourceDetail != "KL Audit" {
		t.Errorf("the type the post already had must stay allowed: %v", errs)
	}
}

func TestSourceWithOnlyArchivedTypesAsksForNone(t *testing.T) {
	f := goodForm()
	f.Source, f.SourceDetail = "Regulator Inspection", ""
	snap := snapWithTypes()
	snap.Sources[1].Archived = false
	if o, errs := check(f, obsCheck{Ref: snap}); errs != nil || o.SourceDetail != "" {
		t.Errorf("a source with no active types must need none: %v", errs)
	}
}

func TestCustomSourceWithTypesRequiresOne(t *testing.T) {
	f := goodForm()
	f.Source, f.SourceDetail = "Custom with types", ""
	if _, errs := check(f, obsCheck{Ref: snapWithTypes()}); errs["source_detail"] == "" {
		t.Error("a source with an active type must require one")
	}
	f.SourceDetail = "Type A"
	if o, errs := check(f, obsCheck{Ref: snapWithTypes()}); errs != nil || o.SourceDetail != "Type A" {
		t.Errorf("valid custom type refused: %v", errs)
	}
}

func TestFormChoicesHideArchivedTypesExceptTheKeptOne(t *testing.T) {
	snap := snapWithTypes()
	for _, c := range snap.SourceDetailChoices("", "") {
		if c.Detail == "KL Audit" || c.Detail == "Old type" {
			t.Errorf("archived type %q offered", c.Detail)
		}
	}
	found := false
	for _, c := range snap.SourceDetailChoices("Internal Audit", "KL Audit") {
		found = found || c.Detail == "KL Audit"
	}
	if !found {
		t.Error("the kept archived type must stay in the form")
	}
}
