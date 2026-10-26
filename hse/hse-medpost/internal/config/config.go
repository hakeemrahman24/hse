package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Port          string
	DBDriver      string // sqlite (default) or mysql (MariaDB)
	DatabasePath  string
	DBHost        string
	DBPort        string
	DBUser        string
	DBPassword    string
	DBName        string
	UploadDir     string
	SessionSecret string
	BaseURL       string

	// Health-certificate PDF generation (internal/entrymcu's certificate.go).
	CertSecret        string // seals the tamper-evident QR/footer fingerprint — never hardcode, unlike the legacy SYSTEM_SECRET
	CertLogoPath      string // local image file; empty uses logo.png in the common uploads folder
	CertSignaturePath string // local image file; empty uses uploads/_MedpostWide/common/signature.png

	// DirectoryAPIToken guards GET /api/directory/employees for other databases.
	DirectoryAPIToken string
}

func Load() Config {
	driver := strings.ToLower(getenv("DB_DRIVER", "sqlite"))
	if driver == "mariadb" {
		driver = "mysql"
	}
	return Config{
		Port:          getenv("PORT", "8080"),
		DBDriver:      driver,
		DatabasePath:  getenv("DATABASE_PATH", "./data/hse_medpost.db"),
		DBHost:        getenv("DB_HOST", "127.0.0.1"),
		DBPort:        getenv("DB_PORT", "3306"),
		DBUser:        getenv("DB_USER", "hse_medpost_user"),
		DBPassword:    getenv("DB_PASSWORD", ""),
		DBName:        getenv("DB_NAME", "hse_medpost"),
		UploadDir:     getenv("UPLOAD_DIR", "./data/uploads"),
		SessionSecret: getenv("SESSION_SECRET", "dev-session-secret-change-me"),
		BaseURL:       getenv("BASE_URL", "http://127.0.0.1:8080"),

		CertSecret:        getenv("MCU_CERT_SECRET", "dev-cert-secret-change-me"),
		CertLogoPath:      getenv("MCU_CERT_LOGO_PATH", ""), // empty uses uploads/_MedpostWide/common/logo.png
		CertSignaturePath: getenv("MCU_CERT_SIGNATURE_PATH", ""),

		DirectoryAPIToken: getenv("DIRECTORY_API_TOKEN", ""),
	}
}

// SigningDir is the private folder for doctor and Chief Medical Officer
// .p12 files. It sits beside the uploads folder so an upload zip cannot
// collect the keys. On the Orange Pi this is the server disk.
func (c Config) SigningDir() string {
	if v := os.Getenv("SIGNING_DIR"); v != "" {
		return v
	}
	return filepath.Join(filepath.Dir(c.UploadDir), "signing")
}

func (c Config) UseMySQL() bool {
	return c.DBDriver == "mysql"
}

// DSN is the database/sql connection string for the selected driver.
func (c Config) DSN() string {
	if c.UseMySQL() {
		var user *url.Userinfo
		if c.DBPassword == "" {
			user = url.User(c.DBUser)
		} else {
			user = url.UserPassword(c.DBUser, c.DBPassword)
		}
		return fmt.Sprintf("%s@tcp(%s:%s)/%s?charset=utf8mb4&collation=utf8mb4_unicode_ci&parseTime=false&timeout=10s",
			user.String(), c.DBHost, c.DBPort, c.DBName)
	}
	return c.DatabasePath + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
