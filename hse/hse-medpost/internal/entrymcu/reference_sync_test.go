package entrymcu

import "testing"

func TestDecideReferencePatchKeepsLadderAndAppliesCorrections(t *testing.T) {
	have := referenceFacts{
		Result: "Fit", Status: "Close", Notes: "note", Until: "2026-01-01", Standing: "Valid", Terminal: "T",
	}
	want := have
	want.Standing = "1 Week Urgent"
	if _, changed := decideReferencePatch("MedC1", want, have); changed {
		t.Fatal("validity ladder text should stay untouched")
	}

	want = have
	want.Standing = "Renewed by MedC9"
	patch, changed := decideReferencePatch("MedC1", want, have)
	if !changed || patch.standing == nil || *patch.standing != "Renewed by MedC9" || patch.terminal == nil || *patch.terminal {
		t.Fatalf("renewed stamp: %+v", patch)
	}
	if patch.status == nil || *patch.status != "Close" {
		t.Fatalf("renewed checkup should close: %+v", patch)
	}

	want = referenceFacts{Result: "Fit", Status: "Close", Standing: "Valid", Until: "2027-09-20"}
	have = referenceFacts{Result: "Unfit", Status: "Close", Standing: "Archived / Exit", Until: "2026-07-05", Terminal: "T"}
	patch, changed = decideReferencePatch("MedC2", want, have)
	if !changed || !patch.clearStanding || patch.result == nil || *patch.result != "Fit" {
		t.Fatalf("leaving unfit should clear the archived stamp: %+v", patch)
	}

	want = referenceFacts{Result: "Fit", Status: "Close", Until: "1/19/20027", UntilInvalid: true, Notes: "kept"}
	have = referenceFacts{Result: "Fit", Status: "Close", Until: "2026-01-01", Notes: "kept"}
	if _, changed := decideReferencePatch("MedC3", want, have); changed {
		t.Fatal("an impossible valid-until date must not change the row")
	}

	want = referenceFacts{Result: "FitWithRestriction", Status: "Close"}
	have = referenceFacts{Result: "FitWithNote", Status: "Close"}
	if _, changed := decideReferencePatch("MedC4", want, have); changed {
		t.Fatal("Fit with restriction already stored as Fit with note")
	}
}

func TestCorrectShiftedYear(t *testing.T) {
	got, ok := correctShiftedYear("1/19/20027")
	if !ok || got != "2027-01-19" {
		t.Fatalf("until = %q %v", got, ok)
	}
	got, ok = correctShiftedYear("20026-05-02")
	if !ok || got != "2026-05-02" {
		t.Fatalf("iso = %q %v", got, ok)
	}
	if _, ok := correctShiftedYear("2026-05-02"); ok {
		t.Fatal("a real 2026 date must stay")
	}
}

func TestSaneImportDate(t *testing.T) {
	if !saneImportDate("2026-10-03") || saneImportDate("20027-01-19") || saneImportDate("1/19/20027") {
		t.Fatal("date window")
	}
}
