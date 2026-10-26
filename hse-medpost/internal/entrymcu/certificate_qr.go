package entrymcu

import (
	"bytes"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/skip2/go-qrcode"

	"hse-medpost/internal/storage"
)

// publicCertOrigin is the site a phone opens when the certificate QR is scanned.
// A loopback BASE_URL cannot be reached from a phone, so those values use the
// public start page instead.
const publicCertOrigin = "https://medical-activities.ayahaif.my.id"

// certificateVerifyURL is the address encoded in the certificate QR code.
// The seal must match the checkup before the verification page shows anything.
func certificateVerifyURL(baseURL, checkUpID, seal string) string {
	base := certPublicOrigin(baseURL)
	return base + "/verification/" + url.PathEscape(strings.TrimSpace(checkUpID)) + "/" + url.PathEscape(seal)
}

func certPublicOrigin(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(base)
	if err != nil || parsed.Hostname() == "" {
		return publicCertOrigin
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "localhost", "127.0.0.1", "::1":
		return publicCertOrigin
	default:
		return base
	}
}

// certificateQRCode draws a QR code and places the site favicon in the middle.
// Highest error correction keeps the code readable with the logo covering the center.
func certificateQRCode(content, faviconPath string) ([]byte, error) {
	code, err := qrcode.New(content, qrcode.Highest)
	if err != nil {
		return nil, err
	}
	img := code.Image(640)
	if faviconPath != "" {
		if logo, loadErr := loadImage(faviconPath); loadErr == nil {
			img = overlayCenterLogo(img, logo)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func faviconImagePath(cfg CertConfig) string {
	if found, ok := storage.FindCommonFile(cfg.UploadDir, "favicon"); ok {
		return found
	}
	for _, candidate := range []string{
		filepath.Join("web", "static", "favicon.png"),
		filepath.Join("..", "..", "web", "static", "favicon.png"),
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func loadImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}

func overlayCenterLogo(qrImg, logo image.Image) image.Image {
	bounds := qrImg.Bounds()
	dst := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			dst.Set(x, y, qrImg.At(x, y))
		}
	}
	size := bounds.Dx()
	if size < 40 {
		return dst
	}
	logoSize := size * 20 / 100
	if logoSize < 16 {
		logoSize = 16
	}
	scaled := scaleNearest(logo, logoSize, logoSize)
	cx := bounds.Min.X + size/2
	cy := bounds.Min.Y + size/2
	fillDisc(dst, cx, cy, int(float64(logoSize)*0.50), color.White)
	drawOver(dst, scaled, cx-logoSize/2, cy-logoSize/2)
	return dst
}

func fillDisc(img *image.RGBA, cx, cy, radius int, c color.Color) {
	r2 := radius * radius
	bounds := img.Bounds()
	for y := cy - radius; y <= cy+radius; y++ {
		for x := cx - radius; x <= cx+radius; x++ {
			if x < bounds.Min.X || y < bounds.Min.Y || x >= bounds.Max.X || y >= bounds.Max.Y {
				continue
			}
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy <= r2 {
				img.Set(x, y, c)
			}
		}
	}
}

func drawOver(dst *image.RGBA, src image.Image, x0, y0 int) {
	sb := src.Bounds()
	for y := sb.Min.Y; y < sb.Max.Y; y++ {
		for x := sb.Min.X; x < sb.Max.X; x++ {
			_, _, _, a := src.At(x, y).RGBA()
			if a == 0 {
				continue
			}
			dst.Set(x0+x-sb.Min.X, y0+y-sb.Min.Y, src.At(x, y))
		}
	}
}

func scaleNearest(src image.Image, width, height int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw == 0 || sh == 0 || width == 0 || height == 0 {
		return dst
	}
	for y := 0; y < height; y++ {
		sy := sb.Min.Y + y*sh/height
		if sy >= sb.Max.Y {
			sy = sb.Max.Y - 1
		}
		for x := 0; x < width; x++ {
			sx := sb.Min.X + x*sw/width
			if sx >= sb.Max.X {
				sx = sb.Max.X - 1
			}
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}
