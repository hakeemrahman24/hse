package mcusubmit

import (
	"net/url"
	"os"
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
		`<th scope="col">Worker</th><th scope="col">Position / company</th><th scope="col">Sent</th><th scope="col">Status</th><th scope="col">Action</th>`,
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
		`<th scope="col">Account</th><th scope="col">Company / position</th><th scope="col">Requested</th><th scope="col">Decide</th>`,
		`<div class="mp-sub">budi@example.com</div>`, "Approve", "Reject",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("requests table lacks %q", want)
		}
	}
}
