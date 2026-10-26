package staff

import (
	"database/sql"

	"golang.org/x/crypto/bcrypt"
)

// EnsureAdminAccount creates the seed admin if AppUserTable has no admin row.
// Needed on MariaDB after applying the schema (procedures exist, but no login).
func EnsureAdminAccount(database *sql.DB) error {
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM AppUserTable WHERE Role = 'admin'`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	res, err := database.Exec(
		`INSERT INTO MedpostStaffTable (StaffName, Position, Status) VALUES (?, ?, 'Active')`,
		"Administrator", "Admin",
	)
	if err != nil {
		return err
	}
	staffID, err := res.LastInsertId()
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("101112"), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = database.Exec(
		`INSERT INTO AppUserTable (FK_StaffID, Username, PasswordHash, Role) VALUES (?, ?, ?, 'admin')`,
		staffID, "admin", string(hash),
	)
	return err
}

// EnsureAdminLogin upgrades legacy seed username 000001 → admin.
func EnsureAdminLogin(database *sql.DB) error {
	_, err := database.Exec(`UPDATE AppUserTable SET Username = 'admin' WHERE Username = '000001' AND Role = 'admin'`)
	return err
}

// EnsurePositionRoles seeds default position → role mappings when the table is empty.
func EnsurePositionRoles(database *sql.DB) error {
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM PositionRoleTable`).Scan(&n); err != nil {
		// Table may not exist yet on very old DBs; caller should migrate first.
		return err
	}
	if n > 0 {
		return nil
	}
	defaults := []struct {
		name, role string
	}{
		{"Chief Medical Officer", "admin"},
		{"Site Doctor", "staff"},
		{"Medpost Nurse", "staff"},
		{"Administrator", "admin"},
	}
	for _, d := range defaults {
		if _, err := database.Exec(
			`INSERT INTO PositionRoleTable (PositionName, Role) VALUES (?, ?)`,
			d.name, d.role,
		); err != nil {
			return err
		}
	}
	return nil
}

// EnsureManagerRoleRetired moves accounts and position mappings off the
// removed manager role onto admin. Safe to run on every startup: after the
// first run there is nothing left with role manager.
func EnsureManagerRoleRetired(database *sql.DB) error {
	if _, err := database.Exec(`UPDATE AppUserTable SET Role = 'admin' WHERE Role = 'manager'`); err != nil {
		return err
	}
	_, err := database.Exec(`UPDATE PositionRoleTable SET Role = 'admin' WHERE Role = 'manager'`)
	return err
}
