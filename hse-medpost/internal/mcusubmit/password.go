// Package mcusubmit is the public medical checkup submission portal (/mcu-submit): accounts for
// outside submitters, their one-time links, the portal admin, and the audit trail.
// Design: plan/mcu-submission-plan.md.
package mcusubmit

import (
	"errors"
	"regexp"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const (
	minPasswordLen = 10
	// bcrypt only reads the first 72 bytes. A longer password would be accepted but silently
	// truncated, so two different long passwords could match; refuse it instead.
	maxPasswordBytes = 72
)

// bcryptCost is 12, not the library default 10: this login faces the internet (the staff 6-digit
// pattern is deliberately not reused, plan section 4). One hash is ~250 ms on the Pi's class of
// CPU. It is a variable only so the tests can use the minimum cost and finish in seconds.
var bcryptCost = 12

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._]{3,40}$`)

// commonPasswords is a short list of passwords that pass the length rule but are guessed first.
// It is not a substitute for a breach list; it stops the obvious ones.
var commonPasswords = map[string]bool{
	"password123": true, "password1234": true, "password12345": true, "1234567890": true,
	"0123456789": true, "qwertyuiop": true, "1q2w3e4r5t": true, "iloveyou123": true,
	"admin12345": true, "administrator": true, "welcome123": true, "letmein123": true,
	"passw0rd123": true, "abcdefghij": true, "1111111111": true, "0000000000": true,
	"qwerty12345": true, "password01": true, "changeme123": true,
}

// ValidatePassword applies the portal's password rules (plan section 4): at least 10 characters,
// at most 72 bytes, not the username or email, and not a very common password.
func ValidatePassword(password, username, email string) error {
	if len([]rune(password)) < minPasswordLen {
		return errors.New("password must be at least 10 characters")
	}
	if len(password) > maxPasswordBytes {
		return errors.New("password must be at most 72 bytes")
	}
	lower := strings.ToLower(password)
	if username != "" && lower == strings.ToLower(username) {
		return errors.New("password must not be the same as the username")
	}
	if email != "" && lower == strings.ToLower(email) {
		return errors.New("password must not be the same as the email")
	}
	if commonPasswords[lower] {
		return errors.New("password is too common")
	}
	return nil
}

// HashPassword returns a bcrypt hash of password.
func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// CheckPassword reports whether password matches hash. An empty hash (an account that has no
// password yet) never matches.
func CheckPassword(hash, password string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// ValidUsername checks the 3 to 40 letters, digits, dot and underscore rule. "admin" is reserved
// for the built-in portal admin, so a submitter can never choose it.
func ValidUsername(u string) error {
	if !usernamePattern.MatchString(u) {
		return errors.New("username must be 3 to 40 letters, digits, dots or underscores")
	}
	if strings.EqualFold(u, builtInUsername) {
		return errors.New("that username is reserved")
	}
	return nil
}
