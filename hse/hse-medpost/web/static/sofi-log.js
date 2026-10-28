// Medical Record > Screening > Fatigue > SOFI tab: the questionnaires employees submitted at /sofi.
// Reads GET /api/sofi-log (staff sign-in); the scores and levels come from the server.
(function () {
  "use strict";

  var SEARCH_DELAY_MS = 250;
  var ROUND_ID = "sofi-round"; // this tab's Period dropdown (sofi-rounds.js)
  var tab = document.getElementById("scr-tab-sofi");
  if (!tab) { return; }

  var searchEl = document.getElementById("sofi-search");
  var bodyEl = document.getElementById("sofi-body");
  var emptyEl = document.getElementById("sofi-empty");
  var errorEl = document.getElementById("sofi-error");
  var truncatedEl = document.getElementById("sofi-truncated");
  var timer = null;
  var seq = 0;

  function cell(text, className) {
    var td = document.createElement("td");
    td.textContent = text;
    if (className) { td.className = className; }
    return td;
  }

  function two(n) { return (n < 10 ? "0" : "") + n; }

  var GMT9_MS = 9 * 60 * 60 * 1000;

  // "2026-10-21T07:30:00" is UTC; show it in GMT+9 (the site's time, the same as the SOFI
  // schedule) as dd/mm/yyyy hh:mm, whatever the viewer's own time zone is.
  function showTime(utc) {
    var d = new Date(utc + "Z");
    if (isNaN(d.getTime())) { return utc; }
    var s = new Date(d.getTime() + GMT9_MS);
    return two(s.getUTCDate()) + "/" + two(s.getUTCMonth() + 1) + "/" + s.getUTCFullYear() + " " + two(s.getUTCHours()) + ":" + two(s.getUTCMinutes());
  }

  function scoreCell(d) {
    var td = cell(d.score + " / " + d.max);
    td.classList.add("sofi-cell-" + d.level.toLowerCase());
    td.title = d.level;
    return td;
  }

  // The server shows the newest rows only; say so, with the real total, so the table is not mistaken
  // for everything (the dashboard and the PDF count every survey).
  function showTruncation(shown, total) {
    var more = total > shown;
    truncatedEl.hidden = !more;
    truncatedEl.textContent = more ? "Showing the newest " + shown + " of " + total + " questionnaires. Narrow the search or pick a period to see the rest." : "";
  }

  function render(rows) {
    bodyEl.textContent = "";
    emptyEl.hidden = rows.length > 0;
    rows.forEach(function (r) {
      var tr = document.createElement("tr");
      tr.appendChild(cell(showTime(r.submittedAt)));
      tr.appendChild(cell(r.name));
      tr.appendChild(cell(r.workPosition || "—"));
      tr.appendChild(cell(r.company || "—"));
      tr.appendChild(cell(r.age ? String(r.age) : "—"));
      tr.appendChild(cell(r.weeksOverYear ? "> 1 year" : String(r.weeksOnSite))); // the marker 54 is not 54 weeks
      tr.appendChild(cell(r.mcuReviewStatus));
      r.dimensions.forEach(function (d) { tr.appendChild(scoreCell(d)); });
      tr.appendChild(cell(r.total + " / " + r.totalMax));
      var result = cell(r.level, "sofi-cell-" + r.level.toLowerCase());
      result.classList.add("sofi-cell-result");
      tr.appendChild(result);
      bodyEl.appendChild(tr);
    });
  }

  function load() {
    var mine = ++seq;
    var params = new URLSearchParams();
    if (searchEl.value.trim()) { params.set("q", searchEl.value.trim()); }
    if (window.SofiRounds && window.SofiRounds.param(ROUND_ID)) { params.set("window", window.SofiRounds.param(ROUND_ID)); }
    fetch("/api/sofi-log?" + params, { credentials: "same-origin" })
      .then(function (res) {
        if (res.status === 401) { location.href = "/"; return Promise.reject(new Error("signed out")); }
        var total = parseInt(res.headers.get("X-Total-Count"), 10);
        return res.json().then(function (data) {
          if (!res.ok) { throw new Error(data.error || "Something went wrong (" + res.status + ")"); }
          return { rows: data, total: isNaN(total) ? data.length : total };
        });
      })
      .then(function (result) {
        if (mine !== seq) { return; } // a newer search is already on its way
        errorEl.hidden = true;
        render(result.rows);
        showTruncation(result.rows.length, result.total);
      })
      .catch(function (err) {
        if (mine !== seq) { return; }
        errorEl.textContent = err.message;
        errorEl.hidden = false;
      });
  }

  tab.addEventListener("click", load);
  document.addEventListener("sofi-round-change", function (e) {
    if (e.detail.id === ROUND_ID) { load(); }
  });
  searchEl.addEventListener("input", function () {
    clearTimeout(timer);
    timer = setTimeout(load, SEARCH_DELAY_MS);
  });
})();
