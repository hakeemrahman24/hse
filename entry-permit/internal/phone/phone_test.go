package phone

import "testing"

func TestGolden29Valid(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"081234567890", true},
		{"0812345", false},
		{"+60123456789", true},
		{"6281234567890", true}, // Indonesia without leading 0
		{"+62 812-3456-7890", true},
		{"", false},
		{"0812345678901234", false}, // too long for Indonesia
		{"12345", false},
	}
	for _, c := range cases {
		if got := Valid(c.in); got != c.want {
			t.Errorf("Valid(%q)=%v want %v", c.in, got, c.want)
		}
	}
}

func TestSplit(t *testing.T) {
	cases := []struct{ in, cc, local string }{
		{"", "62", ""},
		{"0812345678", "62", "0812345678"},
		{"6281234567890", "62", "081234567890"},
		{"60123456789", "60", "123456789"},
		{"8521234567", "852", "1234567"}, // longest code wins over "85..."
		{"999", "62", "999"},             // too short to split: stays Indonesian
	}
	for _, c := range cases {
		cc, local := Split(c.in)
		if cc != c.cc || local != c.local {
			t.Errorf("Split(%q)=%q,%q want %q,%q", c.in, cc, local, c.cc, c.local)
		}
	}
}

func TestLocalValid(t *testing.T) {
	if LocalValid("", "123") || LocalValid("62", "") || LocalValid("1234", "1234567") {
		t.Error("blank or long cc must fail")
	}
	if !LocalValid("62", "81234567890") { // no leading 0 is promoted
		t.Error("indonesian without 0 should pass")
	}
	if LocalValid("60", "123456") || !LocalValid("60", "1234567") || LocalValid("60", "1234567890123456") {
		t.Error("foreign length range")
	}
}

func TestNormalize(t *testing.T) {
	if Normalize("0812-345") != "62812345" || Normalize("+60 12") != "6012" || Normalize("") != "" {
		t.Error("Normalize")
	}
}
