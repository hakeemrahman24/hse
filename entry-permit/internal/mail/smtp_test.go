package mail

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
)

// fakeServer speaks just enough SMTP for one message. It records the envelope
// and the message, and refuses any recipient listed in refuse.
type fakeServer struct {
	addr   string
	rcpts  []string
	from   string
	data   string
	refuse map[string]bool
	done   chan struct{}
}

func startFakeServer(t *testing.T, refuse ...string) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeServer{addr: ln.Addr().String(), refuse: map[string]bool{}, done: make(chan struct{})}
	for _, r := range refuse {
		s.refuse[r] = true
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		defer close(s.done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		say := func(line string) { conn.Write([]byte(line + "\r\n")) }
		say("220 fake ESMTP")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				say("250 fake")
			case strings.HasPrefix(line, "MAIL FROM:"):
				s.from = strings.Trim(strings.TrimPrefix(line, "MAIL FROM:"), "<>")
				say("250 ok")
			case strings.HasPrefix(line, "RCPT TO:"):
				a := strings.Trim(strings.TrimPrefix(line, "RCPT TO:"), "<>")
				if s.refuse[a] {
					say("550 no such user")
					continue
				}
				s.rcpts = append(s.rcpts, a)
				say("250 ok")
			case line == "DATA":
				say("354 go ahead")
				var b strings.Builder
				for {
					l, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if l == ".\r\n" {
						break
					}
					b.WriteString(l)
				}
				s.data = b.String()
				say("250 queued")
			case line == "QUIT":
				say("221 bye")
				return
			default:
				say("250 ok")
			}
		}
	}()
	return s
}

func smtpFor(s *fakeServer) SMTP {
	host, port, _ := net.SplitHostPort(s.addr)
	return SMTP{Host: host, Port: port, From: "noreply@x.id", FromName: "Medical Project LNG"}
}

func TestSMTPSendsToEveryRecipientWithTheFullMessage(t *testing.T) {
	srv := startFakeServer(t)
	m := Message{To: []string{"rina@example.com"}, CC: []string{"boss@x.id"}, Subject: "Approved – Budi", Text: "plain", HTML: "<p>html</p>"}
	if err := smtpFor(srv).Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	<-srv.done
	if srv.from != "noreply@x.id" || strings.Join(srv.rcpts, ",") != "rina@example.com,boss@x.id" {
		t.Errorf("envelope from=%q rcpts=%v", srv.from, srv.rcpts)
	}
	for _, want := range []string{"To: rina@example.com", "Cc: boss@x.id", "Subject: =?utf-8?", "Message-ID: <", "@x.id>", "plain", "<p>html</p>"} {
		if !strings.Contains(srv.data, want) {
			t.Errorf("message lacks %q", want)
		}
	}
}

func TestSMTPReportsARefusedRecipient(t *testing.T) {
	srv := startFakeServer(t, "gone@example.com")
	m := Message{To: []string{"gone@example.com"}, Subject: "s", Text: "t", HTML: "h"}
	err := smtpFor(srv).Send(context.Background(), m)
	if err == nil || !strings.Contains(err.Error(), "gone@example.com") {
		t.Errorf("err = %v", err)
	}
}

func TestSMTPWithoutSettingsRefusesToSend(t *testing.T) {
	m := Message{To: []string{"a@b.id"}, Subject: "s", Text: "t", HTML: "h"}
	if err := (SMTP{}).Send(context.Background(), m); err == nil {
		t.Error("an unconfigured sender must fail, so the row stays queued")
	}
	if (SMTP{}).Configured() || !(SMTP{Host: "h", From: "a@b.id"}).Configured() {
		t.Error("Configured needs a host and a sender")
	}
}
