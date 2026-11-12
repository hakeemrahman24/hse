// Package staff holds staff accounts, roles, PIN login and lockout.
//
// WHY a short PIN: owner decision (not all users are tech friendly). Because
// a short secret is weak, every account locks after a few wrong tries and the
// PIN is stored only as a bcrypt hash, never in plain text.
package staff

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"entry-permit/internal/dates"
	"entry-permit/internal/policy"
)

// Role is one thing a staff member may do.
type Role string

const (
	Admin       Role = "admin"
	GateAdmin   Role = "gate_admin" // limited admin: gate officers, gates, purpose area only
	Final       Role = "final"
	Stage1      Role = "stage1"
	Doctor      Role = "doctor"
	GateOfficer Role = "gate_officer"
	Dashboard   Role = "dashboard"
)

// Priority is the landing order: the first role a user holds wins.
var Priority = []Role{Admin, GateAdmin, Final, Stage1, Doctor, GateOfficer, Dashboard}

// Valid reports whether r is a known role.
func (r Role) Valid() bool {
	for _, p := range Priority {
		if p == r {
			return true
		}
	}
	return false
}

// Slug is the role's page name in the URL (/staff/<slug>).
func (r Role) Slug() string {
	switch r {
	case GateOfficer:
		return "gate"
	case GateAdmin:
		return "gate-admin"
	}
	return string(r)
}

// Label is the page title shown to people.
func (r Role) Label() string {
	switch r {
	case Admin:
		return "Administration"
	case GateAdmin:
		return "Gate administration"
	case Final:
		return "Final review"
	case Stage1:
		return "Stage 1 review"
	case Doctor:
		return "Doctor review"
	case GateOfficer:
		return "Gate"
	case Dashboard:
		return "Dashboard"
	}
	return string(r)
}

// ParseRoles reads a comma-separated list such as "stage1,doctor".
func ParseRoles(s string) ([]Role, error) {
	var out []Role
	for _, part := range strings.Split(s, ",") {
		r := Role(strings.ToLower(strings.TrimSpace(part)))
		if r == "" {
			continue
		}
		if !r.Valid() {
			return nil, fmt.Errorf("unknown role %q", r)
		}
		out = append(out, r)
	}
	return out, nil
}

// LandingRole picks the role whose page a user lands on, or "" with no roles.
func LandingRole(roles []Role) Role {
	for _, p := range Priority {
		for _, r := range roles {
			if r == p {
				return p
			}
		}
	}
	return ""
}

// LandingPath is where login sends the user, or "" if they hold no role.
func LandingPath(roles []Role) string {
	r := LandingRole(roles)
	if r == "" {
		return ""
	}
	return "/staff/" + r.Slug()
}

// User is a staff account without its secrets.
type User struct {
	ID          int64
	Username    string
	DisplayName string
	Email       string
	Active      bool
	Roles       []Role
}

// HasRole reports whether the user holds r.
func (u User) HasRole(r Role) bool {
	for _, x := range u.Roles {
		if x == r {
			return true
		}
	}
	return false
}

// NameEntry is one row of the login dropdown: names only, no roles or emails.
type NameEntry struct{ Username, DisplayName string }

// Errors. ErrBadLogin is deliberately the same for an unknown name, a wrong
// PIN and an inactive account, so the login form reveals nothing.
var (
	ErrBadLogin = errors.New("name or PIN is incorrect")
	ErrLocked   = errors.New("account temporarily locked")
	ErrBadPIN   = errors.New("PIN must be 4 to 8 letters or digits")
	ErrUsername = errors.New("username must be 2 to 40 lower-case letters, digits, dot, dash or underscore")
	ErrExists   = errors.New("that username already exists")
	ErrNoRoles  = errors.New("at least one role is required")
	ErrNoName   = errors.New("display name is required")
	ErrNotFound = errors.New("staff user not found")
)

var (
	pinRe      = regexp.MustCompile(`^[A-Za-z0-9]{4,8}$`)
	usernameRe = regexp.MustCompile(`^[a-z0-9._-]{2,40}$`)
)

// ValidPIN checks the PIN format: 4 to 8 letters or digits.
func ValidPIN(pin string) error {
	if !pinRe.MatchString(pin) {
		return ErrBadPIN
	}
	return nil
}

// Store reads and writes staff accounts.
type Store struct {
	db    *sql.DB
	clock dates.Clock
	pol   policy.Policy
	polFn func() policy.Policy // when set, read at each use so Settings edits apply at once
	cost  int
	dummy []byte // hash used to keep unknown-name logins as slow as real ones
}

// NewStore builds a Store. clock and pol are injected so lockout is testable.
func NewStore(db *sql.DB, clock dates.Clock, pol policy.Policy) *Store {
	return (&Store{db: db, clock: clock, pol: pol}).WithCost(bcrypt.DefaultCost)
}

// WithPolicy makes the store read the policy from fn at each login instead of
// the fixed value it was built with.
func (s *Store) WithPolicy(fn func() policy.Policy) *Store {
	s.polFn = fn
	return s
}

func (s *Store) policy() policy.Policy {
	if s.polFn != nil {
		return s.polFn()
	}
	return s.pol
}

// WithCost sets the bcrypt cost (tests use bcrypt.MinCost to stay fast).
func (s *Store) WithCost(cost int) *Store {
	s.cost = cost
	s.dummy, _ = bcrypt.GenerateFromPassword([]byte("not-a-real-pin"), cost)
	return s
}

// Create adds an active account. The username is lower-cased.
func (s *Store) Create(ctx context.Context, username, display, email, pin string, roles []Role) (User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	display = strings.TrimSpace(display)
	switch {
	case !usernameRe.MatchString(username):
		return User{}, ErrUsername
	case display == "":
		return User{}, ErrNoName
	case len(roles) == 0:
		return User{}, ErrNoRoles
	}
	if err := ValidPIN(pin); err != nil {
		return User{}, err
	}
	for _, r := range roles {
		if !r.Valid() {
			return User{}, fmt.Errorf("unknown role %q", r)
		}
	}
	var taken int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM staff_user WHERE username = ?`, username).Scan(&taken); err != nil {
		return User{}, fmt.Errorf("check username: %w", err)
	}
	if taken > 0 {
		return User{}, ErrExists
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pin), s.cost)
	if err != nil {
		return User{}, fmt.Errorf("hash pin: %w", err)
	}
	return s.insert(ctx, username, display, strings.TrimSpace(email), string(hash), roles)
}

func (s *Store) insert(ctx context.Context, username, display, email, hash string, roles []Role) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is a no-op

	now := string(s.clock.Now())
	res, err := tx.ExecContext(ctx, `INSERT INTO staff_user
		(username, display_name, email, pin_hash, active, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, ?, ?)`, username, display, email, hash, now, now)
	if err != nil {
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("user id: %w", err)
	}
	seen := map[Role]bool{}
	var kept []Role
	for _, r := range roles {
		if seen[r] {
			continue
		}
		seen[r] = true
		kept = append(kept, r)
		if _, err := tx.ExecContext(ctx, `INSERT INTO staff_user_role (user_id, role) VALUES (?, ?)`, id, string(r)); err != nil {
			return User{}, fmt.Errorf("insert role: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit: %w", err)
	}
	return User{ID: id, Username: username, DisplayName: display, Email: email, Active: true, Roles: kept}, nil
}

// Authenticate checks the PIN. It enforces the lockout: LoginMaxTries wrong
// PINs lock the account for LoginLockMinutes, and while locked even the right
// PIN is refused. A correct PIN after the lock has passed resets the counter.
func (s *Store) Authenticate(ctx context.Context, username, pin string) (User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	var (
		u      User
		hash   string
		active int
		failed int
		locked string
	)
	err := s.db.QueryRowContext(ctx, `SELECT id, username, display_name, email, pin_hash, active, failed_count, locked_until
		FROM staff_user WHERE username = ?`, username).Scan(&u.ID, &u.Username, &u.DisplayName, &u.Email, &hash, &active, &failed, &locked)
	if errors.Is(err, sql.ErrNoRows) {
		_ = bcrypt.CompareHashAndPassword(s.dummy, []byte(pin)) // same cost as a real check
		return User{}, ErrBadLogin
	}
	if err != nil {
		return User{}, fmt.Errorf("load user: %w", err)
	}
	now := s.clock.Now()
	if locked != "" && locked > string(now) {
		return User{}, ErrLocked
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(pin)) != nil || active == 0 {
		return User{}, s.recordFailure(ctx, u.ID, failed, now)
	}
	if failed != 0 || locked != "" {
		if _, err := s.db.ExecContext(ctx, `UPDATE staff_user SET failed_count = 0, locked_until = '', updated_at = ? WHERE id = ?`, string(now), u.ID); err != nil {
			return User{}, fmt.Errorf("reset lockout: %w", err)
		}
	}
	return s.ByID(ctx, u.ID)
}

// recordFailure counts a wrong try and returns the error to show. The try
// that reaches the limit already reports ErrLocked.
func (s *Store) recordFailure(ctx context.Context, id int64, failed int, now dates.Moment) error {
	failed++
	lockedUntil := ""
	result := ErrBadLogin
	pol := s.policy()
	if failed >= pol.LoginMaxTries {
		failed = 0
		lockedUntil = string(now.AddMinutes(pol.LoginLockMinutes))
		result = ErrLocked
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE staff_user SET failed_count = ?, locked_until = ?, updated_at = ? WHERE id = ?`,
		failed, lockedUntil, string(now), id); err != nil {
		return fmt.Errorf("record failure: %w", err)
	}
	return result
}

// ByID loads one account with its roles.
func (s *Store) ByID(ctx context.Context, id int64) (User, error) {
	var u User
	var active int
	err := s.db.QueryRowContext(ctx, `SELECT id, username, display_name, email, active FROM staff_user WHERE id = ?`, id).
		Scan(&u.ID, &u.Username, &u.DisplayName, &u.Email, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("load user: %w", err)
	}
	u.Active = active == 1
	rows, err := s.db.QueryContext(ctx, `SELECT role FROM staff_user_role WHERE user_id = ?`, id)
	if err != nil {
		return User{}, fmt.Errorf("load roles: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return User{}, fmt.Errorf("scan role: %w", err)
		}
		u.Roles = append(u.Roles, Role(r))
	}
	return u, rows.Err()
}

// ActiveNames feeds the login dropdown: active accounts only, by name.
func (s *Store) ActiveNames(ctx context.Context) ([]NameEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT username, display_name FROM staff_user WHERE active = 1 ORDER BY display_name, username`)
	if err != nil {
		return nil, fmt.Errorf("list names: %w", err)
	}
	defer rows.Close()
	var out []NameEntry
	for rows.Next() {
		var n NameEntry
		if err := rows.Scan(&n.Username, &n.DisplayName); err != nil {
			return nil, fmt.Errorf("scan name: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Unlock clears a lockout (admin action).
func (s *Store) Unlock(ctx context.Context, username string) error {
	return s.exec(ctx, `UPDATE staff_user SET failed_count = 0, locked_until = '', updated_at = ? WHERE username = ?`, username)
}

// SetActive switches an account on or off (off keeps the history).
func (s *Store) SetActive(ctx context.Context, username string, on bool) error {
	v := 0
	if on {
		v = 1
	}
	return s.execArgs(ctx, `UPDATE staff_user SET active = ?, updated_at = ? WHERE username = ?`, v, string(s.clock.Now()), strings.ToLower(strings.TrimSpace(username)))
}

// SetPIN replaces the PIN and clears any lockout. The PIN is never logged.
func (s *Store) SetPIN(ctx context.Context, username, pin string) error {
	if err := ValidPIN(pin); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pin), s.cost)
	if err != nil {
		return fmt.Errorf("hash pin: %w", err)
	}
	return s.execArgs(ctx, `UPDATE staff_user SET pin_hash = ?, failed_count = 0, locked_until = '', updated_at = ? WHERE username = ?`,
		string(hash), string(s.clock.Now()), strings.ToLower(strings.TrimSpace(username)))
}

func (s *Store) exec(ctx context.Context, query, username string) error {
	return s.execArgs(ctx, query, string(s.clock.Now()), strings.ToLower(strings.TrimSpace(username)))
}

func (s *Store) execArgs(ctx context.Context, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update staff user: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
