package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const streamTimeout = 60 * time.Second

var errMedpostNotFound = errors.New("hse-medpost has no such file")

// medpostFile is one photo or proof file of an activity. URL is the address on this application
// that streams it (the browser never talks to hse-medpost or sees the token).
type medpostFile struct {
	EvidenceID int64  `json:"evidenceId"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	Date       string `json:"date"`
	URL        string `json:"-"`
	IsPDF      bool   `json:"-"`
}

// medpostDoc is one program reference document.
type medpostDoc struct {
	DocumentID int64  `json:"documentId"`
	Program    string `json:"program"`
	Type       string `json:"type"`
	Title      string `json:"title"`
	Notes      string `json:"notes"`
	FormLink   string `json:"formLink"`
	FileName   string `json:"fileName"`
	URL        string `json:"-"`
}

// docGroup is the documents of one program, for the Documents page.
type docGroup struct {
	Program string
	Docs    []medpostDoc
}

func (m *medpostDirectory) newRequest(ctx context.Context, path string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+m.token)
	return req, nil
}

func (m *medpostDirectory) getJSON(ctx context.Context, path string, out any) error {
	req, err := m.newRequest(ctx, path)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second, CheckRedirect: medpostTestClient.CheckRedirect}).Do(req)
	if err != nil {
		return fmt.Errorf("medpost request failed: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return errMedpostNotFound
	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return fmt.Errorf("medpost answered %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out); err != nil {
		return fmt.Errorf("medpost answer could not be read: %w", err)
	}
	return nil
}

// medpostSubHeader is one active program sub-header.
type medpostSubHeader struct {
	SubHeaderID int64  `json:"subHeaderId"`
	Program     string `json:"program"`
	Name        string `json:"name"`
}

// SubHeaders reads the active sub-headers of every program.
func (m *medpostDirectory) SubHeaders(ctx context.Context) ([]medpostSubHeader, error) {
	var out []medpostSubHeader
	err := m.getJSON(ctx, "/api/directory/subheaders", &out)
	return out, err
}

// Documents reads the program reference documents.
func (m *medpostDirectory) Documents(ctx context.Context) ([]medpostDoc, error) {
	var out []medpostDoc
	err := m.getJSON(ctx, "/api/directory/documents", &out)
	for i := range out {
		if out[i].FileName != "" {
			out[i].URL = documentURL(out[i])
		}
		if !safeLink(out[i].FormLink) {
			out[i].FormLink = ""
		}
	}
	return out, err
}

// Stream copies one file from hse-medpost to the browser. Only the headers that describe the
// file are passed on, plus the safety headers set here, so an uploaded .svg or .html cannot run
// scripts under this application's address.
func (m *medpostDirectory) Stream(w http.ResponseWriter, r *http.Request, path string) {
	ctx, cancel := context.WithTimeout(r.Context(), streamTimeout)
	defer cancel()
	req, err := m.newRequest(ctx, path)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	resp, err := (&http.Client{CheckRedirect: medpostTestClient.CheckRedirect}).Do(req)
	if err != nil {
		log.Printf("medpost file: %v", err) // the error text never carries the token
		http.Error(w, "HSE Medpost could not be reached.", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		http.NotFound(w, r)
		return
	default:
		http.Error(w, "HSE Medpost could not send the file.", http.StatusBadGateway)
		return
	}
	h := w.Header()
	for _, k := range []string{"Content-Type", "Content-Length", "Content-Disposition", "Last-Modified"} {
		if v := resp.Header.Get(k); v != "" {
			h.Set(k, v)
		}
	}
	h.Set("Cache-Control", "private, no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox")
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Printf("medpost file copy: %v", err)
	}
}

// finishFiles sets the address on this application and the PDF flag of each file.
func finishFiles(files []medpostFile) {
	for i := range files {
		files[i].URL = evidenceURL(files[i])
		files[i].IsPDF = strings.HasSuffix(strings.ToLower(files[i].Name), ".pdf")
	}
}

// evidenceURL is the address on this application of one evidence file; the name is only there
// so a browser saves the file under its real name.
func evidenceURL(f medpostFile) string {
	return "/activities/files/" + strconv.FormatInt(f.EvidenceID, 10) + "/" + url.PathEscape(f.Name)
}

func documentURL(d medpostDoc) string {
	return "/activities/documents/" + strconv.FormatInt(d.DocumentID, 10) + "/" + url.PathEscape(d.FileName)
}

// safeLink keeps only http(s) links, so a stored "javascript:" form link is never rendered.
func safeLink(s string) bool {
	l := strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://")
}

// groupDocs gathers documents by program, keeping Medpost's order. Documents with no program
// (common templates) go under "Common templates".
func groupDocs(docs []medpostDoc) []docGroup {
	var out []docGroup
	idx := map[string]int{}
	for _, d := range docs {
		name := d.Program
		if name == "" {
			name = "Common templates"
		}
		i, ok := idx[name]
		if !ok {
			i = len(out)
			idx[name] = i
			out = append(out, docGroup{Program: name})
		}
		out[i].Docs = append(out[i].Docs, d)
	}
	return out
}

// pathID reads a numeric path value; ok is false (and a 404 is sent) when it is not a number.
func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return 0, false
	}
	return id, true
}

// documents keeps the old address working: the program documents are the Reference tab of the
// Medical Activities page.
func (h *activitiesHandlers) documents(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/activities?tab=reference", http.StatusSeeOther)
}

// evidenceFile and documentFile stream one file for a signed-in user.
func (h *activitiesHandlers) evidenceFile(w http.ResponseWriter, r *http.Request) {
	h.stream(w, r, "eid", "/api/directory/evidence/")
}

func (h *activitiesHandlers) documentFile(w http.ResponseWriter, r *http.Request) {
	h.stream(w, r, "id", "/api/directory/documents/")
}

func (h *activitiesHandlers) stream(w http.ResponseWriter, r *http.Request, name, upstream string) {
	id, ok := pathID(w, r, name)
	if !ok {
		return
	}
	d := h.conn.Get()
	if d == nil {
		http.Error(w, "This page is not connected to HSE Medpost.", http.StatusServiceUnavailable)
		return
	}
	d.Stream(w, r, upstream+strconv.FormatInt(id, 10))
}
