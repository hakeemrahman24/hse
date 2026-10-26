package main

import (
	"context"
	"database/sql"
	"os"
	"strings"
)

// defaultPassword is what "Reset password" sets. Set DEFAULT_USER_PASSWORD (at least 8 characters)
// in .env to choose it; the built-in value is only a fallback. Either way the person must replace
// it at their next sign-in, so it never stays in use.
const fallbackDefaultPassword = "Welcome@HSE1"

func defaultPassword() string {
	if v := os.Getenv("DEFAULT_USER_PASSWORD"); len(v) >= minPasswordLen {
		return v
	}
	return fallbackDefaultPassword
}

// accountUpdate describes a change to one account. A nil FullName or Role, or an empty
// PasswordHash, leaves that column as it is.
type accountUpdate struct {
	ID                    int64
	OldUsername, Username string
	FullName, Role        *string
	PasswordHash          string
}

// updateAccount saves the change. Posts remember who entered them by username (created_by), so a
// rename moves them to the new name in the same transaction; otherwise the person would lose the
// right to edit their own posts.
func updateAccount(ctx context.Context, db *sql.DB, u accountUpdate) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	set, args := []string{"username = ?"}, []any{u.Username}
	if u.FullName != nil {
		set, args = append(set, "full_name = ?"), append(args, *u.FullName)
	}
	if u.Role != nil {
		set, args = append(set, "role = ?"), append(args, *u.Role)
	}
	if u.PasswordHash != "" {
		set, args = append(set, "password_hash = ?", "must_change_password = 0"), append(args, u.PasswordHash)
	}
	args = append(args, u.ID)
	if _, err := tx.ExecContext(ctx, `UPDATE admin_users SET `+strings.Join(set, ", ")+` WHERE id = ?`, args...); err != nil {
		return err
	}
	if u.OldUsername != u.Username {
		if _, err := tx.ExecContext(ctx, `UPDATE observations_observation SET created_by = ? WHERE created_by = ?`, u.Username, u.OldUsername); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// otherActiveAdmins counts the administrators, apart from one account, who can still sign in.
// Archiving, deleting or demoting the last one would leave nobody able to manage users.
func otherActiveAdmins(ctx context.Context, db *sql.DB, exceptID int64) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_users WHERE role = 'admin' AND archived_at IS NULL AND id <> ?`, exceptID).Scan(&n)
	return n, err
}
