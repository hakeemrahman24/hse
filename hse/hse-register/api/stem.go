package main

import (
	"strings"
	"sync"

	"github.com/RadhiFadlillah/go-sastrawi"
)

// The stemmer builds its root-word dictionary once; it is safe for concurrent use afterwards.
var (
	stemmerOnce sync.Once
	stemmer     sastrawi.Stemmer
	rootWords   sastrawi.Dictionary
)

func initStemmer() {
	stemmerOnce.Do(func() {
		rootWords = sastrawi.DefaultDictionary()
		stemmer = sastrawi.NewStemmer(rootWords)
	})
}

func stemWord(w string) string {
	initStemmer()
	return stemmer.Stem(w)
}

// knownWord reports whether a text word is a real Indonesian word: it is a root word itself, or
// its stem is one. Typo tolerance (classify.go) only "corrects" words that fail this test, so a
// real word such as "sangat" is never turned into the keyword "sengat". A word the stemmer cannot
// reduce comes back unchanged, which is not in the dictionary either, so it counts as unknown.
// stem is stemWord(word); the caller has it already, so it is not computed twice.
func knownWord(word, stem string) bool {
	initStemmer()
	return rootWords.Contains(word) || rootWords.Contains(stem)
}

// stemText reduces every word of a normalized text (see normalizeText) to its Indonesian root, so
// "kebocoran" becomes "bocor" and "dibuang" becomes "buang". The result keeps the same shape: a
// space at both ends and between words.
func stemText(normalized string) string {
	words := strings.Fields(normalized)
	if len(words) == 0 {
		return " "
	}
	for i, w := range words {
		words[i] = stemWord(w)
	}
	return " " + strings.Join(words, " ") + " "
}

// keywordStems caches the stem of each keyword; the keyword list changes rarely but is matched
// against every description.
var keywordStems sync.Map // normalized keyword -> stemmed keyword (no surrounding spaces)

func stemKeyword(keyword string) string {
	if v, ok := keywordStems.Load(keyword); ok {
		return v.(string)
	}
	stem := strings.TrimSpace(stemText(" " + keyword + " "))
	keywordStems.Store(keyword, stem)
	return stem
}
