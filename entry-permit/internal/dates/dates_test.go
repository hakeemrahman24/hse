package dates

import "testing"

// People type dates in many shapes. CleanTyped turns the recognisable ones into d/m/yyyy so the
// strict parsers can read them; anything else comes back unchanged and is refused as before.
func TestCleanTypedAcceptsRunTogetherDigitsAndOtherSeparators(t *testing.T) {
	cases := map[string]string{
		"24071983":     "24/07/1983",
		"24/071983":    "24/07/1983", // a slash forgotten
		"2407/1983":    "24/07/1983",
		"24-07-1983":   "24/07/1983",
		"24.07.1983":   "24/07/1983",
		" 24 07 1983 ": "24/07/1983",
		"24/07/1983":   "24/07/1983",
		"1/2/2020":     "1/2/2020", // one-digit day and month stay
		"1-2-2020":     "1/2/2020",
		"1.2.2020":     "1/2/2020",
		"2024-07-24":   "2024-07-24", // a year first is never reshuffled into day and month
		"2024/07/24":   "2024/07/24",
		"20240724":     "20/24/0724", // eight run-together digits are always ddmmyyyy; the parser then refuses it
		"":             "",
		"abc":          "abc",
		"24/07/19831":  "24/07/19831",
		"24/07/1983x":  "24/07/1983x",
	}
	for in, want := range cases {
		if got := CleanTyped(in); got != want {
			t.Errorf("CleanTyped(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseDay(t *testing.T) {
	cases := []struct {
		in      string
		want    Day
		wantErr bool
	}{
		{"2026-11-06", "2026-11-06", false},
		{" 2026-11-06 ", "2026-11-06", false},
		{"2026-02-30", "", true}, // calendar-invalid
		{"06/11/2026", "", true},
		{"2026-11-06T00:00:00Z", "", true}, // no T/Z allowed by the date rule
		{"", "", true},
	}
	for _, c := range cases {
		got, err := ParseDay(c.in)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("ParseDay(%q) = %q, %v", c.in, got, err)
		}
	}
}

func TestParseMoment(t *testing.T) {
	m, err := ParseMoment("2026-11-06 07:30:00")
	if err != nil || m != "2026-11-06 07:30:00" {
		t.Fatalf("got %q, %v", m, err)
	}
	if m.Day() != "2026-11-06" {
		t.Errorf("Day() = %q", m.Day())
	}
	for _, bad := range []string{"2026-11-06", "2026-11-06T07:30:00", "2026-11-06 25:00:00", ""} {
		if _, err := ParseMoment(bad); err == nil {
			t.Errorf("ParseMoment(%q) should fail", bad)
		}
	}
}

func TestAddDays(t *testing.T) {
	cases := []struct {
		in   Day
		n    int
		want Day
	}{
		{"2026-02-28", 1, "2026-03-01"},
		{"2026-01-01", -1, "2025-12-31"},
		{"2028-02-28", 1, "2028-02-29"}, // leap year
		{"2026-11-06", 0, "2026-11-06"},
	}
	for _, c := range cases {
		if got := c.in.AddDays(c.n); got != c.want {
			t.Errorf("%s+%d = %s, want %s", c.in, c.n, got, c.want)
		}
	}
}

func TestDaysBetweenAndInclusive(t *testing.T) {
	if got := DaysBetween("2026-11-01", "2026-11-08"); got != 7 {
		t.Errorf("DaysBetween = %d", got)
	}
	if got := DaysBetween("2026-11-08", "2026-11-01"); got != -7 {
		t.Errorf("negative DaysBetween = %d", got)
	}
	if got := InclusiveDays("2026-11-01", "2026-11-01"); got != 1 {
		t.Errorf("InclusiveDays same day = %d", got)
	}
	if got := InclusiveDays("2026-11-01", "2026-11-07"); got != 7 {
		t.Errorf("InclusiveDays = %d", got)
	}
}

func TestAddMinutesAndHours(t *testing.T) {
	cases := []struct {
		in   Moment
		min  int
		want Moment
	}{
		{"2026-11-06 23:50:00", 15, "2026-11-07 00:05:00"},
		{"2026-11-06 00:05:00", -10, "2026-11-05 23:55:00"},
		{"bad", 5, "bad"},
	}
	for _, c := range cases {
		if got := c.in.AddMinutes(c.min); got != c.want {
			t.Errorf("%s+%d = %s", c.in, c.min, got)
		}
	}
	if got := Moment("2026-11-06 20:00:00").AddHours(12); got != "2026-11-07 08:00:00" {
		t.Errorf("AddHours = %s", got)
	}
}

func TestCompareByString(t *testing.T) {
	// Why: the whole date rule rests on plain string comparison being correct.
	if !(Day("2026-11-06") < Day("2026-12-01")) {
		t.Error("string order must equal calendar order")
	}
}

func TestNowJakartaIsUTCPlus7(t *testing.T) {
	m := FromUTC(2026, 11, 6, 20, 30, 0) // 20:30 UTC -> 03:30 next day Jakarta
	if m != "2026-11-07 03:30:00" {
		t.Errorf("got %q", m)
	}
	if got := NowJakarta(); len(got) != 19 {
		t.Errorf("NowJakarta() = %q", got)
	}
}

func TestClock(t *testing.T) {
	c := FixedClock("2026-11-06 06:00:00")
	if c.Now() != "2026-11-06 06:00:00" || c.Today() != "2026-11-06" {
		t.Errorf("fixed clock wrong: %q %q", c.Now(), c.Today())
	}
	if (SystemClock{}).Today() == "" {
		t.Error("system clock empty")
	}
}

func TestHoursBetweenIsWholeHoursAndNeverNegative(t *testing.T) {
	cases := []struct {
		from, to Moment
		want     int
		ok       bool
	}{
		{"2026-11-10 07:00:00", "2026-11-11 07:00:00", 24, true},
		{"2026-11-10 07:00:01", "2026-11-11 07:00:00", 23, true}, // a second short of 24h is 23h
		{"2026-11-10 07:00:00", "2026-11-10 07:59:59", 0, true},
		{"2026-11-12 07:00:00", "2026-11-10 07:00:00", 0, true}, // a time in the future waits 0 hours
		{"", "2026-11-10 07:00:00", 0, false},                   // unreadable
		{"2026-11-10 07:00:00", "garbage", 0, false},
	}
	for _, c := range cases {
		got, ok := HoursBetween(c.from, c.to)
		if got != c.want || ok != c.ok {
			t.Errorf("HoursBetween(%q, %q) = %d, %v; want %d, %v", c.from, c.to, got, ok, c.want, c.ok)
		}
	}
}
