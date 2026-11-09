package mcusubmit

import (
	"context"
	"strings"
	"testing"
)

func TestBuildMessageIsPlainTextWithCRLFHeaders(t *testing.T) {
	msg := string(buildMessage("Medical Checkups <noreply@example.com>", "budi@example.com", "Confirm your email", "Hello\nWorld"))

	for _, want := range []string{
		"From: Medical Checkups <noreply@example.com>\r\n",
		"To: budi@example.com\r\n",
		"Subject: Confirm your email\r\n",
		"Content-Type: text/plain; charset=UTF-8\r\n",
		"MIME-Version: 1.0\r\n",
		"\r\n\r\nHello\r\nWorld",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
}

// A subject or address with a line break could add a header such as Bcc. The mailer must never
// let one through, whatever a caller passes.
func TestBuildMessageCannotBeUsedToInjectHeaders(t *testing.T) {
	msg := string(buildMessage("noreply@example.com", "a@example.com\r\nBcc: victim@example.com", "Hi\r\nBcc: victim@example.com", "body"))

	head := strings.SplitN(msg, "\r\n\r\n", 2)[0]
	for _, line := range strings.Split(head, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "bcc:") {
			t.Fatalf("an injected Bcc header got through:\n%s", head)
		}
	}
}

func TestEncodeSubjectKeepsNonASCIIReadable(t *testing.T) {
	got := encodeSubject("Pemeriksaan kesehatan — dikonfirmasi")
	if !strings.HasPrefix(got, "=?UTF-8?") {
		t.Fatalf("a non-ASCII subject must be MIME-encoded, got %q", got)
	}
	if encodeSubject("Plain subject") != "Plain subject" {
		t.Fatal("an ASCII subject should be left alone")
	}
}

func TestLogMailerRecordsTheMessageForDevelopment(t *testing.T) {
	var m LogMailer
	var lines []string
	m.Logf = func(format string, args ...any) { lines = append(lines, format) }
	if err := m.Send(context.Background(), "budi@example.com", "Subject", "body with a link"); err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 {
		t.Fatal("LogMailer logged nothing")
	}
}

func TestSMTPMailerRefusesToSendWithoutAHost(t *testing.T) {
	m := &SMTPMailer{}
	if err := m.Send(context.Background(), "budi@example.com", "s", "b"); err == nil {
		t.Fatal("an unconfigured SMTPMailer must return an error, not silently drop the mail")
	}
}

func TestBuildMessageHTMLIsMultipartWithTextFirst(t *testing.T) {
	msg := string(buildMessageHTML("a@example.com", "b@example.com", "Subject", "plain part", "<p>html part</p>"))
	if !strings.Contains(msg, "multipart/alternative") {
		t.Fatalf("not multipart/alternative:\n%s", msg)
	}
	text, html := strings.Index(msg, "text/plain"), strings.Index(msg, "text/html")
	if text < 0 || html < 0 || text > html {
		t.Fatalf("the text part must come before the HTML part (clients show the last one they understand):\n%s", msg)
	}
	if !strings.Contains(msg, "--=_mcu_alt_7f3a9c--") {
		t.Fatalf("the closing boundary is missing:\n%s", msg)
	}
	if plain := string(buildMessageHTML("a@example.com", "b@example.com", "S", "just text", "")); strings.Contains(plain, "multipart") {
		t.Fatalf("no HTML given, but the message is multipart:\n%s", plain)
	}
}
