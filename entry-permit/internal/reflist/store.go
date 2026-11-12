package reflist

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"entry-permit/internal/audit"
	"entry-permit/internal/dates"
)

// Statuses. Items are archived, never deleted.
const (
	statusActive   = "Active"
	statusArchived = "Archived"
)

// Errors.
var (
	ErrDuplicate = errors.New("That item already exists in this list (it may be archived).")
	ErrNotFound  = errors.New("That item no longer exists.")
)

// Item is one row of a list. Values follow the list's column order.
type Item struct {
	ID       int64
	Values   []string
	Archived bool
}

// Store reads and writes list items. Every write also writes an audit row in
// the same transaction.
type Store struct {
	db    *sql.DB
	clock dates.Clock
}

// NewStore builds a Store.
func NewStore(db *sql.DB, clock dates.Clock) *Store { return &Store{db: db, clock: clock} }

// padded fits values into the four fixed columns.
func padded(values []string) [maxColumns]string {
	var p [maxColumns]string
	copy(p[:], values)
	return p
}

// Items lists a list's items. Archived ones are included only on request.
func (s *Store) Items(ctx context.Context, d Def, includeArchived bool) ([]Item, error) {
	query := `SELECT id, c1, c2, c3, c4, status FROM ref_item WHERE list_key = ?`
	if !includeArchived {
		query += ` AND status = 'Active'`
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY sort_order, id`, d.Key)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", d.Key, err)
	}
	defer rows.Close()
	var items []Item
	for rows.Next() {
		var it Item
		var c [maxColumns]string
		var status string
		if err := rows.Scan(&it.ID, &c[0], &c[1], &c[2], &c[3], &status); err != nil {
			return nil, fmt.Errorf("scan %s: %w", d.Key, err)
		}
		it.Values = append([]string(nil), c[:len(d.Columns)]...)
		it.Archived = status == statusArchived
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortItems(d, items)
	return items, nil
}

// sortItems orders by the list's SortBy columns (case-insensitive), keeping
// the added order for equal rows and for lists without SortBy.
func sortItems(d Def, items []Item) {
	if len(d.SortBy) == 0 {
		return
	}
	sort.SliceStable(items, func(i, j int) bool {
		for _, c := range d.SortBy {
			a, b := strings.ToLower(items[i].Values[c]), strings.ToLower(items[j].Values[c])
			if a != b {
				return a < b
			}
		}
		return false
	})
}

// Add creates an item. actor is the staff username, for the audit log.
func (s *Store) Add(ctx context.Context, actor string, d Def, values []string) (Item, error) {
	clean, err := Validate(d, values)
	if err != nil {
		return Item{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Item{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit

	key := itemKey(d, clean)
	var taken int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM ref_item WHERE list_key = ? AND item_key = ?`, d.Key, key).Scan(&taken); err != nil {
		return Item{}, fmt.Errorf("check duplicate: %w", err)
	}
	if taken > 0 {
		return Item{}, ErrDuplicate
	}
	var order int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sort_order), 0) + 1 FROM ref_item WHERE list_key = ?`, d.Key).Scan(&order); err != nil {
		return Item{}, fmt.Errorf("next order: %w", err)
	}
	now := s.clock.Now()
	p := padded(clean)
	res, err := tx.ExecContext(ctx, `INSERT INTO ref_item (list_key, item_key, c1, c2, c3, c4, status, sort_order, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 'Active', ?, ?, ?)`, d.Key, key, p[0], p[1], p[2], p[3], order, string(now), string(now))
	if err != nil {
		return Item{}, fmt.Errorf("insert item: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Item{}, fmt.Errorf("item id: %w", err)
	}
	target := fmt.Sprintf("%s#%d", d.Key, id)
	if err := audit.Write(ctx, tx, now, actor, "list.add", target, "", join(clean)); err != nil {
		return Item{}, err
	}
	if err := tx.Commit(); err != nil {
		return Item{}, fmt.Errorf("commit: %w", err)
	}
	return Item{ID: id, Values: clean}, nil
}

func join(values []string) string { return strings.Join(values, " | ") }

// loadForUpdate reads the current values of one item inside tx.
func loadForUpdate(ctx context.Context, tx *sql.Tx, d Def, id int64) ([]string, error) {
	var c [maxColumns]string
	err := tx.QueryRowContext(ctx, `SELECT c1, c2, c3, c4 FROM ref_item WHERE id = ? AND list_key = ?`, id, d.Key).
		Scan(&c[0], &c[1], &c[2], &c[3])
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load item: %w", err)
	}
	return append([]string(nil), c[:len(d.Columns)]...), nil
}

// Update changes the values of an item.
func (s *Store) Update(ctx context.Context, actor string, d Def, id int64, values []string) error {
	clean, err := Validate(d, values)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit

	old, err := loadForUpdate(ctx, tx, d, id)
	if err != nil {
		return err
	}
	key := itemKey(d, clean)
	var clash int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM ref_item WHERE list_key = ? AND item_key = ? AND id <> ?`, d.Key, key, id).Scan(&clash); err != nil {
		return fmt.Errorf("check duplicate: %w", err)
	}
	if clash > 0 {
		return ErrDuplicate
	}
	now := s.clock.Now()
	p := padded(clean)
	if _, err := tx.ExecContext(ctx, `UPDATE ref_item SET item_key = ?, c1 = ?, c2 = ?, c3 = ?, c4 = ?, updated_at = ? WHERE id = ?`,
		key, p[0], p[1], p[2], p[3], string(now), id); err != nil {
		return fmt.Errorf("update item: %w", err)
	}
	if err := audit.Write(ctx, tx, now, actor, "list.update", fmt.Sprintf("%s#%d", d.Key, id), join(old), join(clean)); err != nil {
		return err
	}
	return tx.Commit()
}

// SetArchived archives or restores an item.
func (s *Store) SetArchived(ctx context.Context, actor string, d Def, id int64, archived bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit

	current, err := loadForUpdate(ctx, tx, d, id)
	if err != nil {
		return err
	}
	status, action := statusActive, "list.restore"
	if archived {
		status, action = statusArchived, "list.archive"
	}
	now := s.clock.Now()
	if _, err := tx.ExecContext(ctx, `UPDATE ref_item SET status = ?, updated_at = ? WHERE id = ?`, status, string(now), id); err != nil {
		return fmt.Errorf("set status: %w", err)
	}
	if err := audit.Write(ctx, tx, now, actor, action, fmt.Sprintf("%s#%d", d.Key, id), join(current), join(current)); err != nil {
		return err
	}
	return tx.Commit()
}
