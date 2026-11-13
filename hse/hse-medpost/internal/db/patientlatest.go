package db

import (
	"database/sql"
	"fmt"
)

// Execer is the part of *sql.DB and *sql.Tx the sync needs, so a visit save can
// run it inside its own transaction.
type Execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// latestVisitValueSQL is a correlated subquery for one column of a patient's
// most recent checkup that has a non-empty value. "Most recent" is ReviewDate,
// then Timestamp, then McuRecordID, the same order LatestBloodTypeSQL uses.
// Checkups with no value are skipped, so they don't hide an earlier one.
func latestVisitValueSQL(column, patientRef string) string {
	return fmt.Sprintf(`(
		SELECT c.%[1]s FROM McuRecord c
		WHERE c.FK_PatientID = %[2]s AND c.%[1]s IS NOT NULL AND TRIM(CAST(c.%[1]s AS CHAR)) <> ''
		ORDER BY c.ReviewDate DESC, c.Timestamp DESC, c.McuRecordID DESC
		LIMIT 1)`, column, patientRef)
}

// syncPatientSQL sets Patient.FK_CompanyID, WorkPosition, BloodType and
// HeightCm from the latest checkup that has each. A patient with no such
// checkup keeps whatever the column already holds.
var syncPatientSQL = `UPDATE Patient SET
	FK_CompanyID = COALESCE(` + latestVisitValueSQL("FK_CompanyID", "Patient.PatientID") + `, FK_CompanyID),
	WorkPosition = COALESCE(` + latestVisitValueSQL("WorkPosition", "Patient.PatientID") + `, WorkPosition),
	BloodType = COALESCE(` + latestVisitValueSQL("BloodType", "Patient.PatientID") + `, BloodType),
	HeightCm = COALESCE(` + latestVisitValueSQL("HeightCm", "Patient.PatientID") + `, HeightCm)`

// SyncPatientFromLatestVisit refreshes the patient's company, work position,
// blood type and height from their latest checkups. These columns are derived,
// not entered: staff record them on the visit and this keeps the patient row
// in step, so a later checkup can start from them.
//
// Call it after anything that inserts, edits or deletes a McuRecord.
func SyncPatientFromLatestVisit(e Execer, patientID int64) error {
	if _, err := e.Exec(syncPatientSQL+` WHERE PatientID = ?`, patientID); err != nil {
		return fmt.Errorf("sync patient %d from latest visit: %w", patientID, err)
	}
	return nil
}

// SyncAllPatientsFromLatestVisit runs the same derivation for every patient. It
// is idempotent, so it runs at startup to backfill existing rows and to repair
// any drift from writes that bypassed the app.
func SyncAllPatientsFromLatestVisit(e Execer) error {
	if _, err := e.Exec(syncPatientSQL); err != nil {
		return fmt.Errorf("sync all patients from latest visit: %w", err)
	}
	return nil
}
