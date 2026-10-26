package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// The categories on the SOC form (Indonesian first, English second). The values are the same
// ones the staff form stores (observation_rules.go).
var socCategories = []struct{ Value, Label string }{
	{catUnsafeAction, "Unsafe Action"},
	{catUnsafeCondition, "Unsafe Condition"},
	{catPositive, "Positive Finding"},
}

// socInput is the raw form as sent by the browser. The reporter (name, position, company) is not
// on it: it comes from the signed-in employee.
type socInput struct {
	Date, Category, Description, TopicID, HazardID, Area, ResponsibleCompany, ActionTaken string
	KeepHazard                                                                            int64 // the hazard a post being edited already has; stays allowed even if archived
}

// socSubmission is a validated submission, ready to save. Reporter fields are filled from the
// verified employee in the handler, never from the form.
type socSubmission struct {
	EmployeeID, ReporterName, Designation, Company string
	Category, Description                          string
	TopicID, HazardID                              int64
	TopicPick, SuggestionsShown                    string  // how the topic was chosen and what was suggested (v8, see topicPick)
	KeywordsSuggested, KeywordsPicked              []int64 // ref_keyword ids to count when the post is first saved (see keywordCredit)

	// Wizard fields (soc_wizard.go); empty / nil for a report from the classic form.
	Path, SubArea, UnsafeReason, PPEItem, StoppedBy, GoodPractice, Recommendation, ReviewFlags string
	StopWork, ImmediateDanger, Barricaded                                                      *bool
	ControlTopicID                                                                             int64
	Area, ResponsibleCompany, Status, ActionTaken                                              string
	FindingDate                                                                                time.Time
}

// validateSOC checks the form and returns the cleaned submission, or a map field -> message.
// today is passed in so tests do not depend on the clock. ref holds the Area list and the
// topic/hazard lists; a topic or hazard left empty is suggested from the description.
func validateSOC(in socInput, today time.Time, ref *refSnapshot) (socSubmission, map[string]string) {
	errs := map[string]string{}
	sub := socSubmission{
		Category:           in.Category,
		Description:        strings.TrimSpace(in.Description),
		Area:               strings.TrimSpace(in.Area),
		ResponsibleCompany: strings.TrimSpace(in.ResponsibleCompany),
		ActionTaken:        strings.TrimSpace(in.ActionTaken),
	}

	d, err := time.Parse("2006-01-02", in.Date)
	switch {
	case err != nil:
		errs["date"] = "Enter a valid date."
	case d.After(today):
		errs["date"] = "The date cannot be in the future."
	default:
		sub.FindingDate = d
	}
	if !validCategory(in.Category) {
		errs["category"] = "Choose a category."
	}
	if sub.Description == "" {
		errs["description"] = "Describe your finding."
	}
	for field, v := range map[string]string{"description": sub.Description, "responsible_company": sub.ResponsibleCompany, "action_taken": sub.ActionTaken} {
		if tooLongText(v, maxObsText) {
			errs[field] = fmt.Sprintf("Maximum %d characters.", maxObsText)
		}
	}
	if tooLongText(sub.ResponsibleCompany, maxObsName) {
		errs["responsible_company"] = fmt.Sprintf("Maximum %d characters.", maxObsName)
	}
	if sub.Area == "" {
		errs["area"] = "Choose the area."
	} else if canon, ok := ref.CanonicalArea(sub.Area); ok {
		sub.Area = canon
	} else {
		errs["area"] = "Choose an area from the list."
	}
	sub.TopicID, sub.HazardID = resolveClassification(in.TopicID, in.HazardID, sub.Description, ref, in.KeepHazard, false, errs)

	// A new post always starts Open. (A Closed post is locked, so even a positive finding is closed
	// later by staff, never at the moment of sending.)
	sub.Status = statusOpen
	if len(errs) > 0 {
		return socSubmission{}, errs
	}
	// Ranking the text is done only for a valid post.
	sub.TopicPick, sub.SuggestionsShown = topicPick(ref, sub.Description, sub.TopicID)
	sub.KeywordsSuggested, sub.KeywordsPicked = ref.keywordCredit(sub.Description, socTopicSuggestions, sub.TopicID)
	return sub, nil
}

// How the reporter chose the topic (observations_observation.topic_pick).
const (
	topicPickSuggested = "suggested" // one of the topics the suggestion showed
	topicPickList      = "list"      // another topic, from the full list
	topicPickNone      = "none"      // no topic: the post waits for HSE review
)

// socTopicSuggestions is how many topics the form suggests.
const socTopicSuggestions = 3

// topicPick works out how the topic was chosen and what the suggestion offered, for the accuracy
// measure of plan v8 section 5 (top-1 / top-3 rate, "browse list" rate, "none" rate). It is
// computed on the server from the text that was saved, so the browser cannot forge it; if the
// reporter edited the text after picking, it can differ slightly from what was on screen.
// shown is JSON like [{"t":3,"s":5}]: topic id and score, best first.
func topicPick(ref *refSnapshot, description string, topicID int64) (pick, shown string) {
	top := ref.SuggestTop(description, socTopicSuggestions, true)
	type shownTopic struct {
		T int64 `json:"t"`
		S int   `json:"s"`
	}
	list := make([]shownTopic, len(top))
	pick = topicPickList
	for i, sg := range top {
		list[i] = shownTopic{sg.TopicID, sg.Score}
		if sg.TopicID == topicID {
			pick = topicPickSuggested
		}
	}
	if topicID == 0 {
		pick = topicPickNone
	}
	b, _ := json.Marshal(list) // ints only: cannot fail
	return pick, string(b)
}

// socRef is the reference number shown to the employee, derived from the row id.
func socRef(id int64) string { return fmt.Sprintf("SOC-%06d", id) }
