// Command refseed aligns and loads the reference lists.
//
//	refseed align-malaria <in.tsv> <region.tsv> <out.tsv>
//	    Takes the malaria list (endemicity, regency, province) and makes every
//	    regency name and province match the administrative-area data hse-medpost
//	    serves on /api/regions, which the owner decided is the correct source.
//	    Prints every change, drops rows that become duplicates, and writes the
//	    result. Writes nothing and exits 1 if any line needs a person.
//
//	refseed load <list_key> <file.tsv>
//	    Loads a tab-separated file (header line first) into an admin list.
//	    Safe to repeat: lines already present are skipped.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"entry-permit/internal/config"
	"entry-permit/internal/dates"
	"entry-permit/internal/db"
	"entry-permit/internal/reflist"
	"entry-permit/internal/region"
	"entry-permit/migrations"
)

const seedActor = "seed"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "refseed:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	switch {
	case len(args) == 4 && args[0] == "align-malaria":
		return alignMalaria(args[1], args[2], args[3])
	case len(args) == 3 && args[0] == "load":
		return load(args[1], args[2])
	}
	return fmt.Errorf("usage: refseed align-malaria <in.tsv> <region.tsv> <out.tsv> | refseed load <list_key> <file.tsv>")
}

func readRows(path string) ([]region.Row, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var rows []region.Row
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimRight(sc.Text(), "\r")
		if n == 1 || strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			return nil, fmt.Errorf("%s line %d: expected endemicity, regency, province", path, n)
		}
		rows = append(rows, region.Row{Line: n, Endemicity: strings.TrimSpace(parts[0]), Regency: strings.TrimSpace(parts[1]), Province: strings.TrimSpace(parts[2])})
	}
	return rows, sc.Err()
}

func alignMalaria(inPath, regionPath, outPath string) error {
	rows, err := readRows(inPath)
	if err != nil {
		return err
	}
	rf, err := os.Open(regionPath)
	if err != nil {
		return err
	}
	defer rf.Close()
	ix, err := region.LoadIndex(rf)
	if err != nil {
		return err
	}
	res := ix.Align(rows)
	for _, c := range res.Changes {
		fmt.Println("changed:", c)
	}
	for _, p := range res.Problems {
		fmt.Println("PROBLEM:", p)
	}
	if len(res.Problems) > 0 {
		return fmt.Errorf("%d line(s) need a person; nothing written", len(res.Problems))
	}
	var b strings.Builder
	b.WriteString("Endemicity\tRegency/City\tProvince\n")
	for _, r := range res.Rows {
		fmt.Fprintf(&b, "%s\t%s\t%s\n", r.Endemicity, r.Regency, r.Province)
	}
	if err := os.WriteFile(outPath, []byte(b.String()), 0o644); err != nil {
		return err
	}
	fmt.Printf("%d rows in, %d rows out (%d changes) -> %s\n", len(rows), len(res.Rows), len(res.Changes), outPath)
	return nil
}

func load(listKey, path string) error {
	def, ok := reflist.Find(listKey)
	if !ok {
		return fmt.Errorf("unknown list %q", listKey)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	cfg := config.Load(os.Getenv)
	database, err := db.Open(cfg.DBDriver, cfg.DSN())
	if err != nil {
		return err
	}
	defer database.Close()
	if err := db.Migrate(database, migrations.FS, migrations.Dir(cfg.DBDriver)); err != nil {
		return err
	}
	res, err := reflist.LoadTSV(context.Background(), reflist.NewStore(database, dates.SystemClock{}), seedActor, def, f)
	if err != nil {
		return err
	}
	for _, p := range res.Problems {
		fmt.Println("PROBLEM:", p)
	}
	fmt.Printf("%s: %d added, %d already present, %d refused\n", def.Label, res.Added, res.Skipped, len(res.Problems))
	if len(res.Problems) > 0 {
		return fmt.Errorf("%d line(s) refused", len(res.Problems))
	}
	return nil
}
