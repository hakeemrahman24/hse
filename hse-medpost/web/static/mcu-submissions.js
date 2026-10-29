// Staff review list for medical checkups sent through the submission portal (/mcu-submissions).
// Everything from the server is written with textContent, never innerHTML, so a submitted name or
// reason can never inject markup into this page.
(function () {
  'use strict';
  var rows = document.getElementById('ms-rows');
  var filter = document.getElementById('ms-status');
  var msg = document.getElementById('ms-msg');

  function say(text, isError) {
    msg.textContent = text || '';
    msg.className = 'ms-msg' + (isError ? ' err' : '');
  }

  function cell(tr, text) {
    var td = document.createElement('td');
    td.textContent = text || '';
    tr.appendChild(td);
    return td;
  }

  function call(path, method, body) {
    var opts = { method: method, credentials: 'same-origin', headers: {} };
    if (body) { opts.headers['Content-Type'] = 'application/json'; opts.body = JSON.stringify(body); }
    return fetch(path, opts).then(function (r) {
      if (r.ok) return r;
      return r.text().then(function (t) { throw new Error(t.trim() || ('Error ' + r.status)); });
    });
  }

  function actions(d, td) {
    var box = document.createElement('div');
    box.className = 'ms-act';
    if (d.status === 'Pending') {
      var ok = document.createElement('button');
      ok.type = 'button'; ok.className = 'btn-primary'; ok.textContent = 'Mark reviewed';
      ok.addEventListener('click', function () {
        call('/api/mcu-submissions/' + d.id + '/review', 'POST').then(load).catch(function (e) { say(e.message, true); });
      });
      box.appendChild(ok);
    }
    if (d.status === 'Pending' || d.status === 'Reviewed') {
      // The checkup record itself is made on the Medical Checkups page (for a new employee, the
      // patient too). Typing its Checkup ID here links it and closes this document.
      var rec = document.createElement('input');
      rec.type = 'text'; rec.maxLength = 64; rec.placeholder = 'Checkup ID';
      rec.setAttribute('aria-label', 'Checkup ID of the record made for ' + d.worker);
      var close = document.createElement('button');
      close.type = 'button'; close.className = 'btn-primary'; close.textContent = 'Close with record';
      close.addEventListener('click', function () {
        if (!rec.value.trim()) { say('Create the checkup record first, then enter its Checkup ID.', true); rec.focus(); return; }
        call('/api/mcu-submissions/' + d.id + '/close', 'POST', { checkUpId: rec.value })
          .then(load).catch(function (e) { say(e.message, true); });
      });
      box.appendChild(rec); box.appendChild(close);
      var reason = document.createElement('input');
      reason.type = 'text'; reason.maxLength = 200; reason.placeholder = 'Reason';
      reason.setAttribute('aria-label', 'Reason for rejecting ' + d.worker);
      var no = document.createElement('button');
      no.type = 'button'; no.className = 'btn-ghost'; no.textContent = 'Reject';
      no.addEventListener('click', function () {
        if (!reason.value.trim()) { say('Enter a reason before rejecting.', true); reason.focus(); return; }
        call('/api/mcu-submissions/' + d.id + '/reject', 'POST', { reason: reason.value })
          .then(load).catch(function (e) { say(e.message, true); });
      });
      box.appendChild(reason); box.appendChild(no);
    }
    td.appendChild(box);
  }

  function render(list) {
    rows.textContent = '';
    list.forEach(function (d) {
      var tr = document.createElement('tr');
      cell(tr, d.sentAt);
      var worker = cell(tr, d.worker);
      if (d.isNewEmployee) {
        var tag = document.createElement('div');
        tag.className = 'ms-new'; tag.textContent = 'New employee (not on file)';
        worker.appendChild(tag);
      }
      cell(tr, d.dob); cell(tr, d.position); cell(tr, d.company); cell(tr, d.submitter);
      var st = cell(tr, '');
      var badge = document.createElement('span');
      badge.className = 'ms-badge'; badge.textContent = d.status;
      st.appendChild(badge);
      if (d.rejectReason) st.appendChild(document.createTextNode(' ' + d.rejectReason));
      var file = cell(tr, '');
      if (d.hasFile) {
        var a = document.createElement('a');
        a.href = '/api/mcu-submissions/' + d.id + '/file';
        a.target = '_blank'; a.rel = 'noopener'; a.textContent = 'Open PDF';
        file.appendChild(a);
      } else {
        file.textContent = 'No file';
      }
      actions(d, cell(tr, ''));
      rows.appendChild(tr);
    });
    if (!list.length) say('No documents with this status.', false); else say('');
  }

  var page = 1;
  var pages = 1;
  var fields = ['worker', 'company', 'submitter', 'from', 'to'];

  // The filter lives in the query string of the request only; every value is encoded, and the
  // server checks the status and the dates again.
  function query() {
    var q = new URLSearchParams();
    if (filter.value) q.set('status', filter.value);
    fields.forEach(function (f) {
      var v = document.getElementById('ms-' + f).value.trim();
      if (v) q.set(f, v);
    });
    if (page > 1) q.set('page', String(page));
    var text = q.toString();
    return text ? '?' + text : '';
  }

  function load() {
    return fetch('/api/mcu-submissions' + query(), { credentials: 'same-origin' })
      .then(function (r) {
        if (!r.ok) return r.text().then(function (t) { throw new Error(t.trim() || ('Could not load the list (' + r.status + ').')); });
        page = parseInt(r.headers.get('X-Page'), 10) || 1;
        pages = parseInt(r.headers.get('X-Pages'), 10) || 1;
        var total = r.headers.get('X-Total-Count') || '0';
        document.getElementById('ms-page').textContent = total + ' documents, page ' + page + ' of ' + pages;
        document.getElementById('ms-prev').disabled = page <= 1;
        document.getElementById('ms-next').disabled = page >= pages;
        return r.json();
      })
      .then(render)
      .catch(function (e) { say(e.message, true); });
  }

  // A new filter always starts again at page 1.
  function apply() { page = 1; load(); }

  filter.addEventListener('change', apply);
  document.getElementById('ms-apply').addEventListener('click', apply);
  document.getElementById('ms-clear').addEventListener('click', function () {
    filter.value = '';
    fields.forEach(function (f) { document.getElementById('ms-' + f).value = ''; });
    apply();
  });
  document.getElementById('ms-prev').addEventListener('click', function () { page = Math.max(1, page - 1); load(); });
  document.getElementById('ms-next').addEventListener('click', function () { page = Math.min(pages, page + 1); load(); });
  load();
})();
