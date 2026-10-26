package main

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func keywordForm(kv ...string) *httptest.ResponseRecorder {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	r := httptest.NewRequest("POST", "/admin/settings/keywords", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	in, msg := readKeyword(r)
	rec := httptest.NewRecorder()
	rec.WriteString(msg)
	if msg == "" {
		rec.WriteString("ok:" + in.Keyword + ":" + matchType(in.Whole))
	}
	return rec
}

func TestReadKeywordValidation(t *testing.T) {
	good := []string{"topic", "3", "keyword", "  ketinggian   kerja ", "match", "prefix", "weight", "2"}
	if got := keywordForm(good...).Body.String(); got != "ok:ketinggian kerja:prefix" {
		t.Errorf("good form = %q", got)
	}
	cases := []struct {
		name string
		kv   []string
		want string
	}{
		{"empty keyword", []string{"topic", "3", "keyword", " ", "match", "prefix", "weight", "1"}, "Enter a keyword"},
		{"only punctuation", []string{"topic", "3", "keyword", "---", "match", "prefix", "weight", "1"}, "letter or digit"},
		{"no topic", []string{"topic", "", "keyword", "las", "match", "whole", "weight", "1"}, "Choose the topic"},
		{"bad match", []string{"topic", "3", "keyword", "las", "match", "regex", "weight", "1"}, "how the keyword matches"},
		{"weight zero", []string{"topic", "3", "keyword", "las", "match", "whole", "weight", "0"}, "from 1 to 10"},
		{"weight huge", []string{"topic", "3", "keyword", "las", "match", "whole", "weight", "99"}, "from 1 to 10"},
		{"too long", []string{"topic", "3", "keyword", strings.Repeat("a", 101), "match", "whole", "weight", "1"}, "at most 100"},
	}
	for _, c := range cases {
		if got := keywordForm(c.kv...).Body.String(); !strings.Contains(got, c.want) {
			t.Errorf("%s: %q lacks %q", c.name, got, c.want)
		}
	}
}

func TestKeywordsPageShowsTopicTableAndManageRows(t *testing.T) {
	loadTestTemplates()
	kws := []keywordRow{
		{ID: 1, TopicID: 5, Topic: "Work at height", Keyword: "ketinggian", Weight: 2, Active: true},
		{ID: 2, TopicID: 5, Topic: "Work at height", Keyword: "las", Whole: true, Weight: 1},
	}
	topics := []topicOption{
		{ID: 5, Name: "Work at height", Class: "Safety", Category: "Falls", Keywords: kws},
		{ID: 6, Name: "Noise", Class: "Physical", Category: "Noise level"},
	}
	out := renderToString(t, "settings_keywords", PageData{Title: "k", Heading: "k", Active: "set-keywords", Zone: "settings",
		Session: &Session{Username: "arif", Role: roleAdmin}, TopicRows: topics, TopicOptions: topics,
		TopicFilter: 5, ManageName: "Work at height", ManageRows: kws, EditID: 2, EditKeyword: &kws[1]})
	for _, want := range []string{
		"<th>Class</th><th>Category</th><th>Topic</th><th>Keywords</th>", "Falls", "Noise level", "None yet",
		`class="kw-off"`, "keywords?topic=6", "keywords/1/off", "keywords/2/on", "keywords/2/delete",
		"<span>ketinggian</span>; <span class=\"kw-off\" title=\"Turned off\">las</span>", "keywords/2/save", `value="las"`, "Keywords of Work at height", "Add keyword", "Try a description",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	overview := renderToString(t, "settings_keywords", PageData{Title: "k", Heading: "k", Active: "set-keywords", Zone: "settings",
		Session: &Session{Username: "arif", Role: roleAdmin}, TopicRows: topics, TopicOptions: topics, TryText: "kerja di ketinggian",
		TryResult: suggestion{Topic: "Work at height", Hazard: "Falls"}})
	if strings.Contains(overview, "Keywords of") || !strings.Contains(overview, "Suggests <b>Work at height</b>") {
		t.Error("overview must hide the manage table and show the try result")
	}
}
