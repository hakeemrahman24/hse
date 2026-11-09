// NIK / passport field on the New batch page (<input id="id_number">). What can be typed depends on
// the nationality chosen above it (owner, 2026-11-09):
//   Indonesia        -> digits only, at most 16 (the NIK)
//   any other nation -> capital letters and digits only, at most 12 (the passport number)
//   none chosen yet  -> capitals and digits, at most 16, until the nationality tells which one
// Wrong characters are dropped as they are typed or pasted, and the hint under the box says which
// kind of number is expected. This mirrors parseIdentity in internal/mcusubmit/web_upload.go
// (16 digits / 6 to 12 letters or digits); the server runs the same rule again and is the one that
// counts, so a browser without JavaScript is still checked.
(function () {
  'use strict';
  var nat = document.getElementById('nationality');
  var id = document.getElementById('id_number');
  var hint = document.getElementById('id-hint');
  var box = document.getElementById('id-msgs');
  if (!nat || !id) return;

  function msg(key) {
    var el = box && box.querySelector('[data-m="' + key + '"]');
    return el ? el.textContent.trim() : '';
  }

  // The nationality names that mean an Indonesian citizen; the same two the server accepts.
  function kind() {
    var n = (nat.value || '').trim().toLowerCase();
    if (!n) return 'any';
    return n === 'indonesia' || n === 'indonesian' ? 'nik' : 'passport';
  }

  function allowed(raw, k) {
    if (k === 'nik') return raw.replace(/\D/g, '').slice(0, 16);
    var caps = raw.toUpperCase().replace(/[^A-Z0-9]/g, '');
    return caps.slice(0, k === 'passport' ? 12 : 16);
  }

  // Rewrites the value and keeps the caret where the person was typing, counting only the allowed
  // characters before it, so fixing a typo in the middle does not throw the caret to the end.
  function clean() {
    var k = kind();
    var before = id.value;
    var caret = id.selectionStart == null ? before.length : id.selectionStart;
    var after = allowed(before, k);
    if (after === before) return;
    var keptBeforeCaret = allowed(before.slice(0, caret), k).length;
    id.value = after;
    try { id.setSelectionRange(keptBeforeCaret, keptBeforeCaret); } catch (e) { /* some types refuse */ }
  }

  function apply() {
    var k = kind();
    id.maxLength = k === 'passport' ? 12 : 16;
    id.inputMode = k === 'nik' ? 'numeric' : 'text';
    id.autocapitalize = k === 'nik' ? 'off' : 'characters';
    id.placeholder = msg('ph' + k);
    if (hint) hint.textContent = msg('hint' + k);
    clean();
  }

  id.addEventListener('input', clean);
  nat.addEventListener('change', apply);
  apply();
})();
