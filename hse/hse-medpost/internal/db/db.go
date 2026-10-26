package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"
)

// Open connects to SQLite (driver "sqlite") or MariaDB/MySQL (driver "mysql").
func Open(driver, dsn string) (*sql.DB, error) {
	switch strings.ToLower(driver) {
	case "mysql", "mariadb":
		return open("mysql", dsn)
	default:
		path := dsn
		if i := strings.Index(dsn, "?"); i >= 0 {
			path = dsn[:i]
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		return open("sqlite", dsn)
	}
}

func open(driver, dsn string) (*sql.DB, error) {
	database, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(10)
	database.SetMaxIdleConns(2)
	if err := database.Ping(); err != nil {
		_ = database.Close()
		return nil, err
	}
	return database, nil
}

func Migrate(database *sql.DB, migrationSQL string) error {
	_, err := database.Exec(migrationSQL)
	return err
}

func LoadMigrationFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
