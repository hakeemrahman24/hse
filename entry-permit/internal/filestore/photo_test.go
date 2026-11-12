package filestore

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	imagepng "image/png"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// noisy is a photo-like picture: random pixels do not compress, so it is a hard case for size.
func noisy(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewSource(1))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = uint8(r.Intn(256)), uint8(r.Intn(256)), uint8(r.Intn(256)), 255
	}
	return img
}

func jpegOf(t *testing.T, img image.Image, q int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: q}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func decode(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("result is not a JPEG: %v", err)
	}
	return img
}

func TestShrinkMakesALargePhotoSmallAndKeepsItsShape(t *testing.T) {
	in := jpegOf(t, noisy(3000, 2000), 95)
	out, err := Shrink(in)
	if err != nil {
		t.Fatal(err)
	}
	b := decode(t, out).Bounds()
	if b.Dx() > PhotoMaxEdge || b.Dy() > PhotoMaxEdge {
		t.Fatalf("%dx%d is larger than %d", b.Dx(), b.Dy(), PhotoMaxEdge)
	}
	// Pure noise may need a smaller size to fit, so only the 3:2 shape is checked here.
	if d := b.Dx()*2 - b.Dy()*3; d < -6 || d > 6 {
		t.Fatalf("3:2 shape lost: %dx%d", b.Dx(), b.Dy())
	}
	if len(out) > PhotoTargetBytes {
		t.Fatalf("%d bytes, want at most %d even for a noisy picture", len(out), PhotoTargetBytes)
	}
	if len(out)*10 > len(in) {
		t.Fatalf("not small enough: %d from %d", len(out), len(in))
	}
}

func TestShrinkNeverEnlargesASmallPhoto(t *testing.T) {
	out, err := Shrink(jpegOf(t, noisy(200, 100), 90))
	if err != nil {
		t.Fatal(err)
	}
	if b := decode(t, out).Bounds(); b.Dx() != 200 || b.Dy() != 100 {
		t.Fatalf("a small photo must keep its size, got %dx%d", b.Dx(), b.Dy())
	}
}

func TestShrinkReadsAPNGAndFlattensTransparencyOnWhite(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 40, 40)) // fully transparent
	var b bytes.Buffer
	if err := imagepng.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	out, err := Shrink(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	r, g, bl, _ := decode(t, out).At(20, 20).RGBA()
	if r>>8 < 240 || g>>8 < 240 || bl>>8 < 240 {
		t.Fatalf("transparent pixels must become white, got %d %d %d", r>>8, g>>8, bl>>8)
	}
}

func TestShrinkRefusesWhatIsNotAPhoto(t *testing.T) {
	for name, data := range map[string][]byte{"pdf": []byte("%PDF-1.4\nxx"), "text": []byte("hello"), "empty": nil} {
		if _, err := Shrink(data); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestShrinkRefusesAHugeImageBeforeDecodingIt(t *testing.T) {
	// A tiny PNG whose header claims 60000 x 60000 would need 14 GB to decode.
	img := image.NewGray(image.Rect(0, 0, 4, 4))
	var b bytes.Buffer
	_ = imagepng.Encode(&b, img)
	data := b.Bytes()
	data[16], data[17], data[18], data[19] = 0, 0, 0xEA, 0x60 // width 60000
	data[20], data[21], data[22], data[23] = 0, 0, 0xEA, 0x60 // height 60000
	if _, err := Shrink(data); err == nil {
		t.Fatal("an image claiming 60000x60000 pixels must be refused")
	}
}

// withOrientation inserts an EXIF block that carries the orientation tag (6 = turn 90 degrees to view).
func withOrientation(jpegData []byte, orientation byte) []byte {
	exif := []byte{
		0xFF, 0xE1, 0x00, 0x22, 'E', 'x', 'i', 'f', 0, 0,
		'M', 'M', 0, 0x2A, 0, 0, 0, 8, // big-endian header, first IFD at 8
		0, 1, // one entry
		0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, orientation, 0, 0, // tag 0x0112, SHORT, count 1
		0, 0, 0, 0,
	}
	return append(append(append([]byte{}, jpegData[:2]...), exif...), jpegData[2:]...)
}

func TestShrinkTurnsAPhoneShotUprightFromItsExifOrientation(t *testing.T) {
	// 300 wide, 100 tall, stored sideways: orientation 6 means the picture is really 100 wide, 300 tall.
	in := withOrientation(jpegOf(t, noisy(300, 100), 90), 6)
	out, err := Shrink(in)
	if err != nil {
		t.Fatal(err)
	}
	if b := decode(t, out).Bounds(); b.Dx() != 100 || b.Dy() != 300 {
		t.Fatalf("expected the photo turned upright to 100x300, got %dx%d", b.Dx(), b.Dy())
	}
}

func TestSavePhotoWritesAJpegAndRemovesTheOldFileOfAnotherType(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	old, err := s.Save("WEP-AB12C", "id_photo", pngBytes(t), 0)
	if err != nil || old != "WEP-AB12C/id_photo.png" {
		t.Fatalf("setup: %q %v", old, err)
	}
	rel, err := s.SavePhoto("WEP-AB12C", "id_photo", jpegOf(t, noisy(800, 800), 90), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if rel != "WEP-AB12C/id_photo.jpg" {
		t.Fatalf("rel = %q", rel)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, old)); !os.IsNotExist(err) {
		t.Fatal("the old file was left in storage")
	}
	if fi, err := os.Stat(filepath.Join(s.Dir, rel)); err != nil || fi.Size() > PhotoTargetBytes {
		t.Fatalf("stored file: %v", err)
	}
}

func TestSavePhotoOverwritesTheSameNameAndKeepsOtherDocuments(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if _, err := s.Save("WEP-AB12C", "id_doc", pdf, 0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := s.SavePhoto("WEP-AB12C", "id_photo", jpegOf(t, noisy(500, 500), 90), 1<<20); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(s.Dir, "WEP-AB12C"))
	if len(entries) != 2 {
		t.Fatalf("want id_doc.pdf and id_photo.jpg only, got %d files", len(entries))
	}
}

func TestSavePhotoRefusesBadInputAndLeavesTheOldPhotoAlone(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	keep, _ := s.SavePhoto("WEP-AB12C", "id_photo", jpegOf(t, noisy(300, 300), 90), 1<<20)
	before, _ := os.ReadFile(filepath.Join(s.Dir, keep))
	cases := map[string]struct {
		id, kind string
		data     []byte
		max      int
		want     error
	}{
		"pdf":      {"WEP-AB12C", "id_photo", pdf, 1 << 20, ErrType},
		"empty":    {"WEP-AB12C", "id_photo", nil, 1 << 20, ErrEmpty},
		"too big":  {"WEP-AB12C", "id_photo", jpegOf(t, noisy(900, 900), 95), 1000, ErrTooBig},
		"bad id":   {"../x", "id_photo", jpg, 1 << 20, errBadName},
		"bad kind": {"WEP-AB12C", "../x", jpg, 1 << 20, errBadName},
	}
	for name, c := range cases {
		if _, err := s.SavePhoto(c.id, c.kind, c.data, c.max); err != c.want {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
	}
	if after, _ := os.ReadFile(filepath.Join(s.Dir, keep)); !bytes.Equal(before, after) {
		t.Fatal("a refused upload changed the stored photo")
	}
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(1, 1, color.White)
	var b bytes.Buffer
	if err := imagepng.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
