package waiver

import (
	"fmt"
	"strings"
)

// Question is one Yes / No / N/A item of the visitor form (the old system's Risk Exposure and
// Health Declaration cards). Key is the form field suffix, Label is shown on the form and to the
// reviewers, Hint is the one line of advice under a risk item.
type Question struct {
	Key, Label, Hint string
}

// RiskQuestions are the nine safety-critical activities the visitor is asked about.
var RiskQuestions = []Question{
	{"bypassing", "Bypassing safety control", "Obtain authorisation before overriding or disabling safety controls."},
	{"confined", "Confined space", "Obtain authorisation before entering a confined space."},
	{"driving", "Driving", "Follow site rules for light and heavy vehicles."},
	{"energy", "Energy isolation", "Verify isolation and zero energy before work begins."},
	{"hotwork", "Hot work", "Control flammables and ignition sources."},
	{"lineoffire", "Line of fire", "Stay clear of swing radius, stored energy, and moving loads."},
	{"lifting", "Safe mechanical lifting", "Use correct lifting equipment and procedures."},
	{"workauth", "Work authorization", "Obtain required permits before non-routine work."},
	{"height", "Working at height", "Protect yourself against a fall when working at height."},
}

// HealthQuestions are the fifteen health conditions the visitor declares.
var HealthQuestions = []Question{
	{"infections", "Current infections (please list if yes)", ""},
	{"vision", "Impairment of vision", ""},
	{"hearing", "Impairment of hearing", ""},
	{"mobility", "Impairment of mobility", ""},
	{"fearheights", "Fear of heights", ""},
	{"diabetes", "Diabetes mellitus", ""},
	{"epilepsy", "Epilepsy / fainting", ""},
	{"cardiac", "Cardiac / circulatory problems", ""},
	{"chestpain", "Chest pain / shortness of breath", ""},
	{"asthma", "Asthma", ""},
	{"ulcer", "Peptic ulceration", ""},
	{"colostomy", "Colostomy / ileostomy", ""},
	{"prostate", "Prostate problems", ""},
	{"surgery", "Recent major surgery", ""},
	{"pregnancy", "Pregnancy", ""},
}

// NonconformanceText is the fixed "Type of Non-conformance" every visitor request carries: a
// visitor has no medical checkup on record, which is why the form exists at all.
const NonconformanceText = "A comprehensive medical examination has not been performed and/or complete health screening " +
	"records are not available; therefore, health risk factors cannot be fully identified or assessed."

// Answer codes. WHY one letter each, stored as one string per card: the order of the questions
// above is fixed, so "NNYNNNNNN" is a whole card in one small column, and the labels always come
// from this file, never from old rows.
const (
	AnswerNo  = 'N'
	AnswerNA  = 'A'
	AnswerYes = 'Y'
)

var answerWords = map[string]byte{"No": AnswerNo, "N/A": AnswerNA, "Yes": AnswerYes}

// ParseAnswers reads one card from the posted values (field "<prefix>_<key>"). Every question
// must be answered with No, N/A or Yes; the form pre-selects No, so a missing answer is a
// tampered or broken post and is refused rather than guessed.
func ParseAnswers(prefix string, qs []Question, get func(field string) string) (string, error) {
	out := make([]byte, len(qs))
	for i, q := range qs {
		code, ok := answerWords[strings.TrimSpace(get(prefix+"_"+q.Key))]
		if !ok {
			return "", fmt.Errorf("Answer every question: %q is missing.", q.Label)
		}
		out[i] = code
	}
	return string(out), nil
}

// ValidAnswers says whether s is a whole card of answers for qs (what is read back from storage).
func ValidAnswers(s string, qs []Question) bool {
	if len(s) != len(qs) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] != AnswerNo && s[i] != AnswerNA && s[i] != AnswerYes {
			return false
		}
	}
	return true
}

// YesLabels lists the questions answered Yes, for the reviewers and the summary.
func YesLabels(s string, qs []Question) []string {
	var out []string
	for i := 0; i < len(s) && i < len(qs); i++ {
		if s[i] == AnswerYes {
			out = append(out, qs[i].Label)
		}
	}
	return out
}

// HighRisk is the old "highRiskFlag": any Yes on either card.
func HighRisk(risk, health string) bool {
	return strings.ContainsRune(risk, AnswerYes) || strings.ContainsRune(health, AnswerYes)
}

// Word turns one stored code back into the word shown on screen.
func Word(code byte) string {
	switch code {
	case AnswerYes:
		return "Yes"
	case AnswerNA:
		return "N/A"
	}
	return "No"
}
