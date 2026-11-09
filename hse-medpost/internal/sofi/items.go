package sofi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The administrator can reword the 20 questionnaire items and rename the 5 dimensions in Settings (and
// see the form as workers see it). Only the words change: which dimension an item belongs to, how many
// items there are and how they are scored stay in scoring.go, so no wording change can alter a score.
//
// The words live in two complete tables, SofiDimension (5 rows) and SofiItem (20 rows, each with a
// foreign key to its dimension). The server fills them at start-up from the built-in text and never
// overwrites a row that is already there. A blank field falls back to the built-in text and a missing
// table just means "built-in wording everywhere", so /sofi never breaks over them.

const maxItemTextRunes = 300

// itemTexts is one item's wording, or one dimension's name, in the three languages of /sofi.
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
			log.Printf("sofi: item wording unreadable, using the built-in wording (run migrations/mysql/040_sofi_dimensions.sql and 041_sofi_items.sql?): %v", err)
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

type dimensionView struct {
	Key string `json:"key"`
	itemTexts
	Default itemTexts `json:"default"`
	Custom  bool      `json:"custom"`
	Items   []int     `json:"items"` // numbers of the items in this dimension
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
	// Each dimension with its current names, the built-in names, and the numbers of its items, so the
	// editor can list items under their dimension and offer "use built-in names".
	shownDims := a.dimensions()
	dims := make([]dimensionView, len(Dimensions))
	for i, d := range Dimensions {
		now := itemTexts{EN: shownDims[i].EN, ID: shownDims[i].ID, ZH: shownDims[i].ZH}
		def := itemTexts{EN: d.EN, ID: d.ID, ZH: d.ZH}
		dims[i] = dimensionView{Key: d.Key, itemTexts: now, Default: def, Custom: now != def}
		for _, it := range Items {
			if it.Dimension == d.Key {
				dims[i].Items = append(dims[i].Items, it.Number)
			}
		}
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
		if clean == "" {
			clean = f.builtIn // blank means "use the built-in text"
		}
		*f.out = clean
	}
	if err := a.saveItem(n, set, staffName(r)); err != nil {
		log.Printf("sofi: save item %d: %v", n, err)
		staffError(w, http.StatusInternalServerError, "Could not save the wording.")
		return
	}
	log.Printf("sofi: item %d reworded by %s", n, staffName(r))
	a.AdminItems(w, r)
}

// saveItem stores the wording of one item. The row always exists once the server has started
// (seedQuestionnaire); if it does not yet, it is created with the item's own dimension.
func (a *API) saveItem(n int, t itemTexts, by string) error {
	if err := a.seedQuestionnaire(); err != nil {
		return err
	}
	now := a.now().UTC().Format(timeLayoutDB)
	_, err := a.DB.Exec(`UPDATE SofiItem SET TextEN = ?, TextID = ?, TextZH = ?, UpdatedBy = ?, UpdatedAt = ? WHERE ItemNumber = ?`,
		t.EN, t.ID, t.ZH, by, now, n)
	return err
}

// dimensionOverrides reads every renamed dimension, keyed by dimension key. A missing table is logged
// once and treated as "nothing renamed".
func (a *API) dimensionOverrides() map[string]itemTexts {
	out := map[string]itemTexts{}
	rows, err := a.DB.Query(`SELECT DimensionKey, NameEN, NameID, NameZH FROM SofiDimension`)
	if err != nil {
		if !a.dimsWarned.Swap(true) {
			log.Printf("sofi: dimension names unreadable, using the built-in names (run migrations/mysql/040_sofi_dimensions.sql?): %v", err)
		}
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var t itemTexts
		if err := rows.Scan(&key, &t.EN, &t.ID, &t.ZH); err != nil {
			log.Printf("sofi: dimension name row: %v", err)
			continue
		}
		out[key] = t
	}
	if err := rows.Err(); err != nil {
		log.Printf("sofi: dimension name rows: %v", err)
	}
	return out
}

// dimensions is the five dimensions with the administrator's names laid over the built-in ones. The
// result is a copy, safe to change.
func (a *API) dimensions() []Dimension {
	over := a.dimensionOverrides()
	out := make([]Dimension, len(Dimensions))
	copy(out, Dimensions)
	for i := range out {
		t, ok := over[out[i].Key]
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

// named gives a scored result the current dimension names. Score itself works from the built-in
// names, so every place that shows a result passes it through here (dims is read once per request).
func named(res Result, dims []Dimension) Result {
	shown := make([]DimensionResult, len(res.Dimensions))
	copy(shown, res.Dimensions)
	for i := range shown {
		if i < len(dims) && dims[i].Key == shown[i].Key {
			shown[i].EN, shown[i].ID, shown[i].ZH = dims[i].EN, dims[i].ID, dims[i].ZH
		}
	}
	res.Dimensions = shown
	return res
}

// AdminPutDimension is PUT /api/sofi-dimensions/{key} {"en","id","zh"}: renames one dimension, with the
// same rules as AdminPutItem (blank or built-in goes back to the built-in name). Administrator only.
func (a *API) AdminPutDimension(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	var def *Dimension
	for i := range Dimensions {
		if Dimensions[i].Key == key {
			def = &Dimensions[i]
		}
	}
	if def == nil {
		staffError(w, http.StatusNotFound, "No such dimension.")
		return
	}
	var in itemTexts
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&in); err != nil {
		staffError(w, http.StatusBadRequest, "Send the name as {\"en\", \"id\", \"zh\"}.")
		return
	}
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
		if clean == "" {
			clean = f.builtIn // blank means "use the built-in text"
		}
		*f.out = clean
	}
	if err := a.saveDimension(key, set, staffName(r)); err != nil {
		log.Printf("sofi: save dimension %s: %v", key, err)
		staffError(w, http.StatusInternalServerError, "Could not save the name.")
		return
	}
	log.Printf("sofi: dimension %s renamed by %s", key, staffName(r))
	a.AdminItems(w, r)
}

// saveDimension stores the names of one dimension (the row exists once the server has started).
func (a *API) saveDimension(key string, t itemTexts, by string) error {
	if err := a.seedQuestionnaire(); err != nil {
		return err
	}
	_, err := a.DB.Exec(`UPDATE SofiDimension SET NameEN = ?, NameID = ?, NameZH = ?, UpdatedBy = ?, UpdatedAt = ? WHERE DimensionKey = ?`,
		t.EN, t.ID, t.ZH, by, a.now().UTC().Format(timeLayoutDB), key)
	return err
}

// seedQuestionnaire adds the built-in dimensions and items to SofiDimension and SofiItem when they are
// missing, dimensions first because every item points at one. A row that exists is left alone, so the
// administrator's wording survives every restart. Selecting first and inserting after, rather than
// "INSERT IGNORE", keeps one spelling for SQLite and MariaDB. Safe to call often.
func (a *API) seedQuestionnaire() error {
	now := a.now().UTC().Format(timeLayoutDB)
	for i, d := range Dimensions {
		var have int
		if err := a.DB.QueryRow(`SELECT COUNT(*) FROM SofiDimension WHERE DimensionKey = ?`, d.Key).Scan(&have); err != nil {
			return fmt.Errorf("check dimension %s: %w", d.Key, err)
		}
		if have > 0 {
			continue
		}
		if _, err := a.DB.Exec(`INSERT INTO SofiDimension (DimensionKey, SortOrder, NameEN, NameID, NameZH, UpdatedBy, UpdatedAt) VALUES (?, ?, ?, ?, ?, '', ?)`,
			d.Key, i+1, d.EN, d.ID, d.ZH, now); err != nil {
			return fmt.Errorf("add dimension %s: %w", d.Key, err)
		}
	}
	for _, it := range Items {
		var have int
		if err := a.DB.QueryRow(`SELECT COUNT(*) FROM SofiItem WHERE ItemNumber = ?`, it.Number).Scan(&have); err != nil {
			return fmt.Errorf("check item %d: %w", it.Number, err)
		}
		if have > 0 {
			continue
		}
		if _, err := a.DB.Exec(`INSERT INTO SofiItem (ItemNumber, DimensionKey, TextEN, TextID, TextZH, UpdatedBy, UpdatedAt) VALUES (?, ?, ?, ?, ?, '', ?)`,
			it.Number, it.Dimension, it.EN, it.ID, it.ZH, now); err != nil {
			return fmt.Errorf("add item %d: %w", it.Number, err)
		}
	}
	return nil
}

// DropOldQuestionnaireTables removes the first version of SofiItem and SofiDimension from a SQLite
// database that has it: those tables held only changed wording and had no foreign key. They are
// replaced by the complete tables (migrations 026 and 027). Call it before the SQLite migrations.
// Nothing happens on a database that has the new shape or no such tables.
func DropOldQuestionnaireTables(database *sql.DB) error {
	var dimensionKey string
	err := database.QueryRow(`SELECT DimensionKey FROM SofiItem LIMIT 1`).Scan(&dimensionKey)
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		return nil // the new shape
	}
	if !strings.Contains(err.Error(), "no such column") {
		return nil // no SofiItem table yet (or another problem the migrations will report)
	}
	for _, name := range []string{"SofiItem", "SofiDimension"} {
		if _, err := database.Exec(`DROP TABLE IF EXISTS ` + name); err != nil {
			return fmt.Errorf("drop old %s: %w", name, err)
		}
	}
	return nil
}
