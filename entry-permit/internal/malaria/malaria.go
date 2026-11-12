// Package malaria edits the malaria endemicity list.
//
// WHY not the generic list editor: the regency and province must be exactly
// the ones hse-medpost serves (the permit forms take the address from the same
// source, so a later lookup by address only works if the spelling is
// identical). Here the admin never types a regency: the page lists the
// regencies from the API and the admin sets the endemicity of each one, which
// is also how a regency is added to the list. The values are stored in the
// normal reference-list table, so history, archive and audit work as for the
// other lists.
//
// Renames: each item also stores the API's regency code. When medpost renames
// a regency (or moves it to another province) the code still finds it, and the
// stored name and province are updated to the new ones (an audit row records
// it). An item that matches nothing, by code or by name, is an "orphan": it is
// shown to the admin to re-link or archive, never hidden.
package malaria

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"entry-permit/internal/reflist"
	"entry-permit/internal/region"
)

// Endemicities are the allowed levels. "" means the regency is not on the list.
var Endemicities = []string{"Rendah", "Sedang", "Tinggi"}

// systemActor is written to the audit log for automatic rename fixes.
const systemActor = "system"

// Value positions in a stored malaria item (the column order of the list).
const (
	vLevel = iota
	vRegency
	vProvince
	vCode
)

// Errors.
var (
	ErrNoSource      = errors.New("The region list from hse-medpost is not available. Check that hse-medpost is running and the connection settings are filled in.")
	ErrUnknownRegion = errors.New("That regency is not in the region list from hse-medpost.")
	ErrBadEndemicity = errors.New("Endemicity must be Not listed, Rendah, Sedang or Tinggi.")
	ErrNotOrphan     = errors.New("That item is not in the 'no longer in the region list' section.")
	ErrAlreadyListed = errors.New("That regency is already on the list. Archive this one instead.")
)

// Row is one regency with its current endemicity ("" = not listed).
type Row struct {
	Code, Regency, Province, Endemicity string
}

// Orphan is a listed regency that medpost's data no longer has, by code or by name.
type Orphan struct {
	ItemID                              int64
	Regency, Province, Endemicity, Code string
}

// Filter narrows the rows. OnlyListed hides regencies without an endemicity.
type Filter struct {
	Province, Q string
	OnlyListed  bool
}

// Page is everything the admin page shows. Regencies is filled only when there
// are orphans, because it feeds their re-link dropdown.
type Page struct {
	Rows      []Row
	Provinces []string
	Orphans   []Orphan
	Regencies []region.Regency
}

// Service reads the regency list from Regions and keeps the levels in Lists.
type Service struct {
	Lists   *reflist.Store
	Regions region.Source
}

func (s *Service) def() reflist.Def {
	d, _ := reflist.Find("malaria") // defined in reflist.Defs, covered by a test
	return d
}

func itemKey(regency, province string) string {
	return region.Key(regency) + "|" + strings.ToLower(strings.TrimSpace(province))
}

func (s *Service) regencies(ctx context.Context) ([]region.Regency, error) {
	if s.Regions == nil {
		return nil, ErrNoSource
	}
	list, err := s.Regions.Regencies(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w (%v)", ErrNoSource, err)
	}
	return list, nil
}

// snapshot is the list joined with the API's regencies.
type snapshot struct {
	api         []region.Regency
	apiByCode   map[string]region.Regency
	byKey       map[string]reflist.Item // stored items by the API's name+province
	orphans     []Orphan
	orphanItems map[int64]reflist.Item
}

func (sn *snapshot) addOrphan(it reflist.Item) {
	if it.Archived {
		return // an archived item that matches nothing is just history
	}
	sn.orphans = append(sn.orphans, Orphan{
		ItemID: it.ID, Regency: it.Values[vRegency], Province: it.Values[vProvince],
		Endemicity: it.Values[vLevel], Code: it.Values[vCode],
	})
	sn.orphanItems[it.ID] = it
}

// snapshot loads the API list and the stored items and matches them: by code
// first (survives renames), then by name+province (finds items that have no
// code yet). With heal on, a matched item whose stored name, province or code
// differs from the API's is updated to match, which is the rename fix.
func (s *Service) snapshot(ctx context.Context, heal bool) (*snapshot, error) {
	api, err := s.regencies(ctx)
	if err != nil {
		return nil, err
	}
	items, err := s.Lists.Items(ctx, s.def(), true)
	if err != nil {
		return nil, err
	}
	sn := &snapshot{api: api, apiByCode: map[string]region.Regency{}, byKey: map[string]reflist.Item{}, orphanItems: map[int64]reflist.Item{}}
	apiByKey := map[string]region.Regency{}
	for _, rg := range api {
		sn.apiByCode[rg.Code] = rg
		apiByKey[itemKey(rg.Name, rg.Province)] = rg
	}
	def := s.def()
	for _, it := range items {
		target, ok := sn.apiByCode[it.Values[vCode]]
		if !ok || it.Values[vCode] == "" {
			target, ok = apiByKey[itemKey(it.Values[vRegency], it.Values[vProvince])]
		}
		if !ok {
			sn.addOrphan(it)
			continue
		}
		stale := it.Values[vRegency] != target.Name || it.Values[vProvince] != target.Province || it.Values[vCode] != target.Code
		if stale && heal {
			updated := []string{it.Values[vLevel], target.Name, target.Province, target.Code}
			switch err := s.Lists.Update(ctx, systemActor, def, it.ID, updated); {
			case err == nil:
				it.Values = updated
			case errors.Is(err, reflist.ErrDuplicate):
				// Another item already holds the new name: leave this one for the admin.
				sn.addOrphan(it)
				continue
			default:
				return nil, err
			}
		}
		sn.byKey[itemKey(target.Name, target.Province)] = it
	}
	return sn, nil
}

// Page returns the regencies that match f, the province names for the filter,
// and the orphans. Viewing it also applies any pending rename fixes.
func (s *Service) Page(ctx context.Context, f Filter) (Page, error) {
	sn, err := s.snapshot(ctx, true)
	if err != nil {
		return Page{}, err
	}
	seen := map[string]bool{}
	var page Page
	q := strings.ToLower(strings.TrimSpace(f.Q))
	for _, rg := range sn.api {
		if !seen[rg.Province] {
			seen[rg.Province] = true
			page.Provinces = append(page.Provinces, rg.Province)
		}
		if f.Province != "" && rg.Province != f.Province {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(rg.Name), q) {
			continue
		}
		level := ""
		if it, ok := sn.byKey[itemKey(rg.Name, rg.Province)]; ok && !it.Archived {
			level = it.Values[vLevel]
		}
		if f.OnlyListed && level == "" {
			continue
		}
		page.Rows = append(page.Rows, Row{Code: rg.Code, Regency: rg.Name, Province: rg.Province, Endemicity: level})
	}
	sort.Strings(page.Provinces)
	page.Orphans = sn.orphans
	if len(sn.orphans) > 0 {
		page.Regencies = sn.api
	}
	return page, nil
}

// OrphanCount counts the orphans without changing anything (for the overview).
func (s *Service) OrphanCount(ctx context.Context) (int, error) {
	sn, err := s.snapshot(ctx, false)
	if err != nil {
		return 0, err
	}
	return len(sn.orphans), nil
}

// Set changes one regency's endemicity. "" takes it off the list (archived,
// never deleted). A regency not yet on the list is added. The regency is
// identified by its API code, so nothing typed by hand can enter the list.
func (s *Service) Set(ctx context.Context, actor, code, endemicity string) error {
	endemicity, err := cleanLevel(endemicity)
	if err != nil {
		return err
	}
	sn, err := s.snapshot(ctx, true)
	if err != nil {
		return err
	}
	rg, ok := sn.apiByCode[code]
	if !ok {
		return ErrUnknownRegion
	}
	def := s.def()
	it, exists := sn.byKey[itemKey(rg.Name, rg.Province)]
	switch {
	case !exists && endemicity == "":
		return nil // nothing to remove
	case !exists:
		_, err = s.Lists.Add(ctx, actor, def, []string{endemicity, rg.Name, rg.Province, rg.Code})
		return err
	case endemicity == "":
		if it.Archived {
			return nil
		}
		return s.Lists.SetArchived(ctx, actor, def, it.ID, true)
	}
	if it.Archived {
		if err := s.Lists.SetArchived(ctx, actor, def, it.ID, false); err != nil {
			return err
		}
	}
	if it.Values[vLevel] == endemicity {
		return nil
	}
	return s.Lists.Update(ctx, actor, def, it.ID, []string{endemicity, rg.Name, rg.Province, rg.Code})
}

func cleanLevel(level string) (string, error) {
	level = strings.TrimSpace(level)
	if level == "" {
		return "", nil
	}
	for _, e := range Endemicities {
		if strings.EqualFold(e, level) {
			return e, nil
		}
	}
	return "", ErrBadEndemicity
}

// Relink points an orphan at a current regency, carrying its endemicity over.
// If the target is already listed the admin must archive the orphan instead; if
// the target was listed before and archived, it is brought back with the
// orphan's level and the orphan is archived.
func (s *Service) Relink(ctx context.Context, actor string, itemID int64, code string) error {
	sn, err := s.snapshot(ctx, true)
	if err != nil {
		return err
	}
	orphan, ok := sn.orphanItems[itemID]
	if !ok {
		return ErrNotOrphan
	}
	rg, ok := sn.apiByCode[code]
	if !ok {
		return ErrUnknownRegion
	}
	def := s.def()
	level := orphan.Values[vLevel]
	if target, listed := sn.byKey[itemKey(rg.Name, rg.Province)]; listed {
		if !target.Archived {
			return ErrAlreadyListed
		}
		if err := s.Lists.SetArchived(ctx, actor, def, target.ID, false); err != nil {
			return err
		}
		if target.Values[vLevel] != level {
			if err := s.Lists.Update(ctx, actor, def, target.ID, []string{level, rg.Name, rg.Province, rg.Code}); err != nil {
				return err
			}
		}
		return s.Lists.SetArchived(ctx, actor, def, orphan.ID, true)
	}
	return s.Lists.Update(ctx, actor, def, orphan.ID, []string{level, rg.Name, rg.Province, rg.Code})
}

// ArchiveOrphan takes an orphan off the list (archived, never deleted).
func (s *Service) ArchiveOrphan(ctx context.Context, actor string, itemID int64) error {
	sn, err := s.snapshot(ctx, true)
	if err != nil {
		return err
	}
	if _, ok := sn.orphanItems[itemID]; !ok {
		return ErrNotOrphan
	}
	return s.Lists.SetArchived(ctx, actor, s.def(), itemID, true)
}

// Lookup returns the endemicity of a regency ("" = not listed). Permit rules
// call it with the address the requester picked from the same API: the code
// is tried first, so a renamed regency is still found before the page has
// healed the stored name.
func (s *Service) Lookup(ctx context.Context, code, regency, province string) (string, error) {
	items, err := s.Lists.Items(ctx, s.def(), false)
	if err != nil {
		return "", err
	}
	want := itemKey(regency, province)
	for _, it := range items {
		if code != "" && it.Values[vCode] == code {
			return it.Values[vLevel], nil
		}
	}
	for _, it := range items {
		if itemKey(it.Values[vRegency], it.Values[vProvince]) == want {
			return it.Values[vLevel], nil
		}
	}
	return "", nil
}
