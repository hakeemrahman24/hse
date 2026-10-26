package entrymcu

import (
	"testing"
)

func TestReplaceWorkLocationNameSwapsOneNameInList(t *testing.T) {
	got, changed := replaceWorkLocationName("Site A; Site B", "Site A", "Site C")
	if !changed || got != "Site C; Site B" {
		t.Fatalf("got %q changed=%v", got, changed)
	}
}

func TestReplaceWorkLocationNameDropsDuplicateAfterRename(t *testing.T) {
	got, changed := replaceWorkLocationName("Site A; Site B", "Site A", "Site B")
	if !changed || got != "Site B" {
		t.Fatalf("got %q changed=%v", got, changed)
	}
}

func TestReplaceWorkLocationNameIgnoresPartialMatch(t *testing.T) {
	got, changed := replaceWorkLocationName("Site AB", "Site A", "Site C")
	if changed || got != "Site AB" {
		t.Fatalf("got %q changed=%v", got, changed)
	}
}

func TestWorkLocationRenameAndInUseFollowMultiValueRows(t *testing.T) {
	database := openLookupDB(t)
	if _, err := database.Exec(`CREATE TABLE McuRecord (CheckUpID INTEGER PRIMARY KEY, WorkLocation TEXT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO McuRecord (CheckUpID, WorkLocation) VALUES
		(1, 'Site A'), (2, 'Site A; Site B'), (3, 'Site AB'), (4, NULL)`); err != nil {
		t.Fatal(err)
	}

	n, err := workLocationInUse(database, "Site A")
	if err != nil || n != 2 {
		t.Fatalf("in use %d err %v, want 2", n, err)
	}

	if err := renameWorkLocationInRecords(database, "Site A", "Site C"); err != nil {
		t.Fatal(err)
	}
	var got1, got2, got3 string
	database.QueryRow(`SELECT WorkLocation FROM McuRecord WHERE CheckUpID = 1`).Scan(&got1)
	database.QueryRow(`SELECT WorkLocation FROM McuRecord WHERE CheckUpID = 2`).Scan(&got2)
	database.QueryRow(`SELECT WorkLocation FROM McuRecord WHERE CheckUpID = 3`).Scan(&got3)
	if got1 != "Site C" || got2 != "Site C; Site B" || got3 != "Site AB" {
		t.Fatalf("after rename: %q %q %q", got1, got2, got3)
	}

	n, err = workLocationInUse(database, "Site A")
	if err != nil || n != 0 {
		t.Fatalf("in use after rename %d err %v, want 0", n, err)
	}
}
