package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// How evidence files are named and where they live, so that a person looking at the folder (or a
// backup of it) can tell what a file is and which observation it belongs to:
//
//	<EVIDENCE_DIR>/2026/10/OBS-000123/OBS-000123_before_photo_01.jpg
//	                                  OBS-000123_before_pdf_01_scaffold-inspection.pdf
//	                                  OBS-000123_after_photo_01.jpg
//
//   - Year, then month the observation was recorded, then one folder per observation. Folders and
//     files sort by date and by observation number.
//   - OBS-<6-digit id> is the observation id (the same number as the SOC-<id> reference).
//   - before = evidence at the time of the finding, after = proof of the fix.
//   - The number counts the photos (or PDFs) of that kind; PDFs also carry a short form of the
//     name they were uploaded under.
//
// A file is first written with a random name straight into the folder (the observation may not
// exist yet), then moved to its readable place as soon as the database row exists
// (organizeObservation). The database keeps the path relative to EVIDENCE_DIR in stored_name; an
// old random name without a folder still works, and "-organize-evidence" moves those too.

var organizeMu sync.Mutex // one reorganisation at a time, so two uploads cannot pick the same number

var seqInName = regexp.MustCompile(`_(\d{2,})(?:_[^/]*)?\.[a-z]+$`)

// evidenceRel returns the safe relative path for a stored name, or false when it could point outside
// the evidence folder. Names come from the database, but a damaged row must never read or delete
// anything else on the disk.
func evidencePath(dir, name string) (string, bool) {
	if name == "" || filepath.IsAbs(name) || strings.ContainsRune(name, 0) {
		return "", false
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.Join(dir, clean), true
}

// pruneEmptyDirs removes the folders a deleted file leaves empty, up to (not including) dir.
func pruneEmptyDirs(dir, file string) {
	root := filepath.Clean(dir)
	for d := filepath.Dir(file); d != root && strings.HasPrefix(d, root+string(filepath.Separator)); d = filepath.Dir(d) {
		if err := os.Remove(d); err != nil { // fails while the folder still has files: that is fine
			return
		}
	}
}

// slugOf shortens an uploaded file name for use inside a file name: lower case letters and digits,
// single hyphens, at most 40 characters.
func slugOf(display string) string {
	display = strings.TrimSuffix(strings.TrimSuffix(display, ".pdf"), ".PDF")
	var b strings.Builder
	dash := true
	for _, r := range strings.ToLower(display) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			dash = false
		case !dash:
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 40 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

func obsRef(id int64) string { return fmt.Sprintf("OBS-%06d", id) }

// readableName builds the folder-relative path (with forward slashes) for one file.
func readableName(id int64, created time.Time, kind, fileType string, seq int, display string) string {
	ref := obsRef(id)
	base := fmt.Sprintf("%s_%s_%s_%02d", ref, kind, map[string]string{"photo": "photo", "pdf": "pdf"}[fileType], seq)
	ext := ".jpg"
	if fileType == "pdf" {
		ext = ".pdf"
		if s := slugOf(display); s != "" {
			base += "_" + s
		}
	}
	return fmt.Sprintf("%04d/%02d/%s/%s%s", created.Year(), int(created.Month()), ref, base, ext)
}

type evidenceRow struct {
	ID                                  int64
	Kind, FileType, StoredName, Display string
}

type evidenceMove struct {
	RowID    int64
	From, To string
}

// planMoves decides the readable name of every file that still has an old flat name. exists tells
// whether a target path is already taken; numbering continues after the highest number in use for
// the same kind and type, and skips any taken name.
func planMoves(id int64, created time.Time, rows []evidenceRow, exists func(rel string) bool) []evidenceMove {
	highest := map[string]int{}
	for _, r := range rows {
		if strings.Contains(r.StoredName, "/") {
			if m := seqInName.FindStringSubmatch(r.StoredName); m != nil {
				n, _ := strconv.Atoi(m[1])
				highest[r.Kind+"/"+r.FileType] = max(highest[r.Kind+"/"+r.FileType], n)
			}
		}
	}
	var moves []evidenceMove
	for _, r := range rows {
		if strings.Contains(r.StoredName, "/") {
			continue
		}
		key := r.Kind + "/" + r.FileType
		for {
			highest[key]++
			to := readableName(id, created, r.Kind, r.FileType, highest[key], r.Display)
			if !exists(to) {
				moves = append(moves, evidenceMove{RowID: r.ID, From: r.StoredName, To: to})
				break
			}
		}
	}
	return moves
}

// organizeObservation gives the observation's files their readable names and folder. A file that
// cannot be moved keeps its old name (still valid), so a failure never loses a file.
func organizeObservation(ctx context.Context, db *sql.DB, dir string, id int64) error {
	organizeMu.Lock()
	defer organizeMu.Unlock()
	var created time.Time
	if err := db.QueryRowContext(ctx, `SELECT created_at FROM observations_observation WHERE id = ?`, id).Scan(&created); err != nil {
		return err
	}
	rs, err := db.QueryContext(ctx, `SELECT id, kind, file_type, stored_name, COALESCE(display_name,'') FROM observation_files WHERE observation_id = ? ORDER BY id`, id)
	if err != nil {
		return err
	}
	var rows []evidenceRow
	for rs.Next() {
		var r evidenceRow
		if err := rs.Scan(&r.ID, &r.Kind, &r.FileType, &r.StoredName, &r.Display); err != nil {
			rs.Close()
			return err
		}
		rows = append(rows, r)
	}
	if err := closeRows(rs); err != nil {
		return err
	}
	taken := func(rel string) bool {
		p, ok := evidencePath(dir, rel)
		if !ok {
			return true
		}
		_, err := os.Stat(p)
		return err == nil
	}
	var errs []error
	for _, m := range planMoves(id, created, rows, taken) {
		from, ok1 := evidencePath(dir, m.From)
		to, ok2 := evidencePath(dir, m.To)
		if !ok1 || !ok2 {
			continue
		}
		if _, err := os.Stat(from); err != nil {
			errs = append(errs, fmt.Errorf("file of row %d is missing: %w", m.RowID, err))
			continue
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o750); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.Rename(from, to); err != nil {
			errs = append(errs, err)
			continue
		}
		if _, err := db.ExecContext(ctx, `UPDATE observation_files SET stored_name = ? WHERE id = ? AND stored_name = ?`, m.To, m.RowID, m.From); err != nil {
			_ = os.Rename(to, from) // keep file and row in step
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// organizeQuietly is for request handlers: a naming problem is logged, never shown to the user,
// because the files are saved and working either way.
func organizeQuietly(ctx context.Context, db *sql.DB, dir string, id int64) {
	if err := organizeObservation(context.WithoutCancel(ctx), db, dir, id); err != nil {
		log.Printf("evidence naming for observation %d: %v", id, err)
	}
}

// organizeAll renames every file that still has an old random name (command line: -organize-evidence).
func organizeAll(ctx context.Context, db *sql.DB, dir string) (observations int, err error) {
	rs, err := db.QueryContext(ctx, `SELECT DISTINCT observation_id FROM observation_files WHERE stored_name NOT LIKE '%/%' ORDER BY observation_id`)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rs.Next() {
		var id int64
		if err := rs.Scan(&id); err != nil {
			rs.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := closeRows(rs); err != nil {
		return 0, err
	}
	var errs []error
	for _, id := range ids {
		if err := organizeObservation(ctx, db, dir, id); err != nil {
			errs = append(errs, fmt.Errorf("observation %d: %w", id, err))
			continue
		}
		observations++
	}
	return observations, errors.Join(errs...)
}
