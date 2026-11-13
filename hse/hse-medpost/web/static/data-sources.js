// Settings > Region & malaria data. Talks to /api/data-sync (internal/datasync/web.go).
// Everything that comes from the server is written with textContent, never innerHTML, because the
// scraped region and regency names are data from a third-party site.
(function () {
  "use strict";
  const panel = document.querySelector('.settings-panel[data-panel="data-sources"]');
  if (!panel) return;

  const MONTHS = { 1: "month", 3: "3 months", 6: "6 months", 12: "12 months" };
  const $ = (id) => document.getElementById(id);

  // The map's "Eliminasi" means the regency has reached malaria elimination, i.e. it is free of
  // malaria. It is stored exactly as published, and only shown with this plainer name (owner,
  // 2026-11-13). Every other label is shown as published.
  const FREE_LABEL = "Free of malaria (Eliminasi)";
  const shown = (label) => (label === "Eliminasi" ? FREE_LABEL : label);

  function say(id, text, isError) {
    const el = $(id);
    el.textContent = text || "";
    el.classList.toggle("error", !!isError);
  }

  // "2026-11-13T10:00:00" (UTC) -> "13 Nov 2026" in the viewer's locale and zone.
  function day(stamp) {
    const d = new Date(stamp + "Z");
    return isNaN(d) ? stamp : d.toLocaleDateString("en-GB", { day: "numeric", month: "short", year: "numeric" });
  }

  async function call(method, url, body) {
    const res = await fetch(url, {
      method,
      credentials: "same-origin",
      headers: body ? { "Content-Type": "application/json" } : undefined,
      body: body ? JSON.stringify(body) : undefined,
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || "Request failed (" + res.status + ")");
    return data;
  }

  function fillForm(source, v, intervals) {
    const form = panel.querySelector('.ds-form[data-source="' + source + '"]');
    form.querySelector(".ds-enabled").checked = v.enabled;
    const select = form.querySelector(".ds-interval");
    if (!select.options.length) {
      for (const m of intervals) select.add(new Option(MONTHS[m] || m + " months", String(m)));
    }
    select.value = String(v.intervalMonths);
  }

  function statusLine(v) {
    if (!v.enabled) return "Automatic refresh is off.";
    const last = v.lastRunAt ? "Last successful run: " + day(v.lastRunAt) + "." : "Never run.";
    const next = v.nextRun ? " Next: " + day(v.nextRun) + "." : " Due at the next hourly check.";
    return last + next;
  }

  function render(o) {
    fillForm("region", o.region, o.intervals);
    fillForm("endemicity", o.endemicity, o.intervals);
    $("ds-region-status").textContent = statusLine(o.region);
    $("ds-endemicity-status").textContent = statusLine(o.endemicity);
    // The last result: a failure stays visible until the next success.
    say("ds-region-msg", o.region.lastMessage, o.region.lastStatus === "error");
    say("ds-endemicity-msg", o.endemicity.lastMessage, o.endemicity.lastStatus === "error");

    const pending = $("ds-pending");
    pending.hidden = !o.pending;
    if (o.pending) {
      $("ds-pending-summary").textContent =
        o.pending + " regencies were read. " + o.changes.length + " differ from the applied list.";
      const body = $("ds-changes");
      body.replaceChildren();
      for (const c of o.changes) {
        const tr = body.insertRow();
        const proposed = c.new ? shown(c.new) + (c.newYear ? " (" + c.newYear + ")" : "") : "removed";
        for (const text of [c.name, c.province, c.old ? shown(c.old) : "not listed", proposed]) tr.insertCell().textContent = text;
      }
    }

    const labels = o.labels.filter((l) => o.counts[l]).map((l) => shown(l) + ": " + o.counts[l]);
    $("ds-applied-summary").textContent = o.live
      ? o.live + " regencies applied" + (o.endemicity.appliedAt ? " on " + day(o.endemicity.appliedAt) : "") + ". " + labels.join(", ") + "."
      : "Nothing applied yet. Read the map, review it, then apply.";
    $("ds-read-date").textContent = o.endemicity.lastRunAt ? "read " + day(o.endemicity.lastRunAt) : "not read yet";
    const fb = $("ds-fallback-note");
    fb.hidden = !o.fallbackRows;
    fb.textContent = o.fallbackRows
      ? o.fallbackRows + " regencies are missing from the newest map year, so their labels from the previous year are used."
      : "";
  }

  async function load() {
    try {
      render(await call("GET", "/api/data-sync"));
    } catch (e) {
      say("ds-region-msg", e.message, true);
    }
  }

  panel.addEventListener("click", async (e) => {
    const form = e.target.closest(".ds-form");
    const button = e.target.closest("button");
    if (!button) return;
    try {
      if (form && button.classList.contains("ds-save")) {
        const source = form.dataset.source;
        await call("PUT", "/api/data-sync/" + source, {
          enabled: form.querySelector(".ds-enabled").checked,
          intervalMonths: Number(form.querySelector(".ds-interval").value),
        });
        say("ds-" + source + "-msg", "Saved.");
        await load();
      } else if (form && button.classList.contains("ds-run")) {
        const source = form.dataset.source;
        button.disabled = true;
        say("ds-" + source + "-msg", "Working… this can take a minute.");
        try {
          const data = await call("POST", "/api/data-sync/" + source + "/run");
          say("ds-" + source + "-msg", data.message);
        } finally {
          button.disabled = false;
        }
        await load();
      } else if (button.id === "ds-apply") {
        if (!confirm("Apply the proposed malaria endemicity? It replaces the applied list.")) return;
        await call("POST", "/api/data-sync/endemicity/apply");
        say("ds-endemicity-msg", "Applied.");
        await load();
      } else if (button.id === "ds-discard") {
        await call("POST", "/api/data-sync/endemicity/discard");
        say("ds-endemicity-msg", "Discarded.");
        await load();
      }
    } catch (err) {
      const id = form ? "ds-" + form.dataset.source + "-msg" : "ds-endemicity-msg";
      say(id, err.message, true);
      button.disabled = false;
    }
  });

  load();
})();
