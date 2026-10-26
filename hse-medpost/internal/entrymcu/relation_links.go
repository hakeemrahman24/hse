package entrymcu

import (
	"fmt"
	"os"
	"strings"

	"github.com/xuri/excelize/v2"
)

// RelationLinkReport counts what WriteRelationLinkSQL found in the sheet.
type RelationLinkReport struct {
	Updates   int
	Skipped   int
	Unknown   []string
	Conflicts []string
}

// WriteRelationLinkSQL reads the legacy workbook and writes one UPDATE per
// checkup that sets FK_CompanyRelationID from the sheet's Relation code. Each
// UPDATE only fills a NULL relation, so no other column changes and rows that
// are already linked are left alone.
func WriteRelationLinkSQL(xlsxPath, outPath string) (RelationLinkReport, error) {
	var report RelationLinkReport
	f, err := excelize.OpenFile(xlsxPath)
	if err != nil {
		return report, err
	}
	defer f.Close()
	rows, err := f.GetRows(f.GetSheetName(0), excelize.Options{RawCellValue: true})
	if err != nil {
		return report, err
	}
	if len(rows) < 2 {
		return report, fmt.Errorf("workbook has no data rows")
	}
	get := cellGetter(headerIndex(rows[0]))
	seen := map[string]string{}
	var sql strings.Builder
	sql.WriteString("START TRANSACTION;\n")
	for _, row := range rows[1:] {
		id := get(row, "CheckUpID")
		code := cleanLegacyCell(get(row, "Relation"))
		// R00 is not in the relation list; leave those checkups unlinked.
		if id == "" || id == "None" || code == "" || strings.EqualFold(code, "R00") {
			report.Skipped++
			continue
		}
		if prev, ok := seen[id]; ok && prev != code {
			report.Conflicts = append(report.Conflicts, id)
			continue
		}
		seen[id] = code
		dbCode, ok := legacyRelationCodes[strings.ToUpper(code)]
		if !ok {
			report.Unknown = append(report.Unknown, code)
			continue
		}
		report.Updates++
		fmt.Fprintf(&sql, "UPDATE McuRecord SET FK_CompanyRelationID=(SELECT CompanyRelationID FROM CompanyRelation WHERE RelationCode='%s') WHERE CheckUpID='%s' AND FK_CompanyRelationID IS NULL;\n", dbCode, id)
	}
	sql.WriteString("COMMIT;\n")
	if err := os.WriteFile(outPath, []byte(sql.String()), 0o644); err != nil {
		return report, err
	}
	return report, nil
}
