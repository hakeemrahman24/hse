// Settings > SOFI schedule (admin only): when the public /sofi questionnaire is open.
// Reads and writes /api/sofi-windows. The server keeps every window ever scheduled; this page can
// only add one, cancel one that has not opened, or close the open one now. Times are typed and
// shown in GMT+9 whatever the browser's own time zone is (the server does the conversion).
(function () {
  "use strict";

  var GMT9_MS = 9 * 60 * 60 * 1000;
  var HOUR_MS = 60 * 60 * 1000;
  // Crews work shifts and rotations, so a window shorter than a day can leave a whole shift out.
  var MIN_WINDOW_HOURS = 24;
  // Opening a few minutes in the past is just "open now"; further back is probably a typo.
  var PAST_GRACE_MS = 5 * 60 * 1000;
  var FORM_TIME = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/; // what <input type="datetime-local"> holds

  // Soft warnings for a window about to be scheduled. opens / closes are what the form holds
  // (yyyy-MM-ddTHH:mm, GMT+9); nowMs is Date.now(). Warns, never blocks: the admin decides.
  function windowWarnings(opens, closes, nowMs) {
    var warnings = [];
    // Date.parse is lenient (it reads ":00Z" as a date), so check the shape first.
    if (!FORM_TIME.test(opens) || !FORM_TIME.test(closes)) { return warnings; }
    // Both values are GMT+9 wall-clock times, so reading them as UTC keeps every difference exact.
    var from = Date.parse(opens + ":00Z");
    var to = Date.parse(closes + ":00Z");
    if (isNaN(from) || isNaN(to)) { return warnings; }
    var hours = (to - from) / HOUR_MS;
    if (hours > 0 && hours < MIN_WINDOW_HOURS) {
      warnings.push("This window is only " + Math.round(hours * 10) / 10 + " hours long (under " + MIN_WINDOW_HOURS +
        "). People on another shift may miss it.");
    }
    if (from < nowMs + GMT9_MS - PAST_GRACE_MS) {
      warnings.push("It opens in the past (" + opens.replace("T", " ") + " GMT+9), so it will be open straight away.");
    }
    return warnings;
  }

  if (typeof module !== "undefined" && module.exports) { module.exports = { windowWarnings: windowWarnings }; }

  var form = document.getElementById("sofi-window-form");
  if (!form) { return; }

  var bodyEl = document.getElementById("sofi-window-body");
  var statusEl = document.getElementById("sofi-sched-status");
  var msgEl = document.getElementById("sofi-window-msg");
  var liveSwitch = document.getElementById("sofi-live-switch");
  var liveNote = document.getElementById("sofi-live-note");
  var liveDays = document.getElementById("sofi-live-days");
  var emptyEl = document.getElementById("sofi-window-empty");
  var previewSwitch = document.getElementById("sofi-preview-switch");
  var previewNote = document.getElementById("sofi-preview-note");
  var previewOn = false; // what the server last said
  var openWindow = null; // the window open right now, if any (the live switch closes this one)

  function setSwitch(el, on, disabled) {
    el.setAttribute("aria-checked", on ? "true" : "false");
    el.classList.toggle("is-on", on);
    el.disabled = !!disabled;
  }

  var STATE_LABEL = {
    upcoming: "Upcoming", open: "Open", past: "Finished",
    cancelled: "Cancelled", closed_early: "Closed early"
  };
  var MAX_OPEN_DAYS = 60; // a typo like 700 would leave /sofi open for two years

  // The value an <input type="datetime-local"> would hold for this instant in GMT+9.
  function gmt9Input(ms) { return new Date(ms + GMT9_MS).toISOString().slice(0, 16); }

  function showMessage(text) {
    msgEl.textContent = text || "";
    msgEl.hidden = !text;
  }

  function cell(text) {
    var td = document.createElement("td");
    td.textContent = text;
    return td;
  }

  function actionButton(label, win, verb) {
    var btn = document.createElement("button");
    btn.type = "button";
    btn.className = "btn-ghost";
    btn.textContent = label;
    btn.addEventListener("click", function () { endWindow(win, verb); });
    return btn;
  }

  function row(win) {
    var tr = document.createElement("tr");
    var ended = win.ended ? win.ended + " by " + (win.endedBy || "—") : "—";
    tr.appendChild(cell(win.label || "—"));
    tr.appendChild(cell(win.opens));
    tr.appendChild(cell(win.closes));
    tr.appendChild(cell(STATE_LABEL[win.state] || win.state));
    tr.appendChild(cell(win.createdAt + " by " + (win.createdBy || "—")));
    tr.appendChild(cell(ended));
    var action = document.createElement("td");
    if (win.state === "upcoming") { action.appendChild(actionButton("Cancel", win, "cancel")); }
    if (win.state === "open") { action.appendChild(actionButton("Close now", win, "close")); }
    tr.appendChild(action);
    return tr;
  }

  async function readJson(res) { return res.json().catch(function () { return {}; }); }

  async function load() {
    try {
      var results = await Promise.all([
        fetch("/api/sofi-windows", { credentials: "same-origin" }),
        fetch("/api/sofi-windows/status", { credentials: "same-origin" })
      ]);
      var list = await readJson(results[0]);
      var status = await readJson(results[1]);
      if (!results[0].ok) { throw new Error(list.error || "Could not read the schedule."); }
      bodyEl.replaceChildren.apply(bodyEl, list.map(row));
      emptyEl.hidden = list.length > 0;
      previewOn = !!status.preview;
      openWindow = list.find(function (w) { return w.state === "open"; }) || null;
      var liveOn = !!openWindow;
      setSwitch(liveSwitch, liveOn, false);
      liveDays.disabled = liveOn; // the length is only used when turning on
      liveNote.textContent = liveOn
        ? "On. Answers are saved. Open until " + openWindow.closes + " (GMT+9)."
        : "Off. /sofi shows “closed”. Turning on opens it for the number of days set here.";
      // A real window is already open and recording, so preview has nothing to add.
      setSwitch(previewSwitch, previewOn, liveOn);
      previewNote.textContent = liveOn
        ? "Not needed while the live survey is on."
        : previewOn
          ? "On until " + status.previewUntil + " (GMT+9). Answers are not saved."
          : "Off. Turning on lets anyone with the link try it; nothing is saved. Turns itself off after 2 hours.";
      if (previewOn) {
        statusEl.textContent = "PREVIEW is on until " + status.previewUntil + " (GMT+9). /sofi is open, but nothing sent is saved.";
      } else if (status.state === "open") {
        statusEl.textContent = "Open now, until " + status.until + " (GMT+9).";
      } else if (status.state === "upcoming") {
        statusEl.textContent = "Closed. The next window opens " + status.opens + " (GMT+9).";
      } else {
        statusEl.textContent = "Closed. No window is scheduled, so /sofi shows “closed”.";
      }
    } catch (err) {
      statusEl.textContent = err.message;
    }
  }

  async function send(body) {
    var res = await fetch("/api/sofi-windows", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body)
    });
    var data = await readJson(res);
    if (!res.ok) { throw new Error(data.error || "Could not save the window."); }
  }

  async function endWindow(win, verb) {
    var sure = verb === "close"
      ? "Close /sofi now? People who are filling in the form lose the chance to submit."
      : "Cancel the window starting " + win.opens + " (GMT+9)? It stays in the list as cancelled.";
    if (!confirm(sure)) { return; }
    showMessage("");
    var res = await fetch("/api/sofi-windows/" + encodeURIComponent(win.id) + "/end", {
      method: "POST",
      credentials: "same-origin"
    });
    var data = await readJson(res);
    if (!res.ok) { showMessage(data.error || "Could not end the window."); }
    load();
  }

  form.addEventListener("submit", async function (e) {
    e.preventDefault();
    showMessage("");
    var fd = new FormData(form);
    var warnings = windowWarnings(fd.get("opens"), fd.get("closes"), Date.now());
    if (warnings.length > 0 && !confirm(warnings.join("\n\n") + "\n\nSchedule it anyway?")) { return; }
    try {
      await send({ label: fd.get("label"), opens: fd.get("opens"), closes: fd.get("closes") });
      form.reset();
    } catch (err) {
      showMessage(err.message);
    }
    load();
  });

  // Live switch. On = a window that opens now (the same record a scheduled window leaves, so the
  // audit trail and the Round filter keep working). Off = close the open window now.
  liveSwitch.addEventListener("click", async function () {
    if (openWindow) { await endWindow(openWindow, "close"); return; }
    var days = Number(liveDays.value);
    if (!Number.isInteger(days) || days < 1 || days > MAX_OPEN_DAYS) {
      showMessage("Enter a whole number of days from 1 to " + MAX_OPEN_DAYS + ".");
      liveDays.focus();
      return;
    }
    var now = Date.now();
    var closes = gmt9Input(now + days * 24 * 60 * 60 * 1000);
    if (!confirm("Turn the live survey on? /sofi opens now until " + closes.replace("T", " ") + " (GMT+9), and answers are saved.")) { return; }
    showMessage("");
    try {
      await send({ label: "", opens: gmt9Input(now), closes: closes });
    } catch (err) {
      showMessage(err.message);
    }
    load();
  });

  // Preview opens /sofi without a window, for looking at it. Answers are scored and shown but never
  // saved. The server turns it off by itself after two hours, so it cannot stay open by mistake.
  previewSwitch.addEventListener("click", async function () {
    if (!previewOn && !confirm("Open /sofi for preview? Anyone with the link can fill it in, but nothing they send is saved. It turns itself off after 2 hours.")) { return; }
    showMessage("");
    var res = await fetch("/api/sofi-preview", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ on: !previewOn })
    });
    var data = await readJson(res);
    if (!res.ok) { showMessage(data.error || "Could not change preview."); }
    load();
  });

  load();
})();
