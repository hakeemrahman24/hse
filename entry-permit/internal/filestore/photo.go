package filestore

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	imagepng "image/png"
	"os"
	"path/filepath"
)

// Photo limits. WHY these numbers: an ID photo only has to show a face clearly to a reviewer or
// the gate officer, so 640 pixels on the long side is plenty, and a JPEG of that size is
// normally 20-70 KB. The ceiling of 300 KB is the owner's limit. It is a ceiling, not a goal: the encoder stops at the first
// quality that fits under it, so a simple picture ends up even smaller.
const (
	PhotoMaxEdge     = 640
	PhotoTargetBytes = 300_000
	// maxPixels refuses a picture whose header claims more than this before it is decoded, so a
	// few hundred bytes cannot make a 2 GB machine allocate gigabytes.
	maxPixels = 40_000_000
)

var (
	photoEdges     = []int{PhotoMaxEdge, 480, 360, 240}
	photoQualities = []int{72, 60, 48, 38}
)

// ErrImage: the file looked like a picture but could not be read as one.
var ErrImage = errors.New("the picture could not be read, or is too large")

// Shrink turns a JPG or PNG into a small upright JPEG.
//
// It applies the camera's rotation (phones store a portrait shot sideways plus a note, and the
// note is lost when the file is re-encoded), puts transparent areas on white (JPEG has no
// transparency), scales down so the long side is at most PhotoMaxEdge (never up), and then picks
// the lowest quality loss that fits PhotoTargetBytes, shrinking further only if it must.
func Shrink(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, ErrEmpty
	}
	ext := detect(data)
	if ext != ".jpg" && ext != ".png" {
		return nil, ErrType
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxPixels {
		return nil, ErrImage
	}
	var src image.Image
	if ext == ".png" {
		src, err = imagepng.Decode(bytes.NewReader(data))
	} else {
		src, err = jpeg.Decode(bytes.NewReader(data))
	}
	if err != nil {
		return nil, ErrImage
	}
	o := 1
	if ext == ".jpg" {
		o = exifOrientation(data)
	}
	var best []byte
	cur := render(src, o, photoEdges[0])
	for i, edge := range photoEdges {
		if i > 0 {
			cur = render(cur, 1, edge)
		}
		for _, q := range photoQualities {
			var b bytes.Buffer
			if err := jpeg.Encode(&b, cur, &jpeg.Options{Quality: q}); err != nil {
				return nil, fmt.Errorf("encode photo: %w", err)
			}
			best = b.Bytes()
			if len(best) <= PhotoTargetBytes {
				return best, nil
			}
		}
	}
	return best, nil // smallest attempt; only a picture of pure noise gets here
}

// render draws src upright (EXIF orientation o) on white, scaled down by area averaging so the
// long side is at most maxEdge. Area averaging keeps text and faces crisper than picking pixels.
func render(src image.Image, o, maxEdge int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	ow, oh := w, h
	if o >= 5 {
		ow, oh = h, w
	}
	scale := 1.0
	if long := max(ow, oh); long > maxEdge {
		scale = float64(maxEdge) / float64(long)
	}
	dw, dh := max(1, int(float64(ow)*scale+0.5)), max(1, int(float64(oh)*scale+0.5))
	out := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		v0 := int(float64(y) / scale)
		v1 := min(oh, max(v0+1, int(float64(y+1)/scale)))
		for x := 0; x < dw; x++ {
			u0 := int(float64(x) / scale)
			u1 := min(ow, max(u0+1, int(float64(x+1)/scale)))
			var r, g, bl, n uint32
			for v := v0; v < v1; v++ {
				for u := u0; u < u1; u++ {
					sx, sy := sourcePoint(o, u, v, w, h)
					pr, pg, pb, pa := src.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
					white := 0xffff - pa // premultiplied colour over white
					r, g, bl, n = r+(pr+white)>>8, g+(pg+white)>>8, bl+(pb+white)>>8, n+1
				}
			}
			out.SetRGBA(x, y, color.RGBA{R: uint8(r / n), G: uint8(g / n), B: uint8(bl / n), A: 255})
		}
	}
	return out
}

// sourcePoint maps a point of the upright picture back to the stored picture (EXIF orientations 1-8).
func sourcePoint(o, u, v, w, h int) (int, int) {
	switch o {
	case 2:
		return w - 1 - u, v
	case 3:
		return w - 1 - u, h - 1 - v
	case 4:
		return u, h - 1 - v
	case 5:
		return v, u
	case 6:
		return v, h - 1 - u
	case 7:
		return w - 1 - v, h - 1 - u
	case 8:
		return w - 1 - v, u
	}
	return u, v
}

// exifOrientation reads the orientation note a camera leaves in a JPEG (1 when there is none or
// it cannot be read). Only the first EXIF block is looked at and every offset is bounds-checked,
// because the bytes come from a stranger.
func exifOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xFF {
			return 1
		}
		marker, size := data[i+1], int(binary.BigEndian.Uint16(data[i+2:]))
		if marker == 0xDA || size < 2 { // start of image data: no EXIF after this
			return 1
		}
		end := i + 2 + size
		if end > len(data) {
			return 1
		}
		if marker == 0xE1 && size >= 8 && string(data[i+4:i+8]) == "Exif" {
			return tiffOrientation(data[i+10 : end])
		}
		i = end
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "MM":
		bo = binary.BigEndian
	case "II":
		bo = binary.LittleEndian
	default:
		return 1
	}
	ifd := int(bo.Uint32(t[4:]))
	if ifd < 8 || ifd+2 > len(t) {
		return 1
	}
	n := int(bo.Uint16(t[ifd:]))
	for k := 0; k < n; k++ {
		e := ifd + 2 + k*12
		if e+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[e:]) == 0x0112 {
			if v := int(bo.Uint16(t[e+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

// SavePhoto shrinks a photo and stores it as <request id>/<kind>.jpg, replacing the earlier one.
// maxBytes limits what is accepted, before shrinking. It returns the path relative to Dir.
func (s Store) SavePhoto(requestID, kind string, data []byte, maxBytes int) (string, error) {
	if !idPattern.MatchString(requestID) || !kindPattern.MatchString(kind) {
		return "", errBadName
	}
	if maxBytes > 0 && len(data) > maxBytes {
		return "", ErrTooBig
	}
	small, err := Shrink(data)
	if err != nil {
		return "", err
	}
	return s.PlacePhoto(requestID, kind, small)
}

// PlacePhoto stores an already shrunk JPEG as <request id>/<kind>.jpg and removes the same photo
// if it was stored earlier under another extension (a .png from an old upload), so the new file
// replaces the old one in storage and nothing is left behind. The new file is written beside the
// old one and renamed over it, so a failure leaves the old photo as it was.
func (s Store) PlacePhoto(requestID, kind string, small []byte) (string, error) {
	if !idPattern.MatchString(requestID) || !kindPattern.MatchString(kind) {
		return "", errBadName
	}
	if len(small) == 0 {
		return "", ErrEmpty
	}
	dir := filepath.Join(s.Dir, requestID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create upload folder: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".photo-*")
	if err != nil {
		return "", fmt.Errorf("write photo: %w", err)
	}
	defer os.Remove(tmp.Name()) // a no-op once renamed
	if _, err := tmp.Write(small); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write photo: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("write photo: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return "", fmt.Errorf("write photo: %w", err)
	}
	rel := requestID + "/" + kind + ".jpg"
	if err := os.Rename(tmp.Name(), filepath.Join(s.Dir, rel)); err != nil {
		return "", fmt.Errorf("replace photo: %w", err)
	}
	for _, ext := range []string{".png", ".pdf"} {
		if err := os.Remove(filepath.Join(dir, kind+ext)); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("remove old photo: %w", err)
		}
	}
	return rel, nil
}
