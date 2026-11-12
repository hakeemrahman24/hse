package card

import (
	"fmt"
	"strings"

	"rsc.io/qr"
)

// quietZone is the blank border (in modules) the QR standard asks for; without
// it some phone cameras cannot find the code.
const quietZone = 4

// QRSVG draws text as a QR code in an inline SVG.
//
// WHY inline SVG made here: the page's CSP allows no external image and no
// script, and the old system's outside QR service is gone. One <path> of
// horizontal runs keeps the markup small (a few KB) and prints sharply at any size.
func QRSVG(text string) (string, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", fmt.Errorf("encode QR: %w", err)
	}
	var b strings.Builder
	side := code.Size + 2*quietZone
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges" role="img" aria-label="QR code">`, side, side)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, side, side)
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; {
			if !code.Black(x, y) {
				x++
				continue
			}
			run := 1
			for x+run < code.Size && code.Black(x+run, y) {
				run++
			}
			fmt.Fprintf(&b, "M%d %dh%dv1h-%dz", x+quietZone, y+quietZone, run, run)
			x += run
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String(), nil
}
