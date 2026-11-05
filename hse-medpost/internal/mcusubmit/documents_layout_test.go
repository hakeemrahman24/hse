package mcusubmit

import (
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
