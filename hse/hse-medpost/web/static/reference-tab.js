// Reference tab on the Medical Record page (plan/emr-plan.md section 11).
//
// Each screening section (Drug and Alcohol, Fatigue, Malaria) has a Reference tab
// listing the links and offline forms an admin has attached to it. Everyone signed
// in can search the list and open an item; only an admin sees the buttons that
// change it. Hiding a button is a convenience, not the protection: the server
// refuses every change from a non-admin (internal/activity-log/reference_handlers.go).
//
// screening-log.js and malaria.js call window.loadReference(container, section)
// when their Reference tab is shown. The Add dialog is in reference-add.js.
//
// Everything that comes from the server (titles, notes, names) is put on the page
// with textContent or a text node, never as HTML, so a title can't inject markup.

const REFERENCE_MAX_BYTES = 10 * 1024 * 1024; // same cap the server enforces
const REFERENCE_EXTS = [".pdf", ".docx", ".xlsx", ".png", ".jpg", ".jpeg"];
const REFERENCE_FILE_ACCEPT = REFERENCE_EXTS.join(",");
const REFERENCE_TYPE_LABELS = {
  Procedure: "Procedure",
  SocializationMaterial: "Socialization material",
  Forms: "Form",
  Template: "Template",
  Website: "Website",
};
const REFERENCE_SECTION_LABELS = {
  outpatient: "Outpatient",
  inpatient: "Inpatient",
  wellness: "Wellness",
  alcohol: "Drug and Alcohol",
  fatigue: "Fatigue",
  malaria: "Malaria",
};
const REFERENCE_CONFIRM_MS = 4000;

// ---- pure helpers (tested in web/reference-tab.test.js) -------------------

function referenceTypeLabel(type) {
  return REFERENCE_TYPE_LABELS[type] || type;
}

function referenceSectionLabel(key) {
  return REFERENCE_SECTION_LABELS[key] || key;
}

// referenceSafeHref returns the address only when it is a plain http(s) link.
// A javascript: or data: address would run script when clicked, so anything else
// gives "" and the caller shows the text without a link.
function referenceSafeHref(url) {
  const text = String(url == null ? "" : url).trim();
  return /^https?:\/\/\S+$/i.test(text) ? text : "";
}

function referenceDate(iso) {
  return typeof isoToDmy === "function" ? isoToDmy(iso) : iso;
}

// The small grey line under a title: "File · v3", "Updated 05/11/2026" or "Link".
function referenceItemMeta(item) {
  if (!item.hasFile) return ["Link"];
  const parts = ["File · v" + item.versionNo];
  if (item.updatedDate) parts.push("Updated " + referenceDate(item.updatedDate));
  return parts;
}

// Returns a new list; the original is untouched.
function referenceFilterItems(items, query) {
  const q = String(query || "").trim().toLowerCase();
  if (!q) return items.slice();
  return items.filter((item) => {
    const haystack = [item.title, item.notes, referenceTypeLabel(item.documentsType)].join(" ").toLowerCase();
    return haystack.includes(q);
  });
}

// Checks a chosen file before it is sent, so a mistake is caught without a
// 10 MB upload. The server checks the same things again; this is only for speed.
function referenceFileProblem(file) {
  if (!file) return "Choose a file.";
  const name = String(file.name || "").toLowerCase();
  const dot = name.lastIndexOf(".");
  if (!REFERENCE_EXTS.includes(dot >= 0 ? name.slice(dot) : "")) return "Use a PDF, DOCX, XLSX, PNG or JPG file.";
  if (file.size === 0) return "The file is empty.";
  if (file.size > REFERENCE_MAX_BYTES) return "The file is larger than 10 MB.";
  return "";
}

// ---- small DOM and network helpers (shared with reference-add.js) ---------

// refEl builds an element. Strings passed as children become text nodes, never HTML.
function refEl(tag, props, ...children) {
  const el = document.createElement(tag);
  for (const [key, value] of Object.entries(props || {})) {
    if (key === "class") el.className = value;
    else if (key === "text") el.textContent = value;
    else if (key.startsWith("on")) el.addEventListener(key.slice(2), value);
    else if (value === true) el.setAttribute(key, "");
    else if (value !== false && value != null) el.setAttribute(key, value);
  }
  for (const child of children.flat()) if (child != null && child !== false) el.append(child);
  return el;
}

// Always resolves: { ok, status, body }, with status 0 when the server can't be reached.
async function referenceRequest(url, options) {
  try {
    const res = await fetch(url, options);
    let body = null;
    try {
      body = await res.json();
    } catch {
      body = null;
    }
    return { ok: res.ok, status: res.status, body };
  } catch {
    return { ok: false, status: 0, body: null };
  }
}

function referenceMessage(result, fallback) {
  if (result.body && result.body.error) return result.body.error;
  return result.status === 0 ? "Could not reach the server." : fallback;
}

// ---- the tab --------------------------------------------------------------

// One state object per tab container, so a search term and the open History
// panels survive a reload of the list after an action.
const referenceStates = new WeakMap();

function referenceSay(state, text, isError) {
  state.statusEl.textContent = text;
  state.statusEl.classList.toggle("ref-status--error", Boolean(isError));
}

// A button that needs a second click to act: the first click changes its label,
// and it goes back after a few seconds if nothing follows.
function referenceConfirmButton(label, onConfirm) {
  const button = refEl("button", { type: "button", class: "btn-ghost ref-btn ref-btn--danger", text: label });
  let timer = null;
  button.addEventListener("click", () => {
    if (timer === null) {
      button.textContent = "Click again to confirm";
      timer = setTimeout(() => {
        button.textContent = label;
        timer = null;
      }, REFERENCE_CONFIRM_MS);
      return;
    }
    clearTimeout(timer);
    timer = null;
    button.textContent = label;
    onConfirm();
  });
  return button;
}

function referenceButton(label, onClick, title) {
  return refEl("button", { type: "button", class: "btn-ghost ref-btn", text: label, title, onclick: onClick });
}

async function referenceReload(state) {
  const seq = ++state.seq;
  const res = await referenceRequest("/api/reference/" + state.section);
  if (seq !== state.seq) return; // a newer request has already replaced this one
  if (!res.ok) {
    state.items = [];
    referenceSay(state, referenceMessage(res, "Could not load the references."), true);
  } else {
    state.items = res.body.items || [];
    state.canManage = Boolean(res.body.canManage);
  }
  state.addButton.hidden = !state.canManage;
  referenceRenderList(state);
}

async function referenceRun(state, request, successText) {
  const res = await request;
  referenceSay(state, res.ok ? successText(res) : referenceMessage(res, "That did not work."), !res.ok);
  if (res.ok) await referenceReload(state);
}

function referenceReplaceFile(state, item) {
  const input = refEl("input", { type: "file", accept: REFERENCE_FILE_ACCEPT });
  input.addEventListener("change", () => {
    const file = input.files[0];
    const problem = referenceFileProblem(file);
    if (problem) return referenceSay(state, problem, true);
    const form = new FormData();
    form.append("file", file);
    const shared = item.alsoIn.length ? " It is also shown in " + item.alsoIn.map(referenceSectionLabel).join(", ") + "." : "";
    referenceRun(
      state,
      referenceRequest(`/api/reference/documents/${item.documentId}/file`, { method: "POST", body: form }),
      (res) => `Replaced "${item.title}": now version ${res.body.versionNo}.${shared}`,
    );
  });
  input.click();
}

function referenceRenameForm(state, item, titleEl) {
  const input = refEl("input", { type: "text", class: "ref-rename", value: item.title, maxlength: "200", "aria-label": "Title" });
  const save = () => {
    const title = input.value.trim();
    if (!title) return referenceSay(state, "Enter a title.", true);
    referenceRun(
      state,
      referenceRequest("/api/documents/" + item.documentId, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ documentTitle: title }),
      }),
      () => "Title changed.",
    );
  };
  input.addEventListener("keydown", (e) => {
    if (e.key === "Enter") save();
    if (e.key === "Escape") referenceRenderList(state);
  });
  titleEl.replaceChildren(input, referenceButton("Save", save), referenceButton("Cancel", () => referenceRenderList(state)));
  input.focus();
}

async function referenceFillHistory(state, item, panel) {
  panel.textContent = "Loading…";
  const res = await referenceRequest(`/api/reference/documents/${item.documentId}/versions`);
  if (!res.ok) {
    panel.textContent = referenceMessage(res, "Could not load the history.");
    return;
  }
  const rows = res.body.versions.map((v) =>
    refEl(
      "li",
      {},
      `v${v.versionNo} · ${referenceDate(v.uploadDate)} · ${v.uploadedBy || "unknown"} · ${v.originalName || "file"}`,
      v.isCurrent ? refEl("strong", { text: " (current)" }) : null,
      " ",
      refEl("a", { href: `/api/reference/documents/${item.documentId}/versions/${v.versionNo}/file`, text: "Download" }),
    ),
  );
  panel.replaceChildren(refEl("ul", { class: "ref-history-list" }, rows));
}

function referenceToggleHistory(state, item) {
  if (state.historyOpen.has(item.documentId)) state.historyOpen.delete(item.documentId);
  else state.historyOpen.add(item.documentId);
  referenceRenderList(state);
}

function referenceItemLinks(item) {
  const links = [];
  if (item.hasFile) {
    links.push(refEl("a", { class: "btn-ghost ref-btn", href: `/api/documents/${item.documentId}/file`, target: "_blank", rel: "noopener noreferrer", text: "Open file" }));
  }
  const href = referenceSafeHref(item.formLink);
  if (href) links.push(refEl("a", { class: "btn-ghost ref-btn", href, target: "_blank", rel: "noopener noreferrer", text: "Open link" }));
  return links;
}

function referenceAdminActions(state, item, titleEl) {
  const section = state.section;
  const sharedNote = item.alsoIn.length ? "Replaces the file everywhere it is shown" : "Upload a new version";
  return [
    referenceButton("Replace file", () => referenceReplaceFile(state, item), sharedNote),
    referenceButton(state.historyOpen.has(item.documentId) ? "Hide history" : "History", () => referenceToggleHistory(state, item)),
    referenceButton("Rename", () => referenceRenameForm(state, item, titleEl)),
    referenceButton(
      "Remove from tab",
      () =>
        referenceRun(
          state,
          referenceRequest(`/api/reference/${section}/documents/${item.documentId}`, { method: "DELETE" }),
          () => `Removed "${item.title}" from this tab. It is still in the library.`,
        ),
      "Takes it off this tab only",
    ),
    referenceConfirmButton("Delete", () =>
      referenceRun(
        state,
        referenceRequest(`/api/reference/documents/${item.documentId}?section=${encodeURIComponent(section)}`, { method: "DELETE" }),
        () => `Deleted "${item.title}".`,
      ),
    ),
  ];
}

function referenceRenderItem(state, item) {
  const titleEl = refEl("h3", { class: "ref-title", text: item.title });
  const meta = [referenceTypeLabel(item.documentsType), ...referenceItemMeta(item)].join(" · ");
  const main = refEl("div", { class: "ref-main" }, titleEl, refEl("p", { class: "ref-meta", text: meta }));
  if (item.notes) main.append(refEl("p", { class: "ref-notes", text: item.notes }));
  if (item.alsoIn.length) {
    main.append(refEl("p", { class: "ref-also", text: "Also shown in: " + item.alsoIn.map(referenceSectionLabel).join(", ") }));
  }
  const actions = refEl("div", { class: "ref-actions" }, referenceItemLinks(item), state.canManage ? referenceAdminActions(state, item, titleEl) : []);
  const li = refEl("li", { class: "ref-item" }, main, actions);
  if (state.canManage && state.historyOpen.has(item.documentId)) {
    const panel = refEl("div", { class: "ref-history" });
    li.append(panel);
    referenceFillHistory(state, item, panel);
  }
  return li;
}

function referenceRenderList(state) {
  const shown = referenceFilterItems(state.items, state.query);
  if (shown.length === 0) {
    let text = "No references match your search.";
    if (state.items.length === 0) text = state.canManage ? "No references yet. Use Add reference to upload a form or add a link." : "No references yet.";
    state.listEl.replaceChildren(refEl("li", { class: "app-empty", text }));
    return;
  }
  state.listEl.replaceChildren(...shown.map((item) => referenceRenderItem(state, item)));
}

function referenceBuildShell(container, state) {
  state.statusEl = refEl("p", { class: "ref-status", role: "status" });
  state.listEl = refEl("ul", { class: "ref-list" });
  state.addButton = refEl("button", {
    type: "button",
    class: "btn-primary ref-add",
    text: "Add reference",
    hidden: true,
    onclick: () => window.openReferenceAdd?.(state.section, (message) => {
      referenceSay(state, message, false);
      referenceReload(state);
    }),
  });
  const search = refEl("input", { type: "search", class: "ref-search", placeholder: "Search references", "aria-label": "Search references" });
  search.addEventListener("input", () => {
    state.query = search.value;
    referenceRenderList(state);
  });
  container.replaceChildren(refEl("div", { class: "ref-toolbar" }, search, state.addButton), state.statusEl, state.listEl);
}

// Called by screening-log.js / malaria.js whenever their Reference tab is shown.
// Starts the tab fresh (empty search, no open History) and loads the list.
if (typeof document !== "undefined") {
  window.loadReference = function (container, section) {
    if (!container) return Promise.resolve();
    const state = { section, query: "", items: [], canManage: false, historyOpen: new Set(), seq: 0 };
    referenceStates.set(container, state);
    referenceBuildShell(container, state);
    return referenceReload(state);
  };
}
