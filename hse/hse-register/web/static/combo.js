// Replaces the browser's own dropdowns with the same floating, searchable list the Medpost pages
// use. Two kinds of field are enhanced:
//
//   <select data-combo>            - a choice from a fixed list. The <select> stays in the page,
//                                    hidden, and is what the form submits, so the server and
//                                    suggest.js keep working unchanged. <optgroup>s become headings.
//   <input list="x" data-combo-list> - text with suggestions from <datalist id="x">. Free text is
//                                    allowed unless the input also has data-combo-strict.
//
// A script that sets a select's value by code must then fire `combo:sync` on it, because setting
// .value raises no event and the visible text would otherwise go stale.
(function () {
  "use strict";

  var MAX_ITEMS = 200;

  function make(tag, cls, attrs) {
    var e = document.createElement(tag);
    if (cls) { e.className = cls; }
    Object.keys(attrs || {}).forEach(function (k) { e.setAttribute(k, attrs[k]); });
    return e;
  }

  // The popup behaviour both kinds share. getItems() returns [{value, label, group}] fresh on each
  // call, because the choices can change (the Type list follows the Source). Callbacks:
  // onPick(item) when one is chosen, onClose() when the list closes.
  function popup(input, getItems, onPick, onClose) {
    var list = make("div", "combo-list", { role: "listbox", id: "combo-" + Math.random().toString(36).slice(2, 8) });
    list.hidden = true;
    input.parentNode.appendChild(list);
    input.setAttribute("role", "combobox");
    input.setAttribute("aria-autocomplete", "list");
    input.setAttribute("aria-expanded", "false");
    input.setAttribute("aria-controls", list.id);

    var shown = [];      // the items currently listed, in order
    var active = -1;     // index of the highlighted item, -1 = none

    function isOpen() { return !list.hidden; }

    function close() {
      var was = isOpen();
      list.hidden = true;
      active = -1;
      input.setAttribute("aria-expanded", "false");
      if (was) { onClose(); }
    }

    function highlight(i) {
      var rows = list.querySelectorAll(".combo-item");
      if (!rows.length) { active = -1; return; }
      active = (i + rows.length) % rows.length;
      rows.forEach(function (r, n) {
        r.classList.toggle("active", n === active);
        r.setAttribute("aria-selected", n === active ? "true" : "false");
      });
      rows[active].scrollIntoView({ block: "nearest" });
    }

    function pick(i) {
      var item = shown[i];
      if (!item) { return false; }
      list.hidden = true;
      active = -1;
      input.setAttribute("aria-expanded", "false");
      onPick(item);
      return true;
    }

    // query "" lists everything; selected (a value) is highlighted when the list opens.
    function open(query, selected) {
      var q = (query || "").trim().toLowerCase();
      var all = getItems();
      shown = (q ? all.filter(function (it) { return it.label.toLowerCase().indexOf(q) !== -1; }) : all).slice(0, MAX_ITEMS);
      list.textContent = "";
      if (!shown.length) {
        list.appendChild(make("div", "combo-empty")).textContent = window.i18n ? window.i18n("Tidak ada hasil", "No match") : "No match";
        list.hidden = false;
        input.setAttribute("aria-expanded", "true");
        active = -1;
        return;
      }
      var lastGroup = "";
      shown.forEach(function (it, n) {
        if (it.group && it.group !== lastGroup) {
          list.appendChild(make("div", "combo-group")).textContent = it.group;
        }
        lastGroup = it.group || "";
        var b = make("button", "combo-item", { type: "button", role: "option", tabindex: "-1" });
        b.textContent = it.label;
        if (it.muted) { b.classList.add("muted"); }
        // mousedown, not click: the input must keep focus, or its blur would close the list first.
        b.addEventListener("mousedown", function (e) { e.preventDefault(); pick(n); });
        list.appendChild(b);
      });
      list.hidden = false;
      input.setAttribute("aria-expanded", "true");
      var at = -1;
      if (selected !== undefined) {
        shown.forEach(function (it, n) { if (at < 0 && it.value === selected) { at = n; } });
      }
      highlight(at);
    }

    input.addEventListener("keydown", function (e) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        if (!isOpen()) { open("", undefined); }
        highlight(active + (e.key === "ArrowDown" ? 1 : -1));
      } else if (e.key === "Enter") {
        if (isOpen()) { e.preventDefault(); pick(active >= 0 ? active : 0); }
      } else if (e.key === "Tab") {
        // Let focus move on as usual; only keep what the person arrowed to.
        if (isOpen() && active >= 0) { pick(active); }
      } else if (e.key === "Escape") {
        if (isOpen()) { e.preventDefault(); close(); }
      }
    });
    document.addEventListener("mousedown", function (e) {
      if (isOpen() && e.target !== input && !list.contains(e.target)) { close(); }
    });

    return { open: open, close: close, isOpen: isOpen };
  }

  function enhanceSelect(sel) {
    var wrap = make("span", "combo combo-select");
    var input = make("input", "combo-input", { type: "text", autocomplete: "off", spellcheck: "false" });
    // The input goes before the select so a <label> around both points at the input.
    sel.parentNode.insertBefore(wrap, sel);
    wrap.appendChild(input);
    wrap.appendChild(sel);
    sel.hidden = true;
    sel.tabIndex = -1;
    if (sel.id) { input.id = sel.id + "-combo"; }

    // A hidden control that is "required" would block the form with no way to show why, so the
    // requirement moves to the visible input. The page may switch it again (the Type list does).
    function moveRequired() {
      if (sel.required) { input.required = true; sel.required = false; } else if (sel.dataset.comboSynced) { input.required = false; }
    }
    moveRequired();

    function options() {
      return Array.prototype.filter.call(sel.options, function (o) { return !o.disabled && !o.hidden; });
    }
    function labelOfValue() {
      var o = sel.options[sel.selectedIndex];
      return o && o.value !== "" ? o.textContent.trim() : "";
    }
    function placeholder() {
      var empty = Array.prototype.filter.call(sel.options, function (o) { return o.value === ""; })[0];
      return empty ? empty.textContent.trim() : "";
    }
    function show() {
      input.value = labelOfValue();
      input.placeholder = placeholder();
    }
    show();

    function getItems() {
      var items = [];
      options().forEach(function (o) {
        var g = o.parentNode.tagName === "OPTGROUP" ? o.parentNode.label : "";
        var empty = o.value === "";
        // The empty choice lets the person clear the field; it reads as the placeholder.
        items.push({ value: o.value, label: empty ? (o.textContent.trim() || "None") : o.textContent.trim(), group: g, muted: empty });
      });
      return items;
    }

    var box = popup(input, getItems, function (item) {
      var changed = sel.value !== item.value;
      sel.value = item.value;
      show();
      if (changed) { sel.dispatchEvent(new Event("change", { bubbles: true })); }
    }, show);

    // Opening shows the whole list with the current choice highlighted; typing then filters it.
    input.addEventListener("focus", function () { input.select(); box.open("", sel.value); });
    input.addEventListener("click", function () { if (!box.isOpen()) { box.open("", sel.value); } });
    input.addEventListener("input", function () { box.open(input.value, undefined); });
    // Leaving the field keeps only a real choice: the text goes back to the selected option.
    input.addEventListener("blur", function () { box.close(); show(); });
    sel.addEventListener("combo:sync", function () {
      sel.dataset.comboSynced = "1";
      moveRequired();
      show();
    });
  }

  function enhanceList(input) {
    var id = input.getAttribute("list");
    var dl = id ? document.getElementById(id) : null;
    if (!dl) { return; }
    input.removeAttribute("list"); // drop the browser's own suggestion popup
    var strict = input.hasAttribute("data-combo-strict");
    var wrap = make("span", "combo");
    input.parentNode.insertBefore(wrap, input);
    wrap.appendChild(input);
    input.classList.add("combo-input");

    function names() {
      return Array.prototype.map.call(dl.options, function (o) { return o.value; });
    }
    var committed = input.value; // the last value that was accepted, for strict fields

    var box = popup(input, function () {
      return names().map(function (n) { return { value: n, label: n, group: "" }; });
    }, function (item) {
      input.value = item.value;
      committed = item.value;
    }, function () {});

    input.addEventListener("focus", function () { box.open(input.value, undefined); });
    input.addEventListener("input", function () { box.open(input.value, undefined); });
    input.addEventListener("blur", function () {
      box.close();
      var t = input.value.trim().toLowerCase();
      var exact = names().filter(function (n) { return n.trim().toLowerCase() === t; })[0];
      if (exact) { input.value = exact; committed = exact; return; }
      if (strict) { input.value = t ? committed : ""; } // only a listed name can stay
      else { committed = input.value; }
    });
  }

  document.addEventListener("DOMContentLoaded", function () {
    document.querySelectorAll("select[data-combo]").forEach(enhanceSelect);
    document.querySelectorAll("input[data-combo-list]").forEach(enhanceList);
  });
})();
