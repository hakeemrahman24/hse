package entrymcu

import (
	"crypto/subtle"
	_ "embed"
	"hse-medpost/internal/dates"
	"html/template"
	"net/http"
	"strings"
	"sync"
	"time"
)

//go:embed verify_page.html
var verifyPageHTML string

var (
	verifyTplOnce sync.Once
	verifyTpl     *template.Template
	verifyTplErr  error
)

type verifyRow struct {
	Label string
	Value string
}

type verifyView struct {
	OK            bool
	Banner        string
	Tone          string
	Rows          []verifyRow
	Authority     string
	ShowAuthority bool
}

func verifyTemplate() (*template.Template, error) {
	verifyTplOnce.Do(func() {
		verifyTpl, verifyTplErr = template.New("verify").Parse(verifyPageHTML)
	})
	return verifyTpl, verifyTplErr
}

func sealsMatch(got, want string) bool {
	got = strings.TrimSpace(got)
	want = strings.TrimSpace(want)
	if want == "" || len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func verifyViewFrom(d certData, now time.Time) verifyView {
	status := d.CheckUpStatus
	result := d.ResultCriteria
	until := d.ValidUntil
	stored := d.HcValidityStatus
	live := displayHcValidityStatus(&stored, &status, &until, &result, now)

	banner := "Authentic. This certificate matches the medical checkup record."
	tone := "ok"
	switch {
	case adverseResult(d.ResultCriteria):
		banner = "This record matches the certificate. It is not a clearance to work."
		if strings.HasPrefix(live, "Renewed by ") {
			banner += " A later checkup has replaced this record."
		}
		tone = "unfit"
	case live == "Archived / Exit" || strings.HasPrefix(live, "Renewed by "):
		banner = "This record matches the certificate, and it is no longer current."
		tone = "warn"
	case live == "ReEvaluate / Overdue":
		banner = "This record matches the certificate, and it is past its validity date."
		tone = "warn"
	case !certifiableResult(d.ResultCriteria) || d.CheckUpStatus != "Close":
		banner = "This checkup is not a closed fitness certificate."
		tone = "bad"
	}

	standing := live
	if standing == "" {
		if !isTerminalResult(d.ResultCriteria) || d.CheckUpStatus != "Close" {
			standing = "Not closed"
		} else {
			standing = "-"
		}
	}
	authority := strings.TrimSpace(d.AuthorityName)
	if authority == "" {
		authority = "-"
	}
	return verifyView{
		OK:            true,
		Banner:        banner,
		Tone:          tone,
		Authority:     authority,
		ShowAuthority: certIssuedUnderAuthority(d),
		Rows: []verifyRow{
			{"Document No.", orDash(strings.TrimSpace(d.CheckUpID))},
			{"Full name", orDash(strings.TrimSpace(d.Name))},
			{"Date of birth", formatCertDateOrDash(d.DateOfBirth)},
			{"Employer / company", orDash(strings.TrimSpace(d.CompanyName))},
			{"Position", orDash(strings.TrimSpace(d.WorkPosition))},
			{"Checkup purpose", orDash(strings.TrimSpace(d.CheckUpPurpose))},
			{"Examination date", formatCertDateOrDash(firstNonEmpty(d.DateCheckUp, d.ReviewDate))},
			{"Review date", formatCertDateOrDash(d.ReviewDate)},
			{"Reviewer", orDash(strings.TrimSpace(d.Reviewer))},
			{"Fitness status", fitnessStatusLabel(d.ResultCriteria)},
			{"Restrictions or notes", notesOrNone(d.ResultNotes)},
			{"Valid until", formatCertDateOrDash(d.ValidUntil)},
			{"Standing", standing},
		},
	}
}

// VerifyCertificate is public. The seal in the QR address must match the
// checkup summary before any worker details are shown. Clinical exam fields
// are not loaded for this page.
func (a *API) VerifyCertificate(cfg CertConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Robots-Tag", "noindex")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")

		view := verifyView{}
		checkUpID := strings.TrimSpace(r.PathValue("checkUpID"))
		seal := strings.TrimSpace(r.PathValue("seal"))
		if d, ok := a.fetchCertDataByCheckUpID(checkUpID); ok {
			want := generateMasterSeal(d.Name, d.CompanyName, d.ResultCriteria, d.ValidUntil, cfg.Secret)
			if sealsMatch(seal, want) {
				view = verifyViewFrom(d, dates.ClinicNow())
			}
		}
		tpl, err := verifyTemplate()
		if err != nil {
			http.Error(w, "verification page unavailable", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = tpl.Execute(w, view)
	}
}
