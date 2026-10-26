package main

import "testing"

func stemSnapshot(rules []keywordRule) *refSnapshot {
	return newSnapshot(
		[]refTopic{{ID: 1, Name: "One", HazardID: 1}, {ID: 2, Name: "Two", HazardID: 1}},
		[]refHazard{{ID: 1, Class: "Safety", Name: "H"}}, nil, rules)
}

func TestStemTextReducesAffixedWords(t *testing.T) {
	cases := []struct{ in, want string }{
		{"membakar sampah", " bakar sampah "},
		{"kebocoran gas", " bocor gas "},
		{"dibuang sembarangan", " buang sembarang "},
		{"", " "},
	}
	for _, c := range cases {
		if got := stemText(normalizeText(c.in)); got != c.want {
			t.Errorf("stemText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSuggestFindsAffixedFormOfRootKeyword(t *testing.T) {
	snap := stemSnapshot(
		[]keywordRule{{TopicID: 1, Keyword: "bocor", Weight: 1}, {TopicID: 2, Keyword: "buang", Whole: true, Weight: 1}})
	if got := snap.Suggest("ada kebocoran di pipa"); got.TopicID != 1 {
		t.Errorf("kebocoran should match root keyword bocor, got topic %d", got.TopicID)
	}
	if got := snap.Suggest("sampah dibuang sembarangan"); got.TopicID != 2 {
		t.Errorf("dibuang should match whole-word keyword buang, got topic %d", got.TopicID)
	}
}

func TestSuggestStemmingKeepsWholeWordBoundaries(t *testing.T) {
	snap := stemSnapshot([]keywordRule{{TopicID: 1, Keyword: "las", Whole: true, Weight: 1}})
	if got := snap.Suggest("keadaan palsu dan alas kaki"); got.TopicID != 0 {
		t.Errorf("las must not match inside other words, got topic %d", got.TopicID)
	}
}
