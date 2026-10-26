async function logout() {
  await fetch("/api/logout", { method: "POST", credentials: "same-origin" });
  location.href = "/";
}

document.getElementById("logout")?.addEventListener("click", logout);
document.getElementById("mobile-logout")?.addEventListener("click", (e) => {
  e.preventDefault();
  logout();
});

const listEl = document.getElementById("program-list");
const homeNavItem = document.getElementById("home-nav-item");
const titleEl = document.getElementById("program-title");
const subEl = document.getElementById("program-sub");
const filterEl = document.getElementById("date-filter");
const addForm = document.getElementById("add-activity");
guardCase(addForm?.querySelector('[name="activityTitle"]'), toSentenceCase);
guardCase(addForm?.querySelector('[name="activitySummary"]'), toSentenceCase);
mountSummaryEditor(addForm?.querySelector('[name="activitySummary"]'));
const toggleAdd = document.getElementById("toggle-add");
const cancelAdd = document.getElementById("cancel-add");
const addMsg = document.getElementById("add-msg");
compressPhotosInput(addForm?.querySelector('[name="photos"]'), addMsg);
const groupsEl = document.getElementById("groups");
const emptyEl = document.getElementById("empty");
const lightbox = document.getElementById("lightbox");
const lightboxImg = document.getElementById("lightbox-img");
const programTabs = document.getElementById("program-tabs");
const tabActivitiesEl = document.getElementById("tab-activities");
const tabReferenceEl = document.getElementById("tab-reference");
const referenceBody = document.getElementById("reference-body");
const referenceEmpty = document.getElementById("reference-empty");
const tabSubheadersEl = document.getElementById("tab-subheaders");
const addSubHeaderForm = document.getElementById("add-subheader");
const subHeaderListEl = document.getElementById("subheader-list");
const subHeaderEmptyEl = document.getElementById("subheader-empty");
const tabSubheaderEntriesEl = document.getElementById("tab-subheader-entries");
const subHeaderBackBtn = document.getElementById("subheader-back");
const subHeaderTitleEl = document.getElementById("subheader-title");
const toggleAddEntry = document.getElementById("toggle-add-entry");
const cancelAddEntry = document.getElementById("cancel-add-entry");
const addSubHeaderEntryForm = document.getElementById("add-subheader-entry");
const subHeaderEntriesEl = document.getElementById("subheader-entries");
const subHeaderEntriesEmptyEl = document.getElementById("subheader-entries-empty");
const subHeaderEntryMsg = document.getElementById("subheader-entry-msg");
let selectedSubHeaderId = null;
const downloadAll = document.getElementById("download-all");
const downloadAllUploads = document.getElementById("download-all-uploads");
const downloadAllEntries = document.getElementById("download-all-entries");
const downloadAllUploadsMsg = document.createElement("div");
downloadAllUploadsMsg.className = "app-download-batches";
downloadAllUploadsMsg.hidden = true;
downloadAllUploads?.insertAdjacentElement("afterend", downloadAllUploadsMsg);

let activeTab = "activities";

// selectedId is "home" (all-programs feed), "" (nothing loaded yet), or a
// numeric program id as a string.
let selectedId = window.__PROC_ID__ && window.__PROC_ID__ !== "" ? String(window.__PROC_ID__) : "home";
let programs = [];

lightbox?.addEventListener("click", () => lightbox.close());

function escapeHtml(s) {
  return String(s)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

function activitySummaryMarkup(text) {
  const html = renderActivitySummary(text || "");
  return html ? `<div class="app-post-summary">${html}</div>` : "";
}

function uploaderCaption(item) {
  const parts = [];
  if (item.uploadedBy) parts.push(`Uploaded by ${item.uploadedBy}`);
  if (item.uploadDate) parts.push(item.uploadDate);
  return parts.join(" · ");
}

function renderDocLink(d) {
  const thumb = d.isPdf
    ? `<embed src="${escapeHtml(d.url)}#page=1" type="application/pdf" class="app-doc-thumb" />`
    : `<span class="app-doc-thumb app-doc-thumb-generic">FILE</span>`;
  const caption = uploaderCaption(d);
  return `<span class="app-doc-chip" data-evidence-id="${d.evidenceId}">
    <a href="${escapeHtml(d.url)}" target="_blank" rel="noopener" class="app-doc-view" title="View ${escapeHtml(d.filename)}">
      ${thumb}
      <span class="app-doc-info">
        <span class="app-doc-name">${escapeHtml(d.filename)}</span>
        ${caption ? `<span class="app-doc-meta">${escapeHtml(caption)}</span>` : ""}
      </span>
    </a>
    <a href="${escapeHtml(d.url)}" download="${escapeHtml(d.filename)}" class="app-doc-download" title="Download" aria-label="Download">&#8595;</a>
  </span>`;
}

// Activity edit/delete: the activity's own logger, or an admin — same
// ownership rule the server enforces in UpdateActivity/DeleteActivity.
function canEditActivity(a) {
  return window.__USER_ROLE__ === "admin" || Number(window.__USER_STAFF_ID__) === Number(a.loggedByStaffId);
}

function renderEditButton(a) {
  if (!canEditActivity(a)) return "";
  return `<button type="button" class="btn-ghost" data-edit-activity="${a.activityId}">Edit</button>`;
}

function renderDeleteButton(a) {
  if (!canEditActivity(a)) return "";
  return `<button type="button" class="app-post-delete" data-activity-id="${a.activityId}" title="Delete activity" aria-label="Delete activity">&times;</button>`;
}

async function deleteActivity(btn) {
  if (!confirm("Delete this activity? This cannot be undone.")) return;
  const id = btn.dataset.activityId;
  btn.disabled = true;
  try {
    const res = await fetch(`/api/activities/${id}`, {
      method: "DELETE",
      credentials: "same-origin",
    });
    if (!res.ok) {
      alert("Failed to delete activity.");
      btn.disabled = false;
      return;
    }
    btn.closest(".app-post")?.remove();
  } catch {
    alert("Failed to delete activity.");
    btn.disabled = false;
  }
}

function wireDeleteButtons() {
  groupsEl.querySelectorAll(".app-post-delete").forEach((btn) => {
    btn.addEventListener("click", () => deleteActivity(btn));
  });
}

function activityTypeOptions(current) {
  return ["Monitoring/Inspection", "Socialization"]
    .map((value) => `<option value="${escapeHtml(value)}"${value === current ? " selected" : ""}>${escapeHtml(value)}</option>`)
    .join("");
}

// programOptions lists every active program so a wrongly-filed activity can
// be moved to a different one from its edit form (see relocateEvidence on
// the server, which moves its evidence files along with it).
function programOptions(currentProcId) {
  return programs
    .map((p) => `<option value="${p.procId}"${String(p.procId) === String(currentProcId) ? " selected" : ""}>${escapeHtml(p.title)}</option>`)
    .join("");
}

function renderEditPhoto(p) {
  return `<div class="app-edit-thumb" data-evidence-id="${p.evidenceId}">
    <button type="button" class="photo-thumb" data-url="${escapeHtml(p.url)}" aria-label="View photo">
      <img src="${escapeHtml(p.url)}" alt="" />
    </button>
    <button type="button" class="app-edit-remove" data-delete-evidence="${p.evidenceId}" aria-label="Remove photo">&times;</button>
  </div>`;
}

function renderEditDoc(d) {
  const thumb = d.isPdf
    ? `<embed src="${escapeHtml(d.url)}#page=1" type="application/pdf" class="app-edit-doc-preview" />`
    : `<span class="app-edit-doc-preview app-doc-thumb-generic">FILE</span>`;
  return `<div class="app-edit-thumb app-edit-doc" data-evidence-id="${d.evidenceId}">
    <a href="${escapeHtml(d.url)}" target="_blank" rel="noopener" title="${escapeHtml(d.filename)}">${thumb}</a>
    <span class="app-edit-doc-name">${escapeHtml(d.filename)}</span>
    <button type="button" class="app-edit-remove" data-delete-evidence="${d.evidenceId}" aria-label="Remove file">&times;</button>
  </div>`;
}

function renderActivityEditForm(a, currentProcId) {
  if (!canEditActivity(a)) return "";
  const photos = (a.photos || []).map(renderEditPhoto).join("");
  const docs = (a.documents || []).map(renderEditDoc).join("");
  return `<form class="add-activity" data-edit-form="${a.activityId}" hidden>
    <div class="section-label" style="margin-bottom:12px">Edit activity</div>
    <div class="settings-grid">
      <label>
        <span>Program</span>
        <select name="procId" required>${programOptions(currentProcId ?? a.procId)}</select>
      </label>
      <label>
        <span>Type</span>
        <select name="activityType" required>${activityTypeOptions(a.activityType)}</select>
      </label>
      <label>
        <span>Date</span>
        <input type="date" name="activityDate" required value="${escapeHtml(a.activityDate || "")}" />
      </label>
      <label style="grid-column:1/-1">
        <span>Title</span>
        <input name="activityTitle" required value="${escapeHtml(a.activityTitle || "")}" />
      </label>
      <label style="grid-column:1/-1">
        <span>Summary</span>
        <textarea name="activitySummary" rows="3">${escapeHtml(a.activitySummary || "")}</textarea>
      </label>
      <label>
        <span>Location</span>
        <input name="location" value="${escapeHtml(a.location || "")}" />
      </label>
      <label>
        <span>Notes</span>
        <input name="notes" value="${escapeHtml(a.notes || "")}" />
      </label>
    </div>
    <div class="app-edit-media">
      <span>Photos</span>
      <div class="app-edit-album" data-photo-album>
        ${photos}
        <div data-pending-photos></div>
        <label class="app-edit-add" title="Add photos">
          <span>+</span>
          <input type="file" name="photos" accept="image/jpeg,image/png,image/gif,image/webp" multiple />
        </label>
      </div>
      <span>Documents</span>
      ${docs ? `<div class="app-edit-album" data-doc-album>${docs}</div>` : `<div class="app-edit-album" data-doc-album hidden></div>`}
    </div>
    <div class="settings-grid">
      <label>
        <span>Add attendance list (optional)</span>
        <input type="file" name="attendance" accept="application/pdf,.pdf" />
      </label>
      <label>
        <span>Add report attachment (optional)</span>
        <input type="file" name="report" accept="application/pdf,.pdf" />
      </label>
    </div>
    <p class="app-edit-hint">+ adds photos. × removes a file. New documents can be attached below.</p>
    <div style="margin-top:16px;display:flex;gap:10px">
      <button type="submit" class="btn-primary">Save changes</button>
      <button type="button" class="btn-ghost" data-cancel-edit>Cancel</button>
    </div>
    <p data-edit-msg style="margin-top:10px;font-size:0.875rem;color:var(--text-3)"></p>
  </form>`;
}

function syncEditMedia(form) {
  const post = form.closest(".app-post");
  if (!post) return;
  post.querySelectorAll(":scope > .app-photo-grid, :scope > .app-doc-grid").forEach((el) => {
    el.hidden = !form.hidden;
  });
}

function dropRemovedEvidence(evidenceId) {
  document.querySelectorAll(`[data-evidence-id="${evidenceId}"]`).forEach((el) => {
    const grid = el.closest(".app-photo-grid, .app-doc-grid, .app-edit-album");
    el.remove();
    if (grid && grid.classList.contains("app-edit-album") && grid.dataset.docAlbum !== undefined && !grid.querySelector("[data-evidence-id]")) {
      grid.hidden = true;
    }
    if (grid && !grid.classList.contains("app-edit-album") && !grid.querySelector("[data-evidence-id], .photo-thumb, .app-doc-chip")) {
      grid.remove();
    }
  });
}

async function deleteEvidence(btn) {
  if (!confirm("Remove this file? This cannot be undone.")) return;
  const id = btn.dataset.deleteEvidence;
  btn.disabled = true;
  try {
    const res = await fetch(`/api/uploads/${id}`, { method: "DELETE", credentials: "same-origin" });
    if (!res.ok) {
      const d = await res.json().catch(() => ({}));
      alert(d.error || "Could not remove file.");
      btn.disabled = false;
      return;
    }
    dropRemovedEvidence(id);
  } catch {
    alert("Could not remove file.");
    btn.disabled = false;
  }
}

async function appendChosenPhotos(input, statusEl) {
  const incoming = Array.from(input.files || []);
  input.value = "";
  if (!incoming.length) return;
  const kept = input._pendingPhotos || [];
  if (statusEl) statusEl.textContent = "Compressing photos…";
  for (const file of incoming) {
    if (!file.type.startsWith("image/") || file.size <= PHOTO_MIN_BYTES) {
      kept.push(file);
      continue;
    }
    try {
      const blob = await compressImage(file);
      const name = file.name.replace(/\.[^.]+$/, "") + ".jpg";
      kept.push(blob.size < file.size ? new File([blob], name, { type: "image/jpeg" }) : file);
    } catch {
      kept.push(file);
    }
  }
  input._pendingPhotos = kept;
  const dt = new DataTransfer();
  kept.forEach((file) => dt.items.add(file));
  input.files = dt.files;
  if (statusEl) statusEl.textContent = "";
  renderPendingPhotos(input);
}

function renderPendingPhotos(input) {
  const slot = input.closest("[data-photo-album]")?.querySelector("[data-pending-photos]");
  if (!slot) return;
  slot.querySelectorAll("img").forEach((img) => URL.revokeObjectURL(img.src));
  const files = input._pendingPhotos || [];
  slot.innerHTML = files
    .map((file, index) => {
      const url = URL.createObjectURL(file);
      return `<div class="app-edit-thumb" data-pending-index="${index}">
        <img src="${url}" alt="" />
        <button type="button" class="app-edit-remove" data-remove-pending="${index}" aria-label="Remove new photo">&times;</button>
      </div>`;
    })
    .join("");
}

function removePendingPhoto(input, index) {
  const kept = (input._pendingPhotos || []).filter((_, i) => i !== index);
  input._pendingPhotos = kept;
  const dt = new DataTransfer();
  kept.forEach((file) => dt.items.add(file));
  input.files = dt.files;
  renderPendingPhotos(input);
}

function clearPendingPhotos(form) {
  const input = form.querySelector('[name="photos"]');
  if (!input) return;
  input._pendingPhotos = [];
  renderPendingPhotos(input);
}

function dropEmptyFiles(fd, name) {
  const files = fd.getAll(name).filter((f) => f instanceof File && f.size > 0);
  fd.delete(name);
  for (const file of files) fd.append(name, file);
}

function wireActivityEditButtons() {
  groupsEl.querySelectorAll("[data-edit-activity]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const form = groupsEl.querySelector(`[data-edit-form="${btn.dataset.editActivity}"]`);
      if (!form) return;
      const opening = form.hidden;
      groupsEl.querySelectorAll("[data-edit-form]").forEach((other) => {
        other.hidden = true;
        syncEditMedia(other);
      });
      if (!opening) return;
      form.hidden = false;
      syncEditMedia(form);
      if (!form.dataset.wired) {
        form.dataset.wired = "1";
        guardCase(form.querySelector('[name="activityTitle"]'), toSentenceCase);
        guardCase(form.querySelector('[name="activitySummary"]'), toSentenceCase);
        mountSummaryEditor(form.querySelector('[name="activitySummary"]'));
        const photoInput = form.querySelector('[name="photos"]');
        photoInput?.addEventListener("change", () => {
          appendChosenPhotos(photoInput, form.querySelector("[data-edit-msg]"));
        });
        form.addEventListener("click", (event) => {
          const pending = event.target.closest("[data-remove-pending]");
          if (pending) {
            event.preventDefault();
            removePendingPhoto(photoInput, Number(pending.dataset.removePending));
            return;
          }
          const saved = event.target.closest("[data-delete-evidence]");
          if (saved) {
            event.preventDefault();
            deleteEvidence(saved);
          }
        });
      }
      form.querySelector('[name="activityTitle"]')?.focus();
    });
  });

  groupsEl.querySelectorAll("[data-edit-form]").forEach((form) => {
    form.querySelector("[data-cancel-edit]")?.addEventListener("click", () => {
      form.reset();
      clearPendingPhotos(form);
      form.hidden = true;
      syncEditMedia(form);
      const msg = form.querySelector("[data-edit-msg]");
      if (msg) msg.textContent = "";
    });
    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      const editor = form.querySelector(".summary-doc-body");
      const summary = form.querySelector('[name="activitySummary"]');
      if (editor && summary) summary.value = serializeSummary(editor);
      const msg = form.querySelector("[data-edit-msg]");
      const submit = form.querySelector('[type="submit"]');
      if (msg) msg.textContent = "Saving…";
      if (submit) submit.disabled = true;
      const fd = new FormData(form);
      dropEmptyFiles(fd, "photos");
      dropEmptyFiles(fd, "attendance");
      dropEmptyFiles(fd, "report");
      try {
        const r = await fetch(`/api/activities/${form.dataset.editForm}`, {
          method: "PUT",
          credentials: "same-origin",
          body: fd,
        });
        const d = await r.json().catch(() => ({}));
        if (!r.ok) {
          if (msg) msg.textContent = d.error || "Could not save changes";
          if (submit) submit.disabled = false;
          return;
        }
        loadActivities();
      } catch {
        if (msg) msg.textContent = "Could not save changes";
        if (submit) submit.disabled = false;
      }
    });
  });
}

function groupActivities(activities) {
  const byType = new Map();
  for (const a of activities) {
    if (!byType.has(a.activityType)) byType.set(a.activityType, new Map());
    const byDate = byType.get(a.activityType);
    const d = a.activityDate || "No date";
    if (!byDate.has(d)) byDate.set(d, []);
    byDate.get(d).push(a);
  }
  return byType;
}

// Programs whose sub-header tree is currently expanded in the sidebar.
const expandedPrograms = new Set();

function renderSidebar() {
  homeNavItem?.classList.toggle("active", selectedId === "home");
  listEl.innerHTML = "";
  for (const p of programs) {
    const title = p.title || p.healthProcedure;
    const key = String(p.procId);
    const isOpen = expandedPrograms.has(key);

    const node = document.createElement("div");
    node.className = "app-tree-node" + (isOpen ? " is-open" : "");

    const row = document.createElement("div");
    row.className = "app-tree-row";

    const toggle = document.createElement("button");
    toggle.type = "button";
    toggle.className = "app-tree-toggle" + (isOpen ? " open" : "");
    toggle.setAttribute("aria-label", "Toggle sub-headers");
    toggle.setAttribute("aria-expanded", String(isOpen));
    toggle.textContent = isOpen ? "▾" : "▸";

    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "app-program-item" + (key === selectedId ? " active" : "");
    btn.innerHTML = `
      <span class="app-program-item-title">${escapeHtml(title)}</span>
      <span class="app-program-item-meta">Open activities</span>`;
    // The title toggles the program's sub-headers like the Medical Record sidebar:
    // the first click opens it (and selects the program), the next click collapses
    // it and leaves the page as it is.
    btn.addEventListener("click", () => {
      if (expandedPrograms.has(key)) {
        toggleProgramTree(p.procId, toggle, children);
        return;
      }
      expandedPrograms.add(key);
      selectProgram(p.procId, true); // redraws the sidebar with this program open
    });

    const children = document.createElement("div");
    children.className = "app-tree-children";
    children.hidden = !isOpen;

    toggle.addEventListener("click", (e) => {
      e.stopPropagation();
      toggleProgramTree(p.procId, toggle, children);
    });

    row.appendChild(toggle);
    row.appendChild(btn);
    node.appendChild(row);
    node.appendChild(children);
    listEl.appendChild(node);

    if (isOpen) renderProgramTreeChildren(p.procId, children);
  }
}

async function toggleProgramTree(procId, toggle, children) {
  const key = String(procId);
  const node = toggle.closest(".app-tree-node");
  if (expandedPrograms.has(key)) {
    expandedPrograms.delete(key);
    children.hidden = true;
    toggle.classList.remove("open");
    toggle.setAttribute("aria-expanded", "false");
    toggle.textContent = "▸";
    node?.classList.remove("is-open");
    return;
  }
  expandedPrograms.add(key);
  toggle.classList.add("open");
  toggle.setAttribute("aria-expanded", "true");
  toggle.textContent = "▾";
  node?.classList.add("is-open");
  children.hidden = false;
  await renderProgramTreeChildren(procId, children);
}

async function renderProgramTreeChildren(procId, children) {
  children.innerHTML = `<div class="app-tree-empty">Loading…</div>`;
  const res = await fetch(`/api/programs/${procId}/subheaders`, { credentials: "same-origin" });
  if (res.status === 401) {
    location.href = "/";
    return;
  }
  const subHeaders = res.ok ? await res.json() : [];
  if (!subHeaders.length) {
    children.innerHTML = `<div class="app-tree-empty">No sub-headers yet</div>`;
    return;
  }
  children.innerHTML = "";
  for (const s of subHeaders) {
    const isActive = String(procId) === selectedId && String(s.subHeaderId) === String(selectedSubHeaderId);
    const child = document.createElement("button");
    child.type = "button";
    child.className = "app-tree-child" + (isActive ? " active" : "");
    child.textContent = s.subHeaderName + (s.status === "Archived" ? " (Archived)" : "");
    child.addEventListener("click", () => jumpToSubHeader(procId, s.subHeaderId, s.subHeaderName));
    children.appendChild(child);
  }
}

// Clicking a sub-header in the sidebar tree: switch to its program if
// needed, then open its entries view directly (same window any role uses
// to log/view activities, just scoped to this sub-header).
async function jumpToSubHeader(procId, subHeaderId, subHeaderName) {
  if (String(procId) !== selectedId) {
    await selectProgram(procId, true);
  }
  showSubHeaderEntries(subHeaderId, subHeaderName);
}

function renderHomeFeed(data) {
  titleEl.textContent = "Activities";
  subEl.textContent = "Every activity across all health programs, most recent first.";
  filterEl.hidden = false;
  toggleAdd.hidden = true;
  if (downloadAll) downloadAll.hidden = true;
  if (downloadAllUploads) downloadAllUploads.hidden = false;
  if (downloadAllEntries) downloadAllEntries.hidden = false;
  groupsEl.innerHTML = "";

  if (!data.activities?.length) {
    emptyEl.hidden = false;
    emptyEl.textContent = "No activities in this date range.";
    return;
  }
  emptyEl.hidden = true;

  for (const a of data.activities) {
    const photos = (a.photos || [])
      .map(
        (p) =>
          `<button type="button" data-evidence-id="${p.evidenceId}" data-url="${escapeHtml(p.url)}" class="photo-thumb">
            <img src="${escapeHtml(p.url)}" alt="" loading="lazy" title="${escapeHtml(uploaderCaption(p))}" />
          </button>`
      )
      .join("");
    const docs = (a.documents || []).map(renderDocLink).join("");

    const post = document.createElement("article");
    post.className = "app-post";
    post.innerHTML = `
      <div class="app-post-body">
        <div class="app-post-head">
          <div class="app-post-date">${escapeHtml(a.activityDate || "No date")}${a.uploadedBy ? ` · Uploaded by ${escapeHtml(a.uploadedBy)}` : ""} · ${escapeHtml(a.programTitle || "")} · ${escapeHtml(a.activityType)}</div>
          <div style="display:flex;gap:6px;align-items:center">
            ${renderEditButton(a)}
            ${renderDeleteButton(a)}
          </div>
        </div>
        <h3 class="app-post-title">${escapeHtml(a.activityTitle)}</h3>
        ${activitySummaryMarkup(a.activitySummary)}
      </div>
      ${photos ? `<div class="app-photo-grid">${photos}</div>` : ""}
      ${docs ? `<div class="app-doc-grid">${docs}</div>` : ""}
      ${renderActivityEditForm(a)}`;
    groupsEl.appendChild(post);
  }

  groupsEl.querySelectorAll(".photo-thumb").forEach((btn) => {
    btn.addEventListener("click", () => {
      lightboxImg.src = btn.dataset.url;
      lightbox.showModal();
    });
  });
  wireDeleteButtons();
  wireActivityEditButtons();
}

function renderFeed(data) {
  titleEl.textContent = data.title || data.healthProcedure;
  subEl.textContent = "Activities, photo evidence, and summaries for this program.";
  filterEl.hidden = false;
  groupsEl.innerHTML = "";

  if (!data.activities?.length) {
    emptyEl.hidden = false;
    emptyEl.textContent = "No activities in this date range.";
    return;
  }
  emptyEl.hidden = true;

  const grouped = groupActivities(data.activities);
  for (const [type, byDate] of grouped) {
    const typeLabel = document.createElement("div");
    typeLabel.className = "app-type-label";
    typeLabel.textContent = type;
    groupsEl.appendChild(typeLabel);

    for (const [date, acts] of byDate) {
      for (const a of acts) {
        const photos = (a.photos || [])
          .map(
            (p) =>
              `<button type="button" data-evidence-id="${p.evidenceId}" data-url="${escapeHtml(p.url)}" class="photo-thumb">
                <img src="${escapeHtml(p.url)}" alt="" loading="lazy" title="${escapeHtml(uploaderCaption(p))}" />
              </button>`
          )
          .join("");
        const docs = (a.documents || []).map(renderDocLink).join("");

        const post = document.createElement("article");
        post.className = "app-post";
        post.innerHTML = `
          <div class="app-post-body">
            <div class="app-post-head">
              <div class="app-post-date">${escapeHtml(date)}${a.uploadedBy ? ` · Uploaded by ${escapeHtml(a.uploadedBy)}` : ""}</div>
              <div style="display:flex;gap:6px;align-items:center">
                ${renderEditButton(a)}
                ${renderDeleteButton(a)}
              </div>
            </div>
            <h3 class="app-post-title">${escapeHtml(a.activityTitle)}</h3>
            ${activitySummaryMarkup(a.activitySummary)}
          </div>
          ${photos ? `<div class="app-photo-grid">${photos}</div>` : ""}
          ${docs ? `<div class="app-doc-grid">${docs}</div>` : ""}
          ${renderActivityEditForm(a, selectedId)}`;
        groupsEl.appendChild(post);
      }
    }
  }

  groupsEl.querySelectorAll(".photo-thumb").forEach((btn) => {
    btn.addEventListener("click", () => {
      lightboxImg.src = btn.dataset.url;
      lightbox.showModal();
    });
  });
  wireDeleteButtons();
  wireActivityEditButtons();
}

let savedProgramTitle = null;
let savedProgramSub = null;

const ACTIVITY_SECTION_LABELS = {
  activities: "Activity",
  reference: "Reference",
  subheaders: "Sub-headers",
};
let activitySection = "Activity";
let refreshPresence = () => {};

// Switches the screen immediately. Wrapping the update in a view transition
// kept the old pixels frozen until every fetch inside it finished.
function runScreenChange(update) {
  return Promise.resolve().then(update);
}

function showTab(tab) {
  if (savedProgramTitle !== null) {
    titleEl.textContent = savedProgramTitle;
    subEl.textContent = savedProgramSub;
    savedProgramTitle = null;
    savedProgramSub = null;
  }
  selectedSubHeaderId = null;
  if (tabSubheaderEntriesEl) tabSubheaderEntriesEl.hidden = true;

  activeTab = tab;
  programTabs?.querySelectorAll(".app-tab").forEach((btn) => {
    btn.classList.toggle("active", btn.dataset.tab === tab);
  });
  tabActivitiesEl.hidden = tab !== "activities";
  tabReferenceEl.hidden = tab !== "reference";
  if (tabSubheadersEl) tabSubheadersEl.hidden = tab !== "subheaders";
  activitySection = ACTIVITY_SECTION_LABELS[tab] || "Activity";
  refreshPresence();
  renderSidebar();
  if (tab === "reference") return loadReferences();
  if (tab === "subheaders") return loadSubHeaders();
}

programTabs?.addEventListener("click", (e) => {
  const btn = e.target.closest("[data-tab]");
  if (!btn || btn.dataset.tab === activeTab) return;
  runScreenChange(() => showTab(btn.dataset.tab));
});

// Clicking a sub-header (from the sidebar tree, or from the admin
// Sub-headers tab's list) opens the same log/view window as Activities,
// just scoped to that sub-header, with its name shown in the panel head.
function showSubHeaderEntries(id, name) {
  return runScreenChange(async () => {
    if (savedProgramTitle === null) {
      savedProgramTitle = titleEl.textContent;
      savedProgramSub = subEl.textContent;
    }
    selectedSubHeaderId = id;
    titleEl.textContent = `${savedProgramTitle} — ${name}`;
    subEl.textContent = `Entries logged under "${name}".`;
    subHeaderTitleEl.textContent = name;

    programTabs?.querySelectorAll(".app-tab").forEach((btn) => btn.classList.remove("active"));
    tabActivitiesEl.hidden = true;
    tabReferenceEl.hidden = true;
    if (tabSubheadersEl) tabSubheadersEl.hidden = true;
    if (tabSubheaderEntriesEl) tabSubheaderEntriesEl.hidden = false;

    addSubHeaderEntryForm?.reset();
    if (addSubHeaderEntryForm) addSubHeaderEntryForm.hidden = true;
    subHeaderEntryMsg.textContent = "";
    await Promise.all([loadSubHeaderEntries(), loadSubmissionPicker()]);
    renderSidebar();
  });
}

// Lazily fills the "Link to a test" select on the add-entry form, once per
// page load — it's a short lookup list (see screening.ListAlcoholSubmissions)
// used when attaching a locked sub-header's entry (e.g. a Consent + Result
// scan) to the exact DrugAlcoholScreening row it documents.
let submissionPickerLoaded = false;
async function loadSubmissionPicker() {
  const sel = document.getElementById("subheader-entry-submission");
  if (!sel || submissionPickerLoaded) return;
  const res = await fetch("/api/screening/submissions", { credentials: "same-origin" });
  if (!res.ok) return;
  const rows = await res.json();
  submissionPickerLoaded = true;
  sel.innerHTML =
    `<option value="">— no linked test —</option>` +
    rows
      .map(
        (s) =>
          `<option value="${s.submissionId}">${escapeHtml(s.donorName)} — ${escapeHtml(s.createdAt)} (#${s.submissionId})</option>`
      )
      .join("");
}

subHeaderBackBtn?.addEventListener("click", () => runScreenChange(() => showTab("activities")));

toggleAddEntry?.addEventListener("click", () => {
  if (!selectedSubHeaderId || !addSubHeaderEntryForm) return;
  addSubHeaderEntryForm.hidden = !addSubHeaderEntryForm.hidden;
  if (!addSubHeaderEntryForm.hidden) {
    const dateInput = addSubHeaderEntryForm.querySelector('[name="entryDate"]');
    if (dateInput && !dateInput.value) {
      dateInput.value = clinicToday();
    }
  }
});

cancelAddEntry?.addEventListener("click", () => {
  if (addSubHeaderEntryForm) addSubHeaderEntryForm.hidden = true;
  subHeaderEntryMsg.textContent = "";
});

async function loadReferences() {
  if (!selectedId || selectedId === "home") return;
  referenceBody.innerHTML = "";
  referenceEmpty.hidden = true;

  const [programRes, commonRes] = await Promise.all([
    fetch(`/api/documents?procId=${encodeURIComponent(selectedId)}`, { credentials: "same-origin" }),
    fetch(`/api/documents?scope=common`, { credentials: "same-origin" }),
  ]);
  if (programRes.status === 401 || commonRes.status === 401) {
    location.href = "/";
    return;
  }
  const programDocs = programRes.ok ? await programRes.json() : [];
  const commonDocs = commonRes.ok ? await commonRes.json() : [];
  const docs = [...programDocs, ...commonDocs];

  if (!docs.length) {
    referenceEmpty.hidden = false;
    return;
  }

  for (const d of docs) {
    const link = d.formLink
      ? `<a href="${escapeHtml(d.formLink)}" target="_blank" rel="noopener">Open link</a>`
      : d.hasFile
        ? `<a href="${escapeHtml(d.fileUrl)}" target="_blank" rel="noopener">Open</a>`
        : "—";
    const tr = document.createElement("tr");
    tr.innerHTML = `
      <td>${escapeHtml(d.documentTitle)}</td>
      <td>${escapeHtml(d.documentsType)}</td>
      <td>${escapeHtml(d.uploadDate || "—")}</td>
      <td>${link}</td>`;
    referenceBody.appendChild(tr);
  }
}

const trashIcon = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="square" stroke-linejoin="miter" aria-hidden="true"><path d="M3 6h18"/><path d="M8 6V4h8v2"/><path d="M19 6v14H5V6"/><path d="M10 11v6"/><path d="M14 11v6"/></svg>`;

function trashButton(attrs, label = "Remove") {
  return `<button type="button" class="btn-trash" title="${escapeHtml(label)}" aria-label="${escapeHtml(label)}" ${attrs}>${trashIcon}</button>`;
}

function canManageSubHeaders() {
  return window.__USER_ROLE__ === "admin";
}

// Entry edit / add-attachment: the entry's own logger, or an admin — same
// ownership rule the server enforces in UpdateSubHeaderEntry/AddSubHeaderEntryEvidence.
function canEditEntry(e) {
  return window.__USER_ROLE__ === "admin" || Number(window.__USER_STAFF_ID__) === Number(e.loggedByStaffId);
}

function wirePhotoThumbs(root) {
  root.querySelectorAll(".photo-thumb").forEach((btn) => {
    btn.addEventListener("click", () => {
      lightboxImg.src = btn.dataset.url;
      lightbox.showModal();
    });
  });
}

async function loadSubHeaders() {
  if (!selectedId || selectedId === "home" || !subHeaderListEl) return;

  const res = await fetch(`/api/programs/${selectedId}/subheaders`, { credentials: "same-origin" });
  if (res.status === 401) {
    location.href = "/";
    return;
  }
  if (!res.ok) return;
  const subHeaders = await res.json();
  subHeaderListEl.innerHTML = "";
  subHeaderEmptyEl.hidden = subHeaders.length > 0;

  for (const s of subHeaders) {
    const row = document.createElement("div");
    row.className = "settings-row";
    const nextStatus = s.status === "Active" ? "Archived" : "Active";
    const archiveLabel = s.status === "Active" ? "Archive" : "Restore";
    row.innerHTML = `
      <button type="button" class="app-program-item" data-select-sh="${s.subHeaderId}" data-name="${escapeHtml(s.subHeaderName)}" style="flex:1;padding:6px 8px">
        <span class="app-program-item-title">${escapeHtml(s.subHeaderName)}</span>
        ${s.status === "Archived" ? `<span class="app-program-item-meta">Archived</span>` : ""}
        ${s.auditLocked ? `<span class="app-program-item-meta" title="Entries here can be created but never edited or deleted">🔒 Audit-locked</span>` : ""}
      </button>
      ${
        canManageSubHeaders()
          ? `<div class="settings-row-actions">
              ${s.auditLocked ? "" : `<button type="button" data-sh-lock="${s.subHeaderId}" class="btn-ghost">Lock</button>`}
              <button type="button" data-sh-status="${s.subHeaderId}" data-next="${nextStatus}" class="btn-ghost">${archiveLabel}</button>
              ${trashButton(`data-del-sh="${s.subHeaderId}" data-sh-name="${escapeHtml(s.subHeaderName)}"`, "Delete sub-header")}
            </div>`
          : ""
      }`;
    subHeaderListEl.appendChild(row);
  }

  subHeaderListEl.querySelectorAll("[data-select-sh]").forEach((btn) => {
    btn.addEventListener("click", () => showSubHeaderEntries(btn.dataset.selectSh, btn.dataset.name));
  });
  subHeaderListEl.querySelectorAll("[data-sh-lock]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      if (!confirm("Lock this sub-header? Its entries will be creatable but can never be edited or deleted afterward.")) return;
      const r = await fetch(`/api/subheaders/${btn.dataset.shLock}`, {
        method: "PUT",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ auditLocked: true }),
      });
      if (!r.ok) {
        const d = await r.json().catch(() => ({}));
        alert(d.error || "Failed");
        return;
      }
      loadSubHeaders();
    });
  });
  subHeaderListEl.querySelectorAll("[data-sh-status]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const r = await fetch(`/api/subheaders/${btn.dataset.shStatus}`, {
        method: "PUT",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ status: btn.dataset.next }),
      });
      if (!r.ok) {
        const d = await r.json().catch(() => ({}));
        alert(d.error || "Failed");
        return;
      }
      loadSubHeaders();
    });
  });
  subHeaderListEl.querySelectorAll("[data-del-sh]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const name = btn.dataset.shName || "this sub-header";
      if (!confirm(`Permanently delete "${name}"?\n\nAll of its logged entries will be deleted too.\n\nThis cannot be undone.`)) return;
      const r = await fetch(`/api/subheaders/${btn.dataset.delSh}`, {
        method: "DELETE",
        credentials: "same-origin",
      });
      const d = await r.json().catch(() => ({}));
      if (!r.ok) {
        alert(d.error || "Failed");
        return;
      }
      loadSubHeaders();
    });
  });

  if (expandedPrograms.has(selectedId)) renderSidebar();
}


function renderEntryCard(e) {
  const photos = (e.photos || [])
    .map(
      (p) =>
        `<button type="button" data-url="${escapeHtml(p.url)}" class="photo-thumb">
          <img src="${escapeHtml(p.url)}" alt="" loading="lazy" title="${escapeHtml(uploaderCaption(p))}" />
        </button>`
    )
    .join("");
  const docs = (e.documents || []).map(renderDocLink).join("");
  // Locked entries are create-only — no edit/delete/add-evidence control is
  // ever shown for them, matching the server's own enforcement (see
  // subHeaderLocked's callers in subheaders.go).
  const canEdit = canEditEntry(e) && !e.locked;

  const card = document.createElement("article");
  card.className = "app-post";
  card.innerHTML = `
    <div class="app-post-body">
      <div class="app-post-head">
        <div class="app-post-date">
          ${e.code ? `<strong>${escapeHtml(e.code)}</strong> · ` : ""}${escapeHtml(e.entryDate || "No date")}${e.staffName ? ` · Logged by ${escapeHtml(e.staffName)}` : ""}
          ${e.locked ? ` · <span title="This entry cannot be edited or deleted">🔒 locked</span>` : ""}
        </div>
        <div style="display:flex;gap:6px">
          ${canEdit ? `<button type="button" class="btn-ghost" data-edit-entry="${e.entryId}">Edit</button>` : ""}
          ${canEdit ? `<button type="button" class="btn-ghost" data-add-evidence="${e.entryId}">Add photo/doc</button>` : ""}
          ${canEdit ? trashButton(`data-del-entry="${e.entryId}"`, "Delete entry") : ""}
        </div>
      </div>
      <h3 class="app-post-title">${escapeHtml(e.title)}</h3>
      ${activitySummaryMarkup(e.description)}
      ${e.formLink ? `<p><a href="${escapeHtml(e.formLink)}" target="_blank" rel="noopener">Open link</a></p>` : ""}
      ${e.submissionId ? `<p class="app-empty">Linked to test log record #${e.submissionId}</p>` : ""}
    </div>
    ${photos ? `<div class="app-photo-grid">${photos}</div>` : ""}
    ${docs ? `<div class="app-doc-grid">${docs}</div>` : ""}
    <form class="add-activity" data-evidence-form="${e.entryId}" hidden>
      <div class="settings-grid">
        <label>
          <span>Type</span>
          <select name="evidenceType">
            <option value="Photos">Photo</option>
            <option value="Document">Document</option>
          </select>
        </label>
        <label>
          <span>File</span>
          <input type="file" name="file" required />
        </label>
      </div>
      <div style="margin-top:12px;display:flex;gap:8px">
        <button type="submit" class="btn-primary">Upload</button>
        <button type="button" class="btn-ghost" data-cancel-evidence>Cancel</button>
      </div>
    </form>`;
  return card;
}

async function loadSubHeaderEntries() {
  if (!selectedSubHeaderId) return;
  const res = await fetch(`/api/subheaders/${selectedSubHeaderId}/entries`, { credentials: "same-origin" });
  if (res.status === 401) {
    location.href = "/";
    return;
  }
  if (!res.ok) return;
  const entries = await res.json();
  subHeaderEntriesEl.innerHTML = "";
  subHeaderEntriesEmptyEl.hidden = entries.length > 0;

  for (const e of entries) {
    subHeaderEntriesEl.appendChild(renderEntryCard(e));
  }
  wirePhotoThumbs(subHeaderEntriesEl);

  subHeaderEntriesEl.querySelectorAll("[data-del-entry]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      if (!confirm("Delete this entry? This cannot be undone.")) return;
      const r = await fetch(`/api/subheader-entries/${btn.dataset.delEntry}`, {
        method: "DELETE",
        credentials: "same-origin",
      });
      if (!r.ok) {
        const d = await r.json().catch(() => ({}));
        alert(d.error || "Failed");
        return;
      }
      loadSubHeaderEntries();
    });
  });

  subHeaderEntriesEl.querySelectorAll("[data-edit-entry]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const entry = entries.find((e) => String(e.entryId) === btn.dataset.editEntry);
      if (!entry) return;
      const title = prompt("Title:", entry.title);
      if (title === null) return;
      const trimmedTitle = title.trim();
      if (!trimmedTitle) {
        alert("Title is required");
        return;
      }
      const description = prompt("Description:", entry.description || "");
      if (description === null) return;
      const entryDate = prompt("Date (YYYY-MM-DD):", entry.entryDate || "");
      if (entryDate === null) return;
      const formLink = prompt("Link (optional):", entry.formLink || "");
      if (formLink === null) return;
      const r = await fetch(`/api/subheader-entries/${entry.entryId}`, {
        method: "PUT",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          title: trimmedTitle,
          description: description.trim(),
          entryDate: entryDate.trim(),
          formLink: formLink.trim(),
        }),
      });
      const d = await r.json().catch(() => ({}));
      if (!r.ok) {
        alert(d.error || "Failed");
        return;
      }
      loadSubHeaderEntries();
    });
  });

  subHeaderEntriesEl.querySelectorAll("[data-add-evidence]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const form = subHeaderEntriesEl.querySelector(`[data-evidence-form="${btn.dataset.addEvidence}"]`);
      if (form) form.hidden = !form.hidden;
    });
  });
  subHeaderEntriesEl.querySelectorAll("[data-cancel-evidence]").forEach((btn) => {
    btn.addEventListener("click", () => {
      btn.closest("[data-evidence-form]").hidden = true;
    });
  });
  subHeaderEntriesEl.querySelectorAll("[data-evidence-form]").forEach((form) => {
    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      const entryId = form.dataset.evidenceForm;
      const fd = new FormData(form);
      const r = await fetch(`/api/subheader-entries/${entryId}/evidence`, {
        method: "POST",
        credentials: "same-origin",
        body: fd,
      });
      const d = await r.json().catch(() => ({}));
      if (!r.ok) {
        alert(d.error || "Upload failed");
        return;
      }
      loadSubHeaderEntries();
    });
  });
}

addSubHeaderForm?.addEventListener("submit", async (e) => {
  e.preventDefault();
  if (!selectedId || selectedId === "home") return;
  const fd = new FormData(addSubHeaderForm);
  const res = await fetch(`/api/programs/${selectedId}/subheaders`, {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ subHeaderName: fd.get("subHeaderName"), auditLocked: fd.get("auditLocked") === "on" }),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    alert(data.error || "Failed");
    return;
  }
  addSubHeaderForm.reset();
  loadSubHeaders();
});

addSubHeaderEntryForm?.addEventListener("submit", async (e) => {
  e.preventDefault();
  if (!selectedSubHeaderId) return;
  subHeaderEntryMsg.textContent = "Saving…";
  const fd = new FormData(addSubHeaderEntryForm);
  const hasPhotos = fd.getAll("photos").some((f) => f.size > 0);
  if (!hasPhotos) fd.delete("photos");
  const hasDocument = fd.get("document") && fd.get("document").size > 0;
  if (!hasDocument) fd.delete("document");
  const res = await fetch(`/api/subheaders/${selectedSubHeaderId}/entries`, {
    method: "POST",
    credentials: "same-origin",
    body: fd,
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    subHeaderEntryMsg.textContent = data.error || "Could not save entry";
    return;
  }
  addSubHeaderEntryForm.reset();
  addSubHeaderEntryForm.hidden = true;
  subHeaderEntryMsg.textContent = "";
  loadSubHeaderEntries();
});

async function loadActivities() {
  if (!selectedId) {
    programTabs.hidden = true;
    titleEl.textContent = "Select a program";
    subEl.textContent = "Choose a health program on the left to view activities, photos, and summaries.";
    filterEl.hidden = true;
    addForm.hidden = true;
    groupsEl.innerHTML = "";
    emptyEl.hidden = false;
    emptyEl.textContent = "No program selected yet.";
    return;
  }

  const fd = new FormData(filterEl);
  const from = String(fd.get("from") || "");
  const to = String(fd.get("to") || "");
  const q = new URLSearchParams();
  if (from) q.set("from", from);
  if (to) q.set("to", to);

  if (selectedId === "home") {
    programTabs.hidden = true;
    const res = await fetch(`/api/activities-view?${q}`, { credentials: "same-origin" });
    if (res.status === 401) {
      location.href = "/";
      return;
    }
    if (!res.ok) {
      emptyEl.hidden = false;
      emptyEl.textContent = "Could not load activities.";
      groupsEl.innerHTML = "";
      return;
    }
    renderHomeFeed(await res.json());
    return;
  }

  programTabs.hidden = false;
  toggleAdd.hidden = false;
  if (downloadAll) downloadAll.hidden = false;
  if (downloadAllUploads) {
    downloadAllUploads.hidden = true;
    downloadAllUploadsMsg.hidden = true;
  }
  if (downloadAllEntries) downloadAllEntries.hidden = true;
  const res = await fetch(`/api/programs/${selectedId}/activities-view?${q}`, {
    credentials: "same-origin",
  });
  if (res.status === 401) {
    location.href = "/";
    return;
  }
  if (!res.ok) {
    emptyEl.hidden = false;
    emptyEl.textContent = "Could not load activities.";
    groupsEl.innerHTML = "";
    return;
  }
  renderFeed(await res.json());
  if (activeTab === "reference") loadReferences();
}

async function selectProgram(id, pushUrl) {
  await runScreenChange(async () => {
    selectedId = String(id);
    renderSidebar();
    addForm.hidden = true;
    addMsg.textContent = "";
    await showTab("activities");
    if (pushUrl) {
      history.pushState({ procId: selectedId }, "", `/programs/${selectedId}`);
    }
    await loadActivities();
  });
}

function selectHome(pushUrl) {
  return runScreenChange(async () => {
    selectedId = "home";
    renderSidebar();
    addForm.hidden = true;
    addMsg.textContent = "";
    await showTab("activities");
    if (pushUrl) {
      history.pushState({ procId: "home" }, "", `/programs`);
    }
    await loadActivities();
  });
}

homeNavItem?.addEventListener("click", () => selectHome(true));

filterEl.addEventListener("submit", (e) => {
  e.preventDefault();
  loadActivities();
});

document.getElementById("reset-filter")?.addEventListener("click", () => {
  filterEl.reset();
  loadActivities();
});

toggleAdd?.addEventListener("click", () => {
  if (!selectedId) return;
  addForm.hidden = !addForm.hidden;
  if (!addForm.hidden) {
    const dateInput = addForm.querySelector('[name="activityDate"]');
    if (dateInput && !dateInput.value) {
      dateInput.value = clinicToday();
    }
  }
});

// saveBlob writes a downloaded blob to disk. Chromium supports a native
// "Save As" picker that bypasses the browser's silent-download setting.
// Firefox/Safari don't implement it - fall back to a plain <a download>,
// which saves to the browser's default location.
async function saveBlob(blob, filename) {
  if (window.showSaveFilePicker) {
    try {
      const handle = await window.showSaveFilePicker({
        suggestedName: filename,
        types: [{ description: "Zip archive", accept: { "application/zip": [".zip"] } }],
      });
      const writable = await handle.createWritable();
      await writable.write(blob);
      await writable.close();
      return;
    } catch (err) {
      if (err && err.name === "AbortError") return; // user cancelled the save dialog
      // fall through to the plain-download fallback below
    }
  }

  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

downloadAll?.addEventListener("click", async (e) => {
  e.preventDefault();
  if (!selectedId || selectedId === "home") return;
  const fd = new FormData(filterEl);
  const q = new URLSearchParams();
  const from = String(fd.get("from") || "");
  const to = String(fd.get("to") || "");
  if (from) q.set("from", from);
  if (to) q.set("to", to);

  const res = await fetch(`/api/programs/${selectedId}/activities/download?${q}`, {
    credentials: "same-origin",
  });
  if (res.status === 401) {
    location.href = "/";
    return;
  }
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    alert(data.error || "Could not download activities.");
    return;
  }

  const disposition = res.headers.get("Content-Disposition") || "";
  const match = disposition.match(/filename="([^"]+)"/);
  const filename = match ? match[1] : "activities.zip";
  await saveBlob(await res.blob(), filename);
});

function formatBytes(n) {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

let activeUploadsDownload = null;

async function downloadUploadsBatch(index, total, btn) {
  btn.disabled = true;
  const original = btn.textContent;
  btn.textContent = "Downloading…";
  const controller = new AbortController();
  activeUploadsDownload = controller;
  try {
    const res = await fetch(`/api/uploads/download-all?batch=${index}`, {
      credentials: "same-origin",
      signal: controller.signal,
    });
    if (res.status === 401) {
      location.href = "/";
      return;
    }
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      alert(data.error || "Could not download uploads.");
      return;
    }
    const disposition = res.headers.get("Content-Disposition") || "";
    const match = disposition.match(/filename="([^"]+)"/);
    const filename = match ? match[1] : `uploads-part-${index}-of-${total}.zip`;
    await saveBlob(await res.blob(), filename);
    btn.textContent = "Downloaded ✓";
  } catch (err) {
    if (err && err.name === "AbortError") return;
    alert("Could not download uploads.");
  } finally {
    if (activeUploadsDownload === controller) activeUploadsDownload = null;
    btn.disabled = false;
    if (btn.textContent === "Downloading…") btn.textContent = original;
  }
}

downloadAllUploads?.addEventListener("click", async (e) => {
  e.preventDefault();
  downloadAllUploads.textContent = "Checking…";
  try {
    const res = await fetch("/api/uploads/download-all/info", { credentials: "same-origin" });
    if (res.status === 401) {
      location.href = "/";
      return;
    }
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      alert(data.error || "Could not check uploads.");
      return;
    }
    const data = await res.json();
    const batches = data.batches || [];
    if (batches.length === 0) {
      alert("No uploaded files yet.");
      return;
    }
    if (batches.length === 1) {
      await downloadUploadsBatch(1, 1, downloadAllUploads);
      return;
    }
    downloadAllUploadsMsg.innerHTML = `<p>Uploads are too large for one zip (${formatBytes(data.totalBytes)} across ${data.totalFiles} files) — split into ${batches.length} parts by health program:</p>`;
    const list = document.createElement("div");
    list.className = "app-download-batch-list";
    for (const b of batches) {
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = "btn-ghost";
      btn.textContent = `Part ${b.index}: ${b.programTitles.join(", ")} (${formatBytes(b.bytes)})`;
      btn.addEventListener("click", () => downloadUploadsBatch(b.index, batches.length, btn));
      list.appendChild(btn);
    }
    downloadAllUploadsMsg.appendChild(list);
    const cancel = document.createElement("button");
    cancel.type = "button";
    cancel.className = "btn-ghost";
    cancel.textContent = "Cancel";
    cancel.addEventListener("click", () => {
      activeUploadsDownload?.abort();
      activeUploadsDownload = null;
      downloadAllUploadsMsg.hidden = true;
      downloadAllUploadsMsg.replaceChildren();
    });
    downloadAllUploadsMsg.appendChild(cancel);
    downloadAllUploadsMsg.hidden = false;
  } finally {
    downloadAllUploads.textContent = "Download all uploads (zip)";
  }
});

cancelAdd?.addEventListener("click", () => {
  addForm.hidden = true;
  addMsg.textContent = "";
});

addForm?.addEventListener("submit", async (e) => {
  e.preventDefault();
  if (!selectedId || selectedId === "home") return;
  const editor = addForm.querySelector(".summary-doc-body");
  const summary = addForm.querySelector('[name="activitySummary"]');
  if (editor && summary) summary.value = serializeSummary(editor);
  addMsg.textContent = "Saving…";
  const fd = new FormData(addForm);
  const res = await fetch(`/api/programs/${selectedId}/activities`, {
    method: "POST",
    credentials: "same-origin",
    body: fd,
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    addMsg.textContent = data.error || "Could not save activity";
    return;
  }
  addForm.reset();
  addForm.hidden = true;
  addMsg.textContent = "";
  loadActivities();
});

window.addEventListener("popstate", () => {
  runScreenChange(async () => {
    const m = location.pathname.match(/^\/programs\/(\d+)/);
    selectedId = m ? m[1] : "home";
    renderSidebar();
    await showTab("activities");
    await loadActivities();
  });
});

refreshPresence = startPresence(() => activitySection);

async function boot() {
  const res = await fetch("/api/procedures", { credentials: "same-origin" });
  if (res.status === 401) {
    location.href = "/";
    return;
  }
  programs = await res.json();
  renderSidebar();
  await loadActivities();
}

boot();
