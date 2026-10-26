const { readFileSync } = require("fs");
const { test } = require("node:test");
const assert = require("node:assert/strict");
const vm = require("vm");

// da-form-core.js runs in the browser; with no document only its pure helpers load.
const context = {};
vm.createContext(context);
vm.runInContext(readFileSync("web/static/da-form-core.js", "utf8"), context);
const { daBlankValues, daMissing, daFirstName, daStampText, daFormatDateLong, daFormatDateTime, daLocalDateTime, daRecord, daPdfName } = context;

function filled() {
  const v = daBlankValues();
  v.donorName = "Budi Santoso";
  v.donorCompany = "PT Contoh";
  v.donorPosition = "Operator";
  v.testType = "random";
  v.consentDt = "2026-10-07T08:55";
  return v;
}

test("a form with nothing answered lists every missing answer, signature last", () => {
  assert.equal(
    JSON.stringify(daMissing(daBlankValues(), {})),
    JSON.stringify(["Donor name", "Company", "Position", "Test type", "Consent date and time", "Donor signature"]),
  );
});

test("a complete form with the donor's signature has nothing missing", () => {
  assert.equal(daMissing(filled(), { donor: true }).length, 0);
  assert.equal(JSON.stringify(daMissing(filled(), {})), JSON.stringify(["Donor signature"]));
  assert.equal(JSON.stringify(daMissing(filled(), undefined)), JSON.stringify(["Donor signature"]));
});

test("spaces alone are not an answer", () => {
  const v = filled();
  v.donorCompany = "   ";
  assert.equal(JSON.stringify(daMissing(v, { donor: true })), JSON.stringify(["Company"]));
});

test("a signature is the first name, titles skipped, capitalised", () => {
  assert.equal(daFirstName("Budi Santoso"), "Budi");
  assert.equal(daFirstName("  siti   aminah "), "Siti");
  assert.equal(daFirstName("dr. Arif Rahman"), "Arif");
  assert.equal(daFirstName("H. Ahmad"), "Ahmad");
  assert.equal(daFirstName("dr."), "Dr."); // only a title: use it as written
  assert.equal(daFirstName(""), "");
  assert.equal(daFirstName(null), "");
});

test("the stamp reads day, month, year, time and zone", () => {
  const stamp = daStampText(new Date(2026, 9, 7, 8, 5));
  assert.match(stamp, /^07 Oct 2026, 08:05 UTC[+-]\d+(:\d\d)?$/);
});

test("dates are spelled out for the PDF, odd values left alone", () => {
  assert.equal(daFormatDateLong("2026-10-07"), "07 October 2026");
  assert.equal(daFormatDateLong(""), "");
  assert.equal(daFormatDateLong("soon"), "soon");
  assert.equal(daFormatDateLong("2026-13-07"), "2026-13-07");
  assert.equal(daFormatDateTime("2026-10-07T08:55"), "07 October 2026 08:55");
  assert.equal(daFormatDateTime("2026-10-07"), "");
  assert.equal(daLocalDateTime(new Date(2026, 0, 2, 3, 4)), "2026-01-02T03:04");
});

test("the record has the keys the server stores, with trimmed text and true/false boxes", () => {
  const v = filled();
  v.donorNik = " 123 ";
  v.med1Name = "Paracetamol";
  v.med1Presc = "yes";
  v.b1Mouth = true;
  v.b1Res = "neg";
  v.pneg.amp = true;
  v.finalRes = "neg";
  const now = new Date("2026-10-07T01:00:00.000Z");
  const r = daRecord(v, "abc-1", now);
  assert.equal(r.id, "abc-1");
  assert.equal(r.created_at, "2026-10-07T01:00:00.000Z");
  assert.equal(r.form_version, "web1");
  assert.equal(r.donor_nik, "123");
  assert.equal(r.donor_sign_name, "Budi Santoso");
  assert.equal(r.consent_at, "2026-10-07T08:55");
  assert.equal(r.med1_prescribed, "yes");
  assert.equal(r.breath1_mouthpiece_sealed, true);
  assert.equal(r.breath2_mouthpiece_sealed, false);
  assert.equal(r.panel_negative.amp, true);
  assert.equal(r.panel_nonnegative.amp, false);
  assert.equal(JSON.stringify(Object.keys(r.panel_negative)), JSON.stringify(["amp", "met", "thc", "coc", "mop", "bzo"]));
  assert.equal(r.final_result, "neg");
  assert.equal(r.med_none, false);
});

test("every key of the record is one the server reads", () => {
  // The same list as drugAlcoholFields in internal/screening/record.go, plus form_version.
  const server = readFileSync("internal/screening/record.go", "utf8");
  const keys = [...server.matchAll(/\{"([a-z0-9_]+)", "[A-Za-z0-9]+"\}/g)].map((m) => m[1]);
  const sent = Object.keys(daRecord(filled(), "x", new Date())).filter((k) => k !== "form_version");
  assert.equal(JSON.stringify([...sent].sort()), JSON.stringify([...keys].sort()));
});

test("the PDF is named after the donor and the moment", () => {
  assert.equal(daPdfName("Budi Santoso", new Date(2026, 9, 7, 8, 5, 9)), "da-consent-result-budi-santoso-20261007-080509.pdf");
  assert.equal(daPdfName("", new Date(2026, 9, 7, 8, 5, 9)), "da-consent-result-20261007-080509.pdf");
  assert.equal(daPdfName("Ünal / O'Neil", new Date(2026, 0, 2, 3, 4, 5)), "da-consent-result-nal-o-neil-20260102-030405.pdf");
});
