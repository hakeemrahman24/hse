package correctiveaction

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"net/http"
	"testing"
)

// noisyImage is incompressible on purpose: random pixels make a JPEG or PNG far bigger than 600 KB, which
// is what a real phone photo looks like to the encoder. A fixed seed keeps the test repeatable.
func noisyImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rand.New(rand.NewSource(1)).Read(img.Pix)
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	return img
}

func jpegBytes(t *testing.T, img image.Image, quality int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPreparePhotoShrinksALargeJPEGUnderTheLimit(t *testing.T) {
	big := jpegBytes(t, noisyImage(1700, 1100), 95)
	if len(big) <= MaxPhotoBytes {
		t.Fatalf("test setup: the source is only %d bytes, it must exceed the limit", len(big))
	}
	got, err := PreparePhoto(bytes.NewReader(big))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > MaxPhotoBytes {
		t.Fatalf("result is %d bytes, want at most %d", len(got), MaxPhotoBytes)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(got))
	if err != nil || format != "jpeg" {
		t.Fatalf("result is not a readable JPEG: %v (%s)", err, format)
	}
	if cfg.Width > maxPhotoSide || cfg.Height > maxPhotoSide {
		t.Fatalf("result is %dx%d, want the long edge at most %d", cfg.Width, cfg.Height, maxPhotoSide)
	}
}

func TestPreparePhotoKeepsASmallJPEGUntouched(t *testing.T) {
	small := jpegBytes(t, noisyImage(64, 48), 80)
	got, err := PreparePhoto(bytes.NewReader(small))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, small) {
		t.Fatal("a JPEG already under the limit must be stored byte for byte (it keeps its orientation data)")
	}
}

func TestPreparePhotoConvertsPNGToJPEG(t *testing.T) {
	for name, img := range map[string]image.Image{
		"small": noisyImage(40, 30),
		"large": noisyImage(900, 700),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := PreparePhoto(bytes.NewReader(pngBytes(t, img)))
			if err != nil {
				t.Fatal(err)
			}
			if ct := http.DetectContentType(got); ct != "image/jpeg" {
				t.Fatalf("content type = %s, want a JPEG", ct)
			}
			if len(got) > MaxPhotoBytes {
				t.Fatalf("%d bytes, over the limit", len(got))
			}
		})
	}
}

func TestPreparePhotoFlattensTransparencyOntoWhite(t *testing.T) {
	clear := image.NewNRGBA(image.Rect(0, 0, 50, 50)) // every pixel fully transparent
	got, err := PreparePhoto(bytes.NewReader(pngBytes(t, clear)))
	if err != nil {
		t.Fatal(err)
	}
	img, _, err := image.Decode(bytes.NewReader(got))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := img.At(25, 25).RGBA()
	if r>>8 < 240 || g>>8 < 240 || b>>8 < 240 {
		t.Fatalf("transparent pixel became (%d,%d,%d), want near white, not black", r>>8, g>>8, b>>8)
	}
}

func TestPreparePhotoRejectsBadUploads(t *testing.T) {
	gif := append([]byte("GIF89a"), make([]byte, 64)...)
	tooBig := append(jpegBytes(t, noisyImage(32, 32), 80), make([]byte, maxUploadBytes)...)
	cases := map[string][]byte{
		"empty":                {},
		"text file":            []byte("this is not a photo"),
		"pdf":                  []byte("%PDF-1.4\n1 0 obj<<>>endobj\n"),
		"gif":                  gif,
		"over the upload cap":  tooBig,
		"jpeg header, no body": {0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F'},
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := PreparePhoto(bytes.NewReader(data)); !isValidation(err) {
				t.Fatalf("error = %v, want a ValidationError", err)
			}
		})
	}
}

func TestScaleToFit(t *testing.T) {
	cases := []struct {
		name         string
		w, h, side   int
		wantW, wantH int
	}{
		{"landscape", 400, 300, 100, 100, 75},
		{"portrait", 300, 400, 100, 75, 100},
		{"square", 800, 800, 400, 400, 400},
		{"already small", 30, 20, 100, 30, 20},
		{"extreme panorama keeps one pixel", 500, 3, 100, 100, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := image.NewRGBA(image.Rect(0, 0, c.w, c.h))
			got := ScaleToFit(src, c.side)
			if got.Bounds().Dx() != c.wantW || got.Bounds().Dy() != c.wantH {
				t.Fatalf("size = %dx%d, want %dx%d", got.Bounds().Dx(), got.Bounds().Dy(), c.wantW, c.wantH)
			}
		})
	}
}

func TestScaleToFitAveragesPixels(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for x := 0; x < 4; x++ {
		for y := 0; y < 2; y++ {
			v := uint8(0)
			if x >= 2 {
				v = 200
			}
			src.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
	got := ScaleToFit(src, 2) // 4x2 -> 2x1: left block black, right block 200
	if l := got.RGBAAt(0, 0); l.R != 0 {
		t.Fatalf("left = %v, want 0", l)
	}
	if r := got.RGBAAt(1, 0); r.R != 200 {
		t.Fatalf("right = %v, want 200", r)
	}
}
