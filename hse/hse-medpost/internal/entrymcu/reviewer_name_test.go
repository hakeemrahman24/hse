package entrymcu

import (
	"testing"

	"hse-medpost/internal/auth"
)

func TestReviewerNameForSession(t *testing.T) {
	cases := []struct {
		name string
		sess *auth.Session
		want string
	}{
		{"built-in admin uses alias", &auth.Session{Role: "admin", Name: "Administrator"}, "dr. Arif Rahman Hakim"},
		{"chief medical officer uses own name", &auth.Session{Role: "admin", Name: "dr. Edric Sugito, MKKK"}, "dr. Edric Sugito, MKKK"},
		{"staff uses own name", &auth.Session{Role: "staff", Name: "Siti Aminah"}, "Siti Aminah"},
		{"nil session is empty", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ReviewerNameForSession(tc.sess); got != tc.want {
				t.Fatalf("ReviewerNameForSession = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCertIssuedUnderAuthority(t *testing.T) {
	cmo := "dr. Edric Sugito, MKKK"
	cases := []struct {
		name     string
		reviewer string
		want     bool
	}{
		{"CMO reviewed hides line", cmo, false},
		{"other reviewer shows line", "Siti Aminah", true},
		{"alias reviewer shows line", "dr. Arif Rahman Hakim", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := certIssuedUnderAuthority(certData{Reviewer: tc.reviewer, AuthorityName: cmo}); got != tc.want {
				t.Fatalf("certIssuedUnderAuthority = %v, want %v", got, tc.want)
			}
		})
	}
}
