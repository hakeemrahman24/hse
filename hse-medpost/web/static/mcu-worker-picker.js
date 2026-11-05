// Worker picker for /mcu-submit/batch. The submitter searches the patient list and chooses a
// worker instead of typing name and date of birth, so details cannot be misspelled. The chosen id
// goes in a hidden field; the server reads the details from the database and ignores typed values.
// Results are written with textContent only, never innerHTML, so a name cannot inject markup.
(function () {
  'use strict';
  var box = document.getElementById('picker');
  if (!box) return;
  var q = document.getElementById('worker-q');
  var list = document.getElementById('worker-results');
  var hidden = document.getElementById('patient-id');
  var chosen = document.getElementById('worker-chosen');
  var clear = document.getElementById('worker-clear');
  var fresh = document.getElementById('new-employee');
  var timer = null;
  var latest = 0;

  function show(worker) {
    hidden.value = worker.id;
    chosen.textContent = worker.name + ' · born ' + worker.dob + ' · ' + (worker.position || 'no position') + ' · ' + (worker.company || 'no company');
    chosen.hidden = false; clear.hidden = false;
    q.hidden = true; list.textContent = '';
    if (fresh) fresh.open = false;
  }

  function reset() {
    hidden.value = ''; chosen.hidden = true; clear.hidden = true;
    q.hidden = false; q.value = ''; q.focus();
  }

  // The sentence for a worker who cannot be chosen, from the page's hidden (translated) messages.
  function reason(w) {
    if (!w.blocked) return '';
    var el = document.querySelector('#picker-msgs [data-m="' + w.blocked + '"]');
    if (!el) return '';
    return el.textContent.trim().replace('{last}', w.last || '').replace('{until}', w.until || '').replace('{from}', w.from || '');
  }

  function render(workers) {
    list.textContent = '';
    workers.forEach(function (w) {
      var li = document.createElement('li');
      var b = document.createElement('button');
      b.type = 'button';
      b.textContent = w.name;
      var small = document.createElement('small');
      small.textContent = 'born ' + w.dob + ' · ' + (w.position || 'no position') + ' · ' + (w.company || 'no company');
      b.appendChild(small);
      var why = reason(w);
      if (why) {
        // Cannot be chosen: a second document for the same checkup (the server refuses it too).
        b.disabled = true;
        li.className = 'mp-blocked';
        var note = document.createElement('small');
        note.className = 'mp-blocked-why';
        note.textContent = why;
        b.appendChild(note);
      } else {
        b.addEventListener('click', function () { show(w); });
      }
      li.appendChild(b);
      list.appendChild(li);
    });
    if (!workers.length && q.value.trim().length >= 3) {
      var none = document.createElement('li');
      none.className = 'mp-hint';
      none.textContent = 'No worker found. Use the new employee form below.';
      list.appendChild(none);
    }
  }

  function search() {
    var term = q.value.trim();
    if (term.length < 3) { list.textContent = ''; return; }
    var mine = ++latest;
    fetch(box.dataset.search + '?q=' + encodeURIComponent(term), { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : []; })
      .then(function (data) { if (mine === latest) render(data); })
      .catch(function () { if (mine === latest) list.textContent = ''; });
  }

  q.addEventListener('input', function () { clearTimeout(timer); timer = setTimeout(search, 250); });
  clear.addEventListener('click', reset);
  if (hidden.value) { chosen.textContent = 'Worker chosen. Press "Choose another worker" to change it.'; chosen.hidden = false; clear.hidden = false; q.hidden = true; }
})();
