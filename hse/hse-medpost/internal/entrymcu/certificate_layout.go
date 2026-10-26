package entrymcu

import (
	"bytes"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
)

const (
	certNavyR = 0
	certNavyG = 0x33
	certNavyB = 0x66
	certLeft  = 18.0
	// Bottom of the page: notice, then a thin rule, then the developer credit
	// (drawn at y=290). The signature block must finish above certNoticeY.
	certNoticeY       = 268.0
	certNoticeReserve = 22.0
	certCreditRuleY   = 285.0
	certRight         = 192.0
)

// GenerateCertificatePDF draws the fitness-to-work recommendation certificate.
// Document number is the checkup id. The QR code opens the public verification
// page for that checkup.
func GenerateCertificatePDF(d certData, cfg CertConfig) ([]byte, error) {
	width := certRight - certLeft
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(certLeft, 12, 210-certRight)
	pdf.SetAutoPageBreak(false, 12)
	pdf.SetTitle("Fitness-to-work recommendation certificate", true)
	pdf.SetAuthor("PT Layar Nusantara Gas", true)
	pdf.AddPage()
	tr := pdf.UnicodeTranslatorFromDescriptor("")

	warn := adverseResult(d.ResultCriteria)
	y := drawCertHeader(pdf, tr, cfg)
	if err := pdf.Error(); err != nil {
		return nil, err
	}

	title := "FITNESS-TO-WORK RECOMMENDATION CERTIFICATE"
	pdf.SetTextColor(0, 0, 0)
	size := 12.0
	pdf.SetFont("Helvetica", "B", size)
	for pdf.GetStringWidth(tr(title)) > width && size > 8 {
		size -= 0.5
		pdf.SetFont("Helvetica", "B", size)
	}
	pdf.SetXY(certLeft, y)
	pdf.CellFormat(width, 7, tr(title), "", 1, "C", false, 0, "")
	y = pdf.GetY() + 6

	pdf.SetTextColor(0, 0, 0)
	pdf.SetXY(certLeft, y)
	pdf.SetFont("Helvetica", "B", 11)
	idLabel := "Certificate ID: "
	labelW := pdf.GetStringWidth(tr(idLabel))
	pdf.CellFormat(labelW, 6, tr(idLabel), "", 0, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 11)
	pdf.CellFormat(width-labelW, 6, tr(orDash(strings.TrimSpace(d.CheckUpID))), "", 1, "L", false, 0, "")
	y = pdf.GetY() + 4

	exam := formatCertDateOrDash(firstNonEmpty(d.DateCheckUp, d.ReviewDate))
	pdf.SetXY(certLeft, y)
	pdf.SetTextColor(0, 0, 0)
	pdf.SetFont("Helvetica", "", 10.5)
	pdf.MultiCell(width, 5.2, tr(
		"Based on the results of the medical fitness examination conducted on "+exam+
			", we hereby certify that the following worker:"), "", "J", false)
	pdf.Ln(3)

	drawCertFacts(pdf, tr, [][2]string{
		{"Name", d.Name},
		{"Date of Birth", formatCertDateOrDash(d.DateOfBirth)},
		{"Employer / Company", d.CompanyName},
		{"Position", d.WorkPosition},
		{"Fitness Status", fitnessStatusLabel(d.ResultCriteria)},
	}, warn)

	pdf.Ln(3)
	pdf.SetX(certLeft)
	pdf.SetFont("Helvetica", "B", 10.5)
	pdf.SetTextColor(0, 0, 0)
	pdf.CellFormat(width, 5.5, tr("Restrictions or notes (if any)"), "", 1, "L", false, 0, "")
	pdf.SetX(certLeft)
	pdf.SetFont("Helvetica", "", 10.5)
	pdf.SetTextColor(0, 0, 0)
	pdf.MultiCell(width, 5.2, tr(notesOrNone(d.ResultNotes)), "", "L", false)

	writeCertSection(pdf, tr, width, "Validity", certValidityBody(d))
	writeCertSection(pdf, tr, width, "Use of this document",
		"This certificate may only be used for the internal activities of PT LNG and may not be used for any other purpose.")

	if err := pdf.Error(); err != nil {
		return nil, err
	}
	drawCertSignature(pdf, tr, d, cfg)
	drawCertNotice(pdf, tr, width)
	drawCertCredit(pdf, tr)
	if err := pdf.Error(); err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func certValidityBody(d certData) string {
	switch d.ResultCriteria {
	case "Unfit":
		return "This document is not a clearance to work. The worker is unfit, and this checkup is closed."
	case "TemporaryUnfit":
		return "This document is not a clearance to work. The worker is temporarily unfit, and this checkup stays open for review."
	default:
		return "This certificate is valid until " + formatCertDateOrDash(d.ValidUntil) +
			". It becomes invalid before that date if there is any change in the worker's health condition, position or work location, or in the applicable procedures."
	}
}

func writeCertSection(pdf *fpdf.Fpdf, tr func(string) string, width float64, heading, body string) {
	pdf.Ln(4)
	pdf.SetX(certLeft)
	pdf.SetFont("Helvetica", "B", 11)
	pdf.SetTextColor(0, 0, 0)
	pdf.CellFormat(width, 6, tr(heading), "", 1, "L", false, 0, "")
	pdf.SetX(certLeft)
	pdf.SetFont("Helvetica", "", 10.5)
	pdf.SetTextColor(0, 0, 0)
	pdf.MultiCell(width, 5.2, tr(body), "", "J", false)
}

// drawCertHeader places logo.png on the left and centers the company name
// in the width that remains beside it.
func drawCertHeader(pdf *fpdf.Fpdf, tr func(string) string, cfg CertConfig) float64 {
	const nameH = 7.0
	const subH = 5.0
	headerH := nameH + subH
	logoH := headerH * 1.10
	y := 12.0
	logoW := 0.0
	logoY := y + (headerH-logoH)/2
	if logo := certificateLogo(cfg); logo != "" {
		info := pdf.RegisterImageOptions(logo, fpdf.ImageOptions{})
		if info != nil && info.Height() > 0 {
			logoW = logoH * info.Width() / info.Height()
			pdf.ImageOptions(logo, certLeft, logoY, logoW, logoH, false, fpdf.ImageOptions{}, 0, "")
		}
	}

	textX := certLeft
	textW := certRight - certLeft
	if logoW > 0 {
		textX = certLeft + logoW + 4
		textW = certRight - textX
	}
	textY := y
	pdf.SetTextColor(certNavyR, certNavyG, certNavyB)
	pdf.SetFont("Helvetica", "B", 13)
	pdf.SetXY(textX, textY)
	pdf.CellFormat(textW, nameH, tr("PT LAYAR NUSANTARA GAS"), "", 1, "C", false, 0, "")
	pdf.SetFont("Helvetica", "I", 9)
	pdf.SetTextColor(0x55, 0x55, 0x55)
	pdf.SetXY(textX, textY+nameH)
	pdf.CellFormat(textW, subH, tr("A member of the Genting Group | Incorporated in Indonesia"), "", 1, "C", false, 0, "")

	bottom := y + nameH + subH
	if logoBottom := logoY + logoH; logoW > 0 && logoBottom > bottom {
		bottom = logoBottom
	}
	if pdf.GetY() > bottom {
		bottom = pdf.GetY()
	}
	bottom += 3
	pdf.SetDrawColor(certNavyR, certNavyG, certNavyB)
	pdf.SetLineWidth(0.7)
	pdf.Line(certLeft, bottom, certRight, bottom)
	return bottom + 12
}

// drawCertFacts prints the worker rows indented, with the colons in one column
// and no rules between the rows.
func drawCertFacts(pdf *fpdf.Fpdf, tr func(string) string, rows [][2]string, warn bool) {
	const indent = 8.0
	const colonW = 5.0
	const rowH = 6.2
	pdf.SetFont("Helvetica", "", 10.5)
	labelW := 0.0
	for _, row := range rows {
		if w := pdf.GetStringWidth(tr(row[0])); w > labelW {
			labelW = w
		}
	}
	labelW += 3
	x := certLeft + indent
	valueW := certRight - x - labelW - colonW
	for _, row := range rows {
		valueRed := warn && row[0] == "Fitness Status"
		pdf.SetX(x)
		pdf.SetFont("Helvetica", "", 10.5)
		pdf.SetTextColor(0, 0, 0)
		pdf.CellFormat(labelW, rowH, tr(row[0]), "", 0, "L", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
		pdf.CellFormat(colonW, rowH, ":", "", 0, "L", false, 0, "")
		pdf.SetFont("Helvetica", "B", 10.5)
		if valueRed {
			pdf.SetTextColor(180, 0, 0)
		}
		pdf.CellFormat(valueW, rowH, tr(orDash(strings.TrimSpace(row[1]))), "", 1, "L", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
	}
}

// drawCertSignature places the sign block on the right:
// date, QR, then "Issued under authority of" when another doctor reviewed,
// then the active Chief Medical Officer's name and position.
// The reviewing doctor's name is not printed.
func drawCertSignature(pdf *fpdf.Fpdf, tr func(string) string, d certData, cfg CertConfig) {
	const qrSize = 32.0
	const blockW = 78.0
	const lineH = 5.4
	authority := strings.TrimSpace(d.AuthorityName)
	if authority == "" {
		authority = "-"
	}
	otherDoctor := certIssuedUnderAuthority(d)
	signDate := formatCertSignDate(firstNonEmpty(d.ReviewDate, d.DateCheckUp))
	above := 0.0
	if signDate != "" {
		above = lineH
	}
	below := lineH * 2
	if otherDoctor {
		below += lineH
	}
	y := pdf.GetY() + 8
	if y+above+qrSize+below+certNoticeReserve > certNoticeY {
		pdf.AddPage()
		y = 18
	}
	x := certRight - blockW
	cursor := y
	pdf.SetTextColor(0, 0, 0)
	if signDate != "" {
		pdf.SetFont("Helvetica", "", 10)
		pdf.SetXY(x, cursor)
		pdf.CellFormat(blockW, lineH, tr(signDate), "", 1, "C", false, 0, "")
		cursor += lineH
	}

	seal := generateMasterSeal(d.Name, d.CompanyName, d.ResultCriteria, d.ValidUntil, cfg.Secret)
	verifyURL := certificateVerifyURL(cfg.BaseURL, d.CheckUpID, seal)
	if qrPNG, err := certificateQRCode(verifyURL, faviconImagePath(cfg)); err == nil {
		name := "qr-" + seal
		pdf.RegisterImageOptionsReader(name, fpdf.ImageOptions{ImageType: "PNG"}, bytes.NewReader(qrPNG))
		pdf.ImageOptions(name, x+(blockW-qrSize)/2, cursor, qrSize, qrSize, false, fpdf.ImageOptions{ImageType: "PNG"}, 0, "")
	}
	cursor += qrSize + 2

	if otherDoctor {
		pdf.SetFont("Helvetica", "", 10)
		pdf.SetXY(x, cursor)
		pdf.CellFormat(blockW, lineH, tr("Issued under authority of"), "", 1, "C", false, 0, "")
		cursor += lineH
	}
	pdf.SetFont("Helvetica", "B", 11)
	pdf.SetXY(x, cursor)
	pdf.CellFormat(blockW, lineH, tr(authority), "", 1, "C", false, 0, "")
	cursor += lineH
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetXY(x, cursor)
	pdf.CellFormat(blockW, lineH, tr("Chief Medical Officer"), "", 1, "C", false, 0, "")
	cursor += lineH

}

// drawCertNotice prints the electronic-signature notice in the lower part of
// the page, just above the footer line and developer credit.
func drawCertNotice(pdf *fpdf.Fpdf, tr func(string) string, width float64) {
	pdf.SetXY(certLeft, certNoticeY)
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(0, 0, 0)
	pdf.MultiCell(width, 4.4, tr(
		"This document was issued electronically and sealed with an electronic signature. It does not require a wet signature. Verify its authenticity by scanning the QR code."),
		"", "L", false)
}

// certIssuedUnderAuthority is true when the checkup was reviewed by someone
// other than the active Chief Medical Officer. Only then is "Issued under
// authority of" shown on the certificate and the verification page.
func certIssuedUnderAuthority(d certData) bool {
	return strings.TrimSpace(d.AuthorityName) == "" || !sameCertPerson(d.Reviewer, d.AuthorityName)
}

func sameCertPerson(a, b string) bool {
	return normalizeKey(a) != "" && normalizeKey(a) == normalizeKey(b)
}

// drawCertCredit sits in the bottom-left corner of the page, under a thin
// rule that separates it from the notice above.
func drawCertCredit(pdf *fpdf.Fpdf, tr func(string) string) {
	pdf.SetDrawColor(160, 160, 160)
	pdf.SetLineWidth(0.2)
	pdf.Line(certLeft, certCreditRuleY, certRight, certCreditRuleY)
	pdf.SetFont("Helvetica", "", 8)
	pdf.SetTextColor(120, 120, 120)
	pdf.Text(certLeft, 290, tr("Developed by dr. Arif Rahman Hakim"))
}

// certificateLogo is the flame mark (logo.png). An explicit path still wins.
// The wide wordmark is not used here, because the company name is printed beside the logo.
func certificateLogo(cfg CertConfig) string {
	if explicit := strings.TrimSpace(cfg.LogoPath); explicit != "" {
		if path := brandImage(explicit, cfg.UploadDir, "logo"); path != "" {
			return path
		}
	}
	return brandImage("", cfg.UploadDir, "logo")
}

func formatCertDate(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	layouts := []string{
		"2006-01-02",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"02 January 2006",
		"2 January 2006",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.Format("02 January 2006")
		}
	}
	if len(raw) >= 10 {
		if parsed, err := time.Parse("2006-01-02", raw[:10]); err == nil {
			return parsed.Format("02 January 2006")
		}
	}
	return raw
}

func formatCertDateOrDash(raw string) string {
	return orDash(formatCertDate(raw))
}

func formatCertSignDate(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	layouts := []string{
		"2006-01-02",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"Monday, 02 January 2006",
		"02 January 2006",
		"2 January 2006",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.Format("Monday, 02 January 2006")
		}
	}
	if len(raw) >= 10 {
		if parsed, err := time.Parse("2006-01-02", raw[:10]); err == nil {
			return parsed.Format("Monday, 02 January 2006")
		}
	}
	return raw
}

func fitnessStatusLabel(result string) string {
	switch normalizeKey(result) {
	case "fit":
		return "Fit"
	case "fitwithnote", "fitwithrestriction", "fitwithrestrictions":
		return "Fit with Recommendations / Notes"
	case "temporaryunfit", "temporarilyunfit":
		return "Temporarily Unfit"
	case "unfit":
		return "Unfit"
	default:
		return orDash(strings.TrimSpace(result))
	}
}

func notesOrNone(notes string) string {
	s := strings.TrimSpace(notes)
	if s == "" || s == "-" {
		return "None"
	}
	return s
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
