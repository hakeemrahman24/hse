package mcusubmit

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"hse-medpost/internal/dates"
)

// Status and role values live in Go, not in a CHECK/ENUM, so migration 016 is valid on both
// SQLite and MariaDB (CLAUDE.md: SQL must work on MariaDB, not just SQLite).
const (
	StatusRequested = "Requested" // asked, waiting for the email link and then the admin
	StatusApproved  = "Approved"  // admin said yes, no password yet
	StatusActive    = "Active"    // username and password set
	StatusRejected  = "Rejected"
	StatusDisabled  = "Disabled"

	RoleSubmitter = "Submitter"
	RoleAdmin     = "Admin"

	builtInUsername = "admin"
	builtInEmail    = "admin@portal.invalid" // .invalid can never receive mail, so no reset link can reach it
)

var (
	ErrNotFound      = errors.New("mcusubmit: not found")
	ErrEmailTaken    = errors.New("mcusubmit: email already registered")
	ErrUsernameTaken = errors.New("mcusubmit: username already taken")
	// ErrBadState means the account exists but is not in a status that allows the action
	// (for example approving an account whose email was never verified).
	ErrBadState = errors.New("mcusubmit: not allowed in the current status")
)

// Submitter is one outside account. It never holds a password hash; use passwordHash for sign-in.
type Submitter struct {
	ID               int64
	Email            string
	Username         string
	Name             string
	CompanyID        int64 // 0 for the built-in admin
	Position         string
	Role             string
	Status           string
	IsBuiltIn        bool
	SessionVersion   int
	RemindersEnabled bool
	EmailVerifiedAt  string
	EmailBouncedAt   string
	RejectReason     string
	CreatedAt        string
	LastLoginAt      string
}

// Store reads and writes the portal tables.
type Store struct {
	DB  *sql.DB
	now func() time.Time // replaced in tests so expiry and lockout need no sleeping
}

// NewStore returns a Store on database.
func NewStore(database *sql.DB) *Store { return &Store{DB: database, now: time.Now} }

// stamp is the current time as the UTC text every portal column uses.
func (s *Store) stamp() string { return dates.Stamp(s.now()) }

const submitterColumns = `SubmitterID, Email, COALESCE(Username, ''), Name, COALESCE(FK_CompanyID, 0),
	PositionText, Role, Status, IsBuiltIn, SessionVersion, RemindersEnabled,
	COALESCE(EmailVerifiedAt, ''), COALESCE(EmailBouncedAt, ''), COALESCE(RejectReason, ''),
	CreatedAt, COALESCE(LastLoginAt, '')`

type rowScanner interface{ Scan(dest ...any) error }

func scanSubmitter(r rowScanner) (Submitter, error) {
	var u Submitter
	var builtIn, reminders int
	err := r.Scan(&u.ID, &u.Email, &u.Username, &u.Name, &u.CompanyID, &u.Position, &u.Role, &u.Status,
		&builtIn, &u.SessionVersion, &reminders, &u.EmailVerifiedAt, &u.EmailBouncedAt, &u.RejectReason,
		&u.CreatedAt, &u.LastLoginAt)
	u.IsBuiltIn, u.RemindersEnabled = builtIn == 1, reminders == 1
	return u, err
}

func (s *Store) one(ctx context.Context, where string, arg any) (Submitter, error) {
	u, err := scanSubmitter(s.DB.QueryRowContext(ctx, `SELECT `+submitterColumns+` FROM Submitter WHERE `+where, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return Submitter{}, ErrNotFound
	}
	return u, err
}

// ByID returns the submitter with this id, or ErrNotFound.
func (s *Store) ByID(ctx context.Context, id int64) (Submitter, error) {
	return s.one(ctx, `SubmitterID = ?`, id)
}

// ByEmail looks up a lower-cased email.
func (s *Store) ByEmail(ctx context.Context, email string) (Submitter, error) {
	return s.one(ctx, `Email = ?`, normalizeEmail(email))
}

// ByUsername looks up a username, ignoring case (usernames are stored lower-cased).
func (s *Store) ByUsername(ctx context.Context, username string) (Submitter, error) {
	return s.one(ctx, `Username = ?`, strings.ToLower(strings.TrimSpace(username)))
}

func normalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// Register stores a new request with Status Requested. The caller has already sanitized name and
// position and checked the company is on the Company list (plan section 4).
func (s *Store) Register(ctx context.Context, email, name string, companyID int64, position string) (int64, error) {
	email = normalizeEmail(email)
	if _, err := s.ByEmail(ctx, email); err == nil {
		return 0, ErrEmailTaken
	} else if !errors.Is(err, ErrNotFound) {
		return 0, err
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO Submitter (Email, Name, FK_CompanyID, PositionText, Status, CreatedAt)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		email, name, companyID, position, StatusRequested, s.stamp())
	if isUniqueViolation(err) {
		return 0, ErrEmailTaken // two requests for one address raced past the check above
	}
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// isUniqueViolation recognises a duplicate-key error on both engines without importing their
// drivers: SQLite says "UNIQUE constraint failed", MariaDB says "Duplicate entry".
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") || strings.Contains(msg, "duplicate entry")
}

// List returns every submitter except the built-in admin, newest first. status "" means all.
func (s *Store) List(ctx context.Context, status string) ([]Submitter, error) {
	q := `SELECT ` + submitterColumns + ` FROM Submitter WHERE IsBuiltIn = 0`
	var args []any
	if status != "" {
		q += ` AND Status = ?`
		args = append(args, status)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY SubmitterID DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Submitter
	for rows.Next() {
		u, err := scanSubmitter(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) passwordHash(ctx context.Context, id int64) (string, error) {
	var h sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT PasswordHash FROM Submitter WHERE SubmitterID = ?`, id).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return h.String, err
}
