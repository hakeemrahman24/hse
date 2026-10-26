package entrymcu

import (
	"testing"

	"hse-medpost/internal/db"
)

func TestEnsureBloodTypeColumnBackfillsLatestCheckUpOnly(t *testing.T) {
	database := openExportTestDB(t)
	if _, err := database.Exec(`ALTER TABLE McuRecord DROP COLUMN BloodType`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO Patient (Name, DateOfBirth, Gender, BloodType) VALUES
		('Ani Test', '1990-01-02', 'F', 'O+'),
		('Budi Test', '1991-03-04', 'M', '')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO McuRecord (FK_PatientID, Timestamp, ReviewDate, CheckUpID) VALUES
		(1, '2025-01-01T00:00:00', '2025-01-01', 'OLD1'),
		(1, '2026-01-01T00:00:00', '2026-01-01', 'NEW1'),
		(2, '2026-01-01T00:00:00', '2026-01-01', 'BUDI1')`); err != nil {
		t.Fatal(err)
	}

	if err := EnsureBloodTypeColumn(database); err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	rows, err := database.Query(`SELECT CheckUpID, COALESCE(BloodType,'') FROM McuRecord`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, blood string
		if err := rows.Scan(&id, &blood); err != nil {
			t.Fatal(err)
		}
		got[id] = blood
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if got["NEW1"] != "O+" {
		t.Fatalf("latest checkup blood = %q, want O+", got["NEW1"])
	}
	if got["OLD1"] != "" || got["BUDI1"] != "" {
		t.Fatalf("only the latest checkup should get the value, got %v", got)
	}
}

func TestEnsureBloodTypeColumnDoesNotRefillAfterColumnExists(t *testing.T) {
	database := openExportTestDB(t)
	if _, err := database.Exec(`INSERT INTO Patient (Name, DateOfBirth, Gender, BloodType) VALUES ('Ani Test', '1990-01-02', 'F', 'O+')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO McuRecord (FK_PatientID, Timestamp, ReviewDate, CheckUpID, BloodType) VALUES (1, '2026-01-01T00:00:00', '2026-01-01', 'NEW1', NULL)`); err != nil {
		t.Fatal(err)
	}

	if err := EnsureBloodTypeColumn(database); err != nil {
		t.Fatal(err)
	}

	var blood string
	if err := database.QueryRow(`SELECT COALESCE(BloodType,'') FROM McuRecord WHERE CheckUpID = 'NEW1'`).Scan(&blood); err != nil {
		t.Fatal(err)
	}
	if blood != "" {
		t.Fatalf("re-running the migration refilled a cleared value: %q", blood)
	}
}

func TestLatestBloodTypeSQLSkipsEmptyAndPicksNewest(t *testing.T) {
	database := openExportTestDB(t)
	if _, err := database.Exec(`INSERT INTO Patient (Name, DateOfBirth, Gender) VALUES ('Ani Test', '1990-01-02', 'F')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO McuRecord (FK_PatientID, Timestamp, ReviewDate, CheckUpID, BloodType) VALUES
		(1, '2024-01-01T00:00:00', '2024-01-01', 'OLD1', 'O+'),
		(1, '2025-01-01T00:00:00', '2025-01-01', 'MID1', 'B-'),
		(1, '2026-01-01T00:00:00', '2026-01-01', 'NEW1', '')`); err != nil {
		t.Fatal(err)
	}

	var blood string
	query := `SELECT COALESCE(` + db.LatestBloodTypeSQL("p") + `,'') FROM Patient p WHERE p.Name = 'Ani Test'`
	if err := database.QueryRow(query).Scan(&blood); err != nil {
		t.Fatal(err)
	}
	if blood != "B-" {
		t.Fatalf("latest non-empty blood type = %q, want B-", blood)
	}
}
