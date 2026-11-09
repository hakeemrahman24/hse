const ROLE = window.__USER_ROLE__ || "staff";
const DEFAULT_SECTION = ROLE === "admin" ? "staff" : "programs";

async function logout() {
  await fetch("/api/logout", { method: "POST", credentials: "same-origin" });
  location.href = "/";
}
document.getElementById("logout")?.addEventListener("click", logout);
document.getElementById("mobile-logout")?.addEventListener("click", (e) => {
  e.preventDefault();
  logout();
});

guardCase(document.querySelector('#add-staff [name="staffName"]'), toProperCase);
// Titles stay as typed. Proper case would turn "dr" into "Dr" and "MKKK" into "Mkkk".
for (const name of ["titleBefore", "titleAfter"]) {
  const el = document.querySelector(`#add-staff [name="${name}"]`);
  el?.addEventListener("input", () => {
    const next = el.value.replace(/^\s+/, "").replace(/\s{2,}/g, " ");
    if (next !== el.value) el.value = next;
  });
  el?.addEventListener("blur", () => {
    el.value = el.value.trim();
    previewStaffName();
  });
}
document.querySelector('#add-staff [name="staffName"]')?.addEventListener("input", previewStaffName);
document.querySelector('#add-staff [name="titleBefore"]')?.addEventListener("input", previewStaffName);
document.querySelector('#add-staff [name="titleAfter"]')?.addEventListener("input", previewStaffName);

function formatStaffName(before, name, after) {
  name = String(name || "").trim().replace(/\s{2,}/g, " ");
  before = String(before || "").trim().replace(/\s{2,}/g, " ");
  after = String(after || "").trim().replace(/\s{2,}/g, " ");
  if (!name) return "";
  if (before && !before.endsWith(".")) before += ".";
  let display = before ? `${before} ${name}` : name;
  if (after) display += `, ${after}`;
  return display;
}

function isSigningPosition(position) {
  const words = String(position || "").trim().toLowerCase().split(/\s+/).filter(Boolean);
  const joined = words.join(" ");
  return joined === "chief medical officer" || joined === "cmo" || words.includes("doctor");
}

function previewStaffName() {
  const form = document.getElementById("add-staff");
  const out = document.getElementById("staff-name-preview");
  if (!form || !out) return;
  const display = formatStaffName(
    form.titleBefore?.value,
    form.staffName?.value,
    form.titleAfter?.value
  );
  out.textContent = display ? `Saved as ${display}` : "";
}
guardCase(document.querySelector('#add-position [name="positionName"]'), toProperCase);
guardCase(document.querySelector('#create-program [name="name"]'), toProperCase);
guardCase(document.querySelector('#add-document [name="documentTitle"]'), toProperCase);
guardCase(document.querySelector('#add-document [name="notes"]'), toSentenceCase);

// Company name: Proper Case ("John Doe" style) with repeated whitespace
// collapsed as you type. Abbreviation: all caps, same whitespace collapse —
// abbreviations like "PT ABC" can have internal spaces, just not doubled-up
// ones.
function toProperCaseCollapsed(str) {
  return toProperCase(str.replace(/^\s+/, "").replace(/\s{2,}/g, " "));
}
function toUpperCaseCollapsed(str) {
  return str.replace(/\s{2,}/g, " ").toUpperCase();
}
// Matches NormalizeCompanyName on the server, word by word: all-lowercase is
// capitalized, all-capitals up to 5 letters (KSO, PT) is kept, longer
// all-capitals (STARWAY) is proper-cased, and mixed case is kept as typed.
const COMPANY_MAX_ACRONYM_LETTERS = 6;
function companyNameCaseCollapsed(str) {
  const s = str.replace(/^\s+/, "").replace(/\s{2,}/g, " ");
  return s.replace(/[A-Za-z]+/g, (word) => {
    const titled = word[0].toUpperCase() + word.slice(1).toLowerCase();
    if (word === word.toLowerCase()) return titled;
    if (word === word.toUpperCase() && word.length > COMPANY_MAX_ACRONYM_LETTERS) return titled;
    return word;
  });
}
guardCase(document.getElementById("add-company-name"), companyNameCaseCollapsed);
guardCase(document.getElementById("add-company-abbreviation"), toUpperCaseCollapsed);
[document.getElementById("add-company-name"), document.getElementById("add-company-abbreviation")].forEach((el) => {
  el?.addEventListener("blur", () => {
    el.value = el.value.trim();
  });
});

// Browser-native validation bubbles ("Please fill out this field", "Please
// match the requested format") render as an OS-styled black popup that
// can't be restyled via CSS. novalidate turns that UI off on every settings
// form; this document-level capture listener runs before each form's own
// submit handler (capture fires on the way down, ahead of the form's
// bubble-phase listener) and blocks submission the same way, but reports
// the problem as an inline message next to the field instead.
document.querySelectorAll("form").forEach((form) => form.setAttribute("novalidate", ""));

document.addEventListener(
  "submit",
  (e) => {
    const form = e.target;
    if (!(form instanceof HTMLFormElement) || form.checkValidity()) return;
    e.preventDefault();
    e.stopPropagation();
    const invalid = form.querySelector(":invalid");
    if (invalid) {
      invalid.focus();
      showFieldError(invalid, invalid.validationMessage);
    }
  },
  true
);

function showFieldError(field, message) {
  clearFieldError(field);
  const msg = document.createElement("div");
  msg.className = "field-error";
  msg.textContent = message;
  field.insertAdjacentElement("afterend", msg);
  field.addEventListener("input", () => clearFieldError(field), { once: true });
}

function clearFieldError(field) {
  const next = field.nextElementSibling;
  if (next && next.classList.contains("field-error")) next.remove();
}

function escapeHtml(s) {
  return String(s)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

const trashIcon = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="square" stroke-linejoin="miter" aria-hidden="true"><path d="M3 6h18"/><path d="M8 6V4h8v2"/><path d="M19 6v14H5V6"/><path d="M10 11v6"/><path d="M14 11v6"/></svg>`;

function trashButton(attrs, label = "Remove") {
  return `<button type="button" class="btn-trash" title="${escapeHtml(label)}" aria-label="${escapeHtml(label)}" ${attrs}>${trashIcon}</button>`;
}

let settingsEditSubmit = null;

function settingsEditField(field) {
  const idAttr = field.id ? ` id="${escapeHtml(field.id)}"` : "";
  if (field.options) {
    const options = field.options
      .map((opt) => {
        const selected = opt.value === (field.value || "") ? " selected" : "";
        return `<option value="${escapeHtml(opt.value)}"${selected}>${escapeHtml(opt.label)}</option>`;
      })
      .join("");
    return `<label><span>${escapeHtml(field.label)}</span><select name="${escapeHtml(field.name)}"${idAttr}>${options}</select></label>`;
  }
  const type = field.type || "text";
  const extra = [
    field.maxLength ? `maxlength="${field.maxLength}"` : "",
    field.inputMode ? `inputmode="${escapeHtml(field.inputMode)}"` : "",
    field.placeholder ? `placeholder="${escapeHtml(field.placeholder)}"` : "",
    field.readOnly ? "readonly" : "",
    field.disabled ? "disabled" : "",
  ]
    .filter(Boolean)
    .join(" ");
  return `<label><span>${escapeHtml(field.label)}</span><input name="${escapeHtml(field.name)}"${idAttr} type="${escapeHtml(type)}" value="${escapeHtml(field.value || "")}" ${extra} /></label>`;
}

function closeSettingsEdit() {
  const form = document.getElementById("settings-edit");
  if (!form) return;
  form.hidden = true;
  settingsEditSubmit = null;
  const err = document.getElementById("settings-edit-error");
  if (err) {
    err.hidden = true;
    err.textContent = "";
  }
}

function openSettingsEdit({ heading, fields, submitLabel, onSubmit, note }) {
  const form = document.getElementById("settings-edit");
  if (!form) return false;
  document.getElementById("settings-edit-heading").textContent = heading;
  const noteHtml = note ? `<p style="color:var(--text-3);font-size:0.8125rem;margin:0 0 8px">${note}</p>` : "";
  document.getElementById("settings-edit-fields").innerHTML = noteHtml + fields.map(settingsEditField).join("");
  const save = document.getElementById("settings-edit-save");
  if (save) save.textContent = submitLabel || "Save";
  const err = document.getElementById("settings-edit-error");
  err.hidden = true;
  err.textContent = "";
  settingsEditSubmit = onSubmit;
  form.hidden = false;
  form.scrollIntoView({ block: "nearest" });
  form.querySelector("input, select")?.focus();
  return true;
}

document.getElementById("settings-edit")?.addEventListener("submit", async (event) => {
  event.preventDefault();
  if (!settingsEditSubmit) return;
  const err = document.getElementById("settings-edit-error");
  const data = Object.fromEntries(new FormData(event.currentTarget));
  try {
    await settingsEditSubmit(data);
    closeSettingsEdit();
  } catch (ex) {
    err.textContent = ex.message || "Failed";
    err.hidden = false;
  }
});

document.getElementById("settings-edit-cancel")?.addEventListener("click", closeSettingsEdit);
window.openSettingsEdit = openSettingsEdit;

function confirmCascadeDelete(title, details) {
  return confirm(
    `Permanently delete “${title}”?\n\n` +
      `This is NOT archive — related data will be cascade-deleted:\n${details}\n\n` +
      `This cannot be undone.`
  );
}

const sectionMeta = {
  staff: {
    title: "Medpost staff",
    hint: "An admin can edit a staff member’s name, position, role, username, and password. A doctor or the Chief Medical Officer can have a signing certificate created or deleted on this server. Changing the position suggests that position’s role, and the role can still be changed. Status Active/NotActive archives; red trash permanently deletes the person, their login, and cascades related data.",
  },
  positions: {
    title: "Positions & roles",
    hint: "Map job titles to app roles. Example: Chief Medical Officer → admin, Site Doctor → staff.",
  },
  programs: {
    title: "Programs",
    hint: "Archive hides a program. The red trash permanently deletes it and cascade-deletes related activities, evidence, documents, and supervisors.",
  },
  supervisors: {
    title: "Supervisors",
    hint: "Assign medpost staff as supervisors for each program (optional date range).",
  },
  documents: {
    title: "Documents",
    hint: "Procedure, Forms, Socialization material and Websites per program, plus medpost-wide Templates. Sorted oldest first within each type.",
  },
  "company-details": {
    title: "Company details",
    hint: "Global company list shared by every module (MCU, EMR, screening) — name, abbreviation, entity type.",
  },
  "company-relations": {
    title: "Company relations",
    hint: "Links a company to a client/parent for multi-tier subcontracting.",
  },
  projects: {
    title: "Projects",
    hint: "Admin-editable project list (LNG, GOKPL, ...) used as the Project dropdown in company relations.",
  },
  nationalities: {
    title: "Nationalities",
    hint: "Admin-editable nationality list used by the medical checkup form's patient demographics.",
  },
  "checkup-criteria": {
    title: "Checkup purpose criteria",
    hint: "Admin-editable checkup purpose list (PreEmployment, Annual, Bi-Annual, Exit, ConsultationResult) used by the medical checkup form.",
  },
  "review-result-criteria": {
    title: "Review result criteria",
    hint: "Admin-editable review result list (Fit, FitWithNote, IncompleteResult, NeedFollowUp, TemporaryUnfit, Unfit) used by the medical checkup form.",
  },
  "work-locations": {
    title: "Work location",
    hint: "Admin-editable work location list (RemoteSite, NonRemote) used by the medical checkup form.",
  },
  "task-categories": {
    title: "Occupational health risks",
    hint: "Optional risks on the medical checkup form. A trigger word in Work position ticks that box. Staff can untick it, and a checkup can be saved with none selected.",
  },
  "employee-api": {
    title: "Employee API",
    hint: "Token other databases send when they search employees and the latest Fit or Fit with note review. Update replaces it. Give the new value to each database server.",
  },
  "sofi-schedule": {
    title: "SOFI schedule",
    hint: "When the public fatigue questionnaire (/sofi) is open. Times are GMT+9. Windows are never deleted: cancelling or closing one keeps it in the list for audit.",
  },
  "sofi-questionnaire": {
    title: "SOFI questionnaire",
    hint: "The 20 items workers rate at /sofi, as they see them. Edit changes the wording of one item, and Rename the name of a dimension, in English, Indonesian or Chinese; which items belong to which dimension and the scoring stay as they are.",
  },
  "mcu-notifications": {
    title: "Email notifications",
    hint: "SMTP account, recipients (To/Cc), and schedule for the near-expiry MCU fitness certificate warning email. Archived/closed patients are never included.",
  },
};

let settingsSection = "Settings";
let refreshPresence = () => {};

function showSection(id) {
  document.querySelectorAll("#settings-nav .app-program-item").forEach((btn) => {
    btn.classList.toggle("active", btn.dataset.section === id);
  });
  document.querySelectorAll(".settings-panel").forEach((panel) => {
    panel.classList.toggle("active", panel.dataset.panel === id);
  });
  const meta = sectionMeta[id] || sectionMeta[DEFAULT_SECTION];
  settingsSection = meta.title;
  refreshPresence();
  document.getElementById("section-title").textContent = meta.title;
  document.getElementById("section-hint").textContent = meta.hint;
  history.replaceState(null, "", `#${id}`);
}

document.getElementById("settings-nav")?.addEventListener("click", (e) => {
  const btn = e.target.closest("[data-section]");
  if (!btn) return;
  showSection(btn.dataset.section);
});

let programsCache = [];
let staffCache = [];
let positionCache = [];
let companyCache = [];
let editingCompanyId = null;
let entityTypeCache = [];

function fillPositionSelects() {
  const staffPos = document.getElementById("staff-position");
  if (staffPos) {
    const keep = staffPos.value;
    staffPos.innerHTML =
      `<option value="">Select position…</option>` +
      positionCache
        .map(
          (p) =>
            `<option value="${escapeHtml(p.positionName)}" data-role="${escapeHtml(p.role)}">${escapeHtml(p.positionName)} → ${escapeHtml(p.role)}</option>`
        )
        .join("");
    staffPos.value = keep;
    syncStaffRoleFromPosition();
  }
}

function syncStaffRoleFromPosition() {
  const sel = document.getElementById("staff-position");
  const roleHidden = document.getElementById("staff-role");
  const roleDisplay = document.getElementById("staff-role-display");
  if (!sel || !roleHidden || !roleDisplay) return;
  const opt = sel.selectedOptions[0];
  const role = opt?.dataset?.role || "";
  roleHidden.value = role || "staff";
  roleDisplay.value = role || "";
}

document.getElementById("staff-position")?.addEventListener("change", syncStaffRoleFromPosition);

async function loadPositions() {
  const res = await fetch("/api/position-roles", { credentials: "same-origin" });
  if (!res.ok) return;
  positionCache = await res.json();
  fillPositionSelects();
  const body = document.getElementById("position-body");
  if (!body) return;
  body.innerHTML = "";
  for (const p of positionCache) {
    const tr = document.createElement("tr");
    tr.innerHTML = `
      <td>${escapeHtml(p.positionName)}</td>
      <td>
        <select data-pos-role="${p.positionId}">
          <option value="staff" ${p.role === "staff" ? "selected" : ""}>staff</option>
          <option value="admin" ${p.role === "admin" ? "selected" : ""}>admin</option>
        </select>
      </td>
      <td>${trashButton(`data-del-pos="${p.positionId}"`, "Delete position mapping")}</td>`;
    body.appendChild(tr);
  }
  body.querySelectorAll("[data-pos-role]").forEach((sel) => {
    sel.addEventListener("change", async () => {
      const r = await fetch(`/api/position-roles/${sel.dataset.posRole}`, {
        method: "PUT",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ role: sel.value }),
      });
      if (!r.ok) {
        const d = await r.json().catch(() => ({}));
        alert(d.error || "Failed");
        loadPositions();
        return;
      }
      loadPositions();
    });
  });
  body.querySelectorAll("[data-del-pos]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      if (!confirm("Delete this position mapping?\n\nStaff already using this title keep their position text; only the role mapping is removed.")) return;
      const r = await fetch(`/api/position-roles/${btn.dataset.delPos}`, {
        method: "DELETE",
        credentials: "same-origin",
      });
      if (!r.ok) {
        const d = await r.json().catch(() => ({}));
        alert(d.error || "Failed");
        return;
      }
      loadPositions();
    });
  });
}

document.getElementById("add-position")?.addEventListener("submit", async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const res = await fetch("/api/position-roles", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      positionName: fd.get("positionName"),
      role: fd.get("role"),
    }),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    alert(data.error || "Failed");
    return;
  }
  e.target.reset();
  loadPositions();
});

async function loadEntityTypes() {
  const res = await fetch("/api/entity-types", { credentials: "same-origin" });
  if (!res.ok) return;
  entityTypeCache = await res.json();
  const sel = document.getElementById("company-entity-type");
  if (!sel) return;
  const keep = sel.value;
  sel.innerHTML =
    `<option value="">None</option>` +
    entityTypeCache.map((t) => `<option value="${t.entityTypeId}">${escapeHtml(t.name)}</option>`).join("");
  sel.value = keep;
}

// Client and Government stay on the company list. Every other type, once
// the company itself is saved, opens Company relations with that name
// already in the Company field.
const COMPANY_RELATION_EXEMPT_ENTITY_TYPES = ["Client", "Government"];
// Only Government is hidden from the Parent picker: a Client is a valid direct
// parent (e.g. a Consultant under the project client at depth 1).
const RELATION_PARENT_EXCLUDED_ENTITY_TYPES = ["Government"];

// Company pickers (Add-company's Parent field, and the relation-edit form's
// Company/Parent fields) are plain text inputs paired with a floating
// app-program-item dropdown — searchable/filterable as you type, instead of
// scrolling a 54-company <select>, and styled like the patient-name
// typeahead on the MCU page rather than the browser's native <datalist>
// popup. The input's value is the company NAME (Company.Name is UNIQUE, so
// that's an unambiguous key); resolveCompanyIdByName() below turns it back
// into an id at submit time. excludeEntityTypeNames lets the "Parent
// company" picker hide Government, which is never a valid parent.
function attachCompanyTypeahead(inputId, boxId, excludeEntityTypeNames = []) {
  const input = document.getElementById(inputId);
  const box = document.getElementById(boxId);
  if (!input || !box) return;

  // -1 = nothing highlighted (plain typing); ArrowDown/Up move it, Enter/Tab
  // commit whichever item is highlighted (defaulting to the first match if
  // the user hasn't arrowed yet) — same keyboard behavior as mcu.js's
  // attachCompanyFilterTypeahead.
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
    const c = companyCache.find((c) => c.companyId === Number(btn.dataset.companyId));
    if (c) input.value = c.name;
    box.style.display = "none";
    highlighted = -1;
    return true;
  }

  function render(query) {
    const q = query.trim().toLowerCase();
    let matches = q ? companyCache.filter((c) => c.name.toLowerCase().includes(q)) : companyCache;
    if (excludeEntityTypeNames.length > 0) {
      matches = matches.filter((c) => !excludeEntityTypeNames.includes(c.entityTypeName));
    }
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
        const c = companyCache.find((c) => c.companyId === Number(btn.dataset.companyId));
        if (c) input.value = c.name;
        box.style.display = "none";
      });
    });
  }

  input.addEventListener("input", () => render(input.value));
  input.addEventListener("focus", () => render(input.value));
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
      commit(highlighted);
    } else if (e.key === "Escape") {
      box.style.display = "none";
      highlighted = -1;
    }
  });
  document.addEventListener("click", (e) => {
    if (!box.contains(e.target) && e.target !== input) box.style.display = "none";
  });
}

attachCompanyTypeahead("edit-relation-company", "edit-relation-company-typeahead");
attachCompanyTypeahead("edit-relation-parent", "edit-relation-parent-typeahead", RELATION_PARENT_EXCLUDED_ENTITY_TYPES);

function resolveCompanyIdByName(name) {
  const trimmed = (name || "").trim();
  if (!trimmed) return null;
  const match = companyCache.find((c) => c.name.toLowerCase() === trimmed.toLowerCase());
  return match ? match.companyId : undefined; // undefined = typed text that isn't a real company
}

// Relation code is always derived, never hand-typed — <Abbreviation-or-Name>-<Project>.
function computeRelationCode(abbreviation, name, project) {
  const prefix = (abbreviation || "").trim() || (name || "").trim().replace(/\s+/g, "").toUpperCase().slice(0, 10);
  return project && prefix ? `${prefix}-${project}` : prefix;
}

// Pickers in other forms only get active companies; the settings table below
// is fed the full list so archived companies can still be edited or restored.
async function loadCompanies() {
  const res = await fetch("/api/companies?includeArchived=1", { credentials: "same-origin" });
  if (!res.ok) return;
  const all = await res.json();
  companyCache = all.filter((c) => !c.archived);
  const body = document.getElementById("company-body");
  if (!body) return;
  body.innerHTML = "";
  for (const c of all) {
    const tr = document.createElement("tr");
    const archivedTag = c.archived ? ` <em style="color:var(--text-3)">(archived)</em>` : "";
    tr.innerHTML = `
      <td>${escapeHtml(c.name)}${archivedTag}</td>
      <td>${escapeHtml(c.abbreviation || "—")}</td>
      <td>${escapeHtml(c.entityTypeName || "—")}</td>
      <td class="company-actions">
        <button type="button" class="btn-ghost" data-edit-company="${c.companyId}">Edit</button>
        <button type="button" class="btn-ghost" data-archive-company="${c.companyId}" data-archived="${c.archived ? "1" : "0"}">${c.archived ? "Restore" : "Archive"}</button>
        ${trashButton(`data-del-company="${c.companyId}"`, "Delete company")}
      </td>`;
    body.appendChild(tr);
  }
  body.querySelectorAll("[data-edit-company]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const c = all.find((x) => x.companyId === Number(btn.dataset.editCompany));
      if (c) startEditCompany(c);
    });
  });
  body.querySelectorAll("[data-archive-company]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const archive = btn.dataset.archived !== "1";
      const name = all.find((x) => x.companyId === Number(btn.dataset.archiveCompany))?.name || "this company";
      const question = archive
        ? `Archive ${name}?\n\nIt will be hidden from pickers. Existing records keep their link.`
        : `Restore ${name}?`;
      if (!confirm(question)) return;
      setCompanyArchived(Number(btn.dataset.archiveCompany), archive);
    });
  });
  body.querySelectorAll("[data-del-company]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      if (!confirm("Delete this company?\n\nCompanies still referenced by a relation or a record can't be deleted.")) return;
      const r = await fetch(`/api/companies/${btn.dataset.delCompany}`, {
        method: "DELETE",
        credentials: "same-origin",
      });
      if (!r.ok) {
        const d = await r.json().catch(() => ({}));
        alert(d.error || "Failed");
        return;
      }
      loadCompanies();
    });
  });
}

function startEditCompany(c) {
  editingCompanyId = c.companyId;
  const form = document.getElementById("add-company");
  form.elements.name.value = c.name;
  form.elements.abbreviation.value = c.abbreviation || "";
  form.elements.entityTypeId.value = c.entityTypeId ? String(c.entityTypeId) : "";
  document.getElementById("company-form-label").textContent = `Edit company — ${c.name}`;
  document.getElementById("add-company-submit").textContent = "Save company";
  document.getElementById("cancel-edit-company").style.display = "";
  form.elements.name.focus();
}

function stopEditCompany() {
  editingCompanyId = null;
  document.getElementById("add-company")?.reset();
  document.getElementById("company-form-label").textContent = "Add company";
  document.getElementById("add-company-submit").textContent = "Add company";
  document.getElementById("cancel-edit-company").style.display = "none";
}

async function saveEditedCompany(name, fd) {
  const res = await fetch(`/api/companies/${editingCompanyId}`, {
    method: "PUT",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      name,
      abbreviation: String(fd.get("abbreviation") || "").trim(),
      entityTypeId: fd.get("entityTypeId") ? Number(fd.get("entityTypeId")) : null,
    }),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    alert(data.error || "Failed");
    return;
  }
  stopEditCompany();
  await loadCompanies();
  loadCompanyRelations();
}

async function setCompanyArchived(id, archived) {
  const res = await fetch(`/api/companies/${id}`, {
    method: "PUT",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ archived }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    alert(data.error || "Failed");
    return;
  }
  if (editingCompanyId === id) stopEditCompany();
  await loadCompanies();
}

document.getElementById("cancel-edit-company")?.addEventListener("click", stopEditCompany);

document.getElementById("add-company")?.addEventListener("submit", async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const name = companyNameCaseCollapsed(String(fd.get("name") || "")).trim();
  const nameInput = document.getElementById("add-company-name");
  if (!name) {
    showFieldError(nameInput, "Company name is required.");
    nameInput?.focus();
    return;
  }
  if (editingCompanyId) {
    await saveEditedCompany(name, fd);
    return;
  }
  const entityTypeId = fd.get("entityTypeId");
  const entityType = entityTypeCache.find((t) => t.entityTypeId === Number(entityTypeId));
  const needsRelation = !!entityType && !COMPANY_RELATION_EXEMPT_ENTITY_TYPES.includes(entityType.name);

  const res = await fetch("/api/companies", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      name,
      abbreviation: String(fd.get("abbreviation") || "").trim(),
      entityTypeId: entityTypeId ? Number(entityTypeId) : null,
    }),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    alert(data.error || "Failed");
    return;
  }

  e.target.reset();
  await loadCompanies();
  loadCompanyRelations();
  if (!needsRelation) return;

  showSection("company-relations");
  startAddCompanyRelation();
  const companyInput = document.getElementById("edit-relation-company");
  if (companyInput) companyInput.value = name;
  document.getElementById("edit-relation-label").textContent = `Add company relation — ${name}`;
  document.getElementById("edit-relation-parent")?.focus();
});

// Clicking "Edit" on a relation row pulls that row's data into this
// section's own edit form (hidden until then) rather than a second page or
// editing inline in the list. The same form doubles as "Add relation" (see
// startAddCompanyRelation) since a company can have more than one relation —
// one per project it serves — so the only difference is whether
// companyRelationId is set; the submit handler below branches on that.
function startEditCompanyRelation(cr) {
  const form = document.getElementById("edit-company-relation");
  if (!form) return;
  form.elements.companyRelationId.value = cr.companyRelationId;
  document.getElementById("edit-relation-company").value = cr.companyName;
  document.getElementById("edit-relation-parent").value = cr.parentName || "";
  document.getElementById("edit-relation-project").value = cr.project || "";
  document.getElementById("edit-relation-label").textContent = "Edit company relation";
  document.getElementById("edit-relation-label").style.display = "";
  form.style.display = "";
  form.scrollIntoView({ behavior: "smooth", block: "start" });
}

// "+ Add relation" opens the same form empty (companyRelationId blank) so a
// company already in the system can get a second, third, etc. relation for
// another project without re-creating the company itself.
function startAddCompanyRelation() {
  const form = document.getElementById("edit-company-relation");
  if (!form) return;
  form.reset();
  form.elements.companyRelationId.value = "";
  document.getElementById("edit-relation-label").textContent = "Add company relation";
  document.getElementById("edit-relation-label").style.display = "";
  form.style.display = "";
  document.getElementById("edit-relation-company").focus();
  form.scrollIntoView({ behavior: "smooth", block: "start" });
}
document.getElementById("add-relation-btn")?.addEventListener("click", startAddCompanyRelation);

function cancelEditCompanyRelation() {
  const form = document.getElementById("edit-company-relation");
  if (!form) return;
  form.reset();
  document.getElementById("edit-relation-label").style.display = "none";
  form.style.display = "none";
}
document.getElementById("edit-relation-cancel")?.addEventListener("click", cancelEditCompanyRelation);

document.getElementById("edit-company-relation")?.addEventListener("submit", async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const companyId = resolveCompanyIdByName(fd.get("companyId"));
  const parentId = resolveCompanyIdByName(fd.get("parentId"));
  if (!companyId) {
    alert("Company must be picked from the list — no match for what was typed.");
    return;
  }
  if (parentId === undefined) {
    alert("Parent company must be picked from the list — no match for what was typed.");
    return;
  }
  const project = (fd.get("project") || "").toString().trim();
  // Relation code is derived (company abbreviation/name + project), same
  // formula as the joined Add-company flow — never hand-typed or shown in
  // this form, just recomputed from what's actually being saved.
  const company = companyCache.find((c) => c.companyId === companyId);
  const relationCode = computeRelationCode(company?.abbreviation, company?.name, project);
  const body = JSON.stringify({
    relationCode,
    companyId,
    parentId,
    project,
  });
  const relationId = fd.get("companyRelationId");
  const r = await fetch(relationId ? `/api/company-relations/${relationId}` : "/api/company-relations", {
    method: relationId ? "PUT" : "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body,
  });
  const d = await r.json().catch(() => ({}));
  if (!r.ok) {
    alert(d.error || "Failed");
    return;
  }
  cancelEditCompanyRelation();
  loadCompanyRelations();
});

// Project lookup feeding the two "Project" <select>s (joined Add-company
// relation fields, and the standalone relation form) — same soft-lookup
// shape as the nationality/checkup-criteria/task-category tables below, but
// kept separate from makeSimpleLookup since it also has to refill those two
// selects on every reload, not just the settings-page list.
let projectCache = [];

function fillProjectSelects() {
  for (const id of ["edit-relation-project"]) {
    const el = document.getElementById(id);
    if (!el) continue;
    const keep = el.value;
    el.innerHTML =
      `<option value="">Select project…</option>` +
      projectCache.map((p) => `<option value="${escapeHtml(p.name)}">${escapeHtml(p.name)}</option>`).join("");
    el.value = keep;
  }
}

async function loadProjects() {
  const res = await fetch("/api/projects", { credentials: "same-origin" });
  if (!res.ok) return;
  projectCache = await res.json();
  fillProjectSelects();
  const body = document.getElementById("project-body");
  if (!body) return;
  body.innerHTML = "";
  for (const p of projectCache) {
    const tr = document.createElement("tr");
    tr.innerHTML = `
      <td>${escapeHtml(p.name)}</td>
      <td class="staff-row-actions">
        <button type="button" class="btn-ghost" data-edit-project="${p.projectId}" data-name="${escapeHtml(p.name)}">Edit</button>
        ${trashButton(`data-del-project="${p.projectId}"`, `Delete ${escapeHtml(p.name)}`)}
      </td>`;
    body.appendChild(tr);
  }
  body.querySelectorAll("[data-edit-project]").forEach((btn) => {
    btn.addEventListener("click", () => {
      openSettingsEdit({
        heading: "Edit project",
        fields: [{ label: "Name", name: "name", value: btn.dataset.name }],
        onSubmit: async (data) => {
          const name = (data.name || "").trim();
          if (!name) throw new Error("Name is required");
          const r = await fetch(`/api/projects/${btn.dataset.editProject}`, {
            method: "PUT",
            credentials: "same-origin",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ name }),
          });
          const d = await r.json().catch(() => ({}));
          if (!r.ok) throw new Error(d.error || "Failed");
          loadProjects();
        },
      });
    });
  });
  body.querySelectorAll("[data-del-project]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      if (!confirm("Delete this project?")) return;
      const r = await fetch(`/api/projects/${btn.dataset.delProject}`, {
        method: "DELETE",
        credentials: "same-origin",
      });
      if (!r.ok) {
        const d = await r.json().catch(() => ({}));
        alert(d.error || "Failed");
        return;
      }
      loadProjects();
    });
  });
}

document.getElementById("add-project")?.addEventListener("submit", async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const res = await fetch("/api/projects", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name: fd.get("name") }),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    alert(data.error || "Failed");
    return;
  }
  e.target.reset();
  loadProjects();
});

async function loadCompanyRelations() {
  const res = await fetch("/api/company-relations", { credentials: "same-origin" });
  if (!res.ok) return;
  const relations = await res.json();
  const body = document.getElementById("company-relation-body");
  if (!body) return;
  body.innerHTML = "";
  for (const cr of relations) {
    const tr = document.createElement("tr");
    tr.innerHTML = `
      <td>${escapeHtml(cr.companyName)}</td>
      <td>${escapeHtml(cr.parentName || "—")}</td>
      <td>${escapeHtml(cr.project || "—")}</td>
      <td>${cr.depth ?? "—"}</td>
      <td>
        <button type="button" data-edit-relation="${cr.companyRelationId}" class="btn-ghost">Edit</button>
        ${trashButton(`data-del-relation="${cr.companyRelationId}"`, "Delete relation")}
      </td>`;
    body.appendChild(tr);
  }
  body.querySelectorAll("[data-del-relation]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      if (!confirm("Delete this company relation?")) return;
      const r = await fetch(`/api/company-relations/${btn.dataset.delRelation}`, {
        method: "DELETE",
        credentials: "same-origin",
      });
      if (!r.ok) {
        const d = await r.json().catch(() => ({}));
        alert(d.error || "Failed");
        return;
      }
      loadCompanyRelations();
    });
  });
  body.querySelectorAll("[data-edit-relation]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const cr = relations.find((r) => r.companyRelationId === Number(btn.dataset.editRelation));
      if (cr) startEditCompanyRelation(cr);
    });
  });
}

// Three small name-only lookup tables (Nationality, CheckUpCriteria,
// TaskCategory) share the exact same add/list/delete shape, so one
// generic helper drives all three instead of copy-pasting three near-
// identical load/submit pairs.
function archivedValueHtml(name, archived) {
  if (!archived) return escapeHtml(name);
  return `<span class="lookup-archived">${escapeHtml(name)} (Archived)</span>`;
}

function makeSimpleLookup(config) {
  return async function load() {
    const res = await fetch(config.listUrl, { credentials: "same-origin" });
    if (!res.ok) return;
    const items = await res.json();
    const body = document.getElementById(config.bodyId);
    if (!body) return;
    body.innerHTML = "";
    for (const item of items) {
      const archived = item.status === "Archived";
      const nextStatus = archived ? "Active" : "Archived";
      const tr = document.createElement("tr");
      tr.innerHTML = `
        <td>${archivedValueHtml(item.name, archived)}</td>
        <td class="staff-row-actions">
          <button type="button" class="btn-ghost${archived ? " lookup-archived" : ""}" data-status-id="${item[config.idField]}" data-next-status="${nextStatus}">${archived ? "Restore" : "Archive"}</button>
          <button type="button" class="btn-ghost" data-edit-id="${item[config.idField]}" data-name="${escapeHtml(item.name)}">Edit</button>
          ${trashButton(`data-del-id="${item[config.idField]}"`, `Delete ${escapeHtml(item.name)}`)}
        </td>`;
      body.appendChild(tr);
    }
    body.querySelectorAll("[data-edit-id]").forEach((btn) => {
      btn.addEventListener("click", () => {
        openSettingsEdit({
          heading: config.editHeading,
          fields: [{ label: "Name", name: "name", value: btn.dataset.name }],
          onSubmit: async (data) => {
            const name = (data.name || "").trim();
            if (!name) throw new Error("Name is required");
            const r = await fetch(`${config.listUrl}/${btn.dataset.editId}`, {
              method: "PUT",
              credentials: "same-origin",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ name }),
            });
            const d = await r.json().catch(() => ({}));
            if (!r.ok) throw new Error(d.error || "Failed");
            load();
          },
        });
      });
    });
    body.querySelectorAll("[data-status-id]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const r = await fetch(`${config.listUrl}/${btn.dataset.statusId}/status`, {
          method: "PUT",
          credentials: "same-origin",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ status: btn.dataset.nextStatus }),
        });
        const d = await r.json().catch(() => ({}));
        if (!r.ok) {
          alert(d.error || "Failed");
          return;
        }
        load();
      });
    });
    body.querySelectorAll("[data-del-id]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        if (!confirm("Delete this value?")) return;
        const r = await fetch(`${config.listUrl}/${btn.dataset.delId}`, {
          method: "DELETE",
          credentials: "same-origin",
        });
        if (!r.ok) {
          const d = await r.json().catch(() => ({}));
          alert(d.error || "Failed");
          return;
        }
        load();
      });
    });
  };
}

const loadNationalities = makeSimpleLookup({
  listUrl: "/api/nationalities",
  bodyId: "nationality-body",
  idField: "nationalityId",
  editHeading: "Edit nationality",
});
const loadCheckUpCriteria = makeSimpleLookup({
  listUrl: "/api/checkup-criteria",
  bodyId: "checkup-criteria-body",
  idField: "checkUpCriteriaId",
  editHeading: "Edit checkup purpose",
});
const loadReviewResults = makeSimpleLookup({
  listUrl: "/api/review-result-criteria",
  bodyId: "review-result-body",
  idField: "reviewResultCriteriaId",
  editHeading: "Edit review result",
});
const loadWorkLocations = makeSimpleLookup({
  listUrl: "/api/work-locations",
  bodyId: "work-location-body",
  idField: "workLocationId",
  editHeading: "Edit work location",
});
async function loadTaskCategories() {
  const res = await fetch("/api/task-categories", { credentials: "same-origin" });
  if (!res.ok) return;
  const items = await res.json();
  const body = document.getElementById("task-category-body");
  if (!body) return;
  body.innerHTML = "";
  for (const item of items) {
    const archived = item.status === "Archived";
    const nextStatus = archived ? "Active" : "Archived";
    const tr = document.createElement("tr");
    tr.innerHTML = `
      <td>${archivedValueHtml(item.name, archived)}</td>
      <td>${escapeHtml(item.triggerWords || "")}</td>
      <td class="staff-row-actions">
        <button type="button" class="btn-ghost${archived ? " lookup-archived" : ""}" data-status-id="${item.taskCategoryId}" data-next-status="${nextStatus}">${archived ? "Restore" : "Archive"}</button>
        <button type="button" class="btn-ghost" data-edit-id="${item.taskCategoryId}">Edit</button>
        ${trashButton(`data-del-id="${item.taskCategoryId}"`, `Delete ${escapeHtml(item.name)}`)}
      </td>`;
    body.appendChild(tr);
  }
  body.querySelectorAll("[data-edit-id]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const item = items.find((row) => row.taskCategoryId === Number(btn.dataset.editId));
      if (!item) return;
      openSettingsEdit({
        heading: "Edit occupational health risk",
        fields: [
          { label: "Name", name: "name", value: item.name },
          { label: "Trigger words", name: "triggerWords", value: item.triggerWords || "", placeholder: "food handler, cook" },
        ],
        onSubmit: async (data) => {
          const name = (data.name || "").trim();
          if (!name) throw new Error("Name is required");
          const r = await fetch(`/api/task-categories/${item.taskCategoryId}`, {
            method: "PUT",
            credentials: "same-origin",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ name, triggerWords: data.triggerWords || "" }),
          });
          const d = await r.json().catch(() => ({}));
          if (!r.ok) throw new Error(d.error || "Failed");
          loadTaskCategories();
        },
      });
    });
  });
  body.querySelectorAll("[data-status-id]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const r = await fetch(`/api/task-categories/${btn.dataset.statusId}/status`, {
        method: "PUT",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ status: btn.dataset.nextStatus }),
      });
      const d = await r.json().catch(() => ({}));
      if (!r.ok) {
        alert(d.error || "Failed");
        return;
      }
      loadTaskCategories();
    });
  });
  body.querySelectorAll("[data-del-id]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      if (!confirm("Delete this value?")) return;
      const r = await fetch(`/api/task-categories/${btn.dataset.delId}`, { method: "DELETE", credentials: "same-origin" });
      if (!r.ok) {
        const d = await r.json().catch(() => ({}));
        alert(d.error || "Failed");
        return;
      }
      loadTaskCategories();
    });
  });
}

function bindSimpleLookupForm(formId, postUrl, reload) {
  document.getElementById(formId)?.addEventListener("submit", async (e) => {
    e.preventDefault();
    const fd = new FormData(e.target);
    const res = await fetch(postUrl, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: fd.get("name") }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      alert(data.error || "Failed");
      return;
    }
    e.target.reset();
    reload();
  });
}
bindSimpleLookupForm("add-nationality", "/api/nationalities", loadNationalities);
bindSimpleLookupForm("add-checkup-criteria", "/api/checkup-criteria", loadCheckUpCriteria);
bindSimpleLookupForm("add-review-result", "/api/review-result-criteria", loadReviewResults);
bindSimpleLookupForm("add-work-location", "/api/work-locations", loadWorkLocations);
document.getElementById("add-task-category")?.addEventListener("submit", async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const res = await fetch("/api/task-categories", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name: fd.get("name"), triggerWords: fd.get("triggerWords") || "" }),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    alert(data.error || "Failed");
    return;
  }
  e.target.reset();
  loadTaskCategories();
});

function fillProgramSelects() {
  for (const id of ["sv-program", "doc-program", "doc-filter"]) {
    const el = document.getElementById(id);
    if (!el) continue;
    const keep = el.value;
    const first =
      id === "doc-filter"
        ? `<option value="">All program docs + templates</option>`
        : `<option value="">Select…</option>`;
    el.innerHTML =
      first +
      programsCache
        .filter((p) => p.status === "Active" || id === "doc-filter")
        .map(
          (p) =>
            `<option value="${p.procId}">${escapeHtml(p.title || p.healthProcedure)}</option>`
        )
        .join("");
    if (id === "doc-filter") {
      el.innerHTML += `<option value="common">Templates only</option>`;
    }
    el.value = keep;
  }
}

function fillStaffSelect() {
  const el = document.getElementById("sv-staff");
  if (!el) return;
  const keep = el.value;
  el.innerHTML =
    `<option value="">Select…</option>` +
    staffCache
      .filter((s) => s.status === "Active")
      .map(
        (s) =>
          `<option value="${s.staffId}">${escapeHtml(s.staffName)}${s.position ? " — " + escapeHtml(s.position) : ""}</option>`
      )
      .join("");
  el.value = keep;
}

// Populates the supervisor-assignment staff dropdown. The visible roster
// table is rendered by loadUsers() below, since every staff member now has
// a login and /api/users already returns staff+login joined.
async function loadStaff() {
  const res = await fetch("/api/staff", { credentials: "same-origin" });
  if (!res.ok) return;
  staffCache = await res.json();
  fillStaffSelect();
}

async function loadUsers() {
  const res = await fetch("/api/users", { credentials: "same-origin" });
  if (res.status === 401 || res.status === 403) {
    location.href = "/programs";
    return;
  }
  if (!res.ok) return;
  const users = await res.json();
  const body = document.getElementById("staff-body");
  if (!body) return;
  body.innerHTML = "";
  for (const u of users) {
    const inactive = u.status !== "Active";
    const mark = inactive ? ` class="lookup-archived"` : "";
    const tr = document.createElement("tr");
    tr.innerHTML = `
      <td data-label="Name"${mark}>${escapeHtml(u.staffName)}</td>
      <td data-label="Position"${mark}>${escapeHtml(u.position || "—")}</td>
      <td data-label="Username"${mark}>${escapeHtml(u.username)}</td>
      <td data-label="Role"${mark}>${escapeHtml(u.role)}</td>
      <td data-label="Status">
        <select data-status="${u.staffId}" class="${inactive ? "lookup-archived" : ""}">
          <option value="Active" ${u.status === "Active" ? "selected" : ""}>Active</option>
          <option value="NotActive" ${u.status === "NotActive" ? "selected" : ""}>NotActive</option>
        </select>
      </td>
      <td class="staff-row-actions" data-label="Actions">
        <button type="button" data-edit="${u.staffId}" data-name="${escapeHtml(u.staffName)}" data-position="${escapeHtml(u.position || "")}" data-username="${escapeHtml(u.username)}" data-role="${escapeHtml(u.role)}" class="btn-ghost">Edit</button>
        ${isSigningPosition(u.position) ? `<button type="button" data-sign="${u.staffId}" data-name="${escapeHtml(u.staffName)}" class="btn-ghost">${u.signingCertificate ? "Replace signing certificate" : "Create signing certificate"}</button>` : ""}
        ${isSigningPosition(u.position) && u.signingCertificate ? `<button type="button" data-unsign="${u.staffId}" data-name="${escapeHtml(u.staffName)}" class="btn-ghost">Delete signing certificate</button>` : ""}
        <button type="button" data-reset="${u.userId}" class="btn-ghost">Reset password</button>
        ${trashButton(`data-remove="${u.userId}" data-name="${escapeHtml(u.staffName)}"`, "Permanently delete staff + login")}
      </td>`;
    body.appendChild(tr);
  }
  body.querySelectorAll("[data-sign]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const name = btn.dataset.name || "this person";
      if (!confirm(`Create a signing certificate for ${name} and store it on this server?`)) return;
      const res = await fetch(`/api/staff/${btn.dataset.sign}/signing-certificate`, {
        method: "POST",
        credentials: "same-origin",
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        alert(data.error || "Could not create the signing certificate.");
        return;
      }
      alert(`Stored on this server as ${data.file}.\n\nPassword: ${data.password}\n\nThe password is also saved beside the certificate so this server can use it.`);
      loadUsers();
    });
  });
  body.querySelectorAll("[data-unsign]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const name = btn.dataset.name || "this person";
      if (!confirm(`Delete the signing certificate for ${name}? This server will not be able to seal that person's certificates until a new one is created.`)) return;
      const res = await fetch(`/api/staff/${btn.dataset.unsign}/signing-certificate`, {
        method: "DELETE",
        credentials: "same-origin",
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        alert(data.error || "Could not delete the signing certificate.");
        return;
      }
      loadUsers();
    });
  });
  body.querySelectorAll("[data-status]").forEach((sel) => {
    sel.addEventListener("change", async () => {
      const r = await fetch(`/api/staff/${sel.dataset.status}`, {
        method: "PUT",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ status: sel.value }),
      });
      if (!r.ok) {
        const d = await r.json().catch(() => ({}));
        alert(d.error || "Failed");
        loadUsers();
        return;
      }
      loadUsers();
      loadStaff();
      loadSupervisors();
    });
  });
  body.querySelectorAll("[data-edit]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const currentName = btn.dataset.name || "";
      const currentPosition = btn.dataset.position || "";
      const currentUsername = btn.dataset.username || "";
      const currentRole = btn.dataset.role || "";
      const options = positionCache.map((p) => ({
        value: p.positionName,
        label: `${p.positionName} → ${p.role}`,
      }));
      if (currentPosition && !options.some((opt) => opt.value === currentPosition)) {
        options.unshift({ value: currentPosition, label: currentPosition });
      }
      openSettingsEdit({
        heading: "Edit staff",
        fields: [
          { label: "Name", name: "staffName", value: currentName },
          { label: "Position", name: "position", value: currentPosition, options },
          {
            label: "Role",
            name: "role",
            value: currentRole,
            options: [
              { value: "staff", label: "staff" },
              { value: "admin", label: "admin" },
            ],
          },
          { label: "Username", name: "username", value: currentUsername },
          { label: "Password (6 digits)", name: "password", maxLength: 6, inputMode: "numeric", placeholder: "Leave blank to keep" },
        ],
        onSubmit: async (data) => {
          const staffName = (data.staffName || "").trim();
          const username = (data.username || "").trim();
          const password = data.password || "";
          if (!staffName) throw new Error("Name is required");
          if (!username) throw new Error("Username is required");
          if (password && !/^[0-9]{6}$/.test(password)) throw new Error("Password must be 6 digits");
          const role = data.role || "";
          if (!["staff", "admin"].includes(role)) throw new Error("Role is required");
          const body = { staffName, position: (data.position || "").trim(), username, role };
          if (password) body.password = password;
          const r = await fetch(`/api/staff/${btn.dataset.edit}`, {
            method: "PUT",
            credentials: "same-origin",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(body),
          });
          const d = await r.json().catch(() => ({}));
          if (!r.ok) throw new Error(d.error || "Failed");
          loadUsers();
          loadStaff();
          loadSupervisors();
        },
      });
      const editForm = document.getElementById("settings-edit");
      const positionField = editForm?.querySelector('[name="position"]');
      const roleField = editForm?.querySelector('[name="role"]');
      positionField?.addEventListener("change", () => {
        const match = positionCache.find((p) => p.positionName === positionField.value);
        if (roleField && match) roleField.value = match.role;
      });
    });
  });
  body.querySelectorAll("[data-reset]").forEach((btn) => {
    btn.addEventListener("click", () => {
      openSettingsEdit({
        heading: "Reset password",
        submitLabel: "Update password",
        fields: [{ label: "New password", name: "password", maxLength: 6, inputMode: "numeric" }],
        onSubmit: async (data) => {
          const password = data.password || "";
          if (!/^[0-9]{6}$/.test(password)) throw new Error("Password must be 6 digits");
          const r = await fetch(`/api/users/${btn.dataset.reset}/password`, {
            method: "PUT",
            credentials: "same-origin",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ password }),
          });
          const d = await r.json().catch(() => ({}));
          if (!r.ok) throw new Error(d.error || "Failed");
        },
      });
    });
  });
  body.querySelectorAll("[data-remove]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const name = btn.dataset.name || "this staff member";
      if (
        !confirmCascadeDelete(name, [
          "• Login account",
          "• Medpost staff record",
          "• All activities they created",
          "• All evidence on those activities",
          "• All supervisor assignments for them",
        ].join("\n") + "\n\nTo keep history, set status to NotActive instead.")
      ) {
        return;
      }
      const r = await fetch(`/api/users/${btn.dataset.remove}`, {
        method: "DELETE",
        credentials: "same-origin",
      });
      const d = await r.json().catch(() => ({}));
      if (!r.ok) {
        alert(d.error || "Failed");
        return;
      }
      alert(
        `Deleted “${d.staffName || name}”.\n\nCascade removed: ${d.activities || 0} activities, ${d.supervisors || 0} supervisor rows, plus login.`
      );
      loadUsers();
      loadStaff();
      loadSupervisors();
    });
  });
}

async function loadPrograms() {
  const res = await fetch("/api/admin/procedures", { credentials: "same-origin" });
  if (!res.ok) return;
  programsCache = await res.json();
  fillProgramSelects();
  const list = document.getElementById("programs");
  list.innerHTML = "";
  for (const p of programsCache) {
    const row = document.createElement("div");
    row.className = "settings-row";
    const archived = p.status !== "Active";
    const nextStatus = archived ? "Active" : "Archived";
    const archiveLabel = archived ? "Restore" : "Archive";
    row.innerHTML = `
      <div>
        <strong>${archivedValueHtml(p.title, archived)}</strong>
        <div class="${archived ? "lookup-archived" : ""}" style="font-size:0.8rem;margin-top:2px${archived ? "" : ";color:var(--text-3)"}">${escapeHtml(p.status)}</div>
      </div>
      <div class="settings-row-actions">
        <button type="button" data-edit-proc="${p.procId}" data-title="${escapeHtml(p.title)}" class="btn-ghost">Edit</button>
        <button type="button" data-proc="${p.procId}" data-status="${nextStatus}" class="btn-ghost${archived ? " lookup-archived" : ""}">${archiveLabel}</button>
        ${trashButton(`data-del-proc="${p.procId}" data-title="${escapeHtml(p.title)}"`, "Permanently delete program")}
      </div>`;
    list.appendChild(row);
  }
  list.querySelectorAll("[data-edit-proc]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const current = btn.dataset.title || "";
      openSettingsEdit({
        heading: "Edit program",
        fields: [{ label: "Program title", name: "title", value: current }],
        onSubmit: async (data) => {
          const title = (data.title || "").trim();
          if (!title) throw new Error("Title is required");
          if (title === current) return;
          const r = await fetch(`/api/procedures/${btn.dataset.editProc}`, {
            method: "PUT",
            credentials: "same-origin",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ title }),
          });
          const d = await r.json().catch(() => ({}));
          if (!r.ok) throw new Error(d.error || "Failed");
          loadPrograms();
          loadSupervisors();
          loadDocuments();
        },
      });
    });
  });
  list.querySelectorAll("[data-proc]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const r = await fetch(`/api/procedures/${btn.dataset.proc}`, {
        method: "PUT",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ status: btn.dataset.status }),
      });
      if (!r.ok) {
        const d = await r.json().catch(() => ({}));
        alert(d.error || "Failed");
        return;
      }
      loadPrograms();
    });
  });
  list.querySelectorAll("[data-del-proc]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const title = btn.dataset.title || "this program";
      if (
        !confirmCascadeDelete(title, [
          "• All activities for this program",
          "• All evidence (photos / attachments) on those activities",
          "• All program documents",
          "• All supervisor assignments for this program",
        ].join("\n"))
      ) {
        return;
      }
      const r = await fetch(`/api/procedures/${btn.dataset.delProc}`, {
        method: "DELETE",
        credentials: "same-origin",
      });
      const d = await r.json().catch(() => ({}));
      if (!r.ok) {
        alert(d.error || "Failed");
        return;
      }
      alert(
        `Deleted “${d.title || title}”.\n\nCascade removed: ${d.activities || 0} activities, ${d.documents || 0} documents, ${d.supervisors || 0} supervisors.`
      );
      loadPrograms();
      loadSupervisors();
      loadDocuments();
    });
  });
}

async function loadSupervisors() {
  const res = await fetch("/api/supervisors", { credentials: "same-origin" });
  if (!res.ok) return;
  const rows = await res.json();
  const body = document.getElementById("supervisor-body");
  body.innerHTML = "";
  for (const s of rows) {
    const tr = document.createElement("tr");
    tr.innerHTML = `
      <td>${escapeHtml(s.programTitle)}</td>
      <td>${escapeHtml(s.staffName)}</td>
      <td>${escapeHtml(s.startDate || "—")}</td>
      <td>${escapeHtml(s.endDate || "—")}</td>
      <td>${trashButton(`data-del-sv="${s.supervisorId}"`, "Delete supervisor assignment")}</td>`;
    body.appendChild(tr);
  }
  body.querySelectorAll("[data-del-sv]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      if (!confirm("Remove this supervisor assignment?")) return;
      const r = await fetch(`/api/supervisors/${btn.dataset.delSv}`, {
        method: "DELETE",
        credentials: "same-origin",
      });
      if (!r.ok) {
        const d = await r.json().catch(() => ({}));
        alert(d.error || "Failed");
        return;
      }
      loadSupervisors();
    });
  });
}

async function loadDocuments() {
  const filter = document.getElementById("doc-filter").value;
  let url = "/api/documents";
  if (filter === "common") url += "?scope=common";
  else if (filter) url += `?procId=${encodeURIComponent(filter)}`;
  const res = await fetch(url, { credentials: "same-origin" });
  if (!res.ok) return;
  const docs = await res.json();
  const body = document.getElementById("document-body");
  body.innerHTML = "";
  for (const d of docs) {
    const link = d.formLink
      ? `<a href="${escapeHtml(d.formLink)}" target="_blank" rel="noopener">Open link</a>`
      : d.hasFile
        ? `<a href="${escapeHtml(d.fileUrl)}" target="_blank">Download</a>`
        : "—";
    const tr = document.createElement("tr");
    tr.innerHTML = `
      <td>${escapeHtml(d.documentTitle)}</td>
      <td>${escapeHtml(d.documentsType)}</td>
      <td>${escapeHtml(d.programTitle || (d.documentsType === "Template" ? "Medpost-wide" : "—"))}</td>
      <td>${escapeHtml(d.uploadDate || "—")}</td>
      <td>${link}</td>
      <td class="doc-actions">
        <button type="button" data-edit-doc="${d.documentId}" data-title="${escapeHtml(d.documentTitle)}" class="btn-ghost">Edit title</button>
        ${trashButton(`data-del-doc="${d.documentId}"`, "Delete document")}
      </td>`;
    body.appendChild(tr);
  }
  body.querySelectorAll("[data-edit-doc]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const current = btn.dataset.title || "";
      openSettingsEdit({
        heading: "Edit document",
        fields: [{ label: "Document title", name: "documentTitle", value: current }],
        onSubmit: async (data) => {
          const documentTitle = (data.documentTitle || "").trim();
          if (!documentTitle) throw new Error("Title is required");
          if (documentTitle === current) return;
          const r = await fetch(`/api/documents/${btn.dataset.editDoc}`, {
            method: "PUT",
            credentials: "same-origin",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ documentTitle }),
          });
          const d = await r.json().catch(() => ({}));
          if (!r.ok) throw new Error(d.error || "Failed");
          loadDocuments();
        },
      });
    });
  });
  body.querySelectorAll("[data-del-doc]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      if (!confirm("Delete this document?")) return;
      const r = await fetch(`/api/documents/${btn.dataset.delDoc}`, {
        method: "DELETE",
        credentials: "same-origin",
      });
      if (!r.ok) {
        const d = await r.json().catch(() => ({}));
        alert(d.error || "Failed");
        return;
      }
      loadDocuments();
    });
  });
}

document.getElementById("add-staff")?.addEventListener("submit", async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const body = Object.fromEntries(fd.entries());
  const res = await fetch("/api/users", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    alert(data.error || "Failed");
    return;
  }
  e.target.reset();
  syncStaffRoleFromPosition();
  loadUsers();
  loadStaff();
});

document.getElementById("create-program").addEventListener("submit", async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const title = String(fd.get("name") || "").trim();
  const res = await fetch("/api/procedures", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ title }),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    alert(data.error || "Failed");
    return;
  }
  e.target.reset();
  loadPrograms();
});

document.getElementById("add-supervisor").addEventListener("submit", async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const res = await fetch("/api/supervisors", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      procId: Number(fd.get("procId")),
      staffId: Number(fd.get("staffId")),
      startDate: fd.get("startDate") || "",
      endDate: fd.get("endDate") || "",
    }),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    alert(data.error || "Failed");
    return;
  }
  e.target.reset();
  fillProgramSelects();
  fillStaffSelect();
  loadSupervisors();
});

document.getElementById("doc-type").addEventListener("change", () => {
  const isTemplate = document.getElementById("doc-type").value === "Template";
  document.getElementById("doc-program-wrap").hidden = isTemplate;
  document.getElementById("doc-program").required = !isTemplate;
  // A website is a link only, so the file picker goes away and the address becomes required.
  const isWebsite = document.getElementById("doc-type").value === "Website";
  document.getElementById("doc-file-wrap").hidden = isWebsite;
  document.querySelector('#add-document [name="file"]').disabled = isWebsite;
  document.querySelector('#add-document [name="formLink"]').required = isWebsite;
});

document.getElementById("add-document").addEventListener("submit", async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const dtype = fd.get("documentsType");
  if (dtype === "Template") fd.delete("procId");
  const hasFile = fd.get("file") && fd.get("file").size > 0;
  const hasLink = String(fd.get("formLink") || "").trim();
  if (!hasFile && !hasLink) {
    alert("Provide a form URL or a PDF file");
    return;
  }
  if (!hasFile) fd.delete("file");
  const res = await fetch("/api/documents", {
    method: "POST",
    credentials: "same-origin",
    body: fd,
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    alert(data.error || "Failed");
    return;
  }
  e.target.reset();
  document.getElementById("doc-type").dispatchEvent(new Event("change"));
  loadDocuments();
});

document.getElementById("doc-filter").addEventListener("change", loadDocuments);

// --- MCU email notifications (Settings → Email notifications) -----------

async function loadMcuNotificationConfig() {
  const form = document.getElementById("mcu-notification-form");
  if (!form) return;
  const res = await fetch("/api/mcu-email-notification", { credentials: "same-origin" });
  if (!res.ok) return;
  const cfg = await res.json();
  form.querySelector('[name="enabled"]').checked = !!cfg.enabled;
  form.querySelector('[name="smtpHost"]').value = cfg.smtpHost || "";
  form.querySelector('[name="smtpPort"]').value = cfg.smtpPort || 587;
  form.querySelector('[name="smtpUsername"]').value = cfg.smtpUsername || "";
  form.querySelector('[name="fromAddress"]').value = cfg.fromAddress || "";
  form.querySelector('[name="toAddresses"]').value = cfg.toAddresses || "";
  form.querySelector('[name="ccAddresses"]').value = cfg.ccAddresses || "";
  form.querySelector('[name="expiryThresholdDays"]').value = cfg.expiryThresholdDays || 60;
  document.getElementById("mcu-smtp-password").placeholder = cfg.smtpPasswordSet
    ? "Leave blank to keep existing"
    : "No password set yet";
  document.getElementById("mcu-notification-status").textContent =
    `Last sent: ${cfg.lastSentAt || "never"} — ${cfg.lastSendStatus || "—"}`;
}

document.getElementById("mcu-notification-form")?.addEventListener("submit", async (e) => {
  e.preventDefault();
  const fd = new FormData(e.target);
  const msg = document.getElementById("mcu-notification-msg");
  msg.textContent = "";
  const body = {
    enabled: fd.get("enabled") === "on",
    smtpHost: fd.get("smtpHost"),
    smtpPort: Number(fd.get("smtpPort")) || 587,
    smtpUsername: fd.get("smtpUsername"),
    smtpPassword: fd.get("smtpPassword") || "",
    fromAddress: fd.get("fromAddress"),
    toAddresses: fd.get("toAddresses"),
    ccAddresses: fd.get("ccAddresses"),
    expiryThresholdDays: Number(fd.get("expiryThresholdDays")) || 60,
  };
  const res = await fetch("/api/mcu-email-notification", {
    method: "PUT",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    msg.textContent = data.error || "Failed to save";
    return;
  }
  document.getElementById("mcu-smtp-password").value = "";
  loadMcuNotificationConfig();
});

document.getElementById("mcu-notification-send-now")?.addEventListener("click", async () => {
  const msg = document.getElementById("mcu-notification-msg");
  msg.textContent = "Sending…";
  const res = await fetch("/api/mcu-email-notification/send-now", { method: "POST", credentials: "same-origin" });
  const data = await res.json().catch(() => ({}));
  msg.textContent = res.ok ? `Sent: ${data.status}` : data.error || "Failed to send";
  loadMcuNotificationConfig();
});

const initial = (location.hash || "").replace("#", "") || DEFAULT_SECTION;
const initialPanelExists = !!document.querySelector(`.settings-panel[data-panel="${initial}"]`);
showSection(initialPanelExists ? initial : DEFAULT_SECTION);

async function loadDirectoryToken() {
  const input = document.getElementById("directory-token");
  if (!input) return;
  const res = await fetch("/api/directory/token", { credentials: "same-origin" });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    document.getElementById("directory-token-msg").textContent = data.error || "Could not load the token";
    return;
  }
  input.value = data.token || "";
  const full = document.getElementById("directory-full-token");
  if (full) full.value = data.fullToken || "";
}

document.getElementById("directory-token-update")?.addEventListener("click", async () => {
  const msg = document.getElementById("directory-token-msg");
  if (!confirm("Replace the eligible token? Databases that read only valid Fit and Fit with note results keep working only after you give them this new token.")) return;
  msg.textContent = "";
  const res = await fetch("/api/directory/token", { method: "POST", credentials: "same-origin" });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    msg.textContent = data.error || "Could not update the token";
    return;
  }
  document.getElementById("directory-token").value = data.token || "";
  msg.textContent = "Eligible token updated. Copy it only to databases that may read valid Fit and Fit with note results.";
});

document.getElementById("directory-full-token-update")?.addEventListener("click", async () => {
  const msg = document.getElementById("directory-token-msg");
  if (!confirm("Replace the all-checkups token? Databases reading every checkup keep working only after you give them this new token.")) return;
  msg.textContent = "";
  const res = await fetch("/api/directory/full-token", { method: "POST", credentials: "same-origin" });
  const data = await res.json().catch(() => ({}));
  if (!res.ok || !data.fullToken) {
    msg.textContent = data.error || "Could not update the token";
    return;
  }
  document.getElementById("directory-full-token").value = data.fullToken;
  msg.textContent = "All-checkups token updated. Copy it only to a database that may read every checkup.";
});

if (ROLE === "admin") {
  loadPositions();
  loadUsers();
  loadEntityTypes();
  loadProjects();
  loadCompanies().then(() => loadCompanyRelations());
  loadNationalities();
  loadCheckUpCriteria();
  loadReviewResults();
  loadWorkLocations();
  loadTaskCategories();
  loadMcuNotificationConfig();
  loadDirectoryToken();
}
loadStaff();
loadPrograms().then(() => {
  loadSupervisors();
  loadDocuments();
  document.getElementById("doc-type").dispatchEvent(new Event("change"));
});
refreshPresence = startPresence(() => settingsSection);
