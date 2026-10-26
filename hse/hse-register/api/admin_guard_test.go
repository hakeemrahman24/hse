package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseGuardWords(t *testing.T) {
	words, ignored := parseGuardWords("Sangat, masing\nsangat  12345; ab \"memberikan\" a-b-c-d")
	if want := []string{"masing", "memberikan", "sangat"}; !reflect.DeepEqual(words, want) {
		t.Errorf("words = %v, want %v", words, want)
	}
	if ignored != 3 { // 12345, ab, a-b-c-d
		t.Errorf("ignored = %d, want 3", ignored)
	}
	if words, _ := parseGuardWords("  \n "); len(words) != 0 {
		t.Errorf("blank text gave %v", words)
	}
	if words, ignored := parseGuardWords(strings.Repeat("a", maxGuardWordLen+1)); len(words) != 0 || ignored != 1 {
		t.Errorf("an over-long word was accepted: %v", words)
	}
}
