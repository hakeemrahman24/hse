package main

import (
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	maxKeyword      = 100
	maxKeywordScore = 10
	keywordsPath    = "/admin/settings/keywords"
)

// keywordRow is one keyword of a topic.
type keywordRow struct {
	ID      int64
	TopicID int64
	Topic   string
	Keyword string
	Whole   bool // whole word(s) only; otherwise a word may start with the keyword
	Weight  int
	Active  bool
}

// topicOption is one hazard topic: a Topic dropdown choice, and a row of the overview table with
// the class and category it suggests and the keywords it has.
type topicOption struct {
	ID       int64
	Name     string
	Class    string
	Category string
	Keywords []keywordRow
}

// keywordInput is a validated add/save form.
type keywordInput struct {
	TopicID int64
	Keyword string
	Whole   bool
	Weight  int
}

// readKeyword validates the keyword form. The keyword is kept as typed; matching normalizes it the
// same way as the description (normalizeText), so it must contain at least one letter or digit.
func readKeyword(r *http.Request) (keywordInput, string) {
	var in keywordInput
	name, err := cleanListName(r.FormValue("keyword"), "keyword", maxKeyword)
	if err != nil {
		return in, err.Error()
	}
	if strings.TrimSpace(normalizeText(name)) == "" {
		return in, "The keyword needs at least one letter or digit."
	}
	in.Keyword = name
	in.TopicID, err = strconv.ParseInt(r.FormValue("topic"), 10, 64)
	if err != nil || in.TopicID < 1 {
		return in, "Choose the topic this keyword belongs to."
	}
	switch r.FormValue("match") {
	case "whole":
		in.Whole = true
	case "prefix":
	default:
		return in, "Choose how the keyword matches."
	}
	in.Weight, err = strconv.Atoi(r.FormValue("weight"))
	if err != nil || in.Weight < 1 || in.Weight > maxKeywordScore {
		return in, "The weight must be a number from 1 to " + strconv.Itoa(maxKeywordScore) + "."
	}
	return in, ""
}

func (h *hazardHandlers) topicOptions() ([]topicOption, error) {
	rs, err := h.db.Query(`SELECT t.id, t.name, COALESCE(c.hazard_class, ''), COALESCE(c.name, '')
		FROM ref_topic t LEFT JOIN ref_hazard_category c ON c.id = t.hazard_id
		WHERE t.kind = 'hazard' ORDER BY t.sort_order, t.id`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []topicOption
	for rs.Next() {
		var t topicOption
		if err := rs.Scan(&t.ID, &t.Name, &t.Class, &t.Category); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rs.Err()
}

func (h *hazardHandlers) allKeywords() ([]keywordRow, error) {
	rs, err := h.db.Query(`SELECT k.id, k.topic_id, t.name, k.keyword, k.match_type = 'whole', k.weight, k.is_active = 1
		FROM ref_keyword k JOIN ref_topic t ON t.id = k.topic_id
		WHERE t.kind = 'hazard' ORDER BY t.sort_order, t.id, k.keyword`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []keywordRow
	for rs.Next() {
		var k keywordRow
		if err := rs.Scan(&k.ID, &k.TopicID, &k.Topic, &k.Keyword, &k.Whole, &k.Weight, &k.Active); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rs.Err()
}

// keywordPage shows the page: forms on the left, on the right one table row per topic (class,
// category, keywords) and, for the topic opened with Manage (?topic=ID), its keywords one by one.
func (h *hazardHandlers) keywordPage(w http.ResponseWriter, r *http.Request, status int, msg, kind string) {
	filter, _ := strconv.ParseInt(r.URL.Query().Get("topic"), 10, 64)
	all, err := h.allKeywords()
	var topics []topicOption
	if err == nil {
		topics, err = h.topicOptions()
	}
	if err != nil {
		log.Printf("keywords: %v", err)
		http.Error(w, "Could not load the keywords.", http.StatusInternalServerError)
		return
	}
	byTopic := map[int64][]keywordRow{}
	for _, k := range all {
		byTopic[k.TopicID] = append(byTopic[k.TopicID], k)
	}
	var managed string
	for i := range topics {
		topics[i].Keywords = byTopic[topics[i].ID]
		if topics[i].ID == filter {
			managed = topics[i].Name
		}
	}
	data := PageData{
		Title: "Keywords", Heading: "Keywords", Active: "set-keywords",
		Subheading: "The words that suggest a topic and hazard while staff type an observation description.",
		Session:    h.auth.session(r), TopicRows: topics, TopicOptions: topics, ManageRows: byTopic[filter],
		ManageName: managed, TopicFilter: filter, EditID: editID(r), Flash: msg, FlashKind: kind,
	}
	for _, k := range all {
		if k.ID == data.EditID {
			data.EditKeyword = &k
			break
		}
	}
	if text := strings.TrimSpace(r.URL.Query().Get("try")); text != "" {
		data.TryText = text
		if snap, e := h.ref.Get(r.Context()); e == nil {
			data.TryResult = snap.Suggest(text)
		}
	}
	renderStatus(w, status, "settings_keywords", data)
}

func (h *hazardHandlers) keywordList(w http.ResponseWriter, r *http.Request) {
	msg, kind := doneMessage(r, map[string]string{
		"added": "Keyword added.", "saved": "Keyword saved.", "on": "Keyword turned on.",
		"off": "Keyword turned off.", "deleted": "Keyword deleted."})
	h.keywordPage(w, r, http.StatusOK, msg, kind)
}

// backTo returns to the page, keeping the topic that was open.
func backTo(r *http.Request, done string) string {
	q := url.Values{"done": {done}}
	if t := r.FormValue("filter"); t != "" {
		if _, err := strconv.Atoi(t); err == nil {
			q.Set("topic", t)
		}
	}
	return keywordsPath + "?" + q.Encode()
}

func matchType(whole bool) string {
	if whole {
		return "whole"
	}
	return "prefix"
}

func (h *hazardHandlers) keywordAdd(w http.ResponseWriter, r *http.Request) {
	in, msg := readKeyword(r)
	if msg != "" {
		h.keywordPage(w, r, http.StatusUnprocessableEntity, msg, "error")
		return
	}
	res, err := h.db.Exec(`INSERT INTO ref_keyword (topic_id, keyword, match_type, weight, is_active, source)
		SELECT t.id, ?, ?, ?, 1, 'settings' FROM ref_topic t WHERE t.id = ? AND t.kind = 'hazard'`,
		in.Keyword, matchType(in.Whole), in.Weight, in.TopicID)
	if err != nil {
		if isDuplicateKey(err) {
			h.keywordPage(w, r, http.StatusUnprocessableEntity, "That topic already has this keyword.", "error")
			return
		}
		h.fail(w, "keyword add", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		h.keywordPage(w, r, http.StatusUnprocessableEntity, "Choose the topic this keyword belongs to.", "error")
		return
	}
	h.ref.Invalidate()
	http.Redirect(w, r, backTo(r, "added"), http.StatusSeeOther)
}

func (h *hazardHandlers) keywordSave(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	in, msg := readKeyword(r)
	if msg != "" {
		h.keywordPage(w, r, http.StatusUnprocessableEntity, msg, "error")
		return
	}
	var topic int64
	if h.db.QueryRow(`SELECT id FROM ref_topic WHERE id = ? AND kind = 'hazard'`, in.TopicID).Scan(&topic) != nil {
		h.keywordPage(w, r, http.StatusUnprocessableEntity, "Choose the topic this keyword belongs to.", "error")
		return
	}
	if _, err := h.db.Exec(`UPDATE ref_keyword SET topic_id = ?, keyword = ?, match_type = ?, weight = ? WHERE id = ?`,
		topic, in.Keyword, matchType(in.Whole), in.Weight, id); err != nil {
		if isDuplicateKey(err) {
			h.keywordPage(w, r, http.StatusUnprocessableEntity, "That topic already has this keyword.", "error")
			return
		}
		h.fail(w, "keyword save", err)
		return
	}
	h.ref.Invalidate()
	http.Redirect(w, r, backTo(r, "saved"), http.StatusSeeOther)
}

func (h *hazardHandlers) setKeywordActive(w http.ResponseWriter, r *http.Request, active bool) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	flag, done := 0, "off"
	if active {
		flag, done = 1, "on"
	}
	if _, err := h.db.Exec(`UPDATE ref_keyword SET is_active = ? WHERE id = ?`, flag, id); err != nil {
		h.fail(w, "keyword toggle", err)
		return
	}
	h.ref.Invalidate()
	http.Redirect(w, r, backTo(r, done), http.StatusSeeOther)
}

func (h *hazardHandlers) keywordOn(w http.ResponseWriter, r *http.Request) {
	h.setKeywordActive(w, r, true)
}

func (h *hazardHandlers) keywordOff(w http.ResponseWriter, r *http.Request) {
	h.setKeywordActive(w, r, false)
}

// keywordDelete removes a keyword. Nothing else refers to it, so unlike a class or a source it
// can always be deleted; turn it off instead to keep it for later.
func (h *hazardHandlers) keywordDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	if _, err := h.db.Exec(`DELETE FROM ref_keyword WHERE id = ?`, id); err != nil {
		h.fail(w, "keyword delete", err)
		return
	}
	h.ref.Invalidate()
	http.Redirect(w, r, backTo(r, "deleted"), http.StatusSeeOther)
}
