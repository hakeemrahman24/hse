/* Step-by-step view of a long form. Progressive enhancement: without this script the form is one long page
   with a section list that jumps down it. With it, one section shows at a time, the section list on top becomes
   a progress indicator (done, current, to do), and Back / Next buttons move between sections. Next checks the
   section's own fields first, so a mistake is caught on the step where it was made. The last step shows the
   real submit button. Markup contract: form[data-wizard] > .stepnav ol li a[href="#sec-N"], section.fcard#sec-N,
   and .form-actions with the submit button inside it. */
(function (root, factory) {
  var api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  else if (root.document) api.start(root.document);
})(typeof self !== 'undefined' ? self : this, function () {
  'use strict';
  var api = {};

  // clamp keeps a step number inside 0..total-1.
  api.clamp = function (n, total) { return Math.max(0, Math.min(total - 1, n)); };

  // label is the text of the progress line: "Step 3 of 8: The visitor".
  api.label = function (index, total, name) {
    return 'Step ' + (index + 1) + ' of ' + total + (name ? ': ' + name : '');
  };

  function field(el) { return /^(INPUT|SELECT|TEXTAREA)$/.test(el.tagName) && el.type !== 'hidden'; }

  api.setup = function (form) {
    var sections = Array.prototype.slice.call(form.querySelectorAll('section.fcard'));
    var links = Array.prototype.slice.call(form.querySelectorAll('.stepnav a'));
    var actions = form.querySelector('.form-actions');
    var submit = actions && actions.querySelector('button[type="submit"]');
    if (sections.length < 2 || !actions || !submit) return;
    var doc = form.ownerDocument;
    var current = 0, furthest = 0;
    // After the server sent the form back with a message, every step may be opened at once: the message may be
    // about a field on any of them, and the person must be able to go and fix it.
    var reach = doc.querySelector('.error[role="alert"]') ? sections.length - 1 : 0;
    form.classList.add('wizard-on');

    var bar = doc.createElement('div');
    bar.className = 'wizard-bar';
    bar.setAttribute('aria-hidden', 'true');
    var fill = doc.createElement('i');
    bar.appendChild(fill);
    var nav = form.querySelector('.stepnav');
    if (nav) nav.appendChild(bar);

    var back = doc.createElement('button');
    back.type = 'button'; back.className = 'btn-small-quiet wizard-back'; back.textContent = 'Back';
    var next = doc.createElement('button');
    next.type = 'button'; next.className = 'btn wizard-next'; next.textContent = 'Next';
    var count = doc.createElement('span');
    count.className = 'wizard-count'; count.setAttribute('aria-live', 'polite');
    actions.insertBefore(count, actions.firstChild);
    actions.insertBefore(next, submit);
    actions.insertBefore(back, next);

    function titleOf(i) {
      var t = sections[i].querySelector('.fcard-title');
      return t ? (t.textContent || '').replace(/^\s*\d+\s*/, '').trim() : '';
    }

    function show(i, focus) {
      current = api.clamp(i, sections.length);
      if (current > furthest) furthest = current;
      sections.forEach(function (s, k) { s.classList.toggle('is-current', k === current); });
      links.forEach(function (a, k) {
        a.classList.toggle('is-current', k === current);
        a.classList.toggle('is-done', k !== current && k <= furthest);
        if (k === current) a.setAttribute('aria-current', 'step'); else a.removeAttribute('aria-current');
      });
      fill.style.width = (((current + 1) / sections.length) * 100) + '%';
      count.textContent = api.label(current, sections.length, titleOf(current));
      back.hidden = current === 0;
      next.hidden = current === sections.length - 1;
      submit.hidden = current !== sections.length - 1;
      if (focus) {
        var top = nav || sections[current];
        if (top.scrollIntoView) top.scrollIntoView({ block: 'start' });
        var first = Array.prototype.filter.call(sections[current].querySelectorAll('input,select,textarea'), field)[0];
        if (first && first.focus) first.focus({ preventScroll: true });
      }
    }

    // sectionOk checks one section's fields and shows the first complaint.
    function sectionOk(s) {
      var bad = Array.prototype.filter.call(s.querySelectorAll('input,select,textarea'), field)
        .filter(function (el) { return !el.checkValidity(); })[0];
      if (bad) { bad.reportValidity(); return false; }
      return true;
    }

    next.addEventListener('click', function () { if (sectionOk(sections[current])) show(current + 1, true); });
    back.addEventListener('click', function () { show(current - 1, true); });
    links.forEach(function (a, k) {
      a.addEventListener('click', function (e) {
        e.preventDefault();
        // Going back, or to a step already reached, is free; going forward is only by Next so each step is checked.
        if (k <= Math.max(furthest, reach)) show(k, true);
      });
    });
    // Enter in a text box moves on instead of sending half a form.
    form.addEventListener('keydown', function (e) {
      if (e.key === 'Enter' && e.target && e.target.tagName === 'INPUT' && e.target.type !== 'submit' && current < sections.length - 1) {
        e.preventDefault();
        next.click();
      }
    });
    // A field the browser refuses on submit may sit in a hidden section: bring that section forward first.
    form.addEventListener('invalid', function (e) {
      var s = e.target && e.target.closest && e.target.closest('section.fcard');
      var k = sections.indexOf(s);
      if (k >= 0 && k !== current) show(k, false);
    }, true);
    show(0, false);
  };

  api.start = function (doc) {
    Array.prototype.forEach.call(doc.querySelectorAll('form[data-wizard]'), api.setup);
  };
  return api;
});
