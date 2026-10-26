package staff

import "testing"

func TestFormatStaffName(t *testing.T) {
	tests := []struct {
		before, name, after, want string
	}{
		{"dr", "John Doe", "", "dr. John Doe"},
		{"dr.", "John Doe", "MKKK", "dr. John Doe, MKKK"},
		{"", "John Doe", "MKKK", "John Doe, MKKK"},
		{"prof. dr", "John Doe", "Sp.KK", "prof. dr. John Doe, Sp.KK"},
		{"", "John Doe", "", "John Doe"},
	}
	for _, tc := range tests {
		got, err := FormatStaffName(tc.before, tc.name, tc.after)
		if err != nil {
			t.Fatalf("%q %q %q: %v", tc.before, tc.name, tc.after, err)
		}
		if got != tc.want {
			t.Fatalf("%q %q %q = %q, want %q", tc.before, tc.name, tc.after, got, tc.want)
		}
	}
}

func TestFormatStaffNameRequiresName(t *testing.T) {
	if _, err := FormatStaffName("dr", "  ", "MKKK"); err == nil {
		t.Fatal("expected error")
	}
}
