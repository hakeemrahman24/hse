package main

import "testing"

// v8Snapshot builds a small snapshot with topics One..Three (Control marks a control topic).
func v8Snapshot(rules ...keywordRule) *refSnapshot {
	return newSnapshot(
		[]refTopic{
			{ID: 1, Name: "One", HazardID: 1, Label: "Satu"},
			{ID: 2, Name: "Two", HazardID: 1, HighPotential: true},
			{ID: 3, Name: "Three", HazardID: 1},
			{ID: 4, Name: "Ctl", Control: true},
		},
		[]refHazard{{ID: 1, Class: "Safety", Name: "H"}}, nil, rules)
}

func topicIDs(got []suggestion) []int64 {
	out := make([]int64, len(got))
	for i, g := range got {
		out[i] = g.TopicID
	}
	return out
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSuggestTopReturnsBestThreeInScoreOrder(t *testing.T) {
	snap := v8Snapshot(
		keywordRule{TopicID: 1, Keyword: "alpha", Weight: 1},
		keywordRule{TopicID: 2, Keyword: "alpha", Weight: 3},
		keywordRule{TopicID: 3, Keyword: "alpha", Weight: 2},
		keywordRule{TopicID: 4, Keyword: "alpha", Weight: 9},
	)
	got := snap.SuggestTop("alpha", 3, false)
	if want := []int64{4, 2, 3}; !equalIDs(topicIDs(got), want) {
		t.Errorf("top 3 = %v, want %v", topicIDs(got), want)
	}
	got = snap.SuggestTop("alpha", 3, true) // hazard topics only: the control topic is dropped
	if want := []int64{2, 3, 1}; !equalIDs(topicIDs(got), want) {
		t.Errorf("hazard-only top 3 = %v, want %v", topicIDs(got), want)
	}
	if !got[0].HighPotential || got[2].Label != "Satu" {
		t.Errorf("label / high-potential flag not carried: %+v", got)
	}
	if one := snap.SuggestTop("alpha", 1, true); len(one) != 1 {
		t.Errorf("n=1 returned %d suggestions", len(one))
	}
	if none := snap.SuggestTop("selamat pagi", 3, true); len(none) != 0 {
		t.Errorf("no match should give no suggestions, got %+v", none)
	}
}

func TestAffixedKeywordIsNotReducedToACommonRoot(t *testing.T) {
	// "kedalaman" is already an affixed form, so it must not match the everyday word "dalam".
	snap := v8Snapshot(keywordRule{TopicID: 1, Keyword: "kedalaman", Weight: 2})
	if got := snap.SuggestTop("masuk dalam ruangan", 3, true); len(got) != 0 {
		t.Errorf("kedalaman matched 'dalam': %+v", got)
	}
	if got := snap.SuggestTop("kedalaman galian", 3, true); len(got) != 1 {
		t.Errorf("kedalaman should still match itself: %+v", got)
	}
}

func TestTypoToleranceCatchesUnknownWords(t *testing.T) {
	snap := v8Snapshot(
		keywordRule{TopicID: 1, Keyword: "harness", Weight: 2},
		keywordRule{TopicID: 2, Keyword: "laundry", Weight: 2},
		keywordRule{TopicID: 3, Keyword: "scaffolding", Weight: 2},
	)
	cases := []struct {
		name, text string
		want       int64
	}{
		{"one letter missing", "pekerja tidak pakai harnes", 1},
		{"one letter wrong", "ruang loundry kotor", 2},
		{"two letters off, keyword 10+ letters", "perancah scafoldng rusak", 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := snap.SuggestTop(c.text, 3, true)
			if len(got) == 0 || got[0].TopicID != c.want {
				t.Errorf("SuggestTop(%q) = %+v, want topic %d first", c.text, got, c.want)
			}
		})
	}
}

func TestTypoToleranceLimits(t *testing.T) {
	snap := v8Snapshot(
		keywordRule{TopicID: 1, Keyword: "harness", Weight: 2},
		keywordRule{TopicID: 2, Keyword: "sengat", Weight: 2},
		keywordRule{TopicID: 3, Keyword: "scaffolding", Weight: 2},
	)
	cases := []struct{ name, text string }{
		{"a real Indonesian word is never corrected", "sangat panas hari ini"},
		{"first letter must match", "arness rusak"},
		{"two letters off is too far for a 7-letter keyword", "hrnes rusak"},
		{"three letters off is too far for 11 letters", "scafoldg rusak"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := snap.SuggestTop(c.text, 3, true); len(got) != 0 {
				t.Errorf("SuggestTop(%q) matched %+v", c.text, got)
			}
		})
	}
}

func TestTypoGuardListStopsCorrection(t *testing.T) {
	snap := v8Snapshot(keywordRule{TopicID: 1, Keyword: "harness", Weight: 2})
	if got := snap.SuggestTop("harnes", 3, true); len(got) != 1 {
		t.Fatalf("setup: harnes should be corrected, got %+v", got)
	}
	typoGuardWords["harnes"] = true
	t.Cleanup(func() { delete(typoGuardWords, "harnes") })
	if got := snap.SuggestTop("harnes", 3, true); len(got) != 0 {
		t.Errorf("a guarded word was still corrected: %+v", got)
	}
}

func TestWordFamilyCountsOnce(t *testing.T) {
	// Without the word-family rule topic One scores 1+1 = 2 for "membuang" (buang is a root keyword
	// caught through the stemmer, membuang matches directly) and wins the tie against Two.
	snap := v8Snapshot(
		keywordRule{TopicID: 1, Keyword: "buang", Weight: 1},
		keywordRule{TopicID: 1, Keyword: "membuang", Weight: 1},
		keywordRule{TopicID: 2, Keyword: "sampah", Weight: 2},
	)
	got := snap.SuggestTop("membuang sampah", 3, true)
	if len(got) != 2 || got[0].TopicID != 2 || got[0].Score != 2 || got[1].Score != 1 {
		t.Errorf("word family counted twice: %+v", got)
	}
}

func TestWordFamilyKeepsTheHighestWeight(t *testing.T) {
	snap := v8Snapshot(
		keywordRule{TopicID: 1, Keyword: "buang", Weight: 1},
		keywordRule{TopicID: 1, Keyword: "dibuang", Weight: 3},
	)
	got := snap.SuggestTop("sampah dibuang", 3, true)
	if len(got) != 1 || got[0].Score != 3 {
		t.Errorf("family should score its highest weight (3): %+v", got)
	}
}

func TestSuggestKeepsTopOneBehaviour(t *testing.T) {
	snap := v8Snapshot(keywordRule{TopicID: 2, Keyword: "abc", Weight: 2})
	if got := snap.Suggest("abc"); got.TopicID != 2 || got.HazardID != 1 || got.Topic != "Two" {
		t.Errorf("Suggest = %+v", got)
	}
}

func TestTypoToleranceCoversTwoWordKeywords(t *testing.T) {
	snap := v8Snapshot(keywordRule{TopicID: 2, Keyword: "tempat makan", Weight: 2})
	if got := snap.SuggestTop("temapat makan kotor", 3, true); len(got) != 1 || got[0].TopicID != 2 {
		t.Errorf("'temapat makan' should reach 'tempat makan': %+v", got)
	}
	if got := snap.SuggestTop("tempat tidur", 3, true); len(got) != 0 {
		t.Errorf("unrelated real words matched: %+v", got)
	}
}

// A report that only names PPE has no hazard topic of its own. The PPE keyword carries the hazard
// it implies (helm -> Line of Fire); it is offered only when nothing else matched, and only as a
// suggestion the reporter can pick.
func TestPPEFallbackOffersTheImpliedHazardOnlyWhenNothingElseMatches(t *testing.T) {
	snap := v8Snapshot(
		keywordRule{TopicID: 4, Keyword: "helm", Whole: true, Weight: 1, PPETopicID: 3},
		keywordRule{TopicID: 1, Keyword: "kabel", Weight: 1},
	)
	got := snap.SuggestTop("pekerja tidak pakai helm", 3, true)
	if len(got) != 1 || got[0].TopicID != 3 {
		t.Errorf("PPE-only report should offer topic 3: %+v", got)
	}
	got = snap.SuggestTop("tidak pakai helm dekat kabel", 3, true)
	if len(got) != 1 || got[0].TopicID != 1 {
		t.Errorf("a real hazard match must win over the PPE fallback: %+v", got)
	}
	if got := snap.SuggestTop("tidak pakai helm", 3, false); len(got) != 1 || got[0].TopicID != 4 {
		t.Errorf("staff suggestion (all topics) is unchanged: %+v", got)
	}
}

func TestKeywordCreditFollowsShownAndPickedTopics(t *testing.T) {
	snap := v8Snapshot(
		keywordRule{ID: 10, TopicID: 1, Keyword: "alpha", Weight: 1},
		keywordRule{ID: 11, TopicID: 2, Keyword: "alpha", Weight: 2},
		keywordRule{ID: 12, TopicID: 3, Keyword: "beta", Weight: 1},
	)
	suggested, picked := snap.keywordCredit("alpha", 3, 2)
	if !equalIDs(suggested, []int64{10, 11}) || !equalIDs(picked, []int64{11}) {
		t.Errorf("suggested %v picked %v", suggested, picked)
	}
	suggested, picked = snap.keywordCredit("alpha", 3, 0) // nothing picked
	if len(suggested) != 2 || len(picked) != 0 {
		t.Errorf("no pick: suggested %v picked %v", suggested, picked)
	}
}

func TestSnapshotGuardWordsStopCorrection(t *testing.T) {
	snap := v8Snapshot(keywordRule{TopicID: 1, Keyword: "harness", Weight: 2})
	snap.guard = map[string]bool{"harnes": true}
	if got := snap.SuggestTop("harnes", 3, true); len(got) != 0 {
		t.Errorf("a word on the saved guard list was corrected: %+v", got)
	}
}
