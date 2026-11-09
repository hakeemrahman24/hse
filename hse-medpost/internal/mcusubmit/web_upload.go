package mcusubmit

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// maxUploadForm is how much of the multipart body is held in memory; the rest, which is the PDF
// itself, goes to a temporary file that net/http removes when the request ends.
const maxUploadForm = 1 << 20

// Plausible working ages. A worker outside this range is a typo in the date, not a worker.
const (
	minWorkerAge = 15
	maxWorkerAge = 80
)

var (
	nikPattern      = regexp.MustCompile(`^[0-9]{16}$`)
	passportPattern = regexp.MustCompile(`^[A-Z0-9]{6,12}$`)
)

// uploadsOn answers 503 when no pipeline is configured (no Ghostscript on this machine, say): a
// portal that cannot rebuild files must not accept them.
func (a *API) uploadsOn(w http.ResponseWriter, u *Submitter) bool {
	if a.pipeline != nil {
		return true
	}
	a.message(w, http.StatusServiceUnavailable, "Uploads are not available", "Uploading documents is switched off right now. Please try again later.", u)
	return false
}

// ---- pages ------------------------------------------------------------------------------------

// BatchPage is "New batch": the one-worker form, the drafts of the open batch, and Send.
func (a *API) BatchPage(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil || !a.uploadsOn(w, u) {
		return
	}
	a.renderBatch(w, r.Context(), http.StatusOK, u, nil, "")
}

func (a *API) renderBatch(w http.ResponseWriter, ctx context.Context, status int, u *Submitter, form map[string]string, errMsg string) {
	p := a.newPage("New batch")
	p.User, p.Error, p.Caption = u, errMsg, "Drafts: not yet visible to the medical team"
	if form != nil {
		p.Form = form
	}
	if p.Form["company_id"] == "" && u.CompanyID != 0 {
		p.Form["company_id"] = strconv.FormatInt(u.CompanyID, 10)
	}
	var err error
	if p.Companies, err = a.companies(ctx); err != nil {
		a.serverError(w, "companies", err)
		return
	}
	if p.Nationalities, err = a.nationalities(ctx); err != nil {
		a.serverError(w, "nationalities", err)
		return
	}
	docs, err := a.Store.ListOwn(ctx, u.ID)
	if err != nil {
		a.serverError(w, "list own", err)
		return
	}
	for _, d := range docs {
		switch d.Status {
		case SubDraft:
			p.Docs = append(p.Docs, d)
			p.BatchID = d.BatchID
		case SubReview:
			p.Docs = append(p.Docs, d)
			p.BatchID = d.BatchID
			p.Unconfirmed++
		}
	}
	a.render(w, status, "batch.html", p)
}

// Documents is "My documents": every document of the signed-in submitter, newest first. The
// submitter id comes from the session, so nobody can list another account's rows.
func (a *API) Documents(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page")) // not a number becomes 0, which SearchDocs moves to page 1
	sc, err := a.Store.ScopeFor(r.Context(), u)
	if err != nil {
		a.serverError(w, "scope", err)
		return
	}
	account, _ := parseID(q.Get("account")) // 0 = every company in scope
	filter := DocFilter{
		OwnerID: u.ID, Scope: &sc, Account: account, Needs: q.Get("needs") == "1", Status: q.Get("status"), Worker: q.Get("worker"), Company: q.Get("company"),
		From: q.Get("from"), To: q.Get("to"), Page: page,
	}
	p := a.newPage("My documents")
	p.User, p.Caption = u, "Every document of your company, newest first"
	p.AccountCompanies = a.Store.ScopeCompanies(r.Context(), sc)
	// The column is only worth its width when more than one account's documents can be in the list.
	if n, err := a.Store.accountsInCompany(r.Context(), u.CompanyID); err == nil {
		p.ShowUploader = n > 1 || len(p.AccountCompanies) > 1
	}
	p.Form = map[string]string{"needs": q.Get("needs"), "account": q.Get("account"), "status": filter.Status, "worker": filter.Worker, "company": filter.Company, "from": filter.From, "to": filter.To}
	p.Statuses = documentStatuses
	result, err := a.Store.SearchDocs(r.Context(), filter)
	if errors.Is(err, ErrBadFilter) {
		p.Error = "Check the status and the dates you chose."
		a.render(w, http.StatusBadRequest, "documents.html", p)
		return
	}
	if err != nil {
		a.serverError(w, "search own", err)
		return
	}
	p.Docs, p.Result = result.Rows, result
	// The "Needs action" chip carries the company's count, like Home, whatever filter is on.
	p.Counts = map[string]int{}
	if all, err := a.Store.ListScope(r.Context(), sc); err == nil {
		for _, d := range all {
			if d.Status == SubRejected || d.ResultMessage() != "" {
				p.Counts["Needs"]++
			}
		}
	}
	if a.certs != nil {
		for _, d := range result.Rows {
			if d.Status == SubClosed && d.RecordID != 0 {
				p.ZipURL = zipQuery(p.Form)
				break
			}
		}
	}
	p.PrevURL, p.NextURL = pagerLinks(basePath+"/documents", p.Form, result)
	a.render(w, http.StatusOK, "documents.html", p)
}

// documentStatuses are the choices of the status filter, in the order a submitter thinks of them.
var documentStatuses = []string{SubDraft, SubReview, SubPending, SubReviewed, SubClosed, SubRejected, SubWithdrawn}

// pagerLinks builds the Previous and Next addresses for a list, keeping the filter the person typed.
// Empty means there is no such page. url.Values.Encode sorts the keys, so the links are stable.
func pagerLinks(base string, filter map[string]string, r DocPage) (prev, next string) {
	link := func(page int) string {
		q := url.Values{"page": {strconv.Itoa(page)}}
		for k, v := range filter {
			if v != "" {
				q.Set(k, v)
			}
		}
		return base + "?" + q.Encode()
	}
	if r.Page > 1 {
		prev = link(r.Page - 1)
	}
	if r.Page < r.Pages {
		next = link(r.Page + 1)
	}
	return prev, next
}

func (a *API) nationalities(ctx context.Context) ([]string, error) {
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT Name FROM NationalityTable WHERE Status = 'Active' ORDER BY Name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ---- upload -----------------------------------------------------------------------------------

// AddDocument takes one worker's details and one PDF. Order matters: cheap checks first (fields,
// file present, daily cap), then the pipeline (Ghostscript is the expensive part), and only a file
// that came out of the pipeline gets a row. If the row cannot be written the stored file is
// deleted again, so there are never files without rows.
func (a *API) AddDocument(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil || !a.uploadsOn(w, u) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxUploadBytes+maxUploadForm)
	if err := r.ParseMultipartForm(maxUploadForm); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			a.renderBatch(w, r.Context(), http.StatusRequestEntityTooLarge, u, nil, "The file is larger than 10 MB. Split it or scan at a lower resolution.")
			return
		}
		a.renderBatch(w, r.Context(), http.StatusBadRequest, u, nil, "The upload could not be read. Please try again.")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	form := map[string]string{}
	for _, k := range []string{"patient_id", "name", "dob", "position", "company_id", "nationality", "gender", "id_number"} {
		form[k] = strings.TrimSpace(r.FormValue(k))
	}
	in, msg := a.workerDetails(r.Context(), u.ID, form)
	if msg != "" {
		a.renderBatch(w, r.Context(), http.StatusBadRequest, u, form, msg)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		a.renderBatch(w, r.Context(), http.StatusBadRequest, u, form, "Choose the PDF file to upload.")
		return
	}
	defer file.Close()

	if err := a.Store.CheckQuota(r.Context(), u.ID); err != nil {
		a.uploadFailed(w, r, u, form, err)
		return
	}
	stored, err := a.pipeline.Ingest(r.Context(), file)
	if err != nil {
		a.uploadFailed(w, r, u, form, err)
		return
	}
	in.FilePath, in.FileSha256, in.FileBytes = stored.Ref, stored.Sha256, stored.Bytes
	in.NeedsReview = true // the submitter checks the details and the file before it joins the batch
	id, err := a.Store.AddSubmission(r.Context(), u.ID, in)
	if err != nil {
		a.dropFile(stored.Ref)
		a.uploadFailed(w, r, u, form, err)
		return
	}
	// Without a virus scanner (decided 2026-10-29) the rebuild is the check: a file that came out
	// of Ghostscript and the structure check is as clean as this portal can make it.
	if err := a.Store.SetScanResult(r.Context(), id, ScanClean); err != nil {
		if path, rerr := a.Store.RemoveDraft(r.Context(), u.ID, id); rerr == nil {
			a.dropFile(path)
		}
		a.serverError(w, "mark clean", err)
		return
	}
	http.Redirect(w, r, basePath+"/documents/"+strconv.FormatInt(id, 10)+"/review", http.StatusSeeOther)
}

// uploadFailed turns a pipeline or store error into a message the submitter can act on. Anything
// unexpected is logged in full and shown only as a generic failure.
func (a *API) uploadFailed(w http.ResponseWriter, r *http.Request, u *Submitter, form map[string]string, err error) {
	status, msg := http.StatusBadRequest, ""
	switch {
	case errors.Is(err, ErrDailyLimit):
		status, msg = http.StatusTooManyRequests, "You have reached the limit of uploads for today. Please continue tomorrow."
	case errors.Is(err, ErrTooLarge):
		status, msg = http.StatusRequestEntityTooLarge, "The file is larger than 10 MB. Split it or scan at a lower resolution."
	case errors.Is(err, ErrNotPDF):
		msg = "This file is not a PDF. Upload the checkup result as a PDF."
	case errors.Is(err, ErrEncrypted):
		msg = "This PDF is protected by a password. Remove the password and upload it again."
	case errors.Is(err, ErrUnsafeContent):
		msg = "This PDF contains active content (scripts or attachments) and cannot be accepted. Print it to a new PDF and upload that."
	case errors.Is(err, ErrCannotShrink):
		msg = "This PDF is too large even after compression. Split it into smaller files or scan at a lower resolution."
	case errors.Is(err, ErrRebuild):
		msg = "This PDF could not be read. Open it, save or print it to a new PDF, and upload that."
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		log.Printf("mcusubmit: upload cancelled: %v", err)
		return // the visitor left; nobody to answer
	default:
		a.serverError(w, "upload", err)
		return
	}
	a.renderBatch(w, r.Context(), status, u, form, msg)
}

// workerDetails decides where the worker's details come from. A chosen patient wins: everything
// is read from the database and the typed fields are ignored, so a picked worker cannot be
// misspelled and a tampered form cannot change who the document is about. Without a choice the
// submitter is adding a new employee: typed fields, checked strictly, and refused when that person
// is already on file (they must pick the existing record instead).
func (a *API) workerDetails(ctx context.Context, submitterID int64, f map[string]string) (NewSubmission, string) {
	if f["patient_id"] != "" {
		id, ok := parseID(f["patient_id"])
		if !ok {
			return NewSubmission{}, "Choose the worker from the list."
		}
		w, err := a.Store.WorkerByID(ctx, id)
		if err != nil {
			return NewSubmission{}, "Choose the worker from the list."
		}
		// The picker already greys these workers out; this is the check that counts, because a form
		// can be posted without the picker. Two documents for one checkup help nobody.
		block, err := a.Store.WorkerBlock(ctx, submitterID, w.PatientID)
		if err != nil {
			log.Printf("mcusubmit: worker block: %v", err)
			return NewSubmission{}, "The worker could not be checked. Please try again."
		}
		if block.Kind != "" {
			return NewSubmission{}, block.Message()
		}
		in := NewSubmission{
			PatientID: w.PatientID, PatName: w.Name, PatDateOfBirth: w.DateOfBirth, PatPosition: w.Position,
			PatCompanyText: w.Company, PatNationality: w.Nationality, PatGender: cleanGender(w.Gender), CompanyID: w.CompanyID,
		}
		// The ID number is copied from the patient when there is one, so every submission carries it.
		// It is stored as the same kind the new-employee form would: NIK for an Indonesian citizen,
		// passport otherwise. An older patient with none simply has none.
		if id := strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(w.PersonalID)); id != "" {
			if isIndonesian(w.Nationality) {
				in.PatNik = id
			} else {
				in.PatPassportNo = id
			}
		}
		return in, ""
	}
	if f["name"] == "" {
		return NewSubmission{}, "Choose the worker from the list, or fill in the new employee details."
	}
	in, msg := a.parseWorker(ctx, f)
	if msg != "" {
		return in, msg
	}
	exists, err := a.Store.WorkerExists(ctx, in.PatName, in.PatDateOfBirth)
	if err != nil {
		log.Printf("mcusubmit: worker exists: %v", err)
		return in, "The worker could not be checked. Please try again."
	}
	if exists {
		return in, "This worker is already on file. Search for the name and choose them from the list instead of adding them again."
	}
	in.IsNewEmployee = true
	return in, ""
}

type workerJSON struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	DateOfBirth string `json:"dob"`
	Position    string `json:"position"`
	Company     string `json:"company"`
	Nationality string `json:"nationality"`
	// Blocked is "", "waiting" or "valid"; Until and From carry the dates of "valid". The picker
	// builds its sentence from these codes so the page's language dictionary can translate it.
	Blocked string `json:"blocked"`
	Last    string `json:"last"`
	Until   string `json:"until"`
	From    string `json:"from"`
}

// WorkerSearch feeds the picker. It needs a session, asks for at least three letters, returns at
// most ten workers and is rate-limited per account, because the list is patient data (owner,
// 2026-10-29: every approved submitter may search all patients, since a contractor works for
// several companies).
func (a *API) WorkerSearch(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	key := "wk:" + strconv.FormatInt(u.ID, 10)
	if a.lim.Count(key, workerSearchWindow) >= a.limits.WorkerSearchPerUser {
		http.Error(w, "Too many searches. Wait a moment.", http.StatusTooManyRequests)
		return
	}
	a.lim.Add(key)
	found, err := a.Store.SearchWorkers(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		a.serverError(w, "search workers", err)
		return
	}
	out := make([]workerJSON, 0, len(found))
	for _, f := range found {
		j := workerJSON{ID: f.PatientID, Name: f.Name, DateOfBirth: f.DateOfBirth, Position: f.Position, Company: f.Company, Nationality: f.Nationality}
		// A failed check shows the worker as selectable: the upload repeats the check and refuses.
		if b, err := a.Store.WorkerBlock(r.Context(), u.ID, f.PatientID); err != nil {
			log.Printf("mcusubmit: worker block: %v", err)
		} else {
			j.Blocked, j.Last, j.Until, j.From = b.Kind, b.Last, b.Until, b.From
		}
		out = append(out, j)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// ReviewPage shows one document the way it will be recorded, with the PDF a click away, so the
// submitter can check it before it joins the batch.
func (a *API) ReviewPage(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	id, ok := parseID(r.PathValue("id"))
	if !ok {
		a.notFound(w, u)
		return
	}
	docs, err := a.Store.ListOwn(r.Context(), u.ID)
	if err != nil {
		a.serverError(w, "list own", err)
		return
	}
	for _, d := range docs {
		if d.ID == id && (d.Status == SubReview || d.Status == SubDraft) {
			p := a.newPage("Review document")
			p.User, p.Docs = u, []Submission{d}
			a.render(w, http.StatusOK, "review.html", p)
			return
		}
	}
	a.notFound(w, u)
}

// ConfirmDocument is the submitter saying "this is right": Review becomes Draft and the document
// joins the batch.
func (a *API) ConfirmDocument(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	id, ok := parseID(r.PathValue("id"))
	if !ok {
		a.notFound(w, u)
		return
	}
	switch err := a.Store.ConfirmDraft(r.Context(), u.ID, id); {
	case err == nil:
		http.Redirect(w, r, basePath+"/batch", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		a.notFound(w, u)
	case errors.Is(err, ErrBadState):
		a.message(w, http.StatusConflict, "Not possible", "This document was already confirmed or sent.", u)
	default:
		a.serverError(w, "confirm document", err)
	}
}

// parseWorker validates the worker's details. It returns the row to store, or a message for the
// submitter. Company and nationality are pick-only: whatever arrives must exist in the database.
func (a *API) parseWorker(ctx context.Context, f map[string]string) (NewSubmission, string) {
	var in NewSubmission
	in.PatName = SanitizeName(f["name"])
	if in.PatName == "" {
		return in, "Enter the worker's name."
	}
	in.PatPosition = SanitizeJobTitle(f["position"])
	if in.PatPosition == "" {
		return in, "Enter the worker's position."
	}
	dob, err := time.Parse("2006-01-02", f["dob"])
	if err != nil {
		return in, "Enter the date of birth as a valid date."
	}
	now := a.Store.now()
	age := now.Year() - dob.Year()
	if now.Month() < dob.Month() || (now.Month() == dob.Month() && now.Day() < dob.Day()) {
		age--
	}
	if dob.After(now) || age < minWorkerAge || age > maxWorkerAge {
		return in, "The date of birth is not plausible for a worker (age " + strconv.Itoa(minWorkerAge) + " to " + strconv.Itoa(maxWorkerAge) + ")."
	}
	in.PatDateOfBirth = f["dob"]

	companyID, ok := parseID(f["company_id"])
	if !ok {
		return in, "Choose the company from the list."
	}
	name, err := a.companyName(ctx, companyID)
	if err != nil {
		return in, "Choose the company from the list."
	}
	in.CompanyID, in.PatCompanyText = companyID, name

	nat, err := a.nationalityName(ctx, f["nationality"])
	if err != nil {
		return in, "Choose the nationality from the list."
	}
	in.PatNationality = nat

	in.PatGender = cleanGender(f["gender"])
	if in.PatGender == "" {
		return in, "Choose the gender."
	}
	return parseIdentity(in, f["id_number"])
}

// parseIdentity checks the ID number against the nationality: an Indonesian citizen gives a
// 16-digit NIK, anyone else a passport number.
func parseIdentity(in NewSubmission, raw string) (NewSubmission, string) {
	id := strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(raw))
	if isIndonesian(in.PatNationality) {
		if !nikPattern.MatchString(id) {
			return in, "Enter the NIK as exactly 16 digits."
		}
		in.PatNik = id
		return in, ""
	}
	if !passportPattern.MatchString(id) {
		return in, "Enter the passport number as 6 to 12 letters or digits."
	}
	in.PatPassportNo = id
	return in, ""
}

func isIndonesian(nationality string) bool {
	n := strings.ToLower(strings.TrimSpace(nationality))
	return n == "indonesia" || n == "indonesian"
}

func (a *API) nationalityName(ctx context.Context, name string) (string, error) {
	var canonical string
	err := a.Store.DB.QueryRowContext(ctx, `SELECT Name FROM NationalityTable WHERE Name = ? AND Status = 'Active'`, name).Scan(&canonical)
	return canonical, err
}

// dropFile deletes a stored file after a failure. A failure here only leaves an orphan on disk,
// so it is logged, not shown.
func (a *API) dropFile(ref string) {
	if ref == "" {
		return
	}
	if err := a.pipeline.Store.Delete(context.Background(), ref); err != nil {
		log.Printf("mcusubmit: could not delete stored file: %v", err)
	}
}

// ---- send, remove, withdraw -------------------------------------------------------------------

// SendBatchSubmit is the only action that makes documents visible to staff.
func (a *API) SendBatchSubmit(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil || !parseForm(w, r) {
		return
	}
	id, ok := parseID(r.PostFormValue("batch"))
	if !ok {
		a.notFound(w, u)
		return
	}
	// Sending is the one step that cannot be taken back (the batch locks), so the first POST only
	// shows what would be sent; the second one, carrying confirm=yes, sends it.
	if r.PostFormValue("confirm") != "yes" {
		a.sendConfirmPage(w, r, u, id)
		return
	}
	switch _, err := a.Store.SendBatch(r.Context(), u.ID, id); {
	case err == nil:
		http.Redirect(w, r, basePath+"/documents", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		a.notFound(w, u)
	case errors.Is(err, ErrBatchSent):
		a.message(w, http.StatusConflict, "Cannot send", "This batch was already sent.", u)
	case errors.Is(err, ErrBatchEmpty):
		a.message(w, http.StatusConflict, "Cannot send", "This batch has no documents.", u)
	case errors.Is(err, ErrBatchUnfinished):
		a.message(w, http.StatusConflict, "Cannot send", "Confirm or discard the documents that still need your review first.", u)
	case errors.Is(err, ErrBadState):
		a.message(w, http.StatusConflict, "Cannot send", "Your account cannot send documents right now. Contact the medical team.", u)
	default:
		a.serverError(w, "send batch", err)
	}
}

// sendConfirmPage lists the documents of an unsent batch and asks once more. A batch that cannot be
// sent (empty, or a document still in Review) gets the same refusal as the real send would give.
func (a *API) sendConfirmPage(w http.ResponseWriter, r *http.Request, u *Submitter, batchID int64) {
	docs, err := a.Store.ListOwn(r.Context(), u.ID)
	if err != nil {
		a.serverError(w, "list own", err)
		return
	}
	var inBatch []Submission
	for _, d := range docs {
		if d.BatchID == batchID && (d.Status == SubDraft || d.Status == SubReview) {
			inBatch = append(inBatch, d)
		}
	}
	if len(inBatch) == 0 {
		a.message(w, http.StatusConflict, "Cannot send", "This batch is empty or was already sent.", u)
		return
	}
	for _, d := range inBatch {
		if d.Status == SubReview {
			a.message(w, http.StatusConflict, "Cannot send", "Confirm or discard the documents that still need your review first.", u)
			return
		}
	}
	p := a.newPage("Send the batch")
	p.User, p.Docs, p.BatchID, p.Caption = u, inBatch, batchID, "These documents will be sent"
	a.render(w, http.StatusOK, "sendconfirm.html", p)
}

// RemoveDocument deletes one Draft (row and file).
func (a *API) RemoveDocument(w http.ResponseWriter, r *http.Request) {
	a.changeDocument(w, r, a.Store.RemoveDraft, "/batch", "This document was already sent, so it cannot be removed. Withdraw it from My documents instead.")
}

// WithdrawDocument takes back a Pending document: the file is deleted, the row stays as an audit stub.
func (a *API) WithdrawDocument(w http.ResponseWriter, r *http.Request) {
	a.changeDocument(w, r, a.Store.Withdraw, "/documents", "Only a document that is waiting for review can be withdrawn.")
}

func (a *API) changeDocument(w http.ResponseWriter, r *http.Request, act func(context.Context, int64, int64) (string, error), next, conflict string) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	id, ok := parseID(r.PathValue("id"))
	if !ok {
		a.notFound(w, u)
		return
	}
	path, err := act(r.Context(), u.ID, id)
	switch {
	case err == nil:
		if a.pipeline != nil {
			a.dropFile(path)
		}
		http.Redirect(w, r, basePath+next, http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		a.notFound(w, u)
	case errors.Is(err, ErrBadState):
		a.message(w, http.StatusConflict, "Not possible", conflict, u)
	default:
		a.serverError(w, "change document", err)
	}
}

// ---- file -------------------------------------------------------------------------------------

// DocumentFile serves the submitter's own PDF. A document that is someone else's, missing,
// withdrawn or deleted all give the same 404, so ids cannot be probed (plan section 7). The
// response headers keep the browser from running anything in the file or caching a health record.
func (a *API) DocumentFile(w http.ResponseWriter, r *http.Request) {
	u := a.requireUser(w, r)
	if u == nil {
		return
	}
	id, ok := parseID(r.PathValue("id"))
	if !ok || a.pipeline == nil {
		a.notFound(w, u)
		return
	}
	sc, err := a.Store.ScopeFor(r.Context(), u)
	if err != nil {
		a.serverError(w, "scope", err)
		return
	}
	ref, err := a.Store.OwnFile(r.Context(), sc, id)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			log.Printf("mcusubmit: own file: %v", err)
		}
		a.notFound(w, u)
		return
	}
	rc, err := a.pipeline.Store.Open(r.Context(), ref)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			log.Printf("mcusubmit: open stored file: %v", err)
		}
		a.notFound(w, u)
		return
	}
	defer rc.Close()
	if err := a.Store.LogEvent(r.Context(), Event{ActorType: ActorSubmitter, ActorID: u.ID, EntityType: EntitySubmission, EntityID: id, Action: "FileViewed"}); err != nil {
		log.Printf("mcusubmit: log file view: %v", err)
	}
	servePDF(w, rc)
}
