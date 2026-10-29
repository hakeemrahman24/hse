package mcusubmit

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

var goodMail = MailInput{Host: "smtp-relay.brevo.com", Port: 587, Username: "9a1b2c@smtp-brevo.com", Password: "xsmtpsib-SECRET-KEY-123", From: "Medpost <no-reply@example.com>"}

func mailStore(t *testing.T) (*Store, *ConfigMailer, *fakeMailer) {
	t.Helper()
	s, _, _ := newTestStore(t)
	fallback := &fakeMailer{}
	return s, &ConfigMailer{Store: s, Key: MailKey("test-secret"), Fallback: fallback}, fallback
}

func TestMailSettingsAreStoredEncryptedAndNeverReadBack(t *testing.T) {
	s, cm, _ := mailStore(t)
	ctx := context.Background()
	if err := cm.Save(ctx, goodMail); err != nil {
		t.Fatal(err)
	}
	got, err := cm.Settings(ctx)
	if err != nil || got.Host != goodMail.Host || got.Port != 587 || got.Username != goodMail.Username || !strings.Contains(got.From, "Medpost") || !strings.Contains(got.From, "<no-reply@example.com>") || !got.HasPassword {
		t.Fatalf("settings = %+v, %v", got, err)
	}
	var stored string
	if err := s.DB.QueryRow(`SELECT PasswordEnc FROM McuMailSetting WHERE McuMailSettingID = 1`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == "" || strings.Contains(stored, "SECRET") {
		t.Fatalf("the SMTP key is not sealed in the database: %q", stored)
	}
	plain, err := openSecret(MailKey("test-secret"), stored)
	if err != nil || plain != goodMail.Password {
		t.Fatalf("round trip = %q, %v", plain, err)
	}
	if _, err := openSecret(MailKey("another-secret"), stored); err == nil {
		t.Error("a different server secret opened the SMTP key")
	}
}

func TestSavingWithoutAPasswordKeepsTheStoredOneAndANewOneReplacesIt(t *testing.T) {
	s, cm, _ := mailStore(t)
	ctx := context.Background()
	_ = cm.Save(ctx, goodMail)
	var before string
	_ = s.DB.QueryRow(`SELECT PasswordEnc FROM McuMailSetting`).Scan(&before)

	again := goodMail
	again.Password, again.Port = "", 2525
	if err := cm.Save(ctx, again); err != nil {
		t.Fatal(err)
	}
	var after string
	_ = s.DB.QueryRow(`SELECT PasswordEnc FROM McuMailSetting`).Scan(&after)
	if after != before {
		t.Error("an empty password field replaced the stored key")
	}
	if got, _ := cm.Settings(ctx); got.Port != 2525 {
		t.Errorf("port = %d, want 2525", got.Port)
	}
	again.Password = "new-key-456"
	_ = cm.Save(ctx, again)
	_ = s.DB.QueryRow(`SELECT PasswordEnc FROM McuMailSetting`).Scan(&after)
	if plain, _ := openSecret(MailKey("test-secret"), after); plain != "new-key-456" {
		t.Errorf("new key = %q", plain)
	}
}

func TestMailSettingsRefuseValuesThatCouldBreakTheConnectionOrAHeader(t *testing.T) {
	_, cm, _ := mailStore(t)
	ctx := context.Background()
	for name, mutate := range map[string]func(*MailInput){
		"empty host":        func(m *MailInput) { m.Host = "" },
		"host with space":   func(m *MailInput) { m.Host = "smtp relay.com" },
		"host with scheme":  func(m *MailInput) { m.Host = "https://smtp.example.com" },
		"host with a port":  func(m *MailInput) { m.Host = "smtp.example.com:587" },
		"port 70000":        func(m *MailInput) { m.Port = 70000 },
		"negative port":     func(m *MailInput) { m.Port = -1 },
		"from is not mail":  func(m *MailInput) { m.From = "nobody" },
		"empty from":        func(m *MailInput) { m.From = "" },
		"line break in key": func(m *MailInput) { m.Password = "abc\r\nRCPT TO:<x@y>" },
		"line break in who": func(m *MailInput) { m.Username = "abc\ndef" },
	} {
		in := goodMail
		mutate(&in)
		if err := cm.Save(ctx, in); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if got, _ := cm.Settings(ctx); got.Host != "" {
		t.Error("a refused save changed the stored settings")
	}
	noKey := goodMail
	noKey.Password = ""
	if err := cm.Save(ctx, noKey); err == nil {
		t.Error("a login with no key stored and none given was accepted")
	}
	open := MailInput{Host: "relay.example.com", From: "a@example.com"}
	if err := cm.Save(ctx, open); err != nil {
		t.Errorf("a relay without a login is valid: %v", err)
	}
	if got, _ := cm.Settings(ctx); got.Port != 587 {
		t.Errorf("port 0 became %d, want 587", got.Port)
	}
}

func TestMailUsesThePageSettingsFirstThenTheEnvironmentThenFailsClearly(t *testing.T) {
	_, cm, fallback := mailStore(t)
	ctx := context.Background()

	if err := cm.Send(ctx, "a@example.com", "s", "b"); err != nil || len(fallback.sent) != 1 {
		t.Fatalf("no page settings: err %v, fallback got %d", err, len(fallback.sent))
	}
	if src := cm.Source(ctx); src != SourceEnvironment {
		t.Errorf("source = %q, want the environment", src)
	}

	in := goodMail
	in.Host, in.Port = "127.0.0.1", 1 // nothing listens there: the send must fail, not fall back
	if err := cm.Save(ctx, in); err != nil {
		t.Fatal(err)
	}
	if err := cm.Send(ctx, "a@example.com", "s", "b"); err == nil || len(fallback.sent) != 1 {
		t.Fatalf("page settings present: err %v, fallback got %d; the page login must win and must not silently fall back", err, len(fallback.sent))
	}
	if src := cm.Source(ctx); src != SourcePage {
		t.Errorf("source = %q, want the page", src)
	}

	bare := &ConfigMailer{Store: cm.Store, Key: MailKey("test-secret")}
	_, _ = cm.Store.DB.Exec(`UPDATE McuMailSetting SET Host = ''`)
	if err := bare.Send(ctx, "a@example.com", "s", "b"); !errors.Is(err, ErrMailNotConfigured) {
		t.Errorf("nothing set = %v, want ErrMailNotConfigured", err)
	}
	if bare.Configured(ctx) || bare.Source(ctx) != SourceNone {
		t.Error("an unconfigured mailer says it is configured")
	}
}

func TestAnUnreadableStoredKeyIsAnErrorNotASilentFallback(t *testing.T) {
	s, cm, fallback := mailStore(t)
	ctx := context.Background()
	_ = cm.Save(ctx, goodMail)
	moved := &ConfigMailer{Store: s, Key: MailKey("the-secret-was-changed"), Fallback: fallback}
	if err := moved.Send(ctx, "a@example.com", "s", "b"); err == nil || len(fallback.sent) != 0 {
		t.Fatalf("err %v, fallback got %d; a changed SESSION_SECRET must be loud", err, len(fallback.sent))
	}
	if moved.Configured(ctx) {
		t.Error("an unreadable login counts as configured")
	}
}

func TestSignUpAndForgotAreClosedWhileNoMailIsSetUp(t *testing.T) {
	e := newEnv(t)
	e.api.SetMailConfig(&ConfigMailer{Store: e.store, Key: MailKey("test-secret")}) // nothing set, no environment fallback
	for _, path := range []string{"/mcu-submit/register", "/mcu-submit/forgot"} {
		if rec := e.get(path); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("GET %s = %d, want 503 while mail is not set up", path, rec.Code)
		}
		if rec := e.post(path, url.Values{"email": {"x@example.com"}}); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("POST %s = %d, want 503", path, rec.Code)
		}
	}
	if rec := e.get("/mcu-submit/signin"); rec.Code != http.StatusOK {
		t.Errorf("sign-in = %d, want it open so people already registered can still get in", rec.Code)
	}
	e.api.SetMailConfig(&ConfigMailer{Store: e.store, Key: MailKey("test-secret"), Fallback: e.mail})
	if rec := e.get("/mcu-submit/register"); rec.Code != http.StatusOK {
		t.Errorf("register with a mail login = %d, want 200", rec.Code)
	}
}

func TestPortalAdminMailPageSavesAndNeverShowsTheKey(t *testing.T) {
	e := newEnv(t)
	cm := &ConfigMailer{Store: e.store, Key: MailKey("test-secret"), Fallback: e.mail}
	e.api.SetMailConfig(cm)
	e.onboard("hr@example.com", "hr.budi", "a-long-passphrase-1")
	_, member := e.signIn("hr.budi", "a-long-passphrase-1")
	_, admin := e.signIn("admin", "pelican-orbit-77")

	if rec := e.get("/mcu-submit/admin/mail", member); rec.Code != http.StatusNotFound {
		t.Fatalf("a submitter on the mail page = %d, want 404", rec.Code)
	}
	for _, p := range []string{"/mcu-submit/admin/mail", "/mcu-submit/admin/mail/test"} {
		if rec := e.post(p, url.Values{"host": {"x.example.com"}}, member); rec.Code != http.StatusNotFound {
			t.Fatalf("a submitter posting %s = %d, want 404", p, rec.Code)
		}
	}
	form := url.Values{"host": {goodMail.Host}, "port": {"587"}, "username": {goodMail.Username}, "password": {goodMail.Password}, "from": {goodMail.From}}
	if rec := e.post("/mcu-submit/admin/mail", form, admin); rec.Code != http.StatusSeeOther {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body.String())
	}
	if got, _ := cm.Settings(context.Background()); got.Host != goodMail.Host || !got.HasPassword {
		t.Fatalf("the saved settings read back as %+v", got)
	}
	body := e.get("/mcu-submit/admin/mail", admin).Body.String()
	if !strings.Contains(body, goodMail.Host) || !strings.Contains(body, goodMail.Username) || strings.Contains(body, "SECRET-KEY") {
		t.Fatal("the page lacks the saved login or leaks the key")
	}
	if rec := e.post("/mcu-submit/admin/mail", url.Values{"host": {"not a host"}, "from": {"a@example.com"}}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("bad host = %d, want 400", rec.Code)
	}
	_ = cm.Save(context.Background(), MailInput{Host: "127.0.0.1", Port: 1, From: "a@example.com"})
	if rec := e.post("/mcu-submit/admin/mail/test", url.Values{"to": {"me@example.com"}}, admin); rec.Code != http.StatusBadGateway {
		t.Errorf("test mail to a dead server = %d, want 502", rec.Code)
	}
	if rec := e.post("/mcu-submit/admin/mail/test", url.Values{"to": {"nope"}}, admin); rec.Code != http.StatusBadRequest {
		t.Errorf("test mail to a bad address = %d, want 400", rec.Code)
	}
}

func TestAnOverlongSenderIsRefusedBeforeMariaDBCanTruncateIt(t *testing.T) {
	_, cm, _ := mailStore(t)
	// A dot in the display name makes the stored form quote it, which adds two characters:
	// 253 as typed, 255 as stored. The column holds 254, so the stored length is what counts.
	in := goodMail
	in.From = "N." + strings.Repeat("N", 228) + " <no-reply@example.com>"
	if len(in.From) != 253 {
		t.Fatalf("test setup: typed length %d, want 253", len(in.From))
	}
	if err := cm.Save(context.Background(), in); err == nil {
		t.Error("a sender longer than the FromAddress column was accepted")
	}
	in.From = "N." + strings.Repeat("N", 226) + " <no-reply@example.com>" // 251 typed, 253 stored
	if err := cm.Save(context.Background(), in); err != nil {
		t.Errorf("a sender that fits the column was refused: %v", err)
	}
}
