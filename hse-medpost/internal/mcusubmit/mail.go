package mcusubmit

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Mailer sends one plain-text message. The portal depends on this interface, not on SMTP, so tests
// use a fake and the real sender can change (Brevo's SMTP relay today) without touching handlers.
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

// HTMLMailer is a Mailer that can also send an HTML version next to the plain text. Both real
// senders (SMTP and the development log) do; a test fake that only records Send keeps working and
// gets the text. Callers go through sendRich, never assert this themselves.
type HTMLMailer interface {
	SendHTML(ctx context.Context, to, subject, text, html string) error
}

// sendRich sends text plus an HTML table version when the mailer supports it, and the text alone
// otherwise, so the plain-text body stays the one every client and every test can read.
func sendRich(ctx context.Context, m Mailer, to, subject, text, html string) error {
	if hm, ok := m.(HTMLMailer); ok && html != "" {
		return hm.SendHTML(ctx, to, subject, text, html)
	}
	return m.Send(ctx, to, subject, text)
}

// LogMailer writes each message to the log instead of sending it. Development only: the body holds
// working links, so it must never be used in production.
type LogMailer struct {
	Logf func(format string, args ...any)
}

func (m LogMailer) Send(_ context.Context, to, subject, body string) error {
	logf := m.Logf
	if logf == nil {
		logf = log.Printf
	}
	logf("MCU portal mail (NOT SENT, development) to=%s subject=%q\n%s", to, subject, body)
	return nil
}

func (m LogMailer) SendHTML(ctx context.Context, to, subject, text, _ string) error {
	return m.Send(ctx, to, subject, text) // the log shows the readable text part only
}

// SMTPMailer sends through an SMTP relay such as Brevo (smtp-relay.brevo.com:587). It insists on
// STARTTLS before it sends the login, so credentials never cross the network in clear.
type SMTPMailer struct {
	Host, Username, Password, From string
	Port                           int
}

const smtpTimeout = 20 * time.Second

func (m *SMTPMailer) Send(ctx context.Context, to, subject, body string) error {
	return m.SendHTML(ctx, to, subject, body, "")
}

// SendHTML sends multipart/alternative (text first, HTML second) when html is not empty.
func (m *SMTPMailer) SendHTML(ctx context.Context, to, subject, body, html string) error {
	if m == nil || m.Host == "" || m.From == "" {
		return errors.New("mcusubmit: SMTP is not configured")
	}
	from, err := mail.ParseAddress(m.From)
	if err != nil {
		return fmt.Errorf("mcusubmit: bad From address: %w", err)
	}
	rcpt, err := mail.ParseAddress(to)
	if err != nil {
		return fmt.Errorf("mcusubmit: bad recipient: %w", err)
	}
	port := m.Port
	if port == 0 {
		port = 587
	}
	dialer := net.Dialer{Timeout: smtpTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(m.Host, strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("mcusubmit: smtp dial: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(smtpTimeout))
	c, err := smtp.NewClient(conn, m.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("mcusubmit: smtp hello: %w", err)
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); !ok {
		return errors.New("mcusubmit: the SMTP server does not offer STARTTLS; refusing to send credentials")
	}
	if err := c.StartTLS(&tls.Config{ServerName: m.Host, MinVersion: tls.VersionTLS12}); err != nil {
		return fmt.Errorf("mcusubmit: starttls: %w", err)
	}
	if m.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", m.Username, m.Password, m.Host)); err != nil {
			return fmt.Errorf("mcusubmit: smtp auth: %w", err)
		}
	}
	if err := c.Mail(from.Address); err != nil {
		return fmt.Errorf("mcusubmit: smtp mail from: %w", err)
	}
	if err := c.Rcpt(rcpt.Address); err != nil {
		return fmt.Errorf("mcusubmit: smtp rcpt: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mcusubmit: smtp data: %w", err)
	}
	if _, err := w.Write(buildMessageHTML(m.From, rcpt.Address, subject, body, html)); err != nil {
		return fmt.Errorf("mcusubmit: smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mcusubmit: smtp send: %w", err)
	}
	return c.Quit()
}

// headerValue removes line breaks so a value can never start a new header (header injection).
func headerValue(s string) string {
	return strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(s))
}

// encodeSubject MIME-encodes a subject only when it has non-ASCII text.
func encodeSubject(s string) string {
	s = headerValue(s)
	for _, r := range s {
		if r > unicode.MaxASCII {
			return mime.QEncoding.Encode("UTF-8", s)
		}
	}
	return s
}

// buildMessage returns the full RFC 5322 message: CRLF headers, then a quoted-printable UTF-8 body.
func buildMessage(from, to, subject, body string) []byte {
	return buildMessageHTML(from, to, subject, body, "")
}

// buildMessageHTML is buildMessage with an optional HTML alternative. Mail clients show the LAST
// part they understand, so the plain text goes first and the HTML second. The boundary is fixed
// text that cannot occur in a quoted-printable body (it contains "=_").
func buildMessageHTML(from, to, subject, body, html string) []byte {
	const boundary = "=_mcu_alt_7f3a9c"
	var b bytes.Buffer
	b.WriteString("From: " + headerValue(from) + "\r\n")
	b.WriteString("To: " + headerValue(to) + "\r\n")
	b.WriteString("Subject: " + encodeSubject(subject) + "\r\n")
	b.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	if html == "" {
		writeQPPart(&b, "text/plain", body)
		return b.Bytes()
	}
	b.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n")
	b.WriteString("--" + boundary + "\r\n")
	writeQPPart(&b, "text/plain", body)
	b.WriteString("\r\n--" + boundary + "\r\n")
	writeQPPart(&b, "text/html", html)
	b.WriteString("\r\n--" + boundary + "--\r\n")
	return b.Bytes()
}

// writeQPPart writes one part's headers, a blank line and the quoted-printable UTF-8 content.
func writeQPPart(b *bytes.Buffer, contentType, content string) {
	b.WriteString("Content-Type: " + contentType + "; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	b.WriteString("\r\n")
	text := strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\n", "\r\n")
	qp := quotedprintable.NewWriter(b)
	_, _ = qp.Write([]byte(text))
	_ = qp.Close()
}
