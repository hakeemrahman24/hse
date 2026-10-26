// The SOC wizard (SOC_Form_Spec_v1.1.md): one question per screen on the phone. It is one ordinary
// form; this script only shows one screen at a time and the server checks every answer again.
//
//   attachWizard({ form, url, texts })
//
// Screens carry data-paths="ABCD" (the paths that use them), data-cond="ppe|stopped|shouldstop"
// (shown only when the earlier answers call for it), data-auto (a radio screen: tapping an answer
// moves on), data-topic (the topic buttons are shown under the text box). Fields of screens that are
// not used by the chosen path are disabled, so they are not sent.
(function () {
  "use strict";

  var DRAFT_KEY = "soc_draft_v1", DRAFT_MAX_AGE_MS = 24 * 3600 * 1000;
  var LAST_AREA = "soc_last_area", LAST_COMPANY = "soc_last_company";
  var AUTO_DELAY_MS = 180;

  function store(fn) { try { return fn(window.localStorage); } catch (e) { return null; } } // storage can be blocked

  window.attachWizard = function (opts) {
    var form = opts.form;
    if (!form) { return; }
    var $ = function (sel, root) { return (root || form).querySelector(sel); };
    var all = function (sel, root) { return Array.prototype.slice.call((root || form).querySelectorAll(sel)); };
    var screens = all(".wz-screen");
    var pathInput = $("#wz-path"), hazardInput = $("#hazard_id");
    var progress = $("#wz-progress"), back = $("#wz-back"), next = $("#wz-next"), send = $("#wz-send"), missing = $("#wz-missing");
    var topicBlock = $("#wz-topic"), topicSel = $("#topic_id"), urgent = $("#wz-urgent");
    var qr = form.getAttribute("data-qr") === "1", qrSub = form.getAttribute("data-qr-sub") === "1";
    var subAreas = {};
    try { subAreas = JSON.parse(form.getAttribute("data-subareas") || "{}") || {}; } catch (e) { subAreas = {}; }
    var areaSel = $("#wz-area"), subSel = $("#wz-subarea");
    var idx = 0, order = [];

    var suggest = window.attachTopicSuggest && window.attachTopicSuggest({
      description: all(".wz-desc"), topic: topicSel, chips: $("#topic-chips"), none: $("#topic-none"),
      hint: $("#suggest-hint"), url: opts.url, texts: opts.texts
    });

    // ---- the screens of the chosen path ----

    function checked(name) { var r = $("input[name=" + name + "]:checked"); return r; }
    function topicIsHighPotential() {
      var o = topicSel.options[topicSel.selectedIndex];
      return !!(o && o.value && o.getAttribute("data-hp") === "1");
    }
    var conditions = {
      ppe: function () { var r = checked("unsafe_reason"); return !!(r && r.getAttribute("data-ppe") === "1"); },
      stopped: function () { var r = checked("stop_work"); return !!(r && r.value === "yes"); },
      subarea: function () { return !!(areaSel && (subAreas[areaSel.value] || []).length); },
      shouldstop: function () { var r = checked("stop_work"); return !!(r && r.value === "no" && topicIsHighPotential()); }
    };

    function used(screen, path) {
      var paths = screen.getAttribute("data-paths");
      if (paths === null) { return screen.getAttribute("data-screen") === "start"; }
      if (paths.indexOf(path) === -1) { return false; }
      var cond = screen.getAttribute("data-cond");
      return !cond || conditions[cond]();
    }

    function build() {
      var path = pathInput.value;
      order = screens.filter(function (s) {
        if (s.getAttribute("data-screen") === "start") { return true; }
        if (!path || !used(s, path)) { return false; }
        var name = s.getAttribute("data-screen");
        return !(qr && name === "area") && !(qrSub && name === "subarea");
      });
      // Fields of screens this path does not use must not be sent (two screens share the name "work").
      screens.forEach(function (s) {
        var on = s.getAttribute("data-screen") === "start" || (path && used(s, path)) || (qr && s.getAttribute("data-screen") === "area" && !!path);
        all("input, textarea, select", s).forEach(function (el) { el.disabled = !on; });
      });
      // Path D asks for the proposal; A and B take it as an option.
      var d = path === "D";
      var rec = $("#wz-rec");
      if (rec) { if (d) { rec.setAttribute("data-req", ""); } else { rec.removeAttribute("data-req"); } }
      var opt = $(".wz-rec-optional"), req = $(".wz-rec-required");
      if (opt && req) { opt.hidden = d; req.hidden = !d; }
    }

    // The sub-area dropdown holds the sub-areas of the chosen area (kept when it is still one of them).
    var subFor = null;
    function fillSubAreas() {
      if (!subSel || !areaSel || subFor === areaSel.value) { return; }
      var keep = subSel.value, list = subAreas[areaSel.value] || [];
      subFor = areaSel.value;
      while (subSel.options.length > 1) { subSel.remove(1); }
      list.forEach(function (name) {
        var o = document.createElement("option");
        o.value = o.textContent = name;
        subSel.appendChild(o);
      });
      subSel.value = list.indexOf(keep) !== -1 ? keep : "";
    }

    // ---- showing a screen ----

    function show(i) {
      fillSubAreas();
      build();
      idx = Math.max(0, Math.min(i, order.length - 1));
      screens.forEach(function (s) { s.hidden = s !== order[idx]; });
      var cur = order[idx];
      var last = idx === order.length - 1 && order.length > 1;
      back.hidden = idx === 0;
      next.hidden = idx === 0 || last;
      send.hidden = !last;
      missing.hidden = true;
      progress.textContent = idx === 0 ? "" : (form.getAttribute("data-step") || "")
        .replace("{n}", String(idx)).replace("{total}", String(order.length - 1));
      if (cur.hasAttribute("data-topic")) {
        cur.querySelector(".wz-topic-slot").appendChild(topicBlock);
        topicBlock.hidden = false;
        var ta = cur.querySelector(".wz-desc");
        if (suggest && ta) { suggest.ask(ta); }
      } else {
        topicBlock.hidden = true;
      }
      updateUrgent();
      var focusEl = cur.querySelector("textarea:not([disabled]), input[type=text]:not([disabled]), select:not([disabled])");
      if (focusEl && idx > 0) { try { focusEl.focus({ preventScroll: false }); } catch (e) { /* ignore */ } }
      if (idx > 0) { window.scrollTo(0, 0); }
      saveDraft();
    }

    function answered(screen) {
      var ok = true;
      all("[data-req]", screen).forEach(function (el) { if (!el.disabled && el.value.trim() === "") { ok = false; } });
      // A radio screen needs one answer, except the soft "should this have been stopped" prompt.
      if (screen.hasAttribute("data-auto") && screen.getAttribute("data-cond") !== "shouldstop") {
        var names = {};
        all("input[type=radio]", screen).forEach(function (r) { names[r.name] = true; });
        Object.keys(names).forEach(function (n) { if (!$("input[name=" + n + "]:checked", screen)) { ok = false; } });
      }
      return ok;
    }

    function go(delta) {
      if (delta > 0 && !answered(order[idx])) { missing.hidden = false; return; }
      show(idx + delta);
    }

    function updateUrgent() {
      if (!urgent) { return; }
      var d = checked("danger"), b = checked("barricaded");
      urgent.hidden = !(d && b && d.value === "yes" && b.value === "no");
    }

    // ---- events ----

    all(".wz-choice").forEach(function (b) {
      b.addEventListener("click", function () {
        pathInput.value = b.getAttribute("data-path");
        show(1);
      });
    });
    if (areaSel) { areaSel.addEventListener("change", function () { fillSubAreas(); build(); saveDraft(); }); }
    back.addEventListener("click", function () { go(-1); });
    next.addEventListener("click", function () { go(1); });

    all("input[type=radio]").forEach(function (r) {
      r.addEventListener("change", function () {
        updateUrgent();
        build(); // an answer can add or drop a later screen (PPE item, who stopped it, ...)
        saveDraft();
        var screen = r.closest(".wz-screen");
        if (!screen || !screen.hasAttribute("data-auto") || !answered(screen)) { return; }
        if (urgent && !urgent.hidden) { return; } // let the reporter read the warning, then tap Next
        setTimeout(function () { go(1); }, AUTO_DELAY_MS);
      });
    });

    topicSel.addEventListener("change", function () {
      var o = topicSel.options[topicSel.selectedIndex];
      hazardInput.value = o && o.value ? (o.getAttribute("data-hazard") || "") : "";
      saveDraft();
    });
    form.addEventListener("input", saveDraft);

    form.addEventListener("keydown", function (e) { // Enter in a one-line field means "next", not "send"
      if (e.key === "Enter" && e.target.tagName === "INPUT" && e.target.type !== "submit") {
        e.preventDefault();
        if (!send.hidden) { return; }
        go(1);
      }
    });

    form.addEventListener("submit", function (e) {
      build();
      // Every screen of the path must be answered before sending (the server checks again).
      for (var i = 1; i < order.length; i++) {
        if (!answered(order[i])) { e.preventDefault(); show(i); missing.hidden = false; return; }
      }
      store(function (s) {
        var a = $("#wz-area"); if (a && a.value) { s.setItem(LAST_AREA, a.value); }
        var c = $("#wz-company"); if (c && c.value.trim()) { s.setItem(LAST_COMPANY, c.value.trim()); }
        s.removeItem(DRAFT_KEY);
      });
    });

    // ---- draft: saved after every answer, restored on the next visit (files are not kept) ----

    function saveDraft() {
      store(function (s) {
        var values = {};
        all("input, textarea, select").forEach(function (el) {
          if (el.type === "file" || el.type === "submit" || el.name === "path") { return; }
          if (el.type === "radio") { if (el.checked) { values["r:" + el.name] = el.value; } return; }
          if (el.id) { values[el.id] = el.value; }
        });
        s.setItem(DRAFT_KEY, JSON.stringify({ path: pathInput.value, idx: idx, values: values, at: Date.now() }));
      });
    }

    function restoreDraft() {
      var raw = store(function (s) { return s.getItem(DRAFT_KEY); });
      var d = null;
      try { d = raw ? JSON.parse(raw) : null; } catch (e) { d = null; }
      if (!d || !d.path || Date.now() - d.at > DRAFT_MAX_AGE_MS) { return false; }
      pathInput.value = d.path;
      Object.keys(d.values || {}).forEach(function (k) {
        if (k.indexOf("r:") === 0) {
          var r = $("input[name=" + k.slice(2) + "][value=\"" + d.values[k] + "\"]");
          if (r) { r.checked = true; }
        } else {
          var el = document.getElementById(k);
          if (el && el.type !== "file") { el.value = d.values[k]; }
          if (k === "wz-area") { fillSubAreas(); } // the sub-area options depend on the area, which comes first in the form
        }
      });
      if (hazardInput && topicSel.value === "") { hazardInput.value = ""; }
      show(d.idx || 1);
      return true;
    }

    // A "start over" link next to the progress line drops the draft and reloads an empty wizard.
    var reset = document.createElement("button");
    reset.type = "button";
    reset.className = "wz-reset";
    reset.textContent = form.getAttribute("data-reset") || "";
    reset.addEventListener("click", function () {
      store(function (s) { s.removeItem(DRAFT_KEY); });
      window.location.href = window.location.pathname + window.location.search;
    });
    if (reset.textContent) { progress.parentNode.insertBefore(reset, progress.nextSibling); }

    // ---- start ----

    var hasErrors = !!$(".field-error");
    var lastArea = store(function (s) { return s.getItem(LAST_AREA); });
    var lastCompany = store(function (s) { return s.getItem(LAST_COMPANY); });
    var companyEl = $("#wz-company");
    if (!hasErrors) {
      if (areaSel && areaSel.value === "" && lastArea) { areaSel.value = lastArea; if (areaSel.value !== lastArea) { areaSel.value = ""; } }
      if (companyEl && companyEl.value === "" && lastCompany) { companyEl.value = lastCompany; }
    }

    if (pathInput.value) { // the server showed the form again with errors: open the first screen that has one
      build();
      var first = 1;
      for (var i = 1; i < order.length; i++) { if (order[i].querySelector(".field-error")) { first = i; break; } }
      show(first);
    } else if (!restoreDraft()) {
      show(0);
    }
  };
})();
