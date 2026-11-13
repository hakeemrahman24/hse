package datasync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Malaria endemicity, scraped from the Kemenkes malaria map (malaria.kemkes.go.id).
//
// The map page is drawn from a public JSON route, api/sismal/chartMap/<year>, one object per
// regency: its label, case count and population. Reading that route is the "scrape": it is the
// same data the map shows, without parsing HTML.
//
// Three things the data forces:
//   - Kemenkes numbers some regencies differently from the Kemendagri codes in our Region table
//     (Fakfak is 9101 there), and the older Papua rows use the pre-2022 Papua codes. So rows are
//     matched to Region by NAME (kind + letters), with the code and province only as tiebreakers.
//   - The newest year can be missing regencies (the 2025 map has no Papua Tengah, Papua
//     Pegunungan or part of Papua yet). Those regencies take their label from the next older year,
//     and DataYear says which year each row is from.
//   - The site says the case data is not yet validated, so a scrape only fills the staging table;
//     an admin applies it after looking at what would change.

const (
	endemicityURL = "https://malaria.kemkes.go.id/api/sismal/chartMap/%d"
	// yearsToTry is how many map years back are read (newest first), to fill regencies that the
	// newest year lacks.
	yearsToTry = 3
	// minPrimaryRows: a year with fewer regencies than this is treated as not published yet.
	minPrimaryRows = 300
	// minResolvedRows: fewer matched regencies than this means the format or the Region table is off.
	minResolvedRows = 400
)

// EndemicityHosts is what the production fetcher may reach for the map data.
var EndemicityHosts = []string{"https://malaria.kemkes.go.id/"}

// EndemicityLabels are the labels the map publishes, in order of severity. "Eliminasi" means the
// regency is no longer endemic. An unknown label stops the scrape, so a renamed class is noticed.
var EndemicityLabels = []string{"Eliminasi", "Endemis Rendah", "Endemis Sedang", "Endemis Tinggi I", "Endemis Tinggi II", "Endemis Tinggi III"}

// ErrNothingStaged is returned by ApplyStaged when there is no scrape waiting.
var ErrNothingStaged = fmt.Errorf("datasync: there is no scraped data waiting to be applied")

// aliases are regency names that differ between Kemenkes and the Region table. Both sides are
// normal names, normalised by regencyKey.
var aliases = map[string]string{
	regencyKey("Kepulauan Sitaro"):       regencyKey("Kabupaten Kepulauan Siau Tagulandang Biaro"),
	regencyKey("Siau Tagulandang Biaro"): regencyKey("Kabupaten Kepulauan Siau Tagulandang Biaro"),
	regencyKey("Maluku Tenggara Barat"):  regencyKey("Kabupaten Kepulauan Tanimbar"),
	// Found by running against the live map: Kemenkes reads "Kotabaru" as a city, spells
	// Mahakam Ulu "Mahakam Hulu", and still calls Pasangkayu by its old name.
	regencyKey("Kota Baru"):    regencyKey("Kabupaten Kotabaru"),
	regencyKey("Mahakam Hulu"): regencyKey("Kabupaten Mahakam Ulu"),
	regencyKey("Mamuju Utara"): regencyKey("Kabupaten Pasangkayu"),
}

// EndemicityRow is one regency in medpost's terms.
type EndemicityRow struct {
	Code, Name, Province, Label string
	DataYear, Cases, Population int
	SourceUpdatedAt             string
}

// Per1000 is positives per 1,000 of the total population, as the map computes it. It is NOT the
// official endemicity measure, so a label and this number can disagree (Murung Raya is labelled
// Rendah with 2.4 per 1,000); the label is the one to trust.
func (r EndemicityRow) Per1000() float64 {
	if r.Population <= 0 {
		return 0
	}
	return float64(r.Cases) * 1000 / float64(r.Population)
}

// kemkesRow is one object of the map route. Every number arrives as a JSON string.
type kemkesRow struct {
	LastUpdate string `json:"last_update"`
	Province   string `json:"namaprop1"`
	Code       string `json:"id_kabupaten"`
	Regency    string `json:"nama_kabupaten"`
	Positive   string `json:"jum_positif_malaria"`
	Population string `json:"jum_pddk"`
	Label      string `json:"endemisitas"`
}

// regencyKey makes names comparable: lower case, "kota" kept apart from "kabupaten" (Kota Sorong
// and Kabupaten Sorong are different places), "Kep." spelled out, letters only.
func regencyKey(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, "kep.", "kepulauan")
	// The region list calls Jakarta's areas "Kota Administrasi Jakarta Barat" and "Kabupaten
	// Administrasi Kepulauan Seribu"; Kemenkes drops the word.
	s = strings.ReplaceAll(s, " administrasi ", " ")
	kind := "kab"
	switch {
	case strings.HasPrefix(s, "kota "):
		kind, s = "kota", strings.TrimPrefix(s, "kota ")
	case strings.HasPrefix(s, "kabupaten "):
		s = strings.TrimPrefix(s, "kabupaten ")
	case strings.HasPrefix(s, "kab. "):
		s = strings.TrimPrefix(s, "kab. ")
	}
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) {
			b.WriteRune(r)
		}
	}
	return kind + ":" + b.String()
}

// lettersOnly is a province name reduced to lower-case letters.
func lettersOnly(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

type regionRegency struct{ Code, Name, Province string }

// regionIndex finds a Region regency for a Kemenkes row.
type regionIndex struct{ byKey map[string][]regionRegency }

func (s *Store) loadRegionIndex(ctx context.Context) (*regionIndex, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT r.Code, r.Name, p.Name FROM Region r JOIN Region p ON p.Code = r.ParentCode WHERE r.Level = 2`)
	if err != nil {
		return nil, fmt.Errorf("read regencies: %w", err)
	}
	defer rows.Close()
	ix := &regionIndex{byKey: map[string][]regionRegency{}}
	for rows.Next() {
		var rg regionRegency
		if err := rows.Scan(&rg.Code, &rg.Name, &rg.Province); err != nil {
			return nil, err
		}
		k := regencyKey(rg.Name)
		ix.byKey[k] = append(ix.byKey[k], rg)
	}
	return ix, rows.Err()
}

// kemkesDotCode turns Kemenkes' "9101" into the Region style "91.01".
func kemkesDotCode(code string) string {
	if len(code) < 3 {
		return code
	}
	return code[:2] + "." + code[2:]
}

// find returns the one Region regency a Kemenkes row means, or false when there is none or the
// answer is ambiguous. Kemenkes writes "KOTA X" for cities and a bare name for kabupaten.
func (ix *regionIndex) find(k kemkesRow) (regionRegency, bool) {
	key := regencyKey(k.Regency)
	if official, ok := aliases[key]; ok {
		key = official
	}
	found := ix.byKey[key]
	if len(found) == 1 {
		return found[0], true
	}
	var narrowed []regionRegency
	for _, rg := range found {
		if rg.Code == kemkesDotCode(k.Code) || lettersOnly(rg.Province) == lettersOnly(k.Province) {
			narrowed = append(narrowed, rg)
		}
	}
	if len(narrowed) == 1 {
		return narrowed[0], true
	}
	return regionRegency{}, false
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func knownLabel(label string) bool {
	for _, l := range EndemicityLabels {
		if l == label {
			return true
		}
	}
	return false
}

// ParseEndemicity reads one year of the map route.
func ParseEndemicity(body []byte) ([]kemkesRow, error) {
	var rows []kemkesRow
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("the malaria map sent data that is not the expected list: %w", err)
	}
	return rows, nil
}

// mergeYears resolves the years (newest first) into one row per Region regency. A regency takes
// its label from the newest year that has it. Rows that match no regency are returned by name.
func mergeYears(ix *regionIndex, years []int, perYear [][]kemkesRow) (rows []EndemicityRow, unmatched []string, err error) {
	have := map[string]bool{}
	seenUnmatched := map[string]bool{}
	for i, year := range years {
		for _, k := range perYear[i] {
			if !knownLabel(k.Label) {
				return nil, nil, fmt.Errorf("the malaria map has a label this app does not know: %q (%s, %d); the scrape was stopped so nothing is mislabelled", k.Label, k.Regency, year)
			}
			rg, ok := ix.find(k)
			if !ok {
				if name := strings.TrimSpace(k.Regency + " (" + k.Province + ")"); !seenUnmatched[name] {
					seenUnmatched[name] = true
					unmatched = append(unmatched, name)
				}
				continue
			}
			if have[rg.Code] {
				continue // a newer year already supplied this regency
			}
			have[rg.Code] = true
			rows = append(rows, EndemicityRow{
				Code: rg.Code, Name: rg.Name, Province: rg.Province, Label: k.Label, DataYear: year,
				Cases: atoi(k.Positive), Population: atoi(k.Population), SourceUpdatedAt: k.LastUpdate,
			})
		}
	}
	sort.Strings(unmatched)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Code < rows[j].Code })
	return rows, unmatched, nil
}

// EndemicitySync scrapes the map into the staging table.
type EndemicitySync struct {
	Store   *Store
	Fetcher Fetcher
	// MinPrimary and MinResolved replace minPrimaryRows and minResolvedRows when set (tests).
	MinPrimary, MinResolved int
}

// Run reads the map and stages it, then records the outcome. It never touches the live table.
func (e *EndemicitySync) Run(ctx context.Context) (string, error) {
	msg, err := e.run(ctx)
	if recErr := e.Store.recordAttempt(ctx, SourceEndemicity, err, msg); recErr != nil && err == nil {
		err = recErr
	}
	return msg, err
}

func orDefault(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

func (e *EndemicitySync) run(ctx context.Context) (string, error) {
	ix, err := e.Store.loadRegionIndex(ctx)
	if err != nil {
		return "", err
	}
	if len(ix.byKey) == 0 {
		return "", fmt.Errorf("the Region table has no regencies; load it first")
	}
	minPrimary, minResolved := orDefault(e.MinPrimary, minPrimaryRows), orDefault(e.MinResolved, minResolvedRows)
	var years []int
	var perYear [][]kemkesRow
	var lastErr error
	thisYear := e.Store.now().Year()
	for y := thisYear; y > thisYear-yearsToTry; y-- {
		body, err := e.Fetcher.Get(ctx, fmt.Sprintf(endemicityURL, y))
		if err != nil {
			lastErr = err
			continue
		}
		rows, err := ParseEndemicity(body)
		if err != nil {
			return "", err
		}
		// The first year that is published is the primary one; years after it only fill gaps. A
		// year that is not published yet answers with an empty or tiny list and is skipped.
		if len(years) == 0 && len(rows) < minPrimary {
			continue
		}
		years, perYear = append(years, y), append(perYear, rows)
	}
	if len(years) == 0 {
		if lastErr != nil {
			return "", lastErr
		}
		return "", fmt.Errorf("the malaria map has no published year with at least %d regencies", minPrimary)
	}
	rows, unmatched, err := mergeYears(ix, years, perYear)
	if err != nil {
		return "", err
	}
	if len(rows) < minResolved {
		return "", fmt.Errorf("only %d regencies could be matched to the region list; at least %d expected", len(rows), minResolved)
	}
	live, err := e.Store.liveRows(ctx, "MalariaEndemicity")
	if err != nil {
		return "", err
	}
	changes := diffRows(live, rows)
	if err := e.Store.replaceStaged(ctx, rows); err != nil {
		return "", err
	}
	msg := fmt.Sprintf("Read map year %d (older years %v fill gaps). %d regencies, %d would change.", years[0], years[1:], len(rows), len(changes))
	if len(unmatched) > 0 {
		msg += fmt.Sprintf(" Not in the region list, skipped: %s.", strings.Join(unmatched, ", "))
	}
	return msg, nil
}

// Change is one line of the review screen.
type Change struct {
	Code, Name, Province string
	Old, New             string // labels; "" = not on the list
	OldYear, NewYear     int
}

func (s *Store) liveRows(ctx context.Context, table string) (map[string]EndemicityRow, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT RegencyCode, RegencyName, ProvinceName, Label, DataYear, Cases, Population, SourceUpdatedAt FROM `+table)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", table, err)
	}
	defer rows.Close()
	out := map[string]EndemicityRow{}
	for rows.Next() {
		var r EndemicityRow
		if err := rows.Scan(&r.Code, &r.Name, &r.Province, &r.Label, &r.DataYear, &r.Cases, &r.Population, &r.SourceUpdatedAt); err != nil {
			return nil, err
		}
		out[r.Code] = r
	}
	return out, rows.Err()
}

func diffRows(live map[string]EndemicityRow, fresh []EndemicityRow) []Change {
	var out []Change
	seen := map[string]bool{}
	for _, n := range fresh {
		seen[n.Code] = true
		old, had := live[n.Code]
		if had && old.Label == n.Label {
			continue
		}
		out = append(out, Change{Code: n.Code, Name: n.Name, Province: n.Province, Old: old.Label, New: n.Label, OldYear: old.DataYear, NewYear: n.DataYear})
	}
	for code, old := range live {
		if !seen[code] {
			out = append(out, Change{Code: code, Name: old.Name, Province: old.Province, Old: old.Label, OldYear: old.DataYear})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// Pending is the staged scrape and what applying it would change.
type Pending struct {
	Rows    int
	Changes []Change
}

// Pending reads the staging table against the live one.
func (s *Store) Pending(ctx context.Context) (Pending, error) {
	staged, err := s.liveRows(ctx, "MalariaEndemicityStaged")
	if err != nil || len(staged) == 0 {
		return Pending{}, err
	}
	live, err := s.liveRows(ctx, "MalariaEndemicity")
	if err != nil {
		return Pending{}, err
	}
	fresh := make([]EndemicityRow, 0, len(staged))
	for _, r := range staged {
		fresh = append(fresh, r)
	}
	return Pending{Rows: len(staged), Changes: diffRows(live, fresh)}, nil
}

func insertEndemicity(ctx context.Context, tx *sql.Tx, table string, rows []EndemicityRow) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO `+table+` (RegencyCode, RegencyName, ProvinceName, Label, DataYear, Cases, Population, SourceUpdatedAt) VALUES (?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range rows {
		if _, err := stmt.ExecContext(ctx, r.Code, r.Name, r.Province, r.Label, r.DataYear, r.Cases, r.Population, r.SourceUpdatedAt); err != nil {
			return fmt.Errorf("stage %s: %w", r.Code, err)
		}
	}
	return nil
}

func (s *Store) replaceStaged(ctx context.Context, rows []EndemicityRow) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	if _, err := tx.ExecContext(ctx, `DELETE FROM MalariaEndemicityStaged`); err != nil {
		return err
	}
	if err := insertEndemicity(ctx, tx, "MalariaEndemicityStaged", rows); err != nil {
		return err
	}
	return tx.Commit()
}

// ApplyStaged makes the staged scrape the live list, in one transaction, and clears the staging
// table. It refuses when nothing is staged.
func (s *Store) ApplyStaged(ctx context.Context) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM MalariaEndemicityStaged`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNothingStaged
	}
	for _, q := range []string{
		`DELETE FROM MalariaEndemicity`,
		`INSERT INTO MalariaEndemicity (RegencyCode, RegencyName, ProvinceName, Label, DataYear, Cases, Population, SourceUpdatedAt)
		 SELECT RegencyCode, RegencyName, ProvinceName, Label, DataYear, Cases, Population, SourceUpdatedAt FROM MalariaEndemicityStaged`,
		`DELETE FROM MalariaEndemicityStaged`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.markApplied(ctx, SourceEndemicity)
}

// DiscardStaged drops the staged scrape.
func (s *Store) DiscardStaged(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM MalariaEndemicityStaged`)
	return err
}

// Statuses a consumer of the region API can rely on, so nobody has to parse the map's text.
const (
	StatusFree    = "free"    // the map's "Eliminasi"
	StatusLow     = "low"     // Endemis Rendah
	StatusMedium  = "medium"  // Endemis Sedang
	StatusHigh    = "high"    // Endemis Tinggi I, II or III
	StatusUnknown = "unknown" // not on the applied list: NO data, never "free"
)

// Status turns a map label into a stable status. An unrecognised label is "unknown".
func Status(label string) string {
	switch label {
	case "Eliminasi":
		return StatusFree
	case "Endemis Rendah":
		return StatusLow
	case "Endemis Sedang":
		return StatusMedium
	case "Endemis Tinggi I", "Endemis Tinggi II", "Endemis Tinggi III":
		return StatusHigh
	}
	return StatusUnknown
}

// Snapshot is the applied list by regency code, with the date (YYYY-MM-DD) it was applied.
// Before migration 031/047 the tables do not exist: the error says so and the caller decides
// what to show (the region API shows every regency as unknown).
func (s *Store) Snapshot(ctx context.Context) (rows map[string]EndemicityRow, appliedAt string, err error) {
	rows, err = s.liveRows(ctx, "MalariaEndemicity")
	if err != nil {
		return nil, "", err
	}
	st, err := s.Get(ctx, SourceEndemicity)
	if err != nil {
		return nil, "", err
	}
	if len(st.AppliedAt) >= len("2006-01-02") {
		appliedAt = st.AppliedAt[:len("2006-01-02")]
	}
	return rows, appliedAt, nil
}

// One returns the applied row for a regency code, and false when it is not on the list.
func (s *Store) One(ctx context.Context, code string) (EndemicityRow, bool, error) {
	var r EndemicityRow
	err := s.DB.QueryRowContext(ctx, `SELECT RegencyCode, RegencyName, ProvinceName, Label, DataYear, Cases, Population, SourceUpdatedAt
		FROM MalariaEndemicity WHERE RegencyCode = ?`, code).
		Scan(&r.Code, &r.Name, &r.Province, &r.Label, &r.DataYear, &r.Cases, &r.Population, &r.SourceUpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return EndemicityRow{}, false, nil
	}
	if err != nil {
		return EndemicityRow{}, false, fmt.Errorf("read endemicity of %s: %w", code, err)
	}
	return r, true, nil
}

// Live is the applied list, for the Settings page and future readers.
func (s *Store) Live(ctx context.Context) ([]EndemicityRow, error) {
	m, err := s.liveRows(ctx, "MalariaEndemicity")
	if err != nil {
		return nil, err
	}
	out := make([]EndemicityRow, 0, len(m))
	for _, r := range m {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out, nil
}
