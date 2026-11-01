// Phone layout for the portal's tables: each cell gets its column title as a data-label, which the
// stylesheet shows above the value when the screen is narrow (the table becomes a list of cards).
// Without JavaScript the table simply scrolls sideways as before.
(function () {
  'use strict';
  var tables = document.querySelectorAll('table.mp-table');
  Array.prototype.forEach.call(tables, function (table) {
    var titles = Array.prototype.map.call(table.querySelectorAll('thead th'), function (th) { return th.textContent.trim(); });
    Array.prototype.forEach.call(table.querySelectorAll('tbody tr'), function (tr) {
      Array.prototype.forEach.call(tr.children, function (td, i) {
        if (titles[i] && !td.hasAttribute('data-label')) td.setAttribute('data-label', titles[i]);
      });
    });
    // Stored timestamps read "2026-10-31T07:32:02"; show them like the staff app does ("2026-10-31 07:32").
    Array.prototype.forEach.call(table.querySelectorAll('tbody td'), function (td) {
      var m = /^(\d{4}-\d\d-\d\d)T(\d\d:\d\d):\d\d$/.exec(td.textContent.trim());
      if (m && !td.children.length) td.textContent = m[1] + ' ' + m[2];
    });
    table.classList.add('mp-stack');
  });
})();
