package migrateactivities

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Cell is one mysqldump value. Null is true for an unquoted NULL.
type Cell struct {
	Null bool
	Text string
}

// Row is one dumped tuple, keyed by the CREATE TABLE column name.
type Row struct {
	Cells map[string]Cell
}

func (r Row) Null(column string) bool {
	c, ok := r.Cells[column]
	return !ok || c.Null
}

func (r Row) Text(column string) string {
	if r.Null(column) {
		return ""
	}
	return r.Cells[column].Text
}

func (r Row) Int(column string) (int64, error) {
	if r.Null(column) {
		return 0, fmt.Errorf("%s is null", column)
	}
	n, err := strconv.ParseInt(r.Cells[column].Text, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", column, err)
	}
	return n, nil
}

// Dump is the set of tables found in a mysqldump file.
type Dump struct {
	Tables map[string][]Row
}

// ParseDump reads CREATE TABLE column order and INSERT tuples.
// Column order comes from the dump, because the live EvidenceTable lists
// FK_StaffID last rather than in the reference schema's order.
func ParseDump(sql string) (*Dump, error) {
	columns, err := createColumns(sql)
	if err != nil {
		return nil, err
	}
	inserts, err := insertRows(sql)
	if err != nil {
		return nil, err
	}
	out := &Dump{Tables: map[string][]Row{}}
	for name, cols := range columns {
		out.Tables[name] = nil
		for _, tuple := range inserts[name] {
			if len(tuple) != len(cols) {
				return nil, fmt.Errorf("%s: insert has %d values, create has %d columns", name, len(tuple), len(cols))
			}
			row := Row{Cells: make(map[string]Cell, len(cols))}
			for i, col := range cols {
				row.Cells[col] = tuple[i]
			}
			out.Tables[name] = append(out.Tables[name], row)
		}
	}
	for name := range inserts {
		if _, ok := columns[name]; !ok {
			return nil, fmt.Errorf("insert into %s has no create table", name)
		}
	}
	return out, nil
}

func createColumns(sql string) (map[string][]string, error) {
	const marker = "CREATE TABLE `"
	out := map[string][]string{}
	rest := sql
	for {
		idx := strings.Index(rest, marker)
		if idx < 0 {
			break
		}
		rest = rest[idx+len(marker):]
		nameEnd := strings.Index(rest, "`")
		if nameEnd < 0 {
			return nil, fmt.Errorf("truncated create table name")
		}
		name := rest[:nameEnd]
		rest = rest[nameEnd+1:]
		paren := strings.Index(rest, "(")
		if paren < 0 {
			return nil, fmt.Errorf("%s: missing column list", name)
		}
		rest = rest[paren+1:]
		end := strings.Index(rest, "\n) ENGINE")
		if end < 0 {
			end = strings.Index(rest, ") ENGINE")
		}
		if end < 0 {
			return nil, fmt.Errorf("%s: missing engine clause", name)
		}
		body := rest[:end]
		rest = rest[end:]
		var cols []string
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "`") {
				continue
			}
			colEnd := strings.Index(line[1:], "`")
			if colEnd < 0 {
				return nil, fmt.Errorf("%s: bad column line", name)
			}
			cols = append(cols, line[1:1+colEnd])
		}
		if len(cols) == 0 {
			return nil, fmt.Errorf("%s: no columns", name)
		}
		out[name] = cols
	}
	return out, nil
}

func insertRows(sql string) (map[string][][]Cell, error) {
	const marker = "INSERT INTO `"
	out := map[string][][]Cell{}
	rest := sql
	for {
		idx := strings.Index(rest, marker)
		if idx < 0 {
			break
		}
		rest = rest[idx+len(marker):]
		nameEnd := strings.Index(rest, "`")
		if nameEnd < 0 {
			return nil, fmt.Errorf("truncated insert table name")
		}
		name := rest[:nameEnd]
		rest = strings.TrimLeftFunc(rest[nameEnd+1:], unicode.IsSpace)
		if strings.HasPrefix(rest, "(") {
			_, next, err := parseList(rest)
			if err != nil {
				return nil, fmt.Errorf("%s column list: %w", name, err)
			}
			rest = strings.TrimLeftFunc(rest[next:], unicode.IsSpace)
		}
		if !strings.HasPrefix(rest, "VALUES") {
			return nil, fmt.Errorf("%s: expected VALUES", name)
		}
		rest = strings.TrimLeftFunc(rest[len("VALUES"):], unicode.IsSpace)
		tuples, next, err := parseTupleList(rest)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out[name] = append(out[name], tuples...)
		rest = next
	}
	return out, nil
}

func parseTupleList(s string) ([][]Cell, string, error) {
	var tuples [][]Cell
	i := 0
	for {
		i = skipSpace(s, i)
		if i >= len(s) {
			return nil, "", fmt.Errorf("unterminated insert")
		}
		if s[i] == ';' {
			return tuples, s[i+1:], nil
		}
		if s[i] != '(' {
			return nil, "", fmt.Errorf("expected tuple at %q", preview(s[i:]))
		}
		row, next, err := parseList(s[i:])
		if err != nil {
			return nil, "", err
		}
		tuples = append(tuples, row)
		i += next
		i = skipSpace(s, i)
		if i >= len(s) {
			return nil, "", fmt.Errorf("unterminated insert")
		}
		switch s[i] {
		case ',':
			i++
		case ';':
			return tuples, s[i+1:], nil
		default:
			return nil, "", fmt.Errorf("expected comma or semicolon at %q", preview(s[i:]))
		}
	}
}

// parseList reads a parenthesized list and returns the index just after ')'.
func parseList(s string) ([]Cell, int, error) {
	if len(s) == 0 || s[0] != '(' {
		return nil, 0, fmt.Errorf("expected '('")
	}
	i := 1
	var row []Cell
	for {
		i = skipSpace(s, i)
		if i >= len(s) {
			return nil, 0, fmt.Errorf("unterminated tuple")
		}
		if s[i] == ')' {
			return row, i + 1, nil
		}
		cell, next, err := parseCell(s, i)
		if err != nil {
			return nil, 0, err
		}
		row = append(row, cell)
		i = skipSpace(s, next)
		if i >= len(s) {
			return nil, 0, fmt.Errorf("unterminated tuple")
		}
		switch s[i] {
		case ',':
			i++
		case ')':
			return row, i + 1, nil
		default:
			return nil, 0, fmt.Errorf("expected comma or ')' at %q", preview(s[i:]))
		}
	}
}

func parseCell(s string, i int) (Cell, int, error) {
	i = skipSpace(s, i)
	if i >= len(s) {
		return Cell{}, 0, fmt.Errorf("unexpected end of value")
	}
	if strings.HasPrefix(s[i:], "NULL") && (i+4 == len(s) || !isIdent(s[i+4])) {
		return Cell{Null: true}, i + 4, nil
	}
	if s[i] == '\'' {
		return parseString(s, i)
	}
	start := i
	if s[i] == '-' {
		i++
	}
	if i >= len(s) || !unicode.IsDigit(rune(s[i])) {
		return Cell{}, 0, fmt.Errorf("unexpected value at %q", preview(s[start:]))
	}
	for i < len(s) && (unicode.IsDigit(rune(s[i])) || s[i] == '.') {
		i++
	}
	return Cell{Text: s[start:i]}, i, nil
}

func parseString(s string, i int) (Cell, int, error) {
	i++ // opening quote
	var b strings.Builder
	for i < len(s) {
		c := s[i]
		if c == '\\' {
			if i+1 >= len(s) {
				return Cell{}, 0, fmt.Errorf("truncated escape")
			}
			b.WriteString(unescape(s[i+1]))
			i += 2
			continue
		}
		if c == '\'' {
			if i+1 < len(s) && s[i+1] == '\'' {
				b.WriteByte('\'')
				i += 2
				continue
			}
			return Cell{Text: b.String()}, i + 1, nil
		}
		b.WriteByte(c)
		i++
	}
	return Cell{}, 0, fmt.Errorf("unterminated string")
}

func unescape(c byte) string {
	switch c {
	case '0':
		return "\x00"
	case 'n':
		return "\n"
	case 'r':
		return "\r"
	case 't':
		return "\t"
	case 'Z':
		return "\x1a"
	default:
		return string(c)
	}
}

func skipSpace(s string, i int) int {
	for i < len(s) && unicode.IsSpace(rune(s[i])) {
		i++
	}
	return i
}

func isIdent(c byte) bool {
	return unicode.IsLetter(rune(c)) || unicode.IsDigit(rune(c)) || c == '_'
}

func preview(s string) string {
	if len(s) > 24 {
		return s[:24]
	}
	return s
}
