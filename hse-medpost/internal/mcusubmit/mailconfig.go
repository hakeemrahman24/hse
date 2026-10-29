package mcusubmit

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
)

// The outgoing mail login (Brevo's SMTP relay) is edited by the portal admin on the portal's own
// Mail settings page (/mcu-submit/admin/mail) and kept in McuMailSetting. The server's MCU_SMTP_* variables stay as a
// fallback for a machine where nobody has used the page yet. When the page has a login it wins,
// and a broken page login is an error: quietly falling back to another account would hide the
// mistake and send from the wrong sender.

// ErrMailNotConfigured means neither the page nor the environment has a mail login.
var ErrMailNotConfigured = errors.New("mcusubmit: mail is not set up")

// MailSettingError is a refused value, worded for the admin who typed it.
type MailSettingError string

func (e MailSettingError) Error() string { return string(e) }

// Where the mail login in use comes from.
const (
	SourcePage        = "the settings page"
	SourceEnvironment = "the server's environment variables"
	SourceNone        = "nowhere"
)

// MailConfig is what the page may show. The SMTP key is never part of it, only whether one is stored.
type MailConfig struct {
	Host, Username, From string
	Port                 int
	HasPassword          bool
}

// MailInput is what the admin typed. An empty Password keeps the stored key.
type MailInput struct {
	Host, Username, Password, From string
	Port                           int
}

// ConfigMailer is the Mailer the portal uses. It reads the login from the database on every send,
// so a change on the page takes effect at once, with no restart.
type ConfigMailer struct {
	Store    *Store
	Key      []byte // from MailKey
	Fallback Mailer // the MCU_SMTP_* mailer or the development log mailer; may be nil
}

// MailKey derives the key that seals the SMTP key from the server's session secret, with its own
// label, so it is a different key from the one that signs cookies.
func MailKey(sessionSecret string) []byte {
	h := hmac.New(sha256.New, []byte(sessionSecret))
	h.Write([]byte("mcu-mail-settings"))
	return h.Sum(nil)
}

// sealSecret encrypts with AES-256-GCM and a fresh random nonce; the result is base64(nonce+data).
func sealSecret(key []byte, plain string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plain), nil)), nil
}

// openSecret reverses sealSecret. A wrong key or a changed value fails: GCM authenticates the data.
func openSecret(key []byte, sealed string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("mcusubmit: stored mail key is too short")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	return string(plain), err
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

type mailRow struct {
	host, user, enc, from string
	port                  int
}

func (c *ConfigMailer) row(ctx context.Context) (mailRow, error) {
	var r mailRow
	err := c.Store.DB.QueryRowContext(ctx,
		`SELECT Host, Port, Username, PasswordEnc, FromAddress FROM McuMailSetting WHERE McuMailSettingID = 1`).
		Scan(&r.host, &r.port, &r.user, &r.enc, &r.from)
	if errors.Is(err, sql.ErrNoRows) {
		return mailRow{}, nil
	}
	return r, err
}

// Settings reads what the page shows.
func (c *ConfigMailer) Settings(ctx context.Context) (MailConfig, error) {
	r, err := c.row(ctx)
	return MailConfig{Host: r.host, Port: r.port, Username: r.user, From: r.from, HasPassword: r.enc != ""}, err
}

var hostName = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

// Save checks and stores the settings. Every value ends up in a network dialogue or a mail header,
// so anything that could carry a line break, a scheme or a port inside the host is refused.
func (c *ConfigMailer) Save(ctx context.Context, in MailInput) error {
	in.Host, in.Username, in.From = strings.TrimSpace(in.Host), strings.TrimSpace(in.Username), strings.TrimSpace(in.From)
	if !hostName.MatchString(in.Host) {
		return MailSettingError("Enter the mail server name only, for example smtp-relay.brevo.com (no https:// and no port).")
	}
	if in.Port == 0 {
		in.Port = 587
	}
	if in.Port < 1 || in.Port > 65535 {
		return MailSettingError("The port must be a number from 1 to 65535 (Brevo uses 587).")
	}
	if len(in.Username) > 254 || strings.ContainsAny(in.Username, "\r\n\x00") {
		return MailSettingError("The SMTP login is not valid.")
	}
	if len(in.Password) > 200 || strings.ContainsAny(in.Password, "\r\n\x00") {
		return MailSettingError("The SMTP key is not valid.")
	}
	addr, err := mail.ParseAddress(in.From)
	// The normalised form is what is stored (it can add quotes), so its length must fit FromAddress.
	if err != nil || len(addr.String()) > 254 || strings.ContainsAny(in.From, "\r\n") {
		return MailSettingError("Enter the sender address, for example no-reply@yourcompany.com or Medpost <no-reply@yourcompany.com>.")
	}
	current, err := c.row(ctx)
	if err != nil {
		return err
	}
	enc := current.enc
	if in.Password != "" {
		if enc, err = sealSecret(c.Key, in.Password); err != nil {
			return err
		}
	}
	if in.Username != "" && enc == "" {
		return MailSettingError("Enter the SMTP key that goes with this login.")
	}
	// A plain UPDATE, not oneRow: MariaDB reports 0 changed rows when nothing differs.
	_, err = c.Store.DB.ExecContext(ctx,
		`UPDATE McuMailSetting SET Host = ?, Port = ?, Username = ?, PasswordEnc = ?, FromAddress = ?, UpdatedAt = ?
		 WHERE McuMailSettingID = 1`, in.Host, in.Port, in.Username, enc, addr.String(), c.Store.stamp())
	return err
}

// pageMailer builds the SMTP sender from the page's settings. found is false when the page has none.
func (c *ConfigMailer) pageMailer(ctx context.Context) (m *SMTPMailer, found bool, err error) {
	r, err := c.row(ctx)
	if err != nil || r.host == "" {
		return nil, false, err
	}
	m = &SMTPMailer{Host: r.host, Port: r.port, Username: r.user, From: r.from}
	if r.enc != "" {
		if m.Password, err = openSecret(c.Key, r.enc); err != nil {
			return nil, true, errors.New("mcusubmit: the stored mail key cannot be read (was SESSION_SECRET changed?): enter it again on the portal's Mail settings page")
		}
	}
	return m, true, nil
}

// Send uses the page's login if there is one, otherwise the environment's, otherwise fails.
func (c *ConfigMailer) Send(ctx context.Context, to, subject, body string) error {
	m, found, err := c.pageMailer(ctx)
	if err != nil {
		return err
	}
	if found {
		return m.Send(ctx, to, subject, body)
	}
	if c.Fallback == nil {
		return ErrMailNotConfigured
	}
	return c.Fallback.Send(ctx, to, subject, body)
}

// Source says where the login in use comes from.
func (c *ConfigMailer) Source(ctx context.Context) string {
	if _, found, err := c.pageMailer(ctx); found {
		if err != nil {
			return SourceNone
		}
		return SourcePage
	}
	if c.Fallback != nil {
		return SourceEnvironment
	}
	return SourceNone
}

// Configured is true when a send could work: a readable page login, or an environment fallback.
func (c *ConfigMailer) Configured(ctx context.Context) bool { return c.Source(ctx) != SourceNone }

// ---- portal side -------------------------------------------------------------------------------

// SetMailConfig makes the portal send through cm. Until mail is set up, sign-up and "forgot
// password" stay closed, because a form that promises an email it cannot send must not be open.
func (a *API) SetMailConfig(cm *ConfigMailer) { a.mailCfg, a.mailer = cm, cm }

// signUpClosed answers 503 and reports true while no mail login exists anywhere.
func (a *API) signUpClosed(w http.ResponseWriter, r *http.Request) bool {
	if a.mailCfg == nil || a.mailCfg.Configured(r.Context()) {
		return false
	}
	a.message(w, http.StatusServiceUnavailable, "Sign-up is closed",
		"The administrator has not set up the portal's email yet. Please try again later.", nil)
	return true
}

// ---- portal admin page -------------------------------------------------------------------------

// The only place the login is edited: the portal admin's Mail settings page.

func (a *API) mailPage(w http.ResponseWriter, r *http.Request, status int, admin *Submitter, form map[string]string, errMsg, notice string) {
	if a.mailCfg == nil {
		a.notFound(w, admin)
		return
	}
	cfg, err := a.mailCfg.Settings(r.Context())
	if err != nil {
		a.serverError(w, "mail settings", err)
		return
	}
	p := a.newPage("Mail settings")
	p.User, p.MailCfg, p.MailSource, p.Error, p.Notice = admin, cfg, a.mailCfg.Source(r.Context()), errMsg, notice
	if form == nil {
		form = map[string]string{"host": cfg.Host, "port": strconv.Itoa(cfg.Port), "username": cfg.Username, "from": cfg.From}
	}
	p.Form = form
	a.render(w, status, "mail.html", p)
}

// MailPage shows the mail settings. Admin only.
func (a *API) MailPage(w http.ResponseWriter, r *http.Request) {
	if admin := a.requireAdmin(w, r); admin != nil {
		a.mailPage(w, r, http.StatusOK, admin, nil, "", "")
	}
}

// MailSave stores the settings; the SMTP key is never sent back to the browser.
func (a *API) MailSave(w http.ResponseWriter, r *http.Request) {
	admin := a.requireAdmin(w, r)
	if admin == nil {
		return
	}
	if a.mailCfg == nil || !parseForm(w, r) {
		a.mailPage(w, r, http.StatusBadRequest, admin, nil, "The form could not be read.", "")
		return
	}
	port, _ := strconv.Atoi(strings.TrimSpace(r.PostFormValue("port"))) // not a number becomes 0, then 587
	in := MailInput{Host: r.PostFormValue("host"), Port: port, Username: r.PostFormValue("username"), Password: r.PostFormValue("password"), From: r.PostFormValue("from")}
	if err := a.mailCfg.Save(r.Context(), in); err != nil {
		var refused MailSettingError
		if errors.As(err, &refused) {
			form := map[string]string{"host": in.Host, "port": r.PostFormValue("port"), "username": in.Username, "from": in.From}
			a.mailPage(w, r, http.StatusBadRequest, admin, form, refused.Error(), "")
			return
		}
		a.serverError(w, "save mail settings", err)
		return
	}
	http.Redirect(w, r, basePath+"/admin/mail", http.StatusSeeOther)
}

// MailTest sends one message to an address the admin types.
func (a *API) MailTest(w http.ResponseWriter, r *http.Request) {
	admin := a.requireAdmin(w, r)
	if admin == nil {
		return
	}
	if a.mailCfg == nil || !parseForm(w, r) {
		a.mailPage(w, r, http.StatusBadRequest, admin, nil, "The form could not be read.", "")
		return
	}
	to, err := ParseEmail(r.PostFormValue("to"))
	if err != nil {
		a.mailPage(w, r, http.StatusBadRequest, admin, nil, err.Error(), "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), mailTimeout)
	defer cancel()
	if err := a.mailCfg.Send(ctx, to, "Test mail from the checkup portal", "If you can read this, the portal can send email.\n"); err != nil {
		a.mailPage(w, r, http.StatusBadGateway, admin, nil, "The test mail was not sent: "+err.Error(), "")
		return
	}
	a.mailPage(w, r, http.StatusOK, admin, nil, "", "Test mail sent to "+to+" (using "+a.mailCfg.Source(r.Context())+").")
}
