const ROLE = window.__USER_ROLE__ || "staff";

async function logout() {
  await fetch("/api/logout", { method: "POST", credentials: "same-origin" });
  location.href = "/";
}
document.getElementById("logout")?.addEventListener("click", logout);

// Live sanitizing lives in case-guard.js: names keep letters, apostrophe, hyphen and dot, work
// positions keep digits (Welder 3G), both title-cased with extra spaces gone.
guardCase(document.querySelector('#patient-form [name="name"]'), sanitizeName);
guardCase(document.querySelector('#checkup-form [name="workPosition"]'), sanitizePosition);

function escapeHtml(s) {
  return String(s ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

// Shared debounce for every live-as-you-type filter on this page (checkup
// search, MCU data tab's name filter).
function debounce(fn, ms) {
  let timer = null;
  return (...args) => {
    clearTimeout(timer);
    timer = setTimeout(() => fn(...args), ms);
  };
}

function activeLookups(items, selectedValue, valueField) {
  const active = items.filter((item) => item.status !== "Archived");
  if (selectedValue === undefined || selectedValue === null || selectedValue === "") return active;
  const selected = items.find((item) => String(item[valueField]) === String(selectedValue));
  if (selected && selected.status === "Archived" && !active.includes(selected)) {
    return active.concat(selected);
  }
  return active;
}

function fillSelect(el, items, valueField, labelField, placeholder) {
  if (!el) return;
  const keep = el.value;
  el.innerHTML =
    (placeholder !== undefined ? `<option value="">${escapeHtml(placeholder)}</option>` : "") +
    items.map((it) => `<option value="${it[valueField]}">${escapeHtml(it[labelField])}</option>`).join("");
  el.value = keep;
}

let nationalityCache = [];
let companyCache = [];
// Every CompanyRelation row as-is (companyId, companyRelationId, parentId,
// parentName, project), backing the checkup form's Parent/Project picker —
// shown only when the visit's company has more than one row (Salawati
// Motorindo in production, see company/seed.go's seedCompanyRelations).
// Populated in loadLookups.
let companyRelationCache = [];
let checkUpCriteriaCache = [];
let taskCategoryCache = [];
let reviewResultCache = [];
let workLocationCache = [];
let currentPatient = null;
let patientHistoryCache = []; // most recent first (server already orders by ReviewDate DESC), set by loadHistory
// Where checkup-cancel (the checkup form's Close button) should return to:
// "table" when the checkup was opened straight from the Data entry table's
// Checkup ID link, "patientDetail" when opened via that patient's own
// history ("+ New checkup" / an Edit link in loadHistory below).
let checkupReturnTo = "table";
// true while #patient-form is shown *alongside* an open #checkup-form (see
// openCheckupForm's new-checkup case) rather than on its own — changes what
// submitting the patient form does (see patientForm's submit handler).
let patientFormInline = false;
// New patient opens the employee fields and a blank checkup together.
// Cancel sits at the top; one Save at the bottom writes both.
let newPatientCombined = false;

async function loadLookups() {
  const [nat, co, crit, task, results, locations, relations] = await Promise.all([
    fetch("/api/nationalities", { credentials: "same-origin" }).then((r) => r.json()),
    fetch("/api/companies", { credentials: "same-origin" }).then((r) => r.json()),
    fetch("/api/checkup-criteria", { credentials: "same-origin" }).then((r) => r.json()),
    fetch("/api/task-categories", { credentials: "same-origin" }).then((r) => r.json()),
    fetch("/api/review-result-criteria", { credentials: "same-origin" }).then((r) => r.json()),
    fetch("/api/work-locations", { credentials: "same-origin" }).then((r) => r.json()),
    fetch("/api/company-relations", { credentials: "same-origin" }).then((r) => r.json()),
  ]);
  nationalityCache = nat;
  companyCache = co;
  checkUpCriteriaCache = crit;
  taskCategoryCache = task;
  reviewResultCache = results;
  workLocationCache = locations;
  companyRelationCache = relations;

  fillSelect(document.getElementById("patient-nationality"), activeLookups(nationalityCache, "", "nationalityId"), "nationalityId", "name", "—");
  fillCriteriaSelect();
  fillWorkLocationSelect();
  fillResultSelect();
  renderTaskCategoryCheckboxes();
}

function fillCriteriaSelect(selectedId) {
  fillSelect(
    document.getElementById("checkup-criteria-select"),
    activeLookups(checkUpCriteriaCache, selectedId, "checkUpCriteriaId"),
    "checkUpCriteriaId",
    "name",
    "Select…",
  );
}

function fillWorkLocationSelect(selectedText) {
  setWorkLocationCheckboxes(selectedText);
}

// Work location is multi-select like task category: one checkbox per lookup
// row. The hidden workLocation field carries the checked names joined with
// "; ", which is the text the server stores in McuRecord.WorkLocation.
function setWorkLocationCheckboxes(text) {
  const box = document.getElementById("work-location-checkboxes");
  if (!box) return;
  const names = splitWorkLocations(text);
  const selected = new Set(names);
  const known = workLocationCache
    .filter((l) => l.status !== "Archived" || selected.has(l.name))
    .map((l) => l.name);
  // A saved name stays on the list even if its lookup row was archived or
  // renamed since, so opening and saving a checkup never drops it.
  const options = [...known, ...names.filter((n) => !known.includes(n))];
  box.innerHTML = options
    .map(
      (name) => `
      <label>
        <input type="checkbox" value="${escapeHtml(name)}"${selected.has(name) ? " checked" : ""} />
        <span>${escapeHtml(name)}</span>
      </label>`
    )
    .join("");
  box.querySelectorAll('input[type="checkbox"]').forEach((el) => el.addEventListener("change", syncWorkLocationValue));
  syncWorkLocationValue();
}

function splitWorkLocations(text) {
  return (text || "")
    .split(";")
    .map((s) => s.trim())
    .filter(Boolean);
}

function syncWorkLocationValue() {
  const checked = Array.from(document.querySelectorAll('#work-location-checkboxes input[type="checkbox"]:checked')).map(
    (el) => el.value
  );
  const field = document.querySelector('input[name="workLocation"]');
  if (field) field.value = checked.join("; ");
}

// Option text for the Result dropdown. The stored value stays the lookup name.
function resultOptionText(name) {
  return RESULT_CRITERIA_LABELS[name] || name;
}

function fillResultSelect(selectedName) {
  const el = document.getElementById("result-criteria-select");
  if (!el) return;
  const keep = selectedName !== undefined ? selectedName : el.value;
  const items = activeLookups(reviewResultCache, keep, "name");
  el.innerHTML =
    `<option value="">—</option>` +
    items.map((item) => `<option value="${escapeHtml(item.name)}">${escapeHtml(resultOptionText(item.name))}</option>`).join("");
  if (keep && !items.some((item) => item.name === keep)) {
    el.innerHTML += `<option value="${escapeHtml(keep)}">${escapeHtml(resultOptionText(keep))}</option>`;
  }
  el.value = keep || "";
}

const retiredTaskNames = new Set(["remotesiteworker", "nonremotesiteworker/jakartaoffice", "nonremotesiteworker"]);

function normLookupName(name) {
  return (name || "").toLowerCase().replace(/[\s_-]+/g, "");
}

// Task category is multi-select (a checkup can be both Food Handler and
// Work at Height) — rendered as a checkbox per TaskCategoryTable row instead
// of a <select>, since a native multi-select's ctrl/cmd-click selection is
// easy to miss. Selected ids are read by getSelectedTaskCategoryIds() at
// submit time; the server resolves them to names and joins them (see
// resolveTaskCategoryNames in mcurecord.go) — the checkboxes never see or
// send the joined string directly.
function renderTaskCategoryCheckboxes(selectedNames) {
  const box = document.getElementById("task-category-checkboxes");
  if (!box) return;
  const selected = new Set(selectedNames || []);
  box.innerHTML = taskCategoryCache
    .filter((t) => t.status !== "Archived" || selected.has(t.name))
    .filter((t) => !retiredTaskNames.has(normLookupName(t.name)))
    .map(
      (t) => `
      <label>
        <input type="checkbox" name="taskCategoryIds" value="${t.taskCategoryId}" />
        <span>${escapeHtml(t.name)}</span>
      </label>`
    )
    .join("");
}

function getSelectedTaskCategoryIds() {
  return Array.from(document.querySelectorAll('#task-category-checkboxes input[name="taskCategoryIds"]:checked')).map((el) =>
    Number(el.value)
  );
}

// rec.taskCategoryNames is the persisted "; "-joined string (e.g. "Food
// Handler; Work at Height") — matched back against taskCategoryCache by name
// to re-check the right boxes when editing an existing checkup, since the
// server only stores/returns names, not the ids that produced them.
function setTaskCategoryCheckboxes(taskCategoryNames) {
  const names = (taskCategoryNames || "")
    .split(";")
    .map((s) => s.trim())
    .filter(Boolean);
  renderTaskCategoryCheckboxes(names);
  document.querySelectorAll('#task-category-checkboxes input[name="taskCategoryIds"]').forEach((el) => {
    const cat = taskCategoryCache.find((t) => t.taskCategoryId === Number(el.value));
    el.checked = !!cat && names.includes(cat.name);
  });
}

// Sets both halves of a company typeahead field (see attachCompanyFilterTypeahead
// below) from a companyId — the hidden input the form actually submits, and
// the visible text input showing its name — since populating a saved
// record's company can't rely on the user having typed/clicked it themselves.
function setCompanyField(hiddenId, inputId, companyId) {
  const hidden = document.getElementById(hiddenId);
  const input = document.getElementById(inputId);
  if (!hidden || !input) return;
  hidden.value = companyId || "";
  const c = companyId ? companyCache.find((c) => c.companyId === Number(companyId)) : null;
  input.value = c ? c.name : "";
}

// --- Search -----------------------------------------------------------
// Live, as-you-type. A blank query or a query with no matches hides the
// column header; the header comes back only when at least one row matches.
// Rows are every checkup, not deduped to latest-per-patient like the MCU
// data tab. Checkup ID opens that record; Name opens the worker detail.
// Typing searches on its own. The Search button and Enter run the same search immediately.

const mcuSearchInput = document.getElementById("mcu-search-input");

function attachSearchClear(input) {
  const button = input?.parentElement?.querySelector(".search-clear");
  if (!input || !button) return;
  button.addEventListener("mousedown", (event) => event.preventDefault());
  button.addEventListener("click", () => {
    input.value = "";
    input.dispatchEvent(new Event("input", { bubbles: true }));
    input.focus();
  });
}

attachSearchClear(mcuSearchInput);
attachSearchClear(document.getElementById("mcu-data-name"));

mcuSearchInput?.addEventListener("input", debounce((e) => runSearch(e.target.value.trim()), 300));
mcuSearchInput?.addEventListener("keydown", (e) => {
  if (e.key !== "Enter") return;
  e.preventDefault();
  runSearch(e.target.value.trim());
});
document.getElementById("mcu-search-btn")?.addEventListener("click", () => {
  runSearch(mcuSearchInput?.value.trim() || "");
});

// fuzzyMatch: subsequence match (same technique as fzf/VSCode's command
// palette) — every character of query must appear in text, in order, but not
// necessarily contiguous. Deliberately looser than substring matching so a
// typo'd or abbreviated query ("jn doe", "medc11") still finds "Jane Doe" /
// "MedC0011" — plain SQL LIKE '%q%' (the previous behavior) required an
// exact contiguous substring.
function fuzzyMatch(query, text) {
  if (!query) return true;
  query = query.toLowerCase();
  text = (text || "").toLowerCase();
  let qi = 0;
  for (let i = 0; i < text.length && qi < query.length; i++) {
    if (text[i] === query[qi]) qi++;
  }
  return qi === query.length;
}

function setSearchResultsHeaderVisible(visible) {
  const head = document.querySelector("#tab-data-entry .table-scroll thead");
  if (head) head.hidden = !visible;
}

async function runSearch(q) {
  const body = document.getElementById("search-results-body");
  const empty = document.getElementById("search-results-empty");
  if (!q) {
    body.innerHTML = "";
    empty.hidden = true;
    setSearchResultsHeaderVisible(false);
    setNewPatientOffer(false);
    return;
  }
  // Fetch the unfiltered list and fuzzy-match client-side against both name
  // and checkup ID — the placeholder says "Name or checkup ID…" but the
  // backend's ?name= filter only ever matched Patient.Name, so a CheckUpID
  // search silently returned nothing.
  const res = await fetch(`/api/mcu-records/browse`, { credentials: "same-origin" });
  if (!res.ok) return;
  const all = await res.json();
  const results = all.filter((r) => fuzzyMatch(q, r.patientName) || fuzzyMatch(q, r.checkUpId));
  empty.hidden = results.length > 0;
  empty.textContent = "No matches — use \"+ New patient\" above to add one.";
  setSearchResultsHeaderVisible(results.length > 0);
  setNewPatientOffer(results.length === 0);
  body.innerHTML = results
    .map(
      (r) => `
      <tr>
        <td data-label="Checkup ID"><a href="#" data-mcu-id="${r.mcuRecordId}">${escapeHtml(r.checkUpId)}</a></td>
        <td data-label="Name"><a href="#" data-patient-mcu-id="${r.mcuRecordId}">${escapeHtml(r.patientName)}</a></td>
        <td data-label="Company">${escapeHtml(r.companyName || "—")}</td>
        <td data-label="Criteria">${escapeHtml(r.criteria || "—")}</td>
        <td data-label="Review date">${escapeHtml(r.reviewDate)}</td>
        <td data-label="Result">${escapeHtml(r.resultCriteria || "—")}</td>
        <td data-label="Valid until">${escapeHtml(r.resultValidUntil || "—")}</td>
      </tr>`
    )
    .join("");
  body.querySelectorAll("[data-mcu-id]").forEach((a) => {
    a.addEventListener("click", (e) => {
      e.preventDefault();
      showMcuDetailInline(Number(a.dataset.mcuId), a.closest("tr"));
    });
  });
  body.querySelectorAll("[data-patient-mcu-id]").forEach((a) => {
    a.addEventListener("click", (e) => {
      e.preventDefault();
      showWorkerDetailInline(Number(a.dataset.patientMcuId), a.closest("tr"));
    });
  });
}

// calculateAge from legacy WebApp.html: whole years as of today, editable
// afterwards since some checkups (e.g. backdated entries) need it adjusted.
function ageFromDob(iso) {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(iso || "");
  const t = /^(\d{4})-(\d{2})-(\d{2})$/.exec(clinicToday());
  if (!m || !t) return "";
  let age = Number(t[1]) - Number(m[1]);
  if (Number(t[2]) < Number(m[2]) || (Number(t[2]) === Number(m[2]) && Number(t[3]) < Number(m[3]))) age--;
  return age;
}

// The search table returns denormalized rows (patient name, not id), so
// resolving "which patient owns this record" means a second round trip
// through the records + patients lists rather than trusting name matching
// (names aren't unique) — small dataset, same cost class as every other
// list call already used on this page.
async function resolvePatientByMcuId(mcuRecordId) {
  const res = await fetch(`/api/mcu-records`, { credentials: "same-origin" });
  if (!res.ok) return null;
  const all = await res.json();
  const rec = all.find((m) => m.mcuRecordId === mcuRecordId);
  if (!rec) return null;
  const pRes = await fetch(`/api/patients`, { credentials: "same-origin" });
  const patients = await pRes.json();
  const p = patients.find((p) => p.patientId === rec.patientId);
  if (!p) return null;
  return { rec, p };
}

// Opens straight into that checkup's own edit form — used by the "Edit
// checkup" button inside showMcuDetailInline's read-only view below, and
// kept as its own function since checkupReturnTo="table" + checkup-cancel's
// existing handling already does exactly the right thing here (re-run the
// search, no patient-detail/history panel involved).
async function openMcuRecordById(mcuRecordId) {
  const resolved = await resolvePatientByMcuId(mcuRecordId);
  if (!resolved) return;
  currentPatient = resolved.p;
  patientDetail.hidden = true;
  checkupReturnTo = "table";
  await openCheckupForm(resolved.rec);
  setDataEntryListVisible(false);
  checkupForm?.scrollIntoView({ block: "start" });
}

// --- Inline worker/MCU detail rows (Data entry table) --------------------
//
// Clicking a Name or Checkup ID in the search-results table used to jump
// straight to the full patient-detail panel (name/meta header + entire
// checkup history table) or the editable checkup form. Per the redesign:
// clicking either now expands a read-only summary directly under the
// clicked row instead — Name shows just the worker's own fields (no
// history/"mcu list"), Checkup ID shows both the worker's fields and the
// checkup's fields grouped the same way the edit forms group them. Editing
// either is one extra, explicit click away ("Edit employee details" /
// "Edit checkup") rather than the default action.
let expandedDetailRow = null;
let expandedTriggerRow = null;
let expandedTriggerLink = null;
let expandedDetailKind = null;
// Scroll position of the MCU data list before a row was focused, so collapse
// can put the previous list (filtered or the full latest list) back in place.
let mcuListFocusScroll = null;

function mcuDataListScroller() {
  return document.getElementById("mcu-data-list");
}

function focusMcuListOnRow(triggerRow) {
  if (!triggerRow?.closest("#mcu-data-body")) return;
  const scroller = mcuDataListScroller();
  if (scroller && mcuListFocusScroll === null) mcuListFocusScroll = scroller.scrollTop;
  for (const tr of triggerRow.parentElement.children) {
    if (tr === triggerRow || tr.classList.contains("detail-row")) continue;
    tr.classList.add("is-list-hidden");
  }
}

function restoreMcuListRows(triggerRow) {
  const body = triggerRow?.closest("#mcu-data-body");
  if (!body) return;
  for (const tr of body.children) tr.classList.remove("is-list-hidden");
  const scroller = mcuDataListScroller();
  if (scroller && mcuListFocusScroll !== null) scroller.scrollTop = mcuListFocusScroll;
  mcuListFocusScroll = null;
}

function collapseDetailRow() {
  const trigger = expandedTriggerRow;
  expandedTriggerLink?.classList.remove("is-expanded-link");
  expandedTriggerLink = null;
  expandedDetailRow?.remove();
  expandedDetailRow = null;
  expandedTriggerRow = null;
  expandedDetailKind = null;
  restoreMcuListRows(trigger);
}

function markExpandedLink(triggerRow, selector) {
  const link = triggerRow.querySelector(selector);
  if (!link) return;
  link.classList.add("is-expanded-link");
  expandedTriggerLink = link;
}

// kind is "mcu" or "worker". Clicking the link that is already open closes
// it. Clicking the other link on the same row switches the detail and moves
// the highlight.
function showDetailRow(triggerRow, colCount, innerHtml, kind) {
  const sameTarget = triggerRow === expandedTriggerRow && expandedDetailKind === kind;
  const sameRow = triggerRow === expandedTriggerRow;
  if (sameTarget) {
    collapseDetailRow();
    return false;
  }
  if (sameRow) {
    expandedTriggerLink?.classList.remove("is-expanded-link");
    expandedTriggerLink = null;
    expandedDetailRow?.remove();
    expandedDetailRow = null;
    expandedDetailKind = null;
  } else if (expandedTriggerRow) {
    collapseDetailRow();
  }
  const tr = document.createElement("tr");
  tr.className = "detail-row";
  tr.innerHTML = `<td colspan="${colCount}">${innerHtml}</td>`;
  triggerRow.after(tr);
  expandedDetailRow = tr;
  expandedTriggerRow = triggerRow;
  expandedDetailKind = kind;
  focusMcuListOnRow(triggerRow);
  return true;
}

function roField(label, value, gridColumn) {
  return `
    <label${gridColumn ? ` style="grid-column:${gridColumn}"` : ""}>
      <span>${escapeHtml(label)}</span>
      <div class="ro-value">${escapeHtml(value ?? "") || "—"}</div>
    </label>`;
}

function workerDetailFieldsHtml(p) {
  return `
    <div class="ro-grid">
      ${roField("Name", p.name)}
      ${p.workPosition ? roField("Work position", p.workPosition) : ""}
      ${p.companyName ? roField("Company", p.companyName) : ""}
      ${roField("Gender", p.gender)}
      ${roField("Date of birth", p.dateOfBirth)}
      ${roField("Nationality", p.nationalityName)}
      ${roField("Blood type", p.bloodType)}
      ${roField("Height (cm)", p.heightCm)}
      ${roField("Employee/badge ID", p.employeeId)}
      ${roField("NIK/Passport ID", p.personalId)}
      <label data-mcu-valid-until>
        <span>MCU valid until</span>
        <div class="ro-value">…</div>
      </label>
    </div>`;
}

// The patient list carries no checkup data, so the "MCU valid until" field
// workerDetailFieldsHtml draws starts as a placeholder and is filled here
// from the patient's newest checkup (the server orders records newest first).
async function fillMcuValidUntil(container, patientId) {
  const value = container?.querySelector("[data-mcu-valid-until] .ro-value");
  if (!value) return;
  try {
    const res = await fetch(`/api/mcu-records?patientId=${patientId}`, { credentials: "same-origin" });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const records = await res.json();
    value.textContent = (Array.isArray(records) && records[0]?.resultValidUntil) || "—";
  } catch (err) {
    value.textContent = "Could not load";
  }
}

// true while #patient-form is open from one of these inline rows rather
// than from the old patient-detail panel's "Edit patient" button — changes
// what Cancel/save-success should do afterwards (close the form and return
// to the search table, not reveal patient-detail — see patient-cancel and
// patientForm's submit handler below).
let patientFormOpenedFromInlineRow = false;

// Edit employee details and New Medical Checkup replace the search table
// with the form. Cancel (and a successful save) put the table back.
function setDataEntryListVisible(visible) {
  const filter = document.getElementById("mcu-data-toolbar");
  const list = document.getElementById("mcu-data-list");
  const forms = document.getElementById("mcu-data-forms");
  if (filter) {
    filter.hidden = !visible;
    filter.classList.remove("is-header-hold");
  }
  if (list) list.hidden = !visible;
  if (forms) {
    forms.hidden = visible;
    if (!visible) forms.scrollTop = 0;
  }
  document.querySelector(".app-shell")?.classList.toggle("is-form-focus", !visible);
  document.querySelector(".app-panel")?.classList.toggle("is-form-focus", !visible);
  setFormFocusTitle(!visible);
  placeFormFooterActions(!visible);
}

function setFormFocusTitle(on) {
  const title = document.getElementById("panel-title");
  if (!title) return;
  if (!on) {
    title.textContent = "Medical Checkups";
    return;
  }
  let action = "Edit employee";
  if (newPatientCombined) action = "New patient";
  else if (checkupForm && !checkupForm.hidden) {
    action = editingMcuRecordId ? "Edit checkup" : "New checkup";
  }
  title.innerHTML = `Medical Checkup | <span class="title-action">${escapeHtml(action)}</span>`;
}

// Save and Cancel sit in their own bar, directly above the disclaimer.
function formActionBar() {
  let bar = document.getElementById("form-action-bar");
  if (bar) return bar;
  const footer = document.querySelector(".app-dev-credit");
  if (!footer) return null;
  bar = document.createElement("div");
  bar.id = "form-action-bar";
  bar.hidden = true;
  footer.before(bar);
  return bar;
}

// Closing the form puts each button row back where the form expects it.
function placeFormFooterActions(on) {
  const footer = document.querySelector(".app-dev-credit");
  const bar = formActionBar();
  const checkupActions = document.getElementById("checkup-actions");
  const patientActions = document.getElementById("patient-form-buttons");
  if (!footer || !bar || !checkupActions) return;
  const checkupSave = checkupActions.querySelector('button[type="submit"]');
  const checkupCancel = document.getElementById("checkup-cancel");
  const patientSave = patientActions?.querySelector('button[type="submit"]');
  if (patientSave) patientSave.setAttribute("form", "patient-form");
  footer.classList.remove("has-patient-actions");
  if (!on) {
    bar.hidden = true;
    checkupForm?.append(checkupActions);
    if (patientActions) patientForm?.append(patientActions);
    if (checkupSave) checkupSave.textContent = "Save checkup";
    if (checkupCancel) checkupCancel.textContent = "Close";
    if (patientSave) patientSave.textContent = "Save patient";
    return;
  }
  const checkupOpen = checkupForm && !checkupForm.hidden;
  if (checkupOpen) {
    bar.append(checkupActions);
    if (checkupSave) checkupSave.textContent = "Save";
    if (checkupCancel) {
      checkupCancel.hidden = false;
      checkupCancel.textContent = "Cancel";
    }
  } else if (patientActions && !patientActions.hidden) {
    bar.append(patientActions);
    if (patientSave) patientSave.textContent = "Save";
  }
  bar.hidden = false;
  fitFormFooter();
  watchFormFooter();
}

function fitFormFooter() {
  const footer = document.querySelector(".app-dev-credit");
  const bar = document.getElementById("form-action-bar");
  if (!footer || !bar || bar.hidden) return;
  const disclaimerH = Math.ceil(footer.getBoundingClientRect().height);
  const barH = Math.ceil(bar.getBoundingClientRect().height);
  // Line the buttons up with the open form's section headers (the first
  // <summary>), falling back to the form's own left edge if there are none.
  const openForm = document.querySelector("#checkup-form:not([hidden]), #patient-form:not([hidden])");
  const alignTo = openForm?.querySelector("summary") || openForm;
  const formLeft = alignTo ? Math.round(alignTo.getBoundingClientRect().left) : 12;
  bar.style.paddingLeft = `${formLeft}px`;
  document.documentElement.style.setProperty("--mcu-disclaimer-h", `${disclaimerH}px`);
  document.documentElement.style.setProperty("--mcu-footer-h", `${disclaimerH + barH}px`);
}

function watchFormFooter() {
  const footer = document.querySelector(".app-dev-credit");
  if (!footer || footer.dataset.footerWatch) return;
  footer.dataset.footerWatch = "1";
  // The forms are usually unhidden after the footer is placed, so watch them
  // too: when one appears or changes size, the buttons re-align to it.
  const observer = new ResizeObserver(() => fitFormFooter());
  observer.observe(footer);
  for (const form of [checkupForm, patientForm]) {
    if (form) observer.observe(form);
  }
  window.addEventListener("resize", () => fitFormFooter());
}

async function newCheckupForPatientInline(p) {
  currentPatient = p;
  patientDetail.hidden = true;
  checkupReturnTo = "table";
  collapseDetailRow();
  // openCheckupForm's new-checkup branch reads patientHistoryCache
  // (applySuggestedCriteria) to suggest the next criteria — must be loaded
  // for *this* patient first, or it suggests off whatever patient's history
  // happened to be cached last (or nothing, on the very first checkup of a
  // session), always falling back to "Pre Employment (no previous checkups
  // on file)" regardless of the patient's real history.
  await loadHistory(p.patientId);
  await openCheckupForm(null);
  setDataEntryListVisible(false);
  patientForm.scrollIntoView({ block: "start" });
}

async function showWorkerDetailInline(mcuRecordId, triggerRow) {
  const resolved = await resolvePatientByMcuId(mcuRecordId);
  if (!resolved) return;
  showWorkerDetailForPatient(resolved.p, triggerRow, "[data-patient-mcu-id]");
}

// Expands the worker summary under triggerRow for patient p. linkSelector
// names the attribute on the row's clicked link, so markExpandedLink can
// highlight it while the summary is open.
function showWorkerDetailForPatient(p, triggerRow, linkSelector) {
  const expanded = showDetailRow(
    triggerRow,
    triggerRow.children.length,
    `
    <div class="detail-group detail-group--worker">
      <div class="section-label" style="margin-bottom:8px">Worker's Detail</div>
      ${workerDetailFieldsHtml(p)}
    </div>
    <div style="margin-top:10px;display:flex;gap:10px;flex-wrap:wrap">
      <button type="button" class="btn-primary" data-inline-edit-worker="${p.patientId}">Edit employee details</button>
      <button type="button" class="btn-primary" data-inline-new-checkup>New Medical Checkup</button>
      <button type="button" class="btn-outline" data-inline-cancel>Cancel</button>
    </div>`,
    "worker",
  );
  if (!expanded) return;
  markExpandedLink(triggerRow, linkSelector);
  const row = expandedDetailRow;
  fillMcuValidUntil(row, p.patientId);
  row.querySelector("[data-inline-edit-worker]")?.addEventListener("click", () => {
    currentPatient = p;
    patientFormOpenedFromInlineRow = true;
    populatePatientForm(p, "Edit employee details");
    document.getElementById("checkup-form").hidden = true;
    document.getElementById("patient-form-buttons").hidden = false;
    patientForm.hidden = false;
    showLatestCheckupHint(p);
    collapseDetailRow();
    setDataEntryListVisible(false);
    patientForm.scrollIntoView({ block: "start" });
  });
  row.querySelector("[data-inline-new-checkup]")?.addEventListener("click", () => newCheckupForPatientInline(p));
  row.querySelector("[data-inline-cancel]")?.addEventListener("click", collapseDetailRow);
}

// checkupFieldNames' plain exam fields, grouped exactly like the checkup
// form's own <details> sections (see mcu.html) — "keep the grouping" means
// this read-only view shouldn't flatten those five sections into one list.
const mcuDetailGroups = [
  {
    title: "History & Physical Exam",
    fields: [
      ["smoking", "Smoking/vape"],
      ["heightCm", "Height (cm)"], ["weightKg", "Weight (kg)"],
      ["bmiValue", "BMI value"], ["bmiCriteria", "BMI criteria"], ["bmiNotes", "BMI notes"],
      ["herniaHemorrhoidCriteria", "Hernia/hemorrhoid criteria"], ["herniaHemorrhoidNotes", "Hernia/hemorrhoid notes"],
      ["visionCriteria", "Vision"], ["visionNotes", "Vision notes"],
      ["bpCriteria", "Blood pressure criteria"], ["bpNotes", "Blood pressure notes"],
      ["otherPhysicalExamNotes", "Other physical exam notes"],
    ],
  },
  {
    title: "Blood & lab work",
    fields: [
      ["bloodType", "Blood type"],
      ["cbcCriteria", "CBC criteria"], ["cbcNotes", "CBC notes"],
      ["lipidCriteria", "Lipid profile criteria"], ["lipidNotes", "Lipid profile notes"],
      ["glucoseCriteria", "Blood glucose criteria"], ["glucoseNotes", "Blood glucose notes"],
      ["liverCriteria", "Liver function criteria"], ["liverNotes", "Liver function notes"],
      ["kidneyCriteria", "Kidney function criteria"], ["kidneyNotes", "Kidney function notes"],
      ["urinalysisCriteria", "Urinalysis criteria"], ["urinalysisNotes", "Urinalysis notes"],
    ],
  },
  {
    title: "Radiology",
    fields: [
      ["radiologyResult", "Thorax X-ray criteria"], ["radiologyNotes", "Thorax X-ray notes"],
      ["echocardiographyCriteria", "Echocardiography criteria"], ["echocardiographyNotes", "Echocardiography notes"],
      ["ctScanCriteria", "CT scan criteria"], ["ctScanNotes", "CT scan notes"],
      ["usgAbdomenCriteria", "USG abdomen criteria"], ["usgAbdomenNotes", "USG abdomen notes"],
    ],
  },
  {
    title: "ECG & Treadmill",
    fields: [
      ["ecgResult", "ECG result"], ["treadmillResult", "Treadmill result"],
      ["ecgTreadmillNotes", "ECG/treadmill notes"],
    ],
  },
  {
    title: "Spirometry & Audiometry",
    fields: [
      ["spirometryCriteria", "Spirometry criteria"], ["spirometryNotes", "Spirometry notes"],
      ["audiometryCriteria", "Audiometry criteria"], ["audiometryNotes", "Audiometry notes"],
    ],
  },
  {
    title: "Infectious disease screening",
    fields: [
      ["hepBCriteria", "Hepatitis B serology"], ["hepBNotes", "Hepatitis B notes"],
      ["hepACriteria", "Hepatitis A serology"], ["hepANotes", "Hepatitis A notes"],
      ["rectalSwabCriteria", "Rectal swab criteria"], ["rectalSwabNotes", "Rectal swab notes"],
      ["hivCriteria", "HIV serology"],
    ],
  },
  {
    title: "Additional exam",
    fields: [["additionalNotes", "Additional exam notes"]],
  },
];

function mcuDetailFieldsHtml(rec) {
  const criteria = checkUpCriteriaCache.find((c) => c.checkUpCriteriaId === rec.checkUpCriteriaId);
  const company = companyCache.find((c) => c.companyId === rec.companyId);
  const admin = `
    <div class="ro-grid">
      ${roField("Checkup ID", rec.checkUpId)}
      ${roField("Reviewer", rec.reviewer)}
      ${roField("Checkup purpose criteria", criteria?.name)}
      ${roField("Work location", rec.workLocation)}
      ${roField("Occupational health risks", rec.taskCategoryNames)}
      ${roField("Company (at this visit)", company?.name)}
      ${roField("Work position (at this visit)", rec.workPosition)}
      ${roField("Review date", rec.reviewDate)}
      ${roField("Date of checkup", rec.dateCheckUp)}
      ${roField("Submission date", rec.submissionDate)}
      ${roField("Age", rec.age)}
      ${roField("Validity period (months)", rec.validityPeriodMonths)}
      ${roField("Short project", rec.isShortProject ? "Yes" : "No")}
    </div>`;
  const groups = mcuDetailGroups
    .map((g) => {
      const hasData = g.fields.some(([key]) => {
        const v = rec[key];
        return v !== null && v !== undefined && v !== "" && v !== false;
      });
      // Clicking the checkup ID opens every exam section. Empty ones stay
      // muted so recorded sections still stand out.
      return `
      <details open class="${hasData ? "" : "mcu-detail-empty"}" style="margin-top:10px">
        <summary>${escapeHtml(g.title)}</summary>
        <div class="ro-grid" style="margin-top:10px">
          ${g.fields.map(([key, label]) => roField(label, isNoteField(key) ? toParagraphNote(rec[key]) : rec[key])).join("")}
        </div>
      </details>`;
    })
    .join("");
  const result = `
    <div class="section-label" style="margin-bottom:12px">Result</div>
    <div class="ro-grid">
      ${roField("Result", rec.resultCriteria)}
      ${roField("Checkup status", rec.checkUpStatus)}
      ${roField("Valid until", rec.resultValidUntil)}
      ${roField("HC validity", rec.hcValidityStatus)}
      ${roField("Result notes", toParagraphNote(rec.resultNotes), "1/-1")}
      ${roField("Recommendation / follow-up", toParagraphNote(rec.recommendation), "1/-1")}
    </div>`;
  return { exam: admin + groups, result };
}

// The health certificate is offered only for a Fit / Fit-with-note result
// that is still standing (isCertificateEligible) and whose valid-until date
// has not passed. An expired one is not offered here; the history table's
// Certificate link still reaches it.
function validCertificateButtonHtml(rec, p) {
  const certifiable = rec.resultCriteria === "Fit" || rec.resultCriteria === "FitWithNote";
  const days = daysUntilDate(rec.resultValidUntil);
  if (!certifiable || days === null || days < 0) return "";
  if (!isCertificateEligible(rec.checkUpStatus, rec.hcValidityStatus, rec.resultCriteria)) return "";
  const abbrev = companyCache.find((c) => c.companyId === Number(p.companyId))?.abbreviation || "";
  const href = certificateOpenHref(rec.mcuRecordId, rec.checkUpId, abbrev, p.name || "", rec.resultValidUntil);
  return `<a class="btn-outline" href="${escapeHtml(href)}" target="_blank" rel="noopener">View / download health certificate</a>`;
}

async function showMcuDetailInline(mcuRecordId, triggerRow) {
  const resolved = await resolvePatientByMcuId(mcuRecordId);
  if (!resolved) return;
  const { rec, p } = resolved;
  const { exam, result } = mcuDetailFieldsHtml(rec);
  const expanded = showDetailRow(
    triggerRow,
    triggerRow.children.length,
    `
    <div class="detail-group detail-group--worker">
      <div class="section-label" style="margin-bottom:8px">Worker's Detail</div>
      ${workerDetailFieldsHtml(p)}
      <div style="margin-top:10px;display:flex;gap:10px;flex-wrap:wrap">
        <button type="button" class="btn-primary" data-inline-new-checkup>New Medical Checkup</button>
      </div>
    </div>
    <div class="detail-group detail-group--exam">
      <div class="section-label" style="margin-bottom:8px">Exam details</div>
      ${exam}
    </div>
    <div class="detail-group detail-group--result">
      ${result}
    </div>
    <div style="margin-top:14px;display:flex;gap:10px;flex-wrap:wrap">
      <button type="button" class="btn-primary" data-inline-edit-checkup="${rec.mcuRecordId}">Edit checkup</button>
      ${validCertificateButtonHtml(rec, p)}
      <button type="button" class="btn-outline" data-inline-cancel>Cancel</button>
    </div>`,
    "mcu",
  );
  if (!expanded) return;
  markExpandedLink(triggerRow, "[data-mcu-id]");
  const row = expandedDetailRow;
  fillMcuValidUntil(row, p.patientId);
  row.querySelector("[data-inline-new-checkup]")?.addEventListener("click", () => newCheckupForPatientInline(p));
  row.querySelector("[data-inline-edit-checkup]")?.addEventListener("click", () => {
    collapseDetailRow();
    openMcuRecordById(rec.mcuRecordId);
  });
  row.querySelector("[data-inline-cancel]")?.addEventListener("click", collapseDetailRow);
}

// --- Patient form -------------------------------------------------------

const patientForm = document.getElementById("patient-form");
const patientDetail = document.getElementById("patient-detail");
// Native constraint validation draws an OS popup that can't be restyled.
// Missing patient fields are reported in #patient-required-notice instead.
patientForm?.setAttribute("novalidate", "");

const newPatientOffer = document.getElementById("mcu-new-patient-offer");

function setNewPatientOffer(visible) {
  if (newPatientOffer) newPatientOffer.hidden = !visible;
}

function setNewPatientCombined(on) {
  newPatientCombined = on;
  document.querySelector(".app-shell")?.classList.toggle("is-new-patient", on);
  document.querySelector(".app-panel")?.classList.toggle("is-new-patient", on);
  const headerCancel = document.getElementById("new-patient-header-cancel");
  if (headerCancel) headerCancel.hidden = true;
  const bar = document.getElementById("new-patient-cancel-bar");
  if (bar) bar.hidden = true;
  const presence = document.getElementById("presence-banner");
  if (on && presence) presence.hidden = true;
  const certStatus = document.getElementById("cert-status");
  if (certStatus) certStatus.hidden = on;
  if (!on) refreshPresence();
}

function closeCombinedPatientCheckup() {
  const wasReviewing = clearLinkedSubmission();
  setNewPatientCombined(false);
  checkupForm.hidden = true;
  patientForm.hidden = true;
  patientFormInline = false;
  visitPositionLinked = false;
  visitCompanyLinked = false;
  if (wasReviewing) {
    // Cancelling a review goes back to where it started, not to the Employee list.
    showMcuTab("submitted");
    return;
  }
  patientDetail.hidden = true;
  setDataEntryListVisible(true);
  loadMcuDataTable();
}

// Blank employee fields and a new checkup, both open. There is no patient
// row yet; Save creates the patient, then the checkup.
function openBlankPatientCheckup() {
  setNewPatientOffer(false);
  currentPatient = null;
  patientHistoryCache = [];
  patientFormOpenedFromInlineRow = false;
  patientFormInline = true;
  checkupReturnTo = "table";
  editingMcuRecordId = null;
  riskTriggerMatched = new Set();
  visitPositionLinked = true;
  visitCompanyLinked = true;
  patientForm.reset();
  checkupForm.reset();
  showFormMessage(document.getElementById("patient-msg"), "");
  showFormMessage(document.getElementById("checkup-msg"), "");
  fillSelect(
    document.getElementById("patient-nationality"),
    activeLookups(nationalityCache, "", "nationalityId"),
    "nationalityId",
    "name",
    "—",
  );
  updatePersonalIdFormat();
  showLatestCheckupHint(null);
  showPatientRequiredNotice([]);
  document.getElementById("patient-form-title").textContent = "Employee details";
  document.getElementById("checkup-form-title").textContent = "New medical checkup";
  document.getElementById("patient-form-buttons").hidden = true;
  patientDetail.hidden = true;
  patientForm.hidden = false;
  checkupForm.hidden = false;
  setNewPatientCombined(true);

  const parent = document.getElementById("parent-record-select");
  if (parent) parent.innerHTML = `<option value="">None</option>`;
  checkupForm.querySelector('[name="checkUpId"]').value = `MedC${Date.now().toString().slice(-6)}`;
  const reviewerField = checkupForm.querySelector('[name="reviewer"]');
  reviewerField.value = window.__USER_NAME__ || "";
  reviewerField.readOnly = true;
  const today = clinicToday();
  checkupForm.querySelector('[name="reviewDate"]').value = today;
  checkupForm.querySelector('[name="dateCheckUp"]').value = "";
  checkupForm.querySelector('[name="submissionDate"]').value = "";
  syncDateDisplays(checkupForm);
  updateShortProjectDisplay();
  setCompanyField("checkup-company-select", "checkup-company-input", "");
  updateCheckupCompanyRelationField();
  updateCheckupStatusDisplay();
  updateResultDependentFields();
  syncCheckupReadonlyDisplays();
  calcBmi();
  applySuggestedCriteria();
  updateFoodHandlerScopeNote();
  renderTaskCategoryCheckboxes();
  setVisitCompanySame(true);
  setVisitPositionSame(true);
  setDataEntryListVisible(false);
  patientForm.querySelector('[name="name"]')?.focus();
}

document.getElementById("new-patient-btn")?.addEventListener("click", openBlankPatientCheckup);
document.getElementById("new-patient-cancel")?.addEventListener("click", closeCombinedPatientCheckup);
document.getElementById("new-patient-header-cancel")?.addEventListener("click", closeCombinedPatientCheckup);

document.getElementById("patient-cancel")?.addEventListener("click", () => {
  patientForm.hidden = true;
  if (patientFormOpenedFromInlineRow) {
    patientFormOpenedFromInlineRow = false;
    setDataEntryListVisible(true);
    loadMcuDataTable();
    return;
  }
  if (!patientFormInline && currentPatient) {
    patientDetail.hidden = false;
    return;
  }
  setDataEntryListVisible(true);
  loadMcuDataTable();
});

// Shared by the "Edit patient" button and openCheckupForm's new-checkup case
// below (which also expands this form so lacking patient details can be
// fixed on the spot instead of requiring a separate trip to "Edit patient").
// isIndonesiaNationality/updatePersonalIdFormat decide which of PersonalID's
// two formats applies — 16-digit NIK for Indonesian nationality, otherwise a
// 7-9 character alphanumeric passport ID — and keep the input's maxlength/
// placeholder in sync with whatever nationality is currently selected.
// Matched server-side by validatePersonalID in internal/patient/patient.go;
// this is convenience/UX only, not the enforcement boundary.
function isIndonesiaNationality(nationalityIdValue) {
  const nat = nationalityCache.find((n) => String(n.nationalityId) === String(nationalityIdValue));
  return !!nat && nat.name.trim().toLowerCase() === "indonesia";
}

function updatePersonalIdFormat() {
  const natSelect = patientForm?.querySelector('[name="nationalityId"]');
  const input = patientForm?.querySelector('[name="personalId"]');
  if (!natSelect || !input) return;
  if (isIndonesiaNationality(natSelect.value)) {
    input.maxLength = 16;
    input.placeholder = "16-digit NIK";
  } else {
    input.maxLength = 9;
    input.placeholder = "Passport ID (7-9 chars)";
  }
}

patientForm?.querySelector('[name="nationalityId"]')?.addEventListener("change", updatePersonalIdFormat);
patientForm?.querySelector('[name="personalId"]')?.addEventListener("input", (e) => {
  const natSelect = patientForm.querySelector('[name="nationalityId"]');
  e.target.value = isIndonesiaNationality(natSelect?.value)
    ? e.target.value.replace(/\D/g, "").slice(0, 16)
    : e.target.value.replace(/[^A-Za-z0-9]/g, "").toUpperCase().slice(0, 9);
});

const RESULT_CRITERIA_LABELS = {
  Fit: "Fit",
  FitWithNote: "Fit with Recommendations / Notes",
  IncompleteResult: "IncompleteResult",
  Unfit: "Unfit",
  TemporaryUnfit: "TemporaryUnfit",
  NeedFollowUp: "NeedFollowUp",
};

let latestCheckupHintToken = 0;

function formatLatestCheckup(rec) {
  const result = RESULT_CRITERIA_LABELS[rec.resultCriteria] || rec.resultCriteria || "—";
  return `Latest checkup — review date ${rec.reviewDate || "—"}, checkup date ${rec.dateCheckUp || "—"}, result ${result}, valid until ${rec.resultValidUntil || "—"}.`;
}

// Shown only while editing an existing patient (the form's own buttons are
// visible). Hidden for a brand-new patient and while the same form sits
// beside a checkup being entered.
async function showLatestCheckupHint(patient) {
  const token = ++latestCheckupHintToken;
  const box = document.getElementById("patient-latest-checkup");
  const btn = document.getElementById("patient-new-checkup");
  if (!box || !btn) return;
  if (!patient) {
    box.hidden = true;
    box.textContent = "";
    btn.hidden = true;
    return;
  }
  box.hidden = false;
  btn.hidden = false;
  box.textContent = "Loading latest checkup…";
  const res = await fetch(`/api/mcu-records?patientId=${patient.patientId}`, { credentials: "same-origin" });
  if (token !== latestCheckupHintToken) return;
  if (!res.ok) {
    box.textContent = "Could not load the latest checkup.";
    return;
  }
  const records = await res.json();
  if (token !== latestCheckupHintToken) return;
  patientHistoryCache = Array.isArray(records) ? records : [];
  const latest = patientHistoryCache[0];
  box.textContent = latest ? formatLatestCheckup(latest) : "No medical checkup on file.";
}

function populatePatientForm(p, title) {
  fillSelect(
    document.getElementById("patient-nationality"),
    activeLookups(nationalityCache, p?.nationalityId, "nationalityId"),
    "nationalityId",
    "name",
    "—",
  );
  patientForm.reset();
  patientForm.querySelector('[name="name"]').value = p.name || "";
  patientForm.querySelector('[name="gender"]').value = p.gender || "";
  patientForm.querySelector('[name="dateOfBirth"]').value = p.dateOfBirth || "";
  syncDateDisplays(patientForm);
  patientForm.querySelector('[name="nationalityId"]').value = p.nationalityId || "";
  patientForm.querySelector('[name="employeeId"]').value = p.employeeId || "";
  patientForm.querySelector('[name="personalId"]').value = p.personalId || "";
  updatePersonalIdFormat();
  document.getElementById("patient-form-title").textContent = title;
  showPatientRequiredNotice([]);
}

document.getElementById("edit-patient-btn")?.addEventListener("click", () => {
  if (!currentPatient) return;
  patientFormInline = false;
  populatePatientForm(currentPatient, "Edit patient");
  patientDetail.hidden = true;
  patientForm.hidden = false;
  document.getElementById("patient-form-buttons").hidden = false;
  showLatestCheckupHint(currentPatient);
});

document.getElementById("patient-new-checkup")?.addEventListener("click", async () => {
  if (!currentPatient) return;
  checkupReturnTo = patientFormOpenedFromInlineRow ? "table" : "patientDetail";
  patientFormOpenedFromInlineRow = false;
  await loadHistory(currentPatient.patientId);
  await openCheckupForm(null);
  if (checkupReturnTo === "table") setDataEntryListVisible(false);
  patientForm.scrollIntoView({ block: "start" });
});

function patientRequiredNotice() {
  let notice = document.getElementById("patient-required-notice");
  if (notice || !patientForm) return notice;
  notice = document.createElement("p");
  notice.id = "patient-required-notice";
  notice.className = "patient-required-notice";
  notice.hidden = true;
  const buttons = document.getElementById("patient-form-buttons");
  buttons?.before(notice);
  return notice;
}

function missingPatientFields() {
  const value = (selector) => patientForm.querySelector(selector)?.value.trim() ?? "";
  const checks = [
    ["[name='name']", "Name"],
    ["[name='gender']", "Gender"],
    ["[name='dateOfBirth']", "Date of birth"],
    ["[name='nationalityId']", "Nationality"],
  ];
  return checks.filter(([selector]) => !value(selector)).map(([, label]) => label);
}

function formatRequiredList(labels) {
  if (labels.length < 2) return labels[0] || "";
  if (labels.length === 2) return `${labels[0]} and ${labels[1]}`;
  return `${labels.slice(0, -1).join(", ")}, and ${labels[labels.length - 1]}`;
}

function showPatientRequiredNotice(missing) {
  const notice = patientRequiredNotice();
  if (!notice) return;
  if (!missing.length) {
    notice.hidden = true;
    notice.textContent = "";
    return;
  }
  notice.hidden = false;
  notice.textContent = missing.length === 1
    ? `${missing[0]} is required.`
    : `${formatRequiredList(missing)} are required.`;
  notice.scrollIntoView({ block: "nearest" });
}

function focusFirstMissingPatientField() {
  const selectors = [
    ["[name='name']", "[name='name']"],
    ["[name='gender']", "[name='gender']"],
    ["[name='dateOfBirth']", ".date-dmy-text"],
    ["[name='nationalityId']", "[name='nationalityId']"],
  ];
  const value = (selector) => patientForm.querySelector(selector)?.value.trim() ?? "";
  const hit = selectors.find(([check]) => !value(check));
  if (hit) patientForm.querySelector(hit[1])?.focus();
}

patientForm?.addEventListener("input", () => {
  const notice = document.getElementById("patient-required-notice");
  if (notice && !notice.hidden) showPatientRequiredNotice(missingPatientFields());
});
patientForm?.addEventListener("change", () => {
  const notice = document.getElementById("patient-required-notice");
  if (notice && !notice.hidden) showPatientRequiredNotice(missingPatientFields());
});

// Builds the request body from #patient-form's current fields and
// POSTs/PUTs it, returning {ok, patient, error}. Extracted so
// checkupForm's submit handler (below) can save the inline patient fields
// as part of one combined "Save checkup" click instead of requiring its
// own separate "Save patient" button click first — see the comment on
// checkupForm's submit listener for why that mattered.
// Writes a form message. Errors get the red .is-error style; success and
// cleared messages stay neutral, so the two are easy to tell apart.
function showFormMessage(el, text, isError = false) {
  el.textContent = text;
  el.classList.toggle("is-error", isError);
}

// Shows the required-field notice and returns true only when the patient
// form is complete enough to send.
function patientFieldsReady() {
  const missing = missingPatientFields();
  showPatientRequiredNotice(missing);
  if (missing.length) {
    focusFirstMissingPatientField();
    return false;
  }
  return true;
}

// The patient-form fields as the JSON body the patient endpoints expect.
function readPatientBody() {
  const fd = new FormData(patientForm);
  const nationalityId = fd.get("nationalityId");
  return {
    name: fd.get("name"),
    gender: fd.get("gender"),
    dateOfBirth: fd.get("dateOfBirth"),
    nationalityId: nationalityId ? Number(nationalityId) : null,
    employeeId: fd.get("employeeId"),
    personalId: fd.get("personalId"),
  };
}

async function submitPatientForm() {
  if (!patientFieldsReady()) {
    return { ok: false, error: "" };
  }
  const body = readPatientBody();
  const isEdit = !!currentPatient;
  const url = isEdit ? `/api/patients/${currentPatient.patientId}` : "/api/patients";
  const res = await fetch(url, {
    method: isEdit ? "PUT" : "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    return { ok: false, error: data.error || "Failed to save patient" };
  }
  const pRes = await fetch(`/api/patients`, { credentials: "same-origin" });
  const patients = await pRes.json();
  const id = isEdit ? currentPatient.patientId : data.patientId;
  const p = patients.find((p) => p.patientId === id);
  return p ? { ok: true, patient: p } : { ok: false, error: "Failed to reload saved patient" };
}

patientForm?.addEventListener("submit", async (e) => {
  e.preventDefault();
  if (newPatientCombined) {
    checkupForm.requestSubmit();
    return;
  }
  const msg = document.getElementById("patient-msg");
  const result = await submitPatientForm();
  if (!result.ok) {
    showFormMessage(msg, result.error, true);
    return;
  }
  showFormMessage(msg, "");
  const p = result.patient;

  // Inline case (opened alongside a new checkup, see openCheckupForm below):
  // #patient-form-buttons is hidden while inline, but the form itself isn't,
  // so pressing Enter in one of its fields can still trigger this submit
  // event — just sync currentPatient and stop, same as before. Closing the
  // patient fields here would also cancel the in-progress checkup, which
  // isn't what "update lacking details while entering a checkup" means.
  if (patientFormInline) {
    currentPatient = p;
    showFormMessage(msg, "Patient details saved.");
    return;
  }
  // Opened from the Data entry table's inline "Edit employee details"
  // button — just close the form and refresh the table row (name/company
  // may have changed) instead of jumping into the old patient-detail panel.
  if (patientFormOpenedFromInlineRow) {
    patientFormOpenedFromInlineRow = false;
    patientForm.hidden = true;
    setDataEntryListVisible(true);
    loadMcuDataTable();
    return;
  }
  // A brand-new patient has no history yet. Save goes straight into a
  // checkup for them; the patient fields stay on the checkup form so a
  // correction does not need a second trip through Edit patient. Editing
  // someone who already exists still returns to their history.
  if (!currentPatient) {
    currentPatient = p;
    checkupReturnTo = "patientDetail";
    await loadHistory(p.patientId);
    await openCheckupForm(null);
    setDataEntryListVisible(false);
    patientForm.scrollIntoView({ block: "start" });
    return;
  }
  patientForm.hidden = true;
  selectPatient(p);
});

// --- Patient detail + history -------------------------------------------

function selectPatient(p) {
  currentPatient = p;
  document.getElementById("patient-name").textContent = p.name;
  const meta = [
    p.gender,
    p.dateOfBirth,
    p.nationalityName,
    p.companyName,
    p.workPosition,
    p.employeeId ? `ID: ${p.employeeId}` : "",
  ]
    .filter(Boolean)
    .join(" · ");
  document.getElementById("patient-meta").textContent = meta || "—";
  patientDetail.hidden = false;
  document.getElementById("checkup-form").hidden = true;
  loadHistory(p.patientId);
}

// A certificate can only be minted for a closed record that isn't already
// Archived/Exit or ReEvaluate/Overdue (see DownloadCertificate's own check,
// certificate.go — this is UI-only gating so the link isn't shown just to
// fail on click; the server enforces it regardless). NeedFollowUp is never
// terminal (isTerminalResult, validity.go), so it's always CheckUpStatus
// "Open" and already excluded by the first condition alone.
function isCertificateEligible(checkUpStatus, hcValidityStatus, resultCriteria) {
  if (resultCriteria === "TemporaryUnfit" || resultCriteria === "Unfit") return true;
  const certifiable = resultCriteria === "Fit" || resultCriteria === "FitWithNote";
  const standing = hcValidityStatus || "";
  return certifiable && checkUpStatus === "Close" && standing !== "Archived / Exit" && standing !== "ReEvaluate / Overdue" && !standing.startsWith("Renewed by ");
}

async function loadHistory(patientId) {
  const res = await fetch(`/api/mcu-records?patientId=${patientId}`, { credentials: "same-origin" });
  if (!res.ok) return;
  const records = await res.json();
  patientHistoryCache = records;
  const body = document.getElementById("history-body");
  const empty = document.getElementById("history-empty");
  body.innerHTML = "";
  empty.hidden = records.length > 0;
  for (const rec of records) {
    const criteria = checkUpCriteriaCache.find((c) => c.checkUpCriteriaId === rec.checkUpCriteriaId);
    const tr = document.createElement("tr");
    const certLink = isCertificateEligible(rec.checkUpStatus, rec.hcValidityStatus, rec.resultCriteria)
      ? `<a href="${escapeHtml(certificateOpenHref(rec.mcuRecordId, rec.checkUpId, companyCache.find((c) => c.companyId === Number(currentPatient?.companyId))?.abbreviation || "", currentPatient?.name || "", rec.resultValidUntil))}" target="_blank" rel="noopener">Certificate</a>`
      : "—";
    tr.innerHTML = `
      <td>${escapeHtml(rec.checkUpId)}</td>
      <td>${escapeHtml(criteria?.name || "—")}</td>
      <td>${escapeHtml(rec.reviewDate)}</td>
      <td>${escapeHtml(rec.resultCriteria || "—")}</td>
      <td>${escapeHtml(rec.resultValidUntil || "—")}</td>
      <td>${escapeHtml(rec.checkUpStatus || "—")}</td>
      <td style="display:flex;gap:8px;flex-wrap:wrap">
        <button type="button" class="btn-ghost" data-edit-mcu="${rec.mcuRecordId}">Edit</button>
        ${certLink}
      </td>`;
    body.appendChild(tr);
  }
  body.querySelectorAll("[data-edit-mcu]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const rec = records.find((r) => r.mcuRecordId === Number(btn.dataset.editMcu));
      if (rec) {
        checkupReturnTo = "patientDetail";
        openCheckupForm(rec);
      }
    });
  });
}

// --- Checkup form ---------------------------------------------------------

// Each exam's criteria/result select and its notes box are one grid cell, so
// they stay together at every width. A block is the select's label plus the
// notes label right after it, if there is one.
function groupExamPairs() {
  if (!checkupForm) return;
  const selects = checkupForm.querySelectorAll(
    '.settings-grid select[name$="Criteria"], .settings-grid select[name$="Result"]'
  );
  for (const select of selects) {
    const label = select.closest("label");
    if (!label || label.parentElement?.classList.contains("exam-pair")) continue;
    const pair = document.createElement("div");
    pair.className = "exam-pair";
    label.before(pair);
    pair.append(label);
    // ECG and treadmill share one block: the treadmill select follows the ECG
    // select, and their notes box follows the treadmill select.
    if (select.name === "ecgResult" && pair.nextElementSibling?.tagName === "LABEL") {
      pair.append(pair.nextElementSibling);
    }
    const next = pair.nextElementSibling;
    if (next?.tagName === "LABEL" && next.querySelector('[name$="Notes"]')) {
      pair.append(next);
    }
  }
}

const checkupForm = document.getElementById("checkup-form");
groupExamPairs();
let editingMcuRecordId = null;
let riskTriggerMatched = new Set();
let visitPositionLinked = false;
let visitCompanyLinked = false;

// The patient's company is derived server-side from their latest visit, so a
// new patient (or one with no visit that has a company) has none to copy.
function employeeCompanyId() {
  return currentPatient?.companyId == null || currentPatient.companyId === "" ? "" : String(currentPatient.companyId);
}

// Like the company, the patient's position is derived server-side from their
// latest visit, so a new patient has none to copy.
function employeeWorkPosition() {
  return currentPatient?.workPosition || "";
}

function setVisitCompanySame(on) {
  const box = document.getElementById("visit-company-same");
  const input = document.getElementById("checkup-company-input");
  // With no company to copy, hide "Same as previous" and leave the visit's
  // company free to pick; it then becomes the patient's company on save.
  const hasCompany = !!employeeCompanyId();
  on = on && hasCompany;
  if (box) {
    box.checked = on;
    box.closest(".visit-same").hidden = !hasCompany;
  }
  visitCompanyLinked = on;
  if (input) input.readOnly = on;
  if (on) setCompanyField("checkup-company-select", "checkup-company-input", employeeCompanyId());
  updateCheckupCompanyRelationField();
}

// Shows/hides and fills the checkup form's Parent/Project select for the
// company currently chosen in "Company (at this visit)". Most companies
// have exactly one CompanyRelation row, so the field stays hidden and the
// one relation is picked silently — Salawati Motorindo (two rows, one per
// project) is the only production case where staff actually have to choose.
// preserveId re-selects a previously saved companyRelationId (editing an
// existing record) when it's still one of the current options.
function updateCheckupCompanyRelationField(preserveId) {
  const field = document.getElementById("checkup-relation-field");
  const select = document.getElementById("checkup-relation-select");
  if (!field || !select) return;
  const companyId = Number(document.getElementById("checkup-company-select")?.value || 0);
  const relations = companyId ? companyRelationCache.filter((cr) => cr.companyId === companyId) : [];
  if (relations.length <= 1) {
    field.hidden = true;
    select.required = false;
    select.innerHTML = `<option value="">Select…</option>`;
    select.value = relations[0] ? String(relations[0].companyRelationId) : "";
    return;
  }
  field.hidden = false;
  select.required = true;
  select.innerHTML =
    `<option value="">Select…</option>` +
    relations
      .map((cr) => `<option value="${cr.companyRelationId}">${escapeHtml(cr.parentName)} (${escapeHtml(cr.project)})</option>`)
      .join("");
  const keep = preserveId != null ? String(preserveId) : "";
  select.value = relations.some((cr) => String(cr.companyRelationId) === keep) ? keep : "";
}

function setVisitPositionSame(on) {
  const box = document.getElementById("visit-position-same");
  const input = checkupForm?.querySelector('[name="workPosition"]');
  // With no position to copy, hide "Same as previous" and leave the visit's
  // position free to type; it then becomes the patient's position on save.
  const hasPosition = !!employeeWorkPosition();
  on = on && hasPosition;
  if (box) {
    box.checked = on;
    box.closest(".visit-same").hidden = !hasPosition;
  }
  visitPositionLinked = on;
  if (input) input.readOnly = on;
  if (!on || !input) return;
  input.value = employeeWorkPosition();
  applyVisitRiskTriggers();
}

function applyVisitRiskTriggers() {
  const visit = checkupForm?.querySelector('[name="workPosition"]');
  if (!visit) return;
  const next = newlyMatchedRiskIds(visit.value, taskCategoryCache, riskTriggerMatched);
  for (const id of next.tick) {
    const box = document.querySelector(`#task-category-checkboxes input[name="taskCategoryIds"][value="${id}"]`);
    if (box) box.checked = true;
  }
  riskTriggerMatched = new Set(next.matched);
  updateFoodHandlerScopeNote();
}

function rememberRiskTriggers() {
  const visit = checkupForm?.querySelector('[name="workPosition"]');
  riskTriggerMatched = new Set(matchedRiskIds(visit?.value || "", taskCategoryCache));
}

checkupForm?.querySelector('[name="workPosition"]')?.addEventListener("input", () => {
  if (checkupForm.querySelector('[name="workPosition"]')?.readOnly) return;
  visitPositionLinked = false;
  const box = document.getElementById("visit-position-same");
  if (box) box.checked = false;
  applyVisitRiskTriggers();
});
document.getElementById("visit-company-same")?.addEventListener("change", (e) => {
  setVisitCompanySame(e.target.checked);
});
document.getElementById("visit-position-same")?.addEventListener("change", (e) => {
  setVisitPositionSame(e.target.checked);
});
document.querySelector('#patient-form [name="dateOfBirth"]')?.addEventListener("change", () => {
  if (checkupForm.hidden) return;
  checkupForm.querySelector('[name="age"]').value = ageFromDob(patientForm.querySelector('[name="dateOfBirth"]').value);
  syncCheckupReadonlyDisplays();
});

document.getElementById("new-checkup-btn")?.addEventListener("click", () => {
  checkupReturnTo = "patientDetail";
  openCheckupForm(null);
});
document.getElementById("checkup-cancel")?.addEventListener("click", () => {
  const wasReviewing = clearLinkedSubmission();
  setNewPatientCombined(false);
  checkupForm.hidden = true;
  patientForm.hidden = true;
  patientFormInline = false;
  visitPositionLinked = false;
  visitCompanyLinked = false;
  if (wasReviewing) {
    showMcuTab("submitted");
    return;
  }
  if (checkupReturnTo === "patientDetail") {
    patientDetail.hidden = false;
  } else {
    patientDetail.hidden = true;
    setDataEntryListVisible(true);
    loadMcuDataTable();
  }
});

async function fillParentCandidates(patientId, keepValue) {
  const sel = document.getElementById("parent-record-select");
  const res = await fetch(`/api/mcu-records/parent-candidates?patientId=${patientId}`, { credentials: "same-origin" });
  const candidates = res.ok ? await res.json() : [];
  sel.innerHTML =
    `<option value="">None</option>` +
    candidates
      .map((c) => `<option value="${c.mcuRecordId}">${escapeHtml(c.checkUpId)} — ${escapeHtml(c.criteria || "—")} (${escapeHtml(c.reviewDate)})</option>`)
      .join("");
  // Auto-suggest instead of leaving this blank for the user to hunt through:
  // candidates are already ordered most-recent-first (see ListParentCandidates),
  // so on a fresh checkup (no keepValue to restore) default to the most recent
  // open record — the overwhelmingly common case is exactly one candidate
  // (the checkup being renewed). Still an editable dropdown for the rare case
  // of multiple concurrent threads for the same patient.
  sel.value = keepValue || candidates[0]?.mcuRecordId || "";
}

// BMI category thresholds — legacy WebApp.html's calcBMI (Code.js's sibling
// client): Underweight < 18.5, Normal < 25, Overweight < 30, else Obesity.
// This is a reference hint only; the clinical BMI *criteria* (Normal /
// Normal w/ Therapy / Acceptable - monitor / Unacceptable) stays a judgment
// call left to the user in checkup-form's bmiCriteria select, not
// auto-derived from the category.
function bmiCategory(bmi) {
  if (bmi < 18.5) return "Underweight";
  if (bmi < 25) return "Normal";
  if (bmi < 30) return "Overweight";
  return "Obesity";
}

// BMI is computed from height/weight and recorded into the hidden bmiValue
// field. Displayed with a comma decimal separator (23,5) to match this
// app's other user-facing numeric formatting conventions.
function measureNumber(value) {
  const n = parseFloat(String(value || "").replace(",", "."));
  return Number.isFinite(n) ? n : NaN;
}

// Height and weight stay within 5 characters: digits and one decimal separator.
function restrictMeasureInput(input) {
  if (!input) return;
  input.addEventListener("input", () => {
    let next = input.value.replace(/[^\d.,]/g, "").replace(",", ".");
    const dot = next.indexOf(".");
    if (dot !== -1) next = next.slice(0, dot + 1) + next.slice(dot + 1).replace(/\./g, "");
    if (next.length > 5) next = next.slice(0, 5);
    if (next !== input.value) input.value = next;
  });
}

function calcBmi() {
  const h = measureNumber(checkupForm.querySelector('[name="heightCm"]').value) / 100;
  const w = measureNumber(checkupForm.querySelector('[name="weightKg"]').value);
  const bmiValue = checkupForm.querySelector('[name="bmiValue"]');
  const bmiLabel = document.getElementById("bmi-label");
  if (h > 0 && w > 0) {
    const bmi = w / (h * h);
    const rounded = bmi.toFixed(1);
    bmiValue.value = rounded;
    bmiLabel.textContent = `BMI: ${rounded.replace(".", ",")} (${bmiCategory(bmi)})`;
  } else {
    bmiValue.value = "";
    bmiLabel.textContent = "—";
  }
}
restrictMeasureInput(checkupForm?.querySelector('[name="heightCm"]'));
restrictMeasureInput(checkupForm?.querySelector('[name="weightKg"]'));
checkupForm?.querySelector('[name="heightCm"]')?.addEventListener("input", calcBmi);
checkupForm?.querySelector('[name="weightKg"]')?.addEventListener("input", calcBmi);
checkupForm?.querySelectorAll("input, textarea").forEach((el) => {
  if (!isNoteField(el.name)) return;
  el.addEventListener("blur", () => {
    el.value = toParagraphNote(el.value);
  });
});

function isNoteField(name) {
  return name === "recommendation" || /Notes$/.test(name);
}

// Notes are prose. Collapse repeated spaces, and when a note was stored in
// all capitals, turn it into a normal paragraph (sentence case).
function toParagraphNote(value) {
  if (value == null) return "";
  const paragraphs = String(value)
    .replace(/\r\n/g, "\n")
    .split(/\n+/)
    .map((line) => line.replace(/[ \t]+/g, " ").trim())
    .filter(Boolean)
    .map((line) => {
      const letters = line.replace(/[^A-Za-z]/g, "");
      if (letters.length < 2 || letters !== letters.toUpperCase()) return line;
      const lower = line.toLowerCase();
      return lower.replace(/(^|[.!?]\s+)([a-z])/g, (_, lead, ch) => lead + ch.toUpperCase());
    });
  return paragraphs.join("\n");
}

// Checkup status (Open/Close) is a read-only preview here — the server
// always recomputes the real value from Result alone (see
// checkUpStatusFor in validity.go); this just shows staff what to expect
// before they save, so it doesn't drift from what actually gets stored.
// Archived/Exit is a separate field (HcValidityStatus) and doesn't affect
// this display — see the checkbox's own label for that state.
const TERMINAL_RESULTS = ["Fit", "FitWithNote"];

function setCheckupStatusDisplay(status) {
  const el = document.getElementById("checkup-status-display");
  el.textContent = status;
  el.dataset.status = status;
}

function updateUnfitNotice() {
  const notice = document.getElementById("unfit-notice");
  if (!notice || !checkupForm) return;
  notice.hidden = checkupForm.querySelector('[name="resultCriteria"]').value !== "Unfit";
}

function updateCheckupStatusDisplay() {
  const result = checkupForm.querySelector('[name="resultCriteria"]').value;
  setCheckupStatusDisplay(TERMINAL_RESULTS.includes(result) ? "Close" : "Open");
  updateUnfitNotice();
}
checkupForm?.querySelector('[name="resultCriteria"]')?.addEventListener("change", updateCheckupStatusDisplay);

// Result notes and Recommendation / follow-up depend on the result:
//   Fit                         -> both locked and empty
//   Fit with note, Temporarily
//   unfit, Unfit                -> notes required; follow-up locked and empty
//   Need follow up, Incomplete  -> notes locked and empty; follow-up required
//   (no result yet)             -> neither locked nor required
// Locked uses readOnly and clears the box. readOnly still submits, so the
// saved value is empty rather than a disabled field dropping out of FormData.
function missingCheckupFields() {
  const value = (selector) => checkupForm.querySelector(selector)?.value.trim() ?? "";
  const checks = [
    [() => !value('[name="checkUpCriteriaId"]'), "Checkup purpose"],
    [() => !value('[name="workLocation"]'), "Work location"],
    [() => !document.getElementById("checkup-company-select")?.value, "Company (at this visit)"],
    [() => !document.getElementById("checkup-relation-field")?.hidden && !value("#checkup-relation-select"), "Parent / Project (at this visit)"],
    [() => !value('[name="workPosition"]'), "Work position (at this visit)"],
    [() => !value('[name="dateCheckUp"]'), "Date of checkup"],
    [() => !value('[name="submissionDate"]'), "Submission date"],
    [() => !value('[name="resultCriteria"]'), "Result"],
  ];
  const missing = checks.filter(([test]) => test()).map(([, label]) => label);
  const notes = checkupForm.querySelector('[name="resultNotes"]');
  const recommendation = checkupForm.querySelector('[name="recommendation"]');
  if (notes?.required && !notes.value.trim()) missing.push("Result notes");
  if (recommendation?.required && !recommendation.value.trim()) missing.push("Recommendation / follow-up");
  return missing;
}

function focusFirstMissingCheckupField() {
  const notes = checkupForm.querySelector('[name="resultNotes"]');
  const recommendation = checkupForm.querySelector('[name="recommendation"]');
  const selectors = [
    [() => !checkupForm.querySelector('[name="checkUpCriteriaId"]')?.value.trim(), '[name="checkUpCriteriaId"]'],
    [() => !checkupForm.querySelector('[name="workLocation"]')?.value.trim(), "#work-location-checkboxes input"],
    [() => !document.getElementById("checkup-company-select")?.value, "#checkup-company-input"],
    [() => !document.getElementById("checkup-relation-field")?.hidden && !checkupForm.querySelector("#checkup-relation-select")?.value, "#checkup-relation-select"],
    [() => !checkupForm.querySelector('[name="workPosition"]')?.value.trim(), '[name="workPosition"]'],
    [() => !checkupForm.querySelector('[name="dateCheckUp"]')?.value.trim(), '[name="dateCheckUp"]'],
    [() => !checkupForm.querySelector('[name="submissionDate"]')?.value.trim(), '[name="submissionDate"]'],
    [() => !checkupForm.querySelector('[name="resultCriteria"]')?.value.trim(), '[name="resultCriteria"]'],
    [() => notes?.required && !notes.value.trim(), '[name="resultNotes"]'],
    [() => recommendation?.required && !recommendation.value.trim(), '[name="recommendation"]'],
  ];
  const hit = selectors.find(([test]) => test());
  if (!hit) return;
  const el = checkupForm.querySelector(hit[1]);
  (el?.closest(".date-dmy")?.querySelector(".date-dmy-text") || el)?.focus();
}

function updateResultDependentFields() {
  const result = checkupForm.querySelector('[name="resultCriteria"]').value;
  const notes = checkupForm.querySelector('[name="resultNotes"]');
  const recommendation = checkupForm.querySelector('[name="recommendation"]');
  const notesRule = { Fit: "locked", FitWithNote: "required", Unfit: "required", TemporaryUnfit: "required", NeedFollowUp: "locked", IncompleteResult: "locked" }[result];
  const recRule = { Fit: "locked", FitWithNote: "locked", Unfit: "locked", TemporaryUnfit: "locked", NeedFollowUp: "required", IncompleteResult: "required" }[result];
  applyResultFieldRule(notes, notesRule);
  applyResultFieldRule(recommendation, recRule);
}

function applyResultFieldRule(el, rule) {
  if (!el) return;
  el.readOnly = rule === "locked";
  el.required = rule === "required";
  if (rule === "locked") el.value = "";
}

checkupForm?.querySelector('[name="resultCriteria"]')?.addEventListener("change", () => {
  updateResultDependentFields();
  const notes = checkupForm.querySelector('[name="resultNotes"]');
  const recommendation = checkupForm.querySelector('[name="recommendation"]');
  if (notes.required) notes.focus();
  else if (recommendation.required) recommendation.focus();
});

// Field names shared verbatim between the McuRecord JSON shape
// (internal/entrymcu/mcurecord.go's json tags) and this form's input
// `name` attributes, so populate/collect can loop instead of listing every
// exam field twice.
const checkupFieldNames = [
  "reviewer", "age", "heightCm", "weightKg", "bloodType", "workPosition", "validityPeriodMonths",
  "bmiValue", "bmiCriteria", "bmiNotes", "smoking", "otherPhysicalExamNotes", "bpCriteria", "bpNotes",
  "visionCriteria", "visionNotes", "cbcCriteria", "cbcNotes", "lipidCriteria", "lipidNotes",
  "glucoseCriteria", "glucoseNotes", "liverCriteria", "liverNotes", "kidneyCriteria", "kidneyNotes",
  "urinalysisCriteria", "urinalysisNotes", "radiologyResult", "radiologyNotes",
  "echocardiographyCriteria", "echocardiographyNotes", "ctScanCriteria", "ctScanNotes",
  "usgAbdomenCriteria", "usgAbdomenNotes",
  "ecgResult", "treadmillResult", "ecgTreadmillNotes",
  "herniaHemorrhoidCriteria", "herniaHemorrhoidNotes", "spirometryCriteria", "spirometryNotes",
  "audiometryCriteria", "audiometryNotes", "hepBCriteria", "hepBNotes", "hepACriteria", "hepANotes",
  "rectalSwabCriteria", "rectalSwabNotes", "hivCriteria",
  "resultNotes", "recommendation", "additionalNotes",
];

// Suggests the next checkup's criteria from the patient's own history, per
// the user's stated rules (in priority order — an acute follow-up condition
// on the last visit should win over the routine pre-employment/annual
// progression below it):
//   1. no previous records at all           -> Pre Employment
//   2. latest result was open (NeedFollowUp, IncompleteResult, Unfit, or
//      TemporaryUnfit)                      -> Consultation Result
//   3. latest criteria was Pre Employment    -> Annual (same company) or
//                                                Pre Employment (different company)
//   4. otherwise (my own addition, not part of the user's stated rules —
//      a fallback for the steady-state case: Annual/Fit for Task/Bi-annual
//      with a Fit result just continues as a routine Annual checkup)
// This is a suggestion only — staff can always pick a different value from
// the dropdown, so it's implemented client-side rather than as a server-
// enforced rule.
function suggestNextCriteria(history, currentCompanyId) {
  if (!history || history.length === 0) {
    return { name: "PreEmployment", reason: "no previous checkups on file" };
  }
  const latest = history[0];
  if (
    latest.resultCriteria === "NeedFollowUp" ||
    latest.resultCriteria === "IncompleteResult" ||
    latest.resultCriteria === "Unfit" ||
    latest.resultCriteria === "TemporaryUnfit"
  ) {
    return { name: "ConsultationResult", reason: `previous result was ${latest.resultCriteria}` };
  }
  const latestCriteria = checkUpCriteriaCache.find((c) => c.checkUpCriteriaId === latest.checkUpCriteriaId);
  const purpose = normLookupName(latestCriteria?.name);
  const isFoodHandler = (latest.taskCategoryNames || "").toLowerCase().includes("food handler");

  if (isFoodHandler && (purpose === "preemployment" || purpose === "annual")) {
    return { name: "Bi-Annual", reason: `Food Handler 6-month recheck due after previous ${latestCriteria.name}` };
  }
  if (isFoodHandler && purpose === "biannual") {
    return { name: "Annual", reason: "Food Handler's 12-month full checkup is due after the Bi-Annual recheck" };
  }

  if (purpose === "preemployment") {
    const sameCompany = latest.companyId && currentCompanyId && latest.companyId === currentCompanyId;
    return sameCompany
      ? { name: "Annual", reason: "previous was PreEmployment at the same company" }
      : { name: "PreEmployment", reason: "previous was PreEmployment at a different company" };
  }
  return { name: "Annual", reason: "routine follow-on from the previous checkup" };
}

// Food Handler's Bi-annual recheck is a reduced-scope visit (physical exam
// — HerniaHemorrhoidCriteria/Notes — plus RectalSwabCriteria/Notes only,
// not the full exam panel) rather than a hard field lock: this is a
// non-blocking hint, since a real visit can still turn up something that
// needs recording in another section, and every exam field is already
// optional in this form regardless of criteria/task category.
function updateFoodHandlerScopeNote() {
  const note = document.getElementById("food-handler-scope-note");
  if (!note) return;
  const criteria = checkUpCriteriaCache.find(
    (c) => c.checkUpCriteriaId === Number(checkupForm.querySelector('[name="checkUpCriteriaId"]').value)
  );
  const selectedNames = getSelectedTaskCategoryIds()
    .map((id) => taskCategoryCache.find((t) => t.taskCategoryId === id)?.name || "")
    .join("; ");
  const isFoodHandler = selectedNames.toLowerCase().includes("food handler");
  if (isFoodHandler && normLookupName(criteria?.name) === "biannual") {
    note.textContent = "Food Handler Bi-annual recheck: typically only History and Physical Exam (Hernia/Hemorrhoid) and Rectal Swab are needed — other sections can stay blank unless something needs recording.";
  } else {
    note.textContent = "";
  }
}
document.getElementById("checkup-criteria-select")?.addEventListener("change", updateFoodHandlerScopeNote);
document.getElementById("task-category-checkboxes")?.addEventListener("change", updateFoodHandlerScopeNote);

function applySuggestedCriteria() {
  const suggestion = suggestNextCriteria(patientHistoryCache, currentPatient?.companyId);
  const match = checkUpCriteriaCache.find((c) => c.name === suggestion.name);
  const note = document.getElementById("criteria-suggestion-note");
  if (match) {
    checkupForm.querySelector('[name="checkUpCriteriaId"]').value = match.checkUpCriteriaId;
    note.textContent = `Suggested: ${suggestion.name} (${suggestion.reason}). Change the dropdown above if this doesn't apply.`;
  } else {
    note.textContent = "";
  }
}

// Checkup ID and Age are both server/system-derived (ID auto-generated,
// age computed from the patient's DOB) rather than something a reviewer
// fills in — shown as plain text via the hidden-input + .ro-value pair
// below instead of an editable-looking box, same "not editable" cue as the
// Data entry table's other read-only field displays (see .ro-value).
function syncCheckupReadonlyDisplays() {
  const idValue = checkupForm.querySelector('[name="checkUpId"]').value;
  const ageValue = checkupForm.querySelector('[name="age"]').value;
  document.getElementById("checkup-id-display").textContent = idValue || "—";
  document.getElementById("checkup-age-display").textContent = ageValue ? `${ageValue} years` : "—";
}

// The newest record whose field is not blank, or null when every record
// leaves it empty. patientHistoryCache is already newest first.
function latestNonBlankValue(records, key) {
  for (const r of records) {
    const v = r[key];
    if (v !== null && v !== undefined && String(v).trim() !== "") return v;
  }
  return null;
}

// New checkup: start blood type and height from the patient's own values.
// Those are kept in step with the newest checkup that recorded each, so they
// are the reference; the history lookup is only a fallback for a patient row
// that hasn't been synced yet.
function prefillFromHistory() {
  const height = currentPatient?.heightCm ?? latestNonBlankValue(patientHistoryCache, "heightCm");
  if (height !== null && height !== undefined) checkupForm.querySelector('[name="heightCm"]').value = height;
  const bloodType = currentPatient?.bloodType || latestNonBlankValue(patientHistoryCache, "bloodType");
  if (bloodType !== null && bloodType !== undefined) checkupForm.querySelector('[name="bloodType"]').value = bloodType;
}

async function openCheckupForm(rec) {
  setNewPatientCombined(false);
  editingMcuRecordId = rec ? rec.mcuRecordId : null;
  riskTriggerMatched = new Set();
  visitPositionLinked = !rec;
  checkupForm.reset();
  showFormMessage(document.getElementById("checkup-msg"), "");
  patientDetail.hidden = true;
  checkupForm.hidden = false;
  document.getElementById("copy-checkup-row").hidden = true;
  document.getElementById("checkup-form-title").textContent = rec ? `Edit checkup — ${rec.checkUpId}` : "New checkup";

  await fillParentCandidates(currentPatient.patientId, rec?.parentRecordId || "");

  // healthCertificateFileName is server-managed (see certificate.go): once
  // a certificate PDF exists, saving this form again invalidates the cached
  // copy so the next download reflects the edit instead of serving stale
  // content — surfaced here as a status note, not an editable field.
  const certStatus = document.getElementById("cert-status");
  if (rec?.healthCertificateFileName) {
    certStatus.textContent = "A certificate has already been generated for this checkup. Saving changes here will invalidate it — the next download will regenerate a fresh copy reflecting your edits.";
  } else {
    certStatus.textContent = "No certificate generated yet. Close this checkup, then download it from the patient's history once saved.";
  }

  if (!rec) {
    // New checkup: expand the patient's own details right alongside the
    // checkup fields, editable, so any lacking info can be fixed here
    // instead of a separate trip to "Edit patient" — see patientFormInline
    // and its submit-handler branch above.
    patientFormInline = true;
    populatePatientForm(currentPatient, "Patient details");
    patientForm.hidden = false;
    // One Save/Close pair on screen instead of two: this form's own
    // Save patient/Cancel buttons are hidden here, and checkupForm's
    // submit handler saves both tables — see submitPatientForm() above.
    document.getElementById("patient-form-buttons").hidden = true;
    showLatestCheckupHint(null);

    checkupForm.querySelector('[name="checkUpId"]').value = `MedC${Date.now().toString().slice(-6)}`;
    const reviewerField = checkupForm.querySelector('[name="reviewer"]');
    reviewerField.value = window.__USER_NAME__ || "";
    reviewerField.readOnly = true;
    const today = clinicToday();
    checkupForm.querySelector('[name="reviewDate"]').value = today;
    checkupForm.querySelector('[name="dateCheckUp"]').value = "";
  checkupForm.querySelector('[name="submissionDate"]').value = "";
    syncDateDisplays(checkupForm);
    updateShortProjectDisplay();
    setVisitCompanySame(true);
    setVisitPositionSame(true);
    updateCheckupStatusDisplay();
    updateResultDependentFields();
    if (currentPatient.dateOfBirth) {
      checkupForm.querySelector('[name="age"]').value = ageFromDob(currentPatient.dateOfBirth);
    }
    syncCheckupReadonlyDisplays();
    prefillFromHistory();
    fillCopyCheckupSelect();
    calcBmi();
    applySuggestedCriteria();
    updateFoodHandlerScopeNote();
    return;
  }
  patientFormInline = false;
  patientForm.hidden = true;
  document.getElementById("criteria-suggestion-note").textContent = "";

  checkupForm.querySelector('[name="checkUpId"]').value = rec.checkUpId;
  checkupForm.querySelector('[name="reviewDate"]').value = rec.reviewDate || "";
  checkupForm.querySelector('[name="dateCheckUp"]').value = rec.dateCheckUp || "";
  checkupForm.querySelector('[name="submissionDate"]').value = rec.submissionDate || "";
  syncDateDisplays(checkupForm);
  updateShortProjectDisplay();
  checkupForm.querySelector('[name="checkUpCriteriaId"]').value = rec.checkUpCriteriaId || "";
  setWorkLocationCheckboxes(rec.workLocation);
  setTaskCategoryCheckboxes(rec.taskCategoryNames);
  setCompanyField("checkup-company-select", "checkup-company-input", rec.companyId);
  fillResultFromRecord(rec.resultCriteria);
  updateResultDependentFields();
  updateCheckupStatusDisplay();
  fillCheckupFields(rec, checkupFieldNames);
  updateResultDependentFields();
  syncCheckupReadonlyDisplays();
  calcBmi();
  rememberRiskTriggers();
  updateFoodHandlerScopeNote();
  const companySame = String(rec.companyId ?? "") === String(currentPatient?.companyId ?? "");
  const positionSame = String(rec.workPosition || "").trim() === String(currentPatient?.workPosition || "").trim();
  setVisitCompanySame(companySame);
  setVisitPositionSame(positionSame);
  // setVisitCompanySame just called updateCheckupCompanyRelationField()
  // itself (with no preserved id); redo it now with the saved value so an
  // existing Salawati-style record reopens showing which parent/project it
  // was actually recorded under, rather than resetting to unselected.
  updateCheckupCompanyRelationField(rec.companyRelationId);
}

// Fills the checkup form's exam fields from a saved record. Used by Edit
// (every field) and by Copy from previous checkup (the exam subset).
function fillCheckupFields(rec, names) {
  for (const name of names) {
    const el = checkupForm.querySelector(`[name="${name}"]`);
    if (!el) continue;
    const raw = rec[name] ?? "";
    const shown = isNoteField(name) ? toParagraphNote(raw) : raw;
    if (el.tagName === "SELECT" && shown !== "" && !Array.from(el.options).some((opt) => opt.value === String(shown))) {
      const opt = document.createElement("option");
      opt.value = String(shown);
      opt.textContent = String(shown);
      el.appendChild(opt);
    }
    el.value = shown;
    if (name === "reviewer") el.readOnly = true;
  }
}

// Sets the result select to a saved value, keeping a value that is no longer
// an active option so the record still shows what was saved.
function fillResultFromRecord(resultCriteria) {
  const resultSelect = checkupForm.querySelector('[name="resultCriteria"]');
  if (resultCriteria && !Array.from(resultSelect.options).some((opt) => opt.value === resultCriteria)) {
    resultSelect.insertAdjacentHTML("beforeend", `<option value="${escapeHtml(resultCriteria)}">${escapeHtml(resultCriteria)}</option>`);
  }
  resultSelect.value = resultCriteria || "";
}

// Fields a copy leaves as the form set them: reviewer, age, visit position,
// and validity (derived from the result).
const COPY_SKIP_FIELDS = new Set(["reviewer", "age", "workPosition", "validityPeriodMonths"]);

function fillCopyCheckupSelect() {
  const row = document.getElementById("copy-checkup-row");
  const select = document.getElementById("copy-checkup-select");
  const records = patientHistoryCache.filter((r) => r.mcuRecordId);
  row.hidden = records.length === 0;
  select.innerHTML = records
    .map((r) => `<option value="${r.mcuRecordId}">${escapeHtml(r.checkUpId)} · ${escapeHtml(r.reviewDate || "")}</option>`)
    .join("");
}

// Copies exam data from the chosen earlier checkup into a new checkup. The
// visit company, position, dates, result and purpose are not copied; the
// copied checkup is linked to the source through "Links to".
function copyFromPrevious() {
  const select = document.getElementById("copy-checkup-select");
  const rec = patientHistoryCache.find((r) => String(r.mcuRecordId) === select.value);
  if (!rec) return;
  const copied = checkupFieldNames.filter((name) => !COPY_SKIP_FIELDS.has(name));
  fillCheckupFields(rec, copied);
  checkupForm.querySelector('[name="dateCheckUp"]').value = rec.dateCheckUp || "";
  checkupForm.querySelector('[name="submissionDate"]').value = rec.submissionDate || "";
  syncDateDisplays(checkupForm);
  updateShortProjectDisplay();
  fillResultFromRecord(rec.resultCriteria);
  setWorkLocationCheckboxes(rec.workLocation);
  setTaskCategoryCheckboxes(rec.taskCategoryNames);
  const parent = checkupForm.querySelector('[name="parentRecordId"]');
  if (parent && Array.from(parent.options).some((o) => o.value === String(rec.mcuRecordId))) {
    parent.value = String(rec.mcuRecordId);
  }
  updateResultDependentFields();
  syncCheckupReadonlyDisplays();
  calcBmi();
  rememberRiskTriggers();
  updateFoodHandlerScopeNote();
  showFormMessage(document.getElementById("checkup-msg"), `Copied exam data from ${rec.checkUpId}. Check every value before saving.`);
}

document.getElementById("copy-checkup-btn").addEventListener("click", copyFromPrevious);

// Short-project flag — ports Code.js's MCU_CHECKUP_MAX_AGE_DAYS/
// MCU_CHECKUP_HARD_MAX_DAYS soft/hard gate, but as a fact the form displays
// rather than a judgment call the user makes: the checkbox is read-only
// (disabled) and auto-checked once dateCheckUp is >=180 days old, not a
// manual toggle behind a confirm() dialog like before — "is this a short
// project" isn't something a reviewer decides, it's just how old the date
// is. Since a disabled checkbox is excluded from FormData, the submit
// handler below recomputes isShortProject from dateCheckUp directly rather
// than reading the checkbox's checked state, so the checkbox stays purely a
// display and the actual value can't drift from what it shows. More than
// 365 days old is a separate hard block (save is refused entirely,
// regardless of the flag) — this is a UX nudge only, the server enforces
// both thresholds independently in validateCheckupDate (mcurecord.go).
const CHECKUP_MAX_AGE_DAYS = 180;
const CHECKUP_HARD_MAX_DAYS = 360;

// "Today" is the clinic's calendar day (clinic-date.js), not the browser's
// zone, so this check agrees with the server's validation.
function daysAgo(dateStr) {
  return clinicDaysBetween(dateStr, clinicToday());
}

function isShortProjectDue(dateCheckUpValue) {
  const diffDays = daysAgo(dateCheckUpValue);
  return diffDays !== null && diffDays >= CHECKUP_MAX_AGE_DAYS;
}

function updateShortProjectDisplay() {
  const dateVal = checkupForm.querySelector('[name="dateCheckUp"]').value;
  checkupForm.querySelector('[name="isShortProject"]').checked = isShortProjectDue(dateVal);
}

checkupForm?.querySelector('[name="dateCheckUp"]')?.addEventListener("change", (e) => {
  const diffDays = daysAgo(e.target.value);
  if (diffDays !== null && diffDays < 0) {
    alert("The check-up date cannot be in the future. Choose today or an earlier date.");
    e.target.value = "";
    syncDateDisplay(e.target);
  } else if (diffDays !== null && diffDays > CHECKUP_HARD_MAX_DAYS) {
    alert(`This check-up date is more than ${CHECKUP_HARD_MAX_DAYS} days in the past (${diffDays} days ago). Choose a check-up date within the last ${CHECKUP_HARD_MAX_DAYS} days.`);
    e.target.value = "";
    syncDateDisplay(e.target);
  }
  updateShortProjectDisplay();
});

checkupForm?.querySelector('[name="submissionDate"]')?.addEventListener("change", (e) => {
  const diffDays = daysAgo(e.target.value);
  if (diffDays !== null && diffDays < 0) {
    alert("The submission date cannot be in the future. Choose today or an earlier date.");
    e.target.value = "";
  } else if (diffDays !== null && diffDays > CHECKUP_HARD_MAX_DAYS) {
    alert(`This submission date is more than ${CHECKUP_HARD_MAX_DAYS} days in the past (${diffDays} days ago). Choose a date within the last ${CHECKUP_HARD_MAX_DAYS} days.`);
    e.target.value = "";
  }
  syncDateDisplay(e.target);
});

checkupForm?.addEventListener("submit", async (e) => {
  e.preventDefault();
  if (!currentPatient && !patientFormInline) return;
  const msg = document.getElementById("checkup-msg");

  // New-checkup flow shows the patient fields inline (patientFormInline)
  // with its own Save/Cancel buttons hidden (#patient-form-buttons) so
  // there's exactly one Save/Close pair on screen — this is the one place
  // that pair's click actually persists both tables: save the patient
  // edits first (via the same request submitPatientForm's own submit
  // handler above uses), and only continue to the checkup save if that
  // succeeds, so a patient-side validation error doesn't silently leave
  // the patient half unsaved while the checkup half goes through anyway.
  // A brand-new patient is not saved on its own here: the patient and the
  // checkup go to /api/mcu-records/new-patient together, so one failing half
  // leaves neither table changed. Only the fields are checked up front.
  const isNewPatient = patientFormInline && !currentPatient;
  if (patientFormInline && !isNewPatient) {
    const patientResult = await submitPatientForm();
    if (!patientResult.ok) {
      showFormMessage(msg, patientResult.error, true);
      return;
    }
    currentPatient = patientResult.patient;
  }
  if (isNewPatient && !patientFieldsReady()) {
    showFormMessage(msg, "");
    return;
  }
  if (patientFormInline) {
    const dob = patientForm.querySelector('[name="dateOfBirth"]')?.value;
    if (dob) checkupForm.querySelector('[name="age"]').value = ageFromDob(dob);
  }

  const missingCheckup = missingCheckupFields();
  if (missingCheckup.length) {
    showFormMessage(msg, missingCheckup.length === 1
      ? `${missingCheckup[0]} is required.`
      : `${formatRequiredList(missingCheckup)} are required.`, true);
    focusFirstMissingCheckupField();
    return;
  }

  const fd = new FormData(e.target);
  const num = (name) => {
    const v = fd.get(name);
    return v === "" || v === null ? null : Number(v);
  };
  const str = (name) => {
    const v = fd.get(name);
    if (v === "" || v === null) return null;
    if (!isNoteField(name)) return v;
    const note = toParagraphNote(v);
    return note === "" ? null : note;
  };
  const parentRecordId = fd.get("parentRecordId");

  const body = {
    patientId: currentPatient?.patientId,
    timestamp: new Date().toISOString().slice(0, 19).replace("T", " "),
    reviewDate: fd.get("reviewDate"),
    checkUpId: fd.get("checkUpId"),
    reviewer: str("reviewer"),
    checkUpCriteriaId: num("checkUpCriteriaId"),
    workLocation: str("workLocation"),
    taskCategoryIds: getSelectedTaskCategoryIds(),
    age: num("age"),
    heightCm: num("heightCm"),
    weightKg: num("weightKg"),
    companyId: num("companyId"),
    companyRelationId: num("companyRelationId"),
    workPosition: str("workPosition"),
    dateCheckUp: str("dateCheckUp"),
    submissionDate: str("submissionDate"),
    isShortProject: isShortProjectDue(str("dateCheckUp")),
    validityPeriodMonths: num("validityPeriodMonths"),
    bmiValue: num("bmiValue"),
    parentRecordId: parentRecordId ? Number(parentRecordId) : null,
    resultCriteria: str("resultCriteria"),
    // CheckUpStatus (Open/Close) and IsTerminal are always recomputed
    // server-side from resultCriteria (see checkUpStatusFor in
    // validity.go). HcValidityStatus (Archived/Exit, Renewed by X) is
    // fully system-derived server-side and never sent from the client.
  };
  for (const name of checkupFieldNames) {
    // Numeric fields are sent above as numbers; everything else goes as a string.
    if (name === "reviewer" || name === "workPosition" || name === "age" || name === "heightCm" || name === "weightKg" || name === "validityPeriodMonths" || name === "bmiValue") {
      continue;
    }
    body[name] = str(name);
  }

  const isEdit = !!editingMcuRecordId;
  let url = isEdit ? `/api/mcu-records/${editingMcuRecordId}` : "/api/mcu-records";
  let payload = body;
  if (isNewPatient) {
    url = "/api/mcu-records/new-patient";
    payload = { patient: readPatientBody(), checkup: body };
  }
  const res = await fetch(url, {
    method: isEdit ? "PUT" : "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    showFormMessage(msg, data.error || "Failed to save checkup", true);
    return;
  }
  showFormMessage(msg, "");
  // Opened from the Submitted page: link the record just saved to that document (Reviewed when it has
  // a result), so staff never type the Checkup ID. An edit of the already linked checkup links it again. The checkup is already saved at this point; a failed link is
  // reported with the ID so it can still be closed by hand on the Submitted page.
  if (linkedSubmission) {
    const savedPatientId = isNewPatient ? data.patientId : currentPatient?.patientId;
    await closeLinkedSubmission(String(body.checkUpId || "").trim(), savedPatientId, !!body.resultCriteria);
  }
  if (isNewPatient) {
    const pRes = await fetch(`/api/patients`, { credentials: "same-origin" });
    const patients = await pRes.json();
    currentPatient = patients.find((p) => p.patientId === data.patientId) || { patientId: data.patientId };
  }
  setNewPatientCombined(false);
  checkupForm.hidden = true;
  patientForm.hidden = true;
  patientFormInline = false;
  if (returnToSubmitted) {
    returnToSubmitted = false;
    patientDetail.hidden = true;
    showMcuTab("submitted");
    return;
  }
  if (checkupReturnTo === "table") {
    patientDetail.hidden = true;
    setDataEntryListVisible(true);
    loadMcuDataTable();
    return;
  }
  patientDetail.hidden = false;
  loadHistory(currentPatient.patientId);
});

// Dashboard charts and the summary PDF live in dashboard.js. The PDF is
// drawn on the server with go-pdf/fpdf (see dashboard_pdf.go).

// --- Tabs (Data entry / MCU data / Dashboard) -----------------------------
// Same idiom as programs.html's showTab() (app.js) — toggle .active on the
// clicked button, .hidden on the matching pane, load that pane's data on
// demand. Dashboard's button/pane only exist in the DOM for admin (see the
// {{if eq .Role "admin"}} guard in mcu.html), so tab === "dashboard" is
// unreachable for other roles.
const MCU_SECTION_LABELS = {
  submitted: "Submitted",
  employee: "Employee",
  "data-entry": "Data entry",
  "mcu-data": "MCU data",
  dashboard: "Dashboard",
};
let mcuSection = "Employee";
// Employee is one row per person (their latest checkup). MCU data is every visit.
let mcuListLatest = true;
let refreshPresence = () => {};

function showMcuTab(tab) {
  document.querySelectorAll("#mcu-tabs .app-tab").forEach((btn) => {
    btn.classList.toggle("active", btn.dataset.tab === tab);
  });
  const listTab = tab === "employee" || tab === "mcu-data";
  const dataEntryPane = document.getElementById("tab-data-entry");
  if (dataEntryPane) dataEntryPane.hidden = tab !== "data-entry";
  document.getElementById("tab-mcu-data").hidden = !listTab;
  const submittedPane = document.getElementById("tab-submitted");
  if (submittedPane) submittedPane.hidden = tab !== "submitted";
  if (tab === "submitted") {
    // An open checkup form belongs to the Employee view; leave it behind.
    if (newPatientCombined) setNewPatientCombined(false);
    checkupForm.hidden = true;
    patientForm.hidden = true;
    patientDetail.hidden = true;
  }
  // Leaving the list tabs (Submitted after Cancel or Save on a review, or Dashboard mid-form) must also
  // tear down the form chrome: the floating Save/Cancel bar and the is-form-focus layout are only
  // removed by this call, so skipping it left the bar on screen.
  if (!listTab) setDataEntryListVisible(true);
  const dashboardPane = document.getElementById("tab-dashboard");
  if (dashboardPane) dashboardPane.hidden = tab !== "dashboard";
  mcuSection = MCU_SECTION_LABELS[tab] || "MCU data";
  refreshPresence();
  if (tab === "data-entry" && document.getElementById("mcu-search-input")) {
    runSearch(document.getElementById("mcu-search-input").value.trim());
  }
  if (listTab) {
    mcuListLatest = tab === "employee";
    syncEmployeeChrome();
    if (newPatientCombined) setNewPatientCombined(false);
    setDataEntryListVisible(true);
    loadMcuDataTable();
  }
  if (tab === "dashboard") loadReport();
  if (tab === "submitted" && window.loadSubmittedTab) window.loadSubmittedTab();
}
document.getElementById("mcu-tabs")?.addEventListener("click", (e) => {
  const btn = e.target.closest("[data-tab]");
  if (!btn) return;
  // Switching tabs mid-review abandons it, so the next checkup saved is not linked to the document.
  clearLinkedSubmission();
  showMcuTab(btn.dataset.tab);
});

// Deep link for shortcuts like /mcu?tab=dashboard. No-ops for roles that
// don't have that tab button in the DOM (see the admin guard above). Hides
// the other tab buttons too, so a dashboard-only link actually lands on a
// dashboard-only view instead of just pre-selecting the tab. Reads the
// query key/value case-insensitively — text typed into an activity summary
// can get its casing mangled before it reaches here (see case-guard.js), so
// "Tab=dashboard" or "tab=Dashboard" must still work, not just "tab=dashboard".
//
// Runs on DOMContentLoaded, not inline: showMcuTab("dashboard") calls
// loadReport(), which is defined in dashboard.js — loaded by a later
// <script> tag than this one. Calling it inline here would throw
// "loadReport is not defined" and silently abort the rest of this script,
// including everything below this block.
function caseInsensitiveParam(name) {
  for (const [key, value] of new URLSearchParams(location.search)) {
    if (key.toLowerCase() === name) return value.toLowerCase();
  }
  return null;
}
document.addEventListener("DOMContentLoaded", () => {
  const deepLinkTab = caseInsensitiveParam("tab");
  if (!deepLinkTab || !document.querySelector(`#mcu-tabs [data-tab="${deepLinkTab}"]`)) return;
  showMcuTab(deepLinkTab);
  document.querySelectorAll("#mcu-tabs [data-tab]").forEach((btn) => {
    if (btn.dataset.tab !== deepLinkTab) btn.hidden = true;
  });
  const backLink = document.getElementById("mcu-back-to-activities");
  if (backLink) backLink.hidden = false;
  if (deepLinkTab === "dashboard") document.getElementById("report-this-month")?.click();
});

// --- MCU data tab: every checkup, searchable/filterable, client-side sort -
// Same "load everything, sort/render in JS" convention as loadReport()'s
// table and the programs/company lists — the dataset is small enough that a
// dedicated server-side sort isn't worth the extra endpoint surface.
let mcuDataRows = [];
let mcuDataSort = { key: "reviewDate", dir: "desc" };
let employeeSort = { key: "patientName", dir: "asc" };
let mcuDataTodayOnly = false;
let mcuDataExpiringOnly = false;
let mcuDataShownRows = [];

function currentListSort() {
  return mcuListLatest ? employeeSort : mcuDataSort;
}

function setCurrentListSort(next) {
  if (mcuListLatest) employeeSort = next;
  else mcuDataSort = next;
}

function syncEmployeeChrome() {
  document.getElementById("tab-mcu-data")?.classList.toggle("is-employee", mcuListLatest);
  document.getElementById("mcu-employee-head")?.classList.toggle("is-head-off", !mcuListLatest);
  document.getElementById("mcu-data-head")?.classList.toggle("is-head-off", mcuListLatest);
}

function mcuDataFiltersActive() {
  const name = document.getElementById("mcu-data-name")?.value.trim() || "";
  const companyId = document.getElementById("mcu-data-company")?.value || "";
  const today = mcuListLatest ? false : mcuDataTodayOnly;
  return !!(name || companyId || today || mcuDataExpiringOnly);
}

// Phone and other narrow widths start with no rows. A name search or one of
// the filters is what brings matching records onto the screen.
function mcuDataNarrowList() {
  return window.matchMedia("(max-width: 640px)").matches;
}

function clearMcuDataFilters() {
  const name = document.getElementById("mcu-data-name");
  const companyInput = document.getElementById("mcu-data-company-input");
  const company = document.getElementById("mcu-data-company");
  if (name) name.value = "";
  if (companyInput) companyInput.value = "";
  if (company) company.value = "";
  mcuDataTodayOnly = false;
  mcuDataExpiringOnly = false;
  syncMcuDataToggles();
}

function syncResetIndicator() {
  const btn = document.getElementById("mcu-data-reset");
  if (!btn) return;
  const on = mcuDataFiltersActive();
  btn.classList.toggle("is-filtered", on);
  btn.setAttribute("aria-pressed", on ? "true" : "false");
}

// Expands the patient summary under an Employee-tab row. Every Employee row
// takes this path, whether or not the patient has a checkup yet.
async function openEmployeePatient(patientId, triggerRow) {
  const res = await fetch(`/api/patients`, { credentials: "same-origin" });
  if (!res.ok) return;
  const patients = await res.json();
  const p = patients.find((x) => x.patientId === patientId);
  if (!p) return;
  showWorkerDetailForPatient(p, triggerRow, "[data-patient-id]");
}

async function openMcuDataRecord(mcuRecordId, kind) {
  const attr = kind === "name" ? "data-patient-mcu-id" : "data-mcu-id";
  const row = document.querySelector(`#mcu-data-body [${attr}="${mcuRecordId}"]`)?.closest("tr");
  if (!row) return;
  if (kind === "name") await showWorkerDetailInline(mcuRecordId, row);
  else await showMcuDetailInline(mcuRecordId, row);
  expandedTriggerRow?.scrollIntoView({ block: "nearest" });
}

// Whole clinic days from today to a YYYY-MM-DD date. Invalid or non-dates
// return null so the expiring filter can ignore them.
function daysUntilDate(iso) {
  return clinicDaysBetween(clinicToday(), iso);
}

function isCurrentCertificateExpiring(row) {
  const hc = String(row.hcValidityStatus || "");
  const status = String(row.checkUpStatus || "");
  if (hc === "Archived / Exit" || hc.startsWith("Renewed by ")) return false;
  if (status === "Archived / Exit") return false;
  const days = daysUntilDate(row.resultValidUntil);
  return days !== null && days >= 0 && days <= 60;
}

async function loadMcuDataTable() {
  syncResetIndicator();
  const params = new URLSearchParams();
  if (mcuListLatest) params.set("latestOnly", "1");
  const name = document.getElementById("mcu-data-name").value.trim();
  const companyId = document.getElementById("mcu-data-company").value;
  if (name) params.set("name", name);
  if (companyId) params.set("companyId", companyId);
  const qs = params.toString();
  const res = await fetch(`/api/mcu-records/browse${qs ? `?${qs}` : ""}`, { credentials: "same-origin" });
  if (!res.ok) return;
  mcuDataRows = await res.json();
  renderMcuDataTable();
}

function companyListCell(abbreviation, name) {
  const full = String(name || "").trim();
  const abbr = String(abbreviation || "").trim();
  if (!full && !abbr) return "—";
  if (!abbr) return escapeHtml(full);
  const shown = full || abbr;
  return `<span class="co-abbr">${escapeHtml(abbr)}</span> <span class="co-full">(${escapeHtml(shown)})</span>`;
}

const MED_STATUS_LABELS = {
  Fit: "Fit",
  FitWithNote: "Fit with note",
  IncompleteResult: "Incomplete",
  NeedFollowUp: "Need follow up",
  TemporaryUnfit: "Temporarily unfit",
  Unfit: "Unfit",
};

function medStatusLabel(value) {
  const raw = String(value || "").trim();
  if (!raw) return "—";
  return MED_STATUS_LABELS[raw] || raw;
}

// Employee-tab row, one per patient. The name always expands the patient
// (data-patient-id); the latest checkup ID expands that checkup. A patient with
// no checkup yet has mcuRecordId 0, so the checkup cells stay blank.
function employeeRowHtml(r) {
  const standing = String(r.hcValidityStatus || "").trim() || "—";
  if (!r.mcuRecordId) {
    return `
      <tr>
        <td data-label="Name"><a href="#" data-patient-id="${r.patientId}">${escapeHtml(r.patientName)}</a></td>
        <td data-label="Company">${companyListCell(r.companyAbbreviation, r.companyName)}</td>
        <td data-label="Work position">${escapeHtml(r.workPosition || "—")}</td>
        <td data-label="Latest medical ID">—</td>
        <td data-label="Latest med status">—</td>
        <td data-label="Medical validity status">—</td>
      </tr>`;
  }
  return `
      <tr>
        <td data-label="Name"><a href="#" data-patient-id="${r.patientId}">${escapeHtml(r.patientName)}</a></td>
        <td data-label="Company">${companyListCell(r.companyAbbreviation, r.companyName)}</td>
        <td data-label="Work position">${escapeHtml(r.workPosition || "—")}</td>
        <td data-label="Latest medical ID"><a href="#" data-mcu-id="${r.mcuRecordId}">${escapeHtml(r.checkUpId)}</a></td>
        <td data-label="Latest med status">${escapeHtml(medStatusLabel(r.resultCriteria))}</td>
        <td data-label="Medical validity status">${escapeHtml(standing)}</td>
      </tr>`;
}

function mcuDataRowHtml(r) {
  return `
      <tr>
        <td data-label="Checkup ID"><a href="#" data-mcu-id="${r.mcuRecordId}">${escapeHtml(r.checkUpId)}</a></td>
        <td data-label="Name"><a href="#" data-patient-mcu-id="${r.mcuRecordId}">${escapeHtml(r.patientName)}</a></td>
        <td data-label="Company">${companyListCell(r.companyAbbreviation, r.companyName)}</td>
        <td data-label="Criteria">${escapeHtml(r.criteria || "—")}</td>
        <td data-label="Result">${escapeHtml(r.resultCriteria || "—")}</td>
        <td data-label="Check up date">${escapeHtml(r.dateCheckUp || "—")}</td>
        <td data-label="Valid until">${escapeHtml(r.resultValidUntil || "—")}</td>
        <td data-label="Certificate">${
          isCertificateEligible(r.checkUpStatus, r.hcValidityStatus, r.resultCriteria)
            ? `<a class="${r.resultCriteria === "TemporaryUnfit" || r.resultCriteria === "Unfit" ? "cert-open-adverse" : ""}" href="${escapeHtml(certificateOpenHref(r.mcuRecordId, r.checkUpId, r.companyAbbreviation, r.patientName, r.resultValidUntil))}" target="_blank" rel="noopener">View</a>`
            : "—"
        }</td>
      </tr>`;
}

function renderMcuDataTable() {
  const { key, dir } = currentListSort();
  const today = clinicToday();
  const narrowIdle = mcuDataNarrowList() && !mcuDataFiltersActive();
  let visible = narrowIdle ? [] : mcuDataRows;
  if (!mcuListLatest && mcuDataTodayOnly) visible = visible.filter((row) => row.reviewDate === today);
  if (mcuDataExpiringOnly) visible = visible.filter(isCurrentCertificateExpiring);
  const sorted = [...visible].sort((a, b) => {
    const av = a[key] ?? "";
    const bv = b[key] ?? "";
    const cmp = String(av).localeCompare(String(bv), undefined, { numeric: true });
    if (cmp !== 0) return dir === "asc" ? cmp : -cmp;
    return (b.mcuRecordId || 0) - (a.mcuRecordId || 0);
  });
  const body = document.getElementById("mcu-data-body");
  if (expandedDetailRow && body.contains(expandedDetailRow)) collapseDetailRow();
  const rowHtml = mcuListLatest ? employeeRowHtml : mcuDataRowHtml;
  mcuDataShownRows = sorted;
  body.innerHTML = sorted.map(rowHtml).join("");
  body.querySelectorAll("[data-mcu-id]").forEach((a) => {
    a.addEventListener("click", (e) => {
      e.preventDefault();
      openMcuDataRecord(Number(a.dataset.mcuId), "checkup");
    });
  });
  body.querySelectorAll("[data-patient-mcu-id]").forEach((a) => {
    a.addEventListener("click", (e) => {
      e.preventDefault();
      openMcuDataRecord(Number(a.dataset.patientMcuId), "name");
    });
  });
  body.querySelectorAll("[data-patient-id]").forEach((a) => {
    a.addEventListener("click", async (e) => {
      e.preventDefault();
      await openEmployeePatient(Number(a.dataset.patientId), a.closest("tr"));
    });
  });
  const empty = document.getElementById("mcu-data-empty");
  const nameQuery = document.getElementById("mcu-data-name")?.value.trim() || "";
  const companyQuery = document.getElementById("mcu-data-company")?.value || "";
  empty.hidden = !narrowIdle && sorted.length > 0;
  // Offer a new patient once the name has 4+ characters, or whenever a search
  // leaves 4 rows or fewer.
  const searching = !!(nameQuery || companyQuery);
  setNewPatientOffer(!narrowIdle && searching && (nameQuery.length >= 4 || sorted.length <= 4));
  if (narrowIdle) empty.textContent = mcuListLatest
    ? "Search a name or choose a filter to see each employee’s latest checkup."
    : "Search a name or choose a filter to see records.";
  else if (mcuDataExpiringOnly) empty.textContent = "No current certificates expire within 60 days.";
  else if (!mcuListLatest && mcuDataTodayOnly) empty.textContent = "No reviews dated today.";
  else empty.textContent = mcuListLatest ? "No employees match this search." : "No records match this search.";
  updateMcuDataSortIndicators();
  fitMcuListToFooter();
}

// Phone width: the list's bottom edge must stop at the footer's top edge.
// Mobile browsers count the address bar in 100vh, so the list can run under
// the footer and hide the last card. Measure the footer and cap the list.
function fitMcuListToFooter() {
  const list = document.getElementById("mcu-data-list");
  const footer = document.querySelector(".app-dev-credit");
  if (!list || !footer) return;
  if (!window.matchMedia("(max-width: 640px)").matches) {
    list.style.maxHeight = "";
    return;
  }
  const footerTop = footer.getBoundingClientRect().top;
  const listTop = list.getBoundingClientRect().top;
  list.style.maxHeight = `${Math.max(0, Math.floor(footerTop - listTop))}px`;
}

window.addEventListener("resize", fitMcuListToFooter);
window.visualViewport?.addEventListener("resize", fitMcuListToFooter);

// Shows ▲/▼ only on the currently-sorted column (same convention the
// reference legacy portal used) and toggles .sort-active (app.css) for the
// accent-color highlight.
function updateMcuDataSortIndicators() {
  const sort = currentListSort();
  const head = document.getElementById(mcuListLatest ? "mcu-employee-head" : "mcu-data-head");
  head?.querySelectorAll("th[data-sort]").forEach((th) => {
    const isActive = th.dataset.sort === sort.key;
    th.classList.toggle("sort-active", isActive);
    th.textContent = th.dataset.label + (isActive ? (sort.dir === "asc" ? " ▲" : " ▼") : "");
  });
}

document.querySelectorAll("#tab-mcu-data th[data-sort]").forEach((th) => {
  th.dataset.label = th.textContent;
  th.style.cursor = "pointer";
  th.addEventListener("click", () => {
    const sort = currentListSort();
    const key = th.dataset.sort;
    setCurrentListSort({ key, dir: sort.key === key && sort.dir === "asc" ? "desc" : "asc" });
    renderMcuDataTable();
  });
});
document.getElementById("mcu-data-name")?.addEventListener("input", debounce(loadMcuDataTable, 300));
// Same floating-list typeahead as settings.js's attachCompanyTypeahead (not
// shared — this app has no shared-JS-module system yet, see
// internal/README.md), plus a hidden companyId field, since every company
// picker on this page (the MCU data filter, and the patient/checkup forms'
// Company fields) needs an id to submit, not just a display name. onChange
// fires after a selection or a manual clear — the MCU data filter uses it to
// re-run the search; the patient/checkup forms pass a no-op. getSource is
// called fresh on every lookup rather than captured once, since companyCache
// is reassigned (not mutated) once loadLookups' fetches land — a captured
// array reference would stay permanently empty. Defaults to companyCache.
function attachCompanyFilterTypeahead(inputId, hiddenId, boxId, onChange, getSource = () => companyCache) {
  const input = document.getElementById(inputId);
  const hidden = document.getElementById(hiddenId);
  const box = document.getElementById(boxId);
  if (!input || !hidden || !box) return;

  // -1 = nothing highlighted (plain typing); ArrowDown/Up move it, Enter/Tab
  // commit whichever item is highlighted (defaulting to the first match if
  // the user hasn't arrowed yet), matching native <select>/combobox keyboard
  // behavior instead of forcing a mouse click to pick a company.
  let highlighted = -1;

  function items() {
    return Array.from(box.querySelectorAll("[data-company-id]"));
  }

  function highlight(index) {
    const els = items();
    if (els.length === 0) {
      highlighted = -1;
      return;
    }
    highlighted = (index + els.length) % els.length;
    els.forEach((el, i) => el.classList.toggle("active", i === highlighted));
    els[highlighted].scrollIntoView({ block: "nearest" });
  }

  function commit(index) {
    const els = items();
    if (els.length === 0) return false;
    const btn = els[index >= 0 ? index : 0];
    const c = getSource().find((c) => c.companyId === Number(btn.dataset.companyId));
    if (c) {
      input.value = c.name;
      hidden.value = c.companyId;
    }
    box.style.display = "none";
    highlighted = -1;
    onChange();
    return true;
  }

  function render(query) {
    const q = query.trim().toLowerCase();
    const matches = q ? getSource().filter((c) => c.name.toLowerCase().includes(q)) : getSource();
    highlighted = -1;
    if (matches.length === 0) {
      box.style.display = "none";
      box.innerHTML = "";
      return;
    }
    box.innerHTML = matches
      .slice(0, 20)
      .map((c) => `<button type="button" class="app-program-item" data-company-id="${c.companyId}">${escapeHtml(c.name)}</button>`)
      .join("");
    box.style.display = "block";
    box.querySelectorAll("[data-company-id]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const c = getSource().find((c) => c.companyId === Number(btn.dataset.companyId));
        if (c) {
          input.value = c.name;
          hidden.value = c.companyId;
        }
        box.style.display = "none";
        onChange();
      });
    });
  }

  input.addEventListener("input", () => {
    if (input.readOnly) return;
    render(input.value);
    if (!input.value.trim()) {
      hidden.value = "";
      onChange();
    }
  });
  input.addEventListener("focus", () => {
    if (input.readOnly) {
      box.style.display = "none";
      return;
    }
    render(input.value);
  });
  input.addEventListener("keydown", (e) => {
    if (box.style.display === "none" || !box.innerHTML) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      highlight(highlighted + 1);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      highlight(highlighted - 1);
    } else if (e.key === "Enter") {
      e.preventDefault();
      commit(highlighted);
    } else if (e.key === "Tab") {
      // Don't preventDefault — let focus move on to the next field as normal,
      // just commit whatever's highlighted (or the top match) first.
      commit(highlighted);
    } else if (e.key === "Escape") {
      box.style.display = "none";
      highlighted = -1;
    }
  });
  // Only companies from the list can be kept. Leaving the field with text that
  // is not a listed name restores the last chosen company, or clears the field
  // when none was chosen. An exact (case-insensitive) match is accepted.
  input.addEventListener("blur", () => {
    if (input.readOnly) return;
    const typed = input.value.trim().toLowerCase();
    const exact = typed ? getSource().find((c) => c.name.trim().toLowerCase() === typed) : null;
    if (exact) {
      if (String(exact.companyId) !== hidden.value) {
        hidden.value = exact.companyId;
        input.value = exact.name;
        onChange();
      } else {
        input.value = exact.name;
      }
      return;
    }
    const chosen = getSource().find((c) => c.companyId === Number(hidden.value));
    input.value = chosen ? chosen.name : "";
  });
  document.addEventListener("click", (e) => {
    if (!box.contains(e.target) && e.target !== input) box.style.display = "none";
  });
}
attachCompanyFilterTypeahead("mcu-data-company-input", "mcu-data-company", "mcu-data-company-typeahead", loadMcuDataTable);

const certCountChoices = Array.from({ length: 20 }, (_, i) => String(i + 1));

function attachCertCountPicker() {
  const input = document.getElementById("mcu-cert-count");
  const box = document.getElementById("mcu-cert-count-list");
  if (!input || !box) return;

  let highlighted = -1;

  function items() {
    return Array.from(box.querySelectorAll("[data-cert-count]"));
  }

  function highlight(index) {
    const els = items();
    if (els.length === 0) {
      highlighted = -1;
      return;
    }
    highlighted = (index + els.length) % els.length;
    els.forEach((el, i) => el.classList.toggle("active", i === highlighted));
    els[highlighted].scrollIntoView({ block: "nearest" });
  }

  function commit(index) {
    const els = items();
    if (els.length === 0) return false;
    const btn = els[index >= 0 ? index : 0];
    input.value = btn.dataset.certCount;
    box.style.display = "none";
    highlighted = -1;
    return true;
  }

  function render() {
    const q = input.value.trim();
    const matches = certCountChoices.filter((n) => n.startsWith(q));
    highlighted = -1;
    if (matches.length === 0) {
      box.style.display = "none";
      box.innerHTML = "";
      return;
    }
    box.innerHTML = matches
      .map((n) => `<button type="button" class="app-program-item" data-cert-count="${n}">${n}</button>`)
      .join("");
    box.style.display = "block";
    box.querySelectorAll("[data-cert-count]").forEach((btn, i) => {
      btn.addEventListener("click", () => commit(i));
    });
  }

  input.addEventListener("focus", () => {
    // The same click that focuses this field also bubbles to document and
    // would hide the list. Open it after that click has finished.
    setTimeout(render, 0);
  });
  input.addEventListener("input", () => {
    input.value = input.value.replace(/\D/g, "").slice(0, 2);
    render();
  });
  input.addEventListener("keydown", (e) => {
    if (box.style.display === "none") return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      highlight(highlighted + 1);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      highlight(highlighted - 1);
    } else if (e.key === "Enter") {
      e.preventDefault();
      commit(highlighted);
    } else if (e.key === "Escape") {
      box.style.display = "none";
      highlighted = -1;
    }
  });
  document.addEventListener("click", (e) => {
    if (e.target === input || box.contains(e.target) || input.parentElement?.contains(e.target)) return;
    box.style.display = "none";
  });
}

attachCertCountPicker();

document.getElementById("mcu-cert-clear")?.addEventListener("click", async () => {
  const button = document.getElementById("mcu-cert-clear");
  const notice = document.getElementById("mcu-cert-clear-notice");
  const full = button.querySelector(".mcu-label-full");
  const short = button.querySelector(".mcu-label-short");
  if (!button.classList.contains("is-confirm")) {
    button.classList.add("is-confirm");
    if (full) full.textContent = "Click again to clear";
    if (short) short.textContent = "Confirm";
    if (notice) notice.hidden = false;
    return;
  }
  button.disabled = true;
  const res = await fetch("/api/mcu-records/clear-certificates", { method: "POST", credentials: "same-origin" });
  const data = await res.json().catch(() => ({}));
  button.disabled = false;
  button.classList.remove("is-confirm");
  if (full) full.textContent = "Clear cached certificates";
  if (short) short.textContent = "Clear cache";
  if (!res.ok) {
    if (notice) {
      notice.hidden = false;
      notice.textContent = data.error || "Could not clear saved certificates.";
    }
    return;
  }
  const n = Number(data.cleared) || 0;
  if (notice) {
    notice.hidden = false;
    notice.textContent = n === 1 ? "Cleared 1 saved certificate." : `Cleared ${n} saved certificates.`;
  }
});

// Downloads the Excel sheet for the given checkup ids (in that order) and
// names it <prefix>-ddmmyy.xlsx using the clinic date.
async function downloadMcuRowsXlsx(ids, prefix, btn) {
  btn.disabled = true;
  try {
    const res = await fetch(`/api/mcu-records/mcu-data.xlsx?ids=${ids.join(",")}`, { credentials: "same-origin" });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      alert(data.error || "Could not export to Excel.");
      return;
    }
    const [yyyy, mm, dd] = clinicToday().split("-");
    const url = URL.createObjectURL(await res.blob());
    const a = document.createElement("a");
    a.href = url;
    a.download = `${prefix}-${dd}${mm}${yyyy.slice(2)}.xlsx`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  } finally {
    btn.disabled = false;
  }
}

// "Export shown to Excel": exactly the rows on screen, in the order shown
// (name, company, Today and Expiring filters included). Named mcu-expiring-ddmmyy
// while the Expiring filter is on, mcu-shown-ddmmyy otherwise. The ids travel in
// the URL, so a very long list is refused instead of failing at the proxy.
const MAX_SHOWN_EXPORT_ROWS = 1000;
document.getElementById("mcu-data-shown-export")?.addEventListener("click", (e) => {
  const ids = mcuDataShownRows.map((r) => r.mcuRecordId).filter(Boolean);
  if (!ids.length) {
    alert("No rows shown to export.");
    return;
  }
  if (ids.length > MAX_SHOWN_EXPORT_ROWS) {
    alert(`${ids.length} rows are shown. Narrow the search below ${MAX_SHOWN_EXPORT_ROWS}, or use "Export all data to Excel".`);
    return;
  }
  downloadMcuRowsXlsx(ids, mcuDataExpiringOnly ? "mcu-expiring" : "mcu-shown", e.currentTarget);
});

// "Export latest" takes the top N rows of the table as shown (current sort and
// filters), so the sheet matches the screen. With nothing on screen (phone,
// no search yet) it falls back to the server's N newest checkups.
document.getElementById("mcu-data-export-latest")?.addEventListener("click", (e) => {
  e.preventDefault();
  const raw = document.getElementById("mcu-data-limit")?.value.trim() ?? "";
  if (!/^[1-9]\d*$/.test(raw)) {
    alert("Enter how many of the latest rows to export (a whole number of 1 or more).");
    return;
  }
  const ids = mcuDataShownRows.map((r) => r.mcuRecordId).filter(Boolean).slice(0, Number(raw));
  if (!ids.length) {
    window.location.href = `/api/mcu-records/mcu-data.xlsx?limit=${raw}`;
    return;
  }
  downloadMcuRowsXlsx(ids, "mcu-latest", e.currentTarget);
});

document.getElementById("mcu-cert-download")?.addEventListener("click", async () => {
  const raw = document.getElementById("mcu-cert-count")?.value.trim();
  const n = Number(raw);
  if (!certCountChoices.includes(raw) || !Number.isInteger(n)) {
    alert("Pick a number from 1 to 20.");
    return;
  }
  const res = await fetch(`/api/mcu-records/latest-certificates.zip?n=${n}`, { credentials: "same-origin" });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    alert(data.error || "Could not download certificates.");
    return;
  }
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  const match = /filename="([^"]+)"/.exec(res.headers.get("Content-Disposition") || "");
  a.href = url;
  a.download = match ? match[1] : `mcu-latest-eligible-certificates-${n}.zip`;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
});
attachCompanyFilterTypeahead("checkup-company-input", "checkup-company-select", "checkup-company-typeahead", () => {
  if (document.getElementById("checkup-company-input")?.readOnly) return;
  visitCompanyLinked = false;
  const box = document.getElementById("visit-company-same");
  if (box) box.checked = false;
  updateCheckupCompanyRelationField();
});

function syncMcuDataToggles() {
  const todayBtn = document.getElementById("mcu-data-today");
  const expiringBtn = document.getElementById("mcu-data-expiring");
  if (todayBtn) {
    todayBtn.classList.toggle("is-on", mcuDataTodayOnly);
    todayBtn.setAttribute("aria-pressed", mcuDataTodayOnly ? "true" : "false");
  }
  if (expiringBtn) {
    expiringBtn.classList.toggle("is-on", mcuDataExpiringOnly);
    expiringBtn.setAttribute("aria-pressed", mcuDataExpiringOnly ? "true" : "false");
  }
  syncResetIndicator();
}

function setMcuDataTodayOnly(on) {
  mcuDataTodayOnly = on;
  if (on) mcuDataExpiringOnly = false;
  syncMcuDataToggles();
  renderMcuDataTable();
}

function setMcuDataExpiringOnly(on) {
  mcuDataExpiringOnly = on;
  if (on) mcuDataTodayOnly = false;
  syncMcuDataToggles();
  renderMcuDataTable();
}

document.getElementById("mcu-data-today")?.addEventListener("click", () => {
  setMcuDataTodayOnly(!mcuDataTodayOnly);
});
document.getElementById("mcu-data-expiring")?.addEventListener("click", () => {
  setMcuDataExpiringOnly(!mcuDataExpiringOnly);
});

window.matchMedia("(max-width: 640px)").addEventListener("change", () => {
  if (document.getElementById("mcu-data-body")) renderMcuDataTable();
});

// The phone list is sized to the space above the footer (app.css). The footer
// is as tall as its text wraps, so measure it rather than assume a share of
// the viewport. Always tracked, unlike --mcu-footer-h which needs the form bar.
function trackAppFooterHeight() {
  const footer = document.querySelector(".app-dev-credit");
  if (!footer) return;
  const apply = () => {
    const h = Math.ceil(footer.getBoundingClientRect().height);
    document.documentElement.style.setProperty("--app-footer-h", `${h}px`);
  };
  apply();
  new ResizeObserver(apply).observe(footer);
  window.addEventListener("resize", apply);
}
trackAppFooterHeight();

document.getElementById("mcu-data-reset")?.addEventListener("click", () => {
  document.getElementById("mcu-data-name").value = "";
  document.getElementById("mcu-data-company-input").value = "";
  document.getElementById("mcu-data-company").value = "";
  mcuDataSort = { key: "reviewDate", dir: "desc" };
  mcuDataTodayOnly = false;
  mcuDataExpiringOnly = false;
  syncMcuDataToggles();
  loadMcuDataTable();
});

refreshPresence = startPresence(() => mcuSection);

loadLookups().then(openSubmissionFromLink);
showMcuTab("employee");

// --- Opened from the Submitted page (/mcu?submission=ID) --------------------------------------
// The Submitted page already asked "continue to review?" and did not change the document. Here the
// checkup form opens filled in from what the submitter typed; saving it links the document (Reviewed when the checkup has a result).
let linkedSubmission = null;
// Set once the document is closed (or the link failed after the save): the form then returns to the
// Submitted tab instead of the Employee list.
let returnToSubmitted = false;

function submissionBanner(text, kind, withPdf) {
  const box = document.getElementById("submission-banner");
  if (!box) return;
  box.hidden = false;
  box.className = "submission-banner" + (kind ? ` ${kind}` : "");
  const msg = document.getElementById("submission-banner-text");
  msg.textContent = text;
  const pdf = document.getElementById("submission-banner-pdf");
  pdf.hidden = !withPdf;
  if (withPdf && linkedSubmission) pdf.href = `/api/mcu-submissions/${linkedSubmission.id}/file`;
}

function sameDay(a, b) {
  return !!a && !!b && a === b;
}

// The submitter never chooses a Patient, so a new employee may already be on file under their NIK
// or name and birth date. Ask before reusing them: two people can share a name and a birth date.
async function findPatientForSubmission(sub) {
  const res = await fetch("/api/patients", { credentials: "same-origin" });
  if (!res.ok) return null;
  const patients = await res.json();
  if (sub.patientId) return patients.find((p) => p.patientId === sub.patientId) || null;
  const nik = (sub.personalId || "").trim();
  const name = (sub.worker || "").trim().toLowerCase();
  const match =
    (nik && patients.find((p) => (p.personalId || "").trim() === nik)) ||
    patients.find((p) => (p.name || "").trim().toLowerCase() === name && sameDay(p.dateOfBirth, sub.dob));
  if (!match) return null;
  const ok = confirm(
    `${match.name} (born ${match.dateOfBirth || "unknown"}) is already on file and looks like this worker.\n\n` +
      "OK = add the checkup to that employee.\nCancel = create a new employee.",
  );
  return ok ? match : null;
}

function prefillNewEmployee(sub) {
  patientForm.querySelector('[name="name"]').value = sub.worker || "";
  patientForm.querySelector('[name="dateOfBirth"]').value = sub.dob || "";
  syncDateDisplays(patientForm);
  const nat = nationalityCache.find((n) => n.name.trim().toLowerCase() === (sub.nationality || "").trim().toLowerCase());
  if (nat) patientForm.querySelector('[name="nationalityId"]').value = nat.nationalityId;
  if (sub.gender) patientForm.querySelector('[name="gender"]').value = sub.gender;
  updatePersonalIdFormat();
  patientForm.querySelector('[name="personalId"]').value = sub.personalId || "";
}

// Only fills what is still empty: an employee on file keeps their own company and position.
function prefillVisitFromSubmission(sub) {
  const position = checkupForm.querySelector('[name="workPosition"]');
  if (position && !position.value && sub.position) position.value = sub.position;
  if (sub.companyId && !document.getElementById("checkup-company-select").value) {
    setCompanyField("checkup-company-select", "checkup-company-input", sub.companyId);
    updateCheckupCompanyRelationField();
  }
}

// Deep links: /mcu?view=submitted opens the Submitted tab, /mcu?submission=ID opens that review.
function openSubmissionFromLink() {
  const params = new URLSearchParams(location.search);
  if (params.get("view") === "submitted") showMcuTab("submitted");
  const raw = params.get("submission");
  const id = Number(raw);
  if (raw && Number.isInteger(id) && id > 0) openSubmission(id);
}

function highlightSubmittedTab() {
  document.querySelectorAll("#mcu-tabs .app-tab").forEach((btn) => {
    btn.classList.toggle("active", btn.dataset.tab === "submitted");
  });
}

// Opens the checkup form for one submitted document, in place: the Employee view with the form
// filled in from what the submitter typed. Called by the Submitted tab's Review button.
async function openSubmission(id) {
  showMcuTab("employee");
  highlightSubmittedTab(); // the review belongs to Submitted, even though the form is the Employee view
  const res = await fetch(`/api/mcu-submissions/${id}`, { credentials: "same-origin" });
  if (!res.ok) {
    showMcuTab("submitted");
    submissionBanner("That submitted document could not be opened.", "err", false);
    return;
  }
  const sub = await res.json();
  if (sub.status !== "Pending" && sub.status !== "Reviewed") {
    showMcuTab("submitted");
    submissionBanner(`This document is ${sub.status.toLowerCase()} and cannot be reviewed again.`, "err", false);
    return;
  }
  const patient = await findPatientForSubmission(sub);
  if (!patient && sub.patientId) {
    // The submitter picked an existing worker. Opening a blank form here would create a second
    // patient for the same person, and the close would then be refused as another patient's record.
    showMcuTab("submitted");
    submissionBanner(
      `${sub.worker} was picked from the patient list but is not in it now (archived or removed). Nothing was opened; check the Employee list first.`,
      "err",
      false,
    );
    return;
  }
  linkedSubmission = sub;
  // A checkup is already linked (saved earlier without a result): open THAT checkup to finish it,
  // never a second new one for the same worker.
  const linkedRec = sub.recordId && patient ? await findLinkedRecord(patient.patientId, sub.recordId) : null;
  if (sub.recordId && !linkedRec) {
    linkedSubmission = null;
    showMcuTab("submitted");
    submissionBanner("The checkup linked to this document could not be found. Use More, then Unlink, to start again.", "err", false);
    return;
  }
  submissionBanner(
    linkedRec
      ? `Finishing the checkup of ${sub.worker} (sent by ${sub.submitter}). Save it with a result and the document becomes Reviewed.`
      : `Reviewing ${sub.worker} (sent by ${sub.submitter}). Fill in the checkup and save it with a result: the document then becomes Reviewed.`,
    "",
    sub.hasFile,
  );
  if (linkedRec) {
    currentPatient = patient;
    patientDetail.hidden = true;
    checkupReturnTo = "table";
    collapseDetailRow();
    await loadHistory(patient.patientId);
    await openCheckupForm(linkedRec);
    setDataEntryListVisible(false);
    return;
  }
  if (patient) {
    await newCheckupForPatientInline(patient);
  } else {
    openBlankPatientCheckup();
    prefillNewEmployee(sub);
  }
  prefillVisitFromSubmission(sub);
}

// The checkup record with this id among the worker's own records, or null.
async function findLinkedRecord(patientId, recordId) {
  const res = await fetch(`/api/mcu-records?patientId=${patientId}`, { credentials: "same-origin" });
  if (!res.ok) return null;
  const records = await res.json();
  return (Array.isArray(records) ? records : []).find((r) => r.mcuRecordId === recordId) || null;
}

// Called after the checkup is saved. Two things must hold before the document is linked: the
// record was saved under the worker the submitter picked (the server checks new employees by date
// of birth or ID number), and the request reaches the server. When either fails the checkup stays
// saved and the banner says how to close the document by hand.
function unlinked(sub, checkUpId, why) {
  submissionBanner(
    `The checkup was saved as ${checkUpId}, but it was not linked to the submitted document: ${why} ` +
      "Link it on the Submitted page: open More and pick it from the list.",
    "err",
    false,
  );
}

async function closeLinkedSubmission(checkUpId, savedPatientId, hasResult) {
  const sub = linkedSubmission;
  linkedSubmission = null;
  returnToSubmitted = true;
  if (sub.patientId && savedPatientId && sub.patientId !== savedPatientId) {
    unlinked(sub, checkUpId, `it belongs to a different patient than ${sub.worker}.`);
    return;
  }
  let res;
  try {
    res = await fetch(`/api/mcu-submissions/${sub.id}/link`, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ checkUpId }),
    });
  } catch (err) {
    unlinked(sub, checkUpId, "the connection to the server failed.");
    return;
  }
  if (res.ok) {
    // Reviewed needs a result: without one the document stays Pending and the checkup must be finished.
    submissionBanner(
      hasResult
        ? `Saved. ${sub.worker}'s submitted document is Reviewed and linked to ${checkUpId}. The submitter is told automatically in the next 12-hourly mail.`
        : `Saved and linked to ${checkUpId}, but the checkup has no result yet, so the document stays Pending. Open it again from the Submitted tab to add the result.`,
      "ok",
      false,
    );
    return;
  }
  const why = (await res.text().catch(() => "")).trim() || `error ${res.status}`;
  unlinked(sub, checkUpId, why);
}

// Forgets the document being reviewed when the form is closed without saving, so the next checkup
// saved for anybody else is never linked to it.
// Returns true when a review was open, so the caller can send the user back to the Submitted tab.
function clearLinkedSubmission() {
  if (!linkedSubmission) return false;
  linkedSubmission = null;
  const box = document.getElementById("submission-banner");
  if (box) box.hidden = true;
  return true;
}
