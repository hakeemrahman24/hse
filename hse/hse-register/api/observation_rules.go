package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The observation field rules, in one place so the staff form, the public SOC form and the JSON
// API all agree. Nothing here touches the database.

const (
	catUnsafeAction    = "unsafe_action"
	catUnsafeCondition = "unsafe_condition"
	catPositive        = "positive_finding"

	statusOpen       = "open"
	statusInProgress = "in_progress"
	statusClosed     = "closed"

	sourceSOC = "SOC"

	verifNotYet   = "Not yet"
	verifVerified = "Verified"

	maxObsText = 5000
	maxObsName = 255
)

type option struct{ Value, Label string }

var observationCategories = []option{
	{catUnsafeAction, "Unsafe Action"},
	{catUnsafeCondition, "Unsafe Condition"},
	{catPositive, "Positive Finding"},
}

var observationStatuses = []option{
	{statusOpen, "Open"},
	{statusInProgress, "In Progress"},
	{statusClosed, "Closed"},
}

// staffSources are what a signed-in staff member can choose. "SOC" is not among them: it is set
// only when an employee sends the public form.
var staffSources = []string{"Internal Audit", "External Audit", "Management Walkthrough", "Project Monitoring Action Report"}

// defaultSourceDetails is the starting type list of the sources that have one. Administrators
// edit the live list in Settings > Source types.
var defaultSourceDetails = map[string][]string{
	"Internal Audit": {"Site Audit", "HO Audit", "Corporate Audit"},
	"External Audit": {"3rd Party Company", "Government"},
}

func labelOf(opts []option, value string) string {
	for _, o := range opts {
		if o.Value == value {
			return o.Label
		}
	}
	return value
}

func validCategory(c string) bool {
	for _, o := range observationCategories {
		if o.Value == c {
			return true
		}
	}
	return false
}

func validStatus(s string) bool {
	for _, o := range observationStatuses {
		if o.Value == s {
			return true
		}
	}
	return false
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func isPositive(category string) bool { return category == catPositive }

// closeBlocked reports whether closing is refused: an observation that needs follow-up can be
// closed only when it has at least one "after" photo or PDF. A Positive Finding has nothing to
// follow up, so it is exempt.
func closeBlocked(category, status string, afterFiles int) bool {
	return status == statusClosed && !isPositive(category) && afterFiles == 0
}

const msgCloseNeedsAfter = "Add at least one After photo or PDF before closing. Save as In Progress first, upload the files, then close. (A Positive Finding can be closed without them.)"

// obsForm is the staff form as sent by the browser.
type obsForm struct {
	Source, SourceDetail               string
	ObserverName, Designation, Company string
	Category, Description              string
	TopicID, HazardID                  string
	Area, ResponsibleCompany           string
	Verification, Status, ObservedAt   string
	ActionTaken                        string // remarks on what was done about the finding
}

// obsCheck is what the validator needs to know besides the form.
type obsCheck struct {
	Ref        *refSnapshot
	Existing   *Observation // nil for a new observation
	AfterFiles int          // "after" photos and PDFs already saved for Existing
}

func parseID(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 0
}

func tooLongText(s string, limit int) bool { return utf8.RuneCountInString(s) > limit }

// validateObservation checks a staff form and returns the cleaned observation, or a map
// field -> message. When Existing is a SOC post, who sent it (name, employee id, designation,
// company) and the source cannot be changed, whatever the form says: they come from the signed-in
// employee, not from staff.
func validateObservation(f obsForm, c obsCheck) (*Observation, map[string]string) {
	errs := map[string]string{}
	o := &Observation{
		Category:           f.Category,
		Description:        strings.TrimSpace(f.Description),
		Location:           strings.TrimSpace(f.Area),
		ResponsibleCompany: strings.TrimSpace(f.ResponsibleCompany),
		Status:             f.Status,
	}
	if c.Existing != nil {
		o.ID = c.Existing.ID
		o.Recommendation = c.Existing.Recommendation
	}
	o.ActionTaken = strings.TrimSpace(f.ActionTaken)
	if tooLongText(o.ActionTaken, maxObsText) {
		errs["action_taken"] = fmt.Sprintf("Maximum %d characters.", maxObsText)
	}
	fromSOC := c.Existing != nil && c.Existing.Source == sourceSOC

	// Who and where from.
	if fromSOC {
		o.Source = sourceSOC
		o.ObserverName, o.EmployeeID = c.Existing.ObserverName, c.Existing.EmployeeID
		o.Designation, o.Company = c.Existing.Designation, c.Existing.Company
	} else {
		o.ObserverName = strings.TrimSpace(f.ObserverName)
		o.Designation = strings.TrimSpace(f.Designation)
		o.Company = strings.TrimSpace(f.Company)
		if o.ObserverName == "" {
			errs["observer_name"] = "Enter the submitter's name."
		}
		switch {
		case c.Ref.SourceAllowed(f.Source, existingSource(c.Existing)):
			o.Source = f.Source
		case c.Existing != nil && c.Existing.Source == "" && f.Source == "":
			// An old row from before the source existed may stay without one.
		default:
			errs["source"] = "Choose the source."
		}
		keepDetail := ""
		if c.Existing != nil && c.Existing.Source == o.Source {
			keepDetail = c.Existing.SourceDetail
		}
		// A source with types needs one of them; an archived type is accepted only when the post
		// already had it.
		if details := c.Ref.DetailNames(o.Source, keepDetail); len(details) > 0 {
			if contains(details, f.SourceDetail) {
				o.SourceDetail = f.SourceDetail
			} else {
				errs["source_detail"] = "Choose the type of " + strings.ToLower(o.Source) + "."
			}
		}
	}

	if !validCategory(f.Category) {
		errs["category"] = "Choose a category."
	}
	if o.Description == "" {
		errs["description"] = "Describe the finding."
	}
	if tooLongText(o.Description, maxObsText) {
		errs["description"] = fmt.Sprintf("Maximum %d characters.", maxObsText)
	}
	for field, v := range map[string]string{"observer_name": o.ObserverName, "company": o.Company, "responsible_company": o.ResponsibleCompany} {
		if tooLongText(v, maxObsName) {
			errs[field] = fmt.Sprintf("Maximum %d characters.", maxObsName)
		}
	}
	if tooLongText(o.Designation, 100) {
		errs["designation"] = "Maximum 100 characters."
	}

	// Area: from the Settings list, or the value the old row already has.
	canon, inList := c.Ref.CanonicalArea(o.Location)
	switch {
	case o.Location == "":
		errs["area"] = "Choose the area."
	case inList:
		o.Location = canon
	case c.Existing != nil && o.Location == c.Existing.Location:
		// unchanged old value
	default:
		errs["area"] = "Choose an area from the list (administrators can add areas in Settings)."
	}
	if o.ResponsibleCompany == "" {
		errs["responsible_company"] = "Choose or enter the company responsible in that area."
	}

	// Topic and hazard: optional; if left empty they are suggested from the description.
	o.TopicID, o.HazardID = resolveClassification(f.TopicID, f.HazardID, o.Description, c.Ref, existingHazard(c.Existing), true, errs)

	// Verification exists only for posts from the public SOC form, and only staff set it.
	if o.Source == sourceSOC {
		switch f.Verification {
		case verifVerified, verifNotYet:
			o.Verification = f.Verification
		case "":
			o.Verification = verifNotYet
			if c.Existing != nil && c.Existing.Verification != "" {
				o.Verification = c.Existing.Verification
			}
		default:
			errs["verification"] = "Choose Verified or Not yet."
		}
	}

	when, err := time.Parse(formDateTimeLayout, f.ObservedAt)
	if err != nil {
		errs["observed_at"] = "Enter a valid date and time."
	}
	o.ObservedAt = when

	if !validStatus(f.Status) {
		errs["status"] = "Choose a status."
	} else if closeBlocked(f.Category, f.Status, c.AfterFiles) {
		errs["status"] = msgCloseNeedsAfter
	}

	if len(errs) > 0 {
		return nil, errs
	}
	return o, nil
}

// resolveClassification turns the topic and hazard fields into ids. An id that is not in the lists
// is an error; a missing one is filled in: the hazard from the chosen topic, or both from the
// description when guess is true. The staff form guesses; the public SOC form does not (plan v8:
// the system suggests, the reporter picks). keep is the hazard the post being edited already has
// (0 for a new post). It writes problems into errs.
func resolveClassification(topicField, hazardField, description string, ref *refSnapshot, keep int64, guess bool, errs map[string]string) (int64, int64) {
	topic, okT := parseID(topicField)
	hazard, okH := parseID(hazardField)
	if _, found := ref.topicByID[topic]; !okT || (topic != 0 && !found) {
		errs["topic"] = "Choose a topic from the list."
	}
	// An archived hazard is refused unless the post already had it (keep).
	if h, found := ref.hazardByID[hazard]; !okH || (hazard != 0 && (!found || (h.Archived && hazard != keep))) {
		errs["hazard"] = "Choose a hazard from the list."
	}
	if errs["topic"] != "" || errs["hazard"] != "" {
		return 0, 0
	}
	if topic == 0 && hazard == 0 {
		if !guess {
			return 0, 0
		}
		s := ref.Suggest(description)
		return s.TopicID, s.HazardID
	}
	if topic != 0 && hazard == 0 {
		hazard = ref.topicByID[topic].HazardID
	}
	return topic, hazard
}

// existingSource is the source of the observation being edited, or "" for a new one.
func existingSource(o *Observation) string {
	if o == nil {
		return ""
	}
	return o.Source
}

// existingDetail is the source type of the observation being edited, or "" for a new one.
func existingDetail(o *Observation) string {
	if o == nil {
		return ""
	}
	return o.SourceDetail
}

// existingHazard is the hazard of the observation being edited, or 0 for a new one.
func existingHazard(o *Observation) int64 {
	if o == nil {
		return 0
	}
	return o.HazardID
}

// sourceDetailChoice is one detail option together with the source it belongs to, so the form can
// show only the details of the chosen source.
type sourceDetailChoice struct{ Source, Detail string }
