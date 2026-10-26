package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const seedDir = "../migrations/seed"

func parseRealSeeds(t *testing.T) ([]hazardSeed, []topicSeed, []keywordSeed) {
	t.Helper()
	open := func(name string) *os.File {
		f, err := os.Open(filepath.Join(seedDir, name))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return f
	}
	h, err := parseHazardSeed(open("ref_hazard_seed.csv"))
	if err != nil {
		t.Fatalf("hazards: %v", err)
	}
	tp, err := parseTopicSeed(open("ref_soc_topic_seed.csv"))
	if err != nil {
		t.Fatalf("topics: %v", err)
	}
	k, err := parseKeywordSeed(open("ref_keyword_seed.csv"))
	if err != nil {
		t.Fatalf("keywords: %v", err)
	}
	return h, tp, k
}

func TestSeedFilesAreConsistent(t *testing.T) {
	hazards, topics, keywords := parseRealSeeds(t)
	if len(hazards) != 52 {
		t.Errorf("hazard categories = %d, want 52", len(hazards))
	}
	hazardNames := map[string]bool{}
	for _, h := range hazards {
		if hazardNames[h.Name] {
			t.Errorf("duplicate hazard %q", h.Name)
		}
		hazardNames[h.Name] = true
	}
	topicNames := map[string]bool{}
	hazardTopics := 0
	for _, tp := range topics {
		topicNames[tp.Name] = true
		if tp.Kind == "hazard" {
			hazardTopics++
		}
		if tp.HazardName != "" && !hazardNames[tp.HazardName] {
			t.Errorf("topic %q points to unknown hazard %q", tp.Name, tp.HazardName)
		}
	}
	if hazardTopics != 40 {
		t.Errorf("hazard topics = %d, want 40", hazardTopics)
	}
	for _, k := range keywords {
		if !topicNames[k.Topic] {
			t.Errorf("keyword %q points to unknown topic %q", k.Keyword, k.Topic)
		}
	}
	if len(keywords) != 1276 {
		t.Errorf("keywords = %d, want 1276", len(keywords))
	}
}

func TestParseSeedRejectsBadRows(t *testing.T) {
	if _, err := parseKeywordSeed(strings.NewReader("topic,keyword,match_type,weight,is_active,source\nA,x,fuzzy,1,1,s\n")); err == nil {
		t.Error("unknown match type was accepted")
	}
	if _, err := parseKeywordSeed(strings.NewReader("topic,keyword,match_type,weight,is_active,source\nA,x,prefix,0,1,s\n")); err == nil {
		t.Error("weight 0 was accepted")
	}
	if _, err := parseTopicSeed(strings.NewReader("sort_order,topic,kind,suggested_hazard_category\n1,A,other,\n")); err == nil {
		t.Error("unknown topic kind was accepted")
	}
	if _, err := parseHazardSeed(strings.NewReader("x,y\n1,2\n")); err == nil {
		t.Error("missing columns were accepted")
	}
}

// realSnapshot builds a snapshot from the real seed files, with ids equal to the row numbers.
func realSnapshot(t *testing.T) *refSnapshot {
	t.Helper()
	hazards, topics, keywords := parseRealSeeds(t)
	var hs []refHazard
	hazardID := map[string]int64{}
	for i, h := range hazards {
		id := int64(i + 1)
		hazardID[h.Name] = id
		hs = append(hs, refHazard{ID: id, Class: h.Class, Name: h.Name})
	}
	var ts []refTopic
	topicID := map[string]int64{}
	hazardTopic := map[int64]bool{}
	for i, tp := range topics {
		id := int64(i + 1)
		topicID[tp.Name] = id
		if tp.Kind == "hazard" {
			ts = append(ts, refTopic{ID: id, Name: tp.Name, HazardID: hazardID[tp.HazardName]})
			hazardTopic[id] = true
		}
	}
	var rules []keywordRule
	for _, k := range keywords {
		if !k.Active || !hazardTopic[topicID[k.Topic]] {
			continue
		}
		rules = append(rules, keywordRule{TopicID: topicID[k.Topic], Keyword: strings.TrimSpace(normalizeText(k.Keyword)),
			Whole: k.MatchType == "whole", Weight: k.Weight})
	}
	return newSnapshot(ts, hs, []string{"Camp", "Workshop"}, rules)
}

func TestSuggestWithRealKeywords(t *testing.T) {
	snap := realSnapshot(t)
	cases := []struct {
		name, text, topic, hazard string
	}{
		{"indonesian height", "Pekerja bekerja di ketinggian tanpa harnes", "Working at Height", "Fall from Height"},
		{"punctuation and case", "KETINGGIAN!!! (tanpa harnes)", "Working at Height", "Fall from Height"},
		{"welding by whole word", "Ada pekerjaan las di bengkel", "Hot Work", "Fire"},
		{"nothing matches", "Selamat pagi semuanya", "", ""},
		{"empty", "   ", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := snap.Suggest(c.text)
			if got.Topic != c.topic || got.Hazard != c.hazard {
				t.Errorf("Suggest(%q) = %q / %q, want %q / %q", c.text, got.Topic, got.Hazard, c.topic, c.hazard)
			}
		})
	}
}

func TestSuggestWholeWordDoesNotMatchInsideWords(t *testing.T) {
	snap := newSnapshot(
		[]refTopic{{ID: 1, Name: "Hot Work", HazardID: 1}},
		[]refHazard{{ID: 1, Class: "Safety", Name: "Fire"}}, nil,
		[]keywordRule{{TopicID: 1, Keyword: "las", Whole: true, Weight: 1}})
	if got := snap.Suggest("keadaan palsu dan alas kaki"); got.TopicID != 0 {
		t.Errorf("'las' matched inside another word: %+v", got)
	}
	if got := snap.Suggest("kerja las"); got.TopicID != 1 {
		t.Errorf("whole word 'las' not matched: %+v", got)
	}
}

func TestSuggestTieGoesToFirstTopic(t *testing.T) {
	snap := newSnapshot(
		[]refTopic{{ID: 7, Name: "First"}, {ID: 3, Name: "Second"}}, nil, nil,
		[]keywordRule{{TopicID: 3, Keyword: "abc", Weight: 2}, {TopicID: 7, Keyword: "abc", Weight: 2}})
	for i := 0; i < 20; i++ { // repeated, so a map-order dependence would show up
		if got := snap.Suggest("abc"); got.TopicID != 7 {
			t.Fatalf("tie went to topic %d, want the first listed (7)", got.TopicID)
		}
	}
}

func TestHazardGroupsKeepClassOrder(t *testing.T) {
	snap := newSnapshot(nil, []refHazard{{ID: 1, Class: "Safety", Name: "A"}, {ID: 2, Class: "Safety", Name: "B"}, {ID: 3, Class: "Chemical", Name: "C"}}, nil, nil)
	g := snap.HazardGroups()
	if len(g) != 2 || g[0].Class != "Safety" || len(g[0].Items) != 2 || g[1].Class != "Chemical" {
		t.Errorf("groups = %+v", g)
	}
}

func TestCanonicalArea(t *testing.T) {
	snap := newSnapshot(nil, nil, []string{"Camp", "PCC / TCF"}, nil)
	if a, ok := snap.CanonicalArea("  pcc / tcf "); !ok || a != "PCC / TCF" {
		t.Errorf("got %q %v", a, ok)
	}
	if _, ok := snap.CanonicalArea("Moon"); ok {
		t.Error("unknown area accepted")
	}
}

func TestGroupsGatherLateCategoryUnderItsClass(t *testing.T) {
	// A category added later to an old class sits after the other classes in table order.
	snap := newSnapshot(nil, []refHazard{{ID: 1, Class: "Safety", Name: "A"}, {ID: 2, Class: "Chemical", Name: "C"}, {ID: 3, Class: "Safety", Name: "B"}}, nil, nil)
	g := snap.HazardGroups()
	if len(g) != 2 || g[0].Class != "Safety" || len(g[0].Items) != 2 || g[1].Class != "Chemical" {
		t.Errorf("groups = %+v", g)
	}
}

func TestFormGroupsHideArchivedExceptTheKeptOne(t *testing.T) {
	snap := newSnapshot(nil, []refHazard{
		{ID: 1, Class: "Safety", Name: "A"},
		{ID: 2, Class: "Safety", Name: "B", Archived: true},
		{ID: 3, Class: "Chemical", Name: "C", Archived: true},
	}, nil, nil)
	if n := len(snap.HazardGroups()); n != 2 {
		t.Errorf("sidebar groups = %d, want 2 (archived stay visible there)", n)
	}
	g := snap.FormHazardGroups(0)
	if len(g) != 1 || len(g[0].Items) != 1 || g[0].Items[0].ID != 1 {
		t.Errorf("form groups = %+v, want only A", g)
	}
	g = snap.FormHazardGroups(2)
	if len(g) != 1 || len(g[0].Items) != 2 {
		t.Errorf("form groups keeping 2 = %+v, want A and B", g)
	}
}

func TestResolveClassificationRefusesArchivedHazardUnlessKept(t *testing.T) {
	snap := newSnapshot(nil, []refHazard{{ID: 2, Class: "Safety", Name: "B", Archived: true}}, nil, nil)
	errs := map[string]string{}
	resolveClassification("", "2", "x", snap, 0, true, errs)
	if errs["hazard"] == "" {
		t.Error("archived hazard accepted for a new post")
	}
	errs = map[string]string{}
	if _, h := resolveClassification("", "2", "x", snap, 2, true, errs); errs["hazard"] != "" || h != 2 {
		t.Errorf("kept archived hazard refused: %v %d", errs, h)
	}
}

func TestControlClassesFollowTheHierarchyAndKeepEmptyClasses(t *testing.T) {
	snap := newSnapshot([]refTopic{
		{ID: 1, Name: "Working at Height", HazardID: 1},
		{ID: 2, Name: "Permit To Work", Control: true, ControlClass: "Administrative"},
		{ID: 3, Name: "PPE", Control: true, ControlClass: "PPE"},
		{ID: 4, Name: "Barricade / Sign", Control: true, ControlClass: "Engineering"},
		{ID: 5, Name: "Odd One", Control: true, ControlClass: "Something else"},
	}, []refHazard{{ID: 1, Class: "Safety", Name: "Fall from Height"}}, nil, nil)
	var names []string
	for _, g := range snap.ControlClasses() {
		names = append(names, g.Name)
	}
	if got := strings.Join(names, ","); got != "Elimination,Substitution,Engineering,Administrative,PPE,Other" {
		t.Fatalf("classes out of order or missing: %s", got)
	}
	if g := snap.ControlClasses(); len(g[0].Topics) != 0 || g[2].Topics[0].Name != "Barricade / Sign" || g[5].Topics[0].Name != "Odd One" {
		t.Fatalf("topics under the wrong class: %+v", g)
	}
	var groups []string
	for _, g := range snap.TopicGroups() {
		groups = append(groups, g.Label())
	}
	if got := strings.Join(groups, "|"); got != "Hazard|Control · Engineering|Control · Administrative|Control · PPE|Control · Other" {
		t.Fatalf("dropdown groups: %s", got)
	}
}

func TestSeedHasV8TopicLabelsAndPicklists(t *testing.T) {
	_, topics, _ := parseRealSeeds(t)
	high := 0
	for _, tp := range topics {
		if tp.Label == "" {
			t.Errorf("topic %q has no Indonesian label", tp.Name)
		}
		if tp.HighPotential {
			high++
		}
	}
	if high != 13 { // plan v8 section 6, decision 2
		t.Errorf("high-potential topics = %d, want 13", high)
	}
	f, err := os.Open(filepath.Join(seedDir, "ref_picklist_seed.csv"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	items, err := parsePicklistSeed(f)
	if err != nil {
		t.Fatal(err)
	}
	lists := map[string]int{}
	for _, it := range items {
		lists[it.List]++
	}
	want := map[string]int{"unsafe_reason": 7, "ppe_item": 10, "good_practice": 8, "stopped_by": 3, "yes_no": 3}
	for name, n := range want {
		if lists[name] != n {
			t.Errorf("picklist %s has %d options, want %d", name, lists[name], n)
		}
	}
}

func TestParsePicklistSeedRejectsBadRows(t *testing.T) {
	if _, err := parsePicklistSeed(strings.NewReader("list_name,option_order,label_id\nx,0,Ya\n")); err == nil {
		t.Error("option order 0 was accepted")
	}
	if _, err := parsePicklistSeed(strings.NewReader("list_name,option_order,label_id\nx,1,\n")); err == nil {
		t.Error("an empty label was accepted")
	}
}

func TestSeedPPEKeywordsPointToHazardTopics(t *testing.T) {
	_, topics, keywords := parseRealSeeds(t)
	hazard := map[string]bool{}
	for _, tp := range topics {
		hazard[tp.Name] = tp.Kind == "hazard"
	}
	n := 0
	for _, k := range keywords {
		if k.PPETopic == "" {
			continue
		}
		n++
		if k.Topic != "PPE" || !hazard[k.PPETopic] {
			t.Errorf("keyword %q (%s) implies %q, which is not a hazard topic", k.Keyword, k.Topic, k.PPETopic)
		}
	}
	if n != 35 {
		t.Errorf("PPE keywords with an implied hazard = %d, want 35", n)
	}
}
