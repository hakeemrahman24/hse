package entrymcu

import "database/sql"

// EnsureCheckUpCriteriaSeed keeps the checkup-purpose list at the current
// five names. An empty table is filled; an older table is renamed in place
// (Pre Employment → PreEmployment, Bi-annual → Bi-Annual) and any missing
// purpose is inserted. A second spelling that is its own row stays, so an
// archived value can still be shown and restored in Settings.
func EnsureCheckUpCriteriaSeed(database *sql.DB) error {
	return withTx(database, func(database *sql.Tx) error {
		for from, to := range checkupPurposeRenames {
			if err := renameLookup(database, "CheckUpCriteriaTable", "CheckUpCriteriaID", from, to); err != nil {
				return err
			}
		}
		return ensureNames(database, "CheckUpCriteriaTable", checkupPurposeNames)
	})
}

// occupationalRiskNames is the active checklist. A box stays only when it
// changes the exam. Pilot and diver stay job titles in Work position.
var occupationalRiskNames = []string{
	"Food Handler",
	"Heavy Equipment or Crane Operator",
	"Professional Drivers",
	"Health care worker",
	"Seafarer",
	"Work at Height",
	"Confined space",
	"Respirator or SCBA",
	"Work in Extreme Temperature",
	"Fire & Emergency Crew",
	"Shift Worker",
	"Business Travelers",
}

// retiredOccupationalRiskNames stay in the table so an old checkup still
// shows the name it was saved with. They are not offered on a new checkup.
var retiredOccupationalRiskNames = []string{
	"Other Professional (Pilot, Seafarer, Diver)",
	"Remote Site Visitor",
	"Confined Space or SCBA/Respirator Users",
}

// EnsureTaskCategorySeed fills an empty table with the active checklist.
// An existing table is revised by EnsureOccupationalRiskRevision.
func EnsureTaskCategorySeed(database *sql.DB) error {
	return withTx(database, func(database *sql.Tx) error {
		var n int
		if err := database.QueryRow(`SELECT COUNT(*) FROM TaskCategoryTable`).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
		for _, name := range occupationalRiskNames {
			if _, err := database.Exec(`INSERT INTO TaskCategoryTable (Name) VALUES (?)`, name); err != nil {
				return err
			}
		}
		return nil
	})
}

// EnsureOccupationalRiskRevision applies the checklist to a database that
// already has the older rows. Health Professionals is renamed in place.
// Seafarer, Confined space, and Respirator or SCBA are added. The bundled
// professional row, Remote Site Visitor, and the combined confined-space
// row are archived. Stored checkup names are rewritten only for the rename.
func EnsureOccupationalRiskRevision(database *sql.DB) error {
	return withTx(database, func(database *sql.Tx) error {
		const fromName = "Health Professionals"
		const toName = "Health care worker"
		var fromID int64
		err := database.QueryRow(`SELECT TaskCategoryID FROM TaskCategoryTable WHERE Name = ?`, fromName).Scan(&fromID)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil {
			var toID int64
			toErr := database.QueryRow(`SELECT TaskCategoryID FROM TaskCategoryTable WHERE Name = ?`, toName).Scan(&toID)
			if toErr == sql.ErrNoRows {
				if _, err := database.Exec(`UPDATE TaskCategoryTable SET Name = ? WHERE TaskCategoryID = ?`, toName, fromID); err != nil {
					return err
				}
			} else if toErr != nil {
				return toErr
			} else if _, err := database.Exec(`UPDATE TaskCategoryTable SET Status = 'Archived' WHERE TaskCategoryID = ?`, fromID); err != nil {
				return err
			}
			if err := rewriteTaskCategoryName(database, fromName, toName); err != nil {
				return err
			}
		}
		if err := ensureNames(database, "TaskCategoryTable", occupationalRiskNames); err != nil {
			return err
		}
		for _, name := range retiredOccupationalRiskNames {
			if _, err := database.Exec(`INSERT INTO TaskCategoryTable (Name, Status) VALUES (?, 'Archived')`, name); err != nil && !isUniqueViolation(err) {
				return err
			}
			if _, err := database.Exec(`UPDATE TaskCategoryTable SET Status = 'Archived' WHERE Name = ?`, name); err != nil {
				return err
			}
		}
		return nil
	})
}

// taskCategoryTriggerWords are the first-run phrases for each occupational
// health risk. A later admin edit is kept: the seed only fills rows whose
// TriggerWords column is still NULL.
var taskCategoryTriggerWords = map[string]string{
	"Food Handler":                      "food handler, cook",
	"Heavy Equipment or Crane Operator": "heavy equipment, crane",
	"Professional Drivers":              "driver, drivers",
	"Health care worker":                "health professional, nurse, doctor, paramedic, medic",
	"Seafarer":                          "seafarer",
	"Work at Height":                    "work at height",
	"Confined space":                    "confined space",
	"Respirator or SCBA":                "scba, respirator",
	"Work in Extreme Temperature":       "extreme temperature",
	"Fire & Emergency Crew":             "firefighter, fire crew, emergency crew",
	"Shift Worker":                      "shift worker, night shift",
	"Business Travelers":                "business travel",
}

// EnsureTaskCategoryTriggerWordSeed fills trigger words once. An empty
// string is an admin choice to match nothing, so only NULL is seeded.
func EnsureTaskCategoryTriggerWordSeed(database *sql.DB) error {
	// The column add is schema DDL, so it runs before the data transaction.
	if err := EnsureTaskCategoryTriggerWordsColumn(database); err != nil {
		return err
	}
	return withTx(database, func(database *sql.Tx) error {
		for name, words := range taskCategoryTriggerWords {
			if _, err := database.Exec(
				`UPDATE TaskCategoryTable SET TriggerWords = ? WHERE Name = ? AND TriggerWords IS NULL`,
				words, name,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

// EnsureEmailNotificationConfigSeed inserts the single disabled-by-default
// McuEmailNotificationConfig row (id 1) if it doesn't exist yet — see
// emailnotify.go and migrations/008_mcu_email_notification.sql.
func EnsureEmailNotificationConfigSeed(database *sql.DB) error {
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM McuEmailNotificationConfig`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := database.Exec(`
		INSERT INTO McuEmailNotificationConfig
			(McuEmailNotificationConfigID, Enabled, SmtpPort, ExpiryThresholdDays, CheckPeriodMinutes)
		VALUES (1, 0, 587, 60, 1440)`)
	return err
}
