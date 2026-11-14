/* Entry Permit form helpers. Progressive enhancement only: with scripts off the forms still work,
   because the server reads the same shapes (internal/dates CleanTyped) and checks everything again.
   The helpers are pure functions so web/jstest can test them with Node; the page wiring is at the end. */
(function (root, factory) {
  'use strict';
  var api = factory();
  if (typeof module === 'object' && module.exports) {
    module.exports = api;
  } else if (root.document) {
    root.EntryInputs = api;
    api.enhance(root.document);
  }
})(typeof self !== 'undefined' ? self : this, function () {
  'use strict';

  var MAX = [2, 2, 4]; // digits in day, month, year
  var SEPARATORS = '/-. ';

  function pad2(s) { s = String(s); return s.length < 2 ? '0' + s : s; }

  // formatDate builds dd/mm/yyyy while a person types. Digits fill the day, then the month, then the
  // year, and the slash appears by itself; a typed separator ends the group early (so 1/2/2020 works).
  // A trailing slash is kept only when it was typed, so Backspace can always remove it.
  function formatDate(raw) {
    var groups = [''];
    String(raw).split('').forEach(function (ch) {
      var g = groups.length - 1;
      if (ch >= '0' && ch <= '9') {
        if (groups[g].length < MAX[g]) groups[g] += ch;
        else if (g < 2) groups.push(ch);
        // a full year ignores further digits
      } else if (SEPARATORS.indexOf(ch) >= 0) {
        if (groups[g] !== '' && g < 2) groups.push('');
      }
    });
    return groups.join('/');
  }

  // maskRest is what is still blank in the mask __/__/____ after what has been typed, so the box can
  // show "24/0" followed by the faint rest "_/____". A finished date has nothing left to fill.
  function maskRest(value) {
    var v = String(value);
    if (v === '') return '__/__/____';
    var groups = v.split('/');
    var last = Math.min(groups.length, 3) - 1;
    var rest = new Array(Math.max(0, MAX[last] - groups[last].length) + 1).join('_');
    for (var g = last + 1; g < 3; g++) rest += '/' + new Array(MAX[g] + 1).join('_');
    return rest;
  }

  function realDate(d, m, y) {
    if (!(y >= 1900 && y <= 2200 && m >= 1 && m <= 12 && d >= 1)) return false;
    var t = new Date(Date.UTC(y, m - 1, d));
    return t.getUTCFullYear() === y && t.getUTCMonth() === m - 1 && t.getUTCDate() === d;
  }

  // tidyDate pads a short day or month (1/2/2020 -> 01/02/2020) and says whether it is a real date.
  // A blank is fine here: "required" decides whether a blank is allowed.
  function tidyDate(text) {
    var t = formatDate(text);
    if (t === '') return { text: '', ok: true };
    var p = t.split('/');
    if (p.length !== 3 || p[0] === '' || p[1] === '' || p[2].length !== 4) return { text: t, ok: false };
    if (!realDate(+p[0], +p[1], +p[2])) return { text: t, ok: false };
    return { text: pad2(p[0]) + '/' + pad2(p[1]) + '/' + p[2], ok: true };
  }

  function toISO(text) {
    var r = tidyDate(text);
    if (!r.ok || r.text === '') return '';
    var p = r.text.split('/');
    return p[2] + '-' + p[1] + '-' + p[0];
  }

  function fromISO(iso) {
    var m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(String(iso));
    return m ? m[3] + '/' + m[2] + '/' + m[1] : '';
  }

  // isFuture is only about a finished, real date; an unfinished one is not judged here.
  function isFuture(text, todayISO) {
    var iso = toISO(text);
    return iso !== '' && iso > todayISO;
  }

  // cleanPhone keeps the digits and one leading plus, so "+62 812-3456 (789)" becomes +628123456789.
  function cleanPhone(raw) {
    var s = String(raw).trim();
    var d = s.replace(/\D/g, '');
    return d === '' ? '' : (s.charAt(0) === '+' ? '+' : '') + d;
  }

  function digitsOnly(raw) { return String(raw).replace(/\D/g, ''); }
  function tidyText(raw) { return String(raw).replace(/\s+/g, ' ').trim(); }
  // tidyEmail mirrors internal/textclean Email: invisible characters and every space out, lower case,
  // full-width @ and dots made plain, and "mailto:", brackets, quotes and trailing punctuation dropped.
  function tidyEmail(raw) {
    var s = String(raw).replace(/[\u200b\u200c\u200d\ufeff\u00ad\u2060\s]/g, '')
      .replace(/\uff20/g, '@').replace(/[\u3002\uff0e]/g, '.').toLowerCase();
    return s.replace(/^mailto:/, '').replace(/^[<"'`(\[]+/, '').replace(/[>"'`)\].,;:]+$/, '');
  }

  var PARTICLES = /^(bin|binti|bte|binte|van|von|de|del|della|da|dos|das|di|du|la|le|ter|ten|al|el)$/;
  var ROMAN = /^(ii|iii|iv|vi|vii|viii|ix)$/;
  function titleWord(w, first) {
    var lower = w.toLowerCase();
    var core = lower.replace(/^[.,]+|[.,]+$/g, '');
    if (!first && PARTICLES.test(core)) return lower;
    if (ROMAN.test(core)) return w.toUpperCase();
    if (/^\p{Lu}/u.test(w) && /\p{Ll}\p{Lu}/u.test(w.slice(1)) && w.length >= 3) return w; // McDonald, DeShawn: as typed
    var up = true;
    return Array.from(lower).map(function (ch) {
      var out = up && /\p{L}/u.test(ch) ? ch.toUpperCase() : ch;
      if (up && /\p{L}/u.test(ch)) up = false;
      if (ch === '-' || ch === "'" || ch === '.') up = true;
      return out;
    }).join('');
  }
  // tidyName mirrors internal/textclean Name: letters, spaces ' - . , only; curly quotes and long dashes made
  // plain; repeated spaces and punctuation collapsed; Title Case with small particles, initials and numerals kept.
  function tidyName(raw) {
    var s = String(raw).replace(/[\u200b\u200c\u200d\ufeff\u00ad\u2060]/g, '')
      .replace(/[\u2018\u2019`´]/g, "'").replace(/[\u2013\u2014]/g, '-')
      .replace(/[^\p{L}\p{Mn}\s'.,-]/gu, ' ');
    ['..', ',,', '--', "''"].forEach(function (p) { while (s.indexOf(p) >= 0) s = s.split(p).join(p.charAt(0)); });
    s = s.replace(/\s+/g, ' ').replace(/ ,/g, ',').replace(/^[ .,'-]+|[ .,'-]+$/g, '');
    if (!s) return '';
    return s.split(' ').map(function (w, i) { return titleWord(w, i === 0); }).join(' ');
  }

  // idRule mirrors the server (internal/waiver CleanID): an Indonesian gives the 16-digit NIK, anyone
  // else a passport number. Before a nationality is chosen nothing is enforced; the server decides.
  function idRule(nationality) {
    var n = String(nationality).trim().toLowerCase();
    if (n === '') return { kind: 'any', label: 'NIK or passport number', maxlength: 24, numeric: false };
    if (n === 'indonesia') return { kind: 'nik', label: 'NIK (16 digits)', maxlength: 16, numeric: true };
    return { kind: 'passport', label: 'Passport number (7 to 9 letters or numbers)', maxlength: 9, numeric: false };
  }

  // cleanId keeps only what that kind of id allows and cuts it to length.
  function cleanId(kind, raw) {
    var s = String(raw);
    if (kind === 'nik') return digitsOnly(s).slice(0, 16);
    var limit = kind === 'passport' ? 9 : 24;
    return s.replace(/[^A-Za-z0-9]/g, '').toUpperCase().slice(0, limit);
  }

  var api = {
    formatDate: formatDate, maskRest: maskRest, tidyDate: tidyDate, toISO: toISO, fromISO: fromISO, isFuture: isFuture,
    cleanPhone: cleanPhone, digitsOnly: digitsOnly, tidyText: tidyText, tidyEmail: tidyEmail, tidyName: tidyName,
    idRule: idRule, cleanId: cleanId,
  };

  // ---- page wiring (browser only) ----

  function todayISO() {
    var d = new Date();
    return d.getFullYear() + '-' + pad2(d.getMonth() + 1) + '-' + pad2(d.getDate());
  }

  var CALENDAR_ICON = '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" ' +
    'stroke-width="1.8" aria-hidden="true"><rect x="3.5" y="5" width="17" height="15.5"/>' +
    '<path d="M3.5 10h17M8 3v4M16 3v4"/></svg>';

  // setupDate makes a text date box type-friendly (24071983 -> 24/07/1983) and adds a calendar button
  // that opens the browser's date picker and writes the pick back as dd/mm/yyyy.
  function setupDate(input) {
    if (input.dataset.dateReady) return;
    input.dataset.dateReady = '1';
    var isBirth = input.dataset.date === 'dob';
    var wrap = document.createElement('span');
    wrap.className = 'date-field';
    input.parentNode.insertBefore(wrap, input);
    wrap.appendChild(input);

    // The faint mask: the typed part is invisible (the real box draws it), the rest shows the blanks.
    var ghost = document.createElement('span');
    ghost.className = 'date-ghost';
    ghost.setAttribute('aria-hidden', 'true');
    var typed = document.createElement('i');
    typed.className = 'g-typed';
    var blanks = document.createElement('i');
    blanks.className = 'g-rest';
    ghost.appendChild(typed);
    ghost.appendChild(blanks);
    wrap.insertBefore(ghost, input);
    input.placeholder = '';
    function showMask() {
      typed.textContent = input.value;
      blanks.textContent = maskRest(input.value);
    }

    var native = document.createElement('input');
    native.type = 'date';
    native.className = 'date-native';
    native.tabIndex = -1;
    native.setAttribute('aria-hidden', 'true');
    if (isBirth) native.max = todayISO();

    var pick = document.createElement('button');
    pick.type = 'button';
    pick.className = 'date-pick';
    pick.title = 'Pick a date';
    pick.setAttribute('aria-label', 'Pick a date');
    pick.innerHTML = CALENDAR_ICON;
    wrap.appendChild(pick);
    wrap.appendChild(native);

    function check(final) {
      var r = tidyDate(input.value);
      if (final && r.ok && r.text !== input.value) input.value = r.text;
      var message = '';
      if (input.value !== '' && !r.ok) message = 'Enter a real date as dd/mm/yyyy, for example 24/07/1983.';
      else if (isBirth && r.ok && isFuture(r.text, todayISO())) message = 'A date of birth cannot be in the future.';
      input.setCustomValidity(message);
      showMask();
    }
    input.addEventListener('input', function () {
      var f = formatDate(input.value);
      if (f !== input.value) input.value = f;
      check(false);
    });
    input.addEventListener('blur', function () { check(true); });
    pick.addEventListener('click', function () {
      var iso = toISO(input.value);
      if (iso) native.value = iso;
      if (typeof native.showPicker === 'function') native.showPicker();
      else { native.focus(); native.click(); }
    });
    native.addEventListener('change', function () {
      input.value = fromISO(native.value);
      check(true);
      input.dispatchEvent(new Event('input', { bubbles: true }));
      input.focus();
    });
    check(false);
  }

  function onBlur(el, tidy) {
    el.addEventListener('blur', function () {
      var v = tidy(el.value);
      if (v !== el.value) el.value = v;
    });
  }

  function each(doc, selector, fn) {
    Array.prototype.forEach.call(doc.querySelectorAll(selector), fn);
  }

  // setupId makes an id box follow the nationality control it names in data-id-by.
  function setupId(input) {
    var source = document.getElementById(input.dataset.idBy);
    if (!source) return;
    var label = document.getElementById(input.id + '-label');
    var rule = idRule(source.value);
    function apply() {
      rule = idRule(source.value);
      input.maxLength = rule.maxlength;
      input.inputMode = rule.numeric ? 'numeric' : 'text';
      if (label) label.textContent = rule.label;
      var v = cleanId(rule.kind, input.value);
      if (v !== input.value) input.value = v;
    }
    source.addEventListener('change', apply);
    source.addEventListener('input', apply);
    input.addEventListener('input', function () {
      var v = cleanId(rule.kind, input.value);
      if (v !== input.value) input.value = v;
    });
    apply();
  }

  // setupAutoSubmit sends a form as soon as its file is chosen, so a lone "upload" button is not needed.
  function setupAutoSubmit(input) {
    input.addEventListener('change', function () {
      if (input.files && input.files.length && input.form) {
        if (typeof input.form.requestSubmit === 'function') input.form.requestSubmit();
        else input.form.submit();
      }
    });
  }

  // setupAutoForm applies a filter form as soon as a pick-list, tick box or day is changed. The
  // search box is left alone (it has its own button), and without script the button still works.
  function setupAutoForm(form) {
    form.addEventListener('change', function (e) {
      var t = e.target;
      if (t && (t.tagName === 'SELECT' || t.type === 'checkbox' || t.type === 'date')) {
        if (typeof form.requestSubmit === 'function') form.requestSubmit();
        else form.submit();
      }
    });
  }

  // setupMxCheck asks the server whether an address can receive mail as soon as the person leaves the box
  // (not only when the form is sent). A bad domain is written under the box and also set as the box's own
  // validity message, so Next and Send stop there until it is fixed. A failed or slow lookup says nothing:
  // the server checks again on submit.
  function setupMxCheck(input) {
    var url = input.getAttribute('data-mxcheck');
    var note = document.createElement('small');
    note.className = 'field-error';
    note.setAttribute('role', 'alert');
    note.hidden = true;
    input.insertAdjacentElement('afterend', note);
    var known = {};
    var last = '';
    // show writes the note under the box: a red message for a bad domain, a green "can receive email" when the
    // domain passed (ok), or just the did-you-mean hint.
    function show(msg, suggest, ok) {
      var good = ok && !msg && !suggest;
      note.textContent = msg || (good ? 'This domain can receive email.' : '');
      note.className = good ? 'field-error field-ok' : 'field-error';
      note.setAttribute('role', msg ? 'alert' : 'status');
      input.classList.toggle('is-ok', !!good);
      if (suggest) {
        var btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'linklike';
        btn.textContent = 'Use ' + suggest;
        btn.addEventListener('click', function () {
          input.value = suggest;
          show('');
          check();
        });
        note.appendChild(document.createTextNode(msg ? ' Did you mean ' : 'Did you mean '));
        note.appendChild(btn);
        note.appendChild(document.createTextNode('?'));
      }
      note.hidden = !msg && !suggest && !good;
      input.setCustomValidity(msg || '');
      if (msg) input.setAttribute('aria-invalid', 'true'); else input.removeAttribute('aria-invalid');
    }
    function apply(v, res) {
      if (tidyEmail(input.value) !== v) return;
      var bad = res && !res.ok;
      show(bad ? res.message : '', res && res.suggest, res && res.ok);
      if (!bad) input.setCustomValidity('');
    }
    function check() {
      var v = tidyEmail(input.value);
      last = v;
      if (!v || input.validity.typeMismatch) { show(''); return; } // empty, or the browser already complains
      if (known[v]) { apply(v, known[v]); return; }
      if (typeof fetch !== 'function') return;
      fetch(url + '?email=' + encodeURIComponent(v), { credentials: 'same-origin' })
        .then(function (r) { return r.ok ? r.json() : null; })
        .then(function (res) { if (res) { known[v] = res; if (last === v) apply(v, res); } })
        .catch(function () {});
    }
    input.addEventListener('blur', check);
    input.addEventListener('input', function () { if (note.hidden === false) show(''); });
  }

  // setupSort lets a click on any header of a table.sortable order its rows, ascending first and
  // descending on the next click. Numbers sort as numbers, dates and text as text.
  function setupSort(table) {
    var body = table.tBodies[0];
    if (!body) return;
    var heads = table.tHead ? table.tHead.rows[0].cells : [];
    Array.prototype.forEach.call(heads, function (th, col) {
      th.setAttribute('tabindex', '0');
      th.setAttribute('aria-sort', 'none');
      function sort() {
        var dir = th.getAttribute('aria-sort') === 'ascending' ? -1 : 1;
        Array.prototype.forEach.call(heads, function (h) { h.setAttribute('aria-sort', 'none'); });
        th.setAttribute('aria-sort', dir === 1 ? 'ascending' : 'descending');
        var rows = Array.prototype.slice.call(body.rows);
        var cell = function (r) { return (r.cells[col] ? r.cells[col].textContent : '').trim(); };
        rows.sort(function (a, b) {
          var x = cell(a), y = cell(b);
          if (x === '' || y === '') return x === y ? 0 : (x === '' ? 1 : -1); // blanks last
          var nx = parseFloat(x), ny = parseFloat(y);
          if (/^-?[\d.]+$/.test(x) && /^-?[\d.]+$/.test(y)) return (nx - ny) * dir;
          return x.localeCompare(y, undefined, { numeric: true, sensitivity: 'base' }) * dir;
        });
        rows.forEach(function (r) { body.appendChild(r); });
      }
      th.addEventListener('click', sort);
      th.addEventListener('keydown', function (e) {
        if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); sort(); }
      });
    });
  }

  api.enhance = function (doc) {
    each(doc, 'table.sortable', setupSort);
    each(doc, 'form[data-autoform]', setupAutoForm);
    each(doc, 'input[data-autosubmit]', setupAutoSubmit);
    // On a phone the left menu folds behind a "Menu" bar that names the current page; on a wide screen
    // it stays open. Without this script it simply stays open.
    var win = doc.defaultView;
    var menu = doc.querySelector && doc.querySelector('.side-details');
    if (menu && win && win.matchMedia) {
      var cur = menu.querySelector('a[aria-current="page"]');
      var shown = menu.querySelector('.side-current');
      // The current page's name; on a page with no menu item (an error page) fall back to its title.
      if (shown) shown.textContent = cur ? (cur.firstChild ? cur.firstChild.textContent : '').trim() : String(doc.title || '').split(' \u00b7 ')[0];
      var narrow = win.matchMedia('(max-width: 860px)');
      var fit = function () { menu.open = !narrow.matches; };
      fit();
      if (narrow.addEventListener) narrow.addEventListener('change', fit);
    }
    each(doc, 'input[data-id-by]', setupId);
    each(doc, 'input[data-date]', setupDate);
    each(doc, 'input[type="tel"]', function (el) { onBlur(el, cleanPhone); });
    each(doc, 'input[type="email"]', function (el) { onBlur(el, tidyEmail); });
    each(doc, 'input[data-mxcheck]', setupMxCheck);
    each(doc, 'input[data-digits]', function (el) {
      el.addEventListener('input', function () {
        var v = digitsOnly(el.value);
        if (v !== el.value) el.value = v;
      });
    });
    each(doc, 'input[data-name]', function (el) { onBlur(el, tidyName); });
    each(doc, 'input[type="text"]:not([data-date]):not([data-digits]):not([data-id-by]):not([data-name])', function (el) { onBlur(el, tidyText); });
  };

  return api;
});
