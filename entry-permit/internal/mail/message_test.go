package mail

import (
	"strings"
	"testing"
)

func TestCleanAddressesLowercasesDedupesAndSplitsAComma(t *testing.T) {
	got, err := CleanAddresses("Rina@Example.com, rina@example.com ,  boss@x.id")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "rina@example.com|boss@x.id" {
		t.Errorf("got %v", got)
	}
}

func TestCleanAddressesRefusesAnythingThatIsNotAPlainAddress(t *testing.T) {
	for _, bad := range []string{
		"", "   ", "not-an-address", "a@b.c\r\nBcc: evil@x.id", "Name <a@b.id>", "a@b.id\nX: y", "a b@c.id", "@c.id",
	} {
		if got, err := CleanAddresses(bad); err == nil {
			t.Errorf("CleanAddresses(%q) = %v, want an error", bad, got)
		}
	}
}

func TestBuildMIMEMakesATextAndHTMLMessage(t *testing.T) {
	m := Message{To: []string{"rina@example.com"}, CC: []string{"boss@x.id"}, Subject: "Approved – Budi", Text: "plain body", HTML: "<p>html body</p>"}
	raw, err := BuildMIME("noreply@x.id", "Medical Project LNG", m, "Sat, 07 Nov 2026 08:00:00 +0700", "id1@x.id")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{
		"From: \"Medical Project LNG\" <noreply@x.id>\r\n",
		"To: rina@example.com\r\n", "Cc: boss@x.id\r\n",
		"Date: Sat, 07 Nov 2026 08:00:00 +0700\r\n", "Message-ID: <id1@x.id>\r\n",
		"MIME-Version: 1.0\r\n", "multipart/alternative", "text/plain; charset=utf-8", "text/html; charset=utf-8",
		"Content-Transfer-Encoding: quoted-printable", "plain body", "<p>html body</p>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("message lacks %q\n%s", want, s)
		}
	}
	if strings.Contains(s, "Subject: Approved – Budi") {
		t.Error("a non-ASCII subject must be encoded")
	}
	if !strings.Contains(s, "Subject: =?utf-8?") {
		t.Errorf("subject not encoded:\n%s", s)
	}
	if strings.Count(s, "\r\n\r\n") < 3 {
		t.Error("headers and each part end with a blank line")
	}
}

func TestBuildMIMERefusesHeaderInjection(t *testing.T) {
	ok := Message{To: []string{"a@b.id"}, Subject: "s", Text: "t", HTML: "h"}
	cases := map[string]func(*Message){
		"subject newline": func(m *Message) { m.Subject = "hi\r\nBcc: evil@x.id" },
		"bad to":          func(m *Message) { m.To = []string{"a@b.id\r\nBcc: e@x.id"} },
		"bad cc":          func(m *Message) { m.CC = []string{"nope"} },
		"no recipient":    func(m *Message) { m.To = nil },
	}
	for name, mutate := range cases {
		m := ok
		mutate(&m)
		if _, err := BuildMIME("noreply@x.id", "Name", m, "d", "id@x.id"); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := BuildMIME("noreply@x.id", "Bad\r\nName", ok, "d", "id@x.id"); err == nil {
		t.Error("a newline in the sender name must be refused")
	}
}
