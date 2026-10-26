package entrymcu

import (
	"database/sql"
	"fmt"
)

// EnsureBloodTypeColumn moves blood type from Patient to McuRecord.
//
// Blood type is a checkup measurement, like height, so it now lives on the
// checkup row. On first run this adds McuRecord.BloodType and copies each
// patient's existing Patient.BloodType onto that patient's latest checkup,
// the same "latest" rule db.LatestBloodTypeSQL uses for display. The copy runs
// only in the same pass that adds the column, so a value cleared on a checkup
// later is never refilled from the old Patient column.
//
// Patient.BloodType is left in place. Nothing writes to it anymore; it can be
// dropped in a later migration once the new code has run in production.
//
// The production MariaDB database isn't migrated by this code path; see
// migrations/mysql/011_mcu_bloodtype.sql for the equivalent change.
func EnsureBloodTypeColumn(database *sql.DB) error {
	hasColumn, err := mcuRecordHasColumn(database, "BloodType")
	if err != nil {
		return fmt.Errorf("check McuRecord schema: %w", err)
	}
	if hasColumn {
		return nil
	}

	if _, err := database.Exec(`ALTER TABLE McuRecord ADD COLUMN BloodType TEXT NULL`); err != nil {
		return fmt.Errorf("add McuRecord.BloodType: %w", err)
	}
	if _, err := database.Exec(backfillBloodTypeToLatestCheckUpSQL); err != nil {
		return fmt.Errorf("backfill McuRecord.BloodType from Patient: %w", err)
	}
	return nil
}

// backfillBloodTypeToLatestCheckUpSQL writes Patient.BloodType onto each
// patient's most recent checkup. The window function picks the latest row per
// patient inside a subquery, which SQLite allows to reference McuRecord even
// though the outer statement updates it.
const backfillBloodTypeToLatestCheckUpSQL = `
UPDATE McuRecord
SET BloodType = (SELECT p.BloodType FROM Patient p WHERE p.PatientID = McuRecord.FK_PatientID)
WHERE McuRecordID IN (
	SELECT McuRecordID FROM (
		SELECT McuRecordID,
		       ROW_NUMBER() OVER (PARTITION BY FK_PatientID
		                          ORDER BY ReviewDate DESC, Timestamp DESC, McuRecordID DESC) AS rn
		FROM McuRecord
	) WHERE rn = 1
)
AND EXISTS (
	SELECT 1 FROM Patient p
	WHERE p.PatientID = McuRecord.FK_PatientID AND TRIM(COALESCE(p.BloodType,'')) <> ''
)`
