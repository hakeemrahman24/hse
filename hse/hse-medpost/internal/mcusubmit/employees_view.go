package mcusubmit

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// How the Employee checkups list is ordered, and the addresses its clickable parts point to (owner,
// 2026-11-14). The whole list is sent in one go and scrolls inside the page, so the order is chosen on
// the server from two query words, sort and dir. Anything else falls back to the default, so a made-up
// address cannot break the page, and every state is a plain link that works without JavaScript.

// The sortable columns. Position and nationality are not columns any more (they sit behind More).
const (
	sortName     = "name"
	sortCompany  = "company"
	sortCheckup  = "checkup"
	sortUntil    = "until"
	sortValidity = "validity"

	dirAsc  = "asc"
	dirDesc = "desc"

	// The list opens with the latest medical checkup on top (owner, 2026-11-14).
	defaultSortKey = sortCheckup
	defaultSortDir = dirDesc
)

// employeeSortKeys is the set a request may name, in the order the columns appear.
var employeeSortKeys = []string{sortName, sortCompany, sortCheckup, sortUntil, sortValidity}

// firstDir is the direction a column starts with when it is clicked for the first time: dates newest
// first, words A to Z.
func firstDir(key string) string {
	if key == sortCheckup || key == sortUntil {
		return dirDesc
	}
	return dirAsc
}

func oppositeDir(dir string) string {
	if dir == dirAsc {
		return dirDesc
	}
	return dirAsc
}

// parseSort reads sort and dir from the query. explicit says the address named a valid column, which
// is when the choice is carried in the page's links; the default order needs no words in the address.
func parseSort(q url.Values) (key, dir string, explicit bool) {
	key = q.Get("sort")
	for _, k := range employeeSortKeys {
		if k == key {
			dir = q.Get("dir")
			if dir != dirAsc && dir != dirDesc {
				dir = firstDir(key)
			}
			return key, dir, true
		}
	}
	return defaultSortKey, defaultSortDir, false
}

// sortValue is what a row is compared on for a column, and whether it has one. A row with no value
// (no checkup yet, no validity date) always goes last, whichever way the column runs, so the people
// who have something to read stay on top.
func sortValue(e Employee, key string) (string, bool) {
	switch key {
	case sortName:
		return strings.ToUpper(e.Name), true
	case sortCompany:
		return strings.ToUpper(e.Company), e.Company != ""
	case sortCheckup:
		return e.CheckUpDate, e.CheckUpDate != "" // yyyy-mm-dd, so text order is date order
	case sortUntil:
		return e.ValidUntil, e.HasValidity
	case sortValidity:
		for i, s := range employeeStatuses {
			if s == e.Status {
				return fmt.Sprintf("%02d", i), true
			}
		}
	}
	return "", false
}

// sortEmployees returns a sorted copy (the caller's list is not touched). Equal rows fall back to
// name, then id, so the order does not shuffle between two requests.
func sortEmployees(list []Employee, key, dir string) []Employee {
	out := append([]Employee(nil), list...)
	sort.SliceStable(out, func(i, j int) bool {
		a, aok := sortValue(out[i], key)
		b, bok := sortValue(out[j], key)
		switch {
		case aok != bok:
			return aok
		case aok && a != b:
			if dir == dirDesc {
				return a > b
			}
			return a < b
		}
		if an, bn := strings.ToUpper(out[i].Name), strings.ToUpper(out[j].Name); an != bn {
			return an < bn
		}
		return out[i].PatientID < out[j].PatientID
	})
	return out
}

// employeesLink is the page address for the filters now chosen with some words replaced; an empty
// replacement removes that word.
func employeesLink(filter, set map[string]string) string {
	q := url.Values{}
	for k, v := range filter {
		if v != "" {
			q.Set(k, v)
		}
	}
	for k, v := range set {
		if v == "" {
			q.Del(k)
		} else {
			q.Set(k, v)
		}
	}
	if len(q) == 0 {
		return basePath + "/employees"
	}
	return basePath + "/employees?" + q.Encode()
}

// sortLinks gives each sortable column the address a click on its heading goes to: the same filters,
// that column, and the opposite direction when it is the one already in use. aria is the aria-sort
// word of the column in use, which the stylesheet also turns into the arrow.
func sortLinks(filter map[string]string, activeKey, activeDir string) (urls, aria map[string]string) {
	urls, aria = map[string]string{}, map[string]string{}
	for _, key := range employeeSortKeys {
		dir := firstDir(key)
		if key == activeKey {
			dir = oppositeDir(activeDir)
			aria[key] = "ascending"
			if activeDir == dirDesc {
				aria[key] = "descending"
			}
		}
		urls[key] = employeesLink(filter, map[string]string{"sort": key, "dir": dir})
	}
	return urls, aria
}

// StatusTile is one clickable validity label with its count. Clicking the one in use clears the
// filter, so the same tile turns it on and off.
type StatusTile struct {
	Label, Slug, URL string
	Count            int
	On               bool
}

// statusTiles builds the tiles in the order HR thinks of them. A status nobody has is left out, as
// the chips were before, unless it is the one in use (so it can still be switched off).
func statusTiles(counts map[string]int, filter map[string]string, active string) []StatusTile {
	var tiles []StatusTile
	for _, s := range employeeStatuses {
		if counts[s] == 0 && s != active {
			continue
		}
		next := s
		if s == active {
			next = ""
		}
		tiles = append(tiles, StatusTile{
			Label: s, Slug: Employee{Status: s}.StatusSlug(), Count: counts[s], On: s == active,
			URL: employeesLink(filter, map[string]string{"status": next}),
		})
	}
	return tiles
}
