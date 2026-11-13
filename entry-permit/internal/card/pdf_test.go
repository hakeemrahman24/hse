package card

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func sampleCard() PDFCard {
	return PDFCard{
		LEP: "LEP-AB12C", Name: "Budi Santoso", Company: "PT Maju", Parent: "Starway Engineering", Position: "Welder",
		ValidUntil: "31 Jan 2027", State: Valid, Entity: "Contractor", Payload: "https://permit.example/?page=ticket&lep=LEP-AB12C",
		GeneratedAt: "2026-11-07 09:00:00",
	}
}

func render(t *testing.T, c PDFCard) string {
	t.Helper()
	compress = false // the page text is then readable in the bytes
	defer func() { compress = true }()
	var b bytes.Buffer
	if err := PDF(&b, c); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b.Bytes(), []byte("%PDF-")) || !bytes.Contains(b.Bytes(), []byte("%%EOF")) {
		t.Fatal("not a complete PDF")
	}
	return b.String()
}

func TestPDFShowsTheCardDetailsAndIsOnePage(t *testing.T) {
	out := render(t, sampleCard())
	for _, want := range []string{"LEP-AB12C", "Budi Santoso", "PT Maju", "Starway Engineering", "Welder", "31 Jan 2027", "Valid"} {
		if !strings.Contains(out, want) {
			t.Errorf("PDF lacks %q", want)
		}
	}
	if n := strings.Count(out, "/Type /Page\n"); n != 1 {
		t.Errorf("%d pages, want 1", n)
	}
}

func TestPDFStatesPlainlyWhenThePermitIsNotValid(t *testing.T) {
	for state, word := range map[State]string{Expired: "Expired", Replaced: "Replaced by a newer permit", Inactive: "Not valid"} {
		c := sampleCard()
		c.State = state
		if out := render(t, c); !strings.Contains(out, word) {
			t.Errorf("state %v: PDF lacks %q", state, word)
		}
	}
}

func TestPDFEmbedsAPhotoWhenThereIsOne(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 60, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 60; x++ {
			img.Set(x, y, color.RGBA{200, uint8(x * 4), uint8(y * 3), 255})
		}
	}
	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, img, nil); err != nil {
		t.Fatal(err)
	}
	c := sampleCard()
	c.Photo = jb.Bytes()
	// The company logo is always an image, so the photo shows as one more image than the card without it.
	without := strings.Count(render(t, sampleCard()), "/Subtype /Image")
	with := strings.Count(render(t, c), "/Subtype /Image")
	if without == 0 {
		t.Error("the company logo is not in the PDF")
	}
	if with <= without {
		t.Errorf("the photo is not in the PDF: %d images with it, %d without", with, without)
	}
}

func TestPDFIgnoresAPhotoThatIsNotAJpegInsteadOfFailing(t *testing.T) {
	c := sampleCard()
	c.Photo = []byte("not a picture")
	if out := render(t, c); !strings.Contains(out, "Budi Santoso") {
		t.Error("a bad photo must not stop the card")
	}
}

func TestPDFCutsVeryLongTextAndStillWorks(t *testing.T) {
	c := sampleCard()
	c.Name = strings.Repeat("Longname ", 40)
	c.Company = strings.Repeat("Company ", 40)
	out := render(t, c)
	if strings.Contains(out, strings.Repeat("Longname ", 20)) {
		t.Error("long text was not cut")
	}
}

func TestPDFReportsAnUnencodablePayload(t *testing.T) {
	c := sampleCard()
	c.Payload = strings.Repeat("x", 5000) // too long for any QR code
	var b bytes.Buffer
	if err := PDF(&b, c); err == nil {
		t.Fatal("expected an error")
	}
}

// Photos imported from the old Drive are whatever the worker uploaded, often PNG. They must still
// reach the card (owner report 2026-11-08: "the photo on my permit is not loading").
func TestPDFEmbedsAPngOrGifPhotoToo(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 60, 80))
	for y := 0; y < 80; y++ {
		for x := 0; x < 60; x++ {
			a := uint8(255)
			if x < 5 {
				a = 0 // a transparent edge: flattened to white, not left black
			}
			img.Set(x, y, color.NRGBA{200, uint8(x * 4), uint8(y * 3), a})
		}
	}
	var pb, gb bytes.Buffer
	if err := png.Encode(&pb, img); err != nil {
		t.Fatal(err)
	}
	if err := gif.Encode(&gb, img, nil); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"png": pb.Bytes(), "gif": gb.Bytes()} {
		c := sampleCard()
		c.Photo = data
		if out := render(t, c); !strings.Contains(out, "/Subtype /Image") {
			t.Errorf("a %s photo is not in the PDF", name)
		}
	}
}

func TestPDFWrapsALongCompanyNameOntoASecondLineAndCutsAnAbsurdOne(t *testing.T) {
	c := sampleCard()
	c.Company = "PT Karya Sejahtera Bersama Nusantara Engineering Services"
	out := render(t, c)
	if !strings.Contains(out, "Services") || strings.Contains(out, "...") {
		t.Error("a long company name should wrap onto a second line in full")
	}
	c.Company = strings.Repeat("Perusahaan Konstruksi Jaya Abadi ", 8)
	if out := render(t, c); !strings.Contains(out, "...") {
		t.Error("an absurdly long company name should end with ...")
	}
}
