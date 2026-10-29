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

  // Review is the normal way in: it asks first, marks the document Reviewed so a colleague can see it
  // is taken, then opens the checkup form filled in from the submission (mcu.js). That form closes
  // the document when the record is saved, so nobody types a Checkup ID.
  // Times are stored as UTC text "YYYY-MM-DDTHH:MM:SS"; show them with a space and say UTC.
  function when(t) { return t ? t.replace('T', ' ') + ' UTC' : ''; }

  function reviewPrompt(d) {
    if (d.reviewedBy || d.reviewedAt) {
      return (d.reviewedBy || 'Someone') + ' started reviewing ' + d.worker + (d.reviewedAt ? ' on ' + when(d.reviewedAt) : '') +
        '. Open it anyway?';
    }
    return 'Review ' + d.worker + "'s checkup now? This opens the checkup form filled in from the submission.";
  }

  function startReview(d) {
    if (!window.confirm(reviewPrompt(d))) return;
    var open = function () { window.location.href = '/mcu?submission=' + encodeURIComponent(d.id); };
    if (d.status !== 'Pending') { open(); return; }
    call('/api/mcu-submissions/' + d.id + '/review', 'POST').then(open).catch(function (e) {
      // Someone else marked it a moment ago: not an error for the reviewer, just reload the list.
      say(e.message, true);
      load();
    });
  }

  function actions(d, td) {
    var box = document.createElement('div');
    box.className = 'ms-act';
    if (d.status === 'Pending' || d.status === 'Reviewed') {
      var go = document.createElement('button');
      go.type = 'button'; go.className = 'btn-primary'; go.textContent = 'Review';
      go.setAttribute('aria-label', 'Review the checkup of ' + d.worker);
      go.addEventListener('click', function () { startReview(d); });
      box.appendChild(go);

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

      // For a record that was entered by hand before the document was opened from here.
      var more = document.createElement('details');
      var sum = document.createElement('summary');
      sum.textContent = 'Link an existing record';
      more.appendChild(sum);
      var rec = document.createElement('input');
      rec.type = 'text'; rec.maxLength = 64; rec.placeholder = 'Checkup ID';
      rec.setAttribute('aria-label', 'Checkup ID of the record made for ' + d.worker);
      var close = document.createElement('button');
      close.type = 'button'; close.className = 'btn-ghost'; close.textContent = 'Close with record';
      close.addEventListener('click', function () {
        if (!rec.value.trim()) { say('Enter the Checkup ID of the record first.', true); rec.focus(); return; }
        call('/api/mcu-submissions/' + d.id + '/close', 'POST', { checkUpId: rec.value })
          .then(load).catch(function (e) { say(e.message, true); });
      });
      more.appendChild(rec); more.appendChild(close);
      box.appendChild(more);
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
      cell(tr, d.reviewedBy || d.reviewedAt ? [d.reviewedBy, when(d.reviewedAt)].filter(Boolean).join(', ') : '');
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
