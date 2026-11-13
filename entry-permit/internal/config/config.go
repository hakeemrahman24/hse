// Package config reads the server settings from environment variables.
//
// WHY env vars and not the Settings table: these are deployment facts and
// secrets (database password, API tokens). Business numbers live in the
// Settings table so the admin can change them; secrets never do (plan 3a-2).
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const (
	devSessionSecret = "dev-session-secret-change-me"
	// defaultPort: next free port after hse-medpost's 8113 (confirm on the Pi).
	defaultPort = "8114"
)

// Config holds every deployment setting.
type Config struct {
	Port         string
	DBDriver     string // sqlite (dev default) or mysql (MariaDB, production)
	DatabasePath string
	DBHost       string
	DBPort       string
	DBUser       string
	DBPassword   string
	DBName       string

	UploadDir     string
	WebDir        string // templates and static files, read from disk at start
	SessionSecret string
	BaseURL       string

	// hse-medpost directory API (loopback, bearer token).
	MedpostURL string
	// McuPortalURL is the public address of the medical checkup submission portal (hse-medpost's
	// /mcu-submit), linked from the landing page. Empty: the landing page explains it but links nowhere.
	McuPortalURL string
	MedpostToken string
	RegionTSV    string // dev/offline fallback for the regency list; production uses the medpost API

	// Cloudflare Turnstile guards the public "My Layar Entry Permit" lookup.
	// Both keys empty and TurnstileDev off: the lookup is switched off (it fails closed).
	TurnstileSiteKey string
	TurnstileSecret  string
	TurnstileDev     bool // accepts the token "dev-pass"; refused in production
	// TurnstileHostnames (TURNSTILE_HOSTNAMES, comma separated) are the page hostnames whose tokens
	// are accepted. Empty: any hostname the widget allows.
	TurnstileHostnames []string
	TrustCFIP          bool // read the client address from CF-Connecting-IP (behind Cloudflare)

	// SMTP relay (Brevo) for e-mail. All empty: mail stays in the outbox, unsent.
	// The password lives only here, never in Settings.
	SMTPHost, SMTPPort, SMTPUser, SMTPPassword string
	MailFrom, MailFromName                     string

	// Cloudflare gate Worker and the shared secret used by gatesync.
	GateURL    string
	GateSecret string
}

// Load builds a Config from env (pass os.Getenv; tests pass a map lookup).
func Load(env func(string) string) Config {
	get := func(key, fallback string) string {
		if v := strings.TrimSpace(env(key)); v != "" {
			return v
		}
		return fallback
	}
	driver := strings.ToLower(get("DB_DRIVER", "sqlite"))
	if driver == "mariadb" {
		driver = "mysql"
	}
	return Config{
		Port:               get("PORT", defaultPort),
		DBDriver:           driver,
		DatabasePath:       get("DATABASE_PATH", "./data/entry_permit.db"),
		DBHost:             get("DB_HOST", "127.0.0.1"),
		DBPort:             get("DB_PORT", "3306"),
		DBUser:             get("DB_USER", "entry_permit_user"),
		DBPassword:         get("DB_PASSWORD", ""),
		DBName:             get("DB_NAME", "entry_permit"),
		UploadDir:          get("UPLOAD_DIR", "./data/uploads"),
		WebDir:             get("WEB_DIR", "web"),
		SessionSecret:      get("SESSION_SECRET", devSessionSecret),
		BaseURL:            get("BASE_URL", "http://127.0.0.1:"+get("PORT", defaultPort)),
		MedpostURL:         get("MEDPOST_API_URL", ""),
		MedpostToken:       get("MEDPOST_API_TOKEN", ""),
		McuPortalURL:       absoluteHTTP(get("MCU_PORTAL_URL", "")),
		RegionTSV:          get("REGION_TSV", ""),
		TurnstileSiteKey:   get("TURNSTILE_SITEKEY", ""),
		TurnstileSecret:    get("TURNSTILE_SECRET", ""),
		TurnstileDev:       get("TURNSTILE_DEV", "") == "1",
		TurnstileHostnames: splitList(get("TURNSTILE_HOSTNAMES", "")),
		TrustCFIP:          get("TRUST_CF_IP", "") == "1",
		SMTPHost:           get("SMTP_HOST", ""),
		SMTPPort:           get("SMTP_PORT", "587"),
		SMTPUser:           get("SMTP_USER", ""),
		SMTPPassword:       get("SMTP_PASSWORD", ""),
		MailFrom:           get("MAIL_FROM", ""),
		MailFromName:       get("MAIL_FROM_NAME", "Medical Project LNG"),
		GateURL:            get("GATE_WORKER_URL", ""),
		GateSecret:         get("GATE_SHARED_SECRET", ""),
	}
}

// UseMySQL reports whether the production database is selected.
func (c Config) UseMySQL() bool { return c.DBDriver == "mysql" }

// splitList turns "a, b,,c" into ["a" "b" "c"]: trimmed, empty items dropped, nil when empty.
func splitList(s string) []string {
	var out []string
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// Validate refuses to start production with development defaults.
func (c Config) Validate() error {
	if c.DBDriver != "sqlite" && c.DBDriver != "mysql" {
		return fmt.Errorf("DB_DRIVER %q is not sqlite or mysql", c.DBDriver)
	}
	if !c.UseMySQL() {
		return nil
	}
	var missing []string
	if c.SessionSecret == devSessionSecret {
		missing = append(missing, "SESSION_SECRET")
	}
	if c.DBPassword == "" {
		missing = append(missing, "DB_PASSWORD")
	}
	if len(missing) > 0 {
		return errors.New("production needs real values for: " + strings.Join(missing, ", "))
	}
	if c.TurnstileDev {
		return errors.New("TURNSTILE_DEV must be off in production")
	}
	return nil
}

// DSN is the database/sql connection string for the selected driver.
func (c Config) DSN() string {
	if !c.UseMySQL() {
		return c.DatabasePath + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	}
	user := url.User(c.DBUser)
	if c.DBPassword != "" {
		user = url.UserPassword(c.DBUser, c.DBPassword)
	}
	// WHY parseTime=false: days and moments are text (plan 2b), never time.Time.
	// WHY clientFoundRows=true: an UPDATE then reports the rows it matched (as SQLite does), not
	// only the rows whose value changed. Seed and the review and outbox updates rely on that.
	return fmt.Sprintf("%s@tcp(%s:%s)/%s?charset=utf8mb4&collation=utf8mb4_unicode_ci&parseTime=false&timeout=10s&clientFoundRows=true",
		user.String(), c.DBHost, c.DBPort, c.DBName)
}

// absoluteHTTP returns u without a trailing slash when it is an absolute http(s) address, else "".
// WHY: the landing page links straight to it; a bare host name would become a relative link.
func absoluteHTTP(u string) string {
	if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		return ""
	}
	return strings.TrimRight(u, "/")
}
