package mcusubmit

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// One zip of the health certificates of the Closed documents in the list (owner, 2026-11-05: HR
// teams want them in one go). It follows the filter the person has set (worker, company, uploader's
// company, dates), reads only documents inside their scope, and asks the same CertificateSource as
// the single download, so a submitter gets exactly what staff can download. A document whose
// certificate the rules refuse is skipped and named in a short note inside the zip.

// maxZipCertificates caps one download: each certificate may be built on demand, so a very large
// list is fetched in more than one go (narrow the dates) instead of tying the server up.
const maxZipCertificates = 100

// zipQuery is the address of the zip for the filter on screen: the same worker, company, account and
// dates. There is no status: the zip holds every document whose certificate may be offered (Closed,
// or Reviewed with an eligible result), whatever status chip is on.
func zipQuery(form map[string]string) string {
	q := url.Values{}
	for _, k := range []string{"worker", "company", "account", "from", "to"} {
		if v := form[k]; v != "" {
			q.Set(k, v)
		}
	}
	return basePath + "/documents/certificates.zip?" + q.Encode()
}

// CertificatesZip streams the zip.
func (a *API) CertificatesZip(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	if a.certs == nil {
		a.notFound(w, u)
		return
	}
	sc, err := a.Store.ScopeFor(r.Context(), u)
	if err != nil {
		a.serverError(w, "scope", err)
		return
	}
	q := r.URL.Query()
	account, _ := parseID(q.Get("account"))
	filter := DocFilter{OwnerID: u.ID, Scope: &sc, Account: account,
		Worker: q.Get("worker"), Company: q.Get("company"), From: q.Get("from"), To: q.Get("to")}

	var docs []Submission
	for page := 1; len(docs) < maxZipCertificates; page++ {
		filter.Page = page
		res, err := a.Store.SearchDocs(r.Context(), filter)
		if errors.Is(err, ErrBadFilter) {
			a.message(w, http.StatusBadRequest, "Invalid request", "Check the dates you chose.", u)
			return
		}
		if err != nil {
			a.serverError(w, "zip search", err)
			return
		}
		for _, d := range res.Rows {
			if d.CertificateReady() && len(docs) < maxZipCertificates {
				docs = append(docs, d)
			}
		}
		if page >= res.Pages {
			break
		}
	}
	if len(docs) == 0 {
		a.message(w, http.StatusNotFound, "No certificates", "None of the documents in this list has a certificate to download.", u)
		return
	}

	// Build the files first so a failure can still be a normal error page; once the zip starts the
	// status is already sent.
	type entry struct{ name, path string }
	var files []entry
	var skipped []string
	for _, d := range docs {
		cert, err := a.certs.Certificate(r.Context(), d.RecordID)
		var unavailable *CertificateUnavailable
		switch {
		case errors.As(err, &unavailable):
			skipped = append(skipped, fmt.Sprintf("%s: %s", oneLine(d.PatName), unavailable.Reason))
			continue
		case err != nil:
			a.serverError(w, "zip certificate", err)
			return
		}
		// Two documents can share a filename; the document id keeps the names apart.
		files = append(files, entry{name: strconv.FormatInt(d.ID, 10) + "-" + cert.Filename, path: cert.Path})
		if err := a.Store.LogEvent(r.Context(), Event{ActorType: ActorSubmitter, ActorID: u.ID, EntityType: EntitySubmission, EntityID: d.ID, Action: "CertificateDownloaded", Detail: "zip"}); err != nil {
			log.Printf("mcusubmit: log certificate download: %v", err)
		}
	}
	if len(files) == 0 {
		a.message(w, http.StatusConflict, "No certificates available", "A certificate cannot be issued for these checkups. Please contact the medical team.", u)
		return
	}

	h := w.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", `attachment; filename="certificates.zip"`)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	zw := zip.NewWriter(w)
	for _, f := range files {
		if err := addZipFile(zw, f.name, f.path); err != nil {
			log.Printf("mcusubmit: zip %s: %v", f.name, err)
			break
		}
	}
	if len(skipped) > 0 {
		if out, err := zw.Create("not-available.txt"); err == nil {
			_, _ = io.WriteString(out, "No certificate could be issued for:\r\n"+strings.Join(skipped, "\r\n")+"\r\n")
		}
	}
	if err := zw.Close(); err != nil {
		log.Printf("mcusubmit: close zip: %v", err)
	}
}

func addZipFile(zw *zip.Writer, name, path string) error {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, src)
	return err
}
