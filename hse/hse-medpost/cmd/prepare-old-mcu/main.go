package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"hse-medpost/internal/config"
	"hse-medpost/internal/db"
	"hse-medpost/internal/entrymcu"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "dates" {
		path := "_reference/mcu.xlsx"
		if len(os.Args) > 2 {
			path = os.Args[2]
		}
		if err := fixDates(path); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "sync" {
		path := "_reference/mcu.xlsx"
		if len(os.Args) > 2 {
			path = os.Args[2]
		}
		if err := syncReference(path); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "load" {
		path := "_reference/old-mcu-import.xlsx"
		if len(os.Args) > 2 {
			path = os.Args[2]
		}
		if err := loadWorkbook(path); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	inPath := "_reference/old-mcu-data.csv"
	outPath := "_reference/old-mcu-import.xlsx"
	if len(os.Args) > 1 {
		inPath = os.Args[1]
	}
	if len(os.Args) > 2 {
		outPath = os.Args[2]
	}
	in, err := os.Open(inPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer in.Close()
	file, report, err := entrymcu.PrepareOldMCU(in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := file.SaveAs(outPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s\ncheckups %d\npatients %d\nparents linked %d\nparent disagreements %d\nseveral predecessors %d\nshort projects %d\ncurrent %d\nsuperseded %d\nphilippines %d\ncompany value errors %d\nyilybang %d\nfit with restriction %d\nnone id %s\n",
		outPath, report.Checkups, report.Patients, report.ParentsLinked, report.ParentDisagreements, report.MultiPredecessor,
		report.ShortProjects, report.TerminalCurrent, report.TerminalSuperseded, report.PhilippinesFixed,
		report.CompanyValueErrors, report.YilybangFixed, report.FitWithRestriction, report.NoneID)
}

func fixDates(path string) error {
	if err := entrymcu.LoadProjectEnv(".env"); err != nil && !os.IsNotExist(err) {
		return err
	}
	cfg := config.Load()
	database, err := db.Open(cfg.DBDriver, cfg.DSN())
	if err != nil {
		return err
	}
	defer database.Close()
	n, ids, err := entrymcu.CorrectShiftedYears(database, path)
	if err != nil {
		return err
	}
	fmt.Printf("corrected %d\n", n)
	if len(ids) > 0 {
		fmt.Printf("fields %s\n", strings.Join(ids, ", "))
	}
	return nil
}

func syncReference(path string) error {
	if err := entrymcu.LoadProjectEnv(".env"); err != nil && !os.IsNotExist(err) {
		return err
	}
	cfg := config.Load()
	database, err := db.Open(cfg.DBDriver, cfg.DSN())
	if err != nil {
		return err
	}
	defer database.Close()
	report, err := entrymcu.SyncReferenceWorkbook(database, path)
	if err != nil {
		return err
	}
	fmt.Printf("sheet rows %d\ncreated %d\npatched %d\nbad dates %d\nerrors %d\n",
		report.SheetRows, report.Created, report.Patched, len(report.SkippedDate), len(report.Errors))
	if len(report.CreatedIDs) > 0 {
		fmt.Printf("created ids %s\n", strings.Join(report.CreatedIDs, " "))
	}
	if len(report.PatchedIDs) > 0 {
		fmt.Printf("patched ids %s\n", strings.Join(report.PatchedIDs, " "))
	}
	if len(report.SkippedDate) > 0 {
		fmt.Printf("skipped bad date %s\n", strings.Join(report.SkippedDate, " "))
	}
	for _, rowErr := range report.Errors {
		fmt.Printf("row %d: %s\n", rowErr.Row, rowErr.Message)
	}
	if len(report.Errors) > 0 {
		return fmt.Errorf("import did not finish cleanly")
	}
	return nil
}

func loadWorkbook(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "old-mcu-import.xlsx")
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	cfg := config.Load()
	database, err := db.Open(cfg.DBDriver, cfg.DSN())
	if err != nil {
		return err
	}
	defer database.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/mcu-records/import", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	(&entrymcu.API{DB: database}).ImportMcuRecords(rec, req)
	var result entrymcu.ImportResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		return fmt.Errorf("import status %d: %s", rec.Code, rec.Body.String())
	}
	fmt.Printf("status %d\ncreated %d\nupdated %d\nskipped %d\nerrors %d\n", rec.Code, result.Created, result.Updated, result.Skipped, len(result.Errors))
	for _, rowErr := range result.Errors {
		fmt.Printf("row %d: %s\n", rowErr.Row, rowErr.Message)
	}
	if rec.Code != http.StatusOK || len(result.Errors) > 0 {
		return fmt.Errorf("import did not finish cleanly")
	}
	return nil
}
