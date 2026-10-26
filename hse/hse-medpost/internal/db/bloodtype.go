package db

import "fmt"

// LatestBloodTypeSQL returns a correlated subquery that yields a patient's
// blood type from their most recent checkup that has one. Blood type lives
// on McuRecord (see migrations and entrymcu.EnsureBloodTypeColumn), so
// patient-level views use this instead of reading a Patient column.
//
// "Most recent" is ReviewDate, then Timestamp, then McuRecordID as a
// tiebreak. Checkups with an empty BloodType are skipped, so a later
// checkup that didn't record one doesn't hide an earlier value.
//
// patientAlias is the alias of the Patient table in the outer query.
func LatestBloodTypeSQL(patientAlias string) string {
	return fmt.Sprintf(`(
		SELECT b.BloodType FROM McuRecord b
		WHERE b.FK_PatientID = %[1]s.PatientID AND TRIM(COALESCE(b.BloodType,'')) <> ''
		ORDER BY b.ReviewDate DESC, b.Timestamp DESC, b.McuRecordID DESC
		LIMIT 1)`, patientAlias)
}
