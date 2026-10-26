package entrymcu

import (
	"testing"
	"time"
)

// Tests isTerminalResult, checkUpStatusFor, certifiableResult, and
// displayHcValidityStatus. No HTTP handler and no database write.
// User instruction: "Tem Unfit, Need Follow up, incomplete is open
// Fit, FWN and Unfit is closed" and do not revise stored values.
func TestOpenCloseFollowsResult(t *testing.T) {
	closed := []string{"Fit", "FitWithNote", "Unfit"}
	open := []string{"TemporaryUnfit", "NeedFollowUp", "IncompleteResult", ""}
	for _, result := range closed {
		if !isTerminalResult(result) || checkUpStatusFor(isTerminalResult(result)) != "Close" {
			t.Fatalf("%s should close the checkup", result)
		}
	}
	for _, result := range open {
		if isTerminalResult(result) || checkUpStatusFor(isTerminalResult(result)) != "Open" {
			t.Fatalf("%s should leave the checkup open", result)
		}
	}
	if !certifiableResult("Fit") || !certifiableResult("FitWithNote") {
		t.Fatal("Fit and Fit with note are the certificate results")
	}
	for _, result := range []string{"Unfit", "TemporaryUnfit", "NeedFollowUp", "IncompleteResult"} {
		if certifiableResult(result) {
			t.Fatalf("%s must not get a certificate", result)
		}
	}
}

func TestStandingIgnoresStoredCloseForOpenResults(t *testing.T) {
	closeStatus := "Close"
	until := time.Now().AddDate(0, 0, 90).Format(dateOnly)
	today := time.Now()
	for _, result := range []string{"TemporaryUnfit", "NeedFollowUp", "IncompleteResult"} {
		if got := displayHcValidityStatus(nil, &closeStatus, &until, &result, today); got != "" {
			t.Fatalf("%s stored Close still showed standing %q", result, got)
		}
	}
	unfit := "Unfit"
	if got := displayHcValidityStatus(nil, &closeStatus, &until, &unfit, today); got != "Archived / Exit" {
		t.Fatalf("Unfit standing = %q", got)
	}
	fit := "Fit"
	if got := displayHcValidityStatus(nil, &closeStatus, &until, &fit, today); got != "Valid" {
		t.Fatalf("Fit standing = %q", got)
	}
}
