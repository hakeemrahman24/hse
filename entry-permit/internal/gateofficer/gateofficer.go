// Package gateofficer keeps the list of gate officers and their PINs.
//
// The scanner page asks the officer to pick their name from a searchable list
// and type their PIN, so a PIN only has to be right for that one name: two
// officers may have the same PIN, and nothing here forces PINs to be unique.
//
// WHY the PIN is stored only as a salted PBKDF2-SHA256 verifier: the old
// system kept officer codes in plain text on the gate Worker (gate rules
// document). bcrypt is too heavy for Worker CPU limits, so the verifier is
// PBKDF2 (WebCrypto has it), and the iteration count is part of the stored
// text so it can be raised later without breaking old verifiers. Archiving
// keeps the record; removing deletes the row for good.
package gateofficer

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"entry-permit/internal/audit"
	"entry-permit/internal/dates"
)

const (
	defaultIterations = 100_000
	saltBytes         = 16
	keyBytes          = 32
	scheme            = "pbkdf2-sha256"
)

// Errors with messages safe to show the admin.
var (
	ErrName              = errors.New("Name is required (up to 120 characters).")
	ErrPIN               = errors.New("PIN must be 4 to 8 letters or digits.")
	ErrUsername          = errors.New("Username must be 2 to 40 lower-case letters, digits, dot, dash or underscore.")
	ErrDuplicateName     = errors.New("An officer with that name already exists.")
	ErrDuplicateUsername = errors.New("That username is already used by another officer.")
	ErrNotFound          = errors.New("That officer no longer exists.")
	// ErrSuspicious stops an eligibility sweep that would archive everyone.
	ErrSuspicious = errors.New("every officer looks ineligible; not archiving anyone (the employee list may be wrong)")
)

var usernameRe = regexp.MustCompile(`^[a-z0-9._-]{2,40}$`)

var pinRe = regexp.MustCompile(`^[A-Za-z0-9]{4,8}$`)

// Officer is one row, without the verifier.
type Officer struct {
	ID       int64
	Name     string // the worker's full name, picked from the eligible employees
	Username string // given by the gate admin, lower case
	Archived bool
}

// Store reads and writes officers. Every write is audited in its transaction.
type Store struct {
	db    *sql.DB
	clock dates.Clock
	iter  int
}

// New builds a Store.
func New(db *sql.DB, clock dates.Clock) *Store {
	return &Store{db: db, clock: clock, iter: defaultIterations}
}

// WithIterations lowers the PBKDF2 cost (tests only).
func (s *Store) WithIterations(n int) *Store { s.iter = n; return s }

func (s *Store) hash(code string) (string, error) {
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("salt: %w", err)
	}
	key, err := pbkdf2.Key(sha256.New, code, salt, s.iter, keyBytes)
	if err != nil {
		return "", fmt.Errorf("derive key: %w", err)
	}
	return fmt.Sprintf("%s$%d$%s$%s", scheme, s.iter, hex.EncodeToString(salt), hex.EncodeToString(key)), nil
}

// Verify checks a code against a stored verifier in constant time.
// Codes are case-sensitive.
func Verify(code, stored string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != scheme {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 {
		return false
	}
	salt, err1 := hex.DecodeString(parts[2])
	want, err2 := hex.DecodeString(parts[3])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, code, salt, iter, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

func cleanUsername(u string) (string, error) {
	u = strings.ToLower(strings.TrimSpace(u))
	if !usernameRe.MatchString(u) {
		return "", ErrUsername
	}
	return u, nil
}

func usernameTaken(ctx context.Context, tx *sql.Tx, username string, exceptID int64) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gate_officer WHERE LOWER(username)=? AND id<>?`, username, exceptID).Scan(&n)
	return n > 0, err
}

func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 120 {
		return "", ErrName
	}
	return name, nil
}

func nameTaken(ctx context.Context, tx *sql.Tx, name string, exceptID int64) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gate_officer WHERE LOWER(name)=LOWER(?) AND id<>?`, name, exceptID).Scan(&n)
	return n > 0, err
}

func (s *Store) tx(ctx context.Context, fn func(tx *sql.Tx) error) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Add creates an active officer. The PIN is never returned or logged.
func (s *Store) Add(ctx context.Context, actor, name, username, pin string) (Officer, error) {
	name, err := cleanName(name)
	if err != nil {
		return Officer{}, err
	}
	username, err = cleanUsername(username)
	if err != nil {
		return Officer{}, err
	}
	if !pinRe.MatchString(pin) {
		return Officer{}, ErrPIN
	}
	var o Officer
	err = s.tx(ctx, func(tx *sql.Tx) error {
		if dup, err := nameTaken(ctx, tx, name, 0); err != nil {
			return err
		} else if dup {
			return ErrDuplicateName
		}
		if dup, err := usernameTaken(ctx, tx, username, 0); err != nil {
			return err
		} else if dup {
			return ErrDuplicateUsername
		}
		h, err := s.hash(pin)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		res, err := tx.ExecContext(ctx, `INSERT INTO gate_officer (name, username, code_hash, status, created_at, updated_at) VALUES (?, ?, ?, 'Active', ?, ?)`,
			name, username, h, string(now), string(now))
		if err != nil {
			return fmt.Errorf("insert officer: %w", err)
		}
		o.ID, _ = res.LastInsertId()
		o.Name, o.Username = name, username
		return audit.Write(ctx, tx, now, actor, "gate_officer.add", "gate_officer:"+name, "", "")
	})
	return o, err
}

// current loads an officer's name inside tx.
func current(ctx context.Context, tx *sql.Tx, id int64) (string, error) {
	var name string
	err := tx.QueryRowContext(ctx, `SELECT name FROM gate_officer WHERE id=?`, id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return name, err
}

// SetPIN replaces an officer's PIN.
func (s *Store) SetPIN(ctx context.Context, actor string, id int64, pin string) error {
	if !pinRe.MatchString(pin) {
		return ErrPIN
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		name, err := current(ctx, tx, id)
		if err != nil {
			return err
		}
		h, err := s.hash(pin)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		if _, err := tx.ExecContext(ctx, `UPDATE gate_officer SET code_hash=?, updated_at=? WHERE id=?`, h, string(now), id); err != nil {
			return fmt.Errorf("save pin: %w", err)
		}
		return audit.Write(ctx, tx, now, actor, "gate_officer.pin", "gate_officer:"+name, "", "")
	})
}

// SetUsername changes an officer's username.
func (s *Store) SetUsername(ctx context.Context, actor string, id int64, username string) error {
	username, err := cleanUsername(username)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		name, err := current(ctx, tx, id)
		if err != nil {
			return err
		}
		var old string
		if err := tx.QueryRowContext(ctx, `SELECT username FROM gate_officer WHERE id=?`, id).Scan(&old); err != nil {
			return fmt.Errorf("read username: %w", err)
		}
		if old == username {
			return nil
		}
		if dup, err := usernameTaken(ctx, tx, username, id); err != nil {
			return err
		} else if dup {
			return ErrDuplicateUsername
		}
		now := s.clock.Now()
		if _, err := tx.ExecContext(ctx, `UPDATE gate_officer SET username=?, updated_at=? WHERE id=?`, username, string(now), id); err != nil {
			return fmt.Errorf("save username: %w", err)
		}
		return audit.Write(ctx, tx, now, actor, "gate_officer.username", "gate_officer:"+name, old, username)
	})
}

// SetArchived archives or restores an officer.
func (s *Store) SetArchived(ctx context.Context, actor string, id int64, archived bool) error {
	status, action := "Active", "gate_officer.restore"
	if archived {
		status, action = "Archived", "gate_officer.archive"
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		name, err := current(ctx, tx, id)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		if _, err := tx.ExecContext(ctx, `UPDATE gate_officer SET status=?, updated_at=? WHERE id=?`, status, string(now), id); err != nil {
			return fmt.Errorf("set status: %w", err)
		}
		return audit.Write(ctx, tx, now, actor, action, "gate_officer:"+name, "", status)
	})
}

// Remove deletes an officer for good (the audit row stays).
func (s *Store) Remove(ctx context.Context, actor string, id int64) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		name, err := current(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM gate_officer WHERE id=?`, id); err != nil {
			return fmt.Errorf("delete officer: %w", err)
		}
		return audit.Write(ctx, tx, s.clock.Now(), actor, "gate_officer.remove", "gate_officer:"+name, name, "")
	})
}

// List returns officers by name; archived ones only when asked.
func (s *Store) List(ctx context.Context, includeArchived bool) ([]Officer, error) {
	q := `SELECT id, name, username, status FROM gate_officer`
	if !includeArchived {
		q += ` WHERE status='Active'`
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list officers: %w", err)
	}
	defer rows.Close()
	var out []Officer
	for rows.Next() {
		var o Officer
		var status string
		if err := rows.Scan(&o.ID, &o.Name, &o.Username, &status); err != nil {
			return nil, fmt.Errorf("scan officer: %w", err)
		}
		o.Archived = status == "Archived"
		out = append(out, o)
	}
	return out, rows.Err()
}

// Verifier is what the gate Worker is sent for one officer: the name to pick and
// the salted PBKDF2 verifier of the PIN. The PIN itself is never stored or sent.
type Verifier struct{ Name, Hash string }

// Verifiers returns every active officer's name and verifier, for the gate sync.
// Archived officers are left out, so archiving someone removes them from the gate
// at the next sync.
func (s *Store) Verifiers(ctx context.Context) ([]Verifier, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, code_hash FROM gate_officer WHERE status='Active' ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list officer verifiers: %w", err)
	}
	defer rows.Close()
	var out []Verifier
	for rows.Next() {
		var v Verifier
		if err := rows.Scan(&v.Name, &v.Hash); err != nil {
			return nil, fmt.Errorf("scan officer verifier: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// suspiciousFrom is the number of active officers from which "everyone is
// ineligible" is treated as a bad answer from the employee list rather than as
// that many expired MCUs on the same day.
const suspiciousFrom = 3

// ArchiveIneligible archives every active officer whose name check says they
// are no longer an eligible worker, as the actor "system", and returns the
// names it archived. It is all or nothing on doubt: if any check fails (the
// employee list is unreachable) or the answer would archive every officer of
// a group of suspiciousFrom or more, nobody is archived. It never restores
// anyone; coming back is the gate admin's decision.
func (s *Store) ArchiveIneligible(ctx context.Context, eligible func(ctx context.Context, name string) (bool, error)) ([]string, error) {
	active, err := s.List(ctx, false)
	if err != nil {
		return nil, err
	}
	var gone []Officer
	for _, o := range active {
		ok, err := eligible(ctx, o.Name)
		if err != nil {
			return nil, fmt.Errorf("check %q: %w", o.Name, err)
		}
		if !ok {
			gone = append(gone, o)
		}
	}
	if len(gone) == len(active) && len(active) >= suspiciousFrom {
		return nil, ErrSuspicious
	}
	var names []string
	for _, o := range gone {
		if err := s.SetArchived(ctx, "system", o.ID, true); err != nil {
			return names, err
		}
		names = append(names, o.Name)
	}
	return names, nil
}
