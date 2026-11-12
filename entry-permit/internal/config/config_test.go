package config

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	c := Load(env(nil))
	if c.Port != "8114" || c.DBDriver != "sqlite" || c.DBName != "entry_permit" || c.DBUser != "entry_permit_user" {
		t.Errorf("%+v", c)
	}
	if c.BaseURL != "http://127.0.0.1:8114" {
		t.Errorf("BaseURL=%q", c.BaseURL)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("dev defaults must be valid: %v", err)
	}
}

func TestLoadOverridesAndMariaDBAlias(t *testing.T) {
	c := Load(env(map[string]string{"PORT": " 9000 ", "DB_DRIVER": "MariaDB", "MEDPOST_API_URL": "http://127.0.0.1:8113"}))
	if c.Port != "9000" || !c.UseMySQL() || c.MedpostURL != "http://127.0.0.1:8113" {
		t.Errorf("%+v", c)
	}
	if c.BaseURL != "http://127.0.0.1:9000" {
		t.Errorf("BaseURL follows PORT: %q", c.BaseURL)
	}
}

func TestValidate(t *testing.T) {
	if err := Load(env(map[string]string{"DB_DRIVER": "oracle"})).Validate(); err == nil {
		t.Error("unknown driver accepted")
	}
	err := Load(env(map[string]string{"DB_DRIVER": "mysql"})).Validate()
	if err == nil || !strings.Contains(err.Error(), "SESSION_SECRET") || !strings.Contains(err.Error(), "DB_PASSWORD") {
		t.Errorf("production defaults accepted: %v", err)
	}
	ok := Load(env(map[string]string{"DB_DRIVER": "mysql", "SESSION_SECRET": "s3", "DB_PASSWORD": "pw"}))
	if err := ok.Validate(); err != nil {
		t.Errorf("valid production refused: %v", err)
	}
}

func TestDSN(t *testing.T) {
	if got := Load(env(nil)).DSN(); !strings.HasPrefix(got, "./data/entry_permit.db?") || !strings.Contains(got, "foreign_keys(1)") {
		t.Errorf("sqlite dsn %q", got)
	}
	got := Load(env(map[string]string{"DB_DRIVER": "mysql", "DB_PASSWORD": "p@ss/w"})).DSN()
	if !strings.Contains(got, "@tcp(127.0.0.1:3306)/entry_permit?") || !strings.Contains(got, "parseTime=false") || strings.Contains(got, "p@ss/w") {
		t.Errorf("mysql dsn %q (password must be URL-escaped)", got)
	}
	if got := Load(env(map[string]string{"DB_DRIVER": "mysql"})).DSN(); !strings.HasPrefix(got, "entry_permit_user@tcp(") {
		t.Errorf("no-password dsn %q", got)
	}
}

// MariaDB's UPDATE reports rows it changed, SQLite's the rows it matched. The code (Seed, the
// review and outbox updates) means "matched", so the MySQL driver must be told to count that way;
// without it a restart that changes nothing re-inserts a setting and fails on its primary key.
func TestMySQLDSNCountsMatchedRowsLikeSQLite(t *testing.T) {
	got := Load(env(map[string]string{"DB_DRIVER": "mysql"})).DSN()
	if !strings.Contains(got, "clientFoundRows=true") {
		t.Errorf("mysql dsn %q must set clientFoundRows=true", got)
	}
}

func TestTurnstileHostnamesAreAnOptionalTrimmedList(t *testing.T) {
	c := Load(env(map[string]string{"TURNSTILE_HOSTNAMES": " permit.example.com, ,test.example.net "}))
	if got := strings.Join(c.TurnstileHostnames, "|"); got != "permit.example.com|test.example.net" {
		t.Errorf("TurnstileHostnames = %q", got)
	}
	if got := Load(env(nil)).TurnstileHostnames; len(got) != 0 {
		t.Errorf("unset must mean no list, got %v", got)
	}
}

func TestTurnstileSettingsAndProductionGuard(t *testing.T) {
	env := map[string]string{"TURNSTILE_SITEKEY": "site", "TURNSTILE_SECRET": "sec", "TRUST_CF_IP": "1", "TURNSTILE_DEV": "1"}
	c := Load(func(k string) string { return env[k] })
	if c.TurnstileSiteKey != "site" || c.TurnstileSecret != "sec" || !c.TrustCFIP || !c.TurnstileDev {
		t.Fatalf("config = %+v", c)
	}
	if err := c.Validate(); err != nil { // sqlite dev: allowed
		t.Fatalf("dev: %v", err)
	}
	env["DB_DRIVER"], env["SESSION_SECRET"], env["DB_PASSWORD"] = "mysql", "real-secret", "pw"
	if err := Load(func(k string) string { return env[k] }).Validate(); err == nil {
		t.Fatal("production accepted TURNSTILE_DEV")
	}
	env["TURNSTILE_DEV"] = ""
	if err := Load(func(k string) string { return env[k] }).Validate(); err != nil {
		t.Fatalf("production without dev: %v", err)
	}
}
