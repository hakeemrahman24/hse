// Medical Record > Screening > Fatigue: the SOFI "period" and the open/closed status line.
// A period is one window an admin scheduled for /sofi (Settings > SOFI schedule), labelled by its
// dates (GMT+9). The SOFI tab and the dashboard each have their own Period dropdown: the table
// starts on "All periods", the dashboard on the latest period (one test period at a time). Each
// view is told about a change through the "sofi-round-change" event, whose detail.id is the
// dropdown's id. Status and periods come from GET /api/sofi-windows/status.
(function () {
  "use strict";

  var selects = document.querySelectorAll(".sofi-round-select");
  var lines = document.querySelectorAll(".sofi-window-status");
  if (selects.length === 0) { return; }

  var ALL = "";
  var BEFORE_PERIODS = "none"; // questionnaires saved before windows existed
  var touched = {}; // dropdown ids the person has changed: their choice is kept on refresh
  var seq = 0;

  function option(value, text) {
    var o = document.createElement("option");
    o.value = value;
    o.textContent = text;
    return o;
  }

  function periodText(r) {
    return (r.label ? r.label + " · " : "") + r.opens + " – " + r.closes;
  }

  function announce(sel) {
    document.dispatchEvent(new CustomEvent("sofi-round-change", { detail: { id: sel.id } }));
  }

  // periods are newest first, so periods[0] is the latest.
  function fill(periods) {
    selects.forEach(function (sel) {
      var before = sel.value;
      var options = [option(ALL, "All periods")];
      periods.forEach(function (r) { options.push(option(String(r.id), periodText(r))); });
      options.push(option(BEFORE_PERIODS, "Before periods were scheduled"));
      sel.replaceChildren.apply(sel, options);

      var wanted = before;
      if (!touched[sel.id]) {
        wanted = sel.dataset.default === "latest" && periods.length > 0 ? String(periods[0].id) : ALL;
      }
      sel.value = wanted;
      if (sel.value !== wanted) { sel.value = ALL; } // the period is gone from the list
      if (sel.value !== before) { announce(sel); }
    });
  }

  function statusText(s) {
    if (s.state === "open") { return "Open now, until " + s.until + " (" + s.zone + ")."; }
    if (s.state === "upcoming") { return "Closed. The next window opens " + s.opens + " (" + s.zone + ")."; }
    return "Closed. No window is scheduled.";
  }

  function showStatus(text, isOpen) {
    lines.forEach(function (line) {
      line.textContent = text;
      line.classList.toggle("is-open", isOpen);
    });
  }

  function refresh() {
    var mine = ++seq;
    fetch("/api/sofi-windows/status", { credentials: "same-origin" })
      .then(function (res) {
        if (res.status === 401) { location.href = "/"; return Promise.reject(new Error("signed out")); }
        return res.json().then(function (data) {
          if (!res.ok) { throw new Error(data.error || "Something went wrong (" + res.status + ")"); }
          return data;
        });
      })
      .then(function (s) {
        if (mine !== seq) { return; }
        fill(s.rounds);
        showStatus(statusText(s), s.state === "open");
      })
      .catch(function (err) {
        if (mine !== seq) { return; }
        showStatus(err.message, false);
      });
  }

  selects.forEach(function (sel) {
    sel.addEventListener("change", function () {
      touched[sel.id] = true;
      announce(sel);
    });
  });

  // Fresh status whenever either SOFI view is opened: a window may have opened or closed meanwhile.
  ["scr-tab-sofi", "scr-tab-dashboard"].forEach(function (id) {
    var tab = document.getElementById(id);
    if (tab) { tab.addEventListener("click", refresh); }
  });

  function selectById(id) { return document.getElementById(id); }

  window.SofiRounds = {
    // The ?window= value for a dropdown: "" (all), "none" (before periods) or a window id.
    param: function (id) { var el = selectById(id); return el ? el.value : ""; },
    // The chosen period's dates as text, or "" for "All periods".
    label: function (id) {
      var el = selectById(id);
      return el && el.value !== ALL && el.selectedOptions.length > 0 ? el.selectedOptions[0].textContent : "";
    },
    refresh: refresh
  };
  refresh();
})();
