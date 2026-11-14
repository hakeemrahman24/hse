package settings

import (
	"context"
	"testing"
)

func TestFieldRulesStartShownAndOptionalExceptTheCertificate(t *testing.T) {
	s, _, _ := newSvc(t)
	groups, err := s.Groups(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rest, rules := SplitFieldRules(groups)
	if len(rules) != 4 {
		t.Fatalf("%d field rules, want 4 (BPJS Kesehatan, BPJS Ketenagakerjaan, contract, certificate)", len(rules))
	}
	for _, r := range rules {
		wantRequired := r.Field == "certificate"
		if !r.Shown || r.Required != wantRequired || !r.IsDefault {
			t.Errorf("%s: shown=%v required=%v default=%v", r.Field, r.Shown, r.Required, r.IsDefault)
		}
	}
	taken := map[string]bool{}
	for _, r := range rules {
		taken[r.Field+"_shown"], taken[r.Field+"_required"] = true, true
	}
	for _, g := range rest {
		for _, it := range g.Items {
			if taken[it.Key] {
				t.Errorf("%s is still listed as a separate setting", it.Key)
			}
		}
	}
}

func TestSetFieldRuleSavesBothSwitchesAndHiddenCannotBeMandatory(t *testing.T) {
	s, _, _ := newSvc(t)
	ctx := context.Background()
	if err := s.SetFieldRule(ctx, "arif", "bpjs_tk", true, true); err != nil {
		t.Fatal(err)
	}
	if p := s.Policy(); !p.BpjsTkShown || !p.BpjsTkRequired {
		t.Fatalf("show and mandatory: shown=%v required=%v", p.BpjsTkShown, p.BpjsTkRequired)
	}
	if err := s.SetFieldRule(ctx, "arif", "bpjs_tk", false, true); err != nil {
		t.Fatal(err)
	}
	if p := s.Policy(); p.BpjsTkShown || p.BpjsTkRequired {
		t.Fatalf("hidden must drop mandatory: shown=%v required=%v", p.BpjsTkShown, p.BpjsTkRequired)
	}
	if err := s.SetFieldRule(ctx, "arif", "certificate", true, false); err != nil {
		t.Fatal(err)
	}
	if p := s.Policy(); !p.CertificateShown || p.CertificateRequired {
		t.Fatalf("show only: shown=%v required=%v", p.CertificateShown, p.CertificateRequired)
	}
	if err := s.SetFieldRule(ctx, "arif", "nonsense", true, true); err == nil {
		t.Error("an unknown field was accepted")
	}
	if err := s.ResetFieldRule(ctx, "arif", "bpjs_tk"); err != nil {
		t.Fatal(err)
	}
	if p := s.Policy(); !p.BpjsTkShown || p.BpjsTkRequired {
		t.Fatalf("reset: shown=%v required=%v", p.BpjsTkShown, p.BpjsTkRequired)
	}
}
