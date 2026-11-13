package card

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif" // registers the decoders asJPEG relies on
	"image/jpeg"
	_ "image/png"
	"io"
	"strings"

	"github.com/go-pdf/fpdf"
	"rsc.io/qr"
)

// logoWhite is the white company logo on a transparent background, the same file the site header uses.
// WHY a copy inside this package: go:embed cannot reach web/static, and the PDF must not depend on files
// being found at run time.
//
//go:embed assets/logo-white.png
var logoWhite []byte

// logoH is the printed logo height in the header band; the width follows the picture (371 x 96).
const logoH = 10.0

// compress is switched off by tests so the page text can be read in the bytes.
var compress = true

// 148 x 105 mm, landscape (the size is given wide-first with "P": for a custom size fpdf swaps it on "L"); the size of a printed ID card sheet that folds into a wallet.
const (
	pdfW, pdfH = 148.0, 105.0
	pdfMargin  = 6.0
	headerH    = 15.0
	photoW     = 28.0
	photoH     = 36.0
	qrBox      = 32.0
)

// PDFCard is everything one card shows. The worker downloads their own card, so the name is
// in full (the public card page masks it). No birth date, e-mail or address is ever on it.
type PDFCard struct {
	LEP, Name, Company, Parent, Position string
	ValidUntil                           string // as printed, for example "31 Jan 2027"
	State                                State
	Entity                               string // picks the colour, as on the card page
	Payload                              string // what the QR code holds (the ticket address)
	Photo                                []byte // a JPEG, or nil; anything else is left out
	GeneratedAt                          string
}

// PDF writes the card as a one-page PDF.
//
// WHY the state is printed in words and colour: a downloaded file outlives the permit, so the
// page must say plainly whether it was Valid when it was made; the QR code and the gate always
// check the live record, never the file.
func PDF(w io.Writer, c PDFCard) error {
	code, err := qr.Encode(c.Payload, qr.M)
	if err != nil {
		return fmt.Errorf("encode QR: %w", err)
	}
	p := fpdf.NewCustom(&fpdf.InitType{OrientationStr: "P", UnitStr: "mm", Size: fpdf.SizeType{Wd: pdfW, Ht: pdfH}})
	p.SetCompression(compress)
	p.SetMargins(pdfMargin, pdfMargin, pdfMargin)
	p.SetAutoPageBreak(false, 0)
	p.SetTitle("Entry Permit "+c.LEP, false)
	tr := p.UnicodeTranslatorFromDescriptor("") // UTF-8 to the fonts' Western European set
	p.AddPage()

	// Header band in the card colour.
	r, g, b := themeRGB(ThemeFor(c.Entity))
	p.SetFillColor(r, g, b)
	p.Rect(0, 0, pdfW, headerH, "F")
	logoOpt := fpdf.ImageOptions{ImageType: "PNG"}
	p.RegisterImageOptionsReader("logo", logoOpt, bytes.NewReader(logoWhite))
	p.ImageOptions("logo", pdfMargin, (headerH-logoH)/2, 0, logoH, false, logoOpt, 0, "")
	p.SetTextColor(255, 255, 255)
	p.SetFont("Helvetica", "B", 12)
	p.SetXY(pdfW-pdfMargin-60, 3)
	p.CellFormat(60, 9, tr(c.LEP), "", 0, "R", false, 0, "")

	// Photo (left), details (middle), QR code (right).
	textX := pdfMargin
	if drawPhoto(p, c.Photo, pdfMargin, headerH+6) {
		textX = pdfMargin + photoW + 5
	}
	qrX := pdfW - pdfMargin - qrBox
	textW := qrX - textX - 4
	y := headerH + 6.0
	for _, line := range []struct {
		text     string
		style    string
		size     float64
		gray     bool
		maxLines int // a long company name wraps onto a second line before it is cut with "..."
	}{{c.Name, "B", 13, false, 2}, {c.Company, "", 10, false, 2}, {c.Parent, "", 9, true, 1}, {c.Position, "", 10, false, 1}} {
		if strings.TrimSpace(line.text) == "" {
			continue
		}
		p.SetFont("Helvetica", line.style, line.size)
		if line.gray {
			p.SetTextColor(100, 100, 100)
		} else {
			p.SetTextColor(20, 20, 20)
		}
		for _, l := range wrapLines(p, tr(line.text), textW, line.maxLines) {
			p.SetXY(textX, y)
			p.CellFormat(textW, 5.2, l, "", 0, "L", false, 0, "")
			y += 5.2
		}
		y += 1.3
	}
	p.SetTextColor(20, 20, 20)
	p.SetFont("Helvetica", "", 10)
	p.SetXY(textX, y+1)
	p.CellFormat(textW, 6, "Valid until: "+tr(c.ValidUntil), "", 0, "L", false, 0, "")

	drawQR(p, code, qrX, headerH+6, qrBox)
	p.SetFont("Helvetica", "", 7)
	p.SetTextColor(100, 100, 100)
	p.SetXY(qrX, headerH+6+qrBox+1)
	p.CellFormat(qrBox, 4, "Scan at the gate", "", 0, "C", false, 0, "")

	// State, large, at the bottom.
	sr, sg, sb := stateRGB(c.State)
	p.SetFillColor(sr, sg, sb)
	p.Rect(pdfMargin, pdfH-27, pdfW-2*pdfMargin, 11, "F")
	p.SetTextColor(255, 255, 255)
	p.SetFont("Helvetica", "B", 14)
	p.SetXY(pdfMargin, pdfH-27)
	p.CellFormat(pdfW-2*pdfMargin, 11, c.State.Label(), "", 0, "C", false, 0, "")

	p.SetFont("Helvetica", "", 6.5)
	p.SetTextColor(100, 100, 100)
	p.SetXY(pdfMargin, pdfH-12)
	p.MultiCell(pdfW-2*pdfMargin, 3.2, tr("This file shows the permit when it was made ("+c.GeneratedAt+"). The gate always checks the live record, so a printed or saved copy does not make an expired permit valid. Contains personal data."), "", "L", false)
	return p.Output(w)
}

// asJPEG returns the photo as a JPEG: a JPEG is used as it is; a PNG or GIF (photos imported from
// the old Drive are whatever the worker uploaded) is decoded, flattened onto white so transparent
// edges do not turn black, and re-encoded. ok is false for anything else, including a damaged file.
func asJPEG(data []byte) ([]byte, bool) {
	if _, err := jpeg.DecodeConfig(bytes.NewReader(data)); err == nil {
		return data, true
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}
	flat := image.NewRGBA(img.Bounds())
	draw.Draw(flat, flat.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), img, img.Bounds().Min, draw.Over)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, flat, &jpeg.Options{Quality: 90}); err != nil {
		return nil, false
	}
	return out.Bytes(), true
}

// drawPhoto places the photo when it is a readable image and reports whether it did. A bad file
// is left out rather than failing the card: the permit matters more than its picture.
func drawPhoto(p *fpdf.Fpdf, data []byte, x, y float64) bool {
	if len(data) == 0 {
		return false
	}
	data, ok := asJPEG(data)
	if !ok {
		return false
	}
	opt := fpdf.ImageOptions{ImageType: "JPG"}
	p.RegisterImageOptionsReader("photo", opt, bytes.NewReader(data))
	p.ImageOptions("photo", x, y, photoW, photoH, false, opt, 0, "")
	return p.Ok()
}

// drawQR draws the code as black squares on white inside a box of the given size.
func drawQR(p *fpdf.Fpdf, code *qr.Code, x, y, size float64) {
	p.SetFillColor(255, 255, 255)
	p.Rect(x, y, size, size, "F")
	m := size / float64(code.Size+2*quietZone)
	p.SetFillColor(0, 0, 0)
	for row := 0; row < code.Size; row++ {
		for col := 0; col < code.Size; col++ {
			if code.Black(col, row) {
				p.Rect(x+float64(col+quietZone)*m, y+float64(row+quietZone)*m, m+0.02, m+0.02, "F")
			}
		}
	}
}

// fit cuts text with "..." so it never runs past the width.
// wrapLines breaks s into at most maxLines lines that fit width in the current font. Text that still does not
// fit on the last line is cut with "...", so a very long name never runs into the QR code.
func wrapLines(p *fpdf.Fpdf, s string, width float64, maxLines int) []string {
	var out []string
	for _, l := range p.SplitText(s, width) {
		out = append(out, strings.TrimSpace(string(l)))
	}
	if len(out) <= maxLines {
		return out
	}
	last := strings.Join(out[maxLines-1:], " ")
	return append(out[:maxLines-1], fit(p, last, width))
}

func fit(p *fpdf.Fpdf, s string, width float64) string {
	if p.GetStringWidth(s) <= width {
		return s
	}
	rs := []rune(s)
	for len(rs) > 0 && p.GetStringWidth(string(rs)+"...") > width {
		rs = rs[:len(rs)-1]
	}
	return strings.TrimSpace(string(rs)) + "..."
}

func themeRGB(theme string) (int, int, int) {
	if theme == ThemeContractor {
		return 180, 83, 9
	}
	return 30, 64, 175
}

func stateRGB(s State) (int, int, int) {
	if s == Valid {
		return 21, 128, 61
	}
	return 185, 28, 28
}
