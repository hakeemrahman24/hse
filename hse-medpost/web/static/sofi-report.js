// Medical Record > Screening > Fatigue > Dashboard: the doctor's note of the chosen test period
// and the "Download PDF report" button.
//
// The note is saved per period (GET / PUT /api/sofi-windows/{id}/note): anyone signed in can read
// it, only a doctor, the Chief Medical Officer or the administrator can write it (the server
// decides and says so in "canWrite"). The PDF is drawn in the browser by sofi-report-pdf.js from
// the numbers the dashboard has already drawn (window.SofiDashboard, set by sofi-dashboard.js),
// so the report cannot disagree with the screen. jsPDF is loaded on first use.
(function () {
  "use strict";

  var ROUND_ID = "sofi-dash-round"; // the dashboard's Period dropdown (sofi-rounds.js)
  var GMT9_MS = 9 * 60 * 60 * 1000;

  var box = document.getElementById("sofi-note-box");
  var pdfBtn = document.getElementById("sofi-pdf");
  if (!box || !pdfBtn) { return; }

  var textEl = document.getElementById("sofi-note-text");
  var metaEl = document.getElementById("sofi-note-meta");
  var saveBtn = document.getElementById("sofi-note-save");
  var noteMsg = document.getElementById("sofi-note-msg");
  var pdfMsg = document.getElementById("sofi-pdf-msg");

  var saved = { note: "", canWrite: false, writtenBy: "", writtenAt: "" };
  var seq = 0;
  // The period whose note the box holds. Save writes only to it, so a note typed for one period can
  // never be saved under another that was picked while the new note was still loading.
  var loadedFor = "";

  // The chosen period's window id, or "" for "All periods" / "Before periods were scheduled",
  // which have no note.
  function periodId() {
    var v = window.SofiRounds ? window.SofiRounds.param(ROUND_ID) : "";
    return /^[0-9]+$/.test(v) ? v : "";
  }

  function noteUrl(id) { return "/api/sofi-windows/" + encodeURIComponent(id) + "/note"; }

  function fetchJson(url, options) {
    return fetch(url, Object.assign({ credentials: "same-origin" }, options || {})).then(function (res) {
      if (res.status === 401) { location.href = "/"; return Promise.reject(new Error("signed out")); }
      return res.json().catch(function () { return {}; }).then(function (data) {
        if (!res.ok) { throw new Error(data.error || "Something went wrong (" + res.status + ")"); }
        return data;
      });
    });
  }

  function metaText(n) {
    return n.writtenBy ? "Written by " + n.writtenBy + ", " + n.writtenAt + " (GMT+9)." : "No note written yet.";
  }

  // Empties and locks the box until the note of the chosen period has arrived.
  function lockBox() {
    loadedFor = "";
    saved = { note: "", canWrite: false, writtenBy: "", writtenAt: "" };
    textEl.value = "";
    textEl.readOnly = true;
    saveBtn.hidden = true;
    metaEl.textContent = "";
  }

  function showNote(n, id) {
    loadedFor = id;
    saved = n;
    textEl.value = n.note || "";
    textEl.readOnly = !n.canWrite;
    saveBtn.hidden = !n.canWrite;
    metaEl.textContent = metaText(n);
    // Staff who cannot write see the box only when there is a note to read.
    box.hidden = !n.canWrite && !n.note;
    noteMsg.textContent = "";
  }

  function loadNote() {
    var id = periodId();
    var mine = ++seq;
    lockBox();
    if (!id) {
      box.hidden = true;
      return;
    }
    fetchJson(noteUrl(id))
      .then(function (n) { if (mine === seq) { showNote(n, id); } })
      .catch(function (err) {
        if (mine !== seq) { return; }
        box.hidden = false;
        noteMsg.textContent = err.message;
      });
  }

  textEl.addEventListener("input", function () {
    metaEl.textContent = textEl.value !== (saved.note || "") ? "Unsaved changes." : metaText(saved);
  });

  saveBtn.addEventListener("click", function () {
    var id = periodId();
    if (!id || id !== loadedFor) { return; }
    saveBtn.disabled = true;
    noteMsg.textContent = "Saving…";
    fetchJson(noteUrl(id), {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ note: textEl.value })
    })
      .then(function (n) {
        if (periodId() !== id) { return; } // another period was picked while saving
        showNote(n, id);
        noteMsg.textContent = "Saved.";
      })
      .catch(function (err) { noteMsg.textContent = err.message; })
      .then(function () { saveBtn.disabled = false; });
  });

  function loadScript(src) {
    return new Promise(function (resolve, reject) {
      var s = document.createElement("script");
      s.src = src;
      s.onload = resolve;
      s.onerror = function () { reject(new Error("Could not load the PDF library.")); };
      document.head.appendChild(s);
    });
  }

  // The two on-demand PDF fonts (static/fonts/, see build-report-fonts.py), as base64 for jsPDF.
  // Fetched only when the report text has that script, and kept for the next report.
  var FONT_URLS = {
    cjk: "/static/fonts/NotoSansCJK-Report.ttf",
    arabic: "/static/fonts/NotoSansArabic-Regular.ttf"
  };
  var fontCache = {};

  function blobToBase64(blob) {
    return new Promise(function (resolve, reject) {
      var reader = new FileReader();
      reader.onload = function () { resolve(String(reader.result).split(",")[1]); };
      reader.onerror = function () { reject(reader.error); };
      reader.readAsDataURL(blob);
    });
  }

  function loadFont(key) {
    if (!fontCache[key]) {
      fontCache[key] = fetch(FONT_URLS[key], { credentials: "same-origin" })
        .then(function (res) {
          if (!res.ok) { throw new Error("font " + res.status); }
          return res.blob();
        })
        .then(blobToBase64)
        .catch(function (err) { delete fontCache[key]; throw err; });
    }
    return fontCache[key];
  }

  // Returns {fonts: {cjk, arabic} (base64 or null), failed: [keys]}. A font that cannot be loaded
  // is not fatal: that script prints as "?" and the doctor is told.
  async function loadFonts(need) {
    var out = { fonts: { cjk: null, arabic: null }, failed: [] };
    await Promise.all(Object.keys(FONT_URLS).filter(function (k) { return need[k]; }).map(function (k) {
      return loadFont(k).then(
        function (b64) { out.fonts[k] = b64; },
        function () { out.failed.push(k); }
      );
    }));
    return out;
  }

  // Text no PDF font covers (Thai, emoji, ...): the browser draws it on a canvas, with whatever
  // system fonts it has, and the PDF gets a picture. 4 canvas pixels per point (about 290 dpi) keeps
  // it sharp in print. The stack names the usual Thai and emoji fonts of each system.
  var RASTER_SCALE = 4;
  var RASTER_STACK = '"Noto Sans Thai", "Leelawadee UI", "Thonburi", Tahoma, "Noto Color Emoji", "Apple Color Emoji", "Segoe UI Emoji", "Segoe UI Symbol", sans-serif';

  function createRasteriser() {
    var ctx = document.createElement("canvas").getContext("2d");
    var cache = {};
    function setFont(size, style) {
      ctx.font = (style === "bold" ? "bold " : "") + size * RASTER_SCALE + "px " + RASTER_STACK;
    }
    return {
      measure: function (text, size, style) {
        setFont(size, style);
        return ctx.measureText(text).width / RASTER_SCALE;
      },
      image: function (text, size, style, rgb) {
        var key = [text, size, style, rgb.join(",")].join("|");
        if (cache[key]) { return cache[key]; }
        setFont(size, style);
        var m = ctx.measureText(text);
        // Thai stacks marks well above and below the line, so leave room: 0.5 em above the ascent
        var ascent = (m.fontBoundingBoxAscent || size * 0.9) + size * 0.5;
        var descent = (m.fontBoundingBoxDescent || size * 0.3) + size * 0.3;
        var wPt = m.width / RASTER_SCALE;
        // resizing a canvas clears it and resets its font
        ctx.canvas.width = Math.max(1, Math.ceil(m.width) + 2);
        ctx.canvas.height = Math.ceil((ascent + descent) * RASTER_SCALE);
        setFont(size, style);
        ctx.fillStyle = "rgb(" + rgb.join(",") + ")";
        ctx.textBaseline = "alphabetic";
        ctx.fillText(text, 0, ascent * RASTER_SCALE);
        var img = { data: ctx.canvas.toDataURL("image/png"), w: ctx.canvas.width / RASTER_SCALE, h: ctx.canvas.height / RASTER_SCALE, ascent: ascent };
        img.w = Math.max(img.w, wPt); // the next run starts exactly measure() further on
        cache[key] = img;
        return img;
      }
    };
  }

  function gmt9Now() { return new Date(Date.now() + GMT9_MS).toISOString().slice(0, 16).replace("T", " "); }

  async function download() {
    pdfMsg.textContent = "";
    var dash = window.SofiDashboard;
    var param = window.SofiRounds ? window.SofiRounds.param(ROUND_ID) : "";
    // The dashboard remembers which period its numbers are for; if the dropdown has moved on, wait.
    if (!dash || !dash.data || dash.param !== param) {
      pdfMsg.textContent = "The dashboard is still loading. Try again in a moment.";
      return;
    }
    if (dash.data.total === 0) {
      pdfMsg.textContent = "There are no surveys in this period to report.";
      return;
    }
    var id = periodId();
    if (id && textEl.value !== (saved.note || "") &&
        !confirm("The note has unsaved changes. The PDF prints the saved note. Continue?")) {
      return;
    }
    pdfBtn.disabled = true;
    pdfMsg.textContent = "Preparing the PDF…";
    try {
      // The saved note is read again, so the PDF never prints a stale copy of it.
      var results = await Promise.all([id ? fetchJson(noteUrl(id)) : Promise.resolve(null), fetchJson("/api/me")]);
      if (!window.jspdf) { await loadScript("/static/vendor/jspdf.min.js"); }
      var label = window.SofiRounds.label(ROUND_ID);
      var model = sofiReportModel({
        data: dash.data,
        summary: dash.summary,
        periodLabel: label,
        note: results[0] ? { text: results[0].note, writtenBy: results[0].writtenBy, writtenAt: results[0].writtenAt } : null,
        generatedBy: results[1].name || "staff",
        generatedAt: gmt9Now()
      });
      var loaded = await loadFonts(sofiReportScripts(model));
      sofiBuildReportPdf(model, loaded.fonts, createRasteriser()).save(sofiReportFileName(label));
      pdfMsg.textContent = loaded.failed.length
        ? "The PDF was made, but a font for " + loaded.failed.join(" and ") + " text could not be loaded, so that text shows as ?."
        : "";
    } catch (err) {
      pdfMsg.textContent = err.message;
    } finally {
      pdfBtn.disabled = false;
    }
  }

  pdfBtn.addEventListener("click", download);
  document.addEventListener("sofi-round-change", function (e) {
    if (e.detail.id === ROUND_ID) { loadNote(); }
  });
  loadNote();
})();
