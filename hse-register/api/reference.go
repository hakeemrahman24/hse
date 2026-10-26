package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"
)

// refTopic is a hazard topic as offered on the forms. HazardID is the hazard suggested with it
// (0 = none).
type refTopic struct {
	ID       int64
	Name     string
	HazardID int64
	Control  bool // a control topic (PPE, Permit To Work, ...): it has no hazard category of its own
	// ControlClass is where a control topic sits in the hierarchy of controls (see controlClassOrder).
	ControlClass string
	// Label is the plain Indonesian wording the SOC reporter sees instead of the topic name (v8).
	// Empty until the label is loaded; use DisplayLabel.
	Label string
	// HighPotential marks a topic whose work could kill (v8): it drives the stop-work prompt.
	HighPotential bool
	// LifeSavingRule is the draft IOGP Life-Saving Rule of the topic ("" = none of the nine fits).
	LifeSavingRule string
}

// DisplayLabel is what the SOC form shows for the topic: the Indonesian label, or the topic name
// when no label has been loaded yet.
func (t refTopic) DisplayLabel() string {
	if t.Label != "" {
		return t.Label
	}
	return t.Name
}

// controlClassOrder is the hierarchy of controls, most effective first. Control topics are grouped
// under these in the sidebar and in the Topic dropdown, like hazard categories under their class.
var controlClassOrder = []string{"Elimination", "Substitution", "Engineering", "Administrative", "PPE"}

const controlClassOther = "Other" // a control topic whose class is not one of the above

// controlClassGroup is one class of the hierarchy with its topics (possibly none yet).
type controlClassGroup struct {
	Name   string
	Topics []refTopic
}

// ControlClasses lists every class of the hierarchy in order, each with its control topics, so a
// class that has no topic yet still shows. A topic with an unknown class goes under "Other", which
// appears only when there is one.
func (s *refSnapshot) ControlClasses() []controlClassGroup {
	at := map[string]int{}
	out := make([]controlClassGroup, 0, len(controlClassOrder)+1)
	for _, name := range controlClassOrder {
		at[name] = len(out)
		out = append(out, controlClassGroup{Name: name})
	}
	for _, t := range s.Topics {
		if !t.Control {
			continue
		}
		i, ok := at[t.ControlClass]
		if !ok {
			if _, seen := at[controlClassOther]; !seen {
				at[controlClassOther] = len(out)
				out = append(out, controlClassGroup{Name: controlClassOther})
			}
			i = at[controlClassOther]
		}
		out[i].Topics = append(out[i].Topics, t)
	}
	return out
}

type refHazard struct {
	ID       int64
	Class    string // one of the 7 workplace hazard classes
	Name     string
	Archived bool // the category, or its whole class, is archived: kept on old observations, not offered for new ones
}

// refSource is an observation source staff can choose (Internal Audit, External Audit, ...).
type refSource struct {
	ID       int64
	Name     string
	Archived bool
	Details  []refDetail // the types under this source, in list order
}

// refDetail is a type under a source (Site Audit under Internal Audit, ...).
type refDetail struct {
	ID       int64
	Name     string
	Archived bool
}

type hazardGroup struct {
	Class string
	Items []refHazard
}

// refSnapshot is everything the forms need from the lookup tables, read once and cached.
// picklistOption is one answer of a SOC form question (ref_picklist).
type picklistOption struct {
	Label      string // Indonesian wording shown on the form
	ControlTag string // the control topic this answer sets, "" for none
}

type refSnapshot struct {
	Picklists map[string][]picklistOption // list name -> options in order (unsafe_reason, ppe_item, ...)
	Topics    []refTopic                  // hazard topics in sort order
	Hazards   []refHazard                 // in table order, which groups them by class
	Areas     []string
	Sources   []refSource // staff sources in list order, archived ones included
	rules     []keywordRule
	SubAreas  map[string][]string // area name -> its sub-areas (the second level, edited in Settings)
	guard     map[string]bool     // words HSE added in Settings that typo tolerance must never correct

	topicByID  map[int64]refTopic
	hazardByID map[int64]refHazard
}

func newSnapshot(topics []refTopic, hazards []refHazard, areas []string, rules []keywordRule) *refSnapshot {
	s := &refSnapshot{Topics: topics, Hazards: hazards, Areas: areas, Sources: defaultSources(), rules: prepareRules(rules),
		topicByID: map[int64]refTopic{}, hazardByID: map[int64]refHazard{}}
	for _, t := range topics {
		s.topicByID[t.ID] = t
	}
	for _, h := range hazards {
		s.hazardByID[h.ID] = h
	}
	return s
}

// HazardGroups lists every hazard, archived ones included, under its class. The sidebar and the
// filters use it because old observations still carry archived hazards.
// topicChoice is a topic as the SOC form offers it: with the hazard category it maps to and that
// category's class, so the page can show the class without a second dropdown.
type topicChoice struct {
	ID            int64
	Name          string
	Label         string // the Indonesian wording the SOC reporter sees (v8)
	HighPotential bool   // work that could kill: the wizard asks the stop-work soft prompt
	HazardID      int64
	Class         string
}

// topicGroup is one heading of the Topic dropdown: the hazard topics, then the control topics.
type topicGroup struct {
	Kind  string // "Hazard" or "Control" (English; the page translates it)
	Class string // the control class ("Administrative", ...), empty for the hazard topics
	Items []topicChoice
}

// Label is the English heading of the group, for tests and logs.
func (g topicGroup) Label() string {
	if g.Class == "" {
		return g.Kind
	}
	return g.Kind + " · " + g.Class
}

// TopicGroups lists the topics for the SOC form: the hazard topics first, then one group per class
// of the hierarchy of controls that has topics ("Control · Administrative", ...).
func (s *refSnapshot) TopicGroups() []topicGroup {
	hazard := topicGroup{Kind: "Hazard"}
	for _, t := range s.Topics {
		if !t.Control {
			hazard.Items = append(hazard.Items, s.choice(t))
		}
	}
	var out []topicGroup
	if len(hazard.Items) > 0 {
		out = append(out, hazard)
	}
	for _, g := range s.ControlClasses() {
		if len(g.Topics) == 0 {
			continue
		}
		tg := topicGroup{Kind: "Control", Class: g.Name}
		for _, t := range g.Topics {
			tg.Items = append(tg.Items, s.choice(t))
		}
		out = append(out, tg)
	}
	return out
}

func (s *refSnapshot) choice(t refTopic) topicChoice {
	return topicChoice{ID: t.ID, Name: t.Name, Label: t.DisplayLabel(), HighPotential: t.HighPotential, HazardID: t.HazardID, Class: s.hazardByID[t.HazardID].Class}
}

// HazardClassOf is the class of a hazard category, or "" when the id is unknown.
func (s *refSnapshot) HazardClassOf(id int64) string { return s.hazardByID[id].Class }

func (s *refSnapshot) HazardGroups() []hazardGroup {
	return s.groups(func(refHazard) bool { return true })
}

// FormHazardGroups lists the hazards a form may offer: the archived ones are left out, except
// keep (the hazard an observation being edited already has), so editing an old post does not
// silently drop its hazard. keep 0 keeps nothing.
func (s *refSnapshot) FormHazardGroups(keep int64) []hazardGroup {
	return s.groups(func(h refHazard) bool { return !h.Archived || h.ID == keep })
}

// groups gathers the wanted hazards under their class. Classes come in the order of the first
// hazard that names them, so a category added later to an old class still lands under it.
func (s *refSnapshot) groups(want func(refHazard) bool) []hazardGroup {
	var out []hazardGroup
	at := map[string]int{}
	for _, h := range s.Hazards {
		if !want(h) {
			continue
		}
		i, ok := at[h.Class]
		if !ok {
			i = len(out)
			at[h.Class] = i
			out = append(out, hazardGroup{Class: h.Class})
		}
		out[i].Items = append(out[i].Items, h)
	}
	return out
}

// defaultSources is the starting source list: the fixed names with their default types, none
// archived. The database lists replace it once migrations 012 and 013 have run.
func defaultSources() []refSource {
	out := make([]refSource, len(staffSources))
	for i, n := range staffSources {
		out[i] = refSource{ID: int64(i + 1), Name: n}
		for j, d := range defaultSourceDetails[n] {
			out[i].Details = append(out[i].Details, refDetail{ID: int64(10*(i+1) + j), Name: d})
		}
	}
	return out
}

// DetailNames lists the types a form may offer under a source: the archived ones are left out,
// except keep (the type the observation being edited already has).
func (s *refSnapshot) DetailNames(source, keep string) []string {
	var out []string
	for _, src := range s.Sources {
		if src.Name != source {
			continue
		}
		for _, d := range src.Details {
			if !d.Archived || d.Name == keep {
				out = append(out, d.Name)
			}
		}
	}
	return out
}

// SourceDetailChoices lists every type the form may offer, each with its source, so the form can
// show only the types of the chosen source. keepSource/keepDetail are the pair the observation
// being edited already has.
func (s *refSnapshot) SourceDetailChoices(keepSource, keepDetail string) []sourceDetailChoice {
	var out []sourceDetailChoice
	for _, src := range s.SourceNames(keepSource) {
		keep := ""
		if src == keepSource {
			keep = keepDetail
		}
		for _, d := range s.DetailNames(src, keep) {
			out = append(out, sourceDetailChoice{Source: src, Detail: d})
		}
	}
	return out
}

// SourceNames lists the sources a form may offer: the archived ones are left out, except keep
// (the source the observation being edited already has).
func (s *refSnapshot) SourceNames(keep string) []string {
	var out []string
	for _, src := range s.Sources {
		if !src.Archived || src.Name == keep {
			out = append(out, src.Name)
		}
	}
	return out
}

// SourceAllowed reports whether name may be saved on an observation: it is in the list and not
// archived, or it is keep (the source the post already has).
func (s *refSnapshot) SourceAllowed(name, keep string) bool {
	for _, src := range s.Sources {
		if src.Name == name {
			return !src.Archived || name == keep
		}
	}
	return false
}

// KnownSource reports whether name is in the list, archived or not (used for list filters, since
// old observations keep archived sources).
func (s *refSnapshot) KnownSource(name string) bool {
	for _, src := range s.Sources {
		if src.Name == name {
			return true
		}
	}
	return false
}

// CanonicalSubArea returns the stored spelling of a sub-area of the given area, typed in any letter
// case, or false when the area has no such sub-area.
func (s *refSnapshot) CanonicalSubArea(area, name string) (string, bool) {
	name = strings.TrimSpace(name)
	for _, sub := range s.SubAreas[area] {
		if strings.EqualFold(sub, name) {
			return sub, true
		}
	}
	return "", false
}

// CanonicalArea returns the stored spelling of an area typed in any letter case, or false when
// the area is not in the list.
func (s *refSnapshot) CanonicalArea(name string) (string, bool) {
	name = strings.TrimSpace(name)
	for _, a := range s.Areas {
		if strings.EqualFold(a, name) {
			return a, true
		}
	}
	return "", false
}

// refData caches the snapshot for a few minutes. Settings calls Invalidate after an area change.
type refData struct {
	load func(ctx context.Context) (*refSnapshot, error)
	ttl  time.Duration

	mu   sync.Mutex
	snap *refSnapshot
	at   time.Time
}

const refCacheTTL = 5 * time.Minute

func newRefData(db *sql.DB) *refData {
	return &refData{load: func(ctx context.Context) (*refSnapshot, error) { return loadSnapshot(ctx, db) }, ttl: refCacheTTL}
}

// newStaticRef serves a fixed snapshot (tests and development without a database).
func newStaticRef(s *refSnapshot) *refData {
	return &refData{load: func(context.Context) (*refSnapshot, error) { return s, nil }, ttl: refCacheTTL}
}

// Get returns the cached snapshot, reloading when it is older than the cache time. If a reload
// fails, the older snapshot is still served so a short database problem does not break the forms.
func (r *refData) Get(ctx context.Context) (*refSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.snap != nil && time.Since(r.at) < r.ttl {
		return r.snap, nil
	}
	s, err := r.load(ctx)
	if err != nil {
		if r.snap != nil {
			return r.snap, nil
		}
		return nil, fmt.Errorf("load reference lists: %w", err)
	}
	r.snap, r.at = s, time.Now()
	return s, nil
}

// Invalidate drops the cached snapshot, so the next Get reads the tables again.
func (r *refData) Invalidate() {
	r.mu.Lock()
	r.snap = nil
	r.mu.Unlock()
}

func loadSnapshot(ctx context.Context, db *sql.DB) (*refSnapshot, error) {
	var hazards []refHazard
	rows, err := db.QueryContext(ctx, `SELECT h.id, h.hazard_class, h.name, h.is_archived = 1 OR COALESCE(c.is_archived, 0) = 1
		FROM ref_hazard_category h LEFT JOIN ref_hazard_class c ON c.name = h.hazard_class
		ORDER BY COALESCE(c.sort_order, 65535), h.id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var h refHazard
		if err := rows.Scan(&h.ID, &h.Class, &h.Name, &h.Archived); err != nil {
			rows.Close()
			return nil, err
		}
		hazards = append(hazards, h)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}

	var topics []refTopic
	rows, err = db.QueryContext(ctx, `SELECT id, name, COALESCE(hazard_id, 0), kind = 'control', COALESCE(topic_group, ''),
		COALESCE(label_id, ''), high_potential = 1, COALESCE(life_saving_rule, '') FROM ref_topic ORDER BY sort_order`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t refTopic
		if err := rows.Scan(&t.ID, &t.Name, &t.HazardID, &t.Control, &t.ControlClass, &t.Label, &t.HighPotential, &t.LifeSavingRule); err != nil {
			rows.Close()
			return nil, err
		}
		topics = append(topics, t)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}

	var rules []keywordRule
	rows, err = db.QueryContext(ctx, `SELECT k.id, k.topic_id, k.keyword, k.match_type = 'whole', k.weight, COALESCE(k.ppe_topic_id, 0)
		FROM ref_keyword k JOIN ref_topic t ON t.id = k.topic_id
		WHERE k.is_active = 1`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r keywordRule
		var kw string
		if err := rows.Scan(&r.ID, &r.TopicID, &kw, &r.Whole, &r.Weight, &r.PPETopicID); err != nil {
			rows.Close()
			return nil, err
		}
		r.Keyword = strings.TrimSpace(normalizeText(kw))
		rules = append(rules, r)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}

	var areas []string
	rows, err = db.QueryContext(ctx, `SELECT name FROM ref_area ORDER BY name`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			rows.Close()
			return nil, err
		}
		areas = append(areas, a)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	snap := newSnapshot(topics, hazards, areas, rules)

	snap.SubAreas = map[string][]string{}
	rows, err = db.QueryContext(ctx, `SELECT a.name, s.name FROM ref_subarea s JOIN ref_area a ON a.id = s.area_id ORDER BY a.name, s.name`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var area, sub string
		if err := rows.Scan(&area, &sub); err != nil {
			rows.Close()
			return nil, err
		}
		snap.SubAreas[area] = append(snap.SubAreas[area], sub)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}

	var sources []refSource
	rows, err = db.QueryContext(ctx, `SELECT id, name, is_archived = 1 FROM ref_source ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var src refSource
		if err := rows.Scan(&src.ID, &src.Name, &src.Archived); err != nil {
			rows.Close()
			return nil, err
		}
		sources = append(sources, src)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	byID := map[int64]int{}
	for i, src := range sources {
		byID[src.ID] = i
	}
	rows, err = db.QueryContext(ctx, `SELECT source_id, id, name, is_archived = 1 FROM ref_source_detail ORDER BY source_id, sort_order, id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var sid int64
		var d refDetail
		if err := rows.Scan(&sid, &d.ID, &d.Name, &d.Archived); err != nil {
			rows.Close()
			return nil, err
		}
		if i, ok := byID[sid]; ok {
			sources[i].Details = append(sources[i].Details, d)
		}
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	snap.Sources = sources
	snap.guard = loadGuardWords(ctx, db)

	snap.Picklists = map[string][]picklistOption{}
	rows, err = db.QueryContext(ctx, `SELECT list_name, label_id, COALESCE(control_tag, '') FROM ref_picklist ORDER BY list_name, option_order`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name string
		var o picklistOption
		if err := rows.Scan(&name, &o.Label, &o.ControlTag); err != nil {
			rows.Close()
			return nil, err
		}
		snap.Picklists[name] = append(snap.Picklists[name], o)
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	return snap, nil
}

func closeRows(rows *sql.Rows) error {
	err := rows.Err()
	if cerr := rows.Close(); err == nil {
		err = cerr
	}
	return err
}
