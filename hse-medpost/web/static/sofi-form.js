// /sofi questionnaire: 25 words rated 0-6, weeks on site, then the result. The server works out
// every score and level; this script only collects the answers and shows what comes back.
(function () {
  "use strict";

  // A page restored from the back/forward cache after sign-out is a stale copy;
  // reloading lets the server send a signed-out visitor back to /sofi.
  window.addEventListener("pageshow", function (e) { if (e.persisted) location.reload(); });

  var ITEM_COUNT = 25;
  var form = document.getElementById("sofi-form");
  var errEl = document.getElementById("sofi-error");
  var resultEl = document.getElementById("sofi-result");
  var barsEl = document.getElementById("sofi-bars");
  var weeksEl = document.getElementById("weeks");
  // The texts come from the page, already in the visitor's language.
  var msg = form.dataset;
  var lang = msg.lang === "en" || msg.lang === "zh" ? msg.lang : "id";
  var submitting = false;
  var submitBtn = form.querySelector('button[type="submit"]');
  var progressEl = document.getElementById("sofi-progress");

  function showError(text) {
    errEl.textContent = text;
    errEl.hidden = !text;
    if (text) { errEl.scrollIntoView({ behavior: "smooth", block: "center" }); }
  }

  function readAnswers() {
    var out = [];
    for (var n = 1; n <= ITEM_COUNT; n++) {
      var picked = form.querySelector('input[name="q' + n + '"]:checked');
      if (!picked) { return { missing: n }; }
      out.push(+picked.value);
    }
    return { answers: out };
  }

  // Marks the first unanswered word so a long form does not hide what is missing.
  function markMissing(n) {
    var li = form.querySelector('.sofi-item[data-item="' + n + '"]');
    if (!li) { return; }
    li.classList.add("missing");
    li.scrollIntoView({ behavior: "smooth", block: "center" });
  }

  // Every word and the weeks are mandatory, so the button stays disabled until all are answered.
  // "n / 25" shows how far along the person is, so a long form never hides what is still missing.
  function refreshSubmit() {
    var answered = 0;
    for (var n = 1; n <= ITEM_COUNT; n++) {
      if (form.querySelector('input[name="q' + n + '"]:checked')) { answered++; }
    }
    var complete = answered === ITEM_COUNT && /^\d{1,3}$/.test(weeksEl.value.trim());
    submitBtn.disabled = !complete;
    if (progressEl) { progressEl.textContent = answered + " / " + ITEM_COUNT; }
  }

  form.addEventListener("change", function (e) {
    var li = e.target.closest && e.target.closest(".sofi-item");
    if (li) { li.classList.remove("missing"); }
    refreshSubmit();
  });
  // A browser can restore ticked answers after a reload or Back, so count them once at the start.
  refreshSubmit();
  window.addEventListener("pageshow", refreshSubmit);

  function bar(d) {
    var row = document.createElement("div");
    row.className = "sofi-bar";
    var label = document.createElement("span");
    label.className = "sofi-bar-label";
    label.textContent = d[lang]; // textContent, never innerHTML
    var track = document.createElement("span");
    track.className = "sofi-bar-track";
    var fill = document.createElement("span");
    fill.className = "sofi-bar-fill sofi-" + d.level.toLowerCase();
    fill.style.width = Math.round((d.score / d.max) * 100) + "%";
    track.appendChild(fill);
    var value = document.createElement("span");
    value.className = "sofi-bar-value";
    value.textContent = d.score + " / " + d.max + " · " + msg["level" + d.level];
    row.appendChild(label);
    row.appendChild(track);
    row.appendChild(value);
    return row;
  }

  function showResult(res) {
    barsEl.textContent = "";
    res.dimensions.forEach(function (d) { barsEl.appendChild(bar(d)); });
    document.getElementById("sofi-total").textContent = res.total + " / " + res.totalMax;
    var level = document.getElementById("sofi-level");
    level.textContent = msg["level" + res.level];
    level.className = "sofi-level sofi-" + res.level.toLowerCase();
    document.getElementById("sofi-help").textContent = msg["help" + res.level];
    form.hidden = true;
    resultEl.hidden = false;
    window.scrollTo({ top: 0, behavior: "smooth" });
  }

  form.addEventListener("submit", function (e) {
    e.preventDefault();
    showError("");
    if (submitting) { return; }
    var weeks = weeksEl.value.trim();
    if (!/^\d{1,3}$/.test(weeks)) { showError(msg.msgWeeks); weeksEl.focus(); return; } // the choice list has no blank answer
    var read = readAnswers();
    if (read.missing) { showError(msg.msgAnswerAll); markMissing(read.missing); return; }

    submitting = true;
    fetch("/sofi/api/submit", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: JSON.stringify({ weeksOnSite: +weeks, answers: read.answers }),
    })
      .then(function (res) {
        return res.json().catch(function () { return {}; }).then(function (data) { return { ok: res.ok, status: res.status, data: data }; });
      })
      .then(function (r) {
        if (r.status === 401) { location.href = "/sofi"; return; }
        if (!r.ok) { showError(r.data.error || msg.msgFailed); return; }
        showResult(r.data);
      })
      .catch(function () { showError(msg.msgFailed); })
      .then(function () { submitting = false; });
  });
})();
