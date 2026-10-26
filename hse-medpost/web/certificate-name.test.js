const { readFileSync } = require("fs");
const { test } = require("node:test");
const assert = require("node:assert/strict");
const vm = require("vm");

const context = {};
vm.createContext(context);
vm.runInContext(readFileSync("web/static/certificate-name.js", "utf8"), context);
const { certificateOpenFilename, certificateOpenHref } = context;

test("open filename is checkup id, company abbreviation, name, and valid-until", () => {
  assert.equal(
    certificateOpenFilename("MedC0011", "HSE", "John Doe", "2027-03-15"),
    "MedC0011-HSE-JohnDoe-20270315.pdf",
  );
  assert.equal(
    certificateOpenFilename("MedC0007", "SITE", "dr. Edric Sugito, MKKK", "2026-10-01"),
    "MedC0007-SITE-drEdricSugitoMKKK-20261001.pdf",
  );
  assert.equal(certificateOpenFilename("  ", "", "  ", ""), "checkup-NA-Unknown-nodate.pdf");
});

test("the open link ends with that filename", () => {
  assert.equal(
    certificateOpenHref(12, "MedC0011", "HSE", "John Doe", "2027-03-15"),
    "/api/mcu-records/12/MedC0011-HSE-JohnDoe-20270315.pdf",
  );
});
