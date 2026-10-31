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

  // The column titles, so a phone can show each value under its own label (see the card layout in app.css).
  var headings = Array.prototype.map.call(document.querySelectorAll('#tab-submitted thead th'), function (th) { return th.textContent; });

  function cell(tr, text) {
    var td = document.createElement('td');
    td.setAttribute('data-label', headings[tr.children.length] || '');
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

  // Times are stored as UTC text "YYYY-MM-DDTHH:MM:SS"; the table shows "YYYY-MM-DD HH:MM" under
  // a "(UTC)" heading, and the prompt spells the zone out.
  function when(t) { return t ? t.replace('T', ' ').slice(0, 16) : ''; }

  function reviewPrompt(d) {
    return 'Open ' + d.worker + "'s checkup form, filled in from the submission? Nothing is saved until you save the checkup.";
  }

  // Opening a document changes nothing on the server. A document counts as reviewed only once a
  // checkup with a result (fit, unfit, ...) is saved for the worker, so a reviewer who opens it and
  // leaves leaves it exactly as it was.
  function startReview(d) {
    if (!window.confirm(reviewPrompt(d))) return;
    window.openSubmission(d.id);
  }

  // The checkup result has its own column: it is a medical outcome, not a step in the document's life.
  var RESULTS = { Fit: 'Fit', FitWithNote: 'Fit with note', Unfit: 'Unfit', TemporaryUnfit: 'Temporarily unfit', NeedFollowUp: 'Needs follow-up' };

  // Optional: a Reviewed document is complete without it. Pressing it closes the document and puts the
  // worker in the submitter's next daily mail (08:00 Jakarta, one mail per submitter).
  function notify(d) {
    if (!window.confirm('Notify the submitter that ' + d.worker + "'s checkup is done? They are told in the daily mail, and this document is closed.")) return;
    call('/api/mcu-submissions/' + d.id + '/close', 'POST').then(load).catch(function (e) { say(e.message, true); });
  }

  function button(label, cls, onClick, aria) {
    var b = document.createElement('button');
    b.type = 'button'; b.className = cls; b.textContent = label;
    if (aria) b.setAttribute('aria-label', aria);
    b.addEventListener('click', onClick);
    return b;
  }

  function section(title) {
    var box = document.createElement('div');
    box.className = 'sub-group';
    var t = document.createElement('div');
    t.className = 'sub-group-title';
    t.textContent = title;
    var row = document.createElement('div');
    row.className = 'sub-row';
    box.appendChild(t); box.appendChild(row);
    return { box: box, row: row };
  }

  // The checkups of this worker that no document is linked to yet; the server applies the same rule
  // as the close itself, so everything offered here is accepted.
  function loadCandidates(d, row) {
    row.textContent = 'Looking for this worker\u2019s checkups\u2026';
    fetch('/api/mcu-submissions/' + d.id + '/records', { credentials: 'same-origin' }).then(function (r) {
      if (!r.ok) throw new Error('Could not load the checkups (' + r.status + ').');
      return r.json();
    }).then(function (list) {
      row.textContent = '';
      if (!list.length) {
        row.textContent = 'No unlinked checkup found for this worker. Use Review to enter one.';
        row.className = 'sub-row sub-none';
        return;
      }
      var pick = document.createElement('select');
      pick.setAttribute('aria-label', 'Checkup to link to ' + d.worker);
      list.forEach(function (c) {
        var o = document.createElement('option');
        o.value = c.checkUpId;
        o.textContent = [c.checkUpId, c.dateCheckUp, c.result].filter(Boolean).join(' \u00b7 ');
        pick.appendChild(o);
      });
      row.appendChild(pick);
      row.appendChild(button('Link', 'btn-ghost', function () {
        call('/api/mcu-submissions/' + d.id + '/link', 'POST', { checkUpId: pick.value })
          .then(load).catch(function (e) { say(e.message, true); });
      }));
    }).catch(function (e) { row.textContent = e.message; row.className = 'sub-row sub-none'; });
  }

  function actions(d, td) {
    if (d.status !== 'Pending' && d.status !== 'Reviewed') return;
    var box = document.createElement('div');
    box.className = 'sub-act';
    // Once a checkup with a result is linked the document is Reviewed and the next step is telling
    // the submitter; before that the next step is the checkup form.
    if (d.status === 'Reviewed' && d.hasRecord) {
      box.appendChild(button('Notify submitter', 'btn-primary', function () { notify(d); }, 'Notify the submitter about ' + d.worker));
    } else {
      box.appendChild(button('Review', 'btn-primary', function () { startReview(d); }, 'Review the checkup of ' + d.worker));
    }

    // "More" holds the two things a reviewer rarely does, each in its own labelled section so they
    // do not read as one form. Reject needs only a reason. Link is for a checkup that was entered
    // before the document was opened from here: the reviewer picks it from that worker's own
    // unlinked checkups, so nobody has to remember or type a Checkup ID.
    var more = document.createElement('details');
    more.className = 'sub-more';
    var sum = document.createElement('summary');
    sum.textContent = 'More';
    more.appendChild(sum);

    var reject = section('Reject this document');
    var reason = document.createElement('input');
    reason.type = 'text'; reason.maxLength = 200; reason.placeholder = 'Reason the submitter will see';
    reason.setAttribute('aria-label', 'Reason for rejecting ' + d.worker);
    reject.row.appendChild(reason);
    reject.row.appendChild(button('Reject', 'btn-ghost', function () {
      if (!reason.value.trim()) { say('Enter a reason before rejecting.', true); reason.focus(); return; }
      call('/api/mcu-submissions/' + d.id + '/reject', 'POST', { reason: reason.value })
        .then(load).catch(function (e) { say(e.message, true); });
    }));
    more.appendChild(reject.box);

    if (!d.hasRecord) {
      var link = section('Link a checkup already entered');
      link.row.textContent = 'Opens when you expand this.';
      more.appendChild(link.box);
      var loaded = false;
      more.addEventListener('toggle', function () {
        if (!more.open || loaded) return;
        loaded = true;
        loadCandidates(d, link.row);
      });
    }
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
        tag.className = 'sub-note'; tag.textContent = 'New employee';
        worker.appendChild(tag);
      }
      cell(tr, d.company); cell(tr, d.position); cell(tr, d.submitter);
      var st = cell(tr, d.status);
      if (d.rejectReason) {
        var why = document.createElement('div');
        why.className = 'sub-note'; why.textContent = d.rejectReason;
        st.appendChild(why);
      }
      cell(tr, d.result ? RESULTS[d.result] || d.result : '');
      cell(tr, [d.reviewedBy, when(d.reviewedAt)].filter(Boolean).join(', '));
      var file = cell(tr, '');
      if (d.hasFile) {
        var a = document.createElement('a');
        a.href = '/api/mcu-submissions/' + d.id + '/file';
        a.target = '_blank'; a.rel = 'noopener'; a.textContent = 'Open PDF';
        file.appendChild(a);
      } else {
        file.textContent = '—';
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
