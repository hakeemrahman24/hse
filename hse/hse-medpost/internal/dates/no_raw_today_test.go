package dates

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Business dates must come from Today/ClinicNow. These patterns are how the
// server's UTC day (or the browser's zone) used to leak in as "today".
var rawTodayPatterns = map[string]*regexp.Regexp{
	".go": regexp.MustCompile(`time\.Now\(\)\s*\.Format\((dateOnly|"2006-01-02")\)|displayHcValidityStatus\([^)]*time\.Now\(\)`),
	".js": regexp.MustCompile(`toISOString\(\)\s*\.(slice\(0,\s*10\)|split\("T"\)\[0\]|substring\(0,\s*10\))`),
}

func TestNoRawTodayOutsideDatesPackage(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, dir := range []string{"internal", "cmd", filepath.Join("web", "static")} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			re, ok := rawTodayPatterns[filepath.Ext(path)]
			if !ok || strings.HasSuffix(path, "_test.go") || strings.Contains(path, string(filepath.Separator)+"dates"+string(filepath.Separator)) {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(body), "\n") {
				if re.MatchString(line) {
					t.Errorf("%s:%d derives a business date from the raw clock; use dates.Today/ClinicNow (Go) or clinicToday() (JS)", path, i+1)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
