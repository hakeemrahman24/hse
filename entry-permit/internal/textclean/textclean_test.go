package textclean

import "testing"

func TestEmailCleansWhatWasPasted(t *testing.T) {
	cases := map[string]string{
		"  Rina@Example.COM ":              "rina@example.com",
		"rina @ example .com":              "rina@example.com",
		"mailto:rina@example.com":          "rina@example.com",
		"<rina@example.com>":               "rina@example.com",
		"\"rina@example.com\".":            "rina@example.com",
		"rina@example.com,":                "rina@example.com",
		"rina\u200b@example.com":           "rina@example.com", // zero-width space
		"rina\u00a0@example.com":           "rina@example.com", // no-break space
		"rina\uff20example\u3002com":       "rina@example.com", // full-width @ and dot
		"RINA.WIJAYA+Permit@Company.Co.ID": "rina.wijaya+permit@company.co.id",
	}
	for in, want := range cases {
		if got := Email(in); got != want {
			t.Errorf("Email(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidEmailCommonFormat(t *testing.T) {
	good := []string{"rina@example.com", "rina.wijaya@company.co.id", "r+permit@example.com", "a_b-c@sub.example.org", "x@xn--bcher-kva.de", "o'neil@example.ie"}
	bad := []string{"", "rina", "rina@", "@example.com", "rina@example", "rina@example.c", "rina@@example.com", "a@b@example.com",
		".rina@example.com", "rina.@example.com", "ri..na@example.com", "rina@-example.com", "rina@example-.com", "rina@exa_mple.com",
		"rina@example..com", "rina@.example.com", "rina@example.com.", "rina@example.123", "ri na@example.com", "rina@example.com,john@example.com",
		"rina@[127.0.0.1]", "\"rina\"@example.com", "rina@exa mple.com"}
	for _, s := range good {
		if !ValidEmail(s) {
			t.Errorf("ValidEmail(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if ValidEmail(s) {
			t.Errorf("ValidEmail(%q) = true, want false", s)
		}
	}
	long := make([]byte, 0, 80)
	for i := 0; i < 65; i++ {
		long = append(long, 'a')
	}
	if ValidEmail(string(long) + "@example.com") {
		t.Error("a local part over 64 characters is valid")
	}
}

func TestEmailSuggestionCatchesTypedDomains(t *testing.T) {
	cases := map[string]string{
		"a@gmial.com":     "a@gmail.com",
		"a@gmail.co":      "a@gmail.com",
		"a@gnail.com":     "a@gmail.com",
		"a@hotmial.com":   "a@hotmail.com",
		"a@yaho.com":      "a@yahoo.com",
		"a@outlok.com":    "a@outlook.com",
		"a@gmail.com":     "", // already right
		"a@example.com":   "", // not near a popular domain
		"a@company.co.id": "",
		"a@yahoo.co.id":   "", // a real Indonesian domain, not a typo of yahoo.com
	}
	for in, want := range cases {
		if got := EmailSuggestion(in); got != want {
			t.Errorf("EmailSuggestion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNameIsCleanedAndTitleStyled(t *testing.T) {
	cases := map[string]string{
		"budi santoso":           "Budi Santoso",
		"BUDI SANTOSO":           "Budi Santoso",
		"  siti   aminah  ":      "Siti Aminah",
		"Budi123 Santoso":        "Budi Santoso",
		"Budi_Santoso":           "Budi Santoso",
		"Budi @ Santoso!":        "Budi Santoso",
		"abdul rahman BIN ahmad": "Abdul Rahman bin Ahmad",
		"BIN laden":              "Bin Laden", // a particle first in the name is a name
		"ludwig van beethoven":   "Ludwig van Beethoven",
		"anne-marie o'neil":      "Anne-Marie O'Neil",
		"O’NEIL":                 "O'Neil",
		"jean—luc picard":        "Jean-Luc Picard",
		"john a. smith iii":      "John A. Smith III",
		"j. r. r. tolkien":       "J. R. R. Tolkien",
		"McDonald":               "McDonald", // typed with an inner capital on purpose
		"DeShawn Washington":     "DeShawn Washington",
		"sAntoso":                "Santoso",
		"smith, john":            "Smith, John",
		"smith ,, john":          "Smith, John",
		"-- budi --":             "Budi",
		"émile zola":             "Émile Zola",
		"zoë":                    "Zoë",
		"1234 5678":              "",
		"   ":                    "",
		"Budi\u200b Santoso":     "Budi Santoso", // zero-width space
		"nguyễn văn a":           "Nguyễn Văn A",
	}
	for in, want := range cases {
		if got := Name(in); got != want {
			t.Errorf("Name(%q) = %q, want %q", in, got, want)
		}
	}
}
