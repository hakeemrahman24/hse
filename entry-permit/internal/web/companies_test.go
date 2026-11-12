package web

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"entry-permit/internal/company"
)

// fakeCompanies is an in-memory company.Source.
type fakeCompanies struct {
	list []company.Company
	err  error
}

func (f *fakeCompanies) Companies(context.Context) ([]company.Company, error) { return f.list, f.err }

func TestCompaniesPageIsAdminOnlyAndRefreshes(t *testing.T) {
	e := newEnv(t)
	e.companies.list = []company.Company{
		{ID: 1, Name: "PT LNG", Entity: "Client"},
		{ID: 2, Name: "PT Karya", Entity: "Contractor", Parents: []company.Parent{{Name: "PT LNG", Depth: 1}}},
		{ID: 3, Name: "Dinas Kerja", Entity: "Government", Gov: true, Archived: true},
	}
	if r, _ := e.get(t, "/staff/admin/companies", nil); r.StatusCode != 303 {
		t.Fatalf("anonymous got %d, want a redirect to login", r.StatusCode)
	}
	gate, _ := e.gateAdminSession(t)
	if r, _ := e.get(t, "/staff/admin/companies", gate); r.StatusCode != 403 {
		t.Fatalf("gate admin got %d, want 403", r.StatusCode)
	}
	admin, csrf := e.adminSession(t)

	_, body := e.get(t, "/staff/admin/companies", admin)
	if !strings.Contains(body, "Not refreshed yet") || !strings.Contains(body, "Refresh now") {
		t.Fatal("empty mirror page is wrong")
	}
	// A forged post without the token is refused.
	if r := e.post(t, "/staff/admin/companies/refresh", admin, url.Values{}); r.StatusCode != 403 {
		t.Fatalf("no csrf: %d", r.StatusCode)
	}
	r := e.post(t, "/staff/admin/companies/refresh", admin, url.Values{"_csrf": {csrf}})
	if r.StatusCode != 303 || !strings.Contains(r.Header.Get("Location"), "3+added") {
		t.Fatalf("refresh -> %d %s", r.StatusCode, r.Header.Get("Location"))
	}
	_, body = e.get(t, "/staff/admin/companies", admin)
	for _, want := range []string{"PT Karya", "PT LNG", "Contractor", "Government, Archived", "Last refreshed 2026-11-06", "3 companies"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	// Read only: nothing on the page edits a company.
	if strings.Contains(body, `name="name"`) {
		t.Error("the company list must not be editable here")
	}
	// Pressing it again with nothing new says so.
	r = e.post(t, "/staff/admin/companies/refresh", admin, url.Values{"_csrf": {csrf}})
	if !strings.Contains(r.Header.Get("Location"), "Already+up+to+date") {
		t.Errorf("second refresh -> %s", r.Header.Get("Location"))
	}
}

func TestCompaniesRefreshReportsAProblemWithoutChangingTheList(t *testing.T) {
	e := newEnv(t)
	admin, csrf := e.adminSession(t)
	e.companies.err = errors.New("dial tcp 127.0.0.1:8113: connection refused")
	r := e.post(t, "/staff/admin/companies/refresh", admin, url.Values{"_csrf": {csrf}})
	loc := r.Header.Get("Location")
	if r.StatusCode != 303 || !strings.Contains(loc, "err=") || strings.Contains(loc, "127.0.0.1") {
		t.Fatalf("medpost down -> %d %s (internal detail must not reach the page)", r.StatusCode, loc)
	}
	e.companies.err, e.companies.list = nil, []company.Company{}
	r = e.post(t, "/staff/admin/companies/refresh", admin, url.Values{"_csrf": {csrf}})
	if !strings.Contains(r.Header.Get("Location"), "looks+wrong") {
		t.Fatalf("empty list -> %s", r.Header.Get("Location"))
	}
}
