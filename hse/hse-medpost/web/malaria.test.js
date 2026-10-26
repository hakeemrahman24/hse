const { readFileSync } = require("fs");
const { test } = require("node:test");
const assert = require("node:assert/strict");
const vm = require("vm");

// malaria.js runs in the browser; with no document only its pure helpers load.
const context = {};
vm.createContext(context);
vm.runInContext(readFileSync("web/static/malaria.js", "utf8"), context);
const { cleanHeadOfHousehold, sanitizeWeightText, parseWeightKg, matchPatients, patientHint, isoWeekLabel, filterRegions, barPercent, malariaDetailFields, speciesSegments, speciesLabel, nextIndex } = context;

test("head of household is cleaned and Title Cased like the server does", () => {
  const cases = {
    "budi   SANTOSO": "Budi Santoso",
    "budi123 santoso!": "Budi Santoso",
    "ba'u": "Ba'u",
    "anak-agung  made": "Anak-Agung Made",
    "h. abdul r.": "H. Abdul R.",
    "<b>x</b>": "Bxb",
    "yohanes pétrus": "Yohanes Pétrus",
    "1234": "",
  };
  for (const [input, want] of Object.entries(cases)) {
    assert.equal(cleanHeadOfHousehold(input, { final: true }), want, input);
  }
});

test("while typing, a space at the end is kept so the next word can start", () => {
  assert.equal(cleanHeadOfHousehold("budi "), "Budi ");
  assert.equal(cleanHeadOfHousehold("  budi"), "Budi");
  assert.equal(cleanHeadOfHousehold("budi  "), "Budi ");
});

test("weight text keeps digits and one decimal digit", () => {
  assert.equal(sanitizeWeightText("6a1,55"), "61.5");
  assert.equal(sanitizeWeightText("1.2.3"), "1.2");
  assert.equal(sanitizeWeightText("abc"), "");
  assert.equal(sanitizeWeightText("-5"), "5");
});

test("weight must be above 0 and at most 150", () => {
  assert.equal(parseWeightKg("61.5"), 61.5);
  assert.equal(parseWeightKg("150"), 150);
  assert.equal(parseWeightKg("150.1"), null);
  assert.equal(parseWeightKg("0"), null);
  assert.equal(parseWeightKg("0.0"), null);
  assert.equal(parseWeightKg(""), null);
  assert.equal(parseWeightKg("12."), null);
});

test("patient search matches every typed word, best matches first", () => {
  const patients = [
    { patientId: 1, name: "Siti Budi Aminah", dateOfBirth: "1990-01-01", companyName: "A" },
    { patientId: 2, name: "Budi Santoso", dateOfBirth: "1985-05-05", companyName: "PT Contoh" },
    { patientId: 3, name: "Andi Wijaya", dateOfBirth: "", companyName: "" },
  ];
  assert.deepEqual([...matchPatients(patients, "budi").map((p) => p.patientId)], [2, 1]);
  assert.deepEqual([...matchPatients(patients, "san bud").map((p) => p.patientId)], [2]);
  assert.equal(matchPatients(patients, "  ").length, 0);
  assert.equal(matchPatients(patients, "zzz").length, 0);
});

test("patient search shows at most twenty", () => {
  const many = Array.from({ length: 50 }, (_, i) => ({ patientId: i, name: "Budi " + i }));
  assert.equal(matchPatients(many, "budi").length, 20);
});

test("the hint separates people with the same name", () => {
  assert.equal(patientHint({ dateOfBirth: "1985-05-05", companyName: "PT Contoh" }), "1985-05-05 · PT Contoh");
  assert.equal(patientHint({ dateOfBirth: "", companyName: "PT Contoh" }), "PT Contoh");
});

test("week of the year is the ISO week, the same answers as the server's tests", () => {
  assert.equal(isoWeekLabel("2026-01-01"), "2026-W01");
  assert.equal(isoWeekLabel("2026-10-20"), "2026-W43");
  assert.equal(isoWeekLabel("2027-01-01"), "2026-W53"); // belongs to the previous week-year
  assert.equal(isoWeekLabel("2024-12-30"), "2025-W01");
  assert.equal(isoWeekLabel("2026-02-30"), "");
  assert.equal(isoWeekLabel("bad"), "");
  assert.equal(isoWeekLabel(""), "");
});

test("region search keeps names containing every typed word, in the given order", () => {
  const regions = [
    { code: "91.03", name: "Kabupaten Jayapura" },
    { code: "91.05", name: "Kabupaten Kepulauan Yapen" },
    { code: "91.71", name: "Kota Jayapura" },
  ];
  assert.deepEqual([...filterRegions(regions, "jayap").map((r) => r.code)], ["91.03", "91.71"]);
  assert.deepEqual([...filterRegions(regions, "kota jay").map((r) => r.code)], ["91.71"]);
  assert.deepEqual([...filterRegions(regions, "  KAB  ").map((r) => r.code)], ["91.03", "91.05"]);
  assert.equal(filterRegions(regions, "").length, 3);
  assert.equal(filterRegions(regions, "zzz").length, 0);
});

test("bar widths are relative to the longest bar and never vanish", () => {
  assert.equal(barPercent(10, 10), 100);
  assert.equal(barPercent(5, 10), 50);
  assert.equal(barPercent(1, 1000), 2);
  assert.equal(barPercent(0, 10), 0);
  assert.equal(barPercent(3, 0), 0);
});

test("an opened row lists every field of the record, readable", () => {
  const row = {
    testDate: "2026-10-20", week: "2026-W43", slideInitial: "BSU1", patientName: "Budi Santoso", ageYears: 36, weightKg: 61.5,
    headOfHousehold: "Budi Santoso", headIsPatient: true, regency: "Kabupaten Nabire", district: "Wanggar", village: "Bumi Mulia",
    workPosition: "Welder", company: "PT Contoh", examiner: "Dr Examiner", purpose: "Symptomatic", examType: "BloodSmear", result: null,
    recordedAt: "2026-10-20T03:00:00",
  };
  const fields = Object.fromEntries(malariaDetailFields(row).map(([k, v]) => [k, v]));
  assert.equal(fields["Age"], "36 years");
  assert.equal(fields["Weight"], "61.5 kg");
  assert.equal(fields["Head of household"], "Budi Santoso (same as the patient)");
  assert.equal(fields["Screening purpose"], "Symptomatic");
  assert.equal(fields["Test"], "Blood smear");
  assert.equal(fields["Result"], "Pending");
  assert.equal(fields["Recorded at (UTC)"], "2026-10-20 03:00:00");
  assert.equal(malariaDetailFields({ ...row, ageYears: null })[4][1], "");
  assert.equal(malariaDetailFields({ ...row, headIsPatient: false, headOfHousehold: "Siti" })[6][1], "Siti");
});

test("speciesSegments lays the species that have cases end to end", () => {
  const got = speciesSegments({ Pv: 5, Pf: 0, Po: 3, Mixed: 2 }, 10);
  assert.equal(got.length, 3);
  assert.equal(JSON.stringify(got.map((s) => [s.key, s.x, s.width])), JSON.stringify([["Pv", 0, 50], ["Po", 50, 30], ["Mixed", 80, 20]]));
  assert.equal(speciesSegments({}, 10).length, 0);
  assert.equal(speciesSegments(undefined, 10).length, 0);
});

test("speciesSegments never runs past the end of the bar", () => {
  const got = speciesSegments({ Pv: 1, Pf: 1000 }, 1000);
  const last = got[got.length - 1];
  assert.ok(last.x + last.width <= 100);
});

test("speciesLabel lists only the species with cases", () => {
  assert.equal(speciesLabel({ Pv: 2, Pf: 0, Po: 0, Mixed: 1 }), "Pv 2, Mixed 1");
  assert.equal(speciesLabel({ Pv: 0 }), "");
  assert.equal(speciesLabel(undefined), "");
});

test("nextIndex moves the highlight and wraps at both ends", () => {
  assert.equal(nextIndex(-1, 1, 3), 0);
  assert.equal(nextIndex(-1, -1, 3), 2);
  assert.equal(nextIndex(0, 1, 3), 1);
  assert.equal(nextIndex(2, 1, 3), 0);
  assert.equal(nextIndex(0, -1, 3), 2);
  assert.equal(nextIndex(-1, 1, 0), -1);
  assert.equal(nextIndex(5, 1, 0), -1);
});
