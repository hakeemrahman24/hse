// recompress-pdfs shrinks PDFs already in the uploads folder, the same way new
// uploads are shrunk (see storage.CompressPDF). One-off tool: run it by hand.
//
//	set -a; . ./.env; set +a
//	./recompress-pdfs            # dry run: list what would shrink, change nothing
//	./recompress-pdfs -apply     # back up each original, then replace it
//
// Originals go to a folder beside the uploads folder, never inside it, so
// the download-all zips do not pick them up. Roll back by copying a backup
// over its file.
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"hse-medpost/internal/config"
	"hse-medpost/internal/db"
	"hse-medpost/internal/storage"
)

func main() {
	minBytes := flag.Int64("min", 2_000_000, "only PDFs larger than this many bytes are recompressed")
	apply := flag.Bool("apply", false, "back up and replace files; without it nothing is changed")
	backup := flag.String("backup", "", "folder for the original files (default: pdf-backup-<time> beside the uploads folder)")
	flag.Parse()

	cfg := config.Load()
	root := filepath.Clean(cfg.UploadDir)
	if *backup == "" {
		*backup = filepath.Join(filepath.Dir(root), "pdf-backup-"+time.Now().Format("20060102-1504"))
	}

	var database *sql.DB
	if *apply {
		var err error
		if database, err = db.Open(cfg.DBDriver, cfg.DSN()); err != nil {
			fail("open database: %v", err)
		}
		defer database.Close()
	}

	var seen, replaced, failed, hashRows int
	var before, after int64
	err := storage.RecompressTree(storage.RecompressOptions{
		Root: root, BackupDir: *backup, MinBytes: *minBytes, Apply: *apply,
	}, func(r storage.RecompressResult) {
		seen++
		switch {
		case r.Err != nil:
			failed++
			fmt.Printf("FAILED   %s: %v\n", r.Path, r.Err)
			return
		case r.After >= r.Before:
			fmt.Printf("kept     %s  %s (could not be made smaller)\n", r.Path, size(r.Before))
			return
		}
		before += r.Before
		after += r.After
		verb := "would    "
		if r.Replaced {
			replaced++
			verb = "replaced "
			n, err := updateHash(database, r.Path, r.NewHash)
			if err != nil {
				failed++
				fmt.Printf("FAILED   %s: file replaced but stored hash not updated: %v\n", r.Path, err)
			}
			hashRows += n
		}
		fmt.Printf("%s%s  %s -> %s\n", verb, r.Path, size(r.Before), size(r.After))
	})
	if err != nil {
		fail("walk %s: %v", root, err)
	}
	fmt.Printf("\n%d PDF(s) over %s looked at, %d failed; %s -> %s on the ones that shrink\n",
		seen, size(*minBytes), failed, size(before), size(after))
	if *apply {
		fmt.Printf("%d replaced, %d stored hash(es) updated, originals in %s\n", replaced, hashRows, *backup)
	} else {
		fmt.Println("dry run: nothing was changed. Run with -apply to do it.")
	}
	if failed > 0 {
		os.Exit(1)
	}
}

// updateHash keeps EvidenceTable.FileHash (the SHA-256 of the stored bytes)
// true for the replaced file. Rows with no hash stay without one.
func updateHash(database *sql.DB, path, hash string) (int, error) {
	res, err := database.Exec(
		`UPDATE EvidenceTable SET FileHash = ? WHERE FileLink = ? AND COALESCE(FileHash, '') <> ''`, hash, path)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func size(n int64) string { return fmt.Sprintf("%.2f MB", float64(n)/1e6) }

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "recompress-pdfs: "+format+"\n", args...)
	os.Exit(1)
}
