package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif" // GIF decoder (the first frame is used)
	"image/jpeg"
	_ "image/png" // PNG decoder
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"

	_ "golang.org/x/image/webp" // WebP decoder: Android and Chrome often produce WebP
)

// Photo rules (same for the "before" photos on a new post and the "after" photos that document
// a closed action):
//   - at most 4 photos per post (per kind),
//   - each stored photo is at most 500 KB.
const (
	maxPhotosPerPost = 4
	photoMaxBytes    = 500 * 1024

	// photoMaxUploadBytes is what the server will accept before it compresses. The browser
	// already compresses to 500 KB, so this is only a safety margin for a browser that could not.
	photoMaxUploadBytes = 8 << 20

	// photoMaxPixels stops "decompression bombs": a tiny file that claims a huge picture would
	// need gigabytes of RAM to decode, and the Orange Pi has 2 GB in total.
	photoMaxPixels = 40_000_000

	photoMaxSide     = 2000 // longest side after the first downscale
	photoMaxAttempts = 8    // how many times to shrink and re-encode before giving up
)

// photoSlots limits how many photos are decoded at once. One 12-megapixel photo needs about
// 50 MB of RAM to decode, so two at a time keeps the board safe (plan section 8.1a).
var photoSlots = make(chan struct{}, 2)

// acquirePhotoSlot waits for a free slot, or gives up when ctx ends. The returned function
// releases the slot.
func acquirePhotoSlot(ctx context.Context) (func(), error) {
	select {
	case photoSlots <- struct{}{}:
		return func() { <-photoSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// checkPhotoCount enforces the 4-photo limit for one post and one kind.
func checkPhotoCount(have, adding int) error {
	if have+adding > maxPhotosPerPost {
		return fmt.Errorf("a post can have at most %d photos", maxPhotosPerPost)
	}
	return nil
}

// processPhoto turns an uploaded picture into a JPEG of at most photoMaxBytes. Re-encoding also
// drops all metadata (phones put GPS coordinates in EXIF), and it means every stored file is a
// picture this program produced itself, not a file chosen by the visitor.
func processPhoto(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, photoMaxUploadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("could not read the photo: %w", err)
	}
	if len(data) > photoMaxUploadBytes {
		return nil, errors.New("the photo is too big")
	}
	if len(data) == 0 {
		return nil, errors.New("the photo is empty")
	}
	// Look at the bytes, not at the file name or the type the browser claimed.
	switch http.DetectContentType(data) {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
	default:
		return nil, errors.New("use a JPEG, PNG, WebP or GIF photo")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("the file is not a valid picture")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > photoMaxPixels {
		return nil, errors.New("the photo is too large in pixels")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("the file is not a valid picture")
	}

	rgba := flatten(img)
	if long := max(rgba.Bounds().Dx(), rgba.Bounds().Dy()); long > photoMaxSide {
		rgba = resizeBox(rgba, rgba.Bounds().Dx()*photoMaxSide/long, rgba.Bounds().Dy()*photoMaxSide/long)
	}
	for attempt := 0; attempt < photoMaxAttempts; attempt++ {
		for _, q := range []int{85, 75, 65, 55, 45} {
			var buf bytes.Buffer
			if err := jpeg.Encode(&buf, rgba, &jpeg.Options{Quality: q}); err != nil {
				return nil, fmt.Errorf("could not compress the photo: %w", err)
			}
			if buf.Len() <= photoMaxBytes {
				return buf.Bytes(), nil
			}
		}
		// Even the lowest quality was too big: shrink the picture by a quarter and try again.
		rgba = resizeBox(rgba, rgba.Bounds().Dx()*3/4, rgba.Bounds().Dy()*3/4)
	}
	return nil, errors.New("the photo could not be compressed enough")
}

// flatten draws the picture on a white background, so transparent PNG areas do not turn black
// when saved as JPEG (which has no transparency).
func flatten(src image.Image) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Over)
	return dst
}

// resizeBox shrinks an image by averaging the source pixels under each output pixel (a box
// filter). It is simple and gives clean results for shrinking, which is all this needs.
func resizeBox(src *image.RGBA, w, h int) *image.RGBA {
	w, h = max(w, 1), max(h, 1)
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		y0, y1 := y*sh/h, max((y+1)*sh/h, y*sh/h+1)
		for x := 0; x < w; x++ {
			x0, x1 := x*sw/w, max((x+1)*sw/w, x*sw/w+1)
			var r, g, b, n int
			for sy := y0; sy < y1; sy++ {
				row := src.Pix[sy*src.Stride:]
				for sx := x0; sx < x1; sx++ {
					p := row[sx*4 : sx*4+4]
					r, g, b, n = r+int(p[0]), g+int(p[1]), b+int(p[2]), n+1
				}
			}
			o := dst.Pix[y*dst.Stride+x*4:]
			o[0], o[1], o[2], o[3] = uint8(r/n), uint8(g/n), uint8(b/n), 255
		}
	}
	return dst
}

// savePhotoFile writes a processed photo into dir under a random name and returns that name.
func savePhotoFile(dir string, data []byte) (string, error) { return saveFile(dir, data, ".jpg") }

// saveFile writes data into dir under a random name with the given extension and returns the name.
// The name never comes from the visitor, so it cannot point outside the folder.
func saveFile(dir string, data []byte, ext string) (string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	name := hex.EncodeToString(raw) + ext
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(filepath.Join(dir, name))
		return "", err
	}
	return name, f.Close()
}

// removePhotoFiles deletes saved files (photos and PDFs), used to clean up when a post could not
// be saved or when files are removed. Names are paths relative to dir; a name that would point
// outside dir is ignored. Folders left empty are removed too.
func removePhotoFiles(dir string, names []string) {
	for _, n := range names {
		if p, ok := evidencePath(dir, n); ok {
			_ = os.Remove(p)
			pruneEmptyDirs(dir, p)
		}
	}
}

// storedPhoto is a photo that has been compressed and written to the evidence folder.
type storedPhoto struct {
	Name string
	Size int
}

// storeUploads compresses and saves the uploaded files for one post. have is how many photos of
// this kind the post already has. Either every file is saved, or none is: on any failure the files
// already written are removed again.
func storeUploads(ctx context.Context, dir string, files []*multipart.FileHeader, have int) ([]storedPhoto, error) {
	if err := checkPhotoCount(have, len(files)); err != nil {
		return nil, err
	}
	var saved []storedPhoto
	fail := func(err error) ([]storedPhoto, error) {
		names := make([]string, len(saved))
		for i, p := range saved {
			names[i] = p.Name
		}
		removePhotoFiles(dir, names)
		return nil, err
	}
	for _, fh := range files {
		release, err := acquirePhotoSlot(ctx)
		if err != nil {
			return fail(err)
		}
		f, err := fh.Open()
		if err != nil {
			release()
			return fail(errors.New("could not read the photo"))
		}
		data, err := processPhoto(f)
		f.Close()
		release()
		if err != nil {
			return fail(err)
		}
		name, err := savePhotoFile(dir, data)
		if err != nil {
			return fail(fmt.Errorf("could not save the photo: %w", err))
		}
		saved = append(saved, storedPhoto{Name: name, Size: len(data)})
	}
	return saved, nil
}
