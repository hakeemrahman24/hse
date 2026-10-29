package mcusubmit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// cloneDocs copies one document n times with new worker names, in SQL: the daily upload quota
// would stop that many real uploads, and the search does not care how a row got there.
func cloneDocs(t *testing.T, s *Store, id int64, n int, namePrefix string) {
	t.Helper()
	for i := 0; i < n; i++ {
		_, err := s.DB.Exec(`INSERT INTO McuSubmission (FK_SubmitterID, FK_BatchID, PatName, PatDateOfBirth, PatPosition, PatCompanyText,
			PatNationality, PatNik, ScanStatus, Status, SubmittedAt)
			SELECT FK_SubmitterID, FK_BatchID, ?, PatDateOfBirth, PatPosition, PatCompanyText, PatNationality, PatNik, ScanStatus, Status, SubmittedAt
			FROM McuSubmission WHERE SubmissionID = ?`, fmt.Sprintf("%s %03d", namePrefix, i), id)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestSearchDocsOwnViewIsLimitedToTheOwnerAndFiltersCombine(t *testing.T) {
	s, clock, co := newTestStore(t)
	ctx := context.Background()
	mine := activeSubmitter(t, s, co, "hrone@example.com")
	other := activeSubmitter(t, s, co, "hrtwo@example.com")
	a := sentDoc(t, s, mine, "Siti Aminah")
	sentDoc(t, s, other, "Siti Nurhaliza") // someone else's, same first name
	_ = s.MarkReviewed(ctx, a, testStaffID)
	clock.advance(48 * time.Hour)
	addClean(t, s, mine, "Budi Hartono") // a Draft, two days later

	all, err := s.SearchDocs(ctx, DocFilter{OwnerID: mine})
	if err != nil || all.Total != 2 {
		t.Fatalf("own view = %d rows, %v; want 2 (Siti Aminah and the draft), never the other submitter's", all.Total, err)
	}
	if all.Rows[0].PatName != "Budi Hartono" {
		t.Errorf("first row %q, want the newest (the draft)", all.Rows[0].PatName)
	}
	for _, c := range []struct {
		name string
		f    DocFilter
		want int
	}{
		{"status", DocFilter{OwnerID: mine, Status: SubReviewed}, 1},
		{"draft", DocFilter{OwnerID: mine, Status: SubDraft}, 1},
		{"worker words in any order", DocFilter{OwnerID: mine, Worker: "aminah SITI"}, 1},
		{"worker no match", DocFilter{OwnerID: mine, Worker: "nurhaliza"}, 0},
		{"company part", DocFilter{OwnerID: mine, Company: "example"}, 2},
		{"company no match", DocFilter{OwnerID: mine, Company: "other co"}, 0},
		{"from date after everything", DocFilter{OwnerID: mine, From: "2026-10-31"}, 0},
		{"to date before the draft", DocFilter{OwnerID: mine, To: "2026-10-28"}, 1},
		{"status and worker", DocFilter{OwnerID: mine, Status: SubReviewed, Worker: "budi"}, 0},
	} {
		got, err := s.SearchDocs(ctx, c.f)
		if err != nil || got.Total != c.want || len(got.Rows) != c.want {
			t.Errorf("%s: %d rows (%d total), %v; want %d", c.name, len(got.Rows), got.Total, err, c.want)
		}
	}
}

func TestSearchDocsStaffViewHidesUnsentWorkAndCanFilterBySubmitter(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	one := activeSubmitter(t, s, co, "hrone@example.com")
	two := activeSubmitter(t, s, co, "hrtwo@example.com")
	_, _ = s.DB.Exec(`UPDATE Submitter SET Name = 'Andi Pratama' WHERE SubmitterID = ?`, two)
	sentDoc(t, s, one, "Worker One")
	sentDoc(t, s, two, "Worker Two")
	addClean(t, s, one, "Draft Worker")

	all, err := s.SearchDocs(ctx, DocFilter{})
	if err != nil || all.Total != 2 {
		t.Fatalf("staff view = %d, %v; want the 2 sent documents and no draft", all.Total, err)
	}
	if got, _ := s.SearchDocs(ctx, DocFilter{Submitter: "pratama"}); got.Total != 1 || got.Rows[0].PatName != "Worker Two" {
		t.Errorf("submitter filter = %+v", got.Rows)
	}
	for _, st := range []string{SubDraft, SubReview} {
		if _, err := s.SearchDocs(ctx, DocFilter{Status: st}); !errors.Is(err, ErrBadFilter) {
			t.Errorf("staff asking for %s = %v, want ErrBadFilter", st, err)
		}
	}
}

func TestSearchDocsPagesAndClampsThePageNumber(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrone@example.com")
	id := sentDoc(t, s, sub, "Seed Worker")
	cloneDocs(t, s, id, 29, "Clone") // 30 in total: two pages of 25 and 5

	p1, err := s.SearchDocs(ctx, DocFilter{OwnerID: sub, Page: 1})
	if err != nil || p1.Total != 30 || p1.Pages != 2 || len(p1.Rows) != docPageSize || p1.Page != 1 {
		t.Fatalf("page 1 = %d rows of %d, %d pages, %v", len(p1.Rows), p1.Total, p1.Pages, err)
	}
	p2, _ := s.SearchDocs(ctx, DocFilter{OwnerID: sub, Page: 2})
	if len(p2.Rows) != 5 || p2.Page != 2 {
		t.Errorf("page 2 = %d rows", len(p2.Rows))
	}
	seen := map[int64]bool{}
	for _, r := range append(p1.Rows, p2.Rows...) {
		if seen[r.ID] {
			t.Fatalf("row %d is on both pages", r.ID)
		}
		seen[r.ID] = true
	}
	for _, page := range []int{0, -3} {
		if got, _ := s.SearchDocs(ctx, DocFilter{OwnerID: sub, Page: page}); got.Page != 1 {
			t.Errorf("page %d became %d, want 1", page, got.Page)
		}
	}
	if got, _ := s.SearchDocs(ctx, DocFilter{OwnerID: sub, Page: 99}); got.Page != 2 || len(got.Rows) != 5 {
		t.Errorf("page 99 = page %d with %d rows, want the last page", got.Page, len(got.Rows))
	}
	if none, _ := s.SearchDocs(ctx, DocFilter{OwnerID: sub, Worker: "nobody"}); none.Total != 0 || none.Pages != 1 || none.Page != 1 {
		t.Errorf("empty result = %+v", none)
	}
}

func TestSearchDocsRefusesBadFiltersAndTreatsWildcardsAsPlainText(t *testing.T) {
	s, _, co := newTestStore(t)
	ctx := context.Background()
	sub := activeSubmitter(t, s, co, "hrone@example.com")
	sentDoc(t, s, sub, "Worker One")
	for _, f := range []DocFilter{
		{OwnerID: sub, Status: "Bogus"}, {OwnerID: sub, From: "yesterday"}, {OwnerID: sub, To: "2026-13-45"}, {OwnerID: sub, From: "2026-10-10", To: "2026-10-01"},
	} {
		if _, err := s.SearchDocs(ctx, f); !errors.Is(err, ErrBadFilter) {
			t.Errorf("%+v = %v, want ErrBadFilter", f, err)
		}
	}
	// % and _ mean something to LIKE, so they are dropped from the typed words: a lone % leaves
	// no words (no filter), and "P_ Example" is the two words "P" and "Example".
	if got, err := s.SearchDocs(ctx, DocFilter{OwnerID: sub, Worker: "%"}); err != nil || got.Total != 1 {
		t.Errorf("a lone %% = %d rows, %v; want 1 (no filter)", got.Total, err)
	}
	if got, _ := s.SearchDocs(ctx, DocFilter{OwnerID: sub, Company: "zzz%"}); got.Total != 0 {
		t.Errorf("zzz%% matched %d rows; the word zzz is not in any company", got.Total)
	}
}

func TestMyDocumentsPageFiltersPagesAndKeepsTheFilterInTheLinks(t *testing.T) {
	e := newEnv(t)
	id := e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, cookie := e.signIn("hr.budi", "a-long-passphrase-1")
	seed := sentDoc(t, e.store, id, "Seed Worker")
	cloneDocs(t, e.store, seed, 29, "Clone")

	page1 := e.get("/mcu-submit/documents?worker=clone", cookie).Body.String()
	if !strings.Contains(page1, "Clone 028") || strings.Contains(page1, "Seed Worker") {
		t.Fatalf("worker filter did not narrow the list:\n%s", page1)
	}
	if !strings.Contains(page1, "29 documents") {
		t.Errorf("no total on the page")
	}
	if !strings.Contains(page1, `href="/mcu-submit/documents?page=2&amp;worker=clone"`) {
		t.Errorf("Next link does not carry the filter:\n%s", page1)
	}
	page2 := e.get("/mcu-submit/documents?worker=clone&page=2", cookie).Body.String()
	if strings.Contains(page2, "Clone 028") || !strings.Contains(page2, "Clone 000") {
		t.Errorf("page 2 should hold the oldest rows only")
	}
	if rec := e.get("/mcu-submit/documents?status=Bogus", cookie); rec.Code != http.StatusBadRequest {
		t.Errorf("bad status = %d, want 400", rec.Code)
	}
	if rec := e.get("/mcu-submit/documents?from=2026-13-40", cookie); rec.Code != http.StatusBadRequest {
		t.Errorf("bad date = %d, want 400", rec.Code)
	}
	// Someone typing markup into a filter gets it back as text, not as a tag.
	hostile := e.get("/mcu-submit/documents?worker="+url.QueryEscape(`"><script>x</script>`), cookie).Body.String()
	if strings.Contains(hostile, "<script>x") {
		t.Error("a filter value was written into the page unescaped")
	}
}

func TestStaffListFiltersAndReportsThePagingInHeaders(t *testing.T) {
	e := newStaffEnv(t)
	seed := e.doc(t, "Seed Worker", true)
	cloneDocs(t, e.store, seed, 29, "Clone")

	rec := e.do("GET", "/api/mcu-submissions?worker=clone&page=2", "", false)
	if rec.Code != http.StatusOK || rec.Header().Get("X-Total-Count") != "29" || rec.Header().Get("X-Page") != "2" || rec.Header().Get("X-Pages") != "2" {
		t.Fatalf("= %d, headers %v", rec.Code, rec.Header())
	}
	var rows []staffRow
	_ = jsonUnmarshal(rec.Body.Bytes(), &rows)
	if len(rows) != 4 {
		t.Errorf("page 2 has %d rows, want 4 (29 = 25 + 4)", len(rows))
	}
	for _, bad := range []string{"status=Draft", "from=nope", "status=Bogus"} {
		if rec := e.do("GET", "/api/mcu-submissions?"+bad, "", false); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", bad, rec.Code)
		}
	}
	if _, err := strconv.Atoi(e.do("GET", "/api/mcu-submissions", "", false).Header().Get("X-Pages")); err != nil {
		t.Errorf("X-Pages missing on an unfiltered list: %v", err)
	}
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
