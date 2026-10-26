package migrateactivities

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"hse-medpost/internal/db"
)

func TestRunPreservesIDsAndRewritesFiles(t *testing.T) {
	root := t.TempDir()
	srcUploads := filepath.Join(root, "src")
	writeFile(t, filepath.Join(srcUploads, "MalariaManagement", "activities", "Monitoring-Inspection", "Photos", "a.jpg"), "photo")
	writeFile(t, filepath.Join(srcUploads, "_ClinicWide", "documents", "Template", "form.pdf"), "template")
	writeFile(t, filepath.Join(srcUploads, "_SubHeaders", "entries", "7", "Document", "log.pdf"), "log")

	dumpPath := filepath.Join(root, "dump.sql")
	if err := os.WriteFile(dumpPath, []byte(fixtureDump), 0o644); err != nil {
		t.Fatal(err)
	}
	destDB := filepath.Join(root, "out.db")
	destUploads := filepath.Join(root, "uploads")
	result, err := Run(Options{
		DumpPath:      dumpPath,
		SrcUploads:    srcUploads,
		SrcUploadRoot: "/home/orangepi/projects/medical-activities/uploads",
		DestDB:        destDB,
		DestUploads:   destUploads,
		MigrationsDir: filepath.Join("..", "..", "migrations", "sqlite"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Activities != 2 || result.Evidence != 2 || result.Documents != 1 || result.Staff != 1 {
		t.Fatalf("counts = %+v", result)
	}

	database, err := db.Open("sqlite", destDB+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	var summary string
	if err := database.QueryRow(`SELECT ActivitySummary FROM ActivityTable WHERE ActivityID = 2`).Scan(&summary); err != nil {
		t.Fatal(err)
	}
	if summary != "Line one\r\nLine two" {
		t.Fatalf("summary = %q", summary)
	}
	var foldedID int64
	var subID sql.NullInt64
	var activityType string
	if err := database.QueryRow(`
		SELECT ActivityID, FK_SubHeaderID, ActivityType FROM ActivityTable WHERE ActivityTitle = 'Waste log'`,
	).Scan(&foldedID, &subID, &activityType); err != nil {
		t.Fatal(err)
	}
	if foldedID != 3 || !subID.Valid || subID.Int64 != 4 || activityType != "Monitoring/Inspection" {
		t.Fatalf("folded activity = %d sub=%v type=%s", foldedID, subID, activityType)
	}
	var etype, fileLink string
	if err := database.QueryRow(`SELECT EvidenceType, FileLink FROM EvidenceTable WHERE EvidenceID = 1`).Scan(&etype, &fileLink); err != nil {
		t.Fatal(err)
	}
	photo := filepath.Join(destUploads, "MalariaManagement", "activities", "Monitoring-Inspection", "Photos", "a.jpg")
	if etype != "Photos" || fileLink != photo {
		t.Fatalf("photo link = %s %s", etype, fileLink)
	}
	if err := database.QueryRow(`SELECT EvidenceType, FileLink FROM EvidenceTable WHERE FK_IDActivity = ?`, foldedID).Scan(&etype, &fileLink); err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(destUploads, "_SubHeaders", "entries", "7", "Document", "log.pdf")
	if etype != "ReportAttachment" || fileLink != kept {
		t.Fatalf("folded evidence = %s %s", etype, fileLink)
	}
	if _, err := os.Stat(filepath.Join(destUploads, "_MedpostWide", "documents", "Template", "form.pdf")); err != nil {
		t.Fatal(err)
	}
	var hash string
	if err := database.QueryRow(`SELECT PasswordHash FROM AppUserTable WHERE Username = 'admin'`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash != "hash-admin" {
		t.Fatalf("hash = %q", hash)
	}
	var seq int64
	if err := database.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name = 'ActivityTable'`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	if seq != 3 {
		t.Fatalf("activity sequence = %d", seq)
	}
}

func TestRunRejectsMissingSourceFile(t *testing.T) {
	root := t.TempDir()
	srcUploads := filepath.Join(root, "src")
	if err := os.MkdirAll(srcUploads, 0o755); err != nil {
		t.Fatal(err)
	}
	dumpPath := filepath.Join(root, "dump.sql")
	if err := os.WriteFile(dumpPath, []byte(fixtureDump), 0o644); err != nil {
		t.Fatal(err)
	}
	destDB := filepath.Join(root, "out.db")
	_, err := Run(Options{
		DumpPath:      dumpPath,
		SrcUploads:    srcUploads,
		SrcUploadRoot: "/home/orangepi/projects/medical-activities/uploads",
		DestDB:        destDB,
		DestUploads:   filepath.Join(root, "uploads"),
		MigrationsDir: filepath.Join("..", "..", "migrations", "sqlite"),
	})
	if err == nil {
		t.Fatal("expected missing file error")
	}
	if _, statErr := os.Stat(destDB); !os.IsNotExist(statErr) {
		t.Fatal("destination database was left behind")
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const fixtureDump = `
CREATE TABLE ` + "`HealthProcedureTable`" + ` (
  ` + "`ProcID`" + ` int(11) NOT NULL,
  ` + "`HealthProcedure`" + ` varchar(100) NOT NULL,
  ` + "`Status`" + ` enum('Active','Archived') NOT NULL
) ENGINE=InnoDB;
INSERT INTO ` + "`HealthProcedureTable`" + ` VALUES
(1,'MalariaManagement','Active'),
(10,'First Aid Post Management','Active');

CREATE TABLE ` + "`ClinicStaffTable`" + ` (
  ` + "`StaffID`" + ` int(11) NOT NULL,
  ` + "`StaffName`" + ` varchar(100) NOT NULL,
  ` + "`Position`" + ` varchar(100) DEFAULT NULL,
  ` + "`Status`" + ` enum('Active','NotActive') NOT NULL
) ENGINE=InnoDB;
INSERT INTO ` + "`ClinicStaffTable`" + ` VALUES
(1,'Administrator','Admin','Active');

CREATE TABLE ` + "`PositionRoleTable`" + ` (
  ` + "`PositionID`" + ` int(11) NOT NULL,
  ` + "`PositionName`" + ` varchar(100) NOT NULL,
  ` + "`Role`" + ` enum('admin','manager','staff') NOT NULL
) ENGINE=InnoDB;
INSERT INTO ` + "`PositionRoleTable`" + ` VALUES
(1,'Administrator','admin');

CREATE TABLE ` + "`AppUserTable`" + ` (
  ` + "`UserID`" + ` int(11) NOT NULL,
  ` + "`FK_StaffID`" + ` int(11) NOT NULL,
  ` + "`Username`" + ` varchar(100) NOT NULL,
  ` + "`PasswordHash`" + ` varchar(255) NOT NULL,
  ` + "`Role`" + ` enum('admin','manager','staff') NOT NULL
) ENGINE=InnoDB;
INSERT INTO ` + "`AppUserTable`" + ` VALUES
(1,1,'admin','hash-admin','admin');

CREATE TABLE ` + "`SupervisorTable`" + ` (
  ` + "`SupervisorID`" + ` int(11) NOT NULL,
  ` + "`FK_ProcID`" + ` int(11) NOT NULL,
  ` + "`FK_StaffID`" + ` int(11) NOT NULL,
  ` + "`StartDate`" + ` char(10) DEFAULT NULL,
  ` + "`EndDate`" + ` char(10) DEFAULT NULL
) ENGINE=InnoDB;

CREATE TABLE ` + "`SubHeaderTable`" + ` (
  ` + "`SubHeaderID`" + ` int(11) NOT NULL,
  ` + "`FK_ProcID`" + ` int(11) NOT NULL,
  ` + "`SubHeaderName`" + ` varchar(150) NOT NULL,
  ` + "`Status`" + ` enum('Active','Archived') NOT NULL
) ENGINE=InnoDB;
INSERT INTO ` + "`SubHeaderTable`" + ` VALUES
(4,10,'Medical Waste Log','Active');

CREATE TABLE ` + "`ActivityTable`" + ` (
  ` + "`ActivityID`" + ` int(11) NOT NULL,
  ` + "`FK_ProcID`" + ` int(11) NOT NULL,
  ` + "`FK_StaffID`" + ` int(11) NOT NULL,
  ` + "`ActivityType`" + ` enum('Monitoring/Inspection','Socialization') NOT NULL,
  ` + "`ActivityTitle`" + ` varchar(200) NOT NULL,
  ` + "`ActivitySummary`" + ` text,
  ` + "`Location`" + ` varchar(150) DEFAULT NULL,
  ` + "`ActivityDate`" + ` char(10) DEFAULT NULL,
  ` + "`Notes`" + ` text
) ENGINE=InnoDB;
INSERT INTO ` + "`ActivityTable`" + ` VALUES
(2,1,1,'Monitoring/Inspection','Gate check','Line one\r\nLine two','Clinic','2026-09-07',NULL);

CREATE TABLE ` + "`EvidenceTable`" + ` (
  ` + "`EvidenceID`" + ` int(11) NOT NULL,
  ` + "`FK_IDActivity`" + ` int(11) NOT NULL,
  ` + "`EvidenceType`" + ` enum('AttendanceList','ReportAttachment','Photos') NOT NULL,
  ` + "`FileLink`" + ` varchar(500) DEFAULT NULL,
  ` + "`UploadDate`" + ` char(10) DEFAULT NULL,
  ` + "`FK_StaffID`" + ` int(11) DEFAULT NULL
) ENGINE=InnoDB;
INSERT INTO ` + "`EvidenceTable`" + ` VALUES
(1,2,'Photos','/home/orangepi/projects/medical-activities/uploads/MalariaManagement/activities/Monitoring-Inspection/Photos/a.jpg','2026-09-10',NULL);

CREATE TABLE ` + "`DocumentTable`" + ` (
  ` + "`DocumentID`" + ` int(11) NOT NULL,
  ` + "`FK_ProcID`" + ` int(11) DEFAULT NULL,
  ` + "`DocumentsType`" + ` enum('Procedure','SocializationMaterial','Forms','Template') NOT NULL,
  ` + "`DocumentTitle`" + ` varchar(200) NOT NULL,
  ` + "`FormLink`" + ` varchar(500) DEFAULT NULL,
  ` + "`FileLink`" + ` varchar(500) DEFAULT NULL,
  ` + "`Notes`" + ` text
) ENGINE=InnoDB;
INSERT INTO ` + "`DocumentTable`" + ` VALUES
(1,NULL,'Template','Attendance form',NULL,'/home/orangepi/projects/medical-activities/uploads/_ClinicWide/documents/Template/form.pdf',NULL);

CREATE TABLE ` + "`SubHeaderEntryTable`" + ` (
  ` + "`EntryID`" + ` int(11) NOT NULL,
  ` + "`FK_SubHeaderID`" + ` int(11) NOT NULL,
  ` + "`FK_StaffID`" + ` int(11) NOT NULL,
  ` + "`EntryDate`" + ` char(10) DEFAULT NULL,
  ` + "`Title`" + ` varchar(200) NOT NULL,
  ` + "`Description`" + ` text,
  ` + "`FormLink`" + ` varchar(500) DEFAULT NULL
) ENGINE=InnoDB;
INSERT INTO ` + "`SubHeaderEntryTable`" + ` VALUES
(7,4,1,'2026-09-18','Waste log','Monthly log',NULL);

CREATE TABLE ` + "`EntryEvidenceTable`" + ` (
  ` + "`EvidenceID`" + ` int(11) NOT NULL,
  ` + "`FK_EntryID`" + ` int(11) NOT NULL,
  ` + "`FK_StaffID`" + ` int(11) DEFAULT NULL,
  ` + "`EvidenceType`" + ` enum('Photos','Document') NOT NULL,
  ` + "`FileLink`" + ` varchar(500) NOT NULL,
  ` + "`UploadDate`" + ` char(10) DEFAULT NULL
) ENGINE=InnoDB;
INSERT INTO ` + "`EntryEvidenceTable`" + ` VALUES
(9,7,1,'Document','/home/orangepi/projects/medical-activities/uploads/_SubHeaders/entries/7/Document/log.pdf','2026-09-18');
`
