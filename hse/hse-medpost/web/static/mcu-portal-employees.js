// Employee checkups behaviour (owner, 2026-11-14), two small things on one list:
//  - Certificate: one <dialog> serves the whole list. A click on a row's Certificate link asks "Open or
//    Download?" in a popup in the middle of the screen.
//  - More: a click opens the row's extra details (company, position, nationality, the reviewer's note) in
//    a row right under it, and a second click closes it again.
// Everything shown comes from the clicked row's data-* attributes and is written with textContent, so a
// name or a note is never read as markup.
//
// Without JavaScript nothing here runs: the Certificate link is a real link to the PDF and still works.
(function () {
  'use strict';
  var dlg = document.getElementById('mp-emp-dialog');
  var tpl = document.getElementById('mp-more-template');
  var canDialog = !!dlg && typeof dlg.showModal === 'function';
  var canExpand = !!tpl && !!tpl.content && !!tpl.content.firstElementChild;

  // ---- Certificate popup ------------------------------------------------------------------------
  function showCertificate(link) {
    // The link's own address is the "open in the tab" one (?view=1); the plain address downloads.
    var base = link.getAttribute('href').split('?')[0];
    dlg.querySelector('.mp-dlg-title').textContent = link.getAttribute('data-name') || '';
    dlg.querySelector('[data-act="open"]').setAttribute('href', base + '?view=1');
    dlg.querySelector('[data-act="download"]').setAttribute('href', base);
    dlg.showModal();
  }

  if (canDialog) {
    // A click on the dim area around the popup closes it (the body inside has its own padding, so a click
    // on the popup itself never has the dialog as its target). Esc closes it natively.
    dlg.addEventListener('click', function (e) {
      if (e.target === dlg) dlg.close();
    });
    // After choosing Open or Download the popup has done its job.
    Array.prototype.forEach.call(dlg.querySelectorAll('[data-act]'), function (a) {
      a.addEventListener('click', function () {
        setTimeout(function () { dlg.close(); }, 0);
      });
    });
  }

  // ---- More: the row expands -----------------------------------------------------------------------
  // A row of the detail list shows only when it has something to say.
  function setRow(list, name, value) {
    var row = list.querySelector('[data-row="' + name + '"]');
    if (!row) return;
    row.hidden = !value;
    row.querySelector('dd').textContent = value || '';
  }

  function toggleMore(button) {
    var row = button.closest('tr');
    if (!row) return;
    var next = row.nextElementSibling;
    if (next && next.classList.contains('mp-detail')) { // already open: close it
      next.remove();
      button.setAttribute('aria-expanded', 'false');
      return;
    }
    var list = tpl.content.firstElementChild.cloneNode(true);
    var note = button.getAttribute('data-note') || '';
    var label = button.getAttribute('data-note-label') || '';
    setRow(list, 'company', button.getAttribute('data-company'));
    setRow(list, 'position', button.getAttribute('data-position'));
    setRow(list, 'nationality', button.getAttribute('data-nationality'));
    // The label comes from the server ("Recommendation" or "Notes"); the template holds a translated
    // heading for each, and this picks the one that applies.
    setRow(list, 'note-Recommendation', label === 'Recommendation' ? note : '');
    setRow(list, 'note-Notes', label === 'Notes' ? note : '');

    var detail = document.createElement('tr');
    detail.className = 'mp-detail ' + row.className; // keeps the row's status colour (tone-*)
    var cell = document.createElement('td');
    cell.colSpan = row.children.length;
    cell.appendChild(list);
    detail.appendChild(cell);
    row.parentNode.insertBefore(detail, row.nextSibling);
    button.setAttribute('aria-expanded', 'true');
  }

  document.addEventListener('click', function (e) {
    var link = canDialog && e.target.closest ? e.target.closest('a.mp-cert') : null;
    if (link) {
      // Ctrl/Cmd/Shift-click and middle-click keep the browser's own "open in new tab" behaviour.
      if (e.ctrlKey || e.metaKey || e.shiftKey || e.button) return;
      e.preventDefault();
      showCertificate(link);
      return;
    }
    var more = canExpand && e.target.closest ? e.target.closest('button.mp-more-btn') : null;
    if (more) toggleMore(more);
  });
})();
