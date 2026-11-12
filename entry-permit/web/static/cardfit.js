/* The card's details area has a fixed height, as on the legacy card. A long company name wraps to
   several lines, so the text is shrunk a little at a time until it fits (never below 60% of its
   size), as the legacy card did. Progressive enhancement: without scripts the CSS size still holds. */
(function () {
  'use strict';
  var d = document.querySelector('.lepc-details');
  if (!d) return;
  var start = parseFloat(getComputedStyle(d).fontSize);
  var px = start;
  while (d.scrollHeight > d.clientHeight + 1 && px > start * 0.6) {
    px -= 0.5;
    d.style.fontSize = px + 'px';
  }
})();
