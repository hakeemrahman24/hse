async function logout() {
  await fetch("/api/logout", { method: "POST", credentials: "same-origin" });
  location.href = "/";
}

document.getElementById("logout")?.addEventListener("click", logout);

async function load() {
  const res = await fetch("/api/procedures", { credentials: "same-origin" });
  if (res.status === 401) {
    location.href = "/";
    return;
  }
  const programs = await res.json();
  const list = document.getElementById("program-list");
  list.innerHTML = "";
  for (const p of programs) {
    const title = p.title || p.healthProcedure;
    const li = document.createElement("li");
    li.innerHTML = `<a href="/programs/${p.procId}" class="flex items-center gap-3 rounded-lg px-2 py-2.5 transition hover:bg-panel-2">
      <span class="fb-avatar text-xs">${escapeHtml(title.slice(0, 1))}</span>
      <span class="min-w-0 flex-1">
        <span class="block truncate font-semibold text-chrome">${escapeHtml(title)}</span>
        <span class="block text-xs text-silver-dim">View activity feed</span>
      </span>
    </a>`;
    list.appendChild(li);
  }
}

function escapeHtml(s) {
  return String(s)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

load();
