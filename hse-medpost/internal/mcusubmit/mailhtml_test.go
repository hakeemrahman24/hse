package mcusubmit

import (
	"context"
	"strings"
	"testing"
)

func TestDigestHTMLIsATableAndEscapesWhatSubmittersType(t *testing.T) {
	waiting := []digestLine{{worker: `<script>alert(1)</script> & Co`, position: "Welder", company: `PT "Quote"`, submitter: "Budi", sentAt: "2026-11-09 08:00"}}
	got := digestHTML(waiting)
	for _, want := range []string{"<table", "<th", "Employee", "Sent by", "&lt;script&gt;alert(1)&lt;/script&gt; &amp; Co", "&#34;Quote&#34;", "Open them in medisite medical checkup section"} {
		if !strings.Contains(got, want) {
			t.Errorf("digest HTML lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<script>") {
		t.Errorf("a submitter-typed value reached the HTML unescaped:\n%s", got)
	}
	if !strings.Contains(got, `href="`+DigestStaffLink+`"`) {
		t.Errorf("the digest must link to the staff Submitted page:\n%s", got)
	}
}

func TestDigestHTMLCapsTheRowsLikeTheText(t *testing.T) {
	var lines []digestLine
	for i := 0; i < digestMaxLines+3; i++ {
		lines = append(lines, digestLine{worker: "W", position: "P", company: "C", submitter: "S", sentAt: "t"})
	}
	got := digestHTML(lines)
	if rows := strings.Count(got, "<tr>") - 1; rows != digestMaxLines { // minus the header row
		t.Errorf("%d body rows, want %d", rows, digestMaxLines)
	}
	if !strings.Contains(got, "and 3 more.") {
		t.Errorf("the overflow line is missing:\n%s", got)
	}
}

func TestReminderHTMLKeepsTheOrderAndEscapes(t *testing.T) {
	got := reminderHTML("https://example.com", []Expiring{
		{Name: "Soon <b>", Position: "Rigger", Company: "PT A", ValidUntil: "2026-12-01"},
		{Name: "Later", Position: "Welder", Company: "PT B", ValidUntil: "2027-01-15"},
	})
	if strings.Index(got, "2026-12-01") > strings.Index(got, "2027-01-15") {
		t.Errorf("soonest expiry must come first:\n%s", got)
	}
	if strings.Contains(got, "<b>") || !strings.Contains(got, "Soon &lt;b&gt;") {
		t.Errorf("name not escaped:\n%s", got)
	}
	if !strings.Contains(got, "Valid until") {
		t.Errorf("missing the Valid until column:\n%s", got)
	}
}

// A mailer that only knows Send (every existing test fake) must still get the text.
type textOnlyMailer struct{ got []string }

func (m *textOnlyMailer) Send(_ context.Context, _, _, body string) error {
	m.got = append(m.got, body)
	return nil
}

func TestSendRichFallsBackToTheTextForAMailerWithoutHTML(t *testing.T) {
	m := &textOnlyMailer{}
	if err := sendRich(context.Background(), m, "a@example.com", "S", "the text", "<p>html</p>"); err != nil {
		t.Fatal(err)
	}
	if len(m.got) != 1 || m.got[0] != "the text" {
		t.Fatalf("got %v, want the text body", m.got)
	}
}

// The digest closes with the signature in both the text and the HTML version.
func TestDigestEndsWithTheSignatureInTextAndHTML(t *testing.T) {
	waiting := []digestLine{{worker: "A Worker", position: "Welder", company: "PT X", submitter: "Budi", sentAt: "2026-11-09 08:00"}}
	if text := digestBody(waiting); !strings.HasSuffix(text, "\nKind Regards,\nLNG Medical Team\n") {
		t.Errorf("the digest text does not end with the signature:\n%s", text)
	}
	if page := digestHTML(waiting); !strings.Contains(page, "Kind Regards,<br>LNG Medical Team") {
		t.Errorf("the digest HTML lacks the signature:\n%s", page)
	}
}

func TestReminderEndsWithTheSignatureInTextAndHTML(t *testing.T) {
	lines := []Expiring{{Name: "A Worker", Position: "Welder", Company: "PT X", ValidUntil: "2026-12-01"}}
	if text := reminderBody("https://p", lines); !strings.HasSuffix(text, "\nKind Regards,\nLNG Medical Team\n") {
		t.Errorf("the reminder text does not end with the signature:\n%s", text)
	}
	if page := reminderHTML("https://p", lines); !strings.Contains(page, "Kind Regards,<br>LNG Medical Team") {
		t.Errorf("the reminder HTML lacks the signature:\n%s", page)
	}
}
