package entrymcu

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"hse-medpost/internal/dates"
)

// This file ports the legacy calculateValidityDate/closeParentRecord logic
// from _reference/entry-mcu/google_sheet/Code.js almost line-for-line, since
// the user asked explicitly not to lose any feature during the port. Where
// the Go version differs from the original it's noted inline.

var normalizeKeyRe = regexp.MustCompile(`[\s_-]+`)

func normalizeKey(s string) string {
	return normalizeKeyRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "")
}

// isTerminalResult reports whether a checkup's ResultCriteria is a finished
// decision. Fit, FitWithNote, and Unfit close the checkup. Temporarily
// unfit, need follow up, and incomplete stay open because a later visit is
// still expected on this record. CheckUpStatus is that same fact stored as
// Open or Close. A decision counts as soon as it's entered.
func isTerminalResult(resultCriteria string) bool {
	switch resultCriteria {
	case "Fit", "FitWithNote", "Unfit":
		return true
	default:
		return false
	}
}

// certifiableResult is the subset of closed results that can carry a
// fitness certificate. Unfit and Temporarily Unfit get a separate red
// written opinion (adverseResult), not this clearance.
func certifiableResult(resultCriteria string) bool {
	switch resultCriteria {
	case "Fit", "FitWithNote":
		return true
	default:
		return false
	}
}

// adverseResult is a written opinion that the worker is not cleared.
// Temporarily Unfit stays an open checkup. Unfit stays closed.
func adverseResult(resultCriteria string) bool {
	switch resultCriteria {
	case "TemporaryUnfit", "Unfit":
		return true
	default:
		return false
	}
}

// checkUpStatusFor maps the already-computed isTerminalResult verdict to
// CheckUpStatus's two legacy values (Open/Close — per the legacy sheet's BJ
// column, see updateWeeklyStatusesBody_'s rule comments in
// _reference/entry-mcu/google_sheet/Code.js). It exists only to name the
// string encoding: CheckUpStatus carries no information IsTerminal doesn't
// already have (see CreateMcuRecord/UpdateMcuRecord, which compute
// isTerminalResult once and feed it to both fields, computed server-side
// rather than trusted from the client). "Archived / Exit" is a separate axis
// entirely: legacy stores it in BM ("HC Validity" / HcValidityStatus here,
// alongside "Renewed by <id>"), not BJ — CreateMcuRecord/UpdateMcuRecord pass
// the client's HcValidityStatus straight through, unrelated to this.
//
// IsTerminal feeds fetchExpiringSoon's renewal-tracking filter (a pending
// case has no firm decision to expire) and, together with
// closeParentRecord's unconditional supersede-to-false below, "current"
// means not superseded by a newer record and has a resolved outcome.
func checkUpStatusFor(isTerminal bool) string {
	if isTerminal {
		return "Close"
	}
	return "Open"
}

// hcValidityLadder buckets days-until-expiry into the legacy sheet's warning
// ladder (BM column default bands, updateWeeklyStatusesBody_ in
// _reference/entry-mcu/google_sheet/Code.js:4066-4077) — the thresholds a
// CMO uses to decide how urgently to chase a renewal.
func hcValidityLadder(daysLeft int) string {
	switch {
	case daysLeft >= 60:
		return "Valid"
	case daysLeft >= 30:
		return "2 Months Warning"
	case daysLeft >= 14:
		return "1 Month Warning"
	case daysLeft >= 7:
		return "2 Weeks Urgent"
	case daysLeft >= 1:
		return "1 Week Urgent"
	default:
		return "ReEvaluate / Overdue"
	}
}

// displayHcValidityStatus computes what to show for HcValidityStatus. The
// legacy sheet recomputes this ladder for every row on a weekly time-driven
// trigger (updateWeeklyStatusesBody_, _reference/entry-mcu/google_sheet/
// Code.js:3502-3545); this app has no scheduler, so instead of persisting a
// value that would go stale between edits, it's recomputed fresh on every
// read (report rows only — see report.go) against the current date and the
// row's own resultCriteria. The stored column itself (McuRecord.
// HcValidityStatus, still what CreateMcuRecord/UpdateMcuRecord read and
// write) is left untouched by this and keeps holding only the two explicit,
// non-time-based overrides: "Archived / Exit" (the checkbox) and "Renewed by
// <id>" (closeParentRecord's supersede stamp) — both are returned verbatim
// here since neither should be replaced by a ladder band.
//
// Two more legacy auto-archive rules are ported here rather than as a
// stored-column mutation, for the same no-scheduler reason: R-Unfit (Code.js
// ~3926-3945) archives a "Unfit" result immediately, independent of any day
// count, since it's a terminal exit decision, not a pending one; R2
// (Code.js ~4038-4066) archives any other decided (CheckUpStatus="Close")
// record whose ResultValidUntil is more than checkupMaxAgeDays (180) days in
// the past, instead of showing a stale "ReEvaluate / Overdue" band forever.
// Both are safe to leave undone here specifically because supersession
// (a renewal linking back via parentRecordId) is already handled at
// save-time by closeParentRecord, which stamps "Renewed by <id>" into the
// stored column before this function ever runs — so by the time a record
// reaches these two checks it either wasn't renewed, or the "Renewed by "
// branch above already returned. Everything else ladders off
// ResultValidUntil for decided records, or shows nothing for records still
// pending a decision (mirrors legacy's "_" placeholder for BJ=Open).
func displayHcValidityStatus(stored, checkUpStatus, resultValidUntil, resultCriteria *string, today time.Time) string {
	if stored != nil {
		s := strings.TrimSpace(*stored)
		if s == "Archived / Exit" || strings.HasPrefix(s, "Renewed by ") {
			return s
		}
	}
	if resultCriteria != nil && *resultCriteria == "Unfit" {
		return "Archived / Exit"
	}
	// Open results have no firm decision to expire. This is read from the
	// result, not the stored CheckUpStatus, so an older Temporarily Unfit
	// row that was saved as Close does not keep a validity ladder.
	if resultCriteria != nil {
		switch *resultCriteria {
		case "TemporaryUnfit", "NeedFollowUp", "IncompleteResult":
			return ""
		}
	}
	if checkUpStatus == nil || *checkUpStatus != "Close" || resultValidUntil == nil {
		return ""
	}
	vu, err := time.Parse(dateOnly, *resultValidUntil)
	if err != nil {
		return ""
	}
	daysLeft := int(vu.Sub(today.Truncate(24*time.Hour)).Hours() / 24)
	if daysLeft < 0 && -daysLeft > checkupMaxAgeDays {
		return "Archived / Exit"
	}
	return hcValidityLadder(daysLeft)
}

// ShownValidity is the standing a reader sees for one checkup. Stored
// Archived / Exit and Renewed by stay as written. Unfit is Archived / Exit.
// A closed Fit or Fit with note date falls on the validity ladder.
func ShownValidity(stored, status, until, result string, today time.Time) string {
	return displayHcValidityStatus(textPtr(stored), textPtr(status), textPtr(until), textPtr(result), today)
}

func textPtr(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

// isPreviousFitAnchorCriteria reports whether a checkup criteria name anchors
// its validity date off a *previous* checkup rather than today — Code.js's
// isPreviousFitAnchorCriteria_.
func isPreviousFitAnchorCriteria(criteriaName string) bool {
	switch normalizeKey(criteriaName) {
	case "biannual", "fitfortask", "fitfortasktype", "fitforwork", "update":
		return true
	default:
		return false
	}
}

// isFitForTaskCriteria — Code.js's isFitForTaskCriteria_.
func isFitForTaskCriteria(criteriaName string) bool {
	switch normalizeKey(criteriaName) {
	case "fitfortask", "fitfortasktype", "fitforwork":
		return true
	default:
		return false
	}
}

// monthsToDays — Code.js's monthsToDays_: only the four standard periods
// convert exactly (months×30); anything else falls back to a 360-day year.
func monthsToDays(months int) int {
	switch months {
	case 3, 6, 9, 12:
		return months * 30
	default:
		return 360
	}
}

const dateOnly = "2006-01-02"

// checkupMaxAgeDays/checkupHardMaxDays — Code.js's MCU_CHECKUP_MAX_AGE_DAYS /
// MCU_CHECKUP_HARD_MAX_DAYS. Beyond 180 days old, a checkup can only be
// saved if flagged IsShortProject (the client asks "is this a short
// project?" and resets the form on "no" — see mcu.js); beyond 365 days it
// can never be saved regardless of that flag.
const (
	checkupMaxAgeDays  = 180
	checkupHardMaxDays = 360
)

// validateCheckupDate ports Code.js's validateCheckupDateForSave_: the
// server-side half of the short-project gate, since the client-side confirm
// dialog alone can't be trusted (same reasoning as calculateValidityDate
// never trusting a client-computed ResultValidUntil).
func validateCheckupDate(dateCheckUp string, isShortProject bool) error {
	return validateCheckupDateOn(dateCheckUp, isShortProject, dates.Today())
}

// validateCheckupDateOn takes today as a YYYY-MM-DD clinic day, so the rule
// compares text and never depends on the server's timezone.
func validateCheckupDateOn(dateCheckUp string, isShortProject bool, today string) error {
	dateCheckUp = strings.TrimSpace(dateCheckUp)
	if dateCheckUp == "" {
		return fmt.Errorf("date of checkup is required")
	}
	if _, err := dates.Normalize(dateCheckUp); err != nil {
		return fmt.Errorf("invalid date of checkup")
	}
	if dateCheckUp > today {
		return fmt.Errorf("date of checkup cannot be in the future")
	}
	diffDays, err := dates.DaysBetween(dateCheckUp, today)
	if err != nil {
		return fmt.Errorf("invalid date of checkup")
	}
	if diffDays > checkupHardMaxDays {
		return fmt.Errorf("date of checkup is more than %d days in the past (%d days ago) — entry cannot be saved", checkupHardMaxDays, diffDays)
	}
	if diffDays > checkupMaxAgeDays && !isShortProject {
		return fmt.Errorf("date of checkup is more than %d days in the past — entry cannot be saved unless flagged as a short project", checkupMaxAgeDays)
	}
	return nil
}

// validateSubmissionDate applies the same rules as the date of checkup: not in
// the future and not more than checkupHardMaxDays old.
func validateSubmissionDate(submissionDate string) error {
	return validateSubmissionDateOn(submissionDate, dates.Today())
}

func validateSubmissionDateOn(submissionDate, today string) error {
	submissionDate = strings.TrimSpace(submissionDate)
	if _, err := dates.Normalize(submissionDate); err != nil || submissionDate == "" {
		return fmt.Errorf("invalid submission date")
	}
	if submissionDate > today {
		return fmt.Errorf("submission date cannot be in the future")
	}
	diffDays, err := dates.DaysBetween(submissionDate, today)
	if err != nil {
		return fmt.Errorf("invalid submission date")
	}
	if diffDays > checkupHardMaxDays {
		return fmt.Errorf("submission date is more than %d days in the past (%d days ago) — entry cannot be saved", checkupHardMaxDays, diffDays)
	}
	return nil
}

func addValidityDays(baseDate string, months int) *string {
	t, err := time.Parse(dateOnly, baseDate)
	if err != nil {
		return nil
	}
	out := t.AddDate(0, 0, monthsToDays(months)).Format(dateOnly)
	return &out
}

// validityInput mirrors the fields calculateValidityDate reads off the
// in-progress McuRecord, resolved to the plain strings the legacy function
// worked with (criteria/task category names, not our FK ids).
type validityInput struct {
	resultCriteria       string
	criteriaName         string // resolved CheckUpCriteriaTable.Name
	taskCategoryName     string // resolved TaskCategoryTable.Name (legacy "taskType")
	validityPeriodMonths *int64
	dateCheckUp          string
	parentRecordID       *int64
	patientID            int64
}

// foodHandlerSixMonthCriteria — checkup criteria that get capped to 6
// months' validity for a Food Handler, anchored off *that visit's own*
// DateCheckUp (Pre Employment/Annual are not anchor criteria — see
// isPreviousFitAnchorCriteria — so calculateValidityDate uses dateCheckUp
// directly for these). The legacy sheet (Code.js:2542) only special-cased
// Pre Employment; the user extended this to Annual too.
var foodHandlerSixMonthCriteria = map[string]bool{
	"preemployment": true,
	"annual":        true,
}

// foodHandlerBiannualMonths — a Food Handler's Bi-annual recheck (physical +
// rectal swab only, see the checkup-form hint in mcu.js) is valid 12
// months, not 6, and — critically — is anchored off the *preceding* Pre
// Employment/Annual checkup's date (Bi-annual IS an anchor criteria, see
// isPreviousFitAnchorCriteria), never off today's own visit date. This
// bridges the gap: the full checkup's own 6-month cert lapses at month 6,
// the Bi-annual recheck at month 6 extends coverage through month 12 (when
// the next full Annual is due) without another full battery of tests, and
// the cycle repeats (Annual again at month 12 → 6mo from ITS date → next
// Bi-annual at month 18 → 12mo from month 12, etc). Anchoring off the fixed
// preceding date rather than today's date is deliberate: if it anchored off
// today, delaying the actual recheck would still hand out a fresh 12-month
// window from whenever staff got around to it, silently extending coverage
// the later they left it — anchoring off the previous record's date means a
// late recheck can't extend anything, at worst it just arrives already
// past due.
const foodHandlerBiannualMonths = 12

// selectedMonths — Code.js's selectedMonths_, including the Food Handler
// exceptions described above.
func selectedMonths(in validityInput) int {
	months := 12
	if in.resultCriteria == "Fit" || in.resultCriteria == "FitWithNote" {
		if in.validityPeriodMonths != nil {
			m := int(*in.validityPeriodMonths)
			if m == 3 || m == 6 || m == 9 || m == 12 {
				months = m
			}
		}
	} else if in.validityPeriodMonths != nil {
		months = int(*in.validityPeriodMonths)
	}

	isFoodHandler := strings.Contains(strings.ToLower(in.taskCategoryName), "food handler")
	if !isFoodHandler {
		return months
	}
	key := normalizeKey(in.criteriaName)

	if foodHandlerSixMonthCriteria[key] {
		if in.resultCriteria == "Fit" {
			return 6
		}
		if in.resultCriteria == "FitWithNote" {
			if months < 6 {
				return months
			}
			return 6
		}
	}
	if key == "biannual" {
		if in.resultCriteria == "Fit" {
			return foodHandlerBiannualMonths
		}
		if in.resultCriteria == "FitWithNote" {
			if months < foodHandlerBiannualMonths {
				return months
			}
			return foodHandlerBiannualMonths
		}
	}
	return months
}

// getParentCheckUpDate looks up a parent McuRecord's DateCheckUp (not
// ReviewDate — the legacy sheet anchors off DateCheckUp specifically, see
// Code.js:2647).
func (a *API) getParentCheckUpDate(parentID int64) (string, bool) {
	var date sql.NullString
	err := a.DB.QueryRow(`SELECT DateCheckUp FROM McuRecord WHERE McuRecordID = ?`, parentID).Scan(&date)
	if err != nil || !date.Valid || date.String == "" {
		return "", false
	}
	return date.String, true
}

// getMostRecentNonBiannualFitDate — Code.js's getMostRecentNonBiannualFitDate:
// the Biannual/Update fallback when no parent record was picked. Finds this
// patient's most recent Fit/FitWithNote checkup, excluding anchor-criteria
// rows themselves, strictly before beforeDate.
func (a *API) getMostRecentNonBiannualFitDate(patientID int64, beforeDate string) (string, bool) {
	rows, err := a.DB.Query(`
		SELECT m.DateCheckUp, COALESCE(c.Name,'')
		FROM McuRecord m
		LEFT JOIN CheckUpCriteriaTable c ON c.CheckUpCriteriaID = m.FK_CheckUpCriteriaID
		WHERE m.FK_PatientID = ? AND m.ResultCriteria IN ('Fit','FitWithNote') AND m.DateCheckUp IS NOT NULL`,
		patientID)
	if err != nil {
		return "", false
	}
	defer rows.Close()

	beforeT, beforeErr := time.Parse(dateOnly, beforeDate)
	var bestDate string
	var bestT time.Time
	for rows.Next() {
		var d, criteriaName string
		if err := rows.Scan(&d, &criteriaName); err != nil {
			continue
		}
		if isPreviousFitAnchorCriteria(criteriaName) {
			continue
		}
		t, err := time.Parse(dateOnly, d)
		if err != nil {
			continue
		}
		if beforeErr == nil && !t.Before(beforeT) {
			continue
		}
		if bestDate == "" || t.After(bestT) {
			bestDate, bestT = d, t
		}
	}
	if err := rows.Err(); err != nil {
		return "", false
	}
	return bestDate, bestDate != ""
}

// calculateValidityDate ports Code.js's calculateValidityDate almost
// line-for-line (see file header). Returns nil when the record has no
// computable validity (Unfit/NeedFollowUp/TemporaryUnfit/IncompleteResult,
// or a missing anchor/checkup date the legacy version also returns '_' for).
func (a *API) calculateValidityDate(in validityInput) *string {
	switch in.resultCriteria {
	case "Unfit", "NeedFollowUp", "TemporaryUnfit", "IncompleteResult":
		return nil
	}

	months := selectedMonths(in)

	// Fit for Task / Biannual / Update: ValidUntil = previous checkup date + duration.
	if isPreviousFitAnchorCriteria(in.criteriaName) {
		var anchor string
		var ok bool
		if in.parentRecordID != nil {
			anchor, ok = a.getParentCheckUpDate(*in.parentRecordID)
		}
		if !ok && !isFitForTaskCriteria(in.criteriaName) {
			anchor, ok = a.getMostRecentNonBiannualFitDate(in.patientID, in.dateCheckUp)
		}
		if ok {
			return addValidityDays(anchor, months)
		}
		// Fit for Task requires a parent — without one, fall back to this visit's date.
		if in.dateCheckUp == "" {
			return nil
		}
		return addValidityDays(in.dateCheckUp, months)
	}

	// Follow Up (and any other row with a parent CheckUp ID): anchor off the
	// parent's date, not this visit's.
	if in.parentRecordID != nil {
		if anchor, ok := a.getParentCheckUpDate(*in.parentRecordID); ok {
			return addValidityDays(anchor, months)
		}
	}

	if in.dateCheckUp == "" {
		return nil
	}
	return addValidityDays(in.dateCheckUp, months)
}

// closeParentRecord ports Code.js's closeParentRecord: when a new record
// links to a parent via FK_ParentRecordID, that parent — and any *sibling*
// record that already points at the same parent (matches the legacy
// isParent||isSibling check) — gets closed out: CheckUpStatus becomes
// 'Close', HealthCertificateFileName is stamped if given, HcValidityStatus
// notes which record superseded it, and IsTerminal flips to false since a
// renewed/superseded record no longer counts as current.
func closeParentRecord(tx *sql.Tx, parentID int64, newCheckUpID string, certFileName *string, excludeID int64) error {
	rows, err := tx.Query(
		`SELECT McuRecordID FROM McuRecord WHERE (McuRecordID = ? OR FK_ParentRecordID = ?) AND McuRecordID != ?`,
		parentID, parentID, excludeID,
	)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, id := range ids {
		if _, err := tx.Exec(
			`UPDATE McuRecord SET CheckUpStatus = 'Close', IsTerminal = 0,
			 HealthCertificateFileName = COALESCE(?, HealthCertificateFileName),
			 HcValidityStatus = ?
			 WHERE McuRecordID = ?`,
			certFileName, "Renewed by "+newCheckUpID, id,
		); err != nil {
			return err
		}
	}
	return nil
}
