package reflist

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// SeedResult counts what LoadTSV did.
type SeedResult struct {
	Added, Skipped int
	Problems       []string // lines that were refused, with the reason
}

// LoadTSV loads a tab-separated file into a list. The first line is a header
// and is skipped; every other line must have one field per column, in the
// list's column order. WHY: the owner's reference data arrives as sheet
// exports, and one loader serves every list. Lines already in the list are
// counted as skipped, so running it twice is harmless; bad lines are
// reported with their line number and do not stop the rest.
func LoadTSV(ctx context.Context, s *Store, actor string, d Def, r io.Reader) (SeedResult, error) {
	var res SeedResult
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimRight(sc.Text(), "\r")
		if n == 1 || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		fields = padOptional(d, fields)
		if len(fields) != len(d.Columns) {
			res.Problems = append(res.Problems, fmt.Sprintf("line %d: expected %d fields, found %d", n, len(d.Columns), len(fields)))
			continue
		}
		_, err := s.Add(ctx, actor, d, fields)
		var ve ValidationError
		switch {
		case err == nil:
			res.Added++
		case errors.Is(err, ErrDuplicate):
			res.Skipped++
		case errors.As(err, &ve):
			res.Problems = append(res.Problems, fmt.Sprintf("line %d: %s", n, ve.Msg))
		default:
			return res, fmt.Errorf("line %d: %w", n, err)
		}
	}
	if err := sc.Err(); err != nil {
		return res, fmt.Errorf("read list file: %w", err)
	}
	return res, nil
}

// padOptional fills missing trailing fields with "" when those columns are
// optional, so a file written before a column existed still loads.
func padOptional(d Def, fields []string) []string {
	for len(fields) < len(d.Columns) && !d.Columns[len(fields)].Required {
		fields = append(fields, "")
	}
	return fields
}
