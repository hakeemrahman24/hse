package importer

import (
	"reflect"
	"regexp"
	"testing"
)

// kycSpec mirrors the real id folder: <Name>_KTP or <Name>_PassportID, then Drive's " (1)".
var kycSpec = DocSpec{Field: "id_doc", Folder: "id", Pattern: regexp.MustCompile(`^(?P<name>.+)_(KTP|PassportID|ID)$`)}

// contractSpec carries a date: the day the contract was uploaded, which is the request's day.
var contractSpec = DocSpec{Field: "contract_doc", Folder: "contracts", Pattern: regexp.MustCompile(`^(?P<name>.+)_Contract_(?P<date>\d{8})$`)}

func TestOneRequestAndOneFileArePaired(t *testing.T) {
	got := PairFiles(kycSpec,
		[]Request{{ID: "WEP-AAAAA", Name: "Budi Santoso", At: "2026-08-13 09:00:00"}},
		[]string{"BudiSantoso_KTP.jpg"})
	if got.Matched["WEP-AAAAA"] != "BudiSantoso_KTP.jpg" {
		t.Fatalf("matched = %v", got.Matched)
	}
	if len(got.Missing)+len(got.Ambiguous) != 0 {
		t.Fatalf("nothing should be reported: %+v", got)
	}
}

func TestNamePunctuationAndCaseDoNotBreakTheMatch(t *testing.T) {
	got := PairFiles(kycSpec,
		[]Request{{ID: "WEP-AAAAA", Name: "As'adul O'Connor-Ray", At: "2026-08-13 09:00:00"}},
		[]string{"asadulOConnorRay_KTP.png"})
	if got.Matched["WEP-AAAAA"] == "" {
		t.Fatalf("the apostrophes and hyphen must not matter: %+v", got)
	}
}

func TestAPrefixOfAnotherNameIsNotAMatch(t *testing.T) {
	got := PairFiles(kycSpec,
		[]Request{{ID: "WEP-AAAAA", Name: "Ali", At: "2026-08-13 09:00:00"}},
		[]string{"AliAkbar_KTP.jpg"})
	if len(got.Matched) != 0 || !reflect.DeepEqual(got.Missing, []string{"WEP-AAAAA"}) {
		t.Fatalf("Ali must not take AliAkbar's file: %+v", got)
	}
}

func TestRepeatUploadsArePairedInTheOrderOfTheRequests(t *testing.T) {
	got := PairFiles(kycSpec,
		[]Request{
			{ID: "WEP-LATER", Name: "Budi", At: "2026-09-01 10:00:00"},
			{ID: "WEP-FIRST", Name: "Budi", At: "2026-08-01 10:00:00"},
		},
		[]string{"Budi_KTP (1).jpg", "Budi_KTP.jpg"})
	if got.Matched["WEP-FIRST"] != "Budi_KTP.jpg" || got.Matched["WEP-LATER"] != "Budi_KTP (1).jpg" {
		t.Fatalf("the first upload keeps the plain name, the next one gets (1): %v", got.Matched)
	}
}

func TestADifferentCountIsReportedNotGuessed(t *testing.T) {
	got := PairFiles(kycSpec,
		[]Request{{ID: "WEP-AAAAA", Name: "Budi", At: "2026-08-01 10:00:00"}},
		[]string{"Budi_KTP.jpg", "Budi_KTP (1).jpg"})
	if len(got.Matched) != 0 {
		t.Fatalf("two files for one request is a guess, so none is taken: %v", got.Matched)
	}
	if !reflect.DeepEqual(got.Ambiguous, []string{"WEP-AAAAA"}) {
		t.Fatalf("ambiguous = %v", got.Ambiguous)
	}
}

func TestARequestWithNoFileIsListedAsMissing(t *testing.T) {
	got := PairFiles(kycSpec, []Request{{ID: "WEP-AAAAA", Name: "Budi", At: "2026-08-01 10:00:00"}}, nil)
	if !reflect.DeepEqual(got.Missing, []string{"WEP-AAAAA"}) {
		t.Fatalf("missing = %v", got.Missing)
	}
}

func TestADateInTheFileNameDecidesWhichRequestItBelongsTo(t *testing.T) {
	// Two contracts exist but only one request kept a link: the date picks the right file.
	got := PairFiles(contractSpec,
		[]Request{{ID: "WEP-AAAAA", Name: "Budi", At: "9/26/2026 7:30:22"}},
		[]string{"Budi_Contract_20260813.pdf", "Budi_Contract_20260926.pdf"})
	if got.Matched["WEP-AAAAA"] != "Budi_Contract_20260926.pdf" {
		t.Fatalf("the date 20260926 is the request's day: %+v", got)
	}
}

func TestFilesThatDoNotFollowTheNamingAreCountedNotUsed(t *testing.T) {
	got := PairFiles(kycSpec, nil, []string{"scan1.pdf", "Budi_KTP.jpg"})
	if got.Unrecognised != 1 {
		t.Fatalf("unrecognised = %d", got.Unrecognised)
	}
}

// idSpec has the label the old script used first and the older "_ID" label as a fallback.
var idSpec = DocSpec{
	Field: "id_doc", Folder: "id",
	Pattern:  regexp.MustCompile(`^(?P<name>[^_]+)_(KTP|PassportID)$`),
	Fallback: regexp.MustCompile(`^(?P<name>[^_]+)_ID$`),
}

func TestThePreferredLabelWinsWhenAWorkerHasBothLabels(t *testing.T) {
	got := PairFiles(idSpec,
		[]Request{{ID: "WEP-AAAAA", Name: "Budi", At: "2026-08-01 10:00:00"}},
		[]string{"Budi_ID.pdf", "Budi_KTP.jpg"})
	if got.Matched["WEP-AAAAA"] != "Budi_KTP.jpg" || len(got.Ambiguous) != 0 {
		t.Fatalf("both files are the same worker's ID; the KTP one is the one the old script named: %+v", got)
	}
}

func TestTheFallbackLabelIsUsedWhenThereIsNothingElse(t *testing.T) {
	got := PairFiles(idSpec,
		[]Request{{ID: "WEP-AAAAA", Name: "Budi", At: "2026-08-01 10:00:00"}},
		[]string{"Budi_ID.jpeg"})
	if got.Matched["WEP-AAAAA"] != "Budi_ID.jpeg" {
		t.Fatalf("%+v", got)
	}
}

func TestFallbackFilesDoNotCountAsUnrecognised(t *testing.T) {
	got := PairFiles(idSpec, nil, []string{"Budi_ID.jpeg", "Budi_KTP.jpg", "other.pdf"})
	if got.Unrecognised != 1 {
		t.Fatalf("only other.pdf is unrecognised, got %d", got.Unrecognised)
	}
}

func TestARequestCanCarryItsOwnKeyInsteadOfItsSubmissionDay(t *testing.T) {
	// A waiver's letter is named by the visit end day (ddMMyyyy), not the day it was submitted.
	spec := DocSpec{Field: "letter_doc", Folder: "waiver-assignment", Pattern: regexp.MustCompile(`^WVR_AppointmentLetter_(?P<name>.+)_(?P<date>\d{8})$`)}
	got := PairFiles(spec,
		[]Request{{ID: "WVR-AAAAA", Name: "Tamu Satu", At: "8/7/2026 10:00:00", Key: "12112026"}},
		[]string{"WVR_AppointmentLetter_TamuSatu_05112026.pdf", "WVR_AppointmentLetter_TamuSatu_12112026.pdf"})
	if got.Matched["WVR-AAAAA"] != "WVR_AppointmentLetter_TamuSatu_12112026.pdf" {
		t.Fatalf("%+v", got)
	}
}

// photoSpec mirrors the real photo folder, with the owner's rule for a worker whose files and
// requests do not match in number (decided 2026-11-08): a photo is the worker's face, so the newest
// file is used rather than leaving the permit without one.
var photoSpec = DocSpec{Field: "id_photo", Folder: "id-photo", NewestWins: true,
	Pattern: regexp.MustCompile(`^(?P<name>.+)_(KTP|PassportID|ID)_Photo$`)}

func TestPhotoRuleTakesTheNewestFileWhenAWorkerHasMoreFilesThanRequests(t *testing.T) {
	got := PairFiles(photoSpec,
		[]Request{
			{ID: "WEP-AAAAA", Name: "Budi Santoso", At: "2026-01-10 09:00:00"},
			{ID: "WEP-BBBBB", Name: "Budi Santoso", At: "2026-08-13 09:00:00"},
		},
		[]string{"BudiSantoso_KTP_Photo.jpg", "BudiSantoso_KTP_Photo (1).jpg", "BudiSantoso_KTP_Photo (2).jpg"})
	// The two newest files go to the two requests in order; the oldest file is left unused.
	want := map[string]string{"WEP-AAAAA": "BudiSantoso_KTP_Photo (1).jpg", "WEP-BBBBB": "BudiSantoso_KTP_Photo (2).jpg"}
	if !reflect.DeepEqual(got.Matched, want) {
		t.Fatalf("matched = %v, want %v", got.Matched, want)
	}
	if len(got.Ambiguous) != 0 || !reflect.DeepEqual(got.Inferred, []string{"WEP-AAAAA", "WEP-BBBBB"}) {
		t.Fatalf("ambiguous %v, inferred %v", got.Ambiguous, got.Inferred)
	}
}

func TestPhotoRuleGivesEveryRequestTheOnlyPhotoWhenThereAreFewerFilesThanRequests(t *testing.T) {
	got := PairFiles(photoSpec,
		[]Request{
			{ID: "WEP-AAAAA", Name: "Budi Santoso", At: "2026-01-10 09:00:00"},
			{ID: "WEP-BBBBB", Name: "Budi Santoso", At: "2026-05-10 09:00:00"},
			{ID: "WEP-CCCCC", Name: "Budi Santoso", At: "2026-08-13 09:00:00"},
		},
		[]string{"BudiSantoso_KTP_Photo.jpg"})
	for _, id := range []string{"WEP-AAAAA", "WEP-BBBBB", "WEP-CCCCC"} {
		if got.Matched[id] != "BudiSantoso_KTP_Photo.jpg" {
			t.Errorf("%s: matched %q", id, got.Matched[id])
		}
	}
	if len(got.Ambiguous) != 0 || len(got.Inferred) != 3 {
		t.Fatalf("ambiguous %v, inferred %v", got.Ambiguous, got.Inferred)
	}
}

func TestPhotoRuleLeavesCleanPairingsAloneAndOnlyCountsGuessesAsInferred(t *testing.T) {
	got := PairFiles(photoSpec,
		[]Request{{ID: "WEP-AAAAA", Name: "Budi Santoso", At: "2026-08-13 09:00:00"}},
		[]string{"BudiSantoso_KTP_Photo.jpg"})
	if got.Matched["WEP-AAAAA"] == "" || len(got.Inferred) != 0 {
		t.Fatalf("an exact pairing is not a guess: %+v", got)
	}
}

func TestOtherDocumentsStayCautiousWithoutTheRule(t *testing.T) {
	got := PairFiles(kycSpec,
		[]Request{{ID: "WEP-AAAAA", Name: "Budi Santoso", At: "2026-01-10 09:00:00"}},
		[]string{"BudiSantoso_KTP.jpg", "BudiSantoso_KTP (1).jpg"})
	if len(got.Matched) != 0 || len(got.Ambiguous) != 1 || len(got.Inferred) != 0 {
		t.Fatalf("an ID card must not be guessed: %+v", got)
	}
}

// The same rule covers a KTP (owner, 2026-11-08: a photo or a KTP is uploaded once and does not
// expire) but not a passport or visa, which expire and are uploaded again, so a count that does not
// add up there could mean the newest file is the wrong period's document.
var idSpecWithKind = DocSpec{Field: "id_doc", Folder: "id", OneTimeKind: "KTP",
	Pattern: regexp.MustCompile(`^(?P<name>.+)_(?P<kind>KTP|PassportID)$`)}

func TestKtpTakesTheNewestFileButAPassportIsNeverGuessed(t *testing.T) {
	reqs := []Request{{ID: "WEP-AAAAA", Name: "Budi Santoso", At: "2026-01-10 09:00:00"}}
	ktp := PairFiles(idSpecWithKind, reqs, []string{"BudiSantoso_KTP.jpg", "BudiSantoso_KTP (1).jpg"})
	if ktp.Matched["WEP-AAAAA"] != "BudiSantoso_KTP (1).jpg" || len(ktp.Ambiguous) != 0 || len(ktp.Inferred) != 1 {
		t.Errorf("a KTP is uploaded once, so the newest wins: %+v", ktp)
	}
	pass := PairFiles(idSpecWithKind, reqs, []string{"BudiSantoso_PassportID.jpg", "BudiSantoso_PassportID (1).jpg"})
	if len(pass.Matched) != 0 || len(pass.Ambiguous) != 1 || len(pass.Inferred) != 0 {
		t.Errorf("a passport expires and is re-uploaded, so it is never guessed: %+v", pass)
	}
	mixed := PairFiles(idSpecWithKind, reqs, []string{"BudiSantoso_KTP.jpg", "BudiSantoso_PassportID.jpg"})
	if len(mixed.Matched) != 0 || len(mixed.Ambiguous) != 1 {
		t.Errorf("a KTP mixed with a passport stays cautious: %+v", mixed)
	}
}
