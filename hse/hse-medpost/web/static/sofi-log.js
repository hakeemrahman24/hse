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
  var exportEl = document.getElementById("sofi-export");
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

  // The server shows the newest rows only; say so, with the real total, so the table is not mistaken
  // for everything (the dashboard and the PDF count every survey).
  function showTruncation(shown, total) {
    var more = total > shown;
    truncatedEl.hidden = !more;
    truncatedEl.textContent = more ? "Showing the newest " + shown + " of " + total + " questionnaires. Narrow the search or pick a period to see the rest." : "";
  }

  // The table keeps the general values only. The name opens the rest of the questionnaire in a row
  // below it, the same way the medical checkup list does (mcu.js): the other rows are hidden while it
  // is open (focus), and a second click closes it and puts the table back where it was. The look comes
  // from the checkup list's own classes (detail-row, ro-grid, ro-value, is-expanded-link), so the
  // fonts and colours match the other pages.
  var TABLE_COLUMNS = 6; // the detail row spans all of them
  var scroller = bodyEl.closest(".table-scroll");
  var open = null; // {link, detail, scrollTop, pageY} of the row being shown, or null

  function field(label, value, levelClass) {
    var wrap = document.createElement("label");
    var caption = document.createElement("span");
    caption.textContent = label;
    var shown = document.createElement("div");
    shown.className = "ro-value" + (levelClass ? " " + levelClass : "");
    shown.textContent = value;
    wrap.append(caption, shown);
    return wrap;
  }

  function detailRow(r) {
    var tr = document.createElement("tr");
    tr.className = "detail-row";
    var td = document.createElement("td");
    td.colSpan = TABLE_COLUMNS;
    var grid = document.createElement("div");
    grid.className = "ro-grid";
    grid.appendChild(field("Age", r.age ? String(r.age) : "—"));
    grid.appendChild(field("Weeks on site", r.weeksOverYear ? "> 1 year" : String(r.weeksOnSite))); // the marker 54 is not 54 weeks
    grid.appendChild(field("MCU review", r.mcuReviewStatus || "—"));
    r.dimensions.forEach(function (d) {
      grid.appendChild(field(d.en, d.score + " / " + d.max + " (" + d.level + ")", "sofi-cell-" + d.level.toLowerCase()));
    });
    td.appendChild(grid);
    tr.appendChild(td);
    return tr;
  }

  function closeOpen() {
    if (!open) { return; }
    var was = open;
    open = null;
    was.link.classList.remove("is-expanded-link");
    was.detail.remove();
    Array.prototype.forEach.call(bodyEl.children, function (tr) { tr.classList.remove("is-list-hidden"); });
    if (scroller) { scroller.scrollTop = was.scrollTop; }
    window.scrollTo(window.scrollX, was.pageY);
  }

  function openRow(link, tr, r) {
    var detail = detailRow(r);
    open = { link: link, detail: detail, scrollTop: scroller ? scroller.scrollTop : 0, pageY: window.scrollY };
    link.classList.add("is-expanded-link");
    tr.after(detail);
    Array.prototype.forEach.call(bodyEl.children, function (other) {
      if (other !== tr && other !== detail) { other.classList.add("is-list-hidden"); }
    });
    tr.scrollIntoView({ block: "nearest" });
  }

  function nameCell(tr, r) {
    var td = document.createElement("td");
    var link = document.createElement("a");
    link.href = "#";
    link.textContent = r.name;
    link.addEventListener("click", function (e) {
      e.preventDefault();
      var wasThis = open && open.link === link;
      closeOpen();
      if (!wasThis) { openRow(link, tr, r); }
    });
    td.appendChild(link);
    return td;
  }

  function render(rows) {
    open = null; // the old rows are about to be thrown away
    bodyEl.textContent = "";
    emptyEl.hidden = rows.length > 0;
    rows.forEach(function (r) {
      var tr = document.createElement("tr");
      tr.appendChild(cell(showTime(r.submittedAt)));
      tr.appendChild(nameCell(tr, r));
      tr.appendChild(cell(r.workPosition || "—"));
      tr.appendChild(cell(r.company || "—"));
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
    exportEl.href = "/api/sofi-log.xlsx" + (params.toString() ? "?" + params : "");
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
