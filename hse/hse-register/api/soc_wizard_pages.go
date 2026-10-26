package main

import (
	"encoding/json"
	"errors"
	"log"
	"mime/multipart"
	"net/http"
	"strconv"
	"time"
)

// wizOption is one choice of a picklist screen. Value is the 1-based position in the list (what the
// form posts); PPE marks the answers that make the form ask for a PPE item.
type wizOption struct {
	Value, Label string
	PPE          bool
}

// wizardPage is what the wizard template needs besides the shared page data.
type wizardPage struct {
	Input                                             wizardInput
	AreaFromQR                                        bool          // the area came from a QR code, so the area question is skipped
	SubAreaFromQR                                     bool          // so did the sub-area
	SubAreasJSON                                      string        // {"Port": ["Gate 1", "Gate 2"]}, for the sub-area screen
	SubOptions                                        []string      // the sub-areas of the area already chosen (a QR code, or the form shown again)
	Topics                                            []topicChoice // hazard topics, for "choose from the list"
	UnsafeReasons, PPEItems, StoppedBy, GoodPractices []wizOption
}

func wizOptions(ref *refSnapshot, list string) []wizOption {
	opts := ref.Picklists[list]
	out := make([]wizOption, len(opts))
	for i, o := range opts {
		out[i] = wizOption{Value: strconv.Itoa(i + 1), Label: o.Label, PPE: o.ControlTag == controlPPE}
	}
	return out
}

// wizardData fills the wizard page. fromQR says the area came from a QR code.
func (h *socHandlers) wizardData(r *http.Request, sess *socSession, in wizardInput, errs map[string]string, fromQR, subFromQR bool) (socPageData, error) {
	snap, err := h.ref.Get(r.Context())
	if err != nil {
		return socPageData{}, err
	}
	subs, _ := json.Marshal(snap.SubAreas) // strings only: cannot fail
	w := &wizardPage{Input: in, AreaFromQR: fromQR, SubAreaFromQR: subFromQR, SubAreasJSON: string(subs), SubOptions: snap.SubAreas[in.Area],
		UnsafeReasons: wizOptions(snap, listUnsafeReason), PPEItems: wizOptions(snap, listPPEItem),
		StoppedBy: wizOptions(snap, listStoppedBy), GoodPractices: wizOptions(snap, listGoodPractice)}
	for _, g := range snap.TopicGroups() {
		if g.Kind == "Hazard" {
			w.Topics = g.Items
		}
	}
	return socPageData{
		Title: "Observation", Session: sess, Errors: errs, Wiz: w,
		Areas: snap.Areas, Companies: h.companies.List(r.Context()), PhotoSlots: maxPhotosPerPost,
	}, nil
}

func (h *socHandlers) showWizard(w http.ResponseWriter, r *http.Request, status int, sess *socSession, in wizardInput, errs map[string]string, fromQR, subFromQR, serverFailed bool) {
	data, err := h.wizardData(r, sess, in, errs, fromQR, subFromQR)
	if err != nil {
		log.Printf("SOC form lists: %v", err)
		http.Error(w, tr(langOf(r), "The form is not available right now. Please try again later."), http.StatusServiceUnavailable)
		return
	}
	data.ServerFailed = serverFailed
	renderSOC(w, r, status, "soc_wizard", data)
}

// wizardPage opens the wizard. ?area=<name> (from a QR code on site) fills the area and skips that
// question; an area that is not in the list is ignored.
func (h *socHandlers) wizardPage(w http.ResponseWriter, r *http.Request) {
	sess, ok := h.session(r)
	if !ok {
		http.Redirect(w, r, "/soc", http.StatusSeeOther)
		return
	}
	var in wizardInput
	fromQR, subFromQR := false, false
	if area := r.URL.Query().Get("area"); area != "" {
		if snap, err := h.ref.Get(r.Context()); err == nil {
			if canon, ok := snap.CanonicalArea(area); ok {
				in.Area, fromQR = canon, true
				if sub, ok := snap.CanonicalSubArea(canon, r.URL.Query().Get("sub")); ok {
					in.SubArea, subFromQR = sub, true
				}
			}
		}
	}
	h.showWizard(w, r, http.StatusOK, sess, in, nil, fromQR, subFromQR, false)
}

func (h *socHandlers) wizardSubmit(w http.ResponseWriter, r *http.Request) {
	sess, ok := h.session(r)
	if !ok {
		http.Redirect(w, r, "/soc", http.StatusSeeOther)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPhotosPerPost*photoMaxUploadBytes+(256<<10))
	if err := r.ParseMultipartForm(1 << 20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		http.Error(w, tr(langOf(r), "The form or the photos are too large."), http.StatusRequestEntityTooLarge)
		return
	}
	var uploads []*multipart.FileHeader
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll() // delete the parser's temporary files
		uploads = r.MultipartForm.File["photos"]
	}
	in := wizardInput{
		Path: r.FormValue("path"), Area: r.FormValue("area"), SubArea: r.FormValue("sub_area"), Company: r.FormValue("company"), AreaOwner: r.FormValue("area_owner"),
		Work: r.FormValue("work"), Condition: r.FormValue("condition"), Improve: r.FormValue("improve"),
		TopicID: r.FormValue("topic_id"), HazardID: r.FormValue("hazard_id"),
		UnsafeReason: r.FormValue("unsafe_reason"), PPEItem: r.FormValue("ppe_item"),
		StopWork: r.FormValue("stop_work"), StoppedBy: r.FormValue("stopped_by"), ShouldStop: r.FormValue("should_stop"),
		Danger: r.FormValue("danger"), Barricaded: r.FormValue("barricaded"), GoodPractice: r.FormValue("good_practice"),
		Corrective: r.FormValue("corrective"), Recommendation: r.FormValue("recommendation"),
	}
	snap, err := h.ref.Get(r.Context())
	if err != nil {
		log.Printf("SOC form lists: %v", err)
		http.Error(w, tr(langOf(r), "The form is not available right now. Please try again later."), http.StatusServiceUnavailable)
		return
	}
	sub, errs := validateWizard(in, time.Now().UTC().Truncate(24*time.Hour), snap, h.companies.List(r.Context()))
	if len(errs) > 0 {
		h.showWizard(w, r, http.StatusUnprocessableEntity, sess, in, errs, false, false, false)
		return
	}
	// WHY: reporter fields come from the verified session, never from the form.
	sub.EmployeeID, sub.ReporterName, sub.Designation, sub.Company = sess.EmployeeID, sess.Name, sess.Designation, sess.Company
	h.saveWithPhotos(w, r, sub, uploads, nil, func(status int, errs map[string]string, serverFailed bool) {
		h.showWizard(w, r, status, sess, in, errs, false, false, serverFailed)
	})
}
