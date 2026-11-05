package mcusubmit

import (
	"html"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The My documents table is five columns with merged cells, the page buttons sit in the title bar
// and "Needs action" is a chip that carries the count (owner, 2026-11-06). If MCU_DUMP_PAGE is set
// the rendered page is written there so the layout can be opened in a browser.
func TestMyDocumentsPageLayout(t *testing.T) {
	e := newEnv(t)
	id := e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, cookie := e.signIn("hr.budi", "a-long-passphrase-1")
	closeFor(t, e.store, e.companyID, id, "Mahessa Rasyid") // closed as Unfit: its note shows
	sentDoc(t, e.store, id, "Siti Aminah")                  // pending

	body := e.get("/mcu-submit/documents", cookie).Body.String()
	if out := os.Getenv("MCU_DUMP_PAGE"); out != "" {
		_ = os.WriteFile(out, []byte(body), 0o600)
	}
	for _, want := range []string{
		`<th scope="col">Worker</th><th scope="col">Company</th><th scope="col">Sent</th><th scope="col">Status</th><th scope="col">Action</th>`,
		"2 documents, page 1 of 1", `class="mp-titlebar"`, `class="mp-chip"`, `?needs=1`, "Open PDF",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, gone := range []string{"Date of birth", "Nationality</th>", "Uploaded by", `id="f-needs"`} {
		if strings.Contains(body, gone) {
			t.Errorf("page still has %q", gone)
		}
	}
	// The New batch button is in the title bar, before the table, and not repeated below it.
	if strings.Index(body, `href="/mcu-submit/batch">New batch`) > strings.Index(body, "<table") || strings.Count(body, ">New batch<") != 2 {
		// the sidebar also links New batch, so two is the title bar plus the sidebar
		t.Errorf("New batch placement wrong (count %d)", strings.Count(body, ">New batch<"))
	}
	one := e.get("/mcu-submit/documents?worker=Mahessa", cookie).Body.String()
	if !strings.Contains(one, "1 document, page 1 of 1") {
		t.Errorf("singular count missing")
	}
	needs := e.get("/mcu-submit/documents?needs=1", cookie).Body.String()
	if !strings.Contains(needs, `class="mp-chip on"`) || !strings.Contains(needs, `name="needs" value="1"`) {
		t.Errorf("active chip or hidden field missing")
	}
}

// The admin's All accounts table is five columns: name over email, company, status (with its note),
// ROMIF (date underneath) and the actions (owner, 2026-11-06).
func TestAdminAccountsTableLayout(t *testing.T) {
	e := newEnv(t)
	e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, admin := e.signIn("admin", "pelican-orbit-77")
	body := e.get("/mcu-submit/admin", admin).Body.String()
	if out := os.Getenv("MCU_DUMP_ADMIN"); out != "" {
		_ = os.WriteFile(out, []byte(body), 0o600)
	}
	for _, want := range []string{
		`<th scope="col">Account</th><th scope="col">Company</th><th scope="col">Status</th><th scope="col">ROMIF</th><th scope="col">Action</th>`,
		`<div class="mp-sub">hr@example.com</div>`, "Disable", "Archive", "Delete…",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("admin page lacks %q", want)
		}
	}
	if strings.Contains(body, `<th scope="col">Email</th><th scope="col">Company</th><th scope="col">Status</th><th scope="col">ROMIF</th><th scope="col">Note</th>`) {
		t.Error("the old seven-column accounts header is still there")
	}
}

// A waiting request shows as four columns (account, company over position, requested, decide).
func TestAdminRequestsTableLayout(t *testing.T) {
	e := newEnv(t)
	e.post("/mcu-submit/register", e.registerForm("budi@example.com"))
	_ = e.post("/mcu-submit/verify", url.Values{"token": {tokenFrom(t, e.mail.last(t))}})
	_, admin := e.signIn("admin", "pelican-orbit-77")
	body := e.get("/mcu-submit/admin", admin).Body.String()
	if out := os.Getenv("MCU_DUMP_REQ"); out != "" {
		_ = os.WriteFile(out, []byte(body), 0o600)
	}
	for _, want := range []string{
		`<th scope="col">Account</th><th scope="col">Company</th><th scope="col">Requested</th><th scope="col">Decide</th>`,
		`<div class="mp-sub">budi@example.com</div>`, "Approve", "Reject",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("requests table lacks %q", want)
		}
	}
}

// Lists show the company's abbreviation first and its full name small underneath; a company with
// no abbreviation shows its name alone (owner, 2026-11-06).
func TestCompanyShowsItsAbbreviationFirst(t *testing.T) {
	e := newEnv(t)
	id := e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, cookie := e.signIn("hr.budi", "a-long-passphrase-1")
	sentDoc(t, e.store, id, "Siti Aminah") // company text "PT Example"
	_, admin := e.signIn("admin", "pelican-orbit-77")
	withAbbr := `<strong>PTEX</strong><div class="mp-sub">PT Example</div>`
	noAbbr := `<strong>PT Example</strong>`

	if _, err := e.store.DB.Exec(`UPDATE Company SET Abbreviation = 'PTEX' WHERE CompanyID = ?`, e.companyID); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"My documents": e.get("/mcu-submit/documents", cookie).Body.String(),
		"admin":        e.get("/mcu-submit/admin", admin).Body.String(),
	} {
		if !strings.Contains(body, withAbbr) {
			t.Errorf("%s: abbreviation first with the name small is missing", name)
		}
	}
	if _, err := e.store.DB.Exec(`UPDATE Company SET Abbreviation = NULL WHERE CompanyID = ?`, e.companyID); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"My documents": e.get("/mcu-submit/documents", cookie).Body.String(),
		"admin":        e.get("/mcu-submit/admin", admin).Body.String(),
	} {
		if !strings.Contains(body, noAbbr) || strings.Contains(body, `<strong>PTEX</strong>`) {
			t.Errorf("%s: a company without an abbreviation should show its name alone", name)
		}
	}
}

// New batch carries the upload progress block, its script, and the messages in every language
// (owner, 2026-11-06): a big scan must not look like a hang while it uploads and is rebuilt.
func TestNewBatchHasTheUploadProgressBlockInEveryLanguage(t *testing.T) {
	u := newUploadEnv(t)
	body := u.get("/mcu-submit/batch", u.cookie).Body.String()
	for _, want := range []string{`id="up-progress"`, `id="up-status"`, `data-m="checking"`, "/static/mcu-portal-upload.js"} {
		if !strings.Contains(body, want) {
			t.Errorf("New batch lacks %q", want)
		}
	}
	for _, text := range []string{
		"Uploading the file…", "The file is larger than 15 MB. Split it or scan at a lower resolution.",
	} {
		if _, ok := portalText[text]; !ok {
			t.Errorf("no Indonesian/Chinese text for %q", text)
		}
	}
	for _, key := range []string{"Uploaded. Checking and shrinking the file now.", "This file is over 10 MB, so uploading", "This file is over 10 MB. Making it smaller first", "Made smaller: {from} MB", "The connection was cut or timed out."} {
		found := false
		for en := range portalText {
			if strings.HasPrefix(en, key) {
				found = true
			}
		}
		if !found {
			t.Errorf("no translation starting %q", key)
		}
	}
	// Every message in the page must be a dictionary key word for word, or it stays English.
	for _, m := range regexp.MustCompile(`data-m="[a-z]+">([^<]+)</span>`).FindAllStringSubmatch(body, -1) {
		if _, ok := portalText[html.UnescapeString(m[1])]; !ok {
			t.Errorf("page message has no translation entry: %q", m[1])
		}
	}
}
