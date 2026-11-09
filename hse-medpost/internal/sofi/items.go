package sofi

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The administrator can reword the 20 questionnaire items in Settings (and see the form as workers
// see it). Only the words change: which dimension an item belongs to, how many items there are and
// how they are scored stay in scoring.go, so no wording change can alter a score. Wording is kept in
// SofiItem as a difference from the built-in text, so deleting a row (or blanking a field) brings the
// built-in text back and a missing table just means "built-in wording everywhere".

const maxItemTextRunes = 300

// itemTexts is one item's wording in the three languages of /sofi.
type itemTexts struct {
	EN string `json:"en"`
	ID string `json:"id"`
	ZH string `json:"zh"`
}

// itemOverrides reads every changed item, keyed by item number. A missing table is logged once and
// treated as "nothing changed".
func (a *API) itemOverrides() map[int]itemTexts {
	out := map[int]itemTexts{}
	rows, err := a.DB.Query(`SELECT ItemNumber, TextEN, TextID, TextZH FROM SofiItem`)
	if err != nil {
		if !a.itemsWarned.Swap(true) {
			log.Printf("sofi: item wording unreadable, using the built-in wording (run migrations/mysql/040_sofi_items.sql?): %v", err)
		}
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var n int
		var t itemTexts
		if err := rows.Scan(&n, &t.EN, &t.ID, &t.ZH); err != nil {
			log.Printf("sofi: item wording row: %v", err)
			continue
		}
		out[n] = t
	}
	if err := rows.Err(); err != nil {
		log.Printf("sofi: item wording rows: %v", err)
	}
	return out
}

// items is the questionnaire as workers see it: the built-in items with the administrator's wording
// laid over them, language by language. The result is a copy, safe to change.
func (a *API) items() []Item {
	over := a.itemOverrides()
	out := make([]Item, len(Items))
	copy(out, Items)
	for i := range out {
		t, ok := over[out[i].Number]
		if !ok {
			continue
		}
		if t.EN != "" {
			out[i].EN = t.EN
		}
		if t.ID != "" {
			out[i].ID = t.ID
		}
		if t.ZH != "" {
			out[i].ZH = t.ZH
		}
	}
	return out
}

type itemView struct {
	Number    int    `json:"number"`
	Dimension string `json:"dimension"`
	itemTexts
	Default itemTexts `json:"default"`
	Custom  bool      `json:"custom"` // some language differs from the built-in wording
}

// AdminItems is GET /api/sofi-items: the questionnaire for the Settings preview and editor, with the
// built-in wording beside the current one. Administrator only.
func (a *API) AdminItems(w http.ResponseWriter, r *http.Request) {
	current := a.items()
	items := make([]itemView, len(Items))
	for i, it := range Items {
		now := itemTexts{EN: current[i].EN, ID: current[i].ID, ZH: current[i].ZH}
		def := itemTexts{EN: it.EN, ID: it.ID, ZH: it.ZH}
		items[i] = itemView{Number: it.Number, Dimension: it.Dimension, itemTexts: now, Default: def, Custom: now != def}
	}
	dims := make([]map[string]string, len(Dimensions))
	for i, d := range Dimensions {
		dims[i] = map[string]string{"key": d.Key, "en": d.EN, "id": d.ID, "zh": d.ZH}
	}
	say := func(key string) itemTexts {
		return itemTexts{EN: tr(LangEN, key), ID: tr(LangID, key), ZH: tr(LangZH, key)}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"scale":        scalePoints,
		"instruction":  say("How much does each statement describe how you feel? Choose 0 to 6."),
		"anchorLow":    say("Not at all"),
		"anchorHigh":   say("To a very large extent"),
		"dimensions":   dims,
		"items":        items,
		"maxTextRunes": maxItemTextRunes,
	})
}

// cleanItemText trims a typed wording and refuses anything that is not one plain line of text.
func cleanItemText(s string) (string, error) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > maxItemTextRunes {
		return "", fmt.Errorf("Keep each wording under %d characters.", maxItemTextRunes)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("Wording must be a single line of plain text.")
		}
	}
	return s, nil
}

// AdminPutItem is PUT /api/sofi-items/{number} {"en","id","zh"}: sets the wording of one item. A blank
// language, or a text equal to the built-in one, goes back to the built-in wording; when all three
// are built-in the row is removed. Administrator only.
func (a *API) AdminPutItem(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("number"))
	if err != nil || n < 1 || n > ItemCount {
		staffError(w, http.StatusNotFound, "No such item.")
		return
	}
	var in itemTexts
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&in); err != nil {
		staffError(w, http.StatusBadRequest, "Send the wording as {\"en\", \"id\", \"zh\"}.")
		return
	}
	def := Items[n-1]
	var set itemTexts
	for _, f := range []struct {
		in, builtIn string
		out         *string
	}{{in.EN, def.EN, &set.EN}, {in.ID, def.ID, &set.ID}, {in.ZH, def.ZH, &set.ZH}} {
		clean, err := cleanItemText(f.in)
		if err != nil {
			staffError(w, http.StatusBadRequest, err.Error())
			return
		}
		if clean != f.builtIn {
			*f.out = clean
		}
	}
	if err := a.saveItem(n, set, staffName(r)); err != nil {
		log.Printf("sofi: save item %d: %v", n, err)
		staffError(w, http.StatusInternalServerError, "Could not save the wording.")
		return
	}
	log.Printf("sofi: item %d reworded by %s", n, staffName(r))
	a.AdminItems(w, r)
}

// saveItem stores one item's changed wording, or removes the row when nothing differs. Delete then
// insert in one transaction, because "upsert" is spelled differently on SQLite and MariaDB.
func (a *API) saveItem(n int, t itemTexts, by string) error {
	tx, err := a.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM SofiItem WHERE ItemNumber = ?`, n); err != nil {
		return err
	}
	if t != (itemTexts{}) {
		if _, err := tx.Exec(`INSERT INTO SofiItem (ItemNumber, TextEN, TextID, TextZH, UpdatedBy, UpdatedAt) VALUES (?, ?, ?, ?, ?, ?)`,
			n, t.EN, t.ID, t.ZH, by, a.now().UTC().Format(timeLayoutDB)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
