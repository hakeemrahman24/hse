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

  // Status is a row of tap targets (chips) instead of a dropdown: one tap, all choices visible. The hidden
  // #sub-status keeps the value, so query() and Reset work on it as before.
  var chips = Array.prototype.slice.call(document.querySelectorAll('#sub-toolbar .sub-chip'));
  var more = document.getElementById('sub-more');
  var moreCount = document.getElementById('sub-more-n');
  function syncChips() {
    chips.forEach(function (c) { c.setAttribute('aria-checked', c.getAttribute('data-v') === statusSel.value ? 'true' : 'false'); });
  }
  // From 641px up CSS shows every field inline and hides the toggle; on a phone the fields stay folded
  // until the toggle is pressed, with a count so a filter hidden inside is never forgotten.
  var moreToggle = document.getElementById('sub-more-toggle');
  moreToggle.addEventListener('click', function () {
    var open = !more.classList.contains('open');
    more.classList.toggle('open', open);
    moreToggle.setAttribute('aria-expanded', open ? 'true' : 'false');
  });
  function countMore() {
    var n = ['company', 'submitter', 'from', 'to'].filter(function (f) { return document.getElementById('sub-' + f).value.trim(); }).length;
    moreCount.textContent = String(n);
    moreCount.hidden = !n;
  }

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

  // Opening a document changes nothing on the server. A document counts as reviewed only once a
  // checkup with a result (fit, unfit, ...) is saved for the worker, so a reviewer who opens it and
  // leaves leaves it exactly as it was.
  function startReview(d) {
    window.openSubmission(d.id);
  }

  // The checkup result has its own column: it is a medical outcome, not a step in the document's life.
  var RESULTS = { Fit: 'Fit', FitWithNote: 'Fit with note', Unfit: 'Unfit', TemporaryUnfit: 'Temporarily unfit', NeedFollowUp: 'Needs follow-up', IncompleteResult: 'Incomplete' };

  // The page is told by the server whether the signed-in staff member is an admin (data-admin);
  // the server checks the role again on every removal, so this only decides what is shown.
  var isAdmin = document.getElementById('tab-submitted').getAttribute('data-admin') === '1';

  // Removing is final: the entry and the stored PDF go. The linked checkup (medical data) stays.
  function remove(d) {
    if (!window.confirm('Remove the document of ' + d.worker + ' permanently? The PDF and this entry are removed and the submitter no longer sees it. A linked checkup stays.')) return;
    call('/api/mcu-submissions/' + d.id, 'DELETE').then(load).catch(function (e) { say(e.message, true); });
  }

  // Undoes a wrong link; the checkup itself stays. The document goes back to Pending.
  function unlink(d) {
    if (!window.confirm('Unlink the checkup from ' + d.worker + "'s document? The document goes back to Pending. The checkup itself is kept.")) return;
    call('/api/mcu-submissions/' + d.id + '/unlink', 'POST').then(load).catch(function (e) { say(e.message, true); });
  }

  function button(label, cls, onClick, aria) {
    var b = document.createElement('button');
    b.type = 'button'; b.className = cls; b.textContent = label;
    if (aria) b.setAttribute('aria-label', aria);
    if (onClick) b.addEventListener('click', onClick);
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
  function loadCandidates(d, row, fail) {
    row.textContent = 'Looking for this worker\u2019s checkups\u2026';
    fetch('/api/mcu-submissions/' + d.id + '/records', { credentials: 'same-origin' }).then(function (r) {
      if (!r.ok) throw new Error('Could not load the checkups (' + r.status + ').');
      return r.json();
    }).then(function (list) {
      row.textContent = '';
      if (!list.length) {
        row.textContent = 'No unlinked checkup found. Use Review to enter one.';
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
          .then(load).catch(function (e) { fail(e.message); });
      }));
    }).catch(function (e) { row.textContent = e.message; row.className = 'sub-row sub-none'; });
  }

  // The row itself is one compact line: Sent, Name, Company, Status, Result, Open PDF and a More toggle.
  // Everything else (submitter, position, who reviewed, the next step, Remove, Reject, Link) lives in
  // the detail row under the document, so the list stays short until someone asks for more.
  // Returns the detail row; the caller wires the toggle to it.
  function detailRow(d, colspan) {
    var row = document.createElement('tr');
    row.className = 'sub-detail';
    var cellEl = document.createElement('td');
    cellEl.colSpan = colspan;
    var panel = document.createElement('div');
    panel.className = 'sub-panel';

    // Facts first: the details that used to be columns of their own. Empty ones are left out.
    var facts = document.createElement('dl');
    facts.className = 'sub-facts';
    function fact(label, value) {
      if (!value) return;
      var dt = document.createElement('dt'); dt.textContent = label;
      var dd = document.createElement('dd'); dd.textContent = value;
      facts.appendChild(dt); facts.appendChild(dd);
    }
    fact('Submitter', d.submitter);
    fact('Position', d.position);
    fact('New employee', d.isNewEmployee ? 'Yes' : '');
    fact('Reviewed by', d.reviewedBy);
    fact('Reviewed (UTC)', when(d.reviewedAt));
    fact('Renewed by', d.renewedBy); // same words as the MCU table: Renewed by <checkup ID>
    fact('Reject reason', d.rejectReason);
    panel.appendChild(facts);

    var canAct = d.status === 'Pending' || d.status === 'Reviewed';
    var box = document.createElement('div');
    box.className = 'sub-act sub-panel-act';
    // Closed, Rejected, Withdrawn: only an admin has anything to do here.
    if (!canAct) {
      if (isAdmin) box.appendChild(button('Remove', 'btn-ghost sub-reject', function () { remove(d); }, 'Remove the document of ' + d.worker));
      if (box.children.length) panel.appendChild(box);
      cellEl.appendChild(panel); row.appendChild(cellEl);
      return { row: row, onOpen: null };
    }
    // Nobody presses a button to tell the submitter: the system mails them on the schedule set in
    // Settings about every reviewed document it has not told them about yet. Only Fit and Fit with note
    // then close the document; any other result stays Reviewed. A reviewed document therefore shows only
    // a note, and there is no "Open checkup": a follow-up is a new submission, reviewed as a new checkup.
    if (d.status === 'Reviewed' && d.hasRecord) {
      var told = document.createElement('span');
      told.className = 'sub-note';
      told.textContent = d.notified ? 'Submitter notified' : 'Submitter will be notified automatically';
      box.appendChild(told);
    }
    // The Review / Open checkup button is in the row itself (see nextStep), not here.
    if (isAdmin) box.appendChild(button('Remove', 'btn-ghost sub-reject', function () { remove(d); }, 'Remove the document of ' + d.worker));
    panel.appendChild(box);

    // A problem with these actions is shown here, next to the button that was pressed.
    var err = document.createElement('p');
    err.className = 'sub-panel-err';
    err.setAttribute('role', 'alert');
    function fail(text) { err.textContent = text || ''; }

    // Reject needs only a reason; it is for a document that cannot be used (for example unreadable).
    var reject = section('Reject this document');
    var reason = document.createElement('input');
    reason.type = 'text'; reason.maxLength = 200; reason.placeholder = 'Reason the submitter will see';
    reason.setAttribute('aria-label', 'Reason for rejecting ' + d.worker);
    reject.row.appendChild(reason);
    reject.row.appendChild(button('Reject', 'btn-ghost sub-reject', function () {
      if (!reason.value.trim()) { fail('Enter a reason before rejecting.'); reason.focus(); return; }
      fail('');
      call('/api/mcu-submissions/' + d.id + '/reject', 'POST', { reason: reason.value })
        .then(load).catch(function (e) { fail(e.message); });
    }));
    panel.appendChild(reject.box);

    var link;
    if (d.hasRecord) {
      link = section('Linked checkup');
      link.row.appendChild(document.createTextNode(d.result ? 'A checkup with a result is linked.' : 'A checkup is linked but has no result yet.'));
      link.row.appendChild(button('Open', 'btn-ghost', function () { startReview(d); }));
      link.row.appendChild(button('Unlink', 'btn-ghost', function () { unlink(d); }, 'Unlink the checkup of ' + d.worker));
    } else {
      // Link is for a checkup that was entered before the document was opened from here: the
      // reviewer picks it from that worker's own unlinked checkups, so nobody types a Checkup ID.
      link = section('Link a checkup already entered');
      link.row.textContent = 'Looking…';
    }
    panel.appendChild(link.box);
    panel.appendChild(err);
    cellEl.appendChild(panel);
    row.appendChild(cellEl);

    var loaded = false;
    return { row: row, onOpen: function () {
      if (!loaded && !d.hasRecord) { loaded = true; loadCandidates(d, link.row, fail); }
    } };
  }

  // The one action a reviewer takes most often sits in the row, beside Open PDF, so it is one click.
  // A reviewed document has none: the system tells the submitter, and a follow-up is a new submission.
  function nextStep(d) {
    if (d.status !== 'Pending' && !(d.status === 'Reviewed' && !d.hasRecord)) return null;
    if (d.hasRecord) {
      // Linked but without a result yet: finish that checkup, do not start another.
      return button('Open checkup', 'btn-primary', function () { startReview(d); }, 'Open the linked checkup of ' + d.worker);
    }
    return button('Review', 'btn-primary', function () { startReview(d); }, 'Review the checkup of ' + d.worker);
  }

  // Seven columns, one line per document: who sent it and when, who it is about, where it stands and
  // the checkup result. The row's actions are Open PDF and Review (or Open checkup); More opens the rest.
  function render(list) {
    body.textContent = '';
    list.forEach(function (d) {
      var tr = document.createElement('tr');
      // One tint per status (see .sub-st-* in app.css) so the state shows at a glance without reading the column.
      tr.className = 'sub-st-' + String(d.status || '').toLowerCase();
      cell(tr, when(d.sentAt));
      cell(tr, d.worker);
      cell(tr, d.company);
      cell(tr, d.status);
      cell(tr, d.result ? (RESULTS[d.result] || d.result) : '');
      var act = document.createElement('div');
      act.className = 'sub-act'; // same compact button height as the panel
      cell(tr, '').appendChild(act);
      if (d.hasFile) {
        var a = document.createElement('a');
        a.href = '/api/mcu-submissions/' + d.id + '/file';
        a.target = '_blank'; a.rel = 'noopener'; a.className = 'sub-pdf'; a.textContent = 'Open PDF';
        a.setAttribute('aria-label', 'Open the PDF of ' + d.worker);
        act.appendChild(a);
      }
      var step = nextStep(d);
      if (step) act.appendChild(step);
      var moreCell = cell(tr, '');
      var toggle = button('More', 'btn-ghost sub-toggle', null, 'More about the document of ' + d.worker);
      toggle.setAttribute('aria-expanded', 'false');
      moreCell.appendChild(toggle);
      var detail = detailRow(d, headings.length);
      toggle.addEventListener('click', function () {
        var open = detail.row.classList.toggle('open');
        toggle.setAttribute('aria-expanded', open ? 'true' : 'false');
        toggle.textContent = open ? 'Less' : 'More';
        if (open && detail.onOpen) detail.onOpen();
      });
      body.appendChild(tr);
      body.appendChild(detail.row);
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
  function apply() { page = 1; countMore(); load(); }

  chips.forEach(function (c) {
    c.addEventListener('click', function () { statusSel.value = c.getAttribute('data-v'); syncChips(); apply(); });
  });
  document.getElementById('sub-apply').addEventListener('click', apply);
  document.getElementById('sub-clear').addEventListener('click', function () {
    statusSel.value = '';
    syncChips();
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
