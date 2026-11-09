// Settings > SOFI questionnaire (admin only): the questionnaire as workers see it at /sofi, grouped by
// dimension, with a way to reword each item and rename each dimension. Reads GET /api/sofi-items and
// saves with PUT /api/sofi-items/{number} and PUT /api/sofi-dimensions/{key}. Only the words can
// change; the server keeps which items belong to which dimension, the item count and the scoring.
(function () {
  "use strict";

  var listEl = document.getElementById("sofiq-list");
  if (!listEl) { return; }
  var langEl = document.getElementById("sofiq-lang");
  var msgEl = document.getElementById("sofiq-msg");
  var instructionEl = document.getElementById("sofiq-instruction");
  var lowEl = document.getElementById("sofiq-low");
  var highEl = document.getElementById("sofiq-high");
  var LANGS = [["en", "English"], ["id", "Bahasa Indonesia"], ["zh", "简体中文"]];

  var data = null;     // the last GET /api/sofi-items
  var editing = "";    // what is being edited: "item:3" or "dimension:sleepiness"; "" for nothing

  function showMessage(text) {
    msgEl.textContent = text || "";
    msgEl.hidden = !text;
  }

  async function readJson(res) {
    try { return await res.json(); } catch (e) { return {}; }
  }

  // The form shows an Indonesian explanation with a capital first letter (Item.Text on the server).
  function shown(lang, text) {
    return lang === "id" && text ? text.charAt(0).toUpperCase() + text.slice(1) : text;
  }

  function el(tag, className, text) {
    var node = document.createElement(tag);
    if (className) { node.className = className; }
    if (text !== undefined) { node.textContent = text; }
    return node;
  }

  function scale() {
    var row = el("div", "sofiq-scale");
    data.scale.forEach(function (n) { row.appendChild(el("span", "", String(n))); });
    return row;
  }

  function button(label, className, onClick) {
    var b = el("button", className, label);
    b.type = "button";
    b.addEventListener("click", onClick);
    return b;
  }

  function readOnlyRow(item, lang) {
    var li = el("li", "sofiq-item");
    var head = el("div", "sofiq-head");
    head.appendChild(el("span", "sofiq-text", shown(lang, item[lang])));
    if (item.custom) { head.appendChild(el("span", "sofiq-tag", "Edited")); }
    head.appendChild(button("Edit", "btn-ghost", function () { editing = "item:" + item.number; draw(); }));
    li.appendChild(head);
    li.appendChild(scale());
    return li;
  }

  // The heading of a dimension, with its name in the chosen language.
  function dimensionRow(dim, lang) {
    var li = el("li", "sofiq-dimhead");
    li.appendChild(el("span", "sofiq-dimname", dim[lang]));
    li.appendChild(el("span", "sofiq-dimitems", "items " + dim.items[0] + "–" + dim.items[dim.items.length - 1]));
    if (dim.custom) { li.appendChild(el("span", "sofiq-tag", "Renamed")); }
    li.appendChild(button("Rename", "btn-ghost", function () { editing = "dimension:" + dim.key; draw(); }));
    return li;
  }

  // The three-language editor shared by an item and a dimension. `entry` has en, id, zh and default;
  // `note` is the line under the boxes; `onSave` gets {en, id, zh}.
  function editRow(entry, note, onSave, className) {
    var li = el("li", className);
    var inputs = {};
    LANGS.forEach(function (pair) {
      var label = el("label", "sofiq-field");
      label.appendChild(el("span", "", pair[1]));
      var input = document.createElement("input");
      input.type = "text";
      input.maxLength = data.maxTextRunes;
      input.value = entry[pair[0]];
      input.placeholder = entry.default[pair[0]];
      inputs[pair[0]] = input;
      label.appendChild(input);
      li.appendChild(label);
    });
    li.appendChild(el("div", "sofiq-dim", note));
    var actions = el("div", "sofiq-actions");
    actions.appendChild(button("Save", "btn-primary", function () {
      onSave({ en: inputs.en.value, id: inputs.id.value, zh: inputs.zh.value });
    }));
    actions.appendChild(button("Cancel", "btn-ghost", function () { editing = ""; showMessage(""); draw(); }));
    actions.appendChild(button("Use built-in wording", "btn-ghost", function () {
      onSave({ en: "", id: "", zh: "" });
    }));
    li.appendChild(actions);
    return li;
  }

  function draw() {
    var lang = langEl.value;
    instructionEl.textContent = data.instruction[lang];
    lowEl.textContent = "0 = " + data.anchorLow[lang];
    highEl.textContent = "6 = " + data.anchorHigh[lang];
    var rows = [];
    data.dimensions.forEach(function (dim) {
      if (editing === "dimension:" + dim.key) {
        rows.push(editRow(dim, "Name of the dimension. Which items it holds and how it is scored are fixed. Leave a language blank to use the built-in name.",
          function (body) { save("/api/sofi-dimensions/" + encodeURIComponent(dim.key), body); }, "sofiq-item sofiq-editing sofiq-dimedit"));
      } else {
        rows.push(dimensionRow(dim, lang));
      }
      data.items.filter(function (item) { return item.dimension === dim.key; }).forEach(function (item) {
        rows.push(editing === "item:" + item.number
          ? editRow(item, "Item " + item.number + " in " + dim.en + " (fixed). Leave a language blank to use the built-in wording.",
            function (body) { save("/api/sofi-items/" + encodeURIComponent(item.number), body); }, "sofiq-item sofiq-editing")
          : readOnlyRow(item, lang));
      });
    });
    listEl.replaceChildren.apply(listEl, rows);
  }

  async function save(url, body) {
    showMessage("");
    var res = await fetch(url, {
      method: "PUT",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body)
    });
    var out = await readJson(res);
    if (!res.ok) { showMessage(out.error || "Could not save."); return; }
    data = out;
    editing = "";
    draw();
  }

  async function load() {
    showMessage("");
    var res = await fetch("/api/sofi-items", { credentials: "same-origin" });
    var out = await readJson(res);
    if (!res.ok) { showMessage(out.error || "Could not load the questionnaire."); return; }
    data = out;
    draw();
  }

  langEl.addEventListener("change", function () { if (data) { draw(); } });
  // Reload when the section is opened, so an edit made on another screen shows up.
  var opener = document.querySelector('[data-section="sofi-questionnaire"]');
  if (opener) { opener.addEventListener("click", load); }
  load();
})();
