const { readFileSync } = require("fs");
const { test } = require("node:test");
const assert = require("node:assert/strict");
const vm = require("vm");

// Both scripts run in the browser; with no document only their pure helpers load.
// screening-log.js uses barPercent from malaria.js, as it does on the page.
const context = {};
vm.createContext(context);
vm.runInContext(readFileSync("web/static/malaria.js", "utf8"), context);
vm.runInContext(readFileSync("web/static/screening-log.js", "utf8"), context);
const {
  screeningResultKey,
  screeningResultLabel,
  clinicDateTime,
  screeningDisplayValue,
  screeningCellValue,
  screeningDetailFields,
  screeningGroupName,
  screeningSegments,
  screeningAlertLabel,
} = context;

test("a stored result is classified the way the server counts it", () => {
  assert.equal(screeningResultKey("alcohol", "neg"), "negative");
  assert.equal(screeningResultKey("alcohol", " POS "), "positive");
  assert.equal(screeningResultKey("alcohol", "Negative"), "negative");
  assert.equal(screeningResultKey("alcohol", "lab"), "lab");
  assert.equal(screeningResultKey("alcohol", ""), "other");
  assert.equal(screeningResultKey("alcohol", "unknown"), "other");
  assert.equal(screeningResultKey("fatigue", "Medium"), "medium");
  assert.equal(screeningResultKey("fatigue", "high"), "high");
  assert.equal(screeningResultKey("fatigue", "neg"), "other"); // a code of the other test
  assert.equal(screeningResultKey("fatigue", null), "other");
});

test("results are spelled out, and an unknown value is shown as typed", () => {
  assert.equal(screeningResultLabel("alcohol", "neg"), "Negative");
  assert.equal(screeningResultLabel("alcohol", "pos"), "Positive");
  assert.equal(screeningResultLabel("alcohol", "lab"), "Sent to lab");
  assert.equal(screeningResultLabel("alcohol", " maybe "), "maybe");
  assert.equal(screeningResultLabel("fatigue", "mild"), "Mild");
});

test("a UTC timestamp is shown in the clinic's time (Asia/Jayapura, UTC+9)", () => {
  assert.equal(clinicDateTime("2026-10-01T16:00:00"), "2026-10-02 01:00");
  assert.equal(clinicDateTime("2026-10-01T14:59:59"), "2026-10-01 23:59");
  assert.equal(clinicDateTime("2026-12-31T15:00:00"), "2027-01-01 00:00");
  assert.equal(clinicDateTime("2026-10-05"), "2026-10-05"); // not a timestamp: unchanged
  assert.equal(clinicDateTime(""), "");
});

test("table and detail values are shown the way a person reads them", () => {
  assert.equal(screeningDisplayValue("alcohol", "Test date", "2026-10-01T16:00:00"), "2026-10-02 01:00");
  assert.equal(screeningDisplayValue("alcohol", "Final result", "pos"), "Positive");
  assert.equal(screeningDisplayValue("alcohol", "Test type", "suspicion"), "Reasonable suspicion");
  assert.equal(screeningDisplayValue("alcohol", "Test type", "other thing"), "other thing");
  assert.equal(screeningDisplayValue("fatigue", "Risk level", "low"), "Low");
  assert.equal(screeningDisplayValue("fatigue", "Exam date", "2026-10-05"), "2026-10-05");
  assert.equal(screeningDisplayValue("fatigue", "Submitted", "2026-10-06T09:00:00"), "2026-10-06 09:00:00 UTC");
  assert.equal(screeningDisplayValue("alcohol", "Name", null), "");
});

test("the opened row shows every column the table leaves out", () => {
  const headers = ["Submitted", "Test date", "Name", "Company", "Position", "Test type", "Final result", "Remarks"];
  const row = ["2026-10-06T09:00:00", "2026-10-01T16:00:00", "Budi", "PT A", "Driver", "pre", "neg", ""];
  const fields = screeningDetailFields("alcohol", headers, row);
  assert.equal(fields.length, 2);
  assert.equal(fields[0][0], "Submitted");
  assert.equal(fields[0][1], "2026-10-06 09:00:00 UTC");
  assert.equal(fields[1][0], "Remarks");
  assert.equal(fields[1][1], "");
});

test("a chart group's name is readable, blank as not stated", () => {
  assert.equal(screeningGroupName("company", "PT A"), "PT A");
  assert.equal(screeningGroupName("company", ""), "(not stated)");
  assert.equal(screeningGroupName("testType", "pre"), "Pre-employment");
  assert.equal(screeningGroupName("testType", "weird"), "weird");
  assert.equal(screeningGroupName("examiner", null), "(not stated)");
});

test("a stacked bar lays the outcomes that have cases end to end", () => {
  const keys = ["negative", "positive", "lab", "other"];
  const got = screeningSegments({ negative: 5, positive: 3, lab: 0, other: 2 }, keys, 10);
  assert.equal(JSON.stringify(got.map((s) => [s.key, s.x, s.width])), JSON.stringify([["negative", 0, 50], ["positive", 50, 30], ["other", 80, 20]]));
  assert.equal(screeningSegments({}, keys, 10).length, 0);
  assert.equal(screeningSegments(undefined, keys, 10).length, 0);
  const squeezed = screeningSegments({ negative: 1, positive: 1000 }, keys, 1000);
  const last = squeezed[squeezed.length - 1];
  assert.ok(last.x + last.width <= 100);
});

test("the figures worth spelling out next to a bar", () => {
  assert.equal(screeningAlertLabel("alcohol", { negative: 9, positive: 2, lab: 1 }), "Positive 2, Sent to lab 1");
  assert.equal(screeningAlertLabel("alcohol", { negative: 9 }), "");
  assert.equal(screeningAlertLabel("fatigue", { low: 4, mild: 1, medium: 0, high: 2 }), "Mild 1, High 2");
  assert.equal(screeningAlertLabel("fatigue", undefined), "");
});

test("a blank fatigue exam date shows the day the record was created", () => {
  const headers = ["Created at", "Exam date", "Name"];
  assert.equal(screeningCellValue("fatigue", headers, ["2026-10-05T02:00:00", "", "Dewi"], "Exam date"), "2026-10-05");
  assert.equal(screeningCellValue("fatigue", headers, ["2026-10-09T08:00:00", "2026-10-05", "Budi"], "Exam date"), "2026-10-05");
  assert.equal(screeningCellValue("fatigue", headers, ["", "", "X"], "Exam date"), "");
  assert.equal(screeningCellValue("fatigue", headers, ["2026-10-05T02:00:00", "2026-10-05", "Budi"], "Name"), "Budi");
  // The alcohol test date is always a timestamp, shown in the clinic's time.
  assert.equal(screeningCellValue("alcohol", ["Test date"], ["2026-10-01T16:00:00"], "Test date"), "2026-10-02 01:00");
});

// Every tab of the screening panel must be registered in screening-log.js, with its view, or the
// tab silently does nothing and (worse) code that hides a tab by position hides the wrong one.
test("each tab in the screening panel is registered with its view", () => {
  const html = readFileSync("web/templates/emr.html", "utf8");
  const script = readFileSync("web/static/screening-log.js", "utf8");
  const panel = html.slice(html.indexOf('id="mr-panel-screening"'));
  const tabs = [...panel.matchAll(/<button[^>]*\bid="(scr-tab-[a-z]+)"[^>]*\bdata-view="([a-z]+)"/g)];
  assert.deepEqual(tabs.map((t) => t[2]).sort(), ["dashboard", "sofi", "table"]);
  const registered = script.match(/tabs: \[([^\]]*)\]/)[1];
  const views = script.match(/views: \{([^}]*)\}/)[1];
  for (const [, tabId, view] of tabs) {
    assert.ok(registered.includes(`"${tabId}"`), `${tabId} is not in the tabs list`);
    assert.ok(views.includes(`${view}: $("scr-view-${view}")`), `view ${view} is not registered`);
    assert.ok(panel.includes(`id="scr-view-${view}"`), `no panel for ${view}`);
  }
});
