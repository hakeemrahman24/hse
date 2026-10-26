async function logout() {
  await fetch("/api/logout", { method: "POST", credentials: "same-origin" });
  location.href = "/";
}

document.getElementById("logout")?.addEventListener("click", logout);

const procId = window.__PROC_ID__;
const form = document.getElementById("date-filter");
const groupsEl = document.getElementById("groups");
const emptyEl = document.getElementById("empty");
const titleEl = document.getElementById("program-title");
const lightbox = document.getElementById("lightbox");
const lightboxImg = document.getElementById("lightbox-img");

lightbox?.addEventListener("click", () => lightbox.close());

form.addEventListener("submit", (e) => {
  e.preventDefault();
  load();
});

document.getElementById("reset-filter")?.addEventListener("click", () => {
  form.reset();
  load();
});

function escapeHtml(s) {
  return String(s)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
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

function uploaderCaption(item) {
  const parts = [];
  if (item.uploadedBy) parts.push(`Uploaded by ${item.uploadedBy}`);
  if (item.uploadDate) parts.push(item.uploadDate);
  return parts.join(" · ");
}

function renderDocLink(d) {
  const thumb = d.isPdf
    ? `<embed src="${escapeHtml(d.url)}#page=1" type="application/pdf" class="pointer-events-none h-10 w-8 rounded bg-white object-cover" />`
    : `<span class="flex h-10 w-8 items-center justify-center rounded bg-panel-2 text-[0.6rem] font-bold uppercase text-silver-dim">FILE</span>`;
  const caption = uploaderCaption(d);
  return `<span class="inline-flex items-center gap-2 rounded border border-line bg-panel-2 px-2 py-1 text-xs text-silver">
    <a href="${escapeHtml(d.url)}" target="_blank" rel="noopener" class="flex items-center gap-2 hover:text-chrome" title="View ${escapeHtml(d.filename)}">
      ${thumb}
      <span class="flex min-w-0 flex-col">
        <span class="max-w-40 truncate">${escapeHtml(d.filename)}</span>
        ${caption ? `<span class="max-w-40 truncate text-[0.65rem] text-silver-dim">${escapeHtml(caption)}</span>` : ""}
      </span>
    </a>
    <a href="${escapeHtml(d.url)}" download="${escapeHtml(d.filename)}" class="text-silver-dim hover:text-chrome" title="Download" aria-label="Download">&#8595;</a>
  </span>`;
}

function render(data) {
  titleEl.textContent = data.title || data.healthProcedure;
  groupsEl.innerHTML = "";
  if (!data.activities?.length) {
    emptyEl.classList.remove("hidden");
    return;
  }
  emptyEl.classList.add("hidden");

  const grouped = groupActivities(data.activities);
  for (const [type, byDate] of grouped) {
    const typeLabel = document.createElement("div");
    typeLabel.className = "px-1 pt-2 text-xs font-semibold uppercase tracking-wide text-silver-dim";
    typeLabel.textContent = type;
    groupsEl.appendChild(typeLabel);

    for (const [date, acts] of byDate) {
      for (const a of acts) {
        const photos = (a.photos || [])
          .map(
            (p) =>
              `<button type="button" data-url="${escapeHtml(p.url)}" class="photo-thumb aspect-square overflow-hidden bg-ink-2 focus:outline-none focus:ring-1 focus:ring-silver-dim">
                <img src="${escapeHtml(p.url)}" alt="" class="h-full w-full object-cover transition hover:opacity-90" loading="lazy" />
              </button>`
          )
          .join("");

        const docs = (a.documents || []).map(renderDocLink).join("");

        const post = document.createElement("article");
        post.className = "fb-card overflow-hidden";
        post.innerHTML = `
          <div class="flex items-center gap-3 px-4 pt-4">
            <span class="fb-avatar text-xs">${escapeHtml((a.activityTitle || "?").slice(0, 1))}</span>
            <div class="min-w-0">
              <p class="truncate font-semibold text-chrome">${escapeHtml(a.activityTitle)}</p>
              <p class="text-xs text-silver-dim">${escapeHtml(date)}${a.uploadedBy ? ` · Uploaded by ${escapeHtml(a.uploadedBy)}` : ""} · ${escapeHtml(type)}</p>
            </div>
          </div>
          <div class="px-4 py-3">
            <p class="text-sm leading-relaxed text-silver">${escapeHtml(a.activitySummary || "")}</p>
          </div>
          ${
            photos
              ? `<div class="grid grid-cols-2 gap-0.5 border-t border-line sm:grid-cols-3">${photos}</div>`
              : ""
          }
          ${
            docs
              ? `<div class="flex flex-wrap gap-2 border-t border-line px-4 py-3">${docs}</div>`
              : ""
          }
          <div class="flex border-t border-line text-sm text-silver-dim">
            <span class="flex-1 py-2.5 text-center">${(a.photos || []).length} photos</span>
            <span class="flex-1 py-2.5 text-center">${escapeHtml(a.location || "Medpost")}</span>
          </div>`;
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
}

async function load() {
  const fd = new FormData(form);
  const from = String(fd.get("from") || "");
  const to = String(fd.get("to") || "");
  const q = new URLSearchParams();
  if (from) q.set("from", from);
  if (to) q.set("to", to);
  const res = await fetch(`/api/programs/${procId}/activities-view?${q}`, {
    credentials: "same-origin",
  });
  if (res.status === 401) {
    location.href = "/";
    return;
  }
  if (!res.ok) {
    emptyEl.textContent = "Could not load activities.";
    emptyEl.classList.remove("hidden");
    return;
  }
  render(await res.json());
}

load();
