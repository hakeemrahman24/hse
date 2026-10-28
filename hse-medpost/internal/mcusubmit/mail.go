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

// SMTPMailer sends through an SMTP relay such as Brevo (smtp-relay.brevo.com:587). It insists on
// STARTTLS before it sends the login, so credentials never cross the network in clear.
type SMTPMailer struct {
	Host, Username, Password, From string
	Port                           int
}

const smtpTimeout = 20 * time.Second

func (m *SMTPMailer) Send(ctx context.Context, to, subject, body string) error {
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
	if _, err := w.Write(buildMessage(m.From, rcpt.Address, subject, body)); err != nil {
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
	var b bytes.Buffer
	b.WriteString("From: " + headerValue(from) + "\r\n")
	b.WriteString("To: " + headerValue(to) + "\r\n")
	b.WriteString("Subject: " + encodeSubject(subject) + "\r\n")
	b.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	b.WriteString("\r\n")
	text := strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
	qp := quotedprintable.NewWriter(&b)
	_, _ = qp.Write([]byte(text))
	_ = qp.Close()
	return b.Bytes()
}
