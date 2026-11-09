// Settings > SOFI questionnaire (admin only): the questionnaire as workers see it at /sofi, with a
// way to reword each item. Reads GET /api/sofi-items and saves with PUT /api/sofi-items/{number}.
// Only the words can change; the server keeps the dimensions, the item count and the scoring.
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
  var editing = 0;     // number of the item being edited, 0 for none

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

  function dimensionName(key, lang) {
    var d = data.dimensions.filter(function (x) { return x.key === key; })[0];
    return d ? d[lang] : key;
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
    head.appendChild(button("Edit", "btn-ghost", function () { editing = item.number; draw(); }));
    li.appendChild(head);
    li.appendChild(el("div", "sofiq-dim", dimensionName(item.dimension, lang)));
    li.appendChild(scale());
    return li;
  }

  function editRow(item) {
    var li = el("li", "sofiq-item sofiq-editing");
    var inputs = {};
    LANGS.forEach(function (pair) {
      var label = el("label", "sofiq-field");
      label.appendChild(el("span", "", pair[1]));
      var input = document.createElement("input");
      input.type = "text";
      input.maxLength = data.maxTextRunes;
      input.value = item[pair[0]];
      input.placeholder = item.default[pair[0]];
      inputs[pair[0]] = input;
      label.appendChild(input);
      li.appendChild(label);
    });
    li.appendChild(el("div", "sofiq-dim", "Dimension: " + dimensionName(item.dimension, "en") + " (fixed). Leave a language blank to use the built-in wording."));
    var actions = el("div", "sofiq-actions");
    actions.appendChild(button("Save", "btn-primary", function () {
      save(item.number, { en: inputs.en.value, id: inputs.id.value, zh: inputs.zh.value });
    }));
    actions.appendChild(button("Cancel", "btn-ghost", function () { editing = 0; showMessage(""); draw(); }));
    actions.appendChild(button("Use built-in wording", "btn-ghost", function () {
      save(item.number, { en: "", id: "", zh: "" });
    }));
    li.appendChild(actions);
    return li;
  }

  function draw() {
    var lang = langEl.value;
    instructionEl.textContent = data.instruction[lang];
    lowEl.textContent = "0 = " + data.anchorLow[lang];
    highEl.textContent = "6 = " + data.anchorHigh[lang];
    listEl.replaceChildren.apply(listEl, data.items.map(function (item) {
      return item.number === editing ? editRow(item) : readOnlyRow(item, lang);
    }));
  }

  async function save(number, body) {
    showMessage("");
    var res = await fetch("/api/sofi-items/" + encodeURIComponent(number), {
      method: "PUT",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body)
    });
    var out = await readJson(res);
    if (!res.ok) { showMessage(out.error || "Could not save the wording."); return; }
    data = out;
    editing = 0;
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
