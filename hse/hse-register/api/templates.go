package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strings"
)

type PageData struct {
	Title      string
	Heading    string
	Subheading string
	Zone       string // "settings" gives the page the Settings colours (set by the renderers, see zoneFor)
	Active     string
	Flash      string
	FlashKind  string
	Session    *Session

	// home page
	Modules []Module

	// observations
	ObsNav              *obsNav // set on the observation pages: switches the sidebar to Dashboard / Observations / hazard categories
	Dash                *dashboard
	Detail              *obsDetail
	Chips               []chip
	Range               dateRange         // the From/To filter shown above the dashboard and the list
	FilterAction        string            // where the date form sends its GET
	FilterHidden        map[string]string // other filters the date form keeps
	ClearURL            string            // link that removes the dates
	Blocks              []fileBlock
	Observations        []*Observation
	Observation         *Observation // the row being edited (empty for a new one)
	Form                obsForm      // values shown in the form fields
	FormErrors          map[string]string
	Topics              []refTopic
	HazardGroups        []hazardGroup
	Areas               []string
	CompanyOptions      []string
	CategoryOptions     []option
	StatusOptions       []option
	StaffSources        []string
	SourceDetailChoices []sourceDetailChoice

	// settings: areas, hazard classes and categories, sources
	AreaRows      []areaRow
	GuardText     string
	ListFilter    *listFilter // the hazard filter row of the observation list
	ClassRows     []classRow
	CategoryRows  []categoryRow
	ClassOptions  []string
	SourceRows    []sourceRow
	TypeRows      []typeRow
	TopicOptions  []topicOption
	TopicRows     []topicOption // the overview table: one row per topic with its keywords
	ManageRows    []keywordRow  // the keywords of the topic opened with Manage
	ManageName    string
	EditKeyword   *keywordRow // the keyword whose edit form is open
	TopicFilter   int64
	TryText       string
	TryResult     suggestion
	SourceOptions []string
	EditID        int64 // the list row whose edit form is open (0 = none)
	SubOf         int64 // Settings > Areas: the area whose sub-areas are open for editing (0 = none)
	// settings: Medpost API, and the Medical Activities page
	Medpost     medpostStatus
	MedpostURL  string
	ActState    string
	Activities  []medpostActivity
	ActFrom     string
	ActTo       string
	ActProgram  string
	ActPrograms []string
	ActMore     bool
	ActSide     bool // show the program sidebar of the Medical Activities page
	ActTab      string
	ActTree     []actProgram // the sidebar: programs with their sub-headers
	ActSub      *actSub      // the open sub-header, if any
	ActGroups   []actGroup
	DocGroups   []docGroup // the program documents page

	// users
	Users     []*AdminUser
	EditUser  *AdminUser // the account being edited or deleted (nil when adding)
	PostCount int        // observations that account entered, shown before deleting it
}

var pages = map[string]*template.Template{}

func loadPages(dir string) {
	names := []string{"observations_list", "observations_form", "obs_dashboard", "obs_detail", "users_list", "users_form", "users_delete",
		"settings", "settings_areas", "settings_classes", "settings_categories", "settings_sources", "settings_source_types", "settings_keywords", "settings_guard", "settings_medpost", "account", "activities"}
	for _, name := range names {
		t, err := template.New("").Funcs(pageFuncs).ParseFiles(dir+"/base.html", dir+"/obs_files.html", dir+"/"+name+".html")
		if err != nil {
			log.Fatalf("failed to parse template %s: %v", name, err)
		}
		pages[name] = t
	}
}

// pageFuncs are the helpers the staff templates use to show stored codes as words.
var pageFuncs = template.FuncMap{
	"catLabel":    func(v string) string { return labelOf(observationCategories, v) },
	"statusLabel": func(v string) string { return labelOf(observationStatuses, v) },
	"kb":          func(n int) string { return fmt.Sprintf("%d KB", (n+1023)/1024) },
	"titleParts":  titleParts,
}

// titleParts splits a page title such as "Observations · Internal Audit · HO Audit" at " · " so the
// template can colour each level differently (title, header, sub header).
func titleParts(heading string) []string { return strings.Split(heading, " · ") }

// homePages are the medpost-style standalone pages (home, activities, work permit). They share
// home_base.html for the head, top bar and footer, and are not wrapped in the sidebar shell.
var homePages = map[string]*template.Template{}

func loadHomePages(dir string) {
	for _, name := range []string{"start", "workpermit"} {
		t, err := template.ParseFiles(dir+"/home_base.html", dir+"/"+name+".html")
		if err != nil {
			log.Fatalf("failed to parse template %s: %v", name, err)
		}
		homePages[name] = t
	}
}

// zoneFor names the colour zone of a page. The Settings pages (the Settings home, the lists, Users
// and My account) share one sidebar layout with the rest of the app but use their own colours.
func zoneFor(page string) string {
	switch page {
	case "settings", "settings_areas", "settings_classes", "settings_categories", "settings_sources", "settings_source_types", "settings_keywords", "settings_guard", "settings_medpost",
		"account", "users_list", "users_form", "users_delete":
		return "settings"
	}
	return ""
}

func renderHome(w http.ResponseWriter, page string, data PageData) {
	data.Zone = zoneFor(page)
	t, ok := homePages[page]
	if !ok {
		http.Error(w, "template not found: "+page, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "homebase", data); err != nil {
		log.Printf("template error (%s): %v", page, err)
	}
}

var loginPage *template.Template

func loadLoginPage(dir string) {
	t, err := template.ParseFiles(dir + "/login.html")
	if err != nil {
		log.Fatalf("failed to parse login template: %v", err)
	}
	loginPage = t
}

func render(w http.ResponseWriter, page string, data PageData) {
	renderStatus(w, http.StatusOK, page, data)
}

// renderStatus renders a staff page with the given HTTP status (422 when a form is shown again
// with its error messages).
func renderStatus(w http.ResponseWriter, status int, page string, data PageData) {
	data.Zone = zoneFor(page)
	t, ok := pages[page]
	if !ok {
		http.Error(w, "template not found: "+page, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := t.ExecuteTemplate(w, "base", data); err != nil {
		log.Printf("template error (%s): %v", page, err)
	}
}

type LoginPageData struct {
	Error     string
	Usernames []string
}

func renderLogin(w http.ResponseWriter, errMsg string, usernames []string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := LoginPageData{Error: errMsg, Usernames: usernames}
	if err := loginPage.Execute(w, data); err != nil {
		log.Printf("login template error: %v", err)
	}
}
