package audit

import (
	"context"
	"path/filepath"
	"testing"

	"entry-permit/internal/db"
	"entry-permit/migrations"
)

func TestWriteAndRecentNewestFirst(t *testing.T) {
	d, err := db.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := db.Migrate(d, migrations.FS, "sqlite"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := Write(ctx, d, "2026-11-06 08:00:00", "arif", "list.add", "nationality#1", "", "Indonesia"); err != nil {
		t.Fatal(err)
	}
	if err := Write(ctx, d, "2026-11-06 08:05:00", "arif", "list.update", "nationality#1", "Indonesia", "Indonesian"); err != nil {
		t.Fatal(err)
	}
	got, err := Recent(ctx, d, 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	if got[0].Action != "list.update" || got[0].Old != "Indonesia" || got[0].New != "Indonesian" || got[1].Action != "list.add" {
		t.Errorf("order or content wrong: %+v", got)
	}
	if one, _ := Recent(ctx, d, 1); len(one) != 1 || one[0].Action != "list.update" {
		t.Errorf("limit not applied: %+v", one)
	}
}

func TestWriteInsideTransactionRollsBack(t *testing.T) {
	d, err := db.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := db.Migrate(d, migrations.FS, "sqlite"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tx, _ := d.BeginTx(ctx, nil)
	if err := Write(ctx, tx, "2026-11-06 08:00:00", "a", "x", "t", "", ""); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	if got, _ := Recent(ctx, d, 10); len(got) != 0 {
		t.Errorf("rolled-back audit row survived: %+v", got)
	}
}

func TestErrorsAreWrapped(t *testing.T) {
	d, _ := db.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	defer d.Close() // no migration: the audit_log table does not exist
	ctx := context.Background()
	if err := Write(ctx, d, "x", "a", "b", "c", "", ""); err == nil {
		t.Error("write without table should fail")
	}
	if _, err := Recent(ctx, d, 5); err == nil {
		t.Error("read without table should fail")
	}
}
