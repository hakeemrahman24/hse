package main

import (
	"html/template"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func renderToString(t *testing.T, page string, data PageData) string {
	t.Helper()
	rec := httptest.NewRecorder()
	if _, ok := homePages[page]; ok {
		renderHome(rec, page, data)
	} else {
		render(rec, page, data)
	}
	return rec.Body.String()
}

func loadTestTemplates() {
	loadPages("../web/templates")
	loadHomePages("../web/templates")
}

func TestHomePageShowsCardsByRole(t *testing.T) {
	loadTestTemplates()

	user := renderToString(t, "start", PageData{Title: "Home", Session: &Session{Username: "budi", Role: roleUser}, Modules: modulesFor(roleUser)})
	for _, want := range []string{"Medical Activities", "Work Permit Register", "HSE Observation", `href="/start"`, "Log out"} {
		if !strings.Contains(user, want) {
			t.Errorf("user home page is missing %q", want)
		}
	}
	if strings.Contains(user, "Settings") || strings.Contains(user, "/admin/settings") {
		t.Error("user home page must not show Settings")
	}

	admin := renderToString(t, "start", PageData{Title: "Home", Session: &Session{Username: "arif", Role: roleAdmin}, Modules: modulesFor(roleAdmin)})
	if !strings.Contains(admin, `href="/admin/settings"`) || !strings.Contains(admin, "module-card--settings") {
		t.Error("admin home page must show the Settings card")
	}
}

func TestActivitiesPageHasNoWriteControls(t *testing.T) {
	loadTestTemplates()
	out := renderToString(t, "activities", PageData{Title: "Medical Activities", Heading: "Medical Activities", Subheading: "View only. Health programs.", Session: &Session{Username: "budi", Role: roleUser}, ActState: activitiesOK, ActTab: "activities", ActSide: true})
	if strings.Contains(out, "<form method=\"post\" action=\"/activities") || strings.Contains(strings.ToLower(out), "type=\"submit\" value=\"save") {
		t.Error("view-only page must not contain write controls")
	}
	if !strings.Contains(out, "View only") {
		t.Error("page should say it is view only")
	}
}

func TestCrudShellHidesUsersForRegularUser(t *testing.T) {
	loadTestTemplates()
	user := renderToString(t, "observations_list", PageData{Title: "Observations", Heading: "Observations", Active: "observations", Session: &Session{Username: "budi", Role: roleUser}})
	if strings.Contains(user, "/users") || strings.Contains(user, "settings-badge") {
		t.Error("regular user must not see the Users links or the Admin badge in the shell")
	}
	if !strings.Contains(user, `href="/start"`) {
		t.Error("shell logo must link to the home page")
	}
	admin := renderToString(t, "observations_list", PageData{Title: "Observations", Heading: "Observations", Active: "observations", Session: &Session{Username: "arif", Role: roleAdmin}})
	if !strings.Contains(admin, "/users") || !strings.Contains(admin, "settings-badge") {
		t.Error("admin must see the Users links and the Admin badge")
	}
}

func testNav() *obsNav {
	return &obsNav{
		Total: 7,
		Sources: []sidebarSource{
			{Name: "SOC", Value: "SOC", Count: 3, Active: true},
			{Name: "Internal Audit", Value: "Internal Audit", Count: 4, Open: false, Items: []sidebarType{{Name: "Site Audit", Count: 3}, {Name: "HO Audit", Count: 1}}},
		},
	}
}

// staffFormData is the page data of the add/edit form with the lookup lists filled in.
func staffFormData(o *Observation, form obsForm, blocks []fileBlock) PageData {
	snap := testSnap()
	return PageData{
		Title: "Edit", Heading: "Edit", Active: "obs-list", Session: &Session{Username: "arif", Role: roleAdmin},
		Observation: o, Form: form, Blocks: blocks, ObsNav: testNav(),
		Topics: snap.Topics, HazardGroups: snap.HazardGroups(), Areas: snap.Areas, CompanyOptions: []string{"STARWAY ENGINEERING INDONESIA"},
		CategoryOptions: observationCategories, StatusOptions: observationStatuses,
		StaffSources: staffSources, SourceDetailChoices: newSnapshot(nil, nil, nil, nil).SourceDetailChoices("", ""),
	}
}

func TestObservationSidebarShowsDashboardObservationsAndSources(t *testing.T) {
	loadTestTemplates()
	out := renderToString(t, "observations_form", staffFormData(&Observation{}, obsForm{}, nil))
	for _, want := range []string{
		`href="/observations"`, "Dashboard", `href="/observations/list"`, "7 overall", "Sources",
		"SOC", "Internal Audit", `href="/observations/list?source=SOC"`, `href="/observations/list?source=Internal&#43;Audit"`,
		`href="/observations/list?source=Internal&#43;Audit&amp;type=Site&#43;Audit"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sidebar is missing %q", want)
		}
	}
	if strings.Contains(out, "Hazard categories</div>") || strings.Contains(out, "Not classified") || strings.Contains(out, "Control topics") {
		t.Error("the sidebar groups by source now, not by hazard class or control topic")
	}
	// A source with types has a triangle button and a block of types that starts closed; a source
	// without types has no button.
	if strings.Contains(out, "<details") {
		t.Error("the sidebar uses the triangle buttons, not <details>")
	}
	for _, want := range []string{`class="obs-toggle"`, `aria-expanded="false"`, `class="obs-subs" hidden`, `class="obs-toggle-gap"`} {
		if !strings.Contains(out, want) {
			t.Errorf("sidebar is missing %q", want)
		}
	}
	if strings.Count(out, `class="obs-toggle"`) != 1 {
		t.Errorf("only the source with types gets a toggle")
	}
	if strings.Contains(out, "All Work Permit Register entries") {
		t.Error("the observation module has its own sidebar, not the permit one")
	}
}

func TestNavSourcesListSocFirstHideEmptyArchivedAndKeepNotRecorded(t *testing.T) {
	snap := newSnapshot(nil, nil, nil, nil)
	snap.Sources = []refSource{
		{Name: "Internal Audit", Details: []refDetail{{Name: "Site Audit"}, {Name: "KL Audit", Archived: true}}},
		{Name: "Old Source", Archived: true},
		{Name: "External Audit", Archived: true},
	}
	bySource := map[string]int{"SOC": 5, "Internal Audit": 3, "External Audit": 2, "": 4}
	byType := map[[2]string]int{{"Internal Audit", "Site Audit"}: 2, {"Internal Audit", "KL Audit"}: 0}
	got := navSources(snap, bySource, byType, "Internal Audit", "Site Audit")
	var names []string
	for _, s := range got {
		names = append(names, s.Name)
	}
	if want := "SOC,Internal Audit,External Audit,Not recorded"; strings.Join(names, ",") != want {
		t.Fatalf("sources = %v, want %s", names, want)
	}
	ia := got[1]
	if len(ia.Items) != 1 || !ia.Items[0].Active || !ia.Open || ia.Active {
		t.Errorf("Internal Audit = %+v: the archived empty type is hidden, the selected type is active and opens its source", ia)
	}
	if got[3].Value != sourceNone || got[3].Count != 4 {
		t.Errorf("not recorded = %+v", got[3])
	}
}

func sampleBlocks() []dashBlock {
	return []dashBlock{
		{Title: "HSE recommendations status", Total: 10, Open: 4, InProgress: 3, Closed: 3, OpenPct: 40, InProgressPct: 30, ClosedPct: 30,
			TotalURL: "/observations/list", OpenURL: "/observations/list?status=open", InProgressURL: "/observations/list?status=in_progress", ClosedURL: "/observations/list?status=closed"},
		{Title: "SOC status", Total: 6, Open: 3, InProgress: 2, Closed: 1, OpenPct: 50, InProgressPct: 33, ClosedPct: 17, Breakdown: true, ShowPositive: true, Positive: 2,
			Hazards: []countRow{{"Fall from Height", 4, 100}}, Areas: []countRow{{"Camp", 3, 100}}, Subcontractors: []countRow{{"ACME", 2, 100}},
			TotalURL: "/observations/list?source=SOC", OpenURL: "/observations/list?source=SOC&status=open", InProgressURL: "/x", ClosedURL: "/y"},
		{Title: "PMR status", Total: 0, Breakdown: true, TotalURL: "/p", OpenURL: "/p", InProgressURL: "/p", ClosedURL: "/p"},
	}
}

func TestDashboardShowsTheThreeBlocks(t *testing.T) {
	loadTestTemplates()
	out := renderToString(t, "obs_dashboard", PageData{
		Title: "Dashboard", Heading: "Dashboard", Active: "obs-dashboard", Session: &Session{Username: "arif", Role: roleAdmin}, ObsNav: testNav(),
		Dash:         &dashboard{Blocks: sampleBlocks(), AwaitingVerification: 3, AwaitingURL: "/observations/list?verify=1"},
		FilterAction: "/observations", ClearURL: "/observations",
	})
	for _, want := range []string{
		"HSE recommendations status", "SOC status", "PMR status",
		"Total findings", "Total open", "Total in progress", "Total closed",
		"Positive findings", "Hazard categories", "Area", "Subcontractor", "Fall from Height", "ACME",
		`href="/observations/list?source=SOC&amp;status=open"`, "width:40%", "3 SOC posts waiting for verification",
		"No findings in this period.", "dash-two",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dashboard is missing %q", want)
		}
	}
	// Only the SOC block has "Positive findings"; only SOC and PMR have the breakdowns.
	if n := strings.Count(out, "Positive findings"); n != 1 {
		t.Errorf("Positive findings should appear once (SOC block), got %d", n)
	}
	if n := strings.Count(out, "<h3>Hazard categories</h3>"); n != 1 {
		t.Errorf("only the SOC block (PMR is empty) shows breakdowns, got %d", n)
	}
}

func TestDateFilterFormAndSidebarKeepTheRange(t *testing.T) {
	loadTestTemplates()
	nav := testNav()
	nav.Filtered, nav.Q1, nav.Qs = true, template.URL("?from=2026-10-01&to=2026-10-15"), template.URL("&from=2026-10-01&to=2026-10-15")
	data := PageData{
		Title: "Dashboard", Heading: "Dashboard", Active: "obs-dashboard", Session: &Session{Username: "arif", Role: roleAdmin}, ObsNav: nav,
		Dash:  &dashboard{Blocks: sampleBlocks()},
		Range: dateRange{From: "2026-10-01", To: "2026-10-15"}, FilterAction: "/observations", ClearURL: "/observations",
	}
	out := renderToString(t, "obs_dashboard", data)
	for _, want := range []string{
		`name="from" value="2026-10-01"`, `name="to" value="2026-10-15"`, `action="/observations"`, ">Clear<",
		"in range", `href="/observations/list?from=2026-10-01&amp;to=2026-10-15"`, `href="/observations?from=2026-10-01&amp;to=2026-10-15"`,
		`href="/observations/list?source=SOC&amp;from=2026-10-01&amp;to=2026-10-15"`,
		`href="/observations/list?source=Internal&#43;Audit&amp;type=Site&#43;Audit&amp;from=2026-10-01&amp;to=2026-10-15"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("filtered dashboard is missing %q", want)
		}
	}
	data.Range, data.ObsNav = dateRange{}, testNav()
	plain := renderToString(t, "obs_dashboard", data)
	if strings.Contains(plain, ">Clear<") || strings.Contains(plain, "in range") {
		t.Error("without a range there is no Clear link and the count says overall")
	}
}

func TestListPageHasTheDateFilterWithHiddenFilters(t *testing.T) {
	loadTestTemplates()
	out := renderToString(t, "observations_list", PageData{
		Title: "Safety", Heading: "Safety", Active: "obs-list", Session: &Session{Username: "arif", Role: roleAdmin}, ObsNav: testNav(),
		Range: dateRange{From: "2026-10-01"}, FilterAction: "/observations/list", ClearURL: "/observations/list?class=Safety",
		FilterHidden: map[string]string{"class": "Safety", "status": "open"},
	})
	for _, want := range []string{`action="/observations/list"`, `name="class" value="Safety"`, `name="status" value="open"`, `name="from" value="2026-10-01"`, `href="/observations/list?class=Safety"`} {
		if !strings.Contains(out, want) {
			t.Errorf("list date filter is missing %q", want)
		}
	}
}

func detailPage(o *Observation, v viewer) PageData {
	d := &obsDetail{O: o, Perms: permsFor(o, v), Topic: "Working at Height", Hazard: "Fall from Height", HazardClass: "Safety"}
	d.Blocks = []fileBlock{
		{Section: fileSection{Kind: photoBefore, Title: "Evidence / Bukti (before)", Photos: []FileView{{ID: 11, Kind: "before", Type: "photo"}}}, ObsID: o.ID},
		{Section: fileSection{Kind: photoAfter, Title: "After / Sesudah (action done)", PDFs: []FileView{{ID: 12, Kind: "after", Type: "pdf", Display: "done.pdf", Size: 2048}}},
			ObsID: o.ID, CanUpload: d.Perms.CanAddAfter, CanDelete: d.Perms.CanAddAfter},
	}
	return PageData{Title: "Observation", Heading: "Observation", Active: "obs-list", Session: &Session{Username: v.Username, Role: v.Role}, ObsNav: testNav(), Detail: d}
}

func TestDetailPageButtonsFollowThePermissions(t *testing.T) {
	loadTestTemplates()
	mine := &Observation{ID: 5, Source: "Internal Audit", CreatedBy: "budi", Status: statusOpen, Category: catUnsafeAction, ObserverName: "Budi", Description: "x", Location: "Camp"}

	creator := renderToString(t, "obs_detail", detailPage(mine, budi))
	for _, want := range []string{`href="/observations/5/edit"`, `value="in_progress"`, `value="closed"`, `/observations/5/files?kind=after`, `/observations/5/files/12/delete`, "Working at Height", "done.pdf"} {
		if !strings.Contains(creator, want) {
			t.Errorf("creator's page is missing %q", want)
		}
	}
	if strings.Contains(creator, `/observations/5/delete`) {
		t.Error("a regular user must not see Delete")
	}
	if strings.Contains(creator, `/observations/5/files/11/delete`) || strings.Contains(creator, `kind=before`) {
		t.Error("evidence is managed on the edit page, not on the detail page")
	}

	other := renderToString(t, "obs_detail", detailPage(mine, siti))
	if strings.Contains(other, `/observations/5/edit`) {
		t.Error("another user must not get an Edit button")
	}
	if !strings.Contains(other, `kind=after`) || !strings.Contains(other, `value="closed"`) {
		t.Error("another user can still add After files and close")
	}
	if !strings.Contains(other, "Only the person who entered this post can change its details") {
		t.Error("the page should say why Edit is missing")
	}

	admin := renderToString(t, "obs_detail", detailPage(mine, arif))
	if !strings.Contains(admin, `/observations/5/delete`) {
		t.Error("an administrator sees Delete")
	}
}

func TestDetailPageOfAClosedPostIsReadOnly(t *testing.T) {
	loadTestTemplates()
	closed := &Observation{ID: 6, Source: "Internal Audit", CreatedBy: "budi", Status: statusClosed, Category: catUnsafeAction, ObserverName: "Budi", Description: "x", Location: "Camp"}
	out := renderToString(t, "obs_detail", detailPage(closed, budi))
	if !strings.Contains(out, "closed and can no longer be edited") {
		t.Error("a closed post must say it is locked")
	}
	for _, banned := range []string{`/observations/6/edit`, `/observations/6/status`, `/observations/6/files?kind=`, `/files/12/delete`, `/observations/6/verify`} {
		if strings.Contains(out, banned) {
			t.Errorf("a closed post must not offer %q", banned)
		}
	}
}

func TestDetailPageOfASOCPostOffersVerifyNotEdit(t *testing.T) {
	loadTestTemplates()
	post := &Observation{ID: 8, Source: sourceSOC, EmployeeID: "E001", ObserverName: "Budi Santoso", Status: statusOpen, Verification: verifNotYet, Category: catUnsafeCondition, Location: "Camp", Description: "x"}
	out := renderToString(t, "obs_detail", detailPage(post, siti))
	if !strings.Contains(out, `/observations/8/verify`) || strings.Contains(out, `/observations/8/edit`) {
		t.Error("staff verify a SOC post but do not edit it")
	}
	if !strings.Contains(out, "They can edit it until it is verified") {
		t.Error("the page should explain that the employee edits it")
	}
	post.Verification = verifVerified
	if strings.Contains(renderToString(t, "obs_detail", detailPage(post, siti)), `/observations/8/verify`) {
		t.Error("a verified post has no Verify button")
	}
}

func TestEditPageShowsOnlyBeforeEvidence(t *testing.T) {
	loadTestTemplates()
	blocks := []fileBlock{{
		Section: fileSection{Kind: photoBefore, Title: "Evidence / Bukti (before)", Photos: []FileView{{ID: 1, Kind: "before", Type: "photo"}},
			PDFs: []FileView{{ID: 7, Kind: "before", Type: "pdf", Display: "a.pdf", Size: 10}, {ID: 8, Kind: "before", Type: "pdf", Display: "b.pdf", Size: 10}}},
		ObsID: 7, CanUpload: true, CanDelete: true,
	}}
	out := renderToString(t, "observations_form", staffFormData(&Observation{ID: 7, Source: "Internal Audit"}, obsForm{Source: "Internal Audit"}, blocks))
	for _, want := range []string{`/observations/7/files?kind=before`, `/evidence/1`, `(1 of 4 photos, 2 of 2 PDFs)`, `/observations/7/files/1/delete`, `a.pdf`} {
		if !strings.Contains(out, want) {
			t.Errorf("edit page is missing %q", want)
		}
	}
	if strings.Contains(out, `kind=after`) {
		t.Error("After files are not managed on the edit page")
	}
	if n := strings.Count(out, `name="photos"`); n != 1 {
		t.Errorf("want one photo input, got %d", n)
	}
	if strings.Contains(out, `name="documents"`) {
		t.Error("with 2 PDFs already there, the PDF input must be hidden")
	}
	fresh := renderToString(t, "observations_form", staffFormData(&Observation{}, obsForm{}, nil))
	if strings.Contains(fresh, "/files?kind=") {
		t.Error("a new, unsaved observation must not offer file upload yet")
	}
}

func TestStaffFormFields(t *testing.T) {
	loadTestTemplates()
	out := renderToString(t, "observations_form", staffFormData(&Observation{}, obsForm{}, nil))
	for _, want := range []string{
		`id="source"`, `Internal Audit`, `Management Walkthrough`, `Project Monitoring Action Report`,
		`data-source="External Audit"`, `3rd Party Company`, `Unsafe Action`, `Positive Finding`,
		`Working at Height`, `Fall from Height`, `<optgroup label="Safety">`, `list="area-list"`, `<option value="Camp">`,
		`Company responsible in that area`, `STARWAY ENGINEERING INDONESIA`, `/observations/suggest`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("staff form is missing %q", want)
		}
	}
	for _, banned := range []string{`id="verification"`, `id="status"`, `value="SOC"`} {
		if strings.Contains(out, banned) {
			t.Errorf("the form must not contain %q (status and verification have their own buttons)", banned)
		}
	}
}

func TestStaffFormShowsErrorMessages(t *testing.T) {
	loadTestTemplates()
	data := staffFormData(&Observation{ID: 4, Source: "Internal Audit"}, obsForm{Source: "Internal Audit"}, nil)
	data.FormErrors = map[string]string{"area": "Choose the area.", "description": "Describe the finding."}
	out := renderToString(t, "observations_form", data)
	for _, want := range []string{"Please correct the fields marked below.", "Choose the area.", "Describe the finding."} {
		if !strings.Contains(out, want) {
			t.Errorf("error page is missing %q", want)
		}
	}
}

func TestObservationListShowsColumnsFiltersAndAdminDelete(t *testing.T) {
	loadTestTemplates()
	page := func(role string) string {
		return renderToString(t, "observations_list", PageData{
			Title: "Safety", Heading: "Safety", Active: "obs-list", Session: &Session{Username: "x", Role: role}, ObsNav: testNav(),
			Chips: []chip{{"All", "/observations/list", true}, {"Open", "/observations/list?status=open", false}},
			Observations: []*Observation{
				{ID: 1, Source: "Internal Audit", SourceDetail: "Site Audit", ObserverName: "Siti", Category: catUnsafeAction, Location: "Camp", ResponsibleCompany: "ACME", Status: statusInProgress},
				{ID: 2, Source: sourceSOC, ObserverName: "Budi", Category: catPositive, Location: "Jetty", Status: statusClosed, Verification: verifVerified},
				{ID: 3, ObserverName: "Old row", Category: catUnsafeCondition, Location: "X", Status: statusOpen},
			},
		})
	}
	admin := page(roleAdmin)
	for _, want := range []string{"Internal Audit · Site Audit", "Unsafe Action", "In Progress", "Positive Finding", "Verified", "Responsible company",
		`href="/observations/1"`, `href="/observations/list?status=open"`, "obs-chip active", `/observations/1/delete`} {
		if !strings.Contains(admin, want) {
			t.Errorf("list is missing %q", want)
		}
	}
	if strings.Contains(page(roleUser), `/delete`) {
		t.Error("regular users do not see Delete in the list")
	}
}

func dashPage(d *dashboard) PageData {
	return PageData{
		Title: "Dashboard", Heading: "Dashboard", Active: "obs-dashboard", Session: &Session{Username: "arif", Role: roleAdmin}, ObsNav: testNav(),
		Dash: d, FilterAction: "/observations", ClearURL: "/observations",
	}
}

func TestDashboardTabsSwitchBetweenOverviewAndCompaniesAndPeople(t *testing.T) {
	loadTestTemplates()
	overview := &dashboard{Blocks: sampleBlocks(), OverviewURL: "/observations", CompaniesURL: "/observations?tab=companies"}
	out := renderToString(t, "obs_dashboard", dashPage(overview))
	for _, want := range []string{`class="dash-tabs"`, `href="/observations?tab=companies"`, "Companies &amp; people", "HSE recommendations status"} {
		if !strings.Contains(out, want) {
			t.Errorf("overview is missing %q", want)
		}
	}
	if strings.Contains(out, "Top 10 companies") || strings.Contains(out, "Top 10 individuals") {
		t.Error("the overview must not show the company and people charts")
	}
	if !strings.Contains(out, `class="dash-tab active" aria-current="page" href="/observations"`) {
		t.Error("Overview must be the active tab")
	}

	tab := &dashboard{Tab: tabCompanies, Sort: sortRate, SortLinks: sortLinks(sortRate, dateRange{}), OverviewURL: "/observations", CompaniesURL: "/observations?tab=companies",
		TopCompanies: []rankRow{{Pos: 1, Name: "ACME", Rate: 63, RateClass: "fair", Total: 8, Open: 2, InProgress: 1, Closed: 5, OpenPct: 25, InProgressPct: 12, ClosedPct: 63, Bar: 100, URL: "/observations/list?company=ACME"}},
		TopPeople:    []rankRow{{Pos: 1, Name: "Budi Santoso", Sub: "PT Karya", Total: 4, Closed: 1, ClosedPct: 25, OpenPct: 75, Bar: 100}}}
	pd := dashPage(tab)
	pd.FilterHidden = map[string]string{"tab": tabCompanies}
	out = renderToString(t, "obs_dashboard", pd)
	for _, want := range []string{
		"Top 10 companies", "Top 10 individuals", "ACME", `href="/observations/list?company=ACME"`, "8</b> finding", "closed 5 (63%)", "in progress 1 (12%)", "open 2 (25%)",
		"Budi Santoso", "PT Karya", "Closed 63%", "rate-fair", `class="rank-sort-opt active" aria-current="true" href="/observations?sort=rate&amp;tab=companies"`, "Lowest closing rate", "Highest closing rate", "Most still open", "at least 5 findings", "4</b> finding", `style="width:63%"`, `name="tab" value="companies"`,
		`class="dash-tab active" aria-current="page" href="/observations?tab=companies"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("companies tab is missing %q", want)
		}
	}
	if strings.Contains(out, "HSE recommendations status") || strings.Contains(out, "SOC status") {
		t.Error("the companies tab must not repeat the overview blocks")
	}
	tab.TopCompanies, tab.TopPeople = nil, nil
	out = renderToString(t, "obs_dashboard", dashPage(tab))
	if strings.Count(out, "No findings in this period.") != 2 {
		t.Error("empty charts must say so")
	}
}

func TestNewFormHasEvidenceFieldsAndEditFormDoesNot(t *testing.T) {
	loadTestTemplates()
	out := renderToString(t, "observations_form", staffFormData(&Observation{}, obsForm{}, nil))
	for _, want := range []string{
		`class="add-activity"`, `enctype="multipart/form-data"`, `class="section-label"`, "settings-grid",
		`name="photos"`, `name="documents"`, `accept="application/pdf,.pdf"`, `type="datetime-local"`, "Save observation",
		`name="after_photos"`, `name="after_documents"`, `name="action_taken"`, `readonly`, `/static/combo.js`, `data-combo`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("new form is missing %q", want)
		}
	}
	edit := renderToString(t, "observations_form", staffFormData(&Observation{ID: 5}, obsForm{}, nil))
	if strings.Contains(edit, `name="documents"`) && !strings.Contains(edit, "obs-files") {
		t.Error("the edit page manages files in its own evidence block, not in the form")
	}
}

func TestTitleLevelsGetOneClassEach(t *testing.T) {
	loadTestTemplates()
	data := PageData{
		Title: "t", Heading: "Observations · Internal Audit · HO Audit", Active: "obs-list", Session: &Session{Username: "arif", Role: roleAdmin}, ObsNav: testNav(),
	}
	out := renderToString(t, "observations_list", data)
	for _, want := range []string{`<span class="h1-0">Observations</span>`, `<span class="h1-1">Internal Audit</span>`, `<span class="h1-2">HO Audit</span>`} {
		if !strings.Contains(out, want) {
			t.Errorf("the title is missing %s: %s", want, out)
		}
	}
	data.Heading = "Observations"
	if plain := renderToString(t, "observations_list", data); !strings.Contains(plain, `<span class="h1-0">Observations</span>`) || strings.Contains(plain, "h1-1") {
		t.Error("a one-level title has only the first level")
	}
}

func TestSettingsPagesUseTheirOwnColoursButTheSameLayout(t *testing.T) {
	loadTestTemplates()
	admin := &Session{UserID: 1, Username: "arif", Role: roleAdmin}
	for _, page := range []string{"settings", "settings_areas", "settings_guard", "account"} {
		out := renderToString(t, page, PageData{Title: "x", Session: admin})
		if !strings.Contains(out, `class="settings-theme area-settings"`) {
			t.Errorf("%s should carry the settings colours", page)
		}
	}
	users := renderToString(t, "users_list", PageData{Title: "Users", Heading: "Users", Active: "users", Session: admin, Users: []*AdminUser{}})
	if !strings.Contains(users, `class="settings-theme area-settings"`) || !strings.Contains(users, "app-sidebar") {
		t.Error("the Users pages use the settings colours and keep the sidebar layout")
	}
	for _, page := range []string{"start", "activities"} {
		if zoneFor(page) != "" {
			t.Errorf("%s keeps the normal colours", page)
		}
	}
	if zoneFor("observations_list") != "" || zoneFor("obs_dashboard") != "" {
		t.Error("the observation pages keep the normal colours")
	}
}

func TestWorkPermitPlaceholderAndSlimNavigation(t *testing.T) {
	loadTestTemplates()
	admin := &Session{UserID: 1, Username: "arif", Role: roleAdmin}
	wp := renderToString(t, "workpermit", PageData{Title: "Work Permit Register", Session: admin})
	if !strings.Contains(wp, "still under development") || strings.Contains(wp, "<table") {
		t.Errorf("the Work Permit Register page is a blank placeholder: %s", wp)
	}
	users := renderToString(t, "users_list", PageData{Title: "Users", Heading: "Users", Active: "users", Session: admin, Users: []*AdminUser{}})
	obs := renderToString(t, "observations_form", staffFormData(&Observation{}, obsForm{}, nil))
	for name, page := range map[string]string{"users": users, "observations": obs} {
		for _, gone := range []string{`class="nav-links"`, ">Home<", "Work Permit Register"} {
			if strings.Contains(page, gone) {
				t.Errorf("%s page should not contain %q: no top tabs, no Home in the sidebar, no permit links", name, gone)
			}
		}
		if !strings.Contains(page, `class="nav-logo"`) {
			t.Errorf("%s page keeps the top-left logo link to home", name)
		}
	}
}

func TestSettingsPagesUseTheDarkAmberSidebar(t *testing.T) {
	loadTestTemplates()
	admin := &Session{Username: "arif", Role: roleAdmin}
	for _, page := range []struct {
		name, active string
	}{
		{"settings", "settings"}, {"settings_areas", "set-areas"}, {"settings_classes", "set-classes"},
		{"settings_categories", "set-categories"}, {"settings_sources", "set-sources"}, {"account", "account"},
	} {
		out := renderToString(t, page.name, PageData{Title: "T", Heading: "T", Active: page.active, Zone: "settings", Session: admin})
		for _, want := range []string{`class="settings-theme area-settings"`, `href="/admin/settings/sources"`,
			`href="/admin/settings/hazard-classes"`, `href="/admin/settings/hazard-categories"`, `href="/admin/settings/areas"`, `href="/users"`} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: missing %q", page.name, want)
			}
		}
		if !strings.Contains(out, `app-program-item active`) {
			t.Errorf("%s: no active sidebar item", page.name)
		}
	}
}

func TestSettingsSidebarHidesAdminLinksFromRegularUsers(t *testing.T) {
	loadTestTemplates()
	out := renderToString(t, "account", PageData{Title: "My account", Heading: "My account", Active: "account", Session: &Session{Username: "budi", Role: roleUser}})
	if strings.Contains(out, "/admin/settings") || strings.Contains(out, `href="/users"`) {
		t.Error("a regular user must not see the admin Settings links")
	}
	if !strings.Contains(out, `href="/account"`) {
		t.Error("My account link missing")
	}
}

func TestHazardAndSourceListsOfferTheRightActions(t *testing.T) {
	loadTestTemplates()
	admin := &Session{Username: "arif", Role: roleAdmin}

	classes := renderToString(t, "settings_classes", PageData{Title: "c", Heading: "c", Session: admin, ClassRows: []classRow{
		{ID: 1, Name: "Safety", Categories: 3, Used: 5},
		{ID: 2, Name: "Empty", Categories: 0},
		{ID: 3, Name: "Old", Archived: true, Categories: 1},
	}})
	if strings.Contains(classes, "hazard-classes/1/delete") || !strings.Contains(classes, "hazard-classes/2/delete") {
		t.Error("delete must be offered only for a class without categories")
	}
	if !strings.Contains(classes, "hazard-classes/1/archive") || !strings.Contains(classes, "hazard-classes/3/restore") {
		t.Error("archive/restore buttons missing")
	}

	cats := renderToString(t, "settings_categories", PageData{Title: "c", Heading: "c", Session: admin,
		ClassOptions: []string{"Safety", "Chemical"},
		CategoryRows: []categoryRow{{ID: 1, Class: "Chemical", Name: "Spill", Used: 2}, {ID: 2, Class: "Safety", Name: "Fire", ClassOff: true}}})
	if strings.Contains(cats, "hazard-categories/1/delete") || !strings.Contains(cats, "hazard-categories/2/delete") {
		t.Error("delete must be offered only for a category no observation uses")
	}
	if !strings.Contains(cats, "Class archived") || !strings.Contains(cats, "<td>Chemical</td>") {
		t.Error("category row should show its class as text and flag an archived class")
	}
	if strings.Contains(cats, "hazard-categories/1/save") {
		t.Error("no edit form may be open unless a row is being edited")
	}
	editing := renderToString(t, "settings_categories", PageData{Title: "c", Heading: "c", Session: admin, EditID: 1,
		ClassOptions: []string{"Safety", "Chemical"},
		CategoryRows: []categoryRow{{ID: 1, Class: "Chemical", Name: "Spill"}}})
	for _, want := range []string{"Edit category", "hazard-categories/1/save", `value="Spill"`, `<option value="Chemical" selected>`, `href="/admin/settings/hazard-categories">Cancel`, "row-editing"} {
		if !strings.Contains(editing, want) {
			t.Errorf("edit form: missing %q", want)
		}
	}

	src := renderToString(t, "settings_sources", PageData{Title: "s", Heading: "s", Session: admin, SourceRows: []sourceRow{
		{ID: 1, Name: "Internal Audit", Builtin: true},
		{ID: 5, Name: "Regulator Inspection"},
		{ID: 6, Name: "Used one", Used: 3},
	}})
	if strings.Contains(src, "sources?edit=1") || strings.Contains(src, "sources/1/delete") {
		t.Error("a built-in source must not be editable or deletable")
	}
	if !strings.Contains(src, "sources?edit=5") || !strings.Contains(src, "sources/5/delete") || strings.Contains(src, "sources/6/delete") {
		t.Error("custom sources: Edit always, delete only when unused")
	}
	builtinEdit := renderToString(t, "settings_sources", PageData{Title: "s", Heading: "s", Session: admin, EditID: 1,
		SourceRows: []sourceRow{{ID: 1, Name: "Internal Audit", Builtin: true}}})
	if strings.Contains(builtinEdit, "sources/1/rename") {
		t.Error("?edit= on a built-in source must not open an edit form")
	}
}

func TestCleanListName(t *testing.T) {
	if got, err := cleanListName("  Hot   Work ", "class", 40); err != nil || got != "Hot Work" {
		t.Errorf("got %q %v", got, err)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("x", 41), "a\x07b"} {
		if _, err := cleanListName(bad, "class", 40); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSourceListRules(t *testing.T) {
	snap := newSnapshot(nil, nil, nil, nil)
	snap.Sources = []refSource{{ID: 1, Name: "Internal Audit"}, {ID: 2, Name: "Old", Archived: true}, {ID: 3, Name: "Custom"}}
	if !snap.SourceAllowed("Custom", "") || snap.SourceAllowed("Old", "") || !snap.SourceAllowed("Old", "Old") || snap.SourceAllowed("SOC", "") {
		t.Error("SourceAllowed rules wrong")
	}
	if got := snap.SourceNames(""); len(got) != 2 {
		t.Errorf("form sources = %v, want the two active ones", got)
	}
	if got := snap.SourceNames("Old"); len(got) != 3 {
		t.Errorf("form sources keeping Old = %v", got)
	}
	if !snap.KnownSource("Old") || snap.KnownSource("Nope") {
		t.Error("KnownSource wrong")
	}
}

func TestSourceTypesPage(t *testing.T) {
	loadTestTemplates()
	out := renderToString(t, "settings_source_types", PageData{Title: "t", Heading: "t", Active: "set-types", Zone: "settings",
		Session:       &Session{Username: "arif", Role: roleAdmin},
		SourceOptions: []string{"Internal Audit", "External Audit"},
		TypeRows: []typeRow{
			{ID: 1, Source: "Internal Audit", Name: "Site Audit", Used: 4},
			{ID: 2, Source: "Internal Audit", Name: "KL Audit", Archived: true},
		}})
	if strings.Contains(out, "source-types/1/delete") || !strings.Contains(out, "source-types/2/delete") {
		t.Error("delete must be offered only for a type no observation uses")
	}
	if !strings.Contains(out, "source-types/2/restore") || !strings.Contains(out, "source-types/1/archive") {
		t.Error("archive/restore buttons missing")
	}
	if !strings.Contains(out, `<option value="External Audit">`) || !strings.Contains(out, `href="/admin/settings/source-types"`) {
		t.Error("source dropdown or sidebar link missing")
	}
}

func TestMedpostSettingsPageNeverShowsTheToken(t *testing.T) {
	loadTestTemplates()
	admin := &Session{Username: "arif", Role: roleAdmin}
	saved := renderToString(t, "settings_medpost", PageData{Title: "m", Heading: "m", Active: "set-medpost", Zone: "settings", Session: admin,
		Medpost:    medpostStatus{Configured: true, Source: medpostSourceSettings, URL: "https://medpost.example.com"},
		MedpostURL: "https://medpost.example.com"})
	for _, want := range []string{"Using the address and token saved here", `value="https://medpost.example.com"`, "Saved. Leave empty to keep it",
		"/admin/settings/medpost-api/clear", `name="action" value="test"`, `href="/admin/settings/medpost-api"`} {
		if !strings.Contains(saved, want) {
			t.Errorf("saved state: missing %q", want)
		}
	}
	if strings.Contains(saved, `name="token" maxlength="256" autocomplete="new-password"
             placeholder="Saved. Leave empty to keep it"
             required`) {
		t.Error("the token must not be required while one is saved")
	}
	off := renderToString(t, "settings_medpost", PageData{Title: "m", Heading: "m", Zone: "settings", Session: admin})
	if !strings.Contains(off, "Not connected") || strings.Contains(off, "/admin/settings/medpost-api/clear") {
		t.Error("not-configured state wrong")
	}
	env := renderToString(t, "settings_medpost", PageData{Title: "m", Heading: "m", Zone: "settings", Session: admin,
		Medpost: medpostStatus{Configured: true, Source: medpostSourceEnv, URL: "https://x.example"}})
	if !strings.Contains(env, "server's own settings") || strings.Contains(env, "/admin/settings/medpost-api/clear") {
		t.Error("environment state wrong")
	}
}

func TestActivitiesPageStates(t *testing.T) {
	loadTestTemplates()
	user := &Session{Username: "budi", Role: roleUser}
	admin := &Session{Username: "arif", Role: roleAdmin}

	off := renderToString(t, "activities", PageData{Title: "a", Session: user, ActState: activitiesOff})
	if !strings.Contains(off, "not connected") || strings.Contains(off, "/admin/settings/medpost-api") {
		t.Error("a regular user must see the not-connected note without the Settings link")
	}
	offAdmin := renderToString(t, "activities", PageData{Title: "a", Session: admin, ActState: activitiesOff})
	if !strings.Contains(offAdmin, `href="/admin/settings/medpost-api"`) {
		t.Error("an admin should get the link to Settings")
	}
	if !strings.Contains(renderToString(t, "activities", PageData{Title: "a", Session: user, ActState: activitiesNoAPI}), "does not offer the activities list") {
		t.Error("no-api note missing")
	}
	if !strings.Contains(renderToString(t, "activities", PageData{Title: "a", Session: user, ActState: activitiesError}), "could not be reached") {
		t.Error("error note missing")
	}

	acts := []medpostActivity{{Date: "2026-10-01", Program: "Health Screening", Type: "Socialization", Title: "Toolbox <b>talk</b>", Summary: "Hand hygiene", Location: "Camp",
		Photos: []medpostFile{{Name: "a.jpg", URL: "/activities/files/1/a.jpg"}},
		Proof:  []medpostFile{{Name: "list.pdf", Type: "AttendanceList", URL: "/activities/files/2/list.pdf", IsPDF: true}}}}
	ok := renderToString(t, "activities", PageData{Title: "a", Heading: "a", Session: user, ActState: activitiesOK, ActTab: "activities", ActSide: true,
		ActTree: []actProgram{{Name: "Health Screening"}}, ActMore: true, ActGroups: groupByType(acts)})
	for _, want := range []string{"2026-10-01", "Hand hygiene", "Camp", "Health programs", `href="/activities?program=Health%20Screening"`, "newest 500",
		`data-full="/activities/files/1/a.jpg"`, `href="/activities/files/2/list.pdf"`, `download="list.pdf"`, "1 photo<", `id="lightbox"`} {
		if !strings.Contains(ok, want) {
			t.Errorf("ok state: missing %q", want)
		}
	}
	if strings.Contains(ok, "<b>talk</b>") {
		t.Error("activity text must be HTML-escaped")
	}
	if strings.Contains(ok, `method="post" action="/activities`) {
		t.Error("the page is view only")
	}
}

func TestListRowsAreTextWithAnEditButton(t *testing.T) {
	loadTestTemplates()
	admin := &Session{Username: "arif", Role: roleAdmin}
	type page struct {
		name string
		data PageData
		edit string // the link that opens row 7
		form string // the form that appears when row 7 is being edited
	}
	for _, p := range []page{
		{"settings_areas", PageData{AreaRows: []areaRow{{ID: 7, Name: "Camp"}}}, "/admin/settings/areas?edit=7", "areas/7/rename"},
		{"settings_classes", PageData{ClassRows: []classRow{{ID: 7, Name: "Safety"}}}, "/admin/settings/hazard-classes?edit=7", "hazard-classes/7/rename"},
		{"settings_source_types", PageData{TypeRows: []typeRow{{ID: 7, Source: "Internal Audit", Name: "Site Audit"}}}, "/admin/settings/source-types?edit=7", "source-types/7/rename"},
	} {
		p.data.Title, p.data.Heading, p.data.Session = "t", "t", admin
		closed := renderToString(t, p.name, p.data)
		if !strings.Contains(closed, `href="`+p.edit+`"`) {
			t.Errorf("%s: missing the Edit button %q", p.name, p.edit)
		}
		if strings.Contains(closed, p.form) || strings.Contains(closed, `class="list-rename"`) {
			t.Errorf("%s: rows must be plain text, with no input fields until Edit is pressed", p.name)
		}
		p.data.EditID = 7
		open := renderToString(t, p.name, p.data)
		if !strings.Contains(open, p.form) || !strings.Contains(open, "Cancel") {
			t.Errorf("%s: the edit form did not open", p.name)
		}
	}
}

func TestEditIDComesFromTheQueryOrThePath(t *testing.T) {
	get := func(target string) int64 { return editID(httptest.NewRequest("GET", target, nil)) }
	if get("/x?edit=12") != 12 || get("/x") != 0 || get("/x?edit=abc") != 0 || get("/x?edit=-3") != 0 {
		t.Error("editID from the query wrong")
	}
	r := httptest.NewRequest("POST", "/admin/settings/areas/5/rename", nil)
	r.SetPathValue("id", "5")
	if editID(r) != 5 {
		t.Error("editID should fall back to the {id} of the request, so a failed save reopens the form")
	}
}

func TestSidebarHasOneUserAccountsEntry(t *testing.T) {
	loadTestTemplates()
	admin := &Session{Username: "arif", Role: roleAdmin}
	for _, page := range []struct{ name, active string }{{"users_list", "users"}, {"users_form", "users-new"}, {"settings", "settings"}} {
		out := renderToString(t, page.name, PageData{Title: "t", Heading: "t", Active: page.active, Zone: "settings", Session: admin})
		if !strings.Contains(out, "User accounts") || strings.Contains(out, "All users") || strings.Contains(out, ">Add user</span>") {
			t.Errorf("%s: the sidebar should offer only User accounts", page.name)
		}
		if page.active != "settings" && !strings.Contains(out, `href="/users" class="app-program-item active"`) {
			t.Errorf("%s: User accounts should be highlighted", page.name)
		}
	}
	// The Add user button stays on the list page itself.
	list := renderToString(t, "users_list", PageData{Title: "t", Heading: "t", Active: "users", Zone: "settings", Session: admin})
	if !strings.Contains(list, `href="/users/new" class="addlink"`) {
		t.Error("the users list must keep its Add user button")
	}
}

// The hazard class, category and control topic moved from the sidebar to a filter row on the list.
func TestListPageHasTheHazardFilterRow(t *testing.T) {
	loadTestTemplates()
	snap := testSnap()
	lq := listQuery{Source: "SOC", Status: statusOpen, Hazard: 2, Class: "Safety", Range: dateRange{From: "2026-10-01"}}
	out := renderToString(t, "observations_list", PageData{
		Title: "x", Heading: "x", Active: "obs-list", Session: &Session{Username: "arif", Role: roleAdmin}, ObsNav: testNav(),
		ListFilter: newListFilter(snap, lq), FilterAction: "/observations/list",
	})
	for _, want := range []string{`name="class"`, `name="hazard"`, `name="topic"`, `<optgroup label="Safety">`, `<option value="2" selected>Slips, Trips &amp; Falls</option>`,
		`<option value="3">PPE</option>`, `<option value="Safety" selected>Safety</option>`, `name="source" value="SOC"`, `name="status" value="open"`, `name="from" value="2026-10-01"`} {
		if !strings.Contains(out, want) {
			t.Errorf("hazard filter row is missing %q", want)
		}
	}
	for _, gone := range []string{`name="class" value=`, `name="hazard" value=`} {
		if strings.Contains(out, gone) {
			t.Errorf("the chosen hazard must not also be sent as a hidden field (%s)", gone)
		}
	}
}

// The new-post form holds a Before and an After photo group. Each file input must sit in its own
// .photo-field with its own preview and status, and the script must bind by that container; binding
// by the whole form drew the After boxes inside the Before group, so the After box never showed.
func TestEachPhotoInputHasItsOwnPreviewAndIsBoundToIt(t *testing.T) {
	loadTestTemplates()
	out := renderToString(t, "observations_form", staffFormData(&Observation{}, obsForm{}, nil))
	if n := strings.Count(out, `class="span-all photo-field"`); n != 2 {
		t.Fatalf("want 2 photo groups (Before, After), got %d", n)
	}
	for _, group := range strings.Split(out, `class="span-all photo-field"`)[1:] {
		end := strings.Index(group, "</div>\n    <label")
		if end < 0 {
			end = len(group)
		}
		g := group[:end]
		for _, want := range []string{`class="photo-input"`, `class="photo-preview"`, `photo-status`} {
			if !strings.Contains(g, want) {
				t.Errorf("a photo group is missing %s: %.200s", want, g)
			}
		}
	}
	if !strings.Contains(out, `i.closest(".photo-field")`) || strings.Contains(out, `i.closest("form")`) {
		t.Error("the photo script must bind each input to its own .photo-field, not to the whole form")
	}
}

// What the SOC wizard captured is shown to HSE on the post, and an urgent report stands out.
func TestDetailAndListShowTheWizardAnswersAndTheUrgentBadge(t *testing.T) {
	loadTestTemplates()
	o := &Observation{ID: 5, Source: sourceSOC, Category: catUnsafeCondition, Status: statusOpen, Description: "Lubang terbuka", Location: "Camp",
		ObservedAt: time.Now(), CreatedAt: time.Now(), Path: "B", ImmediateDanger: "Yes", Barricaded: "No", Recommendation: "Tutup lubang",
		ReviewFlags: "urgent; topic not picked", TopicPick: topicPickNone}
	out := renderToString(t, "obs_detail", detailPage(o, viewer{Username: "arif", Role: roleAdmin}))
	for _, want := range []string{`badge badge-urgent`, "B · Unsafe condition", "Immediate danger", "barricaded or stopped: No", "not picked", "For HSE review", "urgent; topic not picked", "Tutup lubang"} {
		if !strings.Contains(out, want) {
			t.Errorf("detail page is missing %q", want)
		}
	}
	list := renderToString(t, "observations_list", PageData{
		Title: "x", Heading: "x", Active: "obs-list", Session: &Session{Username: "arif", Role: roleAdmin}, ObsNav: testNav(), Observations: []*Observation{o},
	})
	if !strings.Contains(list, "badge-urgent") {
		t.Error("the list should mark an urgent report")
	}
	o.ReviewFlags = ""
	if out := renderToString(t, "obs_detail", detailPage(o, viewer{Username: "arif", Role: roleAdmin})); strings.Contains(out, "badge-urgent") {
		t.Error("a report without the flag must not be marked urgent")
	}
}
