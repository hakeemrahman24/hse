package datasync

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Region refresh: a Go port of indonesia-region/update.py, so the Pi can do it by itself. The
// source is github.com/cahyadsn/wilayah (MIT licence), which follows the Kemendagri decree. The
// download is pinned to the newest commit that touched the file, read as text only, checked row by
// row, and only then written to the Region table in one transaction. Nothing downloaded is ever
// executed as SQL.

const (
	regionRepo       = "cahyadsn/wilayah"
	regionSourcePath = "db/wilayah.sql"
	regionCommitsURL = "https://api.github.com/repos/" + regionRepo + "/commits?path=" + regionSourcePath + "&per_page=1"
	regionRawURL     = "https://raw.githubusercontent.com/" + regionRepo + "/%s/" + regionSourcePath

	// The whole country is about 91,600 rows; far fewer is a broken download, not a smaller Indonesia.
	minRegionRows = 80_000
	// A refresh may lose at most this share of the current table (update.py's MAX_SHRINK).
	maxRegionShrink = 0.05
	maxRegionName   = 100
	insertBatch     = 500 // 4 values per row, well under every driver's parameter limit
)

// RegionHosts is what the production fetcher may reach.
var RegionHosts = []string{"https://raw.githubusercontent.com/", "https://api.github.com/"}

var (
	regionCodeRe = regexp.MustCompile(`^\d{2}(\.\d{2}(\.\d{2}(\.\d{4})?)?)?$`)
	// One data line of the source INSERT: ('11.01.02.2018','Pasi Kuala Ba''u'),
	regionRowRe = regexp.MustCompile(`^\('([0-9.]+)','((?:[^']|'')*)'\)\s*[,;]\s*$`)
	decreeRe    = regexp.MustCompile(`Kepmendagri No [\w.\-]+ Tahun \d{4}`)
	commitSHARe = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// RegionRow is one area.
type RegionRow struct{ Code, Name string }

// RegionDiff is what a refresh changes.
type RegionDiff struct {
	Added, Removed []RegionRow
	Renamed        int
}

// Changed reports whether anything differs.
func (d RegionDiff) Changed() bool { return len(d.Added)+len(d.Removed)+d.Renamed > 0 }

func regionLevel(code string) int { return strings.Count(code, ".") + 1 }

func regionParent(code string) string {
	if i := strings.LastIndex(code, "."); i >= 0 {
		return code[:i]
	}
	return ""
}

// ParseWilayah reads the (code, name) rows out of the source file. A line that looks like a data
// row but cannot be read is an error, so an upstream format change cannot silently drop rows.
func ParseWilayah(text string) ([]RegionRow, error) {
	var rows []RegionRow
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "('") {
			continue
		}
		m := regionRowRe.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("line %d looks like a data row but cannot be read", n+1)
		}
		rows = append(rows, RegionRow{Code: m[1], Name: strings.ReplaceAll(m[2], "''", "'")})
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no data rows found; is this the right file?")
	}
	return rows, nil
}

// ValidateRegion returns an error unless the rows are safe to load. previous is the current
// row count (0 when the table is empty); minTotal is minRegionRows except in tests.
func ValidateRegion(rows []RegionRow, previous, minTotal int) error {
	if len(rows) < minTotal {
		return fmt.Errorf("only %d rows; at least %d expected", len(rows), minTotal)
	}
	var problems []string
	seen := make(map[string]bool, len(rows))
	for _, r := range rows {
		switch {
		case !regionCodeRe.MatchString(r.Code):
			problems = append(problems, fmt.Sprintf("invalid code %q", r.Code))
		case seen[r.Code]:
			problems = append(problems, fmt.Sprintf("duplicate code %q", r.Code))
		}
		seen[r.Code] = true
		switch {
		case strings.TrimSpace(r.Name) == "":
			problems = append(problems, "empty name for "+r.Code)
		case len([]rune(r.Name)) > maxRegionName:
			problems = append(problems, fmt.Sprintf("name of %s is longer than %d characters", r.Code, maxRegionName))
		case strings.ContainsFunc(r.Name, func(c rune) bool { return c == '\\' || unicode.IsControl(c) }):
			// A backslash means something different to MariaDB and SQLite, so it is refused, not escaped.
			problems = append(problems, fmt.Sprintf("name of %s has a backslash or control character", r.Code))
		}
	}
	for _, r := range rows {
		if p := regionParent(r.Code); p != "" && !seen[p] {
			problems = append(problems, fmt.Sprintf("%s has no parent %s", r.Code, p))
		}
	}
	if previous > 0 && float64(len(rows)) < float64(previous)*(1-maxRegionShrink) {
		problems = append(problems, fmt.Sprintf("row count dropped from %d to %d; check the source before loading", previous, len(rows)))
	}
	if len(problems) == 0 {
		return nil
	}
	more := ""
	if len(problems) > 10 {
		more = fmt.Sprintf(" (and %d more)", len(problems)-10)
		problems = problems[:10]
	}
	return fmt.Errorf("%s%s", strings.Join(problems, "; "), more)
}

// DiffRegion compares the current table with the new rows.
func DiffRegion(old, fresh []RegionRow) RegionDiff {
	oldMap := make(map[string]string, len(old))
	for _, r := range old {
		oldMap[r.Code] = r.Name
	}
	var d RegionDiff
	freshCodes := make(map[string]bool, len(fresh))
	for _, r := range fresh {
		freshCodes[r.Code] = true
		name, ok := oldMap[r.Code]
		switch {
		case !ok:
			d.Added = append(d.Added, r)
		case name != r.Name:
			d.Renamed++
		}
	}
	for _, r := range old {
		if !freshCodes[r.Code] {
			d.Removed = append(d.Removed, r)
		}
	}
	return d
}

func (s *Store) currentRegions(ctx context.Context) ([]RegionRow, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT Code, Name FROM Region`)
	if err != nil {
		return nil, fmt.Errorf("read Region: %w", err)
	}
	defer rows.Close()
	var out []RegionRow
	for rows.Next() {
		var r RegionRow
		if err := rows.Scan(&r.Code, &r.Name); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// replaceRegions swaps the whole table in one transaction; on any error the old rows stay.
func replaceRegions(ctx context.Context, db *sql.DB, rows []RegionRow) error {
	sorted := append([]RegionRow(nil), rows...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Code < sorted[j].Code })
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	if _, err := tx.ExecContext(ctx, `DELETE FROM Region`); err != nil {
		return fmt.Errorf("clear Region: %w", err)
	}
	for start := 0; start < len(sorted); start += insertBatch {
		end := min(start+insertBatch, len(sorted))
		var sb strings.Builder
		sb.WriteString(`INSERT INTO Region (Code, Name, Level, ParentCode) VALUES `)
		args := make([]any, 0, (end-start)*4)
		for i, r := range sorted[start:end] {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString("(?,?,?,?)")
			var parent any // NULL for a province
			if p := regionParent(r.Code); p != "" {
				parent = p
			}
			args = append(args, r.Code, r.Name, regionLevel(r.Code), parent)
		}
		if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
			return fmt.Errorf("insert Region rows: %w", err)
		}
	}
	return tx.Commit()
}

// RegionSync downloads, checks and loads the region list.
type RegionSync struct {
	Store   *Store
	Fetcher Fetcher
	// MinRows is minRegionRows unless a test sets it.
	MinRows int
}

// Run does one refresh and records the outcome. The returned message is what the Settings page
// shows; a failure leaves the Region table untouched.
func (r *RegionSync) Run(ctx context.Context) (string, error) {
	msg, err := r.run(ctx)
	if recErr := r.Store.recordAttempt(ctx, SourceRegion, err, msg); recErr != nil && err == nil {
		err = recErr
	}
	return msg, err
}

func (r *RegionSync) run(ctx context.Context) (string, error) {
	minRows := r.MinRows
	if minRows == 0 {
		minRows = minRegionRows
	}
	listing, err := r.Fetcher.Get(ctx, regionCommitsURL)
	if err != nil {
		return "", err
	}
	var commits []struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(listing, &commits); err != nil || len(commits) == 0 || !commitSHARe.MatchString(commits[0].SHA) {
		return "", fmt.Errorf("GitHub sent an unexpected commit list")
	}
	sha := commits[0].SHA
	body, err := r.Fetcher.Get(ctx, fmt.Sprintf(regionRawURL, sha))
	if err != nil {
		return "", err
	}
	fresh, err := ParseWilayah(string(body))
	if err != nil {
		return "", err
	}
	current, err := r.Store.currentRegions(ctx)
	if err != nil {
		return "", err
	}
	if err := ValidateRegion(fresh, len(current), minRows); err != nil {
		return "", fmt.Errorf("refused, region table unchanged: %w", err)
	}
	decree := decreeRe.FindString(string(body))
	diff := DiffRegion(current, fresh)
	if !diff.Changed() {
		return fmt.Sprintf("Checked %s: no change (%d areas). %s", sha[:7], len(fresh), decree), nil
	}
	if err := replaceRegions(ctx, r.Store.DB, fresh); err != nil {
		return "", err
	}
	return fmt.Sprintf("Updated from %s: %d added, %d removed, %d renamed, %d areas in total. %s",
		sha[:7], len(diff.Added), len(diff.Removed), diff.Renamed, len(fresh), decree), nil
}
