// Package mail holds everything about e-mail: the message and its MIME form,
// the SMTP sender, the outbox that stores every mail before it is sent, and the
// templates for the approval and rejection mails. It reads no request.
package mail

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
)

// Message is one e-mail before it is sent.
type Message struct {
	Kind      string   // wep-approved, wep-rejected, ...
	SourceKey string   // the permit or waiver number it is about
	To, CC    []string // already cleaned by CleanAddresses
	Subject   string
	Text      string
	HTML      string
}

// Mailer sends one message. The SMTP sender is the real one; tests use a fake.
type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// CleanAddresses reads a comma list of plain addresses, lower-cases them and
// drops repeats ("primary + CC, duplicates removed, lower-cased", gate-jobs doc 9).
//
// WHY only plain addresses and an exact match: the list comes from a public
// form. A display name, an angle bracket or a line break would let a visitor
// add headers (Bcc:) to a mail we send, so anything but "name@domain" is refused.
func CleanAddresses(list string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(list, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.ContainsAny(part, "\r\n\t <>\"") {
			return nil, fmt.Errorf("%q is not a plain e-mail address", part)
		}
		a, err := mail.ParseAddress(part)
		if err != nil || a.Address != part {
			return nil, fmt.Errorf("%q is not a plain e-mail address", part)
		}
		addr := strings.ToLower(part)
		if !seen[addr] {
			seen[addr] = true
			out = append(out, addr)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no e-mail address")
	}
	return out, nil
}

// cleanEach checks addresses that are already split.
func cleanEach(addrs []string) error {
	for _, a := range addrs {
		got, err := CleanAddresses(a)
		if err != nil || len(got) != 1 {
			return fmt.Errorf("bad e-mail address %q", a)
		}
	}
	return nil
}

// BuildMIME writes the message as bytes ready for the SMTP DATA command: a
// text part and an HTML part, both quoted-printable, headers CRLF-ended.
// date and messageID come from the caller so the result is repeatable in tests.
func BuildMIME(from, fromName string, m Message, date, messageID string) ([]byte, error) {
	if err := cleanEach([]string{from}); err != nil {
		return nil, err
	}
	if len(m.To) == 0 {
		return nil, errors.New("no recipient")
	}
	if err := cleanEach(m.To); err != nil {
		return nil, err
	}
	if err := cleanEach(m.CC); err != nil {
		return nil, err
	}
	for _, v := range []string{m.Subject, fromName, date, messageID} {
		if strings.ContainsAny(v, "\r\n") {
			return nil, errors.New("a header value contains a line break")
		}
	}
	sum := sha256.Sum256([]byte(messageID))
	boundary := "ep-" + hex.EncodeToString(sum[:12])

	var b strings.Builder
	h := func(name, value string) { b.WriteString(name + ": " + value + "\r\n") }
	h("From", fromHeader(fromName, from))
	h("To", strings.Join(m.To, ", "))
	if len(m.CC) > 0 {
		h("Cc", strings.Join(m.CC, ", "))
	}
	h("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	h("Date", date)
	h("Message-ID", "<"+messageID+">")
	h("MIME-Version", "1.0")
	h("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	b.WriteString("\r\n")
	for _, p := range []struct{ ctype, body string }{{"text/plain", m.Text}, {"text/html", m.HTML}} {
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString("Content-Type: " + p.ctype + "; charset=utf-8\r\n")
		b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		qp, err := encodeQP(p.body)
		if err != nil {
			return nil, err
		}
		b.WriteString(qp + "\r\n")
	}
	b.WriteString("--" + boundary + "--\r\n")
	return []byte(b.String()), nil
}

func fromHeader(name, addr string) string {
	if name == "" {
		return addr
	}
	if enc := mime.QEncoding.Encode("utf-8", name); enc != name { // non-ASCII name
		return enc + " <" + addr + ">"
	}
	q := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(name)
	return `"` + q + `" <` + addr + ">"
}

func encodeQP(s string) (string, error) {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
	var b strings.Builder
	w := quotedprintable.NewWriter(&b)
	if _, err := w.Write([]byte(s)); err != nil {
		return "", fmt.Errorf("encode body: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("encode body: %w", err)
	}
	return b.String(), nil
}
