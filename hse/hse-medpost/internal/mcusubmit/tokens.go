package mcusubmit

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"hse-medpost/internal/dates"
)

// Purposes of a one-time link.
const (
	PurposeVerify      = "Verify"      // confirm the email at registration
	PurposeSetup       = "Setup"       // choose username and password after approval
	PurposeReset       = "Reset"       // forgot password
	PurposeEmailChange = "EmailChange" // confirm a new address from Settings
)

// ErrTokenInvalid covers unknown, expired, already used and wrong-purpose links with one error, so
// a page cannot be used to learn which of those it was.
var ErrTokenInvalid = errors.New("mcusubmit: link is invalid or has expired")

// Token is what a consumed link carries.
type Token struct {
	SubmitterID int64
	NewEmail    string
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// stampOf formats t as the portal's UTC text.
func stampOf(t time.Time) string { return dates.Stamp(t) }

// IssueToken creates a one-time link valid for ttl and returns the raw token to put in the email.
// Only its sha256 is stored, so a database leak does not leak working links. 32 random bytes make
// guessing a link pointless, and the token is not derived from anything about the account.
func (s *Store) IssueToken(ctx context.Context, submitterID int64, purpose, newEmail string, ttl time.Duration) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	raw := hex.EncodeToString(buf)
	var nullEmail sql.NullString
	if newEmail != "" {
		nullEmail = sql.NullString{String: normalizeEmail(newEmail), Valid: true}
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO SubmitterToken (TokenHash, FK_SubmitterID, Purpose, NewEmail, ExpiresAt) VALUES (?, ?, ?, ?, ?)`,
		hashToken(raw), submitterID, purpose, nullEmail, stampOf(s.now().Add(ttl)))
	if err != nil {
		return "", err
	}
	return raw, nil
}

// PeekToken checks a link without using it up, so a page can show a "Confirm" button first. Email
// scanners and link previews open every link in a message; if opening it consumed the token, the
// real person would arrive at a dead link. The state change happens on the button's POST, through
// ConsumeToken.
func (s *Store) PeekToken(ctx context.Context, raw, purpose string) (Token, error) {
	var t Token
	var newEmail sql.NullString
	err := s.DB.QueryRowContext(ctx,
		`SELECT FK_SubmitterID, NewEmail FROM SubmitterToken WHERE TokenHash = ? AND Purpose = ? AND UsedAt IS NULL AND ExpiresAt > ?`,
		hashToken(raw), purpose, s.stamp()).Scan(&t.SubmitterID, &newEmail)
	if errors.Is(err, sql.ErrNoRows) {
		return Token{}, ErrTokenInvalid
	}
	t.NewEmail = newEmail.String
	return t, err
}

// ConsumeToken marks the link used and returns what it carries. Use, purpose and expiry are all in
// one UPDATE, so two requests with the same link cannot both succeed.
func (s *Store) ConsumeToken(ctx context.Context, raw, purpose string) (Token, error) {
	h := hashToken(raw)
	res, err := s.DB.ExecContext(ctx,
		`UPDATE SubmitterToken SET UsedAt = ? WHERE TokenHash = ? AND Purpose = ? AND UsedAt IS NULL AND ExpiresAt > ?`,
		s.stamp(), h, purpose, s.stamp())
	if err != nil {
		return Token{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return Token{}, ErrTokenInvalid
	}
	var t Token
	var newEmail sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT FK_SubmitterID, NewEmail FROM SubmitterToken WHERE TokenHash = ?`, h).
		Scan(&t.SubmitterID, &newEmail); err != nil {
		return Token{}, err
	}
	t.NewEmail = newEmail.String
	return t, nil
}
