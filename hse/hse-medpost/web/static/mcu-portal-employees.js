// Employee checkups popups (owner, 2026-11-14). One <dialog> serves the whole list: a click on a row's
// Certificate link asks "Open or Download?", and a click on More shows the extra details (company,
// position, nationality and the reviewer's note). Everything the popup shows comes from the clicked
// row's data-* attributes and is written with textContent, so a name or a note is never read as markup.
//
// Without JavaScript, or in a browser without <dialog>, nothing here runs: the Certificate link is a real
// link to the PDF and still works, and the page is otherwise complete.
(function () {
  'use strict';
  var dlg = document.getElementById('mp-emp-dialog');
  if (!dlg || typeof dlg.showModal !== 'function') return;

  var title = dlg.querySelector('.mp-dlg-title');
  var certBox = dlg.querySelector('.mp-dlg-cert');
  var moreBox = dlg.querySelector('.mp-dlg-more');
  var openLink = dlg.querySelector('[data-act="open"]');
  var downloadLink = dlg.querySelector('[data-act="download"]');

  // A row of the details list shows only when it has something to say.
  function setRow(name, value) {
    var row = moreBox.querySelector('[data-row="' + name + '"]');
    if (!row) return;
    row.hidden = !value;
    row.querySelector('dd').textContent = value || '';
  }

  function showCertificate(link) {
    // The link's own address is the "open in the tab" one (?view=1); the plain address downloads.
    var base = link.getAttribute('href').split('?')[0];
    title.textContent = link.getAttribute('data-name') || '';
    openLink.setAttribute('href', base + '?view=1');
    downloadLink.setAttribute('href', base);
    certBox.hidden = false;
    moreBox.hidden = true;
    dlg.showModal();
  }

  function showMore(button) {
    var note = button.getAttribute('data-note') || '';
    var label = button.getAttribute('data-note-label') || '';
    title.textContent = button.getAttribute('data-name') || '';
    setRow('company', button.getAttribute('data-company'));
    setRow('position', button.getAttribute('data-position'));
    setRow('nationality', button.getAttribute('data-nationality'));
    // The label comes from the server ("Recommendation" or "Notes"); the dialog holds a translated
    // heading for each, and this picks the one that applies.
    setRow('note-Recommendation', label === 'Recommendation' ? note : '');
    setRow('note-Notes', label === 'Notes' ? note : '');
    certBox.hidden = true;
    moreBox.hidden = false;
    dlg.showModal();
  }

  document.addEventListener('click', function (e) {
    var link = e.target.closest ? e.target.closest('a.mp-cert') : null;
    if (link) {
      // Ctrl/Cmd/Shift-click and middle-click keep the browser's own "open in new tab" behaviour.
      if (e.ctrlKey || e.metaKey || e.shiftKey || e.button) return;
      e.preventDefault();
      showCertificate(link);
      return;
    }
    var more = e.target.closest ? e.target.closest('button.mp-more-btn') : null;
    if (more) showMore(more);
  });

  // A click on the dim area around the popup closes it (the body inside has its own padding, so a click
  // on the popup itself never has the dialog as its target). Esc closes it natively.
  dlg.addEventListener('click', function (e) {
    if (e.target === dlg) dlg.close();
  });

  // After choosing Open or Download the popup has done its job.
  [openLink, downloadLink].forEach(function (a) {
    a.addEventListener('click', function () {
      setTimeout(function () { dlg.close(); }, 0);
    });
  });
})();
