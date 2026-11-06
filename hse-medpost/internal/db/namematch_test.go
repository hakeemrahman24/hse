package db

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestNameLikeIgnoresApostrophes(t *testing.T) {
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(`CREATE TABLE p (Name TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"As'adul", "O’Connor", "Budi Santoso"} {
		if _, err := conn.Exec(`INSERT INTO p VALUES (?)`, n); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		typed string
		want  int
	}{
		{"asadul", 1}, {"as'adul", 1}, {"as’adul", 1}, {"AS‘ADUL", 1},
		{"oconnor", 1}, {"o'connor", 1}, {"o’connor", 1},
		{"budi", 1}, {"nobody", 0}, {"", 3},
	}
	for _, c := range cases {
		var n int
		q := `SELECT COUNT(*) FROM p WHERE ` + NameLikeSQL("Name")
		if err := conn.QueryRow(q, NameLikeArg(c.typed)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != c.want {
			t.Errorf("typed %q: got %d rows, want %d", c.typed, n, c.want)
		}
	}
}
