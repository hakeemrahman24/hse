// Command link-relations writes SQL that fills in missing company relations
// on existing checkups, using the Relation code on each row of the legacy
// workbook. It does not connect to the database; run the generated file
// yourself after a backup.
//
// Usage: go run ./cmd/link-relations <legacy.xlsx> <out.sql>
package main

import (
	"fmt"
	"os"
	"strings"

	"hse-medpost/internal/entrymcu"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: link-relations <legacy.xlsx> <out.sql>")
		os.Exit(2)
	}
	report, err := entrymcu.WriteRelationLinkSQL(os.Args[1], os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s\nupdates %d\nskipped %d\nconflicts %d\nunknown codes %s\n",
		os.Args[2], report.Updates, report.Skipped, len(report.Conflicts), strings.Join(report.Unknown, " "))
}
