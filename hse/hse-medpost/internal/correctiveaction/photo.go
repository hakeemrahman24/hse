package correctiveaction

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png" // registers the PNG decoder for image.Decode; PNG uploads are converted to JPEG
	"io"
	"net/http"
)

const (
	// MaxPhotoBytes is the owner's limit: every stored photo is under 600 KB.
	MaxPhotoBytes = 600 * 1024
	// maxUploadBytes bounds how much of one upload is read at all, so a huge file cannot fill memory on
	// the Pi. A phone photo is a few MB; the browser shrinks it before sending, so this is a backstop.
	maxUploadBytes = 15 << 20
	// maxPhotoSide is the longest edge a stored photo is shrunk to when it has to be re-encoded.
	maxPhotoSide = 1600
	// minPhotoSide is how small the retry loop will go before giving up on the 600 KB limit.
	minPhotoSide = 400
	// maxPixels rejects a "decompression bomb": a tiny file that expands to a giant bitmap.
	maxPixels = 40_000_000
)

// PreparePhoto is the server's side of the 600 KB rule. The browser already resizes and compresses before
// uploading, but a limit that only the browser enforces can be bypassed, so the server checks again.
//
// It accepts JPEG and PNG only (sniffed from the bytes, not the file name). A JPEG already under the limit
// is stored untouched, which keeps its orientation data. Anything else is decoded, flattened onto white,
// shrunk to at most 1600 px on the long edge and re-encoded as JPEG at falling quality until it fits. The
// re-encode drops EXIF data, so a phone photo that relied on the orientation tag can come out sideways;
// that only happens when the browser did not do its own resize, which is why it is a fallback.
func PreparePhoto(src io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(src, maxUploadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read photo: %w", err)
	}
	if len(data) == 0 {
		return nil, ValidationError("the photo file is empty")
	}
	if len(data) > maxUploadBytes {
		return nil, ValidationError("a photo can be at most 15 MB before it is reduced")
	}
	head := data
	if len(head) > 512 {
		head = head[:512]
	}
	contentType := http.DetectContentType(head)
	if contentType != "image/jpeg" && contentType != "image/png" {
		return nil, ValidationError("a photo must be a JPEG or PNG image")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, ValidationError("the photo could not be read")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, ValidationError("the photo dimensions are too large")
	}
	if contentType == "image/jpeg" && len(data) <= MaxPhotoBytes {
		return data, nil
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ValidationError("the photo could not be read")
	}
	flat := flatten(img)
	for side := maxPhotoSide; side >= minPhotoSide; side = side * 3 / 4 {
		scaled := scaleRGBA(flat, side)
		for _, quality := range []int{85, 75, 65, 55, 45} {
			var buf bytes.Buffer
			if err := jpeg.Encode(&buf, scaled, &jpeg.Options{Quality: quality}); err != nil {
				return nil, fmt.Errorf("encode photo: %w", err)
			}
			if buf.Len() <= MaxPhotoBytes {
				return buf.Bytes(), nil
			}
		}
	}
	return nil, ValidationError("the photo could not be reduced below 600 KB")
}

// ThumbnailSide is the longer edge, in pixels, of the pictures in the findings table.
const ThumbnailSide = 240

// thumbnailQuality is low on purpose: at this size a thumbnail is a few KB and only has to be recognisable.
const thumbnailQuality = 70

// Thumbnail returns a small JPEG copy of a stored photo, its longer edge at most side pixels. The Excel
// export uses the same function so the sheet and the page show the same picture.
func Thumbnail(data []byte, side int) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode photo for thumbnail: %w", err)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, ScaleToFit(img, side), &jpeg.Options{Quality: thumbnailQuality}); err != nil {
		return nil, fmt.Errorf("encode thumbnail: %w", err)
	}
	return buf.Bytes(), nil
}

// ScaleToFit shrinks img so its longer edge is at most maxSide, keeping the aspect ratio, and flattens
// any transparency onto white (JPEG has no alpha). An image already small enough is only flattened.
// The Excel export reuses it to make thumbnails.
func ScaleToFit(img image.Image, maxSide int) *image.RGBA {
	return scaleRGBA(flatten(img), maxSide)
}

// flatten draws img onto a white RGBA canvas starting at (0,0). Working on *image.RGBA also lets the
// scaler read the pixel slice directly instead of calling At() once per pixel, which is slow for 12 MP.
func flatten(img image.Image) *image.RGBA {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Over)
	return dst
}

// scaleRGBA is a box filter: each output pixel is the average of the block of source pixels it covers.
// That is plenty for photos going down to 1600 px or to a spreadsheet thumbnail, and needs no dependency.
func scaleRGBA(src *image.RGBA, maxSide int) *image.RGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	if maxSide <= 0 || (w <= maxSide && h <= maxSide) {
		return src
	}
	dw, dh := maxSide, max(1, h*maxSide/w)
	if h > w {
		dw, dh = max(1, w*maxSide/h), maxSide
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		y0, y1 := y*h/dh, (y+1)*h/dh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < dw; x++ {
			x0, x1 := x*w/dw, (x+1)*w/dw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, b, n uint32
			for sy := y0; sy < y1; sy++ {
				row := src.Pix[sy*src.Stride:]
				for sx := x0; sx < x1; sx++ {
					p := row[sx*4 : sx*4+3]
					r += uint32(p[0])
					g += uint32(p[1])
					b += uint32(p[2])
					n++
				}
			}
			o := dst.PixOffset(x, y)
			dst.Pix[o], dst.Pix[o+1], dst.Pix[o+2], dst.Pix[o+3] = uint8(r/n), uint8(g/n), uint8(b/n), 255
		}
	}
	return dst
}
