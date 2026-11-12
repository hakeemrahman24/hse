package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"entry-permit/internal/worker"
)

// The site wears the same look as the gate scanner page (owner decision 2026-11-08): the company
// mark in a blue header, and square corners everywhere.

func TestCompanyLogoIsServedFromTheSiteItself(t *testing.T) {
	// WHY from the site: the page policy allows only "self", so a logo hosted elsewhere would not load.
	e := newEnv(t)
	r, body := e.get(t, "/static/fire_lng.png", nil)
	if r.StatusCode != 200 {
		t.Fatalf("status %d", r.StatusCode)
	}
	if ct := r.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("content type = %q", ct)
	}
	if !strings.HasPrefix(body, "\x89PNG") {
		t.Error("the file is not a PNG")
	}
}

func TestEveryPageHeaderShowsTheCompanyLogoAndTheBrowserIcon(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{"/", "/request", "/permit", "/login"} {
		_, body := e.get(t, path, nil)
		for _, want := range []string{`src="/static/logo-white.png"`, `rel="icon"`, `class="brand-logo"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %q", path, want)
			}
		}
	}
}

// A worker's own page has a sidebar with three options (owner decision 2026-11-08). The same
// pieces as before, regrouped: the permit and its downloads, the working-ticket buttons, the photo.
func TestWorkerPageHasASidebarWithMyPermitMyWorkingTicketsAndPhotos(t *testing.T) {
	e := newEnv(t)
	e.seedPermit(t, "WEP-AAAAA", "Approved", "LEP-AAAAA", "PT Maju", "2027-01-31", "2027-01-31")
	e.workers.people = []worker.Person{budi}
	_, body := e.lookup(t, "Budi Santoso", "05/03/1988", "dev-pass")
	for _, want := range []string{`id="my-permit"`, `id="my-photos"`, "My permit", "My working tickets", "Photos"} {
		if !strings.Contains(body, want) {
			t.Errorf("worker page lacks %q", want)
		}
	}
	// The options live in the blue column (owner request 2026-11-08), never in the white panel.
	shell := strings.Index(body, `<div class="shell">`)
	if shell < 0 || strings.Contains(body, `class="sidebar"`) || strings.Contains(body, `class="with-sidebar"`) {
		t.Fatal("the worker options must not have a white-panel sidebar of their own")
	}
	blue := body[:shell]
	for _, want := range []string{`href="#my-permit"`, `href="/ticket?lep=LEP-AAAAA"`, `href="#my-photos"`, `action="/my/logout"`} {
		if !strings.Contains(blue, want) {
			t.Errorf("the blue column lacks %q", want)
		}
	}
	// The working tickets option is itself the link (no second button); the permit download and the
	// photo form each sit under their own option, in this order.
	if !strings.Contains(blue, `href="/ticket?lep=LEP-AAAAA"`) {
		t.Error("the working tickets option does not lead to the Valid permit's schedule")
	}
	if strings.Contains(body, `id="my-tickets"`) || strings.Contains(body, `action="/my/ticket"`) {
		t.Error("a Valid permit needs no working tickets section or button of its own")
	}
	at := func(s string) int { return strings.Index(body, s) }
	permit, photos := at(`id="my-permit"`), at(`id="my-photos"`)
	pdf, photo := at(`action="/my/permit.pdf"`), at(`action="/my/photo"`)
	if !(permit < pdf && pdf < photos && photos < photo) {
		t.Errorf("order wrong: permit %d pdf %d photos %d photo %d", permit, pdf, photos, photo)
	}
	if strings.Contains(body, "Change my photo") {
		t.Error("the photo option still has its own submit button")
	}
}

func TestWorkerPageSaysSoWhenNoPermitCanTakeAWorkingTicket(t *testing.T) {
	e := newEnv(t)
	e.workers.people = []worker.Person{budi}
	_, body := e.lookup(t, "Budi Santoso", "05/03/1988", "dev-pass")
	i := strings.Index(body, `id="my-tickets"`)
	j := strings.Index(body, `id="my-photos"`)
	if i < 0 || j < i || !strings.Contains(body[i:j], "No valid permit") {
		t.Errorf("the working tickets option should say there is no valid permit yet")
	}
}

// Every page sits in one frame (revamp 2026-11-12): a slim navy top bar with the logo and the menu,
// and one centred white panel under it. Only the gate officer's scanner page is made for a phone,
// and that one is the gate Worker's own page, not this site's.
func TestEveryPageUsesTheDesktopShell(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{"/", "/request", "/permit", "/waiver", "/login"} {
		_, body := e.get(t, path, nil)
		for _, want := range []string{`<div class="app">`, `<header class="topbar"`, `<nav aria-label="Main">`,
			`<div class="shell">`, `class="brand-logo"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %q", path, want)
			}
		}
		// The panel closes cleanly: main, then the panel, then the page frame.
		if !strings.Contains(body, "</main>") || strings.Count(body, "<main") != strings.Count(body, "</main>") {
			t.Errorf("%s has unbalanced main tags", path)
		}
		if strings.Count(body, "<div") != strings.Count(body, "</div") {
			t.Errorf("%s has unbalanced div tags", path)
		}
	}
}

// The long forms use the width of the desktop panel: numbered cards, each with two columns, and
// long fields, the declaration and the button across both (owner decision 2026-11-08, cards 2026-11-12).
func TestTheWaiverFormIsNumberedCardsOfTwoColumns(t *testing.T) {
	e := newEnv(t)
	e.seedCompanies(t)
	_, body := e.get(t, "/waiver", nil)
	if !strings.Contains(body, `<form method="post" action="/waiver" enctype="multipart/form-data" autocomplete="off">`) ||
		strings.Count(body, `class="fcard"`) != 8 || !strings.Contains(body, `class="form-grid"`) {
		t.Error("the waiver form is not eight cards of form-grids")
	}
	if strings.Contains(body, `class="card narrow"`) {
		t.Error("the waiver form is still squeezed into a narrow card")
	}
	if n := strings.Count(body, `field field-wide`); n < 4 {
		t.Errorf("%d wide fields (risk, precaution, prevention, declaration need the full row)", n)
	}
}

func TestPermitStepTwoIsATwoColumnGrid(t *testing.T) {
	e := newEnv(t)
	e.workers.people = []worker.Person{budi}
	code, body := e.permitPost(t, nil)
	if code != 200 || !strings.Contains(body, `action="/permit/submit"`) {
		t.Fatalf("status %d: step 2 is not shown", code)
	}
	if !strings.Contains(body, `enctype="multipart/form-data" autocomplete="off" class="form-grid">`) {
		t.Error("step 2 is not a form-grid")
	}
	if strings.Contains(body, `<section class="card narrow">`) {
		t.Error("step 2 is still squeezed into a narrow card")
	}
	if !strings.Contains(body, `field field-wide`) {
		t.Error("the declaration and the street address need the full row")
	}
}

// Form field rules (owner decision 2026-11-08): a date can be typed as digits (24071983 becomes
// 24/07/1983) or picked from a calendar; phone numbers, ids and names are tidied as typed.
func TestEveryPageLoadsTheFormHelpers(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{"/", "/request", "/permit", "/waiver", "/login"} {
		_, body := e.get(t, path, nil)
		if !strings.Contains(body, `<script src="/static/inputs.js" defer></script>`) {
			t.Errorf("%s does not load the form helpers", path)
		}
	}
	r, body := e.get(t, "/static/inputs.js", nil)
	if r.StatusCode != 200 || !strings.Contains(body, "formatDate") {
		t.Errorf("inputs.js: status %d", r.StatusCode)
	}
}

func TestEveryDateBoxIsMarkedForTheDatePickerAndBirthDatesAreMarkedAsSuch(t *testing.T) {
	files, err := filepath.Glob("../../web/templates/*.html")
	if err != nil || len(files) == 0 {
		t.Fatalf("templates: %v", err)
	}
	inputTag := regexp.MustCompile(`(?s)<input\b[^>]*>`)
	n := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, tag := range inputTag.FindAllString(string(raw), -1) {
			if !strings.Contains(tag, `placeholder="dd/mm/yyyy"`) {
				continue
			}
			n++
			if !strings.Contains(tag, "data-date") {
				t.Errorf("%s: a date box has no data-date: %.80s", filepath.Base(f), tag)
			}
			if strings.Contains(tag, `name="dob"`) && !strings.Contains(tag, `data-date="dob"`) {
				t.Errorf("%s: a date of birth box is not marked data-date=\"dob\"", filepath.Base(f))
			}
		}
	}
	if n < 15 {
		t.Errorf("only %d date boxes found; the templates changed shape?", n)
	}
}

func TestIdNumbersThatAreDigitsOnlyAreMarkedSo(t *testing.T) {
	raw, err := os.ReadFile("../../web/templates/permit.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bpjs_kes", "bpjs_tk"} {
		re := regexp.MustCompile(`<input[^>]*name="` + name + `"[^>]*>`)
		tag := re.FindString(string(raw))
		if tag == "" || !strings.Contains(tag, "data-digits") {
			t.Errorf("%s is not marked data-digits: %s", name, tag)
		}
	}
}

func TestStylesheetHasSharpEdgesOnly(t *testing.T) {
	css, err := os.ReadFile("../../web/static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	// Every border-radius must be zero. A rounded one (8px, 10px, 999px) is a style slip.
	for _, m := range regexp.MustCompile(`border-radius:\s*([^;}]+)`).FindAllStringSubmatch(string(css), -1) {
		if v := strings.TrimSpace(m[1]); v != "0" {
			t.Errorf("border-radius: %s (square corners only)", v)
		}
	}
}

// ruleBody joins the declarations of every rule whose selector list names the class (as the class
// itself, not as the start of a longer one), so a test can ask what a button class ends up with.
func ruleBody(css, class string) string {
	re := regexp.MustCompile(`(?m)^([^{}@/]+)\{([^}]*)\}`)
	var out strings.Builder
	for _, m := range re.FindAllStringSubmatch(css, -1) {
		for _, sel := range strings.Split(m[1], ",") {
			sel = strings.TrimSpace(sel)
			if sel == class || strings.HasPrefix(sel, class+":") {
				out.WriteString(m[2])
				out.WriteString(";")
			}
		}
	}
	return out.String()
}

// Every button, whether a <button> or a link dressed as one, has one of two heights: the normal
// one for the main action, the small one for actions inside lists and tables (owner request
// 2026-11-08: "make sure all button are consistent in height").
func TestButtonsShareTwoHeights(t *testing.T) {
	raw, err := os.ReadFile("../../web/static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(raw)
	for class, height := range map[string]string{
		".btn": "40px", ".btn-small": "30px", ".btn-small-solid": "30px", ".btn-small-quiet": "30px",
	} {
		body := ruleBody(css, class)
		for _, want := range []string{"display: inline-flex", "box-sizing: border-box", "min-height: " + height} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %q", class, want)
			}
		}
	}
}
