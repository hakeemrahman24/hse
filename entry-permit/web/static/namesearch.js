// Searchable name list for the public "My Layar Entry Permit" form.
//
// Unlike combobox.js (a fixed list in the page), the names come from the
// server, and only after the person has typed MIN characters, so the full
// list of workers is never sent to a browser. Names are written with
// textContent only, never innerHTML, so a name cannot inject markup.
// Without JavaScript the field is a plain text box and the server checks
// the typed name exactly, so the form still works.
(function () {
  'use strict';

  var DELAY_MS = 250;

  function build(input) {
    var url = input.getAttribute('data-namesearch');
    var min = parseInt(input.getAttribute('data-min'), 10) || 4;

    var wrap = document.createElement('div');
    wrap.className = 'cb';
    input.parentNode.insertBefore(wrap, input);
    wrap.appendChild(input);

    var list = document.createElement('div');
    list.className = 'cb-list';
    list.id = input.id + '-list';
    list.setAttribute('role', 'listbox');
    list.hidden = true;
    wrap.appendChild(list);

    input.setAttribute('role', 'combobox');
    input.setAttribute('aria-expanded', 'false');
    input.setAttribute('aria-autocomplete', 'list');
    input.setAttribute('aria-controls', list.id);

    // A name already in the box (after a failed try) was picked before.
    var picked = input.value.trim() !== '';
    var active = -1;
    var timer = 0;
    var request = null;

    function validity() {
      input.setCustomValidity(input.value.trim() !== '' && !picked ? (input.getAttribute('data-pick-message') || 'Pick your name from the list.') : '');
    }

    function close() {
      list.hidden = true;
      input.setAttribute('aria-expanded', 'false');
      input.removeAttribute('aria-activedescendant');
      active = -1;
    }

    function pick(name) {
      input.value = name;
      picked = true;
      validity();
      close();
    }

    function mark() {
      var items = list.children;
      for (var i = 0; i < items.length; i++) {
        var on = i === active;
        items[i].classList.toggle('active', on);
        items[i].setAttribute('aria-selected', on ? 'true' : 'false');
        if (on) {
          items[i].scrollIntoView({ block: 'nearest' });
          input.setAttribute('aria-activedescendant', items[i].id);
        }
      }
    }

    function show(names, message) {
      list.textContent = '';
      active = -1;
      if (names.length === 0) {
        var none = document.createElement('p');
        none.className = 'cb-none';
        none.textContent = message || 'No matching name.';
        list.appendChild(none);
      }
      names.forEach(function (name, i) {
        var b = document.createElement('button');
        b.type = 'button';
        b.className = 'cb-item';
        b.id = list.id + '-' + i;
        b.setAttribute('role', 'option');
        b.setAttribute('aria-selected', 'false');
        b.textContent = name;
        // mousedown, not click: the input would lose focus and close the list first.
        b.addEventListener('mousedown', function (e) { e.preventDefault(); pick(name); });
        list.appendChild(b);
      });
      list.hidden = false;
      input.setAttribute('aria-expanded', 'true');
    }

    function search() {
      var q = input.value.trim();
      if (q.length < min) { close(); return; }
      if (request) request.abort();
      request = new AbortController();
      fetch(url + '?q=' + encodeURIComponent(q), { signal: request.signal, headers: { Accept: 'application/json' } })
        .then(function (res) {
          if (res.status === 429) { show([], 'Too many searches. Wait a moment and try again.'); return null; }
          if (!res.ok) { show([], 'The name list is not available right now.'); return null; }
          return res.json();
        })
        .then(function (data) { if (data) show(data.names || []); })
        .catch(function (err) { if (err.name !== 'AbortError') show([], 'The name list is not available right now.'); });
    }

    input.addEventListener('input', function () {
      picked = false;
      validity();
      window.clearTimeout(timer);
      if (input.value.trim().length < min) { close(); return; }
      timer = window.setTimeout(search, DELAY_MS);
    });

    input.addEventListener('keydown', function (e) {
      var items = list.querySelectorAll('.cb-item');
      if (e.key === 'ArrowDown' && items.length) {
        e.preventDefault();
        active = (active + 1) % items.length;
        mark();
      } else if (e.key === 'ArrowUp' && items.length) {
        e.preventDefault();
        active = (active - 1 + items.length) % items.length;
        mark();
      } else if (e.key === 'Enter' && !list.hidden && active >= 0) {
        e.preventDefault();
        pick(items[active].textContent);
      } else if (e.key === 'Escape') {
        close();
      }
    });

    input.addEventListener('blur', function () { window.setTimeout(close, 120); });
    validity();
  }

  var inputs = document.querySelectorAll('input[data-namesearch]');
  for (var i = 0; i < inputs.length; i++) build(inputs[i]);
})();
