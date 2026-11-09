// The detail dialog for one finding: its facts, Before / After photos, follow-ups, the close / reopen
// action, delete (admin only) and the change history. Every action asks the server and then redraws from
// the server's answer, so the dialog never shows a state the server did not accept.
//
// Needs corrective.js (window.CA), corrective-core.js, corrective-pic.js, corrective-photo.js.
(function () {
  "use strict";
  const CA = (window.CA = window.CA || {});
  const dialog = document.getElementById("ca-detail-dialog");
  if (!dialog) return;
  const body = document.getElementById("ca-detail-body");
  const note = document.getElementById("ca-detail-note");
  const el = CA.el;
  let currentId = 0;

  const say = (text) => (note.textContent = text || "");

  // The labels the history shows for the field names the server logs.
  const HISTORY_LABELS = {
    created: "Finding recorded", program: "Program", inspection_date: "Inspection date", location: "Location",
    finding: "Finding", recommendation: "Recommendation", pic: "PIC", due_date: "Due date", severity: "Severity",
    status: "Status", source_activity: "Source activity", closed: "Closed", reopened: "Reopened",
    photo_added: "Photo added", photo_removed: "Photo removed", follow_up_added: "Follow-up added",
    follow_up_done: "Follow-up done", follow_up_reopened: "Follow-up reopened", follow_up_removed: "Follow-up removed",
    follow_up_description: "Follow-up text", follow_up_pic: "Follow-up PIC", follow_up_due_date: "Follow-up due date",
  };

  function field(label, control) {
    const wrap = el("label", "ca-field");
    wrap.append(el("span", "", label), control);
    return wrap;
  }

  function input(type, value) {
    const node = el("input");
    node.type = type;
    if (value) node.value = value;
    return node;
  }

  function button(text, className, onClick) {
    const node = el("button", className || "btn-ghost", text);
    node.type = "button";
    if (onClick) node.addEventListener("click", onClick);
    return node;
  }

  // Runs one server call; on failure shows the server's reason and keeps the dialog as it is.
  async function act(method, url, payload, failure) {
    say("");
    const res = await CA.request(method, url, payload);
    if (!res.ok) {
      say(CA.errorText(res, failure));
      return false;
    }
    await changed();
    return true;
  }

  async function changed() {
    await refresh(currentId);
    CA.reload();
  }

  CA.openDetail = async function (id) {
    currentId = id;
    say("");
    body.textContent = "";
    body.append(el("p", "ca-note", "Loading…"));
    dialog.showModal();
    await refresh(id);
  };
  CA.refreshDetail = (id) => (dialog.open && currentId === id ? refresh(id) : undefined);

  async function refresh(id) {
    const res = await CA.request("GET", "/api/corrective-actions/" + id);
    if (!res.ok) {
      body.textContent = "";
      body.append(el("p", "ca-note", CA.errorText(res, "Could not load this finding.")));
      return;
    }
    render(res.data);
  }

  function render(a) {
    body.textContent = "";
    body.append(headerPart(a), factsPart(a), textsPart(a), photosPart(a, "Before"), photosPart(a, "After"), followUpsPart(a), closePart(a), historyPart(a));
  }

  function headerPart(a) {
    const head = el("header", "ca-detail-head");
    const title = el("h2", "ca-detail-title");
    title.id = "ca-detail-title";
    title.append("Finding " + a.id + " · " + a.programName + " ", CA.chip(a), " ", CA.severityChip(a.severity));
    const actions = el("div", "ca-actions");
    actions.append(button("Edit", "btn-ghost", () => CA.openForm(a, a.procId)));
    if (CA.isAdmin) {
      const del = button("Delete finding", "btn-ghost");
      CA.confirmButton(del, "Really delete? Photos and history go too.", async () => {
        const res = await CA.request("DELETE", "/api/corrective-actions/" + a.id);
        if (!res.ok) return say(CA.errorText(res, "Could not delete the finding."));
        dialog.close();
        CA.reload();
      });
      actions.append(del);
    }
    actions.append(button("Close window", "btn-ghost", () => dialog.close()));
    head.append(title, actions);
    return head;
  }

  function factsPart(a) {
    const list = el("dl", "ca-facts");
    const add = (label, value) => {
      if (value === "" || value === undefined) return;
      list.append(el("dt", "", label), el("dd", "", value));
    };
    add("Inspection date", caFormatDate(a.inspectionDate));
    add("Location", a.location);
    if (a.activityId) add("From activity", a.activityTitle + (a.activityDate ? " (" + caFormatDate(a.activityDate) + ")" : ""));
    add("PIC", a.picName + (a.picEmployeeId ? " (" + a.picEmployeeId + ")" : ""));
    add("Due date", caFormatDate(a.dueDate));
    add("Days open", String(a.daysOpen));
    add("Recorded by", a.createdByName);
    if (a.status === "Closed") {
      add("Closing date", caFormatDate(a.closingDate));
      add("Closed by", a.closedByName);
    }
    return list;
  }

  function textsPart(a) {
    const wrap = el("div", "ca-texts");
    [["Finding", a.finding], ["Recommendation", a.recommendation]].forEach(([label, text]) => {
      const block = el("section", "ca-section");
      block.append(el("h3", "", label), el("p", "ca-prewrap", text));
      wrap.append(block);
    });
    if (a.status === "Closed" && a.actionTaken) {
      const block = el("section", "ca-section");
      block.append(el("h3", "", "Action taken"), el("p", "ca-prewrap", a.actionTaken));
      wrap.append(block);
    }
    return wrap;
  }

  function photosPart(a, phase) {
    const list = phase === "After" ? a.afterPhotos : a.beforePhotos;
    const section = el("section", "ca-section");
    section.append(el("h3", "", phase + " photos (" + list.length + "/" + CA_MAX_PHOTOS_PER_PHASE + ")"));
    const grid = el("div", "ca-photo-grid");
    list.forEach((photo, i) => {
      const figure = el("figure", "ca-photo");
      const open = el("button", "ca-thumb ca-thumb-large");
      open.type = "button";
      open.setAttribute("aria-label", "View " + phase + " photo " + (i + 1));
      const img = document.createElement("img");
      img.src = photo.url + "?thumb=1";
      img.alt = phase + " photo " + (i + 1);
      img.loading = "lazy";
      open.append(img);
      open.addEventListener("click", () => CA.openPhoto(photo.url));
      const remove = button("Remove", "ca-btn-small");
      CA.confirmButton(remove, "Really remove?", () => act("DELETE", "/api/corrective-action-photos/" + photo.id, undefined, "Could not remove the photo."));
      figure.append(open, remove);
      grid.append(figure);
    });
    section.append(grid);

    const room = caPhotoRoom(a, phase);
    if (room > 0) {
      const status = el("span", "ca-note");
      status.setAttribute("role", "status");
      const picker = input("file");
      picker.accept = "image/jpeg,image/png";
      picker.multiple = room > 1;
      picker.addEventListener("change", async () => {
        const files = Array.from(picker.files || []);
        if (!files.length) return;
        if (files.length > room) {
          status.textContent = "You can add " + room + " more " + phase + " photo" + (room === 1 ? "" : "s") + ".";
          picker.value = "";
          return;
        }
        try {
          const res = await CA.uploadPhotos(a.id, phase, files, (text) => (status.textContent = text));
          if (!res.ok) throw new Error(CA.errorText(res, "Upload failed."));
        } catch (err) {
          status.textContent = err.message;
          picker.value = "";
          return;
        }
        await changed();
      });
      section.append(field("Add " + phase + " photo", picker), status);
    }
    return section;
  }

  // ---- follow-ups -------------------------------------------------------------------------------

  function followUpsPart(a) {
    const section = el("section", "ca-section");
    const locked = a.status === "Closed";
    section.append(el("h3", "", "Follow-ups" + (a.followUpsTotal ? " · " + caFollowUpSummary(a) : "")));
    if (locked) section.append(el("p", "ca-note", "This finding is closed. Reopen it to change its follow-ups."));
    a.followUps.forEach((f) => section.append(followUpRow(f, locked)));
    if (!a.followUps.length) {
      section.append(el("p", "ca-none", "No follow-ups. Add one when this finding needs several actions, each with its own person and due date."));
    }
    if (!locked) section.append(addFollowUpForm(a));
    return section;
  }

  function followUpRow(f, locked) {
    const box = el("div", "ca-followup");
    const head = el("div", "ca-followup-head");
    const done = f.status === "Done";
    const chipKey = done ? "closed" : f.isOverdue ? "overdue" : "open";
    head.append(el("strong", "", f.description), " ", el("span", "ca-chip ca-chip-" + chipKey, done ? "Done" : f.isOverdue ? "Overdue" : "Open"));
    const meta = f.picName + (f.picEmployeeId ? " (" + f.picEmployeeId + ")" : "") + (f.dueDate ? " · due " + caFormatDate(f.dueDate) : "");
    box.append(head, el("div", "ca-sub", meta));
    if (done) {
      box.append(el("div", "ca-sub", "Done " + caFormatDate(f.doneDate) + (f.doneByName ? " by " + f.doneByName : "") + ": " + f.actionTaken));
    }
    if (locked) return box;

    const panel = el("div", "ca-inline");
    panel.hidden = true;
    const actions = el("div", "ca-actions");
    if (!done) actions.append(button("Mark done", "ca-btn-small", () => showPanel(panel, doneForm(f, panel))));
    else actions.append(button("Reopen", "ca-btn-small", () => act("POST", "/api/corrective-action-follow-ups/" + f.id + "/reopen", undefined, "Could not reopen the follow-up.")));
    actions.append(button("Edit", "ca-btn-small", () => showPanel(panel, editForm(f, panel))));
    const remove = button("Remove", "ca-btn-small");
    CA.confirmButton(remove, "Really remove?", () => act("DELETE", "/api/corrective-action-follow-ups/" + f.id, undefined, "Could not remove the follow-up."));
    actions.append(remove);
    box.append(actions, panel);
    return box;
  }

  function showPanel(panel, content) {
    panel.textContent = "";
    panel.append(content);
    panel.hidden = false;
  }

  function doneForm(f, panel) {
    const form = el("form", "ca-form-inline");
    const date = input("date", clinicToday());
    date.required = true;
    const taken = el("textarea");
    taken.rows = 2;
    taken.required = true;
    taken.maxLength = 4000;
    const save = el("button", "btn-primary", "Mark done");
    save.type = "submit";
    form.append(field("Done date", date), field("Action taken", taken), save, button("Cancel", "btn-ghost", () => (panel.hidden = true)));
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      act("POST", "/api/corrective-action-follow-ups/" + f.id + "/done", { doneDate: date.value, actionTaken: taken.value }, "Could not mark the follow-up done.");
    });
    return form;
  }

  // A follow-up's PIC is picked from the employee list like the finding's; leaving it alone keeps it.
  function editForm(f, panel) {
    const form = el("form", "ca-form-inline");
    const text = input("text", f.description);
    text.required = true;
    text.maxLength = 4000;
    const due = input("date", f.dueDate);
    const picHost = el("div");
    const picker = caCreatePicPicker(picHost, { label: "Follow-up PIC" });
    picker.setSaved(f.picName, f.picEmployeeId);
    const save = el("button", "btn-primary", "Save");
    save.type = "submit";
    form.append(field("What must be done", text), field("Person in charge", picHost), field("Due date", due), save, button("Cancel", "btn-ghost", () => (panel.hidden = true)));
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      if (!picker.hasPick()) return picker.validate();
      const payload = { description: text.value, dueDate: due.value };
      if (picker.getId()) payload.picId = picker.getId();
      act("PUT", "/api/corrective-action-follow-ups/" + f.id, payload, "Could not save the follow-up.");
    });
    return form;
  }

  function addFollowUpForm(a) {
    const form = el("form", "ca-form-inline ca-add-followup");
    const text = input("text");
    text.required = true;
    text.maxLength = 4000;
    text.placeholder = "e.g. Buy and install shelves";
    const picHost = el("div");
    const picker = caCreatePicPicker(picHost, { label: "Follow-up PIC" });
    const due = input("date");
    const save = el("button", "btn-primary", "Add follow-up");
    save.type = "submit";
    form.append(field("New follow-up", text), field("Person in charge", picHost), field("Due date", due), save);
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      if (!picker.hasPick()) return picker.validate();
      act("POST", "/api/corrective-actions/" + a.id + "/follow-ups", { description: text.value, picId: picker.getId(), dueDate: due.value }, "Could not add the follow-up.");
    });
    return form;
  }

  // ---- close / reopen ---------------------------------------------------------------------------

  function closePart(a) {
    const section = el("section", "ca-section");
    if (a.status === "Closed") {
      section.append(el("h3", "", "Closed"), el("p", "", "Closed on " + caFormatDate(a.closingDate) + (a.closedByName ? " by " + a.closedByName : "") + "."));
      section.append(button("Reopen finding", "btn-ghost", () => act("POST", "/api/corrective-actions/" + a.id + "/reopen", undefined, "Could not reopen the finding.")));
      return section;
    }
    section.append(el("h3", "", "Close this finding"));
    const blockers = caCloseBlockers(a);
    if (blockers.length) {
      const list = el("ul", "ca-blockers");
      blockers.forEach((reason) => list.append(el("li", "", reason)));
      section.append(el("p", "ca-note", "Before it can be closed:"), list);
    }
    const form = el("form", "ca-form-inline");
    const date = input("date", clinicToday());
    date.required = true;
    const taken = el("textarea");
    taken.rows = 2;
    taken.required = true;
    taken.maxLength = 4000;
    const save = el("button", "btn-primary", "Close finding");
    save.type = "submit";
    save.disabled = blockers.length > 0;
    form.append(field("Closing date", date), field("Action taken", taken), save);
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      act("POST", "/api/corrective-actions/" + a.id + "/close", { closingDate: date.value, actionTaken: taken.value }, "Could not close the finding.");
    });
    section.append(form);
    return section;
  }

  // ---- history ----------------------------------------------------------------------------------

  const whenText = (stamp) => {
    const date = new Date(stamp + "Z"); // stored as UTC
    return isNaN(date) ? stamp : date.toLocaleString("en-GB", { timeZone: "Asia/Jayapura", dateStyle: "short", timeStyle: "short" });
  };
  const valueText = (value) => caFormatDate(value) || value;

  function historyPart(a) {
    const details = el("details", "ca-section ca-history");
    details.append(el("summary", "", "History"));
    const list = el("ul", "ca-history-list");
    details.append(list);
    let loaded = false;
    details.addEventListener("toggle", async () => {
      if (!details.open || loaded) return;
      loaded = true;
      const res = await CA.request("GET", "/api/corrective-actions/" + a.id + "/history");
      if (!res.ok) {
        list.append(el("li", "", CA.errorText(res, "Could not load the history.")));
        loaded = false;
        return;
      }
      res.data.forEach((h) => {
        const item = el("li");
        item.append(el("strong", "", HISTORY_LABELS[h.field] || h.field), " · " + whenText(h.changedAt) + (h.staffName ? " · " + h.staffName : ""));
        if (h.oldValue || h.newValue) {
          const change = h.oldValue ? valueText(h.oldValue) + " → " + valueText(h.newValue) : valueText(h.newValue);
          item.append(el("div", "ca-sub ca-prewrap", change));
        }
        list.append(item);
      });
    });
    return details;
  }
})();
