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
		a.Store.AttachCertificates(r.Context(), res.Rows)
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
	var files []zipEntry
	var skipped []string
	for _, d := range docs {
		cert, err := a.certs.Certificate(r.Context(), d.CertRecordID)
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
		files = append(files, zipEntry{name: strconv.FormatInt(d.ID, 10) + "-" + cert.Filename, path: cert.Path})
		if err := a.Store.LogEvent(r.Context(), Event{ActorType: ActorSubmitter, ActorID: u.ID, EntityType: EntitySubmission, EntityID: d.ID, Action: "CertificateDownloaded", Detail: "zip"}); err != nil {
			log.Printf("mcusubmit: log certificate download: %v", err)
		}
	}
	if len(files) == 0 {
		a.message(w, http.StatusConflict, "No certificates available", "A certificate cannot be issued for these checkups. Please contact the medical team.", u)
		return
	}

	writeZip(w, "certificates.zip", files, skipped)
}

// zipEntry is one certificate file to put in a zip: its name inside the zip and where it is on disk.
type zipEntry struct{ name, path string }

// writeZip sends the files as a zip download. It is shared by the documents zip and the Employee
// checkups zip, so both behave the same: once it starts the status is already sent, which is why the
// callers build every file first. Anything skipped is named in a short note inside the zip.
func writeZip(w http.ResponseWriter, filename string, files []zipEntry, skipped []string) {
	h := w.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", `attachment; filename="`+filename+`"`)
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

// EmployeeCertificatesZip sends one zip with the certificates of the N most recent checkups among the
// workers the account may see (owner, 2026-11-14), newest first. Like "Latest N records" in the Excel
// export it ignores the filters on screen: it is "the latest certificates", not "these rows". It asks the
// same CertificateSource as the single download and applies the same rule (Unfit always, Fit once Close,
// already worked out as Employee.HasCertificate), and N is capped like the documents zip, because each
// certificate may be built on demand. The list comes from employeeSource, so a company account never
// gets another company's certificates; the admin gets everybody's.
func (a *API) EmployeeCertificatesZip(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	if a.certs == nil {
		a.notFound(w, u)
		return
	}
	q := r.URL.Query()
	n, err := strconv.Atoi(strings.TrimSpace(q.Get("n")))
	if err != nil || n < 1 || n > maxZipCertificates {
		a.message(w, http.StatusBadRequest, "Invalid request", fmt.Sprintf("Enter how many certificates, from 1 to %d.", maxZipCertificates), u)
		return
	}
	all, _, _, err := a.employeeSource(r.Context(), u, q.Get("subs") == "1")
	if err != nil {
		a.serverError(w, "company employees", err)
		return
	}
	var picked []Employee
	for _, e := range sortEmployees(all, sortCheckup, dirDesc) {
		if !e.HasCertificate || e.RecordID == 0 {
			continue
		}
		picked = append(picked, e)
		if len(picked) == n {
			break
		}
	}
	if len(picked) == 0 {
		a.message(w, http.StatusNotFound, "No certificates", "None of the employees has a certificate to download yet.", u)
		return
	}

	// Build the files first so a failure can still be a normal error page (see writeZip).
	var files []zipEntry
	var skipped []string
	for _, e := range picked {
		cert, err := a.certs.Certificate(r.Context(), e.RecordID)
		var unavailable *CertificateUnavailable
		switch {
		case errors.As(err, &unavailable):
			skipped = append(skipped, fmt.Sprintf("%s: %s", oneLine(e.Name), unavailable.Reason))
			continue
		case err != nil:
			a.serverError(w, "employee certificate zip", err)
			return
		}
		files = append(files, zipEntry{name: strconv.FormatInt(e.RecordID, 10) + "-" + cert.Filename, path: cert.Path})
		// The audit row is about the account that downloaded; Detail holds the checkup record id, never a name.
		if err := a.Store.LogEvent(r.Context(), Event{ActorType: ActorSubmitter, ActorID: u.ID, EntityType: EntitySubmitter, EntityID: u.ID, Action: "CertificateDownloaded", Detail: "zip record " + strconv.FormatInt(e.RecordID, 10)}); err != nil {
			log.Printf("mcusubmit: log certificate download: %v", err)
		}
	}
	if len(files) == 0 {
		a.message(w, http.StatusConflict, "No certificates available", "A certificate cannot be issued for these checkups. Please contact the medical team.", u)
		return
	}
	writeZip(w, "latest-certificates.zip", files, skipped)
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
