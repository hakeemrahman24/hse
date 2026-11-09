package sofi

import "testing"

func answers(v int) []int {
	out := make([]int, ItemCount)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestItemsAreFiveDimensionsOfFourItems(t *testing.T) {
	if len(Items) != ItemCount || ItemCount != 20 {
		t.Fatalf("items = %d", len(Items))
	}
	per := map[string]int{}
	for _, it := range Items {
		if it.EN == "" || it.ID == "" {
			t.Fatalf("item %d has an empty word: %+v", it.Number, it)
		}
		per[it.Dimension]++
	}
	if len(per) != len(Dimensions) {
		t.Fatalf("dimensions used = %v", per)
	}
	for _, d := range Dimensions {
		if per[d.Key] != itemsPerDimension {
			t.Fatalf("%s has %d items", d.Key, per[d.Key])
		}
	}
}

func TestScoreSumsEachDimension(t *testing.T) {
	a := answers(0)
	// Items 1-4 are Lack of energy: 6+5+4+3 = 18.
	copy(a[0:4], []int{6, 5, 4, 3})
	r, err := Score(a)
	if err != nil {
		t.Fatal(err)
	}
	if r.Dimensions[0].Key != "lack_of_energy" || r.Dimensions[0].Score != 18 {
		t.Fatalf("lack of energy = %+v", r.Dimensions[0])
	}
	for _, d := range r.Dimensions[1:] {
		if d.Score != 0 {
			t.Fatalf("%s = %d, want 0", d.Key, d.Score)
		}
	}
	if r.Total != 18 {
		t.Fatalf("total = %d", r.Total)
	}
}

func TestScoreBandsAtTheEdges(t *testing.T) {
	cases := []struct {
		name  string
		value int // every answer; each dimension then scores 4 * value and the total 20 * value
		dim   string
		total string
	}{
		{"all zero", 0, LevelLow, LevelLow},
		{"all two", 2, LevelLow, LevelLow},             // 8 per dimension (top of Low), total 40 (top of Low)
		{"all three", 3, LevelModerate, LevelModerate}, // 12, total 60
		{"all four", 4, LevelModerate, LevelModerate},  // 16 (top of Moderate), total 80 (top of Moderate)
		{"all five", 5, LevelHigh, LevelHigh},          // 20, total 100
		{"all six", 6, LevelHigh, LevelHigh},           // 24, total 120
	}
	for _, c := range cases {
		r, err := Score(answers(c.value))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		for _, d := range r.Dimensions {
			if d.Level != c.dim {
				t.Fatalf("%s: %s level = %s (score %d), want %s", c.name, d.Key, d.Level, d.Score, c.dim)
			}
		}
		if r.Level != c.total {
			t.Fatalf("%s: overall = %s (total %d), want %s", c.name, r.Level, r.Total, c.total)
		}
	}
}

func TestScoreRejectsBadAnswers(t *testing.T) {
	short := answers(1)[:ItemCount-1]
	tooHigh := answers(1)
	tooHigh[7] = 7
	negative := answers(1)
	negative[0] = -1
	for name, a := range map[string][]int{"short": short, "too high": tooHigh, "negative": negative, "nil": nil} {
		if _, err := Score(a); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

// Answers saved by the earlier 25-word form still score: the five words the 20-item version left
// out (Exhausted, Warm, Hurting, Lack of concern, Lazy) are ignored and the rest are summed.
func TestScoreReadsTheEarlier25WordAnswers(t *testing.T) {
	old := make([]int, legacyItemCount)
	for i := range old {
		old[i] = 1
	}
	for pos := range legacyDropped {
		old[pos] = 6 // would push the score up if it were counted
	}
	r, err := Score(old)
	if err != nil {
		t.Fatal(err)
	}
	if r.Total != ItemCount || r.TotalMax != ItemCount*MaxAnswer {
		t.Fatalf("total = %d of %d, want %d of %d", r.Total, r.TotalMax, ItemCount, ItemCount*MaxAnswer)
	}
	for _, d := range r.Dimensions {
		if d.Score != itemsPerDimension || d.Max != itemsPerDimension*MaxAnswer {
			t.Fatalf("%s = %d of %d", d.Key, d.Score, d.Max)
		}
	}
	if _, err := Score(make([]int, 24)); err == nil {
		t.Fatal("24 answers were accepted")
	}
}
