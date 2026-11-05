const { readFileSync } = require("fs");
const { test } = require("node:test");
const assert = require("node:assert/strict");
const vm = require("vm");

// reference-tab.js runs in the browser; here it is loaded with no document, so
// only its pure helpers are exercised (the page wiring is skipped without one).
function load(extra = {}) {
  const context = { ...extra };
  vm.createContext(context);
  vm.runInContext(readFileSync("web/static/reference-tab.js", "utf8"), context);
  return context;
}

const ctx = load();
const { referenceSafeHref, referenceFilterItems, referenceItemMeta, referenceFileProblem, referenceSectionLabel, referenceTypeLabel } = ctx;

test("only plain http and https addresses become a clickable link", () => {
  assert.equal(referenceSafeHref("https://example.org/who"), "https://example.org/who");
  assert.equal(referenceSafeHref("  http://example.org  "), "http://example.org");
  // Anything else would run script or leave the app when clicked.
  for (const bad of ["javascript:alert(1)", "JaVaScRiPt:alert(1)", "data:text/html,x", "ftp://example.org", "//example.org", "example.org", "https://a b", "", null, undefined]) {
    assert.equal(referenceSafeHref(bad), "", String(bad));
  }
});

test("search matches the title, notes and type label without regard to case, and blank shows everything", () => {
  const items = [
    { title: "Malaria SOP", notes: "Rapid test steps", documentsType: "Procedure" },
    { title: "Intake form", notes: "", documentsType: "Forms" },
  ];
  assert.equal(referenceFilterItems(items, "").length, 2);
  assert.equal(referenceFilterItems(items, "   ").length, 2);
  assert.deepEqual(referenceFilterItems(items, "MALARIA").map((i) => i.title), ["Malaria SOP"]);
  assert.deepEqual(referenceFilterItems(items, "rapid").map((i) => i.title), ["Malaria SOP"]);
  assert.deepEqual(referenceFilterItems(items, "form").map((i) => i.title), ["Intake form"]);
  assert.deepEqual(referenceFilterItems(items, "nothing like this"), []);
});

test("filtering returns a new list and leaves the original alone", () => {
  const items = [{ title: "A" }, { title: "B" }];
  const out = referenceFilterItems(items, "a");
  assert.notEqual(out, items);
  assert.equal(items.length, 2);
});

test("a file item says its version and when it was updated; a link just says Link", () => {
  assert.deepEqual([...referenceItemMeta({ hasFile: true, versionNo: 3, updatedDate: "2026-11-05" })], ["File · v3", "Updated 2026-11-05"]);
  assert.deepEqual([...referenceItemMeta({ hasFile: false, versionNo: 0, formLink: "https://example.org" })], ["Link"]);
  // A file with no recorded date (older rows) simply omits the date.
  assert.deepEqual([...referenceItemMeta({ hasFile: true, versionNo: 1, updatedDate: "" })], ["File · v1"]);
});

test("the updated date is shown as dd/mm/yyyy when the shared date helper is on the page", () => {
  const withHelper = load({ isoToDmy: (iso) => iso.split("-").reverse().join("/") });
  assert.deepEqual([...withHelper.referenceItemMeta({ hasFile: true, versionNo: 2, updatedDate: "2026-11-05" })], ["File · v2", "Updated 05/11/2026"]);
});

test("a chosen file is checked for type, emptiness and the 10 MB cap before it is sent", () => {
  const MB = 1024 * 1024;
  assert.equal(referenceFileProblem({ name: "form.pdf", size: 1000 }), "");
  assert.equal(referenceFileProblem({ name: "FORM.DOCX", size: 10 * MB }), ""); // exactly the cap is allowed
  assert.match(referenceFileProblem({ name: "form.pdf", size: 10 * MB + 1 }), /10 MB/);
  assert.match(referenceFileProblem({ name: "run.exe", size: 10 }), /PDF, DOCX, XLSX, PNG or JPG/);
  assert.match(referenceFileProblem({ name: "noextension", size: 10 }), /PDF, DOCX, XLSX, PNG or JPG/);
  assert.match(referenceFileProblem({ name: "empty.pdf", size: 0 }), /empty/);
  assert.match(referenceFileProblem(null), /choose a file/i);
});

test("section and type keys have readable labels, and unknown ones fall back to the key", () => {
  assert.equal(referenceSectionLabel("alcohol"), "Drug and Alcohol");
  assert.equal(referenceSectionLabel("malaria"), "Malaria");
  assert.equal(referenceSectionLabel("mystery"), "mystery");
  assert.equal(referenceTypeLabel("SocializationMaterial"), "Socialization material");
  assert.equal(referenceTypeLabel("Forms"), "Form");
  assert.equal(referenceTypeLabel("Other"), "Other");
});
