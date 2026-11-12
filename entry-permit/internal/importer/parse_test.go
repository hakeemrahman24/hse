package importer

import "testing"

func TestDayReadsEveryFormatTheSheetUsesAndRefusesTheRest(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"2026-11-07", "2026-11-07", true},
		{"04 Oct 2026", "2026-10-04", true},
		{"4 Oct 2026", "2026-10-04", true},
		{"09-20-26", "2026-09-20", true}, // month first: the 20 can only be a day
		{"9/20/2026", "2026-09-20", true},
		{"1988-03-05", "1988-03-05", true}, // a birth date
		{"", "", true},
		{"  ", "", true},
		{"_", "", true}, // the sheet's "nothing here"
		{"-", "", true},
		{"04 Oct 20266", "", false}, // a typo in the year
		{"09-20-266", "", false},
		{"31/12/2026", "", false}, // there is no month 31: never guess day-first
		{"2026-02-30", "", false},
		{"0026-01-01", "", false}, // a year that cannot be right
		{"soon", "", false},
	}
	for _, c := range cases {
		got, ok := Day(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("Day(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestMomentReadsTheSheetsTimestampsWithoutShiftingThem(t *testing.T) {
	// Sheet cells are already Jakarta wall-clock time (plan section 2b): no hour is added or removed.
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"8/7/2026 11:11:51", "2026-08-07 11:11:51", true}, // month first
		{"10/5/2026 9:03:04", "2026-10-05 09:03:04", true},
		{"07 Aug 2026 11:15:50", "2026-08-07 11:15:50", true},
		{"2026-08-07 11:11:51", "2026-08-07 11:11:51", true},
		{"", "", true},
		{"_", "", true},
		{"nope", "", false},
		{"8/7/2026", "", false}, // a day with no time is not a moment
	}
	for _, c := range cases {
		got, ok := Moment(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("Moment(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestPhoneIsRecoveredFromANumericCellAndFlaggedWhenDigitsMayBeLost(t *testing.T) {
	cases := []struct {
		in, want string
		lossy    bool
	}{
		{"8.12345678901E+11", "812345678901", false}, // 12 digits written for a 12 digit number
		{"8.1234567890E+11", "812345678900", true},   // one digit short: the last one may have been rounded off
		{"+62 812-3456-7890", "6281234567890", false},
		{"081234567890", "6281234567890", false}, // a leading 0 becomes 62, as the forms do
		{"", "", false},
		{"_", "", false},
	}
	for _, c := range cases {
		got, lossy := Phone(c.in)
		if got != c.want || lossy != c.lossy {
			t.Errorf("Phone(%q) = %q, lossy %v; want %q, %v", c.in, got, lossy, c.want, c.lossy)
		}
	}
}

func TestBlankTreatsTheSheetsPlaceholdersAsNothing(t *testing.T) {
	for _, s := range []string{"", " ", "_", "-", "#N/A", "N#A"} {
		if !Blank(s) {
			t.Errorf("Blank(%q) = false", s)
		}
	}
	for _, s := range []string{"N", "Fit", "0"} {
		if Blank(s) {
			t.Errorf("Blank(%q) = true", s)
		}
	}
}

func TestAnExcelSerialNumberIsReadAsTheDateItStandsFor(t *testing.T) {
	// A cell with no date format shows the number Excel keeps inside: days since 30 Dec 1899.
	// The real sheet has 19 such cells (the waiver Final approval time).
	if got, ok := Day("46331"); got != "2026-11-05" || !ok {
		t.Errorf("Day(46331) = %q, %v", got, ok)
	}
	if got, ok := Day("46331.481281493056"); got != "2026-11-05" || !ok {
		t.Errorf("Day(46331.48...) = %q, %v", got, ok)
	}
	if got, ok := Moment("46331.481281493056"); got != "2026-11-05 11:33:02" || !ok {
		t.Errorf("Moment(46331.48...) = %q, %v", got, ok)
	}
	// numbers that cannot be a date of this system are refused, never turned into one
	for _, s := range []string{"12", "812345678901", "5", "99999", "-46331", "46331.5.5"} {
		if got, ok := Day(s); ok || got != "" {
			t.Errorf("Day(%q) = %q, %v: a plain number was read as a date", s, got, ok)
		}
	}
}
