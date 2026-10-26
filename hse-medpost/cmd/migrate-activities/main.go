package main

import (
	"flag"
	"fmt"
	"os"

	"hse-medpost/internal/migrateactivities"
)

func main() {
	dump := flag.String("dump", "", "mysqldump of medical_activities")
	srcUploads := flag.String("src-uploads", "", "legacy uploads directory")
	srcRoot := flag.String("src-upload-root", "/home/orangepi/projects/medical-activities/uploads", "upload prefix stored in FileLink")
	destDB := flag.String("dest-db", "", "fresh SQLite database to create")
	destUploads := flag.String("dest-uploads", "", "destination uploads directory to create")
	migrations := flag.String("migrations", "migrations", "directory of SQLite migration files")
	flag.Parse()

	result, err := migrateactivities.Run(migrateactivities.Options{
		DumpPath:      *dump,
		SrcUploads:    *srcUploads,
		SrcUploadRoot: *srcRoot,
		DestDB:        *destDB,
		DestUploads:   *destUploads,
		MigrationsDir: *migrations,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate-activities: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("programs=%d staff=%d users=%d subheaders=%d activities=%d evidence=%d documents=%d files=%d\n",
		result.Programs, result.Staff, result.Users, result.SubHeaders, result.Activities, result.Evidence, result.Documents, result.Files)
}
