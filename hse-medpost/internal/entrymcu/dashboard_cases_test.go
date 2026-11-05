package entrymcu

import "testing"

func TestDashboardCasesCarryNoteForNonFitResults(t *testing.T) {
	d := buildDashboard([]dashSrc{
		{Company: "A", Result: "NeedFollowUp", Status: "Open", Name: "Ani", CheckUpID: "C1", Recommendation: " see cardiologist "},
		{Company: "A", Result: "Unfit", Status: "Close", Name: "Budi", CheckUpID: "C2", ResultNotes: "uncontrolled BP"},
		{Company: "A", Result: "Fit", Status: "Close", Name: "Cici", CheckUpID: "C3"},
		{Company: "A", Result: "Unfit", Status: "Close", Name: "Old", CheckUpID: "C4", Superseded: true},
		{Company: "A", Result: "Unfit", Status: "Close"}, // PDF path: no Name, no case kept
	})
	if got := d.ResultCases["NeedFollowUp"]; len(got) != 1 || got[0].Text != "see cardiologist" {
		t.Fatalf("NeedFollowUp cases = %+v", got)
	}
	if got := d.ResultCases["Unfit"]; len(got) != 1 || got[0].Name != "Budi" || got[0].Text != "uncontrolled BP" {
		t.Fatalf("Unfit cases = %+v", got)
	}
	if _, ok := d.ResultCases["Fit"]; ok {
		t.Fatal("Fit must not be expandable")
	}
}
