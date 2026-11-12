// Package db opens the database and applies migrations.
package db

import (
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"

	"entry-permit/internal/dates"
)

// Open connects to SQLite (driver "sqlite") or MariaDB (driver "mysql").
func Open(driver, dsn string) (*sql.DB, error) {
	if driver == "mysql" {
		return open("mysql", dsn)
	}
	file, _, _ := strings.Cut(dsn, "?")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return nil, fmt.Errorf("create database folder: %w", err)
	}
	return open("sqlite", dsn)
}

func open(driver, dsn string) (*sql.DB, error) {
	d, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", driver, err)
	}
	d.SetMaxOpenConns(10)
	d.SetMaxIdleConns(2)
	if err := d.Ping(); err != nil {
		_ = d.Close()
		return nil, fmt.Errorf("ping %s: %w", driver, err)
	}
	return d, nil
}

// Migrate applies every *.sql file in dir of fsys that is not yet recorded in
// schema_migrations, in filename order. WHY our own runner: medpost applies
// MariaDB migrations by hand, which is easy to forget; here a deploy that
// restarts the service brings the schema up to date, and a file never runs
// twice. MariaDB cannot roll back DDL, so each file should be small.
func Migrate(d *sql.DB, fsys fs.FS, dir string) error {
	if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    VARCHAR(100) NOT NULL PRIMARY KEY,
		applied_at CHAR(19)     NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	files, err := fs.Glob(fsys, path.Join(dir, "*.sql"))
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(files)
	for _, f := range files {
		version := path.Base(f)
		var seen int
		if err := d.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&seen); err != nil {
			return fmt.Errorf("check %s: %w", version, err)
		}
		if seen > 0 {
			continue
		}
		body, err := fs.ReadFile(fsys, f)
		if err != nil {
			return fmt.Errorf("read %s: %w", version, err)
		}
		for _, stmt := range Statements(string(body)) {
			if _, err := d.Exec(stmt); err != nil {
				return fmt.Errorf("migration %s failed: %w", version, err)
			}
		}
		if _, err := d.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			version, string(dates.NowJakarta())); err != nil {
			return fmt.Errorf("record %s: %w", version, err)
		}
	}
	return nil
}

// Statements splits SQL text into single statements: full-line "--" comments
// are dropped and the rest is split on ";". WHY: the MariaDB driver runs one
// statement per Exec. Migration files must not contain ";" inside strings.
func Statements(sqlText string) []string {
	var kept []string
	for _, line := range strings.Split(sqlText, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		kept = append(kept, line)
	}
	var out []string
	for _, s := range strings.Split(strings.Join(kept, "\n"), ";") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
