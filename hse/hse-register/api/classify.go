package main

import (
	"sort"
	"strings"
	"unicode"
)

// keywordRule is one active keyword of a hazard topic. Keyword is already normalized
// (normalizeText), so matching is a plain substring test.
type keywordRule struct {
	ID      int64 // ref_keyword.id, 0 in tests; used to count how often the keyword is suggested / picked
	TopicID int64
	Keyword string
	Whole   bool // true: whole word(s) only; false: a word may start with the keyword (prefix)
	Weight  int
	// PPETopicID is, for a PPE keyword, the hazard topic it implies (helm -> Line of Fire); 0 = none.
	// Used only when no hazard topic matched at all (plan v8, change 3).
	PPETopicID int64

	// Filled in by prepareRules when the snapshot is built, so callers only set the four fields above.
	stem  string // the keyword reduced to its root; keywords of one topic with the same stem are one word family
	root  bool   // the keyword is its own root (bocor, buang): affixed text (kebocoran, dibuang) may match it
	runes []rune // the keyword as letters, for typo tolerance
}

// suggestion is a topic and hazard proposed for a description. Zero ids mean "no suggestion".
type suggestion struct {
	TopicID       int64  `json:"topicId"`
	HazardID      int64  `json:"hazardId"`
	Topic         string `json:"topic"`
	Hazard        string `json:"hazard"`
	Label         string `json:"label,omitempty"`         // the Indonesian wording the SOC form shows
	HighPotential bool   `json:"highPotential,omitempty"` // work that could kill: drives the stop-work prompt
	Score         int    `json:"score,omitempty"`
}

// Typo tolerance (plan v8, section 2, rule 4): a word that is not a real Indonesian word may differ
// from a keyword by one letter when the keyword has 6 or more letters, by two when it has 10 or
// more. The first letter must be the same.
const (
	typoMinLetters1 = 6  // keyword length from which one wrong letter is tolerated
	typoMinLetters2 = 10 // keyword length from which two are
	typoMinWord     = 4  // shorter text words are never treated as typos
)

// typoGuardWords are real words that look like a keyword but are not in the stemmer dictionary or
// that the dictionary misses. They are never "corrected". HSE adds to this list every month from the
// pilot data (plan v8, section 5); a test changes it, so it is a variable.
var typoGuardWords = map[string]bool{
	"sangat": true, "masing": true, "memberikan": true,
}

// prepareRules fills in the derived fields of every rule (stem, root, runes). It returns a copy.
func prepareRules(in []keywordRule) []keywordRule {
	out := make([]keywordRule, len(in))
	for i, r := range in {
		r.stem = stemKeyword(r.Keyword)
		// A keyword that is already an affixed form ("kedalaman") is not a root, so it is never
		// reduced and cannot match the common word "dalam" (plan v8, section 2, rule 3).
		r.root = r.stem == r.Keyword
		r.runes = []rune(r.Keyword)
		out[i] = r
	}
	return out
}

// normalizeText lower-cases the text and turns everything that is not a letter or digit into a
// single space, with a space at both ends. Keywords and descriptions go through the same
// function, so " ketinggian" finds a word that starts with "ketinggian" and " las " finds only the
// whole word "las" (not "palsu" or "alas").
func normalizeText(s string) string {
	var b strings.Builder
	b.WriteByte(' ')
	space := true
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	if !space {
		b.WriteByte(' ')
	}
	return b.String()
}

// matches tests the keyword against the description as written. A root keyword is tested again
// against the description reduced to root words, so "bocor" also finds "kebocoran" without listing
// every affixed form. Only root keywords do this: reducing every keyword matched too much
// ("kedalaman" found "dalam", "merusak" found "rusak").
func (r keywordRule) matches(text, stemmed string) bool {
	if r.Keyword == "" {
		return false
	}
	if r.matchIn(text) {
		return true
	}
	return r.root && r.matchIn(stemmed)
}

func (r keywordRule) matchIn(text string) bool {
	if r.Whole {
		return strings.Contains(text, " "+r.Keyword+" ")
	}
	return strings.Contains(text, " "+r.Keyword)
}

// typoMatches reports whether a stretch of the text is a typo of the keyword. A stretch is as many
// consecutive words as the keyword has ("temapat makan" for "tempat makan") and must contain at
// least one word that is not a real Indonesian word, so two correct words are never "corrected".
func (r keywordRule) typoMatches(idx *typoIndex) bool {
	n := len(r.runes)
	if idx == nil || n < typoMinLetters1 {
		return false
	}
	maxDist := 1
	if n >= typoMinLetters2 {
		maxDist = 2
	}
	for _, w := range idx.stretches(strings.Count(r.Keyword, " ") + 1) {
		if w[0] != r.runes[0] {
			continue
		}
		if r.Whole {
			if osaDistance(w, r.runes) <= maxDist {
				return true
			}
			continue
		}
		// Prefix keyword: compare with the start of the stretch, as an exact prefix match would.
		for l := n - maxDist; l <= n+maxDist; l++ {
			if l >= 1 && l <= len(w) && osaDistance(w[:l], r.runes) <= maxDist {
				return true
			}
		}
	}
	return false
}

// typoIndex holds the words of a description that typo tolerance may correct, and builds the
// stretches of consecutive words that contain one of them.
type typoIndex struct {
	words   [][]rune
	unknown []bool
	cache   map[int][][]rune
}

// stretches returns every run of k consecutive words with at least one unknown word, each joined
// with single spaces.
func (x *typoIndex) stretches(k int) [][]rune {
	if out, ok := x.cache[k]; ok {
		return out
	}
	var out [][]rune
	for i := 0; i+k <= len(x.words); i++ {
		has := false
		var joined []rune
		for j := i; j < i+k; j++ {
			has = has || x.unknown[j]
			if j > i {
				joined = append(joined, ' ')
			}
			joined = append(joined, x.words[j]...)
		}
		if has {
			out = append(out, joined)
		}
	}
	x.cache[k] = out
	return out
}

// osaDistance is the edit distance counting a changed, missing or extra letter, or two neighbouring
// letters swapped ("temapat" -> "tempat"), as one step each.
func osaDistance(a, b []rune) int {
	prev2 := make([]int, len(b)+1)
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d := min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				d = min(d, prev2[j-2]+1)
			}
			cur[j] = d
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(b)]
}

// analyzeText normalizes a description and returns it, the same text reduced to root words, and the
// typo index. A word may be corrected when it is not a real Indonesian word, not guarded, long
// enough and without digits; the index is nil when there is no such word.
func analyzeText(description string, guard map[string]bool) (text, stemmed string, typos *typoIndex) {
	text = normalizeText(description)
	words := strings.Fields(text)
	if len(words) == 0 {
		return text, " ", nil
	}
	stems := make([]string, len(words))
	idx := &typoIndex{words: make([][]rune, len(words)), unknown: make([]bool, len(words)), cache: map[int][][]rune{}}
	found := false
	for i, w := range words {
		stems[i] = stemWord(w)
		idx.words[i] = []rune(w)
		if len(idx.words[i]) >= typoMinWord && !typoGuardWords[w] && !guard[w] && strings.IndexFunc(w, unicode.IsDigit) < 0 && !knownWord(w, stems[i]) {
			idx.unknown[i] = true
			found = true
		}
	}
	if !found {
		idx = nil
	}
	return text, " " + strings.Join(stems, " ") + " ", idx
}

type topicHit struct {
	topic refTopic
	score int
}

// matchedKeyword is a keyword that matched a description, with the topic it counts for.
type matchedKeyword struct {
	ID, Topic int64
}

// rank scores every topic for the description and lists those with a score, best first. Each word
// family (keywords of one topic with the same root: buang, membuang, dibuang) counts once, with its
// highest weight, so a spelling variant does not count twice (plan v8, rule 5). A tie goes to the
// topic listed first (lowest sort order), so the answer never depends on map order.
//
// With hazardOnly, if no hazard topic scored at all, the PPE keywords that matched stand in for the
// hazard they imply (plan v8, change 3). The second result lists every keyword that counted.
func (s *refSnapshot) rank(description string, hazardOnly bool) ([]topicHit, []matchedKeyword) {
	text, stemmed, typos := analyzeText(description, s.guard)
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	type family struct {
		topic int64
		stem  string
	}
	best := map[family]int{}
	var hit []keywordRule
	for _, r := range s.rules {
		if !r.matches(text, stemmed) && !r.typoMatches(typos) {
			continue
		}
		hit = append(hit, r)
		k := family{r.TopicID, r.stem}
		if r.Weight > best[k] {
			best[k] = r.Weight
		}
	}
	score := map[int64]int{}
	for k, w := range best {
		score[k.topic] += w
	}
	hits := s.hitsFrom(score, hazardOnly)
	matched := make([]matchedKeyword, 0, len(hit))
	if len(hits) == 0 && hazardOnly {
		for _, r := range hit {
			if r.PPETopicID != 0 {
				score[r.PPETopicID] += r.Weight
				matched = append(matched, matchedKeyword{r.ID, r.PPETopicID})
			}
		}
		return s.hitsFrom(score, hazardOnly), matched
	}
	for _, r := range hit {
		matched = append(matched, matchedKeyword{r.ID, r.TopicID})
	}
	return hits, matched
}

// hitsFrom lists the topics that have a score, best first (ties by sort order).
func (s *refSnapshot) hitsFrom(score map[int64]int, hazardOnly bool) []topicHit {
	var hits []topicHit
	for _, t := range s.Topics { // Topics is in sort order
		if hazardOnly && t.Control {
			continue
		}
		if sc := score[t.ID]; sc > 0 {
			hits = append(hits, topicHit{t, sc})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	return hits
}

// keywordCredit lists the keywords to count after a SOC post is sent: those that matched a topic
// the form showed (suggested) and, of those, the ones of the topic the reporter picked (picked,
// empty when nothing was picked). A keyword often suggested but never picked is a removal
// candidate (plan v8, change 6).
func (s *refSnapshot) keywordCredit(description string, n int, pickedTopic int64) (suggested, picked []int64) {
	hits, matched := s.rank(description, true)
	if len(hits) > n {
		hits = hits[:n]
	}
	shown := map[int64]bool{}
	for _, h := range hits {
		shown[h.topic.ID] = true
	}
	for _, m := range matched {
		if m.ID == 0 || !shown[m.Topic] {
			continue
		}
		suggested = append(suggested, m.ID)
		if pickedTopic != 0 && m.Topic == pickedTopic {
			picked = append(picked, m.ID)
		}
	}
	return suggested, picked
}

func (s *refSnapshot) toSuggestion(h topicHit) suggestion {
	out := suggestion{TopicID: h.topic.ID, Topic: h.topic.Name, Label: h.topic.DisplayLabel(),
		HighPotential: h.topic.HighPotential, Score: h.score}
	if hz, ok := s.hazardByID[h.topic.HazardID]; ok && !hz.Archived {
		out.HazardID, out.Hazard = hz.ID, hz.Name
	}
	return out
}

// SuggestTop proposes up to n topics for a description, best first. hazardOnly leaves out control
// topics (PPE, Permit To Work, ...): the SOC form offers hazard topics only, because the control tag
// comes from the picklist answer (plan v8, change 2). No match gives an empty list.
func (s *refSnapshot) SuggestTop(description string, n int, hazardOnly bool) []suggestion {
	hits, _ := s.rank(description, hazardOnly)
	if len(hits) > n {
		hits = hits[:n]
	}
	out := make([]suggestion, len(hits))
	for i, h := range hits {
		out[i] = s.toSuggestion(h)
	}
	return out
}

// Suggest proposes the one topic whose keywords score highest in the description, and that topic's
// hazard. The staff form fills the topic from it; the SOC form uses SuggestTop and lets the
// reporter pick. No match gives an empty suggestion.
func (s *refSnapshot) Suggest(description string) suggestion {
	if top := s.SuggestTop(description, 1, false); len(top) > 0 {
		return top[0]
	}
	return suggestion{}
}
