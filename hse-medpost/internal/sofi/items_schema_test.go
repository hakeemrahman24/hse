package sofi

import (
	"context"
	"net/http"
	"testing"
)

// The server fills both tables with the built-in dimensions and items; every item carries the
// dimension its position belongs to, and a second start leaves the administrator's wording alone.
func TestQuestionnaireTablesAreSeededOnceAndNeverOverwritten(t *testing.T) {
	api := newTestAPI(t)
	var dims, items int
	if err := api.DB.QueryRow(`SELECT COUNT(*) FROM SofiDimension`).Scan(&dims); err != nil || dims != 5 {
		t.Fatalf("dimensions = %d %v", dims, err)
	}
	if err := api.DB.QueryRow(`SELECT COUNT(*) FROM SofiItem`).Scan(&items); err != nil || items != ItemCount {
		t.Fatalf("items = %d %v", items, err)
	}
	for _, it := range Items {
		var key string
		if err := api.DB.QueryRow(`SELECT DimensionKey FROM SofiItem WHERE ItemNumber = ?`, it.Number).Scan(&key); err != nil || key != it.Dimension {
			t.Fatalf("item %d dimension = %q %v, want %q", it.Number, key, err, it.Dimension)
		}
	}
	if rec := putItem(api, "3", `{"en":"Kept after restart","id":"","zh":""}`); rec.Code != http.StatusOK {
		t.Fatalf("save: %d", rec.Code)
	}
	if err := api.seedQuestionnaire(); err != nil {
		t.Fatal(err)
	}
	if got := storedItemText(t, api, 3); got != "Kept after restart" {
		t.Fatalf("seeding overwrote the wording: %q", got)
	}
}

// Every item must point at a real dimension: with foreign keys on, an item with an unknown
// dimension is refused, and a dimension that still has items cannot be deleted. SQLite needs the
// pragma switched on per connection (MariaDB always enforces it), so this uses one connection.
func TestItemForeignKeyToItsDimension(t *testing.T) {
	api := newTestAPI(t)
	ctx := context.Background()
	conn, err := api.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO SofiItem (ItemNumber, DimensionKey, TextEN, TextID, TextZH, UpdatedAt) VALUES (99, 'no_such_dimension', 'x', 'x', 'x', '2026-01-01T00:00:00')`); err == nil {
		t.Error("an item with an unknown dimension was accepted")
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM SofiDimension WHERE DimensionKey = 'sleepiness'`); err == nil {
		t.Error("a dimension that still has items was deleted")
	}
}

// A SQLite database that has the first version of the tables (changed wording only, no foreign key)
// gets them dropped so the complete tables can be created; the new shape is left alone.
func TestOldQuestionnaireTablesAreReplaced(t *testing.T) {
	api := newBareTestAPI(t)
	if err := DropOldQuestionnaireTables(api.DB); err != nil {
		t.Fatal(err)
	}
	if got := storedItemText(t, api, 1); got != Items[0].EN {
		t.Fatalf("the new tables were dropped or emptied: %q", got)
	}
	// Put the old shape in its place (a throwaway test database).
	for _, stmt := range []string{
		`DROP TABLE SofiItem`,
		`DROP TABLE SofiDimension`,
		`CREATE TABLE SofiItem (ItemNumber INTEGER PRIMARY KEY, TextEN TEXT NOT NULL DEFAULT '', UpdatedAt TEXT NOT NULL)`,
		`CREATE TABLE SofiDimension (DimensionKey TEXT PRIMARY KEY, NameEN TEXT NOT NULL DEFAULT '', UpdatedAt TEXT NOT NULL)`,
	} {
		if _, err := api.DB.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := DropOldQuestionnaireTables(api.DB); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := api.DB.QueryRow(`SELECT COUNT(*) FROM SofiItem`).Scan(&n); err == nil {
		t.Fatalf("the old SofiItem table is still there (%d rows)", n)
	}
}
