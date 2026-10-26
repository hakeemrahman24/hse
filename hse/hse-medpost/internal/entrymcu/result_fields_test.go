package entrymcu

import "testing"

func strPtr(s string) *string { return &s }

func TestResultNotesAndFollowUp(t *testing.T) {
	notes, followUp, err := validateResultDependentFields("Fit", strPtr("old note"), strPtr("old follow-up"))
	if err != nil || notes != nil || followUp != nil {
		t.Fatalf("Fit should store neither field, got notes=%v followUp=%v err=%v", notes, followUp, err)
	}

	for _, result := range []string{"FitWithNote", "TemporaryUnfit", "Unfit"} {
		_, _, err = validateResultDependentFields(result, nil, strPtr("come back"))
		if err == nil {
			t.Fatalf("%s requires result notes", result)
		}
		notes, followUp, err = validateResultDependentFields(result, strPtr("blood pressure"), strPtr("come back"))
		if err != nil {
			t.Fatal(err)
		}
		if notes == nil || *notes != "blood pressure" || followUp != nil {
			t.Fatalf("%s should keep notes and clear follow-up, notes=%v followUp=%v", result, notes, followUp)
		}
	}

	_, _, err = validateResultDependentFields("NeedFollowUp", strPtr("pending"), nil)
	if err == nil {
		t.Fatal("need follow up requires a recommendation")
	}
	notes, followUp, err = validateResultDependentFields("NeedFollowUp", strPtr("pending"), strPtr("repeat the blood count"))
	if err != nil {
		t.Fatal(err)
	}
	if notes != nil || followUp == nil || *followUp != "repeat the blood count" {
		t.Fatalf("need follow up should clear notes and keep follow-up, notes=%v followUp=%v", notes, followUp)
	}

	_, _, err = validateResultDependentFields("IncompleteResult", strPtr("labs still pending"), nil)
	if err == nil {
		t.Fatal("incomplete result requires a recommendation")
	}
	notes, followUp, err = validateResultDependentFields("IncompleteResult", strPtr("labs still pending"), strPtr("repeat the blood count"))
	if err != nil {
		t.Fatal(err)
	}
	if notes != nil || followUp == nil {
		t.Fatalf("incomplete should clear notes and keep follow-up, notes=%v followUp=%v", notes, followUp)
	}
}

func TestRequiredCheckupFields(t *testing.T) {
	companyID := int64(4)
	criteriaID := int64(2)
	complete := McuRecord{
		CheckUpCriteriaID: &criteriaID,
		WorkLocation:      strPtr("RemoteSiteWorker"),
		CompanyID:         &companyID,
		WorkPosition:      strPtr("Medic"),
		ResultCriteria:    strPtr("Fit"),
	}
	if err := validateRequiredCheckupFields(complete); err != nil {
		t.Fatal(err)
	}
	missingPurpose := complete
	missingPurpose.CheckUpCriteriaID = nil
	if err := validateRequiredCheckupFields(missingPurpose); err == nil {
		t.Fatal("checkup purpose is required")
	}
	missingLocation := complete
	missingLocation.WorkLocation = nil
	if err := validateRequiredCheckupFields(missingLocation); err == nil {
		t.Fatal("work location is required")
	}
	missingCompany := complete
	missingCompany.CompanyID = nil
	if err := validateRequiredCheckupFields(missingCompany); err == nil {
		t.Fatal("company is required")
	}
	missingPosition := complete
	missingPosition.WorkPosition = strPtr("  ")
	if err := validateRequiredCheckupFields(missingPosition); err == nil {
		t.Fatal("work position is required")
	}
	missingResult := complete
	missingResult.ResultCriteria = nil
	if err := validateRequiredCheckupFields(missingResult); err == nil {
		t.Fatal("result is required")
	}
}
