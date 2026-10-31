package mcusubmit

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// TestMain drops bcrypt to its minimum cost for the whole package: the hashing is not what these
// tests check, and cost 12 made the suite take ~45 s under -race.
func TestMain(m *testing.M) {
	bcryptCost = bcrypt.MinCost
	os.Exit(m.Run())
}

func TestValidatePassword(t *testing.T) {
	cases := []struct {
		name     string
		password string
		username string
		email    string
		wantErr  bool
	}{
		{"long enough and unusual", "pelican-orbit-77", "budi.s", "budi@example.com", false},
		{"too short", "abc12", "budi.s", "budi@example.com", true},
		{"five characters is still short", "k7m2q", "budi.s", "budi@example.com", true},
		{"six characters is enough", "k7m2qx", "budi.s", "budi@example.com", false},
		{"six digits is enough when not common", "482913", "budi.s", "budi@example.com", false},
		{"a common six digit password is still refused", "123456", "budi.s", "budi@example.com", true},
		{"equals the username", "budi.s.budi.s", "budi.s.budi.s", "budi@example.com", true},
		{"equals the email", "budi@example.com", "budi.s", "budi@example.com", true},
		{"equals the username ignoring case", "Budi.S.Budi.S", "budi.s.budi.s", "x@example.com", true},
		{"common password", "password123", "budi.s", "budi@example.com", true},
		{"common password ignoring case", "Qwertyuiop", "budi.s", "budi@example.com", true},
		// bcrypt silently ignores everything after 72 bytes, so a longer password would be a lie.
		{"over bcrypt's 72 byte limit", strings.Repeat("a1", 40), "budi.s", "budi@example.com", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidatePassword(c.password, c.username, c.email)
			if (err != nil) != c.wantErr {
				t.Fatalf("ValidatePassword(%q) error = %v, wantErr %v", c.password, err, c.wantErr)
			}
		})
	}
}

func TestHashAndCheckPassword(t *testing.T) {
	hash, err := HashPassword("pelican-orbit-77")
	if err != nil {
		t.Fatal(err)
	}
	if hash == "pelican-orbit-77" {
		t.Fatal("hash must not be the plain password")
	}
	if !CheckPassword(hash, "pelican-orbit-77") {
		t.Fatal("right password rejected")
	}
	if CheckPassword(hash, "pelican-orbit-78") {
		t.Fatal("wrong password accepted")
	}
	if CheckPassword("", "pelican-orbit-77") {
		t.Fatal("an empty hash (account without a password yet) must never match")
	}
}

func TestValidUsername(t *testing.T) {
	good := []string{"abc", "budi.s", "hr_pt.", "A1b2c3", strings.Repeat("a", 40)}
	// The plan allows letters, digits, dot and underscore only; "admin" belongs to the built-in account.
	bad := []string{"", "ab", "has space", "semi;colon", "dash-ed", "emoji😀", strings.Repeat("a", 41), "admin", "ADMIN"}
	for _, u := range good {
		if err := ValidUsername(u); err != nil {
			t.Errorf("ValidUsername(%q) = %v, want ok", u, err)
		}
	}
	for _, u := range bad {
		if err := ValidUsername(u); err == nil {
			t.Errorf("ValidUsername(%q) accepted, want error", u)
		}
	}
}

func TestOperatorPasswordAllowsCommonButNotShort(t *testing.T) {
	if err := ValidateOperatorPassword("101112", "admin", "a@b.c"); err != nil {
		t.Fatalf("101112 should be allowed for the built-in admin: %v", err)
	}
	if err := ValidatePassword("101112", "bob", "b@x.y"); err == nil {
		t.Fatal("101112 must stay refused for portal users")
	}
	if err := ValidateOperatorPassword("12345", "admin", ""); err == nil {
		t.Fatal("five characters must be refused")
	}
	if err := ValidateOperatorPassword("admin", "admin", ""); err == nil {
		t.Fatal("the username must be refused")
	}
}
