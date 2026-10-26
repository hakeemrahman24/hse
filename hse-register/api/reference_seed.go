package main

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The seed files in migrations/seed hold the starting lookup lists. They are read once, on the
// first start after migration 007, and only while the table is still empty, so later edits made
// in the database are never overwritten.

type hazardSeed struct{ Class, Name string }

type topicSeed struct {
	Order          int
	Name, Kind     string
	Group          string
	HazardName     string
	Label          string // v8: the Indonesian wording the SOC reporter sees
	HighPotential  bool
	LifeSavingRule string
}

// picklistSeed is one answer of a SOC form question (plan v8: SOC_Form_Picklists_v8.csv).
type picklistSeed struct {
	List       string
	Order      int
	Label      string
	ControlTag string
	UsedInPath string
	Note       string
}

type keywordSeed struct {
	Topic, Keyword, MatchType string
	Weight                    int
	Active                    bool
	Source                    string
	PPETopic                  string // hazard topic a PPE keyword implies (v8), "" for none
}

// defaultAreas is the starting list for the Area dropdown; administrators edit it in Settings.
var defaultAreas = []string{"PCC / TCF", "Camp", "Main / Access Road", "Parking Area", "Workshop", "Jetty", "Other / Lainnya"}

// readCSV returns the rows of a CSV as maps from header name to value.
func readCSV(r io.Reader, required ...string) ([]map[string]string, error) {
	cr := csv.NewReader(r)
	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	for i := range header {
		header[i] = strings.TrimSpace(strings.TrimPrefix(header[i], "\uFEFF"))
	}
	have := map[string]bool{}
	for _, h := range header {
		have[h] = true
	}
	for _, want := range required {
		if !have[want] {
			return nil, fmt.Errorf("missing column %q", want)
		}
	}
	var out []map[string]string
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		row := make(map[string]string, len(header))
		for i, h := range header {
			if i < len(rec) {
				row[h] = strings.TrimSpace(rec[i])
			}
		}
		out = append(out, row)
	}
}

func parseHazardSeed(r io.Reader) ([]hazardSeed, error) {
	rows, err := readCSV(r, "hazard_class", "hazard_category")
	if err != nil {
		return nil, err
	}
	out := make([]hazardSeed, 0, len(rows))
	for i, row := range rows {
		if row["hazard_class"] == "" || row["hazard_category"] == "" {
			return nil, fmt.Errorf("hazard row %d is incomplete", i+2)
		}
		out = append(out, hazardSeed{Class: row["hazard_class"], Name: row["hazard_category"]})
	}
	return out, nil
}

func parseTopicSeed(r io.Reader) ([]topicSeed, error) {
	rows, err := readCSV(r, "sort_order", "topic", "kind", "suggested_hazard_category")
	if err != nil {
		return nil, err
	}
	out := make([]topicSeed, 0, len(rows))
	for i, row := range rows {
		order, err := strconv.Atoi(row["sort_order"])
		if err != nil || row["topic"] == "" {
			return nil, fmt.Errorf("topic row %d is invalid", i+2)
		}
		if row["kind"] != "hazard" && row["kind"] != "control" {
			return nil, fmt.Errorf("topic row %d has kind %q", i+2, row["kind"])
		}
		out = append(out, topicSeed{Order: order, Name: row["topic"], Kind: row["kind"],
			Group: row["topic_group"], HazardName: row["suggested_hazard_category"],
			Label: row["label_id"], HighPotential: row["high_potential"] == "Y", LifeSavingRule: row["life_saving_rule_draft"]})
	}
	return out, nil
}

func parsePicklistSeed(r io.Reader) ([]picklistSeed, error) {
	rows, err := readCSV(r, "list_name", "option_order", "label_id")
	if err != nil {
		return nil, err
	}
	out := make([]picklistSeed, 0, len(rows))
	for i, row := range rows {
		order, err := strconv.Atoi(row["option_order"])
		if err != nil || order < 1 || row["list_name"] == "" || row["label_id"] == "" {
			return nil, fmt.Errorf("picklist row %d is invalid", i+2)
		}
		out = append(out, picklistSeed{List: row["list_name"], Order: order, Label: row["label_id"],
			ControlTag: row["control_tag_set"], UsedInPath: row["used_in_path"], Note: row["note"]})
	}
	return out, nil
}

func parseKeywordSeed(r io.Reader) ([]keywordSeed, error) {
	rows, err := readCSV(r, "topic", "keyword", "match_type", "weight", "is_active")
	if err != nil {
		return nil, err
	}
	out := make([]keywordSeed, 0, len(rows))
	for i, row := range rows {
		w, err := strconv.Atoi(row["weight"])
		if err != nil || w < 1 || w > 255 || row["keyword"] == "" || row["topic"] == "" {
			return nil, fmt.Errorf("keyword row %d is invalid", i+2)
		}
		if row["match_type"] != "prefix" && row["match_type"] != "whole" {
			return nil, fmt.Errorf("keyword row %d has match type %q", i+2, row["match_type"])
		}
		out = append(out, keywordSeed{Topic: row["topic"], Keyword: row["keyword"], MatchType: row["match_type"],
			Weight: w, Active: row["is_active"] != "0", Source: row["source"], PPETopic: row["ppe_inferred_topic"]})
	}
	return out, nil
}

// seedReference fills the lookup tables from the CSV files in dir, each table only when it is
// empty. Everything is done in one transaction.
func seedReference(db *sql.DB, dir string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	empty := func(table string) (bool, error) {
		var n int
		err := tx.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n) // table names are constants below
		return n == 0, err
	}
	load := func(name string) (*os.File, error) { return os.Open(filepath.Join(dir, name)) }

	if ok, err := empty("ref_hazard_category"); err != nil {
		return fmt.Errorf("seed: %w (did you run migration 007?)", err)
	} else if ok {
		f, err := load("ref_hazard_seed.csv")
		if err != nil {
			return err
		}
		hazards, err := parseHazardSeed(f)
		f.Close()
		if err != nil {
			return fmt.Errorf("ref_hazard_seed.csv: %w", err)
		}
		for _, h := range hazards {
			if _, err := tx.Exec(`INSERT IGNORE INTO ref_hazard_category (hazard_class, name) VALUES (?, ?)`, h.Class, h.Name); err != nil {
				return err
			}
		}
	}

	if ok, err := empty("ref_topic"); err != nil {
		return err
	} else if ok {
		f, err := load("ref_soc_topic_seed.csv")
		if err != nil {
			return err
		}
		topics, err := parseTopicSeed(f)
		f.Close()
		if err != nil {
			return fmt.Errorf("ref_soc_topic_seed.csv: %w", err)
		}
		for _, t := range topics {
			var hazardID any // NULL when the topic has no suggested hazard
			if t.HazardName != "" {
				var id int64
				if err := tx.QueryRow(`SELECT id FROM ref_hazard_category WHERE name = ?`, t.HazardName).Scan(&id); err != nil {
					return fmt.Errorf("topic %q: unknown hazard %q: %w", t.Name, t.HazardName, err)
				}
				hazardID = id
			}
			if _, err := tx.Exec(`INSERT IGNORE INTO ref_topic (sort_order, name, kind, topic_group, hazard_id, label_id, high_potential, life_saving_rule)
				VALUES (?, ?, ?, NULLIF(?, ''), ?, NULLIF(?, ''), ?, NULLIF(?, ''))`,
				t.Order, t.Name, t.Kind, t.Group, hazardID, t.Label, t.HighPotential, t.LifeSavingRule); err != nil {
				return err
			}
		}
	}

	if ok, err := empty("ref_keyword"); err != nil {
		return err
	} else if ok {
		f, err := load("ref_keyword_seed.csv")
		if err != nil {
			return err
		}
		kws, err := parseKeywordSeed(f)
		f.Close()
		if err != nil {
			return fmt.Errorf("ref_keyword_seed.csv: %w", err)
		}
		for _, k := range kws {
			var topicID int64
			if err := tx.QueryRow(`SELECT id FROM ref_topic WHERE name = ?`, k.Topic).Scan(&topicID); err != nil {
				return fmt.Errorf("keyword %q: unknown topic %q: %w", k.Keyword, k.Topic, err)
			}
			if _, err := tx.Exec(`INSERT IGNORE INTO ref_keyword (topic_id, keyword, match_type, weight, is_active, source) VALUES (?, ?, ?, ?, ?, NULLIF(?, ''))`,
				topicID, k.Keyword, k.MatchType, k.Weight, k.Active, k.Source); err != nil {
				return err
			}
		}
	}

	if err := backfillControlClasses(tx, dir); err != nil {
		return err
	}
	if err := backfillTopicLabels(tx, dir); err != nil {
		return err
	}
	if err := backfillPPETopics(tx, dir); err != nil {
		return err
	}
	if err := seedPicklists(tx, dir); err != nil {
		return err
	}

	if ok, err := empty("ref_area"); err != nil {
		return err
	} else if ok {
		for _, a := range defaultAreas {
			if _, err := tx.Exec(`INSERT IGNORE INTO ref_area (name) VALUES (?)`, a); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// backfillControlClasses gives the control topics (PPE, Permit To Work, ...) their class from the
// seed file when they have none yet, so a database seeded before the classes existed gets them on
// the next start without a manual migration. A class that is already set is never overwritten.
func backfillControlClasses(tx *sql.Tx, dir string) error {
	f, err := os.Open(filepath.Join(dir, "ref_soc_topic_seed.csv"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	topics, err := parseTopicSeed(f)
	f.Close()
	if err != nil {
		return fmt.Errorf("ref_soc_topic_seed.csv: %w", err)
	}
	for _, t := range topics {
		if t.Kind != "control" || t.Group == "" {
			continue
		}
		if _, err := tx.Exec(`UPDATE ref_topic SET topic_group = ? WHERE name = ? AND kind = 'control' AND topic_group IS NULL`, t.Group, t.Name); err != nil {
			return err
		}
	}
	return nil
}

// backfillTopicLabels gives topics seeded before v8 their Indonesian label, high-potential flag and
// draft Life-Saving Rule. A topic that already has a label is left alone, so a value edited in the
// database is never overwritten (the three always travel together).
func backfillTopicLabels(tx *sql.Tx, dir string) error {
	f, err := os.Open(filepath.Join(dir, "ref_soc_topic_seed.csv"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	topics, err := parseTopicSeed(f)
	f.Close()
	if err != nil {
		return fmt.Errorf("ref_soc_topic_seed.csv: %w", err)
	}
	for _, t := range topics {
		if t.Label == "" {
			continue
		}
		if _, err := tx.Exec(`UPDATE ref_topic SET label_id = ?, high_potential = ?, life_saving_rule = NULLIF(?, '')
			WHERE name = ? AND label_id IS NULL`, t.Label, t.HighPotential, t.LifeSavingRule, t.Name); err != nil {
			return fmt.Errorf("topic labels: %w (did you run migration 015?)", err)
		}
	}
	return nil
}

// seedPicklists fills ref_picklist from the seed file while the table is empty.
func seedPicklists(tx *sql.Tx, dir string) error {
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM ref_picklist`).Scan(&n); err != nil {
		return fmt.Errorf("picklists: %w (did you run migration 015?)", err)
	}
	if n > 0 {
		return nil
	}
	f, err := os.Open(filepath.Join(dir, "ref_picklist_seed.csv"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	items, err := parsePicklistSeed(f)
	f.Close()
	if err != nil {
		return fmt.Errorf("ref_picklist_seed.csv: %w", err)
	}
	for _, it := range items {
		if _, err := tx.Exec(`INSERT IGNORE INTO ref_picklist (list_name, option_order, label_id, control_tag, used_in_path, note)
			VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''))`,
			it.List, it.Order, it.Label, it.ControlTag, it.UsedInPath, it.Note); err != nil {
			return err
		}
	}
	return nil
}

// backfillPPETopics sets the hazard a PPE keyword implies, for databases seeded before v8 as well as
// new ones. A keyword that already has one is left alone, so a value edited in the database stays.
func backfillPPETopics(tx *sql.Tx, dir string) error {
	f, err := os.Open(filepath.Join(dir, "ref_keyword_seed.csv"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	kws, err := parseKeywordSeed(f)
	f.Close()
	if err != nil {
		return fmt.Errorf("ref_keyword_seed.csv: %w", err)
	}
	for _, k := range kws {
		if k.PPETopic == "" {
			continue
		}
		if _, err := tx.Exec(`UPDATE ref_keyword k JOIN ref_topic t ON t.id = k.topic_id
			SET k.ppe_topic_id = (SELECT id FROM ref_topic WHERE name = ?)
			WHERE t.name = ? AND k.keyword = ? AND k.ppe_topic_id IS NULL`, k.PPETopic, k.Topic, k.Keyword); err != nil {
			return fmt.Errorf("ppe topics: %w (did you run migration 016?)", err)
		}
	}
	return nil
}
