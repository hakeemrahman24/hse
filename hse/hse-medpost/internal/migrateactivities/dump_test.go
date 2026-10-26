package migrateactivities

import "testing"

func TestParseDumpUsesCreateColumnOrder(t *testing.T) {
	sql := `
CREATE TABLE ` + "`EvidenceTable`" + ` (
  ` + "`EvidenceID`" + ` int(11) NOT NULL,
  ` + "`FK_IDActivity`" + ` int(11) NOT NULL,
  ` + "`EvidenceType`" + ` enum('AttendanceList','ReportAttachment','Photos') NOT NULL,
  ` + "`FileLink`" + ` varchar(500) DEFAULT NULL,
  ` + "`UploadDate`" + ` char(10) DEFAULT NULL,
  ` + "`FK_StaffID`" + ` int(11) DEFAULT NULL
) ENGINE=InnoDB;
INSERT INTO ` + "`EvidenceTable`" + ` VALUES
(1,2,'Photos','/uploads/a.jpg','2026-09-10',NULL),
(2,2,'ReportAttachment','/uploads/b.pdf','2026-09-11',3);
`
	dump, err := ParseDump(sql)
	if err != nil {
		t.Fatal(err)
	}
	rows := dump.Tables["EvidenceTable"]
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	if got := rows[0].Text("EvidenceType"); got != "Photos" {
		t.Fatalf("type = %q", got)
	}
	if !rows[0].Null("FK_StaffID") {
		t.Fatal("expected null staff id")
	}
	if got, err := rows[1].Int("FK_StaffID"); err != nil || got != 3 {
		t.Fatalf("staff = %d, %v", got, err)
	}
}

func TestParseDumpUnescapesMySQLStrings(t *testing.T) {
	sql := `
CREATE TABLE ` + "`ActivityTable`" + ` (
  ` + "`ActivityID`" + ` int(11) NOT NULL,
  ` + "`ActivityTitle`" + ` varchar(200) NOT NULL,
  ` + "`ActivitySummary`" + ` text
) ENGINE=InnoDB;
INSERT INTO ` + "`ActivityTable`" + ` VALUES
(2,'O\'Brien','Line one\r\nLine two');
`
	dump, err := ParseDump(sql)
	if err != nil {
		t.Fatal(err)
	}
	row := dump.Tables["ActivityTable"][0]
	if got := row.Text("ActivityTitle"); got != "O'Brien" {
		t.Fatalf("title = %q", got)
	}
	if got := row.Text("ActivitySummary"); got != "Line one\r\nLine two" {
		t.Fatalf("summary = %q", got)
	}
}
