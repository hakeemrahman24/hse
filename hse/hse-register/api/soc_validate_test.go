package main

import (
	"strings"
	"testing"
	"time"
)

var today = time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)

// testSnap is a small set of lookup lists: two topics, two hazards, two areas, three keywords.
func testSnap() *refSnapshot {
	return newSnapshot(
		[]refTopic{{ID: 1, Name: "Working at Height", HazardID: 1}, {ID: 2, Name: "Housekeeping", HazardID: 2}, {ID: 3, Name: "PPE", Control: true, ControlClass: "PPE"}},
		[]refHazard{{ID: 1, Class: "Safety", Name: "Fall from Height"}, {ID: 2, Class: "Safety", Name: "Slips, Trips & Falls"}},
		[]string{"Camp", "PCC / TCF"},
		[]keywordRule{
			{TopicID: 1, Keyword: "ketinggian", Weight: 1},
			{TopicID: 2, Keyword: "kabel", Weight: 1},
			{TopicID: 2, Keyword: "berserakan", Weight: 2},
			{TopicID: 3, Keyword: "helm", Whole: true, Weight: 1},
		})
}

func validInput() socInput {
	return socInput{
		Date: "2026-10-15", Category: catUnsafeCondition,
		Description: "Kabel berserakan di area camp", Area: "camp", ResponsibleCompany: "SALAWATI MOTORINDO",
	}
}

func TestValidateSOCAcceptsGoodInput(t *testing.T) {
	sub, errs := validateSOC(validInput(), today, testSnap())
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if sub.Status != statusOpen || sub.Category != catUnsafeCondition || sub.FindingDate.Format("2006-01-02") != "2026-10-15" {
		t.Fatalf("bad submission: %+v", sub)
	}
	if sub.Area != "Camp" {
		t.Errorf("area should use the stored spelling, got %q", sub.Area)
	}
}

// v8: the system suggests, the reporter picks. A topic left empty stays empty (the post goes to HSE
// review as "not picked"); the suggestions that were available are kept to measure accuracy.
func TestValidateSOCDoesNotGuessTheTopic(t *testing.T) {
	sub, errs := validateSOC(validInput(), today, testSnap())
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if sub.TopicID != 0 || sub.HazardID != 0 {
		t.Errorf("topic must not be filled for the reporter, got topic %d hazard %d", sub.TopicID, sub.HazardID)
	}
	if sub.TopicPick != topicPickNone {
		t.Errorf("pick = %q, want %q", sub.TopicPick, topicPickNone)
	}
	if sub.SuggestionsShown != `[{"t":2,"s":3}]` {
		t.Errorf("suggestions shown = %q", sub.SuggestionsShown)
	}
}

func TestValidateSOCRecordsHowTheTopicWasPicked(t *testing.T) {
	in := validInput()
	in.TopicID = "2" // Housekeeping is the suggestion
	sub, errs := validateSOC(in, today, testSnap())
	if len(errs) != 0 || sub.TopicPick != topicPickSuggested || sub.TopicID != 2 || sub.HazardID != 2 {
		t.Errorf("picked from suggestions: %+v %v", sub, errs)
	}
	in.TopicID = "1" // Working at Height was not suggested: the reporter used the full list
	sub, errs = validateSOC(in, today, testSnap())
	if len(errs) != 0 || sub.TopicPick != topicPickList {
		t.Errorf("picked from the list: %+v %v", sub, errs)
	}
	in.Description = "Semua baik"
	in.TopicID = ""
	sub, _ = validateSOC(in, today, testSnap())
	if sub.TopicPick != topicPickNone || sub.SuggestionsShown != "[]" {
		t.Errorf("nothing suggested, nothing picked: %+v", sub)
	}
}

func TestValidateSOCKeepsTheEmployeesOwnChoice(t *testing.T) {
	in := validInput()
	in.TopicID = "1" // the employee picked Working at Height although the words say Housekeeping
	sub, errs := validateSOC(in, today, testSnap())
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if sub.TopicID != 1 || sub.HazardID != 1 {
		t.Errorf("own topic must win and bring its hazard, got topic %d hazard %d", sub.TopicID, sub.HazardID)
	}
	in.HazardID = "2"
	sub, _ = validateSOC(in, today, testSnap())
	if sub.TopicID != 1 || sub.HazardID != 2 {
		t.Errorf("own topic and hazard must both be kept, got %d / %d", sub.TopicID, sub.HazardID)
	}
}

func TestValidateSOCNoMatchLeavesThemEmpty(t *testing.T) {
	in := validInput()
	in.Description = "Semua baik"
	sub, errs := validateSOC(in, today, testSnap())
	if len(errs) != 0 || sub.TopicID != 0 || sub.HazardID != 0 {
		t.Errorf("want empty topic/hazard and no error, got %+v %v", sub, errs)
	}
}

func TestValidateSOCRejects(t *testing.T) {
	cases := map[string]struct {
		mutate func(*socInput)
		field  string
	}{
		"future date":      {func(i *socInput) { i.Date = "2026-10-17" }, "date"},
		"not a date":       {func(i *socInput) { i.Date = "not a date" }, "date"},
		"bad category":     {func(i *socInput) { i.Category = "bogus" }, "category"},
		"old category":     {func(i *socInput) { i.Category = "safe_action" }, "category"},
		"blank text":       {func(i *socInput) { i.Description = "   " }, "description"},
		"no area":          {func(i *socInput) { i.Area = "" }, "area"},
		"unknown area":     {func(i *socInput) { i.Area = "Moon base" }, "area"},
		"unknown topic":    {func(i *socInput) { i.TopicID = "999" }, "topic"},
		"topic not a num":  {func(i *socInput) { i.TopicID = "abc" }, "topic"},
		"unknown hazard":   {func(i *socInput) { i.HazardID = "999" }, "hazard"},
		"too long":         {func(i *socInput) { i.Description = strings.Repeat("a", 5001) }, "description"},
		"company too long": {func(i *socInput) { i.ResponsibleCompany = strings.Repeat("a", 256) }, "responsible_company"},
	}
	for name, c := range cases {
		in := validInput()
		c.mutate(&in)
		_, errs := validateSOC(in, today, testSnap())
		if errs[c.field] == "" {
			t.Errorf("%s: expected an error on %q, got %v", name, c.field, errs)
		}
	}
}

func TestValidateSOCResponsibleCompanyIsOptional(t *testing.T) {
	in := validInput()
	in.ResponsibleCompany = ""
	if _, errs := validateSOC(in, today, testSnap()); len(errs) != 0 {
		t.Errorf("an employee who does not know the company must still be able to report: %v", errs)
	}
}

func TestValidateSOCStatusRules(t *testing.T) {
	// A new post from an employee always starts Open, whatever else is on the form.
	sub, _ := validateSOC(validInput(), today, testSnap())
	if sub.Status != statusOpen {
		t.Fatalf("new post should be open, got %q", sub.Status)
	}
	// A positive finding also starts Open: a Closed post is locked, so staff close it later.
	in := validInput()
	in.Category = catPositive
	sub, errs := validateSOC(in, today, testSnap())
	if len(errs) != 0 || sub.Status != statusOpen {
		t.Fatalf("positive finding should be saved open, got %q %v", sub.Status, errs)
	}
}

func TestSOCRef(t *testing.T) {
	if got := socRef(42); got != "SOC-000042" {
		t.Fatalf("socRef(42) = %q", got)
	}
}

func TestValidateSOCCreditsTheKeywordsOfShownAndPickedTopics(t *testing.T) {
	snap := newSnapshot(
		[]refTopic{{ID: 1, Name: "A", HazardID: 1}, {ID: 2, Name: "B", HazardID: 1}},
		[]refHazard{{ID: 1, Class: "Safety", Name: "H"}}, []string{"Camp"},
		[]keywordRule{{ID: 5, TopicID: 1, Keyword: "kabel", Weight: 1}, {ID: 6, TopicID: 2, Keyword: "kabel", Weight: 2}})
	in := validInput()
	in.TopicID = "2"
	sub, errs := validateSOC(in, today, snap)
	if len(errs) != 0 || !equalIDs(sub.KeywordsSuggested, []int64{5, 6}) || !equalIDs(sub.KeywordsPicked, []int64{6}) {
		t.Errorf("suggested %v picked %v errs %v", sub.KeywordsSuggested, sub.KeywordsPicked, errs)
	}
}
