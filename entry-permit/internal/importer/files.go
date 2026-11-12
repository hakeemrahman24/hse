package importer

import (
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// DocSpec says how one kind of uploaded document was named in the old Drive folder.
//
// WHY names and not Drive ids: the sheet holds a Drive file id, but the files were downloaded
// from Drive with their names, not their ids. The old script named every upload
// "<Name without spaces>_<label>[_<date>]", so the name (and the day, where the name has one)
// is the only thing that ties a file back to a request.
type DocSpec struct {
	Field   string         // the permit column that stores the path, and the label filestore uses
	Folder  string         // the folder under drive-files
	Pattern *regexp.Regexp // matches a file name without extension and without " (1)"; group "name", optional group "date"
	// Fallback is an older label for the same document, tried only for requests Pattern left
	// without a file (the old sheet's ID uploads exist as both "_KTP" and "_ID").
	Fallback *regexp.Regexp
	// NewestWins and OneTimeKind are the owner's rule for a worker whose files and requests do not
	// match in number (decided 2026-11-08). A photo and a KTP are uploaded once and never expire, so
	// any of a worker's files shows the right face or number and the newest is used. A passport or
	// visa expires and is uploaded again, so there the count is a real question and nothing is taken.
	// NewestWins applies to every file of the spec (the photo folder); OneTimeKind applies to files
	// whose "kind" group of Pattern equals it (the "KTP" files of the ID folder).
	NewestWins  bool
	OneTimeKind string
}

// Request is one request that holds a link for this kind of document.
type Request struct {
	ID, Name string
	At       string // when the request was submitted, any form Moment reads
	// Key, when set, is what a file's "date" group must equal instead of the submission day:
	// a waiver letter is named by the visit end day, a ticket photo by its ticket id.
	Key string
}

// Pairing is the result of matching one folder to its requests. Request ids only, so a report
// built from it holds no personal data.
type Pairing struct {
	Matched      map[string]string // request id -> file name
	Missing      []string          // requests with no file at all
	Ambiguous    []string          // requests whose files cannot be told apart, so none was taken
	Inferred     []string          // requests given a file by the newest-file rule: a choice, not an exact pairing
	Unrecognised int               // files whose name does not follow the old naming
}

// dupSuffix is what a download adds to a second file of the same name: "Name_KTP (1).jpg".
var dupSuffix = regexp.MustCompile(`\s*\((\d+)\)$`)

// squash makes names comparable: case, spaces and punctuation are dropped, so "O'Connor-Ray"
// and the file name "OConnorRay" are the same.
func squash(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

type namedFile struct {
	file string
	date string // yyyymmdd from the file name, or ""
	dup  int    // 0 for the plain name, 1 for " (1)"
	// oneTime: the document does not expire, so the newest of a worker's files may stand in for any
	// of their requests when the counts differ.
	oneTime bool
}

// PairFiles matches requests to the files of one folder, by worker name.
//
// Rules, in order. A file date equal to the request's day settles that pair. What is left of
// a worker is paired in order (earliest request with the plain name, the next with " (1)")
// only when the counts are equal. Any other count is a guess between documents of a person,
// so it is reported and nothing is taken: a wrong ID card on a permit is worse than none.
// Requests still unpaired are tried once more against the spec's Fallback label.
func PairFiles(spec DocSpec, reqs []Request, files []string) Pairing {
	primary, rest := indexFiles(spec, spec.Pattern, files, true)
	out := pairAll(primary, reqs)
	out.Unrecognised = len(rest)
	if spec.Fallback == nil {
		return out
	}
	fallback, rest := indexFiles(spec, spec.Fallback, rest, false) // the older label is of unknown kind: never guessed
	out.Unrecognised = len(rest)
	byID := map[string]Request{}
	for _, r := range reqs {
		byID[r.ID] = r
	}
	// Only a worker who has files under the fallback label is tried again; for anyone else the
	// first answer (missing, or several files) stands.
	var retry []Request
	retried := map[string]bool{}
	for _, id := range append(append([]string{}, out.Missing...), out.Ambiguous...) {
		if r := byID[id]; len(fallback[squash(r.Name)]) > 0 {
			retry = append(retry, r)
			retried[id] = true
		}
	}
	if len(retry) == 0 {
		return out
	}
	second := pairAll(fallback, retry)
	out.Missing = append(dropRetried(out.Missing, retried), second.Missing...)
	out.Ambiguous = append(dropRetried(out.Ambiguous, retried), second.Ambiguous...)
	sort.Strings(out.Missing)
	sort.Strings(out.Ambiguous)
	for id, f := range second.Matched {
		out.Matched[id] = f
	}
	return out
}

// dropRetried removes the ids that were tried again, so each id is reported once.
func dropRetried(ids []string, retried map[string]bool) []string {
	var out []string
	for _, id := range ids {
		if !retried[id] {
			out = append(out, id)
		}
	}
	return out
}

// indexFiles groups the files a pattern recognises by worker name and returns the others.
func indexFiles(spec DocSpec, pattern *regexp.Regexp, files []string, allowRule bool) (map[string][]namedFile, []string) {
	nameAt, dateAt, kindAt := pattern.SubexpIndex("name"), pattern.SubexpIndex("date"), pattern.SubexpIndex("kind")
	byName := map[string][]namedFile{}
	var rest []string
	for _, f := range files {
		stem := strings.TrimSuffix(f, filepath.Ext(f))
		nf := namedFile{file: f}
		if m := dupSuffix.FindStringSubmatch(stem); m != nil {
			nf.dup, _ = strconv.Atoi(m[1])
			stem = dupSuffix.ReplaceAllString(stem, "")
		}
		m := pattern.FindStringSubmatch(stem)
		if m == nil || nameAt < 0 {
			rest = append(rest, f)
			continue
		}
		if dateAt >= 0 {
			nf.date = m[dateAt]
		}
		nf.oneTime = allowRule && (spec.NewestWins || (kindAt >= 0 && spec.OneTimeKind != "" && m[kindAt] == spec.OneTimeKind))
		key := squash(m[nameAt])
		byName[key] = append(byName[key], nf)
	}
	return byName, rest
}

// pairAll pairs every worker's requests with that worker's files.
func pairAll(byName map[string][]namedFile, reqs []Request) Pairing {
	out := Pairing{Matched: map[string]string{}}
	byPerson := map[string][]Request{}
	for _, r := range reqs {
		key := squash(r.Name)
		byPerson[key] = append(byPerson[key], r)
	}
	for key, rs := range byPerson {
		pairPerson(&out, rs, byName[key])
	}
	sort.Strings(out.Missing)
	sort.Strings(out.Ambiguous)
	sort.Strings(out.Inferred)
	return out
}

// pairPerson applies the rules to the requests and files of one worker.
func pairPerson(out *Pairing, rs []Request, fs []namedFile) {
	sort.SliceStable(rs, func(i, j int) bool { return momentOf(rs[i].At) < momentOf(rs[j].At) })
	sort.SliceStable(fs, func(i, j int) bool {
		if fs[i].date != fs[j].date {
			return fs[i].date < fs[j].date
		}
		if fs[i].dup != fs[j].dup {
			return fs[i].dup < fs[j].dup
		}
		return fs[i].file < fs[j].file
	})
	taken := make([]bool, len(fs))
	var restR []Request
	for _, r := range rs {
		day := r.Key
		if day == "" {
			day = strings.ReplaceAll(momentOf(r.At), "-", "")
			if len(day) >= 8 {
				day = day[:8]
			}
		}
		hit := -1
		for i, f := range fs {
			if !taken[i] && f.date != "" && f.date == day {
				hit = i
				break
			}
		}
		if hit < 0 {
			restR = append(restR, r)
			continue
		}
		taken[hit] = true
		out.Matched[r.ID] = fs[hit].file
	}
	var restF []namedFile
	for i, f := range fs {
		if !taken[i] {
			restF = append(restF, f)
		}
	}
	switch {
	case len(restR) == 0:
	case len(restF) == 0:
		for _, r := range restR {
			out.Missing = append(out.Missing, r.ID)
		}
	case len(restR) == len(restF):
		for i, r := range restR {
			out.Matched[r.ID] = restF[i].file
		}
	case allOneTime(restF):
		// Newest wins: the last request gets the newest file, the one before it the next newest, and
		// so on; surplus old files are left unused. With fewer files than requests the oldest file
		// is reused for the earliest requests, which is right for a document that never changes.
		offset := len(restF) - len(restR)
		for i, r := range restR {
			j := i + offset
			if j < 0 {
				j = 0
			}
			out.Matched[r.ID] = restF[j].file
			out.Inferred = append(out.Inferred, r.ID)
		}
	default:
		for _, r := range restR {
			out.Ambiguous = append(out.Ambiguous, r.ID)
		}
	}
}

// allOneTime is true when every file is a document that does not expire.
func allOneTime(fs []namedFile) bool {
	for _, f := range fs {
		if !f.oneTime {
			return false
		}
	}
	return len(fs) > 0
}

// momentOf reads a sheet time into "yyyy-mm-dd hh:mm:ss" so times sort; unreadable stays as is.
func momentOf(s string) string {
	if m, ok := Moment(s); ok {
		return m
	}
	return s
}
