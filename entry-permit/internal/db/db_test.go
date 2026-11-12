package db

import (
	"database/sql"
	"io/fs"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"

	"entry-permit/migrations"
)

func openTemp(t *testing.T) (*sql.DB, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "sub", "t.db")
	d, err := Open("sqlite", file+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d, file
}

func TestStatements(t *testing.T) {
	in := "-- comment; with semicolon\nCREATE TABLE a (x INT);\n\n-- another\nINSERT INTO a VALUES (1);\n  \n"
	want := []string{"CREATE TABLE a (x INT)", "INSERT INTO a VALUES (1)"}
	if got := Statements(in); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q", got)
	}
}

func TestOpenCreatesFolderAndMigrates(t *testing.T) {
	d, _ := openTemp(t)
	if err := Migrate(d, migrations.FS, migrations.Dir("sqlite")); err != nil {
		t.Fatal(err)
	}
	// Running again must be a no-op, not a "table already exists" error.
	if err := Migrate(d, migrations.FS, migrations.Dir("sqlite")); err != nil {
		t.Fatalf("second run: %v", err)
	}
	files, _ := fs.Glob(migrations.FS, "sqlite/*.sql")
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil || n != len(files) {
		t.Errorf("recorded %d of %d files, %v", n, len(files), err)
	}
	for _, table := range []string{"setting", "audit_log", "job_run"} {
		if _, err := d.Exec("SELECT 1 FROM " + table); err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}
}

func TestSettingRoundTrip(t *testing.T) {
	d, _ := openTemp(t)
	if err := Migrate(d, migrations.FS, "sqlite"); err != nil {
		t.Fatal(err)
	}
	_, err := d.Exec(`INSERT INTO setting (setting_key, grp, label, value_type, value, default_value)
		VALUES ('onshore_weeks', 'rotation', 'Onshore weeks', 'int', '8', '8')`)
	if err != nil {
		t.Fatal(err)
	}
	var v string
	if err := d.QueryRow(`SELECT value FROM setting WHERE setting_key = 'onshore_weeks'`).Scan(&v); err != nil || v != "8" {
		t.Errorf("%q %v", v, err)
	}
	// Primary key must refuse a duplicate key.
	if _, err := d.Exec(`INSERT INTO setting (setting_key, grp, label, value_type, value, default_value)
		VALUES ('onshore_weeks', 'rotation', 'x', 'int', '9', '8')`); err == nil {
		t.Error("duplicate key accepted")
	}
}

func TestMigrateOrderAndFailure(t *testing.T) {
	d, _ := openTemp(t)
	fsys := fstest.MapFS{
		"m/002_b.sql": {Data: []byte("INSERT INTO t VALUES (2);")},
		"m/001_a.sql": {Data: []byte("CREATE TABLE t (x INT);\nINSERT INTO t VALUES (1);")},
	}
	if err := Migrate(d, fsys, "m"); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&n)
	if n != 2 {
		t.Errorf("rows=%d: files must run in name order", n)
	}
	bad := fstest.MapFS{"m/001_bad.sql": {Data: []byte("NOT SQL;")}}
	d2, _ := openTemp(t)
	if err := Migrate(d2, bad, "m"); err == nil {
		t.Error("bad migration should fail")
	}
	var seen int
	_ = d2.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&seen)
	if seen != 0 {
		t.Error("failed migration must not be recorded")
	}
}

func TestOpenErrors(t *testing.T) {
	if _, err := Open("mysql", "nobody@tcp(127.0.0.1:1)/none?timeout=1s"); err == nil {
		t.Error("unreachable mysql should fail")
	}
	// A folder that cannot be created (parent is a file).
	d, file := openTemp(t)
	_ = d
	if _, err := Open("sqlite", filepath.Join(file, "x", "y.db")); err == nil {
		t.Error("impossible path accepted")
	}
}
