// Package audit writes and reads the audit log: who changed what, and when.
//
// WHY a small shared package: every admin save (lists now, settings and
// users later) must leave the same kind of trail. Write takes either a
// *sql.DB or a *sql.Tx, so a change and its audit row can commit together:
// if the audit row fails, the change is rolled back and never goes unrecorded.
package audit

import (
	"context"
	"database/sql"
	"fmt"

	"entry-permit/internal/dates"
)

// Execer is satisfied by *sql.DB and *sql.Tx.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Queryer is satisfied by *sql.DB and *sql.Tx.
type Queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Entry is one audit row.
type Entry struct {
	ID                        int64
	At, Actor, Action, Target string
	Old, New                  string
}

// Write adds one row. Never put a secret (a PIN, a token) in old or new:
// log the fact that it changed instead.
func Write(ctx context.Context, ex Execer, at dates.Moment, actor, action, target, oldValue, newValue string) error {
	_, err := ex.ExecContext(ctx, `INSERT INTO audit_log (at, actor, action, target, old_value, new_value)
		VALUES (?, ?, ?, ?, ?, ?)`, string(at), actor, action, target, oldValue, newValue)
	if err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}

// Recent returns the newest rows first.
func Recent(ctx context.Context, q Queryer, limit int) ([]Entry, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, at, actor, action, target, old_value, new_value
		FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("read audit log: %w", err)
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.At, &e.Actor, &e.Action, &e.Target, &e.Old, &e.New); err != nil {
			return nil, fmt.Errorf("scan audit row: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
