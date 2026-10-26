package entrymcu

import "testing"

func TestTrimReviewerKeepsTitleSpelling(t *testing.T) {
	in := "  dr. Edric Sugito, MKKK  "
	got := trimReviewer(&in)
	if got == nil || *got != "dr. Edric Sugito, MKKK" {
		t.Fatalf("got %#v", got)
	}
}
