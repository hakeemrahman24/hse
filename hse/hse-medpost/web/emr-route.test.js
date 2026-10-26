const { readFileSync } = require("fs");
const { test } = require("node:test");
const assert = require("node:assert/strict");
const vm = require("vm");

// emr.js runs in the browser; here it is loaded with no document, so only its
// pure route helpers are exercised (the page wiring is skipped without one).
const context = {};
vm.createContext(context);
vm.runInContext(readFileSync("web/static/emr.js", "utf8"), context);

const { parseMedicalRecordRoute, medicalRecordHash, medicalRecordHasTestLog, medicalRecordHasMalariaForm, medicalRecordTitleParts } = context;

test("an empty or unknown hash selects nothing, so the page shows its prompt", () => {
  assert.equal(parseMedicalRecordRoute(""), null);
  assert.equal(parseMedicalRecordRoute("#"), null);
  assert.equal(parseMedicalRecordRoute("#bogus"), null);
});

test("each plain section parses to itself with no sub-header", () => {
  for (const section of ["outpatient", "inpatient", "wellness"]) {
    assert.deepEqual({ ...parseMedicalRecordRoute("#" + section) }, { section, sub: null });
  }
});

test("screening opens on alcohol unless a sub-header is named", () => {
  assert.deepEqual({ ...parseMedicalRecordRoute("#screening") }, { section: "screening", sub: "alcohol" });
  assert.deepEqual({ ...parseMedicalRecordRoute("#screening/fatigue") }, { section: "screening", sub: "fatigue" });
  assert.deepEqual({ ...parseMedicalRecordRoute("#screening/alcohol") }, { section: "screening", sub: "alcohol" });
});

test("malaria is a screening sub-header", () => {
  assert.deepEqual({ ...parseMedicalRecordRoute("#screening/malaria") }, { section: "screening", sub: "malaria" });
});

test("an unknown screening sub-header falls back to alcohol", () => {
  assert.deepEqual({ ...parseMedicalRecordRoute("#screening/unknown") }, { section: "screening", sub: "alcohol" });
});

test("only alcohol and fatigue have a test log to show", () => {
  assert.equal(medicalRecordHasTestLog({ section: "screening", sub: "alcohol" }), true);
  assert.equal(medicalRecordHasTestLog({ section: "screening", sub: "fatigue" }), true);
  assert.equal(medicalRecordHasTestLog({ section: "screening", sub: "malaria" }), false);
  assert.equal(medicalRecordHasTestLog({ section: "outpatient", sub: null }), false);
  assert.equal(medicalRecordHasTestLog(null), false);
});

test("the alcohol view is titled Drug and Alcohol", () => {
  assert.equal(vm.runInContext("MEDICAL_RECORD_TEXT.alcohol.title", context), "Screening · Drug and Alcohol");
});

test("only screening has sub-headers", () => {
  assert.deepEqual({ ...parseMedicalRecordRoute("#outpatient/alcohol") }, { section: "outpatient", sub: null });
});

test("a route turns back into the same hash", () => {
  assert.equal(medicalRecordHash({ section: "wellness", sub: null }), "#wellness");
  assert.equal(medicalRecordHash({ section: "screening", sub: "fatigue" }), "#screening/fatigue");
});

test("only the malaria sub-header shows the malaria form", () => {
  assert.equal(medicalRecordHasMalariaForm({ section: "screening", sub: "malaria" }), true);
  assert.equal(medicalRecordHasMalariaForm({ section: "screening", sub: "fatigue" }), false);
  assert.equal(medicalRecordHasMalariaForm({ section: "wellness", sub: null }), false);
  assert.equal(medicalRecordHasMalariaForm(null), false);
});

test("the heading splits into a dark part and an accent part", () => {
  assert.deepEqual([...medicalRecordTitleParts("Screening · Fatigue")], ["Screening ·", "Fatigue"]);
  assert.deepEqual([...medicalRecordTitleParts("Screening · Drug and Alcohol")], ["Screening ·", "Drug and Alcohol"]);
  assert.deepEqual([...medicalRecordTitleParts("Medical Record")], ["Medical", "Record"]);
  assert.deepEqual([...medicalRecordTitleParts("Outpatient")], ["Outpatient", ""]);
});
