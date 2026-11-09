// The add / edit dialog for one finding. Adding can attach up to three Before photos (reduced under
// 600 KB in the browser first); editing leaves photos to the detail dialog. The PIC is picked from the
// employee list (corrective-pic.js), never typed.
//
// Needs corrective.js (window.CA), corrective-core.js, corrective-pic.js.
(function () {
  "use strict";
  const CA = (window.CA = window.CA || {});
  const dialog = document.getElementById("ca-form-dialog");
  if (!dialog) return;
  const form = document.getElementById("ca-form");
  const title = document.getElementById("ca-form-title");
  const message = document.getElementById("ca-form-msg");
  const photosWrap = document.getElementById("ca-form-photos-wrap");
  const statusWrap = document.getElementById("ca-form-status-wrap");
  const saveBtn = document.getElementById("ca-form-save");
  let picker = null;
  const sourceNote = document.getElementById("ca-form-source");
  let editing = null; // the finding being edited, or null when adding
  let linkedActivityId = 0; // the activity this finding comes from, 0 = none (kept on edit, or set by "Report finding")

  async function fillPrograms(selected) {
    const select = form.elements.procId;
    select.textContent = "";
    (await CA.loadPrograms()).forEach((p) => {
      const option = document.createElement("option");
      option.value = p.procId;
      option.textContent = p.title || p.healthProcedure;
      select.append(option);
    });
    if (selected) select.value = String(selected);
  }

  // action = the finding to edit, or null to add. procId = the program to start on when adding (0 = none).
  // source = optional {activityId, title, date, location} when the finding is raised from an activity: the
  // program is then fixed (the server only accepts an activity of the same program) and the form starts
  // with the activity's date and location.
  CA.openForm = async function (action, procId, source) {
    editing = action;
    linkedActivityId = action ? action.activityId || 0 : source ? source.activityId : 0;
    form.reset();
    message.textContent = "";
    saveBtn.disabled = false;
    title.textContent = action ? "Edit finding " + action.id : "Add finding";
    await fillPrograms(action ? action.procId : procId);

    picker = caCreatePicPicker(document.getElementById("ca-form-pic"), { label: "Person in charge (PIC)" });
    if (action) {
      form.elements.inspectionDate.value = action.inspectionDate;
      form.elements.location.value = action.location;
      form.elements.severity.value = action.severity;
      form.elements.dueDate.value = action.dueDate;
      form.elements.finding.value = action.finding;
      form.elements.recommendation.value = action.recommendation;
      picker.setSaved(action.picName, action.picEmployeeId);
    } else {
      form.elements.inspectionDate.value = (source && source.date) || clinicToday();
      if (source && source.location) form.elements.location.value = source.location;
    }
    // A linked finding keeps its program, and says where it came from.
    form.elements.procId.disabled = Boolean(linkedActivityId);
    const sourceTitle = action ? action.activityTitle : source ? source.title : "";
    sourceNote.hidden = !linkedActivityId;
    sourceNote.textContent = linkedActivityId ? "From activity: " + sourceTitle : "";
    // Photos are added here only for a new finding; an existing one manages them in its detail view.
    photosWrap.hidden = Boolean(action);
    // The status can be typed only while the finding is not closed and has no follow-ups; with
    // follow-ups it follows them (the server enforces the same).
    const typedStatus = action && action.status !== "Closed" && !(action.followUps || []).length;
    statusWrap.hidden = !typedStatus;
    if (typedStatus) form.elements.status.value = action.status;
    dialog.showModal();
    form.elements.inspectionDate.focus();
  };

  function payload() {
    const data = new FormData(form);
    const body = {
      // Read from the field, not FormData: a linked finding's program select is disabled.
      procId: Number(form.elements.procId.value),
      activityId: linkedActivityId,
      inspectionDate: data.get("inspectionDate"),
      location: data.get("location"),
      severity: data.get("severity"),
      dueDate: data.get("dueDate"),
      finding: data.get("finding"),
      recommendation: data.get("recommendation"),
    };
    // picId is sent only for a fresh pick; an edit that leaves the PIC alone omits it so the server keeps it.
    if (picker.getId()) body.picId = picker.getId();
    if (!statusWrap.hidden) body.status = data.get("status");
    return body;
  }

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    if (!picker.hasPick()) {
      picker.validate();
      return;
    }
    if (!form.reportValidity()) return;
    const chosen = editing ? [] : Array.from(form.elements.beforePhotos.files || []);
    if (chosen.length > CA_MAX_PHOTOS_PER_PHASE) {
      message.textContent = "Choose at most " + CA_MAX_PHOTOS_PER_PHASE + " Before photos.";
      return;
    }
    saveBtn.disabled = true;
    message.textContent = "Saving…";
    const body = payload();
    const res = editing
      ? await CA.request("PUT", "/api/corrective-actions/" + editing.id, body)
      : await CA.request("POST", "/api/corrective-actions", body);
    if (!res.ok) {
      message.textContent = CA.errorText(res, "Could not save the finding.");
      saveBtn.disabled = false;
      return;
    }
    const saved = res.data;
    const files = editing ? [] : Array.from(form.elements.beforePhotos.files || []);
    if (files.length) {
      try {
        const up = await CA.uploadPhotos(saved.id, "Before", files, (text) => (message.textContent = text));
        if (!up.ok) throw new Error(CA.errorText(up, "Upload failed."));
      } catch (err) {
        // The finding itself is saved; say so, so nobody records it twice.
        message.textContent = "The finding was saved, but the photos were not: " + err.message + " Add them from the finding.";
        saveBtn.disabled = false;
        await CA.reload();
        return;
      }
    }
    dialog.close();
    await CA.reload();
    if (!editing) CA.openDetail(saved.id);
    else if (CA.refreshDetail) CA.refreshDetail(saved.id);
  });

  document.getElementById("ca-form-cancel").addEventListener("click", () => dialog.close());
})();
