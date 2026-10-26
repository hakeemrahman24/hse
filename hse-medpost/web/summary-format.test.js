const { readFileSync } = require("fs");
const { test } = require("node:test");
const assert = require("node:assert/strict");
const vm = require("vm");

const context = {};
vm.createContext(context);
vm.runInContext(readFileSync("web/static/summary-format.js", "utf8"), context);
vm.runInContext(readFileSync("web/static/case-guard.js", "utf8"), context);

const { renderActivitySummary, applySummaryList, continueSummaryList, toSentenceCase } = context;

test("plain text stays escaped and keeps line breaks", () => {
  const html = renderActivitySummary("Checked <b>clinic</b>.\nSecond line.");
  assert.equal(html, "<p>Checked &lt;b&gt;clinic&lt;/b&gt;.<br>Second line.</p>");
});

test("bullet lines become a list", () => {
  const html = renderActivitySummary("- Gloves restocked\n- Spill kit checked");
  assert.equal(html, "<ul><li>Gloves restocked</li><li>Spill kit checked</li></ul>");
});

test("numbered lines keep their starting number", () => {
  const html = renderActivitySummary("2. Fit with note\n3. Need follow up");
  assert.equal(html, '<ol start="2"><li>Fit with note</li><li>Need follow up</li></ol>');
});

test("a blank line separates a paragraph from a list", () => {
  const html = renderActivitySummary("Findings:\n\n- Open case\n- Closed case");
  assert.equal(html, "<p>Findings:</p><ul><li>Open case</li><li>Closed case</li></ul>");
});

test("empty summary renders nothing", () => {
  assert.equal(renderActivitySummary("  \n"), "");
});

test("a paragraph, a list, and another paragraph stay in that order", () => {
  const html = renderActivitySummary(
    "Checked the clinic.\n- Gloves restocked\n- Spill kit checked\nFollow up next week."
  );
  assert.equal(
    html,
    "<p>Checked the clinic.</p><ul><li>Gloves restocked</li><li>Spill kit checked</li></ul><p>Follow up next week.</p>"
  );
});

test("bullet button prefixes each selected line", () => {
  const next = applySummaryList("Gloves\nSpill kit", 0, 15, "bullet");
  assert.equal(next.value, "- Gloves\n- Spill kit");
  assert.equal(next.selectionStart, next.selectionEnd);
  assert.equal(next.selectionStart, next.value.length);
});

test("bullet button removes markers when every line is already a bullet", () => {
  const next = applySummaryList("- Gloves\n- Spill kit", 0, 20, "bullet");
  assert.equal(next.value, "Gloves\nSpill kit");
});

test("number button numbers the selected lines and can be toggled off", () => {
  const numbered = applySummaryList("Gloves\nSpill kit", 0, 15, "number");
  assert.equal(numbered.value, "1. Gloves\n2. Spill kit");
  const plain = applySummaryList(numbered.value, 0, numbered.value.length, "number");
  assert.equal(plain.value, "Gloves\nSpill kit");
});

test("enter continues a bullet and drops an empty marker", () => {
  const continued = continueSummaryList("- Gloves", 8, 8);
  assert.equal(continued.value, "- Gloves\n- ");
  assert.equal(continued.selectionStart, continued.value.length);

  const exited = continueSummaryList(continued.value, continued.selectionStart, continued.selectionEnd);
  assert.equal(exited.value, "- Gloves\n");
  assert.equal(exited.selectionStart, exited.value.length);
});

test("enter continues a numbered list and renumbers the lines below", () => {
  const next = continueSummaryList("1. Gloves\n2. Spill kit", 9, 9);
  assert.equal(next.value, "1. Gloves\n2. \n3. Spill kit");
  assert.equal(next.selectionStart, "1. Gloves\n2. ".length);
});

test("a new line in the summary starts a new sentence", () => {
  assert.equal(toSentenceCase("walked the fence.\n- second item"), "Walked the fence.\n- Second item");
});

test("a list nested inside a paragraph stays separate from the following lines", () => {
  context.Node = { TEXT_NODE: 3, ELEMENT_NODE: 1 };
  const text = (value) => ({ nodeType: 3, nodeName: "#text", textContent: value, childNodes: [] });
  const br = { nodeType: 1, nodeName: "BR", childNodes: [], getAttribute() { return null; }, querySelectorAll() { return []; } };
  const item = {
    nodeType: 1,
    nodeName: "LI",
    childNodes: [text("Sore throat")],
    getAttribute() { return null; },
    querySelectorAll() { return []; },
  };
  const list = {
    nodeType: 1,
    nodeName: "UL",
    childNodes: [item],
    getAttribute() { return null; },
    querySelectorAll(sel) { return sel === ":scope > li" ? [item] : []; },
  };
  const paragraph = {
    nodeType: 1,
    nodeName: "P",
    childNodes: [list, text("Warning sign : high fever"), br, text("Prevention : wear a mask")],
    getAttribute() { return null; },
    querySelectorAll() { return []; },
  };
  assert.equal(
    context.serializeSummary({ childNodes: [paragraph] }),
    "- Sore throat\nWarning sign : high fever\nPrevention : wear a mask"
  );
});

test("a line break inside a summary block is kept when the editor saves", () => {
  context.Node = { TEXT_NODE: 3, ELEMENT_NODE: 1 };
  const text = (value) => ({ nodeType: 3, nodeName: "#text", textContent: value, childNodes: [] });
  const br = { nodeType: 1, nodeName: "BR", childNodes: [], getAttribute() { return null; }, querySelectorAll() { return []; } };
  const paragraph = {
    nodeType: 1,
    nodeName: "P",
    childNodes: [text("Sore throat"), br, text("Warning sign : high fever")],
    getAttribute() { return null; },
    querySelectorAll() { return []; },
  };
  assert.equal(context.serializeSummary({ childNodes: [paragraph] }), "Sore throat\nWarning sign : high fever");
});
