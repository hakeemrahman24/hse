package entrymcu

import (
	"bytes"
	"fmt"
	"hse-medpost/internal/dates"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
)

// Summary PDFs are drawn with go-pdf/fpdf, the same library as certificates.
// Charts are vectors (rects and polygons), not embedded images and not a
// browser PDF library, so a summary stays small on the Orange Pi 3B's 2 GB.

const dashPDFMargin = 14.0

type dashPDFOptions struct {
	From       string
	To         string
	Company    string
	Chart      string // bar | pie
	StatusView string // combined | perCompany
}

func dashboardPDFFilename(from, to string) string {
	return "MCU_dashboard_" + dashSafeToken(from) + "_" + dashSafeToken(to) + ".pdf"
}

func dashSafeToken(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "na"
	}
	var b strings.Builder
	gap := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
		if ok {
			if gap && b.Len() > 0 {
				b.WriteByte('_')
			}
			gap = false
			b.WriteRune(r)
			continue
		}
		gap = true
	}
	if b.Len() == 0 {
		return "na"
	}
	return b.String()
}

// packTwoColumnCount is how many remaining blocks fit on one page when the
// page is split into two columns (left takes the first ceil(n/2), right the
// rest). A single block that is taller than the page still takes the page
// so the list can move on.
func packTwoColumnCount(heights []float64, usable float64) int {
	take := 0
	for n := 1; n <= len(heights); n++ {
		leftN := (n + 1) / 2
		rightN := n - leftN
		if sumHeights(heights[:leftN]) <= usable && sumHeights(heights[leftN:leftN+rightN]) <= usable {
			take = n
			continue
		}
		break
	}
	if take == 0 && len(heights) > 0 {
		return 1
	}
	return take
}

func sumHeights(heights []float64) float64 {
	total := 0.0
	for _, h := range heights {
		total += h
	}
	return total
}

type pdfDoc struct {
	pdf                  *fpdf.Fpdf
	tr                   func(string) string
	y                    float64
	margin, pageW, pageH float64
	contentW             float64
}

func newPDFDoc() *pdfDoc {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetAutoPageBreak(false, 0)
	pdf.SetMargins(dashPDFMargin, dashPDFMargin, dashPDFMargin)
	pdf.AddPage()
	pageW, pageH := pdf.GetPageSize()
	return &pdfDoc{
		pdf:      pdf,
		tr:       pdf.UnicodeTranslatorFromDescriptor(""),
		y:        dashPDFMargin,
		margin:   dashPDFMargin,
		pageW:    pageW,
		pageH:    pageH,
		contentW: pageW - 2*dashPDFMargin,
	}
}

// pdfSafe keeps text inside the core font width table (runes 0-255).
// SplitText indexes that table by rune, so a translated cp1252 byte string
// or a rune above 255 panics. Drawing still runs the result through enc.
func pdfSafe(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\u00b7':
			b.WriteString(" | ")
		case '\u2014', '\u2013':
			b.WriteString(" - ")
		case '\u2026':
			b.WriteString("...")
		default:
			if r > 255 {
				b.WriteByte('?')
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func (c *pdfDoc) enc(s string) string {
	return c.tr(pdfSafe(s))
}

func (c *pdfDoc) remaining() float64 {
	return c.pageH - c.margin - c.y
}

func (c *pdfDoc) newPage() {
	c.pdf.AddPage()
	c.y = c.margin
}

func (c *pdfDoc) ensure(mm float64) {
	if c.y+mm > c.pageH-c.margin {
		c.newPage()
	}
}

func renderDashboardPDF(full DashboardAnalytics, opts dashPDFOptions) ([]byte, error) {
	if opts.Chart != "pie" {
		opts.Chart = "bar"
	}
	// The on-screen per-company view stays in the browser. The PDF always
	// prints the overall tables and charts, including a company filter.
	view := filterDashboard(full, opts.Company)

	c := newPDFDoc()
	c.pdf.SetTextColor(0, 0, 0)
	c.pdf.SetFont("Helvetica", "B", 16)
	c.pdf.Text(c.margin, c.y+5, c.enc("MCU Review Dashboard"))
	c.y += 10

	c.writeWrapped(fmt.Sprintf("Range: From %s to %s", dashLongDate(opts.From), dashLongDate(opts.To)), 11, 5.2)
	c.y += 2
	c.writeWrapped(fmt.Sprintf("%d record%s", view.Total, pluralS(view.Total)), 10, 4.8)
	if strings.TrimSpace(opts.Company) != "" {
		c.writeWrapped("Company filter: "+opts.Company, 10, 4.8)
	}
	c.y += 2

	if view.Total == 0 {
		c.writeWrapped("No records in this review date range.", 11, 5.2)
		return c.bytes()
	}

	leftW := c.contentW * 0.47
	rightW := c.contentW * 0.47
	rightX := c.margin + leftW + c.contentW*0.06

	c.splitSection("1. Check-up status", companySubtitle(opts.Company, false), "", view.StatusRows, opts.Chart, false, leftW, rightX, rightW, func(x, y, w float64) float64 {
		return c.drawOpenCloseTable(x, y, w, view)
	}, func(float64) float64 {
		return openCloseTableHeight(view)
	})
	purposeTotal := sumCounts(view.PurposeRows)
	purposeNote := ""
	if gap := view.Total - purposeTotal; gap > 0 {
		noun, verb := "visits", "have"
		if gap == 1 {
			noun, verb = "visit", "has"
		}
		purposeNote = fmt.Sprintf("%d %s %s no purpose recorded, so this chart totals %d of %d.", gap, noun, verb, purposeTotal, view.Total)
	}
	c.splitSection("2. Purpose", "", purposeNote, view.PurposeRows, opts.Chart, true, leftW, rightX, rightW, func(x, y, w float64) float64 {
		return c.drawTitledCountTable(x, y, w, "Purpose", view.PurposeRows, purposeTotal, true)
	}, func(w float64) float64 {
		return c.countTableHeight(w, "Purpose", view.PurposeRows, true)
	})
	c.splitSection("3. Result criteria", "", resultGapNote(view), view.ResultCriteriaRows, opts.Chart, false, leftW, rightX, rightW, func(x, y, w float64) float64 {
		return c.drawTitledCountTable(x, y, w, "Result criteria", view.ResultCriteriaRows, view.ResultCriteriaTotal, true)
	}, func(w float64) float64 {
		return c.countTableHeight(w, "Result criteria", view.ResultCriteriaRows, true)
	})

	if len(view.CompanyPivot) > 0 {
		if c.remaining() < 40 {
			c.newPage()
		}
		c.pdf.SetFont("Helvetica", "B", 11)
		c.pdf.SetTextColor(0, 0, 0)
		c.pdf.Text(c.margin, c.y+3, c.enc("Records by company"))
		c.y += 6
		totalLabel := "Total (all companies)"
		if strings.TrimSpace(opts.Company) != "" {
			totalLabel = "Total (selected company)"
		}
		c.y = c.drawPivot(c.margin, c.y, c.contentW, view.CompanyPivot, view.Total, totalLabel)
		c.y += 4
	}

	c.ensure(12)
	c.pdf.SetFont("Helvetica", "", 8)
	c.pdf.SetTextColor(120, 120, 120)
	footer := fmt.Sprintf("Generated %s UTC", time.Now().UTC().Format("2006-01-02 15:04:05"))
	c.pdf.Text(c.margin, c.y+3, c.enc(footer))
	c.y += 5
	c.ensure(4)
	c.pdf.Text(c.margin, c.y+3, c.enc("Developed by dr. Arif Rahman Hakim"))
	return c.bytes()
}

func (c *pdfDoc) bytes() ([]byte, error) {
	if err := c.pdf.Error(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := c.pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (c *pdfDoc) writeWrapped(text string, size, lineH float64) {
	c.pdf.SetTextColor(0, 0, 0)
	c.pdf.SetFont("Helvetica", "", size)
	for _, line := range c.pdf.SplitText(pdfSafe(text), c.contentW) {
		c.ensure(lineH)
		c.pdf.Text(c.margin, c.y+lineH-1, c.enc(line))
		c.y += lineH
	}
}

func (c *pdfDoc) splitSection(title, subtitle, note string, rows []dashCount, chart string, tall bool, leftW, rightX, rightW float64, drawLeft func(x, y, width float64) float64, measureLeft func(width float64) float64) {
	maxH := 70.0
	if tall {
		maxH = 88
	}
	headH := c.sectionHeadHeight(subtitle, note)
	leftH := 6.0
	if measureLeft != nil {
		leftH = measureLeft(leftW)
	}
	need := headH + math.Max(leftH, c.chartBlockHeight(rows, chart, rightW, maxH))
	// Keep the title, table, and chart on one page. A section that would
	// cross the bottom margin starts on the next page instead.
	if c.y+need > c.pageH-c.margin && c.y > c.margin+0.5 {
		c.newPage()
	}
	c.pdf.SetTextColor(0, 0, 0)
	c.pdf.SetFont("Helvetica", "B", 11)
	c.pdf.Text(c.margin, c.y+3, c.enc(title))
	c.y += 6
	if subtitle != "" {
		c.pdf.SetFont("Helvetica", "", 9)
		c.pdf.SetTextColor(80, 80, 80)
		c.pdf.Text(c.margin, c.y+3, c.enc(subtitle))
		c.y += 5
		c.pdf.SetTextColor(0, 0, 0)
	}
	if note != "" {
		c.pdf.SetFont("Helvetica", "", 8)
		c.pdf.SetTextColor(90, 90, 90)
		for _, line := range c.pdf.SplitText(pdfSafe(note), c.contentW) {
			c.ensure(3.8)
			c.pdf.Text(c.margin, c.y+3, c.enc(line))
			c.y += 3.8
		}
		c.pdf.SetTextColor(0, 0, 0)
		c.y += 2
	}

	startY := c.y
	startPage := c.pdf.PageNo()
	rightY := c.drawChart(rightX, startY, rightW, maxH, rows, chart)
	leftY := startY
	if drawLeft != nil {
		leftY = drawLeft(c.margin, leftY, leftW)
	}
	if c.pdf.PageNo() != startPage {
		c.y = leftY + 6
		return
	}
	c.y = math.Max(leftY, rightY) + 6
}

func (c *pdfDoc) sectionHeadHeight(subtitle, note string) float64 {
	h := 6.0
	if subtitle != "" {
		h += 5
	}
	if note != "" {
		c.pdf.SetFont("Helvetica", "", 8)
		h += 3.8*float64(len(c.pdf.SplitText(pdfSafe(note), c.contentW))) + 2
	}
	return h
}

func (c *pdfDoc) chartBlockHeight(rows []dashCount, chart string, width, maxH float64) float64 {
	if len(rows) == 0 || sumCounts(rows) == 0 {
		return 6
	}
	if chart == "pie" {
		d := dashPieDiameter
		gap := 5.0
		minLegend := 30.0
		if d+gap+minLegend > width {
			d = math.Max(32, width-gap-minLegend)
		}
		legendH := 4.2 * float64(len(rows))
		return math.Max(d, legendH) + 2
	}
	rowH := 6.0
	need := rowH*float64(len(rows)) + 2
	if need > maxH {
		rowH = math.Max(4, (maxH-2)/float64(len(rows)))
	}
	return rowH*float64(len(rows)) + 1
}

func openCloseTableHeight(view DashboardAnalytics) float64 {
	n := 0
	for _, name := range view.Companies {
		rows := view.CompanyStatusRows[name]
		if sumCounts(rows) > 0 {
			n++
		}
	}
	if n == 0 {
		return 6
	}
	h := 5.4 + 4.2*float64(n)
	if n > 1 {
		h += 4.2
	}
	return h + 4.2 + 6
}

func (c *pdfDoc) countTableHeight(width float64, title string, rows []dashCount, withShare bool) float64 {
	h := 0.0
	if title != "" {
		h += 4
	}
	if len(rows) == 0 {
		return h + 6
	}
	labelW := width * 0.56
	if !withShare {
		labelW = width * 0.72
	}
	h += 5.7
	c.pdf.SetFont("Helvetica", "", 8)
	for _, row := range rows {
		lines := c.pdf.SplitText(pdfSafe(row.Label), labelW-5)
		n := len(lines)
		if n == 0 {
			n = 1
		}
		h += 4 * float64(n)
	}
	return h + 2
}

func (c *pdfDoc) pastMargin(y, rowH float64) bool {
	return y+rowH > c.pageH-c.margin
}

func (c *pdfDoc) drawTitledCountTable(x, y, width float64, title string, rows []dashCount, total int, withShare bool) float64 {
	if title != "" {
		c.pdf.SetFont("Helvetica", "B", 8)
		c.pdf.SetTextColor(80, 80, 80)
		c.pdf.Text(x, y+3, c.enc(title))
		y += 4
		c.pdf.SetTextColor(0, 0, 0)
	}
	return c.drawCountTable(x, y, width, rows, total, withShare)
}

func dashPct(part, whole int) string {
	if whole <= 0 {
		return "0.0%"
	}
	return fmt.Sprintf("%.1f%%", float64(part)/float64(whole)*100)
}

func countStatusKey(rows []dashCount, key string) int {
	n := 0
	for _, row := range rows {
		if row.Key == key {
			n += row.Count
		}
	}
	return n
}

func (c *pdfDoc) drawOpenCloseTable(x, y, width float64, view DashboardAnalytics) float64 {
	type line struct {
		name                       string
		open, closeN, other, total int
	}
	lines := make([]line, 0, len(view.Companies))
	showOther := false
	for _, name := range view.Companies {
		rows := view.CompanyStatusRows[name]
		open := countStatusKey(rows, "Open")
		closeN := countStatusKey(rows, "Close")
		other := 0
		for _, row := range rows {
			if row.Key != "Open" && row.Key != "Close" {
				other += row.Count
			}
		}
		total := open + closeN + other
		if total == 0 {
			continue
		}
		if other > 0 {
			showOther = true
		}
		lines = append(lines, line{name: name, open: open, closeN: closeN, other: other, total: total})
	}
	if len(lines) == 0 {
		c.pdf.SetFont("Helvetica", "", 9)
		c.pdf.SetTextColor(0, 0, 0)
		c.pdf.Text(x, y+3, c.enc("No data."))
		return y + 6
	}
	cols := 3
	if showOther {
		cols = 4
	}
	nameW := width * 0.40
	numW := (width - nameW) / float64(cols)
	head := func(y0 float64) float64 {
		c.pdf.SetFont("Helvetica", "B", 7)
		c.pdf.SetTextColor(0, 0, 0)
		c.pdf.SetXY(x, y0)
		c.pdf.CellFormat(nameW, 4, "Company", "", 0, "L", false, 0, "")
		c.pdf.CellFormat(numW, 4, "Open", "", 0, "R", false, 0, "")
		c.pdf.CellFormat(numW, 4, "Close", "", 0, "R", false, 0, "")
		if showOther {
			c.pdf.CellFormat(numW, 4, "Other", "", 0, "R", false, 0, "")
		}
		c.pdf.CellFormat(numW, 4, "Total", "", 0, "R", false, 0, "")
		y0 += 4.2
		c.pdf.SetDrawColor(190, 190, 190)
		c.pdf.Line(x, y0, x+width, y0)
		return y0 + 1.2
	}
	writeNums := func(open, closeN, other, total int, asPct bool, whole int) {
		cell := func(n int) {
			text := strconv.Itoa(n)
			if asPct {
				text = dashPct(n, whole)
			}
			c.pdf.CellFormat(numW, 4, text, "", 0, "R", false, 0, "")
		}
		cell(open)
		cell(closeN)
		if showOther {
			cell(other)
		}
		cell(total)
	}
	y = head(y)
	sums := line{}
	c.pdf.SetFont("Helvetica", "", 7)
	for _, row := range lines {
		if c.pastMargin(y, 4.2) {
			c.newPage()
			y = head(c.margin)
			c.pdf.SetFont("Helvetica", "", 7)
		}
		c.pdf.SetTextColor(0, 0, 0)
		c.pdf.SetXY(x, y)
		c.pdf.CellFormat(nameW, 4, c.enc(trimLabel(c, row.name, nameW-1)), "", 0, "L", false, 0, "")
		writeNums(row.open, row.closeN, row.other, row.total, false, 0)
		sums.open += row.open
		sums.closeN += row.closeN
		sums.other += row.other
		sums.total += row.total
		y += 4.2
	}
	if len(lines) > 1 {
		if c.pastMargin(y, 4.2) {
			c.newPage()
			y = head(c.margin)
		}
		c.pdf.SetFont("Helvetica", "B", 7)
		c.pdf.SetXY(x, y)
		c.pdf.CellFormat(nameW, 4, "Total", "", 0, "L", false, 0, "")
		writeNums(sums.open, sums.closeN, sums.other, sums.total, false, 0)
		y += 4.2
	}
	if c.pastMargin(y, 4.2) {
		c.newPage()
		y = head(c.margin)
	}
	c.pdf.SetFont("Helvetica", "B", 7)
	c.pdf.SetTextColor(70, 70, 70)
	c.pdf.SetXY(x, y)
	c.pdf.CellFormat(nameW, 4, "Share of total", "", 0, "L", false, 0, "")
	writeNums(sums.open, sums.closeN, sums.other, sums.total, true, sums.total)
	c.pdf.SetTextColor(0, 0, 0)
	return y + 6
}

func (c *pdfDoc) drawCountTable(x, y, width float64, rows []dashCount, total int, withShare bool) float64 {
	if len(rows) == 0 {
		c.pdf.SetFont("Helvetica", "", 9)
		c.pdf.SetTextColor(0, 0, 0)
		c.pdf.Text(x, y+3, c.enc("No data."))
		return y + 6
	}
	labelW := width * 0.56
	countW := width * 0.22
	shareW := width - labelW - countW
	if !withShare {
		labelW = width * 0.72
		countW = width - labelW
		shareW = 0
	}
	drawHead := func(y0 float64, continued bool) float64 {
		c.pdf.SetFont("Helvetica", "B", 7)
		c.pdf.SetTextColor(0, 0, 0)
		item := "Item"
		if continued {
			item = "Item (continued)"
		}
		c.pdf.SetXY(x, y0)
		c.pdf.CellFormat(labelW, 4, c.enc(item), "", 0, "L", false, 0, "")
		c.pdf.CellFormat(countW, 4, "Count", "", 0, "R", false, 0, "")
		if withShare {
			c.pdf.CellFormat(shareW, 4, "Share", "", 0, "R", false, 0, "")
		}
		y0 += 4.2
		c.pdf.SetDrawColor(190, 190, 190)
		c.pdf.Line(x, y0, x+width, y0)
		return y0 + 1.5
	}
	y = drawHead(y, false)
	c.pdf.SetFont("Helvetica", "", 8)
	for _, row := range rows {
		pct := "0.0%"
		if total > 0 {
			pct = fmt.Sprintf("%.1f%%", float64(row.Count)/float64(total)*100)
		}
		lines := c.pdf.SplitText(pdfSafe(row.Label), labelW-5)
		if len(lines) == 0 {
			lines = []string{""}
		}
		for i, line := range lines {
			if c.pastMargin(y, 4) {
				c.newPage()
				y = drawHead(c.margin, true)
				c.pdf.SetFont("Helvetica", "", 8)
			}
			c.pdf.SetTextColor(0, 0, 0)
			if i == 0 {
				r, g, b := parseHex(row.Color)
				c.pdf.SetFillColor(r, g, b)
				c.pdf.Rect(x, y+0.6, 2.4, 2.4, "F")
			}
			c.pdf.SetXY(x+3.2, y)
			c.pdf.CellFormat(labelW-3.2, 4, c.enc(line), "", 0, "L", false, 0, "")
			if i == 0 {
				c.pdf.SetXY(x+labelW, y)
				c.pdf.CellFormat(countW, 4, strconv.Itoa(row.Count), "", 0, "R", false, 0, "")
				if withShare {
					c.pdf.SetXY(x+labelW+countW, y)
					c.pdf.CellFormat(shareW, 4, pct, "", 0, "R", false, 0, "")
				}
			}
			y += 4
		}
	}
	return y + 2
}

func (c *pdfDoc) drawChart(x, y, width, maxH float64, rows []dashCount, chart string) float64 {
	if len(rows) == 0 || sumCounts(rows) == 0 {
		c.pdf.SetFont("Helvetica", "", 9)
		c.pdf.SetTextColor(0, 0, 0)
		c.pdf.Text(x, y+3, c.enc("No chart data."))
		return y + 6
	}
	if chart == "pie" {
		return c.drawPie(x, y, width, maxH, rows)
	}
	return c.drawBars(x, y, width, maxH, rows)
}

func (c *pdfDoc) drawBars(x, y, width, maxH float64, rows []dashCount) float64 {
	maxCount := 1
	for _, row := range rows {
		if row.Count > maxCount {
			maxCount = row.Count
		}
	}
	rowH := 6.0
	need := rowH*float64(len(rows)) + 2
	if need > maxH && len(rows) > 0 {
		rowH = math.Max(4, (maxH-2)/float64(len(rows)))
	}
	labelW := width * 0.38
	barMax := width * 0.42
	total := sumCounts(rows)
	c.pdf.SetFont("Helvetica", "", 7)
	for _, row := range rows {
		c.pdf.SetTextColor(0, 0, 0)
		c.pdf.SetXY(x, y)
		c.pdf.CellFormat(labelW, rowH, c.enc(trimLabel(c, row.Label, labelW-1)), "", 0, "L", false, 0, "")
		bw := barMax * float64(row.Count) / float64(maxCount)
		if bw < 0.4 {
			bw = 0.4
		}
		r, g, b := parseHex(row.Color)
		c.pdf.SetFillColor(r, g, b)
		c.pdf.Rect(x+labelW, y+1, bw, math.Max(2, rowH-2.5), "F")
		c.pdf.SetXY(x+labelW+barMax, y)
		c.pdf.CellFormat(width-labelW-barMax, rowH, dashPct(row.Count, total), "", 0, "R", false, 0, "")
		y += rowH
	}
	return y + 1
}

func trimLabel(c *pdfDoc, label string, maxW float64) string {
	if c.pdf.GetStringWidth(c.enc(label)) <= maxW {
		return label
	}
	cut := label
	for len(cut) > 1 && c.pdf.GetStringWidth(c.enc(cut+"...")) > maxW {
		cut = cut[:len(cut)-1]
	}
	return strings.TrimSpace(cut) + "..."
}

// dashPieDiameter is the same circle for every chart. A larger pie used to
// push that chart's labels under the circle while the others kept them beside it.
const dashPieDiameter = 46.0

func (c *pdfDoc) drawPie(x, y, width, _ float64, rows []dashCount) float64 {
	d := dashPieDiameter
	gap := 5.0
	minLegend := 30.0
	if d+gap+minLegend > width {
		d = math.Max(32, width-gap-minLegend)
	}
	legendW := width - d - gap
	lineH := 4.2
	legendH := lineH * float64(len(rows))
	pieTop := y
	legTop := y
	if legendH < d {
		legTop = y + (d-legendH)/2
	} else {
		pieTop = y + (legendH-d)/2
	}
	c.paintPie(x+d/2, pieTop+d/2, d/2, rows)
	legX := x + d + gap
	legY := legTop
	total := sumCounts(rows)
	c.pdf.SetFont("Helvetica", "", 7)
	for _, row := range rows {
		r, g, b := parseHex(row.Color)
		c.pdf.SetFillColor(r, g, b)
		c.pdf.Rect(legX, legY+0.8, 2.2, 2.2, "F")
		c.pdf.SetTextColor(0, 0, 0)
		c.pdf.SetXY(legX+3.2, legY)
		label := row.Label + "  " + dashPct(row.Count, total)
		c.pdf.CellFormat(legendW-3.2, lineH, c.enc(trimLabel(c, label, legendW-4)), "", 0, "L", false, 0, "")
		legY += lineH
	}
	return math.Max(pieTop+d, legTop+legendH) + 2
}

func (c *pdfDoc) paintPie(cx, cy, radius float64, rows []dashCount) {
	total := sumCounts(rows)
	if total == 0 || radius <= 0 {
		return
	}
	if len(rows) == 1 {
		r, g, b := parseHex(rows[0].Color)
		c.pdf.SetFillColor(r, g, b)
		c.pdf.Circle(cx, cy, radius, "F")
		return
	}
	angle := -math.Pi / 2
	for _, row := range rows {
		sweep := 2 * math.Pi * float64(row.Count) / float64(total)
		if sweep <= 0 {
			continue
		}
		c.paintWedge(cx, cy, radius, angle, angle+sweep, row.Color)
		angle += sweep
	}
}

func (c *pdfDoc) paintWedge(cx, cy, radius, start, end float64, color string) {
	span := end - start
	n := int(math.Max(2, math.Abs(span)/(math.Pi/18)))
	pts := make([]fpdf.PointType, 0, n+2)
	pts = append(pts, fpdf.PointType{X: cx, Y: cy})
	for i := 0; i <= n; i++ {
		t := start + span*float64(i)/float64(n)
		pts = append(pts, fpdf.PointType{X: cx + radius*math.Cos(t), Y: cy + radius*math.Sin(t)})
	}
	r, g, b := parseHex(color)
	c.pdf.SetFillColor(r, g, b)
	c.pdf.Polygon(pts, "F")
}

func (c *pdfDoc) drawPivot(x, y, width float64, pivot []dashCompanyBlock, grand int, totalLabel string) float64 {
	gutter := 6.0
	colW := (width - gutter) / 2
	leftX := x
	rightX := x + colW + gutter
	heights := make([]float64, len(pivot))
	for i := range pivot {
		heights[i] = c.measureBlock(pivot[i], colW)
	}
	idx := 0
	continued := false
	for idx < len(pivot) {
		headY := y
		if continued {
			c.newPage()
			headY = c.margin
		}
		usable := (c.pageH - c.margin) - (headY + 7)
		if usable < 12 {
			usable = 12
		}
		take := packTwoColumnCount(heights[idx:], usable)
		leftN := (take + 1) / 2
		rightN := take - leftN
		if idx+leftN > len(pivot) {
			leftN = len(pivot) - idx
			rightN = 0
		}
		if idx+leftN+rightN > len(pivot) {
			rightN = len(pivot) - idx - leftN
		}
		yL := c.pivotHeader(leftX, headY, colW, continued)
		yR := c.pivotHeader(rightX, headY, colW, continued)
		for i := 0; i < leftN; i++ {
			yL = c.drawBlock(leftX, yL, colW, pivot[idx+i])
		}
		for i := 0; i < rightN; i++ {
			yR = c.drawBlock(rightX, yR, colW, pivot[idx+leftN+i])
		}
		idx += leftN + rightN
		y = math.Max(yL, yR)
		continued = true
	}
	if y+8 > c.pageH-c.margin {
		c.newPage()
		y = c.margin
	}
	c.pdf.SetDrawColor(190, 190, 190)
	c.pdf.Line(x, y, x+width, y)
	y += 4
	c.pdf.SetFont("Helvetica", "B", 8)
	c.pdf.SetTextColor(0, 0, 0)
	c.pdf.SetXY(x, y)
	c.pdf.CellFormat(width*0.7, 4, c.enc(totalLabel), "", 0, "L", false, 0, "")
	c.pdf.SetXY(x+width*0.7, y)
	c.pdf.CellFormat(width*0.3, 4, strconv.Itoa(grand), "", 0, "R", false, 0, "")
	return y + 5
}

func (c *pdfDoc) pivotHeader(x, y, width float64, continued bool) float64 {
	c.pdf.SetFont("Helvetica", "B", 7)
	c.pdf.SetTextColor(0, 0, 0)
	title := "Company / breakdown"
	if continued {
		title = "Company / breakdown (continued)"
	}
	c.pdf.SetXY(x, y)
	c.pdf.CellFormat(width*0.7, 3.5, c.enc(title), "", 0, "L", false, 0, "")
	c.pdf.SetXY(x+width*0.7, y)
	c.pdf.CellFormat(width*0.3, 3.5, "Records", "", 0, "R", false, 0, "")
	y += 4
	c.pdf.SetDrawColor(190, 190, 190)
	c.pdf.Line(x, y, x+width, y)
	return y + 3
}

func (c *pdfDoc) measureBlock(block dashCompanyBlock, width float64) float64 {
	c.pdf.SetFont("Helvetica", "B", 8)
	h := float64(lineCount(c.pdf.SplitText(pdfSafe(block.Company), width-16))) * 3.6
	if len(block.PurposeRows) > 0 {
		h += 3.2
		c.pdf.SetFont("Helvetica", "", 7.5)
		for _, row := range block.PurposeRows {
			h += float64(lineCount(c.pdf.SplitText(pdfSafe(row.Label), width-18))) * 3.3
		}
	}
	if len(block.ResultCriteriaRows) > 0 {
		h += 3.2
		c.pdf.SetFont("Helvetica", "", 7.5)
		for _, row := range block.ResultCriteriaRows {
			h += float64(lineCount(c.pdf.SplitText(pdfSafe(row.Label), width-18))) * 3.3
		}
	}
	return h + 1.2
}

func lineCount(lines []string) int {
	if len(lines) < 1 {
		return 1
	}
	return len(lines)
}

func (c *pdfDoc) drawBlock(x, y, width float64, block dashCompanyBlock) float64 {
	c.pdf.SetFont("Helvetica", "B", 8)
	c.pdf.SetTextColor(0, 0, 0)
	lines := c.pdf.SplitText(pdfSafe(block.Company), width-16)
	if len(lines) == 0 {
		lines = []string{""}
	}
	for i, line := range lines {
		c.pdf.SetXY(x, y)
		c.pdf.CellFormat(width*0.7, 3.6, c.enc(line), "", 0, "L", false, 0, "")
		if i == 0 {
			c.pdf.SetXY(x+width*0.7, y)
			c.pdf.CellFormat(width*0.3, 3.6, strconv.Itoa(block.Total), "", 0, "R", false, 0, "")
		}
		y += 3.6
	}
	y = c.drawPivotGroup(x, y, width, "Purpose", block.PurposeRows)
	y = c.drawPivotGroup(x, y, width, "Result criteria", block.ResultCriteriaRows)
	return y + 1.2
}

func (c *pdfDoc) drawPivotGroup(x, y, width float64, title string, rows []dashCount) float64 {
	if len(rows) == 0 {
		return y
	}
	c.pdf.SetFont("Helvetica", "B", 6.5)
	c.pdf.SetTextColor(90, 90, 90)
	c.pdf.Text(x+1.5, y+2.4, c.enc(title))
	y += 3.2
	c.pdf.SetFont("Helvetica", "", 7.5)
	c.pdf.SetTextColor(0, 0, 0)
	for _, row := range rows {
		lines := c.pdf.SplitText(pdfSafe(row.Label), width-18)
		if len(lines) == 0 {
			lines = []string{""}
		}
		for i, line := range lines {
			c.pdf.SetXY(x+3, y)
			c.pdf.CellFormat(width*0.7-3, 3.3, c.enc(line), "", 0, "L", false, 0, "")
			if i == 0 {
				c.pdf.SetXY(x+width*0.7, y)
				c.pdf.CellFormat(width*0.3, 3.3, strconv.Itoa(row.Count), "", 0, "R", false, 0, "")
			}
			y += 3.3
		}
	}
	return y
}

func sumCounts(rows []dashCount) int {
	n := 0
	for _, row := range rows {
		n += row.Count
	}
	return n
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func dashLongDate(iso string) string {
	t, err := time.Parse(dateOnly, strings.TrimSpace(iso))
	if err != nil {
		if strings.TrimSpace(iso) == "" {
			return "—"
		}
		return iso
	}
	return t.Format("02 January 2006")
}

func companySubtitle(company string, perCompany bool) string {
	if perCompany {
		return "Per company pie view"
	}
	if strings.TrimSpace(company) != "" {
		return "Filtered to: " + company
	}
	return ""
}

func resultGapNote(view DashboardAnalytics) string {
	if view.ClosedNoResultCriteriaCount <= 0 {
		return ""
	}
	return fmt.Sprintf("%d closed row(s) have no result criteria — %d closed in cohort; %d with an outcome below.", view.ClosedNoResultCriteriaCount, view.ClosedCount, view.ResultCriteriaTotal)
}

func parseHex(s string) (int, int, int) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return 84, 93, 106
	}
	n, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 84, 93, 106
	}
	return int(n >> 16), int((n >> 8) & 0xff), int(n & 0xff)
}

func (a *API) loadDashboardAnalytics(from, to string) (DashboardAnalytics, error) {
	supSQL, supArgs := dashSupersededSQL(from, to)
	query := `
		SELECT COALESCE(co.Name,''), COALESCE(c.Name,''),
		       m.ResultCriteria, m.CheckUpStatus, m.HcValidityStatus, m.ResultValidUntil,
		       ` + supSQL + `
		FROM McuRecord m
		LEFT JOIN Company co ON co.CompanyID = m.FK_CompanyID
		LEFT JOIN CheckUpCriteriaTable c ON c.CheckUpCriteriaID = m.FK_CheckUpCriteriaID`
	args := []any{}
	var conds []string
	if from != "" {
		conds = append(conds, "m.ReviewDate >= ?")
		args = append(args, from)
	}
	if to != "" {
		conds = append(conds, "m.ReviewDate <= ?")
		args = append(args, to)
	}
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}

	rows, err := a.DB.Query(query, append(append([]any{}, supArgs...), args...)...)
	if err != nil {
		return DashboardAnalytics{}, err
	}
	defer rows.Close()

	acc := newDashAccum()
	now := dates.ClinicNow()
	for rows.Next() {
		var company, criteria string
		var result, status, storedHC, validUntil *string
		var superseded bool
		if err := rows.Scan(&company, &criteria, &result, &status, &storedHC, &validUntil, &superseded); err != nil {
			return DashboardAnalytics{}, err
		}
		acc.add(dashSrc{
			Company:  company,
			Criteria: criteria,
			Result:   derefStr(result),
			Status:   derefStr(status),
			HC:       displayHcValidityStatus(storedHC, status, validUntil, result, now),

			Superseded: superseded,
		})
	}
	if err := rows.Err(); err != nil {
		return DashboardAnalytics{}, err
	}
	return acc.finish(), nil
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (a *API) GetMcuDashboardPDF(w http.ResponseWriter, r *http.Request) {
	from, fromOK := parseDashDate(r.URL.Query().Get("from"))
	to, toOK := parseDashDate(r.URL.Query().Get("to"))
	if !fromOK || !toOK {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "dates must be YYYY-MM-DD"})
		return
	}
	if from != "" && to != "" && from > to {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from date must be on or before to date"})
		return
	}
	analytics, err := a.loadDashboardAnalytics(from, to)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	body, err := renderDashboardPDF(analytics, dashPDFOptions{
		From:       from,
		To:         to,
		Company:    r.URL.Query().Get("company"),
		Chart:      r.URL.Query().Get("chart"),
		StatusView: r.URL.Query().Get("statusView"),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not build pdf"})
		return
	}
	name := dashboardPDFFilename(from, to)
	disp := "inline"
	if r.URL.Query().Get("download") == "1" {
		disp = "attachment"
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"`, disp, name))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func parseDashDate(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", true
	}
	if _, err := time.Parse(dateOnly, s); err != nil {
		return "", false
	}
	return s, true
}
