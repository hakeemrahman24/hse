// The Corrective actions tab: the table, its filters, and the small helpers the form and detail dialogs
// share (window.CA). It is shown in two places by app.js: inside one program (that program's findings) and
// on the all-programs home view (every finding, with a Program filter).
//
// Everything from the server is written with textContent, never innerHTML, so a finding's text cannot
// inject markup. Needs corrective-core.js.
(function () {
  "use strict";
  const CA = (window.CA = window.CA || {});
  const PAGE_SIZE = 100;

  CA.isAdmin = window.__USER_ROLE__ === "admin";

  // el("div", "class names", "text") -> element; text goes in as text, never as HTML.
  CA.el = function (tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined && text !== null) node.textContent = text;
    return node;
  };

  // One place that talks to the server. FormData is sent as is (the browser sets the multipart header);
  // anything else is sent as JSON. A 401 means the session ended, so go back to sign in.
  CA.request = async function (method, url, body) {
    const init = { method, credentials: "same-origin", headers: {} };
    if (body instanceof FormData) {
      init.body = body;
    } else if (body !== undefined) {
      init.headers["Content-Type"] = "application/json";
      init.body = JSON.stringify(body);
    }
    let res;
    try {
      res = await fetch(url, init);
    } catch (err) {
      return { ok: false, status: 0, data: { error: "Could not reach the server." } };
    }
    if (res.status === 401) {
      location.href = "/";
      return { ok: false, status: 401, data: {} };
    }
    const data = await res.json().catch(() => ({}));
    return { ok: res.ok, status: res.status, data };
  };

  CA.errorText = (res, fallback) => (res.data && res.data.error) || fallback;

  // Reduces each chosen photo under 600 KB in the browser, then uploads them in one request.
  CA.uploadPhotos = async function (actionId, phase, files, onStatus) {
    const form = new FormData();
    form.append("phase", phase);
    for (const file of files) {
      if (onStatus) onStatus("Reducing " + file.name + "…");
      const small = await caReducePhoto(file); // throws a message fit to show
      form.append("photos", small, small.name);
    }
    if (onStatus) onStatus("Uploading…");
    return CA.request("POST", "/api/corrective-actions/" + actionId + "/photos", form);
  };

  CA.uploadDocuments = function (actionId, files) {
    const form = new FormData();
    files.forEach((file) => form.append("documents", file, file.name));
    return CA.request("POST", "/api/corrective-actions/" + actionId + "/documents", form);
  };

  // A two-step button: the first click asks "are you sure" in place, the second does it. No browser
  // confirm() box, which the other pages in this app avoid too.
  CA.confirmButton = function (button, askLabel, onConfirm) {
    const original = button.textContent;
    let armed = false;
    let timer = null;
    button.addEventListener("click", () => {
      if (!armed) {
        armed = true;
        button.textContent = askLabel;
        button.classList.add("ca-danger");
        timer = setTimeout(reset, 4000);
        return;
      }
      reset();
      onConfirm();
    });
    function reset() {
      clearTimeout(timer);
      armed = false;
      button.textContent = original;
      button.classList.remove("ca-danger");
    }
    return button;
  };

  CA.chip = function (action) {
    return CA.el("span", "ca-chip ca-chip-" + caStatusKey(action), caStatusLabel(action));
  };

  CA.severityChip = (severity) => CA.el("span", "ca-chip ca-sev-" + String(severity).toLowerCase(), severity);

  CA.openPhoto = function (url) {
    const box = document.getElementById("lightbox");
    const img = document.getElementById("lightbox-img");
    if (!box || !img) return;
    img.src = url;
    box.showModal();
  };

  // Programs for the Program filter and the form's program choice.
  let programs = null;
  CA.loadPrograms = async function () {
    if (programs) return programs;
    const res = await CA.request("GET", "/api/procedures");
    programs = res.ok && Array.isArray(res.data) ? res.data : [];
    return programs;
  };

  const root = document.getElementById("tab-corrective");
  if (!root) return;
  const filterForm = document.getElementById("ca-filter");
  const programWrap = document.getElementById("ca-program-wrap");
  const programSelect = filterForm.elements.procId;
  const table = document.getElementById("ca-table");
  const body = document.getElementById("ca-body");
  const summary = document.getElementById("ca-summary");
  const empty = document.getElementById("ca-empty");
  const moreBtn = document.getElementById("ca-more");
  const exportLink = document.getElementById("ca-export");

  const state = { procId: 0, limit: PAGE_SIZE, actions: [] };
  CA.currentProgramId = () => state.procId;

  function readFilters() {
    const data = new FormData(filterForm);
    return {
      procId: state.procId || data.get("procId"),
      status: data.get("status"),
      severity: data.get("severity"),
      q: data.get("q"),
      from: data.get("from"),
      to: data.get("to"),
      limit: state.limit,
    };
  }

  // One compact cell: the text on a single line, cut with an ellipsis when it is long, with the whole text
  // in the hover title. The class (ca-w-sm / ca-w-md / ca-w-lg) sets the widest the cell may grow.
  function textCell(label, text, width) {
    const cell = CA.el("td");
    cell.dataset.label = label;
    const inner = CA.el("div", "ca-cell" + (width ? " " + width : ""), text);
    if (text) inner.title = text;
    cell.append(inner);
    return cell;
  }

  // The list shows no pictures: just how many there are (Before / After). They open in the details.
  function photosCell(action) {
    const cell = CA.el("td", "ca-center", caPhotoCounts(action));
    cell.dataset.label = "Photos";
    cell.title = caPhotoCountsTitle(action);
    return cell;
  }

  function row(action) {
    const tr = CA.el("tr", "ca-row");
    const finding = CA.el("td");
    finding.dataset.label = "Finding";
    const open = CA.el("button", "ca-link ca-cell ca-w-lg", action.finding);
    open.type = "button";
    open.title = action.finding;
    open.addEventListener("click", () => CA.openDetail(action.id));
    finding.append(open);

    // Status, severity and the follow-up count on one line, so a row stays one line high.
    const status = CA.el("td", "ca-status-cell");
    status.dataset.label = "Status";
    status.append(CA.chip(action), CA.severityChip(action.severity));
    const badge = caFollowUpBadge(action);
    if (badge) {
      const followUps = CA.el("span", "ca-fu-badge", badge);
      followUps.title = caFollowUpTitle(action);
      status.append(followUps);
    }

    const program = textCell("Program", action.programName, "ca-w-md");
    program.classList.add("ca-col-program");
    tr.append(
      program,
      textCell("Inspected", caFormatDate(action.inspectionDate)),
      textCell("Location", action.location, "ca-w-sm"),
      finding,
      textCell("Recommendation", action.recommendation, "ca-w-lg"),
      textCell("PIC", action.picName, "ca-w-md"),
      textCell("Due", caFormatDate(action.dueDate)),
      textCell("Closed", caFormatDate(action.closingDate)),
      photosCell(action),
      status,
    );
    // The whole row opens the details, not only the finding link (which stays for keyboard users).
    tr.addEventListener("click", (e) => {
      if (!e.target.closest("a, button")) CA.openDetail(action.id);
    });
    return tr;
  }

  function render() {
    table.classList.toggle("ca-hide-program", state.procId !== 0);
    body.textContent = "";
    state.actions.forEach((action) => body.append(row(action)));
    summary.textContent = caSummaryLine(state.actions);
    empty.hidden = state.actions.length > 0;
    empty.textContent = "No findings match. Use “Add finding” to record one.";
    table.hidden = state.actions.length === 0;
    moreBtn.hidden = state.actions.length < state.limit;
  }

  CA.reload = async function () {
    const filters = readFilters();
    // The Excel file follows the filters last applied, so it holds what the table shows (the server
    // caps it at 500 findings and says so inside the file).
    exportLink.href = "/api/corrective-actions.xlsx?" + caListQuery({ ...filters, limit: "" });
    const res = await CA.request("GET", "/api/corrective-actions?" + caListQuery(filters));
    if (!res.ok) {
      state.actions = [];
      render();
      empty.hidden = false;
      empty.textContent = CA.errorText(res, "Could not load findings.");
      return;
    }
    state.actions = res.data;
    render();
  };

  // Called by app.js when the tab opens. procId 0 = every program (the home view).
  CA.show = async function (procId) {
    state.procId = Number(procId) || 0;
    state.limit = PAGE_SIZE;
    programWrap.hidden = state.procId !== 0;
    if (state.procId === 0 && programSelect.options.length <= 1) {
      (await CA.loadPrograms()).forEach((p) => {
        const option = document.createElement("option");
        option.value = p.procId;
        option.textContent = p.title || p.healthProcedure;
        programSelect.append(option);
      });
    }
    await CA.reload();
  };

  filterForm.addEventListener("submit", (e) => {
    e.preventDefault();
    state.limit = PAGE_SIZE;
    CA.reload();
  });
  document.getElementById("ca-reset").addEventListener("click", () => {
    filterForm.reset();
    state.limit = PAGE_SIZE;
    CA.reload();
  });
  moreBtn.addEventListener("click", () => {
    state.limit += PAGE_SIZE;
    CA.reload();
  });
  document.getElementById("ca-add").addEventListener("click", () => CA.openForm(null, state.procId));
})();
