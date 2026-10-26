// SOC sign-in page: Cloudflare tick-box -> name search -> date of birth -> sign in.
// The server does every check; this script only moves the visitor through the steps.
(function () {
  "use strict";

  var box = document.getElementById("turnstile-box");
  var stepCheck = document.getElementById("step-check");
  var form = document.getElementById("signin-form");
  var errEl = document.getElementById("soc-error");
  var nameInput = document.getElementById("name-input");
  var nameList = document.getElementById("name-list");
  var dobInput = document.getElementById("dob-input");
  var dobNative = document.getElementById("dob-native");
  var dobPick = document.getElementById("dob-pick");
  var chosenRef = "";
  var timer = null;
  // The texts come from the page, already in the visitor's language.
  var msg = form.dataset;

  function showError(msg) {
    errEl.textContent = msg;
    errEl.style.display = msg ? "block" : "none";
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

  // Step 1: the check. Nothing else is shown until the server accepts the token.
  function onToken(token) {
    showError("");
    postJSON("/soc/api/check", { token: token }).then(function (r) {
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
    // Development stub only (SOC_DEV_STUBS=1): a button stands in for Cloudflare.
    var btn = document.createElement("button");
    btn.type = "button";
    btn.className = "btn-ghost";
    btn.textContent = "Development check (stub)"; // development only, never shown to visitors
    btn.addEventListener("click", function () { onToken("dev-pass"); });
    box.appendChild(btn);
  } else {
    var tries = 0;
    var wait = setInterval(function () {
      tries++;
      if (window.turnstile) {
        clearInterval(wait);
        window.turnstile.render(box, { sitekey: box.dataset.sitekey, callback: onToken, language: box.dataset.lang });
      } else if (tries > 100) {
        clearInterval(wait);
        showError(msg.msgCheckLoad);
      }
    }, 100);
  }

  // Step 2: searchable dropdown, same look and keys as the Medpost combo (static/combo.js).
  // It only asks the server after 3 letters, and only a listed name can be chosen.
  var active = -1;

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

  function pick(it) {
    chosenRef = it.ref;
    nameInput.value = it.label;
    closeList();
    dobInput.focus();
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
    if (q.length < 3) { closeList(); return; }
    timer = setTimeout(function () {
      fetch("/soc/api/names?q=" + encodeURIComponent(q), { credentials: "same-origin" })
        .then(function (res) { return res.ok ? res.json() : Promise.reject(res); })
        .then(renderList)
        .catch(function () { closeList(); showError(msg.msgSearchDown); });
    }, 250);
  });

  // Date of birth: typed as dd/mm/yyyy (slashes are added as you type), or picked from the browser's
  // own calendar with the button. The server still gets yyyy-mm-dd and does the real check.
  var MIN_YEAR = 1900;

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

  // "1990-05-14" -> "14/05/1990"
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

  // The calendar button opens the browser's date picker; a chosen day fills the text field.
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

  // Steps 3 and 4: date of birth and sign in. One generic message for any failure.
  form.addEventListener("submit", function (e) {
    e.preventDefault();
    showError("");
    if (!chosenRef) { showError(msg.msgChooseName); return; }
    var dob = parseDOB(dobInput.value);
    if (!dob) { showError(msg.msgBadDate); dobInput.focus(); return; }
    postJSON("/soc/api/signin", { ref: chosenRef, dob: dob }).then(function (r) {
      if (!r.ok) {
        showError(r.data.error || msg.msgNotFound);
        chosenRef = "";
        nameInput.value = "";
        dobInput.value = ""; // never keep a failed date of birth on screen
        lastDigits = "";
        return;
      }
      location.href = r.data.redirect || "/soc/new";
    });
  });
})();
