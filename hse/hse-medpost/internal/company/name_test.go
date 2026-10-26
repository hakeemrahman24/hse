package company

import "testing"

func TestNormalizeCompanyName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  JOHN   DOE ", "JOHN DOE"},
		{"china eleventh", "China Eleventh"},
		{"  china   eleventh  ", "China Eleventh"},
		{"PT. STARWAY", "PT. Starway"},
		{"STARWAY ENGINEERING INDONESIA", "Starway Engineering Indonesia"},
		{"KSO Sikars Petroleum Indonesia", "KSO Sikars Petroleum Indonesia"},
		{"Layar Nusantara Gas", "Layar Nusantara Gas"},
		{"YILKI", "YILKI"},
		{"KSO-IBT", "KSO-IBT"},
		{"IBTKPN", "IBTKPN"},
		{"STARWAYS", "Starways"},
		{"McDonald Indonesia", "McDonald Indonesia"},
	}
	for _, tc := range cases {
		if got := NormalizeCompanyName(tc.in); got != tc.want {
			t.Errorf("NormalizeCompanyName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
