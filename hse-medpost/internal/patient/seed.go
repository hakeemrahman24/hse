package patient

import "database/sql"

// EnsureNationalitySeed seeds the seven real production nationality values
// (given during the Settings-page discussion — see plan §2/§3) when the
// table is empty, same self-migration shape as staff.EnsurePositionRoles /
// company.EnsureEntityTypeSeed.
func EnsureNationalitySeed(database *sql.DB) error {
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM NationalityTable`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	names := []string{"Indonesia", "Philippines", "Malaysia", "Singapore", "USA", "UK", "China"}
	for _, name := range names {
		if _, err := database.Exec(`INSERT INTO NationalityTable (Name) VALUES (?)`, name); err != nil {
			return err
		}
	}
	return nil
}
