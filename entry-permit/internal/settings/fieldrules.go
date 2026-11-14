package settings

import "context"

// FieldRule is one optional part of the work-permit form whose visibility the admin decides. Each has two
// switches: Show (the box is on the form at all) and Mandatory (the requester must fill it in). Mandatory only
// counts while it is shown, so "hidden" is Show off, "show only" is Show on, and "show and mandatory" is both on.
// The two switches are the settings "<Field>_shown" and "<Field>_required".
type FieldRule struct {
	Field, Label, Note string
}

// FieldRules are the parts of the form the admin can switch, in the order the page lists them.
var FieldRules = []FieldRule{
	{"bpjs_kes", "BPJS Kesehatan number", "Indonesian workers only. The card upload is always optional."},
	{"bpjs_tk", "BPJS Ketenagakerjaan number", "Indonesian workers only. The card upload is always optional."},
	{"contract", "Contract document and validity date", "A mandatory contract must be valid today or later."},
	{"certificate", "Certificate of competence", "Only for positions whose list entry says a certificate is needed."},
}

// FieldRuleItem is a FieldRule with its current switches, for the page.
type FieldRuleItem struct {
	FieldRule
	Shown, Required, IsDefault bool
}

// SplitFieldRules takes the switch settings out of the generic groups and returns them as one list, so the page
// shows each field as one row with two checkboxes instead of two separate on/off rows.
func SplitFieldRules(groups []Group) ([]Group, []FieldRuleItem) {
	state := map[string]Item{}
	for _, g := range groups {
		for _, it := range g.Items {
			state[it.Key] = it
		}
	}
	var rules []FieldRuleItem
	taken := map[string]bool{}
	for _, fr := range FieldRules {
		sh, okS := state[fr.Field+"_shown"]
		rq, okR := state[fr.Field+"_required"]
		if !okS || !okR {
			continue
		}
		taken[sh.Key], taken[rq.Key] = true, true
		rules = append(rules, FieldRuleItem{FieldRule: fr, Shown: sh.Value == "on", Required: rq.Value == "on", IsDefault: sh.IsDefault && rq.IsDefault})
	}
	var rest []Group
	for _, g := range groups {
		kept := Group{Name: g.Name}
		for _, it := range g.Items {
			if !taken[it.Key] {
				kept.Items = append(kept.Items, it)
			}
		}
		if len(kept.Items) > 0 {
			rest = append(rest, kept)
		}
	}
	return rest, rules
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func ruleOf(field string) (FieldRule, bool) {
	for _, fr := range FieldRules {
		if fr.Field == field {
			return fr, true
		}
	}
	return FieldRule{}, false
}

// SetFieldRule saves both switches of one field. Mandatory without Show is saved as not mandatory, because a hidden
// box cannot be required.
func (s *Service) SetFieldRule(ctx context.Context, actor, field string, shown, required bool) error {
	if _, ok := ruleOf(field); !ok {
		return invalid("That form field does not exist.")
	}
	if err := s.Set(ctx, actor, field+"_shown", onOff(shown)); err != nil {
		return err
	}
	return s.Set(ctx, actor, field+"_required", onOff(shown && required))
}

// ResetFieldRule puts both switches of one field back to their defaults.
func (s *Service) ResetFieldRule(ctx context.Context, actor, field string) error {
	if _, ok := ruleOf(field); !ok {
		return invalid("That form field does not exist.")
	}
	if err := s.Reset(ctx, actor, field+"_shown"); err != nil {
		return err
	}
	return s.Reset(ctx, actor, field+"_required")
}
