package web

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"

	"entry-permit/internal/company"
)

// CompaniesData is what the company list page receives.
type CompaniesData struct {
	Rows     []company.Company
	SyncedAt string // "" = never refreshed
	Unavail  string
	Flash    string
	Err      string
}

// adminCompanies shows the read-only mirror of hse-medpost's company list.
// WHY read-only: the list is edited in one place only, medpost (plan 4.3).
func (a *App) adminCompanies(w http.ResponseWriter, r *http.Request) {
	u := a.adminUser(w, r)
	if u == nil {
		return
	}
	data := CompaniesData{Flash: flash(r, "ok"), Err: flash(r, "err")}
	if a.Companies == nil {
		data.Unavail = company.ErrNoSource.Error()
	} else if rows, err := a.Companies.List(r.Context(), true); err != nil {
		log.Printf("admin companies: %v", err)
		data.Unavail = "The company list could not be read. Please try again."
	} else {
		data.Rows = rows
		if at, _, err := a.Companies.LastSync(r.Context()); err == nil {
			data.SyncedAt = at
		}
	}
	v := a.adminView(r, u, "Companies", AdminData{})
	v.CompaniesPage = data
	a.render(w, "admin_companies", http.StatusOK, v)
}

// adminCompaniesRefresh pulls the list from medpost now instead of waiting for
// the nightly sync. A bad or empty answer changes nothing (company.Sync).
func (a *App) adminCompaniesRefresh(w http.ResponseWriter, r *http.Request) {
	u := a.adminPost(w, r)
	if u == nil {
		return
	}
	keep := url.Values{}
	switch {
	case a.Companies == nil || a.CompanySource == nil:
		keep.Set("err", company.ErrNoSource.Error())
	default:
		res, err := a.Companies.Sync(r.Context(), a.CompanySource, u.Username)
		switch {
		case errors.Is(err, company.ErrNoSource), errors.Is(err, company.ErrBadData):
			keep.Set("err", err.Error())
		case err != nil:
			log.Printf("admin companies refresh: %v", err)
			keep.Set("err", "Could not get the company list from hse-medpost. The saved list was not changed.")
		case res.Any():
			keep.Set("ok", "Updated: "+plural(res.Added, "added")+", "+plural(res.Changed, "changed")+", "+plural(res.Removed, "removed")+".")
		default:
			keep.Set("ok", "Already up to date.")
		}
	}
	http.Redirect(w, r, "/staff/admin/companies?"+keep.Encode(), http.StatusSeeOther)
}

func plural(n int, word string) string { return fmt.Sprintf("%d %s", n, word) }
