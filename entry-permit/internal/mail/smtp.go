package mail

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// smtpTimeout bounds the whole conversation with the mail server, so a stuck
// relay cannot hold a request or the drain for long.
const smtpTimeout = 30 * time.Second

// SMTP sends through an SMTP relay (Brevo). The password comes from .env, never
// from Settings or the database.
type SMTP struct {
	Host, Port, User, Password string
	From, FromName             string
}

// Configured reports whether there is enough to send. A site without it keeps
// mail in the outbox (Pending) instead of failing the request.
func (s SMTP) Configured() bool { return s.Host != "" && s.From != "" }

// Send delivers one message. Port 465 uses TLS from the start; any other port
// must offer STARTTLS before the password is sent (a local test server on
// 127.0.0.1 is the only plain-text exception).
func (s SMTP) Send(ctx context.Context, m Message) error {
	if !s.Configured() {
		return errors.New("SMTP is not configured")
	}
	domain := s.From[strings.LastIndex(s.From, "@")+1:]
	id := make([]byte, 12)
	if _, err := rand.Read(id); err != nil {
		return fmt.Errorf("message id: %w", err)
	}
	raw, err := BuildMIME(s.From, s.FromName, m, time.Now().Format(time.RFC1123Z), hex.EncodeToString(id)+"@"+domain)
	if err != nil {
		return err
	}

	port := s.Port
	if port == "" {
		port = "587"
	}
	addr := net.JoinHostPort(s.Host, port)
	d := net.Dialer{Timeout: smtpTimeout}
	var conn net.Conn
	if port == "465" {
		conn, err = (&tls.Dialer{NetDialer: &d, Config: &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("connect to mail server: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(smtpTimeout))

	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		return fmt.Errorf("mail server greeting: %w", err)
	}
	defer c.Close()
	if port != "465" {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}); err != nil {
				return fmt.Errorf("start TLS: %w", err)
			}
		} else if s.Host != "127.0.0.1" && s.Host != "localhost" {
			return errors.New("mail server does not offer TLS; refusing to send")
		}
	}
	if s.User != "" {
		if err := c.Auth(smtp.PlainAuth("", s.User, s.Password, s.Host)); err != nil {
			return fmt.Errorf("mail login: %w", err)
		}
	}
	if err := c.Mail(s.From); err != nil {
		return fmt.Errorf("sender refused: %w", err)
	}
	for _, rcpt := range append(append([]string{}, m.To...), m.CC...) {
		if err := c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("recipient %s refused: %w", rcpt, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("start message: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("finish message: %w", err)
	}
	return c.Quit()
}
