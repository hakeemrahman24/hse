package patient

import "testing"

func TestNormalizePersonName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"O'hara", "Ohara"},
		{"JOHN DOE", "John Doe"},
		{"  john   doe  ", "John Doe"},
		{"John2", "John"},
		{"mary-jane", "Maryjane"},
	}
	for _, tc := range cases {
		if got := NormalizePersonName(tc.in); got != tc.want {
			t.Errorf("NormalizePersonName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeWorkPosition(t *testing.T) {
	cases := []struct{ in, want string }{
		{"welder  3g", "Welder 3G"},
		{"  WELDER   3G  ", "Welder 3G"},
		{"Welder-3g!", "Welder3G"},
	}
	for _, tc := range cases {
		if got := NormalizeWorkPosition(tc.in); got != tc.want {
			t.Errorf("NormalizeWorkPosition(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
