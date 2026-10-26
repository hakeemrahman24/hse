package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The SOC wizard (SOC_Form_Spec_v1.1.md): four paths, one question per screen on the phone.
//
//	A unsafe action     B unsafe condition     C safe observation     D suggestion
//
// The browser shows the screens one at a time, but it is one form: everything arrives in one POST and
// is checked here, per path. The picklist answers arrive as the 1-based position of the option in its
// list (the position never changes, the wording may), and the server turns them back into wording.

const (
	pathUnsafeAction    = "A"
	pathUnsafeCondition = "B"
	pathSafe            = "C"
	pathSuggestion      = "D"
)

// Names of the picklists (ref_picklist.list_name).
const (
	listUnsafeReason = "unsafe_reason"
	listPPEItem      = "ppe_item"
	listGoodPractice = "good_practice"
	listStoppedBy    = "stopped_by"
)

// The control tag whose picklist answer asks for a PPE item.
const controlPPE = "PPE"

// Reasons HSE should look at a report (observations_observation.review_flags).
const (
	flagTopicNotPicked   = "topic not picked"
	flagCompanyNotInList = "company not in list"
	flagUrgent           = "urgent"
	flagStopNotUsed      = "stop work not used on high-potential work"
)

// wizardInput is the form as sent by the browser. The reporter is not on it: it comes from the
// signed-in employee. Fields that belong to another path are ignored.
type wizardInput struct {
	Path, Area          string
	SubArea             string // the second level of the area; asked only when the area has sub-areas
	Company             string // A, C: the company of the people observed
	AreaOwner           string // B: the company that owns the area
	Work                string // A: the work; C: the work (optional)
	Condition           string // B
	Improve             string // D: what needs to be improved
	TopicID, HazardID   string
	UnsafeReason        string // A: position in the unsafe_reason list
	PPEItem             string // A, when the reason is PPE
	StopWork, StoppedBy string // A: "yes" / "no", position in the stopped_by list
	ShouldStop          string // A: soft prompt answer, only for high-potential work that was not stopped
	Danger, Barricaded  string // B: "yes" / "no"
	GoodPractice        string // C: position in the good_practice list
	Corrective          string // A, B: what was done on the spot
	Recommendation      string // A, B optional; D required
}

// pickOption returns the option at the 1-based position v of a picklist.
func pickOption(ref *refSnapshot, list, v string) (picklistOption, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	opts := ref.Picklists[list]
	if err != nil || n < 1 || n > len(opts) {
		return picklistOption{}, false
	}
	return opts[n-1], true
}

// yesNo reads "yes" / "no"; anything else is not an answer.
func yesNo(v string) (*bool, bool) {
	switch strings.TrimSpace(v) {
	case "yes":
		b := true
		return &b, true
	case "no":
		b := false
		return &b, true
	}
	return nil, false
}

// controlTopicByName finds the control topic (PPE, Permit To Work, ...) a picklist answer sets.
func (s *refSnapshot) controlTopicByName(name string) int64 {
	if name == "" {
		return 0
	}
	for _, t := range s.Topics {
		if t.Control && t.Name == name {
			return t.ID
		}
	}
	return 0
}

func inList(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

// validateWizard checks a wizard form and returns the cleaned submission, or a map field -> message.
// A report is dated the day it is sent. companies is the list the company dropdown offered; a company
// typed outside it is accepted but flagged for HSE (with no list at all nothing is flagged).
func validateWizard(in wizardInput, today time.Time, ref *refSnapshot, companies []string) (socSubmission, map[string]string) {
	errs := map[string]string{}
	sub := socSubmission{Path: in.Path, FindingDate: today, Status: statusOpen}
	var flags []string

	text := func(field, v, missing string, required bool) string {
		v = strings.TrimSpace(v)
		switch {
		case v == "" && required:
			errs[field] = missing
		case tooLongText(v, maxObsText):
			errs[field] = fmt.Sprintf("Maximum %d characters.", maxObsText)
		}
		return v
	}
	company := func(field, v string) string {
		v = strings.TrimSpace(v)
		switch {
		case v == "":
			errs[field] = "Enter the company."
		case tooLongText(v, maxObsName):
			errs[field] = fmt.Sprintf("Maximum %d characters.", maxObsName)
		case len(companies) > 0 && !inList(companies, v):
			flags = append(flags, flagCompanyNotInList)
		}
		return v
	}

	// Area, on every path.
	area := strings.TrimSpace(in.Area)
	if canon, ok := ref.CanonicalArea(area); ok {
		sub.Area = canon
	} else if area == "" {
		errs["area"] = "Choose the area."
	} else {
		errs["area"] = "Choose an area from the list."
	}
	// Sub-area: asked only for an area that has some; a value posted for any other area is dropped.
	if subs := ref.SubAreas[sub.Area]; len(subs) > 0 {
		if canon, ok := ref.CanonicalSubArea(sub.Area, in.SubArea); ok {
			sub.SubArea = canon
		} else if strings.TrimSpace(in.SubArea) == "" {
			errs["sub_area"] = "Choose the sub-area."
		} else {
			errs["sub_area"] = "Choose a sub-area from the list."
		}
	}

	switch in.Path {
	case pathUnsafeAction:
		sub.Category = catUnsafeAction
		sub.ResponsibleCompany = company("company", in.Company)
		sub.Description = text("work", in.Work, "Describe the work.", true)
		if reason, ok := pickOption(ref, listUnsafeReason, in.UnsafeReason); !ok {
			errs["unsafe_reason"] = "Choose what was unsafe."
		} else {
			sub.UnsafeReason = reason.Label
			sub.ControlTopicID = ref.controlTopicByName(reason.ControlTag)
			if reason.ControlTag == controlPPE {
				if item, ok := pickOption(ref, listPPEItem, in.PPEItem); !ok {
					errs["ppe_item"] = "Choose the PPE item."
				} else {
					sub.PPEItem = item.Label
				}
			}
		}
		stop, ok := yesNo(in.StopWork)
		if !ok {
			errs["stop_work"] = "Answer yes or no."
		}
		sub.StopWork = stop
		if stop != nil && *stop {
			if by, ok := pickOption(ref, listStoppedBy, in.StoppedBy); !ok {
				errs["stopped_by"] = "Choose who stopped the work."
			} else {
				sub.StoppedBy = by.Label
			}
		}
		sub.ActionTaken = text("corrective", in.Corrective, "Describe what you did on the spot.", true)
		sub.Recommendation = text("recommendation", in.Recommendation, "", false)

	case pathUnsafeCondition:
		sub.Category = catUnsafeCondition
		sub.ResponsibleCompany = company("area_owner", in.AreaOwner)
		sub.Description = text("condition", in.Condition, "Describe the condition.", true)
		danger, okD := yesNo(in.Danger)
		barricaded, okB := yesNo(in.Barricaded)
		if !okD {
			errs["danger"] = "Answer yes or no."
		}
		if !okB {
			errs["barricaded"] = "Answer yes or no."
		}
		sub.ImmediateDanger, sub.Barricaded = danger, barricaded
		if okD && okB && *danger && !*barricaded {
			flags = append(flags, flagUrgent)
		}
		sub.ActionTaken = text("corrective", in.Corrective, "Describe what you did on the spot.", true)
		sub.Recommendation = text("recommendation", in.Recommendation, "", false)

	case pathSafe:
		sub.Category = catPositive
		sub.ResponsibleCompany = company("company", in.Company)
		work := text("work", in.Work, "", false)
		if good, ok := pickOption(ref, listGoodPractice, in.GoodPractice); !ok {
			errs["good_practice"] = "Choose what was done well."
		} else {
			sub.GoodPractice = good.Label
			sub.ControlTopicID = ref.controlTopicByName(good.ControlTag)
			sub.Description = work
			if work == "" {
				sub.Description = good.Label // the work is optional: the report still has a text
			}
		}

	case pathSuggestion:
		sub.Category = catUnsafeCondition // a suggestion is stored as an unsafe condition, as before the wizard
		sub.Description = text("improve", in.Improve, "Describe what needs to be improved.", true)
		sub.Recommendation = text("recommendation", in.Recommendation, "Describe your proposal.", true)

	default:
		errs["path"] = "Choose what you saw."
	}

	// Topic: the reporter picks, nothing is chosen for them. Only hazard topics can be picked.
	sub.TopicID, sub.HazardID = resolveClassification(in.TopicID, in.HazardID, sub.Description, ref, 0, false, errs)
	if t, ok := ref.topicByID[sub.TopicID]; ok && t.Control {
		errs["topic"] = "Choose a topic from the list."
	}
	if len(errs) > 0 {
		return socSubmission{}, errs
	}

	sub.TopicPick, sub.SuggestionsShown = topicPick(ref, sub.Description, sub.TopicID)
	sub.KeywordsSuggested, sub.KeywordsPicked = ref.keywordCredit(sub.Description, socTopicSuggestions, sub.TopicID)

	// A safe observation without work text asks for no topic, so it is not flagged for lacking one.
	if sub.TopicID == 0 && !(in.Path == pathSafe && strings.TrimSpace(in.Work) == "") {
		flags = append(flags, flagTopicNotPicked)
	}
	// The soft prompt: high-potential work that was not stopped, and the reporter says it should have been.
	if t, ok := ref.topicByID[sub.TopicID]; ok && in.Path == pathUnsafeAction && t.HighPotential &&
		sub.StopWork != nil && !*sub.StopWork && strings.TrimSpace(in.ShouldStop) == "yes" {
		flags = append(flags, flagStopNotUsed)
	}
	sub.ReviewFlags = strings.Join(flags, "; ")
	return sub, nil
}
