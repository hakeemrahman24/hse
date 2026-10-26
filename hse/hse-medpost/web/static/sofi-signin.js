// /sofi sign-in: name search -> date of birth -> sign in. Same steps as the SOC page in
// hse-register. The server does every check; this script only moves the visitor through them.
(function () {
  "use strict";

  var MIN_LETTERS = 3;
  var SEARCH_DELAY_MS = 250;
  var MIN_YEAR = 1900;

  var form = document.getElementById("signin-form");
  // The texts come from the page, already in the visitor's language.
  var msg = form.dataset;
  var errEl = document.getElementById("sofi-error");
  var nameInput = document.getElementById("name-input");
  var nameList = document.getElementById("name-list");
  var dobInput = document.getElementById("dob-input");
  var dobNative = document.getElementById("dob-native");
  var dobPick = document.getElementById("dob-pick");
  var chosenRef = "";
  var timer = null;
  var active = -1;

  function showError(text) {
    errEl.textContent = text;
    errEl.hidden = !text;
  }

  function postJSON(url, body) {
    return fetch(url, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: JSON.stringify(body),
    }).then(function (res) {
      return res.json().catch(function () { return {}; }).then(function (data) {
        return { ok: res.ok, data: data };
      });
    });
  }

  // ---- step 1: the Cloudflare check. Nothing else shows until the server accepts it. -----
  var box = document.getElementById("turnstile-box");
  var stepCheck = document.getElementById("step-check");
  var LOAD_POLL_MS = 100;
  var LOAD_POLL_MAX = 100;

  function onToken(token) {
    showError("");
    postJSON("/sofi/api/check", { token: token }).then(function (r) {
      if (!r.ok) {
        showError(r.data.error || msg.msgCheckFailed);
        if (window.turnstile) { window.turnstile.reset(); }
        return;
      }
      stepCheck.hidden = true;
      form.hidden = false;
      nameInput.focus();
    });
  }

  if (box.dataset.dev === "true") {
    // Development only: a button stands in for Cloudflare. Never shown to visitors.
    var stub = document.createElement("button");
    stub.type = "button";
    stub.className = "btn-ghost";
    stub.textContent = "Development check (stub)";
    stub.addEventListener("click", function () { onToken("dev-pass"); });
    box.appendChild(stub);
  } else {
    var tries = 0;
    var wait = setInterval(function () {
      tries++;
      if (window.turnstile) {
        clearInterval(wait);
        window.turnstile.render(box, { sitekey: box.dataset.sitekey, callback: onToken, language: box.dataset.lang });
      } else if (tries > LOAD_POLL_MAX) {
        clearInterval(wait);
        showError(msg.msgCheckLoad);
      }
    }, LOAD_POLL_MS);
  }

  // ---- name search: only a listed name can be chosen ------------------------
  function closeList() {
    nameList.hidden = true;
    nameList.textContent = "";
    active = -1;
    nameInput.setAttribute("aria-expanded", "false");
  }

  function highlight(i) {
    var rows = nameList.querySelectorAll(".combo-item");
    if (!rows.length) { active = -1; return; }
    active = (i + rows.length) % rows.length;
    rows.forEach(function (r, n) {
      r.classList.toggle("active", n === active);
      r.setAttribute("aria-selected", n === active ? "true" : "false");
    });
  }

  function pick(it) {
    chosenRef = it.ref;
    nameInput.value = it.label;
    closeList();
    dobInput.focus();
  }

  function renderList(items) {
    nameList.textContent = "";
    active = -1;
    if (!items.length) {
      var none = document.createElement("div");
      none.className = "combo-empty";
      none.textContent = msg.msgNoMatch;
      nameList.appendChild(none);
    }
    items.forEach(function (it) {
      var b = document.createElement("button");
      b.type = "button";
      b.className = "combo-item";
      b.setAttribute("role", "option");
      b.tabIndex = -1;
      // textContent, never innerHTML: names are data
      var nm = document.createElement("strong");
      nm.textContent = it.label;
      b.appendChild(nm);
      if (it.company) {
        var co = document.createElement("small");
        co.className = "combo-sub";
        co.textContent = it.company;
        b.appendChild(co);
      }
      // mousedown, not click: the input keeps focus so its blur does not close the list first.
      b.addEventListener("mousedown", function (e) {
        e.preventDefault();
        pick(it);
      });
      nameList.appendChild(b);
    });
    nameList.hidden = false;
    nameInput.setAttribute("aria-expanded", "true");
    if (items.length) { highlight(0); }
    nameList._items = items;
  }

  nameInput.addEventListener("keydown", function (e) {
    if (nameList.hidden) { return; }
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      highlight(active + (e.key === "ArrowDown" ? 1 : -1));
    } else if (e.key === "Enter") {
      e.preventDefault();
      var items = nameList._items || [];
      if (items[active]) { pick(items[active]); }
    } else if (e.key === "Escape") {
      e.preventDefault();
      closeList();
    }
  });

  nameInput.addEventListener("blur", function () {
    closeList();
    // Leaving the field keeps only a real choice: free text that was not picked is cleared.
    if (!chosenRef) { nameInput.value = ""; }
  });

  nameInput.addEventListener("input", function () {
    chosenRef = ""; // typing again invalidates an earlier choice
    clearTimeout(timer);
    var q = nameInput.value.trim();
    if (q.length < MIN_LETTERS) { closeList(); return; }
    timer = setTimeout(function () {
      fetch("/sofi/api/names?q=" + encodeURIComponent(q), { credentials: "same-origin" })
        .then(function (res) { return res.ok ? res.json() : Promise.reject(res); })
        .then(function (items) { showError(""); renderList(items); })
        .catch(function () { closeList(); showError(msg.msgSearchDown); });
    }, SEARCH_DELAY_MS);
  });

  // ---- date of birth: typed as dd/mm/yyyy (slashes added as you type) or picked -----
  function two(n) { return (n < 10 ? "0" : "") + n; }

  function todayISO() {
    var d = new Date();
    return d.getFullYear() + "-" + two(d.getMonth() + 1) + "-" + two(d.getDate());
  }

  // "14/05/1990" -> "1990-05-14", or "" when it is not a real date in the past.
  function parseDOB(text) {
    var m = /^(\d{2})\/(\d{2})\/(\d{4})$/.exec(text.trim());
    if (!m) { return ""; }
    var d = +m[1], mo = +m[2], y = +m[3];
    var check = new Date(y, mo - 1, d);
    if (check.getFullYear() !== y || check.getMonth() !== mo - 1 || check.getDate() !== d) { return ""; } // 31/02
    var iso = y + "-" + two(mo) + "-" + two(d);
    return y >= MIN_YEAR && iso <= todayISO() ? iso : "";
  }

  function showISO(iso) {
    var m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso);
    return m ? m[3] + "/" + m[2] + "/" + m[1] : "";
  }

  function digitsOf(text) { return text.replace(/\D/g, "").slice(0, 8); }

  function slashed(digits) {
    var out = digits.slice(0, 2);
    if (digits.length > 2) { out += "/" + digits.slice(2, 4); }
    if (digits.length > 4) { out += "/" + digits.slice(4); }
    return out;
  }

  var lastDigits = "";
  dobInput.addEventListener("input", function (e) {
    var raw = dobInput.value;
    var iso = /^(\d{4})-(\d{2})-(\d{2})$/.exec(raw.trim()); // a pasted yyyy-mm-dd
    var digits = iso ? iso[3] + iso[2] + iso[1] : digitsOf(raw);
    // Backspace over a slash removes only the slash, which would put it straight back: take the digit before it.
    if (e.inputType && e.inputType.indexOf("delete") === 0 && digits === lastDigits && lastDigits.length > 0 && dobInput.selectionStart === dobInput.value.length) {
      digits = digits.slice(0, -1);
    }
    lastDigits = digits;
    dobInput.value = slashed(digits);
  });

  dobPick.addEventListener("click", function () {
    dobNative.max = todayISO();
    dobNative.min = MIN_YEAR + "-01-01";
    dobNative.value = parseDOB(dobInput.value);
    try { dobNative.showPicker(); } catch (err) { dobNative.focus(); dobNative.click(); }
  });
  dobNative.addEventListener("change", function () {
    if (!dobNative.value) { return; }
    dobInput.value = showISO(dobNative.value);
    lastDigits = digitsOf(dobInput.value);
    showError("");
  });

  // ---- sign in: one generic message for any failure -------------------------------
  form.addEventListener("submit", function (e) {
    e.preventDefault();
    showError("");
    if (!chosenRef) { showError(msg.msgChooseName); return; }
    var dob = parseDOB(dobInput.value);
    if (!dob) { showError(msg.msgBadDate); dobInput.focus(); return; }
    postJSON("/sofi/api/signin", { ref: chosenRef, dob: dob }).then(function (r) {
      if (!r.ok) {
        showError(r.data.error || msg.msgNotFound);
        chosenRef = "";
        nameInput.value = "";
        dobInput.value = ""; // never keep a failed date of birth on screen
        lastDigits = "";
        return;
      }
      location.href = r.data.redirect || "/sofi/start";
    });
  });
})();
