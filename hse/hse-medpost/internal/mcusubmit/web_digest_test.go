package mcusubmit

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestDigestAdminPagesAreOnlyForTheAdmin(t *testing.T) {
	e := newEnv(t)
	e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, member := e.signIn("hr.budi", "a-long-passphrase-1")
	if rec := e.get("/mcu-submit/admin/digest", member); rec.Code != http.StatusNotFound {
		t.Errorf("a submitter on the digest page = %d, want 404", rec.Code)
	}
	for _, p := range []string{"/mcu-submit/admin/digest", "/mcu-submit/admin/reviewers/add", "/mcu-submit/admin/reviewers/toggle", "/mcu-submit/admin/reviewers/delete"} {
		if rec := e.post(p, url.Values{"hours": {"48"}, "id": {"1"}, "email": {"x@example.com"}}, member); rec.Code != http.StatusNotFound {
			t.Errorf("a submitter posting %s = %d, want 404", p, rec.Code)
		}
	}
	if n, _ := e.store.Reviewers(context.Background()); len(n) != 0 {
		t.Fatal("a submitter managed to add a reviewer")
	}
}

func TestAdminChoosesTheIntervalAndManagesReviewers(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, admin := e.signIn("admin", "pelican-orbit-77")

	page := e.get("/mcu-submit/admin/digest", admin)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `value="36"`) {
		t.Fatalf("digest page = %d, want the 24/36/48 choices", page.Code)
	}

	if rec := e.post("/mcu-submit/admin/digest", url.Values{"hours": {"36"}}, admin); rec.Code != http.StatusSeeOther {
		t.Fatalf("save 36 h = %d", rec.Code)
	}
	if cfg, _ := e.store.DigestSettings(ctx); cfg.IntervalHours != 36 {
		t.Fatalf("interval = %d, want 36", cfg.IntervalHours)
	}
	if rec := e.post("/mcu-submit/admin/digest", url.Values{"hours": {"12"}}, admin); rec.Code != http.StatusBadRequest {
		t.Fatalf("12 h = %d, want 400", rec.Code)
	}
	if cfg, _ := e.store.DigestSettings(ctx); cfg.IntervalHours != 36 {
		t.Fatal("a refused value changed the interval")
	}

	if rec := e.post("/mcu-submit/admin/reviewers/add", url.Values{"email": {"Doc@Example.com"}, "name": {"Dr Ayu"}}, admin); rec.Code != http.StatusSeeOther {
		t.Fatalf("add = %d", rec.Code)
	}
	list, _ := e.store.Reviewers(ctx)
	if len(list) != 1 || list[0].Email != "doc@example.com" {
		t.Fatalf("list = %+v", list)
	}
	if rec := e.post("/mcu-submit/admin/reviewers/add", url.Values{"email": {"doc@example.com"}}, admin); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate = %d, want 409", rec.Code)
	}
	if rec := e.post("/mcu-submit/admin/reviewers/add", url.Values{"email": {"nope"}}, admin); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad address = %d, want 400", rec.Code)
	}
	id := strconv.FormatInt(list[0].ID, 10)
	e.post("/mcu-submit/admin/reviewers/toggle", url.Values{"id": {id}, "on": {"0"}}, admin)
	if list, _ = e.store.Reviewers(ctx); list[0].Active {
		t.Fatal("toggle off did nothing")
	}
	e.post("/mcu-submit/admin/reviewers/delete", url.Values{"id": {id}}, admin)
	if list, _ = e.store.Reviewers(ctx); len(list) != 0 {
		t.Fatal("delete did nothing")
	}
	if rec := e.post("/mcu-submit/admin/reviewers/delete", url.Values{"id": {id}}, admin); rec.Code != http.StatusNotFound {
		t.Fatalf("delete twice = %d, want 404", rec.Code)
	}
}
