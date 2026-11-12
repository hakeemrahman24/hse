package staff

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	netmail "net/mail"
	"strings"

	"entry-permit/internal/audit"

	"golang.org/x/crypto/bcrypt"
)

// DefaultPIN is the only PIN an admin can give. WHY: an admin never types or
// sees a user's PIN, so nobody but the owner of an account knows it; after a
// reset the user signs in with this one and changes it on their own page.
const DefaultPIN = "123456"

// Errors for the management actions.
var (
	ErrWrongPIN  = errors.New("current PIN is incorrect")
	ErrLastAdmin = errors.New("there must be at least one active admin")
	ErrMismatch  = errors.New("the two new PINs are not the same")
)

// Account is a User plus what the admin list needs.
type Account struct {
	User
	Locked bool
}

// All lists every account, inactive ones included.
func (s *Store) All(ctx context.Context) ([]Account, error) {
	now := string(s.clock.Now())
	rows, err := s.db.QueryContext(ctx, `SELECT id, username, display_name, email, active, locked_until FROM staff_user ORDER BY display_name, username`)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	var out []Account
	for rows.Next() {
		var a Account
		var active int
		var locked string
		if err := rows.Scan(&a.ID, &a.Username, &a.DisplayName, &a.Email, &active, &locked); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan account: %w", err)
		}
		a.Active, a.Locked = active == 1, locked != "" && locked > now
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close() // free the connection before the role queries
	for i := range out {
		u, err := s.ByID(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Roles = u.Roles
	}
	return out, nil
}

// audited runs fn and the audit row in one transaction.
func (s *Store) audited(ctx context.Context, actor, action, target, oldV, newV string, fn func(tx *sql.Tx) error) (err error) {
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
	if err = audit.Write(ctx, tx, s.clock.Now(), actor, action, target, oldV, newV); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// CreateByAdmin adds an account with the default PIN.
func (s *Store) CreateByAdmin(ctx context.Context, actor, username, display, email string, roles []Role) (User, error) {
	u, err := s.Create(ctx, username, display, email, DefaultPIN, roles)
	if err != nil {
		return User{}, err
	}
	// The account exists even if this fails; the error is returned so it is not silent.
	if err := audit.Write(ctx, s.db, s.clock.Now(), actor, "staff.create", "staff:"+u.Username, "", joinRoles(u.Roles)); err != nil {
		return u, err
	}
	return u, nil
}

// ResetPIN sets the PIN to DefaultPIN and clears any lock. It takes no PIN
// argument on purpose.
func (s *Store) ResetPIN(ctx context.Context, actor, username string) error {
	username = strings.ToLower(strings.TrimSpace(username))
	hash, err := bcrypt.GenerateFromPassword([]byte(DefaultPIN), s.cost)
	if err != nil {
		return fmt.Errorf("hash pin: %w", err)
	}
	return s.audited(ctx, actor, "staff.pin_reset", "staff:"+username, "", "", func(tx *sql.Tx) error {
		return mustChange(tx.ExecContext(ctx, `UPDATE staff_user SET pin_hash=?, failed_count=0, locked_until='', updated_at=? WHERE username=?`,
			string(hash), string(s.clock.Now()), username))
	})
}

// ChangeOwnPIN checks the current PIN (wrong tries count toward the lockout,
// like a login) and sets a new one.
func (s *Store) ChangeOwnPIN(ctx context.Context, id int64, current, newPIN string) error {
	u, err := s.ByID(ctx, id)
	if err != nil {
		return err
	}
	if _, err := s.Authenticate(ctx, u.Username, current); err != nil {
		if errors.Is(err, ErrBadLogin) {
			return ErrWrongPIN
		}
		return err
	}
	if err := ValidPIN(newPIN); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPIN), s.cost)
	if err != nil {
		return fmt.Errorf("hash pin: %w", err)
	}
	return s.audited(ctx, u.Username, "staff.pin_change", "staff:"+u.Username, "", "", func(tx *sql.Tx) error {
		return mustChange(tx.ExecContext(ctx, `UPDATE staff_user SET pin_hash=?, updated_at=? WHERE id=?`, string(hash), string(s.clock.Now()), id))
	})
}

// UpdateProfile lets a user change their own username and display name.
func (s *Store) UpdateProfile(ctx context.Context, id int64, username, display string) error {
	username = strings.ToLower(strings.TrimSpace(username))
	display = strings.TrimSpace(display)
	switch {
	case !usernameRe.MatchString(username):
		return ErrUsername
	case display == "":
		return ErrNoName
	}
	u, err := s.ByID(ctx, id)
	if err != nil {
		return err
	}
	if username == u.Username && display == u.DisplayName {
		return nil
	}
	var taken int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM staff_user WHERE username=? AND id<>?`, username, id).Scan(&taken); err != nil {
		return fmt.Errorf("check username: %w", err)
	}
	if taken > 0 {
		return ErrExists
	}
	return s.audited(ctx, u.Username, "staff.profile", "staff:"+u.Username, u.Username+" / "+u.DisplayName, username+" / "+display, func(tx *sql.Tx) error {
		return mustChange(tx.ExecContext(ctx, `UPDATE staff_user SET username=?, display_name=?, updated_at=? WHERE id=?`, username, display, string(s.clock.Now()), id))
	})
}

// AdminUpdate changes display name, email and roles of another account.
func (s *Store) AdminUpdate(ctx context.Context, actor, username, display, email string, roles []Role) error {
	display = strings.TrimSpace(display)
	switch {
	case display == "":
		return ErrNoName
	case len(roles) == 0:
		return ErrNoRoles
	}
	for _, r := range roles {
		if !r.Valid() {
			return fmt.Errorf("unknown role %q", r)
		}
	}
	username = strings.ToLower(strings.TrimSpace(username))
	cur, err := s.byUsername(ctx, username)
	if err != nil {
		return err
	}
	keepsAdmin := false
	for _, r := range roles {
		keepsAdmin = keepsAdmin || r == Admin
	}
	if cur.Active && cur.HasRole(Admin) && !keepsAdmin {
		if err := s.needOtherAdmin(ctx, cur.ID); err != nil {
			return err
		}
	}
	return s.audited(ctx, actor, "staff.update", "staff:"+username, joinRoles(cur.Roles), joinRoles(roles), func(tx *sql.Tx) error {
		if err := mustChange(tx.ExecContext(ctx, `UPDATE staff_user SET display_name=?, email=?, updated_at=? WHERE id=?`,
			display, strings.TrimSpace(email), string(s.clock.Now()), cur.ID)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM staff_user_role WHERE user_id=?`, cur.ID); err != nil {
			return fmt.Errorf("clear roles: %w", err)
		}
		seen := map[Role]bool{}
		for _, r := range roles {
			if seen[r] {
				continue
			}
			seen[r] = true
			if _, err := tx.ExecContext(ctx, `INSERT INTO staff_user_role (user_id, role) VALUES (?, ?)`, cur.ID, string(r)); err != nil {
				return fmt.Errorf("insert role: %w", err)
			}
		}
		return nil
	})
}

// AdminSetActive switches an account on or off, never leaving zero active admins.
func (s *Store) AdminSetActive(ctx context.Context, actor, username string, on bool) error {
	username = strings.ToLower(strings.TrimSpace(username))
	cur, err := s.byUsername(ctx, username)
	if err != nil {
		return err
	}
	if !on && cur.Active && cur.HasRole(Admin) {
		if err := s.needOtherAdmin(ctx, cur.ID); err != nil {
			return err
		}
	}
	v, label := 0, "off"
	if on {
		v, label = 1, "on"
	}
	return s.audited(ctx, actor, "staff.active", "staff:"+username, "", label, func(tx *sql.Tx) error {
		return mustChange(tx.ExecContext(ctx, `UPDATE staff_user SET active=?, updated_at=? WHERE id=?`, v, string(s.clock.Now()), cur.ID))
	})
}

// AdminUnlock clears a lockout.
func (s *Store) AdminUnlock(ctx context.Context, actor, username string) error {
	username = strings.ToLower(strings.TrimSpace(username))
	return s.audited(ctx, actor, "staff.unlock", "staff:"+username, "", "", func(tx *sql.Tx) error {
		return mustChange(tx.ExecContext(ctx, `UPDATE staff_user SET failed_count=0, locked_until='', updated_at=? WHERE username=?`, string(s.clock.Now()), username))
	})
}

func (s *Store) byUsername(ctx context.Context, username string) (User, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM staff_user WHERE username=?`, username).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("find user: %w", err)
	}
	return s.ByID(ctx, id)
}

// needOtherAdmin fails with ErrLastAdmin unless another active admin exists.
func (s *Store) needOtherAdmin(ctx context.Context, exceptID int64) error {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM staff_user u JOIN staff_user_role r ON r.user_id=u.id
		WHERE r.role=? AND u.active=1 AND u.id<>?`, string(Admin), exceptID).Scan(&n)
	if err != nil {
		return fmt.Errorf("count admins: %w", err)
	}
	if n == 0 {
		return ErrLastAdmin
	}
	return nil
}

func mustChange(res sql.Result, err error) error {
	if err != nil {
		return fmt.Errorf("update staff user: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func joinRoles(roles []Role) string {
	parts := make([]string, len(roles))
	for i, r := range roles {
		parts[i] = string(r)
	}
	return strings.Join(parts, ",")
}

// EmailsForRole lists the addresses of the active accounts that hold a role, for the
// reminder and digest mails. A blank or unusable address is skipped and a repeated one
// is listed once, so one bad entry never stops the mail reaching the others.
func (s *Store) EmailsForRole(ctx context.Context, role Role) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT u.email FROM staff_user u
		JOIN staff_user_role r ON r.user_id = u.id
		WHERE r.role = ? AND u.active = 1 AND u.email <> '' ORDER BY u.id`, string(role))
	if err != nil {
		return nil, fmt.Errorf("list %s addresses: %w", role, err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	var out []string
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan address: %w", err)
		}
		a, err := netmail.ParseAddress(strings.TrimSpace(raw))
		if err != nil || a.Name != "" {
			continue // "Name <x@y>" or text that is not an address
		}
		addr := strings.ToLower(a.Address)
		if !seen[addr] {
			seen[addr] = true
			out = append(out, addr)
		}
	}
	return out, rows.Err()
}
