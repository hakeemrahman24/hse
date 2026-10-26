// Medical Record > Screening > Fatigue > Dashboard: the SOFI Fatigue Survey section under the
// sleep-score charts. Reads GET /api/sofi-log/summary (staff sign-in) with the dashboard's own
// From / To dates and its "Chart by" choice. The counts and levels come from the server.
(function () {
  "use strict";

  var LEVELS = ["low", "moderate", "high"];
  var SVG_NS = "http://www.w3.org/2000/svg";

  var block = document.getElementById("sofi-dash");
  var sofiTab = document.getElementById("scr-tab-sofi");
  if (!block || !sofiTab) { return; }

  var statsEl = document.getElementById("sofi-stats");
  var dimEl = document.getElementById("sofi-dim-bars");
  var groupEl = document.getElementById("sofi-group-bars");
  var groupTitle = document.getElementById("sofi-group-title");
  var groupHint = document.getElementById("sofi-group-hint");
  var emptyEl = document.getElementById("sofi-dash-empty");
  var errorEl = document.getElementById("sofi-dash-error");
  var fromEl = document.getElementById("scr-dash-from");
  var toEl = document.getElementById("scr-dash-to");
  var byEl = document.getElementById("scr-dash-by");
  var seq = 0;

  function tile(label, value, className) {
    var box = document.createElement("div");
    box.className = "mal-stat" + (className ? " " + className : "");
    var caption = document.createElement("span");
    caption.textContent = label;
    var number = document.createElement("strong");
    number.textContent = String(value);
    box.append(caption, number);
    return box;
  }

  // One stacked bar: a segment per band, as wide as its share of max.
  function stack(results, max) {
    var svg = document.createElementNS(SVG_NS, "svg");
    svg.setAttribute("viewBox", "0 0 100 12");
    svg.setAttribute("preserveAspectRatio", "none");
    svg.setAttribute("aria-hidden", "true");
    var x = 0;
    LEVELS.forEach(function (level) {
      var width = max > 0 ? ((results[level] || 0) / max) * 100 : 0;
      if (width === 0) { return; }
      var rect = document.createElementNS(SVG_NS, "rect");
      rect.setAttribute("class", "scr-bar-" + level);
      rect.setAttribute("x", String(x));
      rect.setAttribute("y", "0");
      rect.setAttribute("width", String(Math.min(width, 100 - x)));
      rect.setAttribute("height", "12");
      svg.appendChild(rect);
      x += width;
    });
    return svg;
  }

  function row(label, results, max, valueText) {
    var line = document.createElement("div");
    line.className = "dash-bar-row";
    var name = document.createElement("span");
    name.textContent = label; // textContent, never innerHTML: names are data
    name.title = label;
    var value = document.createElement("b");
    value.textContent = valueText;
    line.append(name, stack(results, max), value);
    return line;
  }

  function bars(rows) {
    var wrap = document.createElement("div");
    wrap.className = "dash-bars";
    rows.forEach(function (r) { wrap.appendChild(r); });
    return wrap;
  }

  // "3 (High 1)": the count, then the surveys that came out High, like the other charts.
  function countText(g) {
    return g.results.high > 0 ? g.count + " (High " + g.results.high + ")" : String(g.count);
  }

  function draw(data) {
    statsEl.replaceChildren(
      tile("Surveys done", data.total),
      tile("People", data.people),
      tile("Low", data.results.low, "scr-stat--low"),
      tile("Moderate", data.results.moderate, "scr-stat--moderate"),
      tile("High", data.results.high, "scr-stat--high"),
    );
    dimEl.replaceChildren(bars(data.dimensions.map(function (d) {
      return row(d.en, d.results, data.total, "avg " + d.average.toFixed(1) + " / " + d.max);
    })));

    var by = data.by === "position" ? "position" : "company";
    groupTitle.textContent = "Surveys by " + by;
    groupHint.textContent = data.moreGroups > 0 ? "Showing the " + data.groups.length + " busiest; " + data.moreGroups + " more not shown." : "";
    var max = Math.max(0, ...data.groups.map(function (g) { return g.count; }));
    groupEl.replaceChildren();
    emptyEl.hidden = data.groups.length > 0;
    if (data.groups.length > 0) {
      groupEl.appendChild(bars(data.groups.map(function (g) { return row(g.name || "—", g.results, max, countText(g)); })));
    }
  }

  function refresh() {
    // The SOFI tab is shown for Fatigue only, so it tells which test the panel is on.
    block.hidden = sofiTab.hidden;
    if (block.hidden) { return; }
    var mine = ++seq;
    var params = new URLSearchParams();
    if (fromEl.value) { params.set("from", fromEl.value); }
    if (toEl.value) { params.set("to", toEl.value); }
    params.set("by", byEl.value === "position" ? "position" : "company");
    fetch("/api/sofi-log/summary?" + params, { credentials: "same-origin" })
      .then(function (res) {
        if (res.status === 401) { location.href = "/"; return Promise.reject(new Error("signed out")); }
        return res.json().then(function (data) {
          if (!res.ok) { throw new Error(data.error || "Something went wrong (" + res.status + ")"); }
          return data;
        });
      })
      .then(function (data) {
        if (mine !== seq) { return; } // a newer request is already on its way
        errorEl.hidden = true;
        draw(data);
      })
      .catch(function (err) {
        if (mine !== seq) { return; }
        errorEl.textContent = err.message;
        errorEl.hidden = false;
      });
  }

  // The same moments screening-log.js redraws its own charts. Its handlers are registered first,
  // so by the time these run the dates, the "Chart by" choice and the tab state are up to date.
  document.getElementById("scr-tab-dashboard").addEventListener("click", refresh);
  document.getElementById("scr-dash-filter").addEventListener("submit", refresh);
  document.getElementById("scr-dash-reset").addEventListener("click", refresh);
  byEl.addEventListener("change", refresh);
})();
