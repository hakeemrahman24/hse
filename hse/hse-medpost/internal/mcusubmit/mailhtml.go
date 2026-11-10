package mcusubmit

import (
	"html"
	"strings"
)

// The HTML side of the staff digest and the expiry reminder: one small table per list. Mail
// clients (Gmail, Outlook) ignore <style> blocks and class names, so every rule is an inline style
// on plain <table> markup. Every stored value (worker, company, submitter) is typed by a submitter,
// so it goes through html.EscapeString: a name such as "<script>" must arrive as text.

const (
	mailFont    = "font-family:Arial,Helvetica,sans-serif;font-size:14px;color:#1f2933;"
	mailTableSt = "border-collapse:collapse;width:100%;margin:8px 0 16px;"
	mailThSt    = "text-align:left;padding:6px 10px;background:#eef2f6;border:1px solid #d5dce4;font-weight:bold;"
	mailTdSt    = "padding:6px 10px;border:1px solid #d5dce4;vertical-align:top;"
)

// htmlTable renders headers and rows; values are escaped here, so callers pass them as stored.
func htmlTable(headers []string, rows [][]string) string {
	var b strings.Builder
	b.WriteString(`<table role="presentation" cellspacing="0" cellpadding="0" style="` + mailTableSt + `"><thead><tr>`)
	for _, h := range headers {
		b.WriteString(`<th align="left" style="` + mailThSt + `">` + html.EscapeString(h) + `</th>`)
	}
	b.WriteString(`</tr></thead><tbody>`)
	for _, row := range rows {
		b.WriteString(`<tr>`)
		for _, c := range row {
			b.WriteString(`<td style="` + mailTdSt + `">` + html.EscapeString(oneLine(c)) + `</td>`)
		}
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}

func htmlPara(text string) string {
	return `<p style="margin:0 0 8px;">` + html.EscapeString(text) + `</p>`
}

// htmlLink shows the address as the link text as well, so a client that blocks links still shows it.
func htmlLink(label, url string) string {
	u := html.EscapeString(url)
	return `<p style="margin:0 0 8px;">` + html.EscapeString(label) + ` <a href="` + u + `">` + u + `</a></p>`
}

// htmlSignature is the closing of a mail, the same words as noticeSignature (owner, 2026-11-10).
func htmlSignature() string {
	return `<p style="margin:16px 0 0;">Kind Regards,<br>LNG Medical Team</p>`
}

func htmlPage(parts ...string) string {
	return `<!DOCTYPE html><html><body style="` + mailFont + `">` + strings.Join(parts, "") + `</body></html>`
}
