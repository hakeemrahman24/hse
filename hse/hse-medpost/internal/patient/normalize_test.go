package patient

import "testing"

func TestNormalizePersonName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"O'hara", "O'Hara"},
		{"as'adul", "As'adul"},
		{"AS’ADUL", "As'adul"},
		{"o'connor", "O'Connor"},
		{"d'angelo", "D'Angelo"},
		{"ma'ruf", "Ma'ruf"},
		{"m.yusuf", "M.Yusuf"},
		{"JOHN DOE", "John Doe"},
		{"  john   doe  ", "John Doe"},
		{"John2", "John"},
		{"mary-jane", "Mary-Jane"},
		{"nur-aini", "Nur-Aini"},
		{"José  GARCÍA", "José García"},
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
