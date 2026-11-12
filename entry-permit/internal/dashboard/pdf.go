package dashboard

import (
	"fmt"
	"io"
	"strings"

	"github.com/go-pdf/fpdf"
)

// WHY these limits: the Pi shares 2 GB with other services and a PDF is built
// in memory. 5,000 rows is about 100 landscape pages, far beyond what anyone
// prints, and keeps one export to a few MB.
var (
	maxRows  = 5000
	compress = true // tests switch this off so the page text is readable
)

const (
	pageW, pageH = 297.0, 210.0 // A4 landscape, mm
	margin       = 10.0
	rowH         = 5.6
	headH        = 6.4
	footerSpace  = 12.0
	cellPad      = 1.0
	ellipsis     = "..."
	maxCellRunes = 200
)

// Doc is everything one PDF shows. It carries the same rows the page shows, so
// the file never holds more than the screen: no birth date, e-mail or file path.
type Doc struct {
	Tab, Title, Query string
	GeneratedAt, By   string
	Sync              string // the "gate data last synced" line, if any
	Rows              []Row  // a list tab
	Inside, Activity  []Row  // the Gate tab
}

var cWeeks = col{"Weeks in", 16, func(r Row) string { return r.Weeks }}

type col struct {
	head string
	w    float64
	get  func(Row) string
}

func windowText(r Row) string {
	if r.Arrival == "" {
		return ""
	}
	return r.Arrival + " to " + r.Departure
}

func kindLabel(r Row) string {
	if r.Kind == KindVisitor {
		return "Visitor"
	}
	return "Work"
}

var (
	cName    = col{"Name", 50, func(r Row) string { return r.Name }}
	cCompany = col{"Company", 52, func(r Row) string { return r.Company }}
	cPos     = col{"Position", 44, func(r Row) string { return r.Position }}
	cNumber  = col{"Number", 28, func(r Row) string { return r.ID }}
	cStatus  = col{"Status", 38, func(r Row) string { return r.Status }}
	cGate    = col{"Gate", 22, func(r Row) string { return r.Gate }}
	cSince   = col{"Since", 34, func(r Row) string { return r.At }}
	cOfficer = col{"Officer", 30, func(r Row) string { return r.Officer }}
)

// columnsFor picks the table of a tab. An unknown tab gets the registered table.
func columnsFor(tab string) []col {
	switch tab {
	case "pending":
		return []col{cName, cCompany, cPos, {"Request", 28, func(r Row) string { return r.ID }}, {"Status", 40, func(r Row) string { return r.Status }}, {"Window", 50, windowText}}
	case "active":
		return []col{cName, cCompany, cPos, cNumber, {"Status", 28, func(r Row) string { return r.Status }}, {"Window", 46, windowText}, cWeeks, cGate, cSince}
	case "scheduled":
		return []col{cName, cCompany, cPos, cNumber, {"Arrival", 28, func(r Row) string { return r.Arrival }}, {"Departure", 28, func(r Row) string { return r.Departure }}, cWeeks}
	case "rest":
		return []col{cName, cCompany, cNumber, {"Previous ended", 28, func(r Row) string { return r.PrevEnd }}, {"Next arrival", 28, func(r Row) string { return r.Arrival }}, {"Rest", 62, func(r Row) string { return r.RestLabel }}}
	case "inside":
		return []col{cName, cCompany, cNumber, cGate, cSince, cOfficer}
	case "activity":
		return []col{{"Time", 34, func(r Row) string { return r.At }}, cName, cNumber, {"Action", 22, func(r Row) string { return r.Action }}, {"Reason", 44, func(r Row) string { return r.Reason }}, cGate, cOfficer}
	}
	return []col{cName, cCompany, cPos, cNumber, {"Type", 16, kindLabel}, cStatus, {"Valid until", 26, func(r Row) string { return r.ValidUntil }}}
}

// WritePDF renders the lists of one dashboard tab as an A4 landscape PDF.
func WritePDF(w io.Writer, d Doc) error {
	p := fpdf.New("L", "mm", "A4", "")
	p.SetCompression(compress)
	p.SetTitle(d.Title, true)
	p.SetCreator("Entry Permit", true)
	p.SetMargins(margin, margin, margin)
	p.SetAutoPageBreak(false, 0) // page breaks are made by hand so the headings repeat
	p.AliasNbPages("{nb}")
	tr := p.UnicodeTranslatorFromDescriptor("") // UTF-8 to the fonts' Western European set

	doc := &pdfDoc{p: p, tr: tr, d: d}
	p.SetHeaderFunc(doc.header)
	p.SetFooterFunc(doc.footer)
	p.AddPage()

	if d.Tab == "gate" {
		doc.section(fmt.Sprintf("Inside now (%d)", len(d.Inside)), columnsFor("inside"), d.Inside, "No one is inside.")
		doc.section("Latest gate activity", columnsFor("activity"), d.Activity, "No gate activity yet.")
	} else {
		doc.summary()
		doc.table(columnsFor(d.Tab), d.Rows, "Nothing on this list.")
	}
	if err := p.Output(w); err != nil {
		return fmt.Errorf("write pdf: %w", err)
	}
	return nil
}

type pdfDoc struct {
	p  *fpdf.Fpdf
	tr func(string) string
	d  Doc
}

func (g *pdfDoc) header() {
	g.p.SetFont("Helvetica", "B", 13)
	g.p.SetTextColor(20, 20, 20)
	g.p.CellFormat(0, 7, g.tr(g.d.Title), "", 1, "L", false, 0, "")
	g.p.SetFont("Helvetica", "", 8)
	g.p.SetTextColor(95, 95, 95)
	line := "Generated " + g.d.GeneratedAt
	if g.d.By != "" {
		line += " by " + g.d.By
	}
	g.p.CellFormat(0, 4.5, g.tr(line), "", 1, "L", false, 0, "")
	g.p.Ln(2)
}

func (g *pdfDoc) footer() {
	g.p.SetY(-footerSpace)
	g.p.SetFont("Helvetica", "", 8)
	g.p.SetTextColor(95, 95, 95)
	g.p.CellFormat(0, 5, "Staff use only. Contains personal data.", "", 0, "L", false, 0, "")
	g.p.SetX(margin)
	g.p.CellFormat(0, 5, fmt.Sprintf("Page %d of {nb}", g.p.PageNo()), "", 0, "R", false, 0, "")
}

// summary is the line under the title: the search and the row count.
func (g *pdfDoc) summary() {
	g.p.SetFont("Helvetica", "", 9)
	g.p.SetTextColor(40, 40, 40)
	parts := []string{plural(len(g.d.Rows), "row")}
	if q := strings.TrimSpace(g.d.Query); q != "" {
		parts = append([]string{"Search: " + q}, parts...)
	}
	if g.d.Sync != "" {
		parts = append(parts, g.d.Sync)
	}
	g.p.CellFormat(0, 5.5, g.tr(strings.Join(parts, "  |  ")), "", 1, "L", false, 0, "")
	g.p.Ln(1.5)
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// section is a titled table, used twice on the Gate tab.
func (g *pdfDoc) section(title string, cols []col, rows []Row, empty string) {
	g.room(headH + rowH*2 + 8) // a heading never sits alone at the foot of a page
	g.p.SetFont("Helvetica", "B", 11)
	g.p.SetTextColor(20, 20, 20)
	g.p.CellFormat(0, 7, g.tr(title), "", 1, "L", false, 0, "")
	g.table(cols, rows, empty)
	g.p.Ln(3)
}

// room starts a new page when less than need mm remain above the footer.
func (g *pdfDoc) room(need float64) bool {
	if g.p.GetY()+need <= pageH-footerSpace {
		return false
	}
	g.p.AddPage()
	return true
}

func (g *pdfDoc) table(cols []col, rows []Row, empty string) {
	if len(rows) == 0 {
		g.p.SetFont("Helvetica", "I", 10)
		g.p.SetTextColor(95, 95, 95)
		g.p.CellFormat(0, 8, g.tr(empty), "", 1, "L", false, 0, "")
		return
	}
	cols = scaled(cols)
	shown := rows
	if len(shown) > maxRows {
		shown = shown[:maxRows]
	}
	g.heading(cols)
	for i, r := range shown {
		if g.room(rowH) {
			g.heading(cols)
		}
		g.p.SetFont("Helvetica", "", 8)
		g.p.SetTextColor(20, 20, 20)
		fill := i%2 == 1 // light banding keeps a long row readable
		if fill {
			g.p.SetFillColor(244, 244, 241)
		}
		for _, c := range cols {
			g.p.CellFormat(c.w, rowH, g.fit(c.get(r), c.w), "", 0, "L", fill, 0, "")
		}
		g.p.Ln(rowH)
	}
	if len(rows) > len(shown) {
		g.p.SetFont("Helvetica", "I", 8)
		g.p.SetTextColor(95, 95, 95)
		g.p.CellFormat(0, 6, g.tr(fmt.Sprintf("Showing the first %d of %d rows. Narrow the search to see the rest.", len(shown), len(rows))), "", 1, "L", false, 0, "")
	}
}

func (g *pdfDoc) heading(cols []col) {
	g.p.SetFont("Helvetica", "B", 8)
	g.p.SetFillColor(228, 232, 228)
	g.p.SetTextColor(20, 20, 20)
	for _, c := range cols {
		g.p.CellFormat(c.w, headH, g.tr(c.head), "B", 0, "L", true, 0, "")
	}
	g.p.Ln(headH)
}

// scaled stretches the columns to fill the page width between the margins.
func scaled(cols []col) []col {
	var sum float64
	for _, c := range cols {
		sum += c.w
	}
	k := (pageW - 2*margin) / sum
	out := make([]col, len(cols))
	for i, c := range cols {
		c.w *= k
		out[i] = c
	}
	return out
}

// fit cuts text with "..." so it never runs into the next column. It cuts the
// Unicode text first and translates after, because the translated text is no
// longer valid UTF-8.
func (g *pdfDoc) fit(s string, w float64) string {
	r := []rune(s)
	if len(r) > maxCellRunes {
		r = r[:maxCellRunes]
	}
	limit := w - 2*cellPad
	if g.p.GetStringWidth(g.tr(string(r))) <= limit {
		return g.tr(string(r))
	}
	for len(r) > 0 && g.p.GetStringWidth(g.tr(string(r)+ellipsis)) > limit {
		r = r[:len(r)-1]
	}
	return g.tr(string(r) + ellipsis)
}
