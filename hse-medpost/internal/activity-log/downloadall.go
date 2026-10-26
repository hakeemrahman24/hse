package activitylog

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"hse-medpost/internal/display"
)

const maxFilenameTitleLen = 100

var unsafeFilenameChars = regexp.MustCompile(`[^A-Za-z0-9 ()&,.-]+`)

// Per-batch caps: keeps a single zip generation within something the
// orangepi3b can stream in one request without exhausting RAM/CPU or
// tripping a reverse-proxy/tunnel timeout. Batches are packed file by file,
// so a large program folder is split across parts. A single file bigger
// than the byte cap still ships on its own as an oversized batch of one.
// The byte cap is measured on the zip itself (see zipEntryCost), not on the
// raw file sizes.
const (
	downloadAllBatchByteCap = 10 * 1024 * 1024
	downloadAllBatchFileCap = 3000

	// zipFixedOverhead is the end-of-archive record plus slack.
	zipFixedOverhead = 1024
	// zipEntryOverhead covers an entry's local header, data descriptor and
	// central directory record, not counting the file name (stored twice).
	zipEntryOverhead = 130
)

// zipEntryCost is the most a file can add to the zip. Uploads are mostly
// PDFs and photos that deflate no smaller, and deflate adds a little to
// incompressible data, so the file counts at full size plus 0.2%.
func zipEntryCost(f uploadFile) int64 {
	return f.size + f.size/512 + zipEntryOverhead + 2*int64(len(f.rel))
}

type uploadFile struct {
	rel  string // slash-separated path relative to UploadDir, used as the zip entry name
	size int64
}

type uploadBatch struct {
	Index    int      `json:"index"`
	Programs []string `json:"programs"`
	Files    int      `json:"files"`
	Bytes    int64    `json:"bytes"`
	entries  []uploadFile
	zipBytes int64 // worst-case zip size, compared with the byte cap
}

// listUploadFiles returns every file under UploadDir's top-level program
// folders in a stable order (folders by name, then files by path), which is
// what makes batch N re-derivable across the info and per-batch calls.
func listUploadFiles(uploadDir string) ([]uploadFile, error) {
	entries, err := os.ReadDir(uploadDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var files []uploadFile
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		err := filepath.WalkDir(filepath.Join(uploadDir, e.Name()), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(uploadDir, path)
			if err != nil {
				return err
			}
			files = append(files, uploadFile{rel: filepath.ToSlash(rel), size: info.Size()})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

// planDownloadAllBatches greedily packs the uploads tree's files into
// batches under the caps above. Program names are taken from each file's
// top-level folder.
func planDownloadAllBatches(uploadDir string) ([]uploadBatch, error) {
	files, err := listUploadFiles(uploadDir)
	if err != nil {
		return nil, err
	}

	var batches []uploadBatch
	var cur uploadBatch
	for _, f := range files {
		cost := zipEntryCost(f)
		wouldExceed := zipFixedOverhead+cur.zipBytes+cost > downloadAllBatchByteCap || cur.Files+1 > downloadAllBatchFileCap
		if wouldExceed && cur.Files > 0 {
			batches = append(batches, cur)
			cur = uploadBatch{}
		}
		program := strings.SplitN(f.rel, "/", 2)[0]
		if !slices.Contains(cur.Programs, program) {
			cur.Programs = append(cur.Programs, program)
		}
		cur.entries = append(cur.entries, f)
		cur.Files++
		cur.Bytes += f.size
		cur.zipBytes += cost
	}
	if cur.Files > 0 {
		batches = append(batches, cur)
	}
	for i := range batches {
		batches[i].Index = i + 1
	}
	return batches, nil
}

// DownloadAllUploadsInfo reports how the current uploads tree would be
// split into batches, so the frontend can offer one download button when
// everything fits in a single zip, or one button per part otherwise.
func (a *API) DownloadAllUploadsInfo(w http.ResponseWriter, r *http.Request) {
	batches, err := planDownloadAllBatches(a.UploadDir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read uploads"})
		return
	}
	var totalFiles int
	var totalBytes int64
	type batchOut struct {
		Index    int      `json:"index"`
		Programs []string `json:"programs"`
		Labels   []string `json:"programTitles"`
		Files    int      `json:"files"`
		Bytes    int64    `json:"bytes"`
	}
	out := make([]batchOut, 0, len(batches))
	for _, b := range batches {
		labels := make([]string, len(b.Programs))
		for i, p := range b.Programs {
			labels[i] = display.ProgramTitle(p)
		}
		out = append(out, batchOut{
			Index: b.Index, Programs: b.Programs, Labels: labels, Files: b.Files, Bytes: b.Bytes,
		})
		totalFiles += b.Files
		totalBytes += b.Bytes
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"batches":    out,
		"totalFiles": totalFiles,
		"totalBytes": totalBytes,
	})
}

// DownloadAllUploads streams one batch of the uploads tree as a zip,
// preserving the on-disk folder structure (program/activities/type/...)
// so it matches what's browsable on the device.
func (a *API) DownloadAllUploads(w http.ResponseWriter, r *http.Request) {
	batchNum, err := strconv.Atoi(r.URL.Query().Get("batch"))
	if err != nil || batchNum < 1 {
		batchNum = 1
	}

	batches, err := planDownloadAllBatches(a.UploadDir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read uploads"})
		return
	}
	if batchNum > len(batches) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such batch"})
		return
	}
	batch := batches[batchNum-1]

	titles := make([]string, len(batch.Programs))
	for i, p := range batch.Programs {
		titles[i] = display.ProgramTitle(p)
	}
	filename := batchFilename(titles, batchNum, len(batches))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

	zw := zip.NewWriter(w)
	defer zw.Close()

	flusher, _ := w.(http.Flusher)
	for _, f := range batch.entries {
		addFileToZipEntry(zw, f.rel, filepath.Join(a.UploadDir, filepath.FromSlash(f.rel)))
		_ = zw.Flush()
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// batchFilename names a part by its position first, then the program(s) it
// contains, e.g. "Part 3 of 9 - Fatigue Management Program.zip". Only ASCII
// letters, digits and a few punctuation marks survive, so the value is safe
// to put inside the quoted Content-Disposition filename.
func batchFilename(programTitles []string, index, total int) string {
	name := strings.Join(programTitles, ", ")
	name = strings.Join(strings.Fields(unsafeFilenameChars.ReplaceAllString(name, " ")), " ")
	if len(name) > maxFilenameTitleLen {
		name = strings.TrimSpace(name[:maxFilenameTitleLen])
	}
	prefix := fmt.Sprintf("Part %d of %d", index, total)
	if name == "" {
		return prefix + ".zip"
	}
	return fmt.Sprintf("%s - %s.zip", prefix, name)
}

func addFileToZipEntry(zw *zip.Writer, entryName, filePath string) {
	src, err := os.Open(filePath)
	if err != nil {
		return
	}
	defer src.Close()
	dst, err := zw.Create(entryName)
	if err != nil {
		return
	}
	_, _ = io.Copy(dst, src)
}
