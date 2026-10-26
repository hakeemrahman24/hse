const { readFileSync } = require("fs");
const { test } = require("node:test");
const assert = require("node:assert/strict");
const vm = require("vm");

// fatigue-form-core.js runs in the browser; with no document only its pure helpers load.
const context = {};
vm.createContext(context);
vm.runInContext(readFileSync("web/static/fatigue-form-core.js", "utf8"), context);
const { faScore, faBandIndex, faBpLevel, faCleanName, faNumber, faBlankValues, faProblems, faRecord, faPdfName } = context;

function filled() {
  const v = faBlankValues();
  Object.assign(v, { examiner: "Dewi", name: "Budi", company: "PT A", position: "Driver", sys: 120, dia: 80, pulse: 70, healthy: "Y", medicine: "N", x: 7, y: 13, z: 10 });
  return v;
}
const both = { worker: true, assessor: true };

test("points follow the sheet: 4 per hour short of 6, 2 per hour short of 12, 1 per hour past 14", () => {
  assert.equal(JSON.stringify(faScore(5, 11, 16)), JSON.stringify({ x: 4, y: 2, z: 2, total: 8 }));
  assert.equal(JSON.stringify(faScore(6, 12, 14)), JSON.stringify({ x: 0, y: 0, z: 0, total: 0 }));
  assert.equal(faScore(5.5, 12, 14).total, 2);
  assert.equal(faScore(null, 1, 1), null);
  assert.equal(faScore(1, 1, undefined), null);
});

test("bands: 0 low, up to 3 mild (3 is mild), up to 7 medium, 8 or more high", () => {
  assert.equal([0, 0.5, 3, 3.1, 7, 7.5, 8, 30].map(faBandIndex).join(), "0,1,1,2,2,3,3,3"); // 7.5 is above 7, so High
});

test("blood pressure is high above 160/100, elevated above 140/90", () => {
  assert.equal(faBpLevel(120, 80), "");
  assert.equal(faBpLevel(141, 80), "elevated");
  assert.equal(faBpLevel(120, 91), "elevated");
  assert.equal(faBpLevel(161, 80), "high");
  assert.equal(faBpLevel(150, 101), "high");
  assert.equal(faBpLevel(null, null), "");
});

test("names are tidied and number boxes read as numbers or null", () => {
  assert.equal(faCleanName("  bUDI   santoso "), "Budi Santoso");
  assert.equal(faCleanName(""), "");
  assert.equal(faNumber(""), null);
  assert.equal(faNumber(" 7.5 "), 7.5);
  assert.equal(faNumber(null), null);
  assert.ok(Number.isNaN(faNumber("abc")));
});

test("a blank form lists everything missing", () => {
  const p = faProblems(faBlankValues(), {});
  assert.equal(p.length, 4 + 3 + 3 + 2 + 2);
  assert.ok(p.includes("Worker signature") && p.includes("Examiner signature"));
});

test("a complete form with both signatures has no problems", () => {
  assert.equal(faProblems(filled(), both).length, 0);
  assert.equal(JSON.stringify(faProblems(filled(), { worker: true })), JSON.stringify(["Examiner signature"]));
});

test("out-of-range hours and pressures, and Y below X, are reported", () => {
  const v = filled();
  v.z = 1;
  assert.match(faProblems(v, both)[0], /^Z must be a number from 2 to 24/);
  const w = filled();
  w.x = 8;
  w.y = 7;
  assert.match(faProblems(w, both)[0], /cannot be less/);
  const b = filled();
  b.sys = 300;
  assert.match(faProblems(b, both)[0], /systolic/);
});

test("the record has the keys and numbers the server stores", () => {
  const v = filled();
  v.x = 5; v.y = 11; v.z = 16; v.date = "2026-10-07"; v.time = "08:30";
  const r = faRecord(v, "id-1", new Date("2026-10-07T01:00:00.000Z"));
  assert.equal(r.id, "id-1");
  assert.equal(r.form_version, "web1");
  assert.equal(r.score_total, 8);
  assert.equal(r.risk_level, "High");
  assert.equal(r.risk_points, "8 or more points");
  assert.equal(r.sleep_24h, 5);
  assert.equal(r.bp_systolic, 120);
  assert.equal(r.feels_healthy, "Y");
  assert.equal(r.examiner, "Dewi");
});

test("every key the server reads from a fatigue record is sent", () => {
  const server = readFileSync("internal/screening/fatigue.go", "utf8");
  const keys = [...server.matchAll(/json:"([a-z0-9_]+)"/g)].map((m) => m[1]);
  const sent = Object.keys(faRecord(filled(), "x", new Date()));
  for (const key of keys) assert.ok(sent.includes(key), key);
});

test("the PDF is named after the worker and the moment", () => {
  assert.equal(faPdfName("Budi Santoso", new Date(2026, 9, 7, 8, 5, 9)), "fatigue-scoring-budi-santoso-20261007-080509.pdf");
  assert.equal(faPdfName("", new Date(2026, 9, 7, 8, 5, 9)), "fatigue-scoring-20261007-080509.pdf");
});
