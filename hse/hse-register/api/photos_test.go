package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// noisyImage is the worst case for compression: random pixels do not shrink, so a big one forces
// the resize loop to work.
func noisyImage(w, h int) image.Image {
	rng := rand.New(rand.NewSource(1))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), 255
	}
	return img
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func encodeJPEG(t *testing.T, img image.Image, q int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: q}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestProcessPhotoShrinksBigPhotoUnderLimit(t *testing.T) {
	in := encodePNG(t, noisyImage(1500, 1000)) // about 4 MB of noise: far over 500 KB, under the upload limit
	if len(in) < photoMaxBytes*4 {
		t.Fatalf("test input should be much bigger than the limit, got %d", len(in))
	}
	out, err := processPhoto(bytes.NewReader(in))
	if err != nil {
		t.Fatalf("processPhoto: %v", err)
	}
	if len(out) > photoMaxBytes {
		t.Fatalf("output is %d bytes, limit %d", len(out), photoMaxBytes)
	}
	if _, err := jpeg.Decode(bytes.NewReader(out)); err != nil {
		t.Fatalf("output must be a valid JPEG: %v", err)
	}
}

func TestProcessPhotoKeepsSmallPhotoUsable(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 640, 480))
	for y := 0; y < 480; y++ {
		for x := 0; x < 640; x++ {
			img.Set(x, y, color.RGBA{uint8(x / 3), uint8(y / 2), 120, 255})
		}
	}
	out, err := processPhoto(bytes.NewReader(encodeJPEG(t, img, 90)))
	if err != nil || len(out) > photoMaxBytes {
		t.Fatalf("small photo: %d bytes, %v", len(out), err)
	}
	cfg, _, _ := image.DecodeConfig(bytes.NewReader(out))
	if cfg.Width != 640 || cfg.Height != 480 {
		t.Fatalf("a small photo must not be resized, got %dx%d", cfg.Width, cfg.Height)
	}
}

func TestProcessPhotoStripsEXIF(t *testing.T) {
	plain := encodeJPEG(t, noisyImage(64, 64), 80)
	// Insert an APP1 (EXIF) segment holding fake GPS text right after the JPEG start marker.
	payload := append([]byte("Exif\x00\x00"), []byte("GPSLatitude=-3.14 GPSLongitude=102.5")...)
	seg := []byte{0xFF, 0xE1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}
	seg = append(seg, payload...)
	withEXIF := append(append([]byte{0xFF, 0xD8}, seg...), plain[2:]...)
	if !bytes.Contains(withEXIF, []byte("GPSLatitude")) {
		t.Fatal("test setup: EXIF not inserted")
	}
	out, err := processPhoto(bytes.NewReader(withEXIF))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("GPS")) || bytes.Contains(out, []byte("Exif")) {
		t.Fatal("location data must be removed from stored photos")
	}
}

func TestProcessPhotoRejectsNonImages(t *testing.T) {
	var g bytes.Buffer
	_ = gif.Encode(&g, image.NewPaletted(image.Rect(0, 0, 4, 4), []color.Color{color.Black, color.White}), nil)
	_ = g
	cases := map[string][]byte{
		"text":      []byte("this is not a picture"),
		"html":      []byte("<html><script>alert(1)</script></html>"),
		"empty":     {},
		"truncated": encodeJPEG(t, noisyImage(100, 100), 80)[:200],
	}
	for name, data := range cases {
		if _, err := processPhoto(bytes.NewReader(data)); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
}

// A 1x1 lossless WebP (the format Android and many browsers produce). The Go standard library
// cannot encode WebP, so the test carries a tiny real file.
const tinyWebP = "UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA=="

func TestProcessPhotoAcceptsCommonFormatsAndStoresJPEG(t *testing.T) {
	var g bytes.Buffer
	pal := image.NewPaletted(image.Rect(0, 0, 8, 8), []color.Color{color.Black, color.White})
	_ = gif.Encode(&g, pal, nil)
	webp, err := base64.StdEncoding.DecodeString(tinyWebP)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"jpeg": encodeJPEG(t, noisyImage(50, 50), 80),
		"png":  encodePNG(t, noisyImage(50, 50)),
		"gif":  g.Bytes(),
		"webp": webp,
	}
	for name, data := range cases {
		out, err := processPhoto(bytes.NewReader(data))
		if err != nil {
			t.Errorf("%s must be accepted: %v", name, err)
			continue
		}
		// Whatever comes in, what is stored is a JPEG, which every phone and browser can show.
		if !bytes.HasPrefix(out, []byte{0xFF, 0xD8}) {
			t.Errorf("%s: output is not a JPEG", name)
		}
	}
}

func TestProcessPhotoRejectsHugeDimensions(t *testing.T) {
	// A PNG header that claims 30000x30000 pixels would need ~3.6 GB to decode.
	var b bytes.Buffer
	b.Write([]byte("\x89PNG\r\n\x1a\n"))
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], 30000)
	binary.BigEndian.PutUint32(ihdr[4:], 30000)
	ihdr[8], ihdr[9] = 8, 6 // bit depth 8, RGBA
	chunk := append([]byte("IHDR"), ihdr...)
	_ = binary.Write(&b, binary.BigEndian, uint32(13))
	b.Write(chunk)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	if _, err := processPhoto(bytes.NewReader(b.Bytes())); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("huge dimensions must be refused before decoding, got %v", err)
	}
}

func TestProcessPhotoRejectsOversizedUpload(t *testing.T) {
	big := bytes.Repeat([]byte{0xFF}, photoMaxUploadBytes+10)
	if _, err := processPhoto(bytes.NewReader(big)); err == nil {
		t.Fatal("an upload over the byte limit must be refused")
	}
}

func TestCheckPhotoCount(t *testing.T) {
	cases := []struct {
		have, adding int
		ok           bool
	}{{0, 4, true}, {0, 5, false}, {3, 1, true}, {3, 2, false}, {4, 0, true}, {4, 1, false}}
	for _, c := range cases {
		if err := checkPhotoCount(c.have, c.adding); (err == nil) != c.ok {
			t.Errorf("have %d adding %d: ok=%v, got %v", c.have, c.adding, c.ok, err)
		}
	}
}

func TestSaveAndRemovePhotoFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "evidence")
	name, err := savePhotoFile(dir, []byte("jpegbytes"))
	if err != nil || !strings.HasSuffix(name, ".jpg") || strings.ContainsAny(name, "/\\") {
		t.Fatalf("bad name %q (%v)", name, err)
	}
	got, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil || string(got) != "jpegbytes" {
		t.Fatalf("file not written: %v", err)
	}
	other, _ := savePhotoFile(dir, []byte("x"))
	if other == name {
		t.Fatal("names must be random and unique")
	}
	removePhotoFiles(dir, []string{name, other})
	if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
		t.Fatal("file should be removed")
	}
}

func TestPhotoSlotsLimitConcurrentWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r1, _ := acquirePhotoSlot(ctx)
	r2, _ := acquirePhotoSlot(ctx)
	cancel() // the third caller must give up instead of waiting forever
	if _, err := acquirePhotoSlot(ctx); err == nil {
		t.Fatal("a third concurrent photo job must have to wait")
	}
	r1()
	r2()
}
