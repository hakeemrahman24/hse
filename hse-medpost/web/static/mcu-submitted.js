// "Submitted" tab on /mcu: the medical checkups companies sent through the submission portal, in the
// same table format as the other tabs. Everything from the server is written with textContent, never
// innerHTML, so a submitted name or reason can never inject markup into this page.
// Review hands over to mcu.js (openSubmission), which opens the checkup form in place; saving that
// form closes the document, so nobody types a Checkup ID.
(function () {
  'use strict';
  var body = document.getElementById('sub-body');
  if (!body) return;
  var statusSel = document.getElementById('sub-status');
  var msg = document.getElementById('sub-msg');
  var fields = ['worker', 'company', 'submitter', 'from', 'to'];
  var page = 1;
  var pages = 1;

  function say(text, isError) {
    msg.textContent = text || '';
    msg.className = 'sub-msg' + (isError ? ' err' : '');
  }

  function cell(tr, text) {
    var td = document.createElement('td');
    td.textContent = text || '';
    tr.appendChild(td);
    return td;
  }

  function call(path, method, payload) {
    var opts = { method: method, credentials: 'same-origin', headers: {} };
    if (payload) { opts.headers['Content-Type'] = 'application/json'; opts.body = JSON.stringify(payload); }
    return fetch(path, opts).then(function (r) {
      if (r.ok) return r;
      return r.text().then(function (t) { throw new Error(t.trim() || ('Error ' + r.status)); });
    });
  }

  // Times are stored as UTC text "YYYY-MM-DDTHH:MM:SS"; show them with a space and say UTC.
  function when(t) { return t ? t.replace('T', ' ') + ' UTC' : ''; }

  function reviewPrompt(d) {
    if (d.reviewedBy || d.reviewedAt) {
      return (d.reviewedBy || 'Someone') + ' started reviewing ' + d.worker + (d.reviewedAt ? ' on ' + when(d.reviewedAt) : '') +
        '. Open it anyway?';
    }
    return 'Review ' + d.worker + "'s checkup now? This opens the checkup form filled in from the submission.";
  }

  // Review asks first, marks a Pending document Reviewed so a colleague can see it is taken, then
  // opens the checkup form in place.
  function startReview(d) {
    if (!window.confirm(reviewPrompt(d))) return;
    if (d.status !== 'Pending') { window.openSubmission(d.id); return; }
    call('/api/mcu-submissions/' + d.id + '/review', 'POST').then(function () {
      window.openSubmission(d.id);
    }).catch(function (e) {
      // Someone else changed it a moment ago: show why and refresh the list.
      say(e.message, true);
      load();
    });
  }

  function button(label, cls, onClick, aria) {
    var b = document.createElement('button');
    b.type = 'button'; b.className = cls; b.textContent = label;
    if (aria) b.setAttribute('aria-label', aria);
    b.addEventListener('click', onClick);
    return b;
  }

  function actions(d, td) {
    if (d.status !== 'Pending' && d.status !== 'Reviewed') return;
    var box = document.createElement('div');
    box.className = 'sub-act';
    box.appendChild(button('Review', 'btn-primary', function () { startReview(d); }, 'Review the checkup of ' + d.worker));

    // Reject is for a file that cannot be used (wrong worker, unreadable), with a reason the submitter sees.
    var more = document.createElement('details');
    var sum = document.createElement('summary');
    sum.textContent = 'More';
    more.appendChild(sum);
    var reason = document.createElement('input');
    reason.type = 'text'; reason.maxLength = 200; reason.placeholder = 'Reason';
    reason.setAttribute('aria-label', 'Reason for rejecting ' + d.worker);
    more.appendChild(reason);
    more.appendChild(button('Reject', 'btn-ghost', function () {
      if (!reason.value.trim()) { say('Enter a reason before rejecting.', true); reason.focus(); return; }
      call('/api/mcu-submissions/' + d.id + '/reject', 'POST', { reason: reason.value })
        .then(load).catch(function (e) { say(e.message, true); });
    }));
    // For a record that was entered by hand before the document was opened from here.
    var rec = document.createElement('input');
    rec.type = 'text'; rec.maxLength = 64; rec.placeholder = 'Checkup ID';
    rec.setAttribute('aria-label', 'Checkup ID of the record made for ' + d.worker);
    more.appendChild(rec);
    more.appendChild(button('Link record', 'btn-ghost', function () {
      if (!rec.value.trim()) { say('Enter the Checkup ID of the record first.', true); rec.focus(); return; }
      call('/api/mcu-submissions/' + d.id + '/close', 'POST', { checkUpId: rec.value })
        .then(load).catch(function (e) { say(e.message, true); });
    }));
    box.appendChild(more);
    td.appendChild(box);
  }

  function render(list) {
    body.textContent = '';
    list.forEach(function (d) {
      var tr = document.createElement('tr');
      cell(tr, when(d.sentAt));
      var worker = cell(tr, d.worker);
      if (d.isNewEmployee) {
        var tag = document.createElement('div');
        tag.className = 'sub-new'; tag.textContent = 'New employee';
        worker.appendChild(tag);
      }
      cell(tr, d.company); cell(tr, d.position); cell(tr, d.submitter);
      var st = cell(tr, '');
      var badge = document.createElement('span');
      badge.className = 'sub-badge'; badge.textContent = d.status;
      st.appendChild(badge);
      if (d.rejectReason) st.appendChild(document.createTextNode(' ' + d.rejectReason));
      cell(tr, [d.reviewedBy, when(d.reviewedAt)].filter(Boolean).join(', '));
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
      body.appendChild(tr);
    });
    if (!list.length) say('No documents with this status.', false); else say('');
  }

  // The filter lives in the query string of the request only; every value is encoded, and the
  // server checks the status and the dates again.
  function query() {
    var q = new URLSearchParams();
    if (statusSel.value) q.set('status', statusSel.value);
    fields.forEach(function (f) {
      var v = document.getElementById('sub-' + f).value.trim();
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
        document.getElementById('sub-page').textContent = (r.headers.get('X-Total-Count') || '0') + ' documents, page ' + page + ' of ' + pages;
        document.getElementById('sub-prev').disabled = page <= 1;
        document.getElementById('sub-next').disabled = page >= pages;
        return r.json();
      })
      .then(render)
      .catch(function (e) { say(e.message, true); });
  }

  // A new filter always starts again at page 1.
  function apply() { page = 1; load(); }

  statusSel.addEventListener('change', apply);
  document.getElementById('sub-apply').addEventListener('click', apply);
  document.getElementById('sub-clear').addEventListener('click', function () {
    statusSel.value = '';
    fields.forEach(function (f) { document.getElementById('sub-' + f).value = ''; });
    apply();
  });
  document.getElementById('sub-prev').addEventListener('click', function () { if (page > 1) { page--; load(); } });
  document.getElementById('sub-next').addEventListener('click', function () { if (page < pages) { page++; load(); } });
  fields.forEach(function (f) {
    document.getElementById('sub-' + f).addEventListener('keydown', function (e) { if (e.key === 'Enter') apply(); });
  });

  // mcu.js calls this each time the tab is shown, so the list is never stale.
  window.loadSubmittedTab = load;
})();
