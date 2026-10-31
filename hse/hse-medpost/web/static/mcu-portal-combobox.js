// Searchable dropdown for /mcu-submit, like the company pickers in the staff app: the person types
// to filter the list and picks one. It upgrades every <select data-combobox>.
// The <select> stays in the page as the form value, so the server reads company_id exactly as
// before, and without JavaScript the plain select still works. Option names are written with
// textContent only, never innerHTML, so a company name can never inject markup.
(function () {
  'use strict';

  function build(select) {
    var wrap = document.createElement('div');
    wrap.className = 'mp-cb';

    var input = document.createElement('input');
    input.type = 'text';
    input.autocomplete = 'off';
    input.spellcheck = false;
    input.setAttribute('role', 'combobox');
    input.setAttribute('aria-expanded', 'false');
    input.setAttribute('aria-autocomplete', 'list');
    input.placeholder = select.options.length ? select.options[0].textContent : '';
    input.id = select.id + '-cb';

    var list = document.createElement('div');
    list.className = 'app-program-list company-typeahead mp-cb-list';
    list.id = select.id + '-list';
    list.setAttribute('role', 'listbox');
    list.hidden = true;
    input.setAttribute('aria-controls', list.id);

    // Real options only: the first, empty "Choose ..." option is the placeholder.
    var options = [];
    for (var i = 0; i < select.options.length; i++) {
      if (select.options[i].value !== '') options.push({ value: select.options[i].value, label: select.options[i].textContent });
    }

    var active = -1;
    var shown = [];

    function chosenLabel() {
      var o = select.options[select.selectedIndex];
      return o && o.value !== '' ? o.textContent : '';
    }

    // A typed text that is not a picked company must not submit: the select is empty then, so a
    // required field gets the browser's own message, and a half-typed name gets ours.
    function validity() {
      if (input.value.trim() !== '' && chosenLabel() !== input.value) {
        input.setCustomValidity('Pick a company from the list.');
      } else {
        input.setCustomValidity('');
      }
    }

    function close() {
      list.hidden = true;
      input.setAttribute('aria-expanded', 'false');
      active = -1;
    }

    function pick(option) {
      select.value = option.value;
      input.value = option.label;
      validity();
      close();
      select.dispatchEvent(new Event('change', { bubbles: true }));
    }

    function mark() {
      var items = list.children;
      for (var i = 0; i < items.length; i++) {
        var on = i === active;
        items[i].classList.toggle('active', on);
        items[i].setAttribute('aria-selected', on ? 'true' : 'false');
        if (on) { items[i].scrollIntoView({ block: 'nearest' }); input.setAttribute('aria-activedescendant', items[i].id); }
      }
    }

    function render() {
      var q = input.value.trim().toLowerCase();
      // While the box still shows the chosen company, show the whole list, not just that one.
      var all = q === '' || input.value === chosenLabel();
      shown = options.filter(function (o) { return all || o.label.toLowerCase().indexOf(q) !== -1; });
      list.textContent = '';
      shown.forEach(function (o, i) {
        var b = document.createElement('button');
        b.type = 'button';
        b.className = 'app-program-item';
        b.id = list.id + '-' + i;
        b.setAttribute('role', 'option');
        b.tabIndex = -1;
        var t = document.createElement('span');
        t.className = 'app-program-item-title';
        t.textContent = o.label;
        b.appendChild(t);
        // mousedown, not click: click would fire after the input lost focus and closed the list.
        b.addEventListener('mousedown', function (e) { e.preventDefault(); pick(o); });
        list.appendChild(b);
      });
      if (!shown.length) {
        var none = document.createElement('p');
        none.className = 'mp-hint mp-cb-none';
        none.textContent = 'No company matches.';
        list.appendChild(none);
      }
      active = -1;
      list.hidden = false;
      input.setAttribute('aria-expanded', 'true');
    }

    input.addEventListener('focus', function () { input.select(); render(); });
    input.addEventListener('input', function () {
      select.value = '';   // typing un-picks: only a click or Enter on the list chooses
      validity();
      render();
    });
    input.addEventListener('keydown', function (e) {
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault();
        if (list.hidden) render();
        if (!shown.length) return;
        active = e.key === 'ArrowDown' ? (active + 1) % shown.length : (active <= 0 ? shown.length - 1 : active - 1);
        mark();
      } else if (e.key === 'Enter') {
        if (!list.hidden && shown.length) {
          e.preventDefault();
          pick(shown[active >= 0 ? active : 0]);
        }
      } else if (e.key === 'Escape') {
        if (!list.hidden) { e.preventDefault(); close(); input.value = chosenLabel(); validity(); }
      }
    });
    input.addEventListener('blur', function () {
      close();
      // Leaving the box with a name that was never picked puts the chosen company (or nothing) back.
      if (chosenLabel() !== input.value && select.value === '') { input.value = ''; }
      validity();
    });

    // The select moves out of sight, but not out of the page: a hidden control cannot be focused
    // for the browser's "required" message, so the text box carries that instead.
    input.required = select.required;
    select.required = false;
    select.tabIndex = -1;
    select.setAttribute('aria-hidden', 'true');
    select.classList.add('mp-cb-native');

    var label = select.id ? document.querySelector('label[for="' + select.id + '"]') : null;
    if (label) label.setAttribute('for', input.id);

    input.value = chosenLabel();
    select.parentNode.insertBefore(wrap, select);
    wrap.appendChild(input);
    wrap.appendChild(list);
    wrap.appendChild(select);
  }

  var selects = document.querySelectorAll('select[data-combobox]');
  for (var i = 0; i < selects.length; i++) build(selects[i]);
})();
