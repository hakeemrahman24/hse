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
    table.classList.add('mp-cards');
  });
})();
