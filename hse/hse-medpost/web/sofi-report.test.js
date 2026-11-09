const { readFileSync } = require("fs");
const { test } = require("node:test");
const assert = require("node:assert/strict");
const vm = require("vm");

// sofi-report-pdf.js runs in the browser; it only declares functions, so it loads here and the
// drawing is checked against a fake jsPDF document that records what would be printed.
const context = {};
vm.createContext(context);
vm.runInContext(readFileSync("web/static/sofi-report-pdf.js", "utf8"), context);
const {
  sofiPdfText, sofiReportSegments, sofiReportModel, sofiReportFileName, sofiReportDraw,
  sofiTextRuns, sofiWrapText, sofiReportScripts, sofiTextWidth,
} = context;

// The font names the report registers for the two on-demand fonts; passed to the drawing code.
const FONTS = { cjk: "CJK", arabic: "Arab" };
const ARABIC = "شركة النفط";
const CJK = "石油天然气有限公司";

const PAGE_H = 841.89;
const json = (v) => JSON.parse(JSON.stringify(v)); // objects built inside the vm have another Object prototype

function summaryData(groupCount) {
  const groups = [];
  for (let i = 0; i < groupCount; i++) {
    groups.push({ name: "PT Company " + i, count: 3, results: { low: 1, moderate: 1, high: 1 } });
  }
  const dim = (en) => ({ key: en, en, id: en, average: 12.5, max: 30, results: { low: 1, moderate: 1, high: 1 } });
  return {
    total: 3, people: 3, by: "company", moreGroups: 5,
    results: { low: 1, moderate: 0, high: 2 },
    weeksOnSite: { low: { average: 12.5, counted: 1, overYear: 2 }, moderate: { average: 0, counted: 0, overYear: 0 }, high: { average: 30, counted: 2, overYear: 0 } },
    dimensions: ["Lack of Energy at Work", "Exerting Physical Effort", "Physical Discomfort", "Lack of Motivation at Work", "Feeling of Sleepiness"].map(dim),
    groups,
  };
}

function input(over) {
  return Object.assign({
    data: summaryData(2), summary: "3 surveys from 3 people.", periodLabel: "Round 1 · 2026-10-05 00:00 – 2026-10-12 00:00",
    note: null, generatedBy: "Dr Sari", generatedAt: "2026-10-29 08:30",
  }, over);
}

function fakeDoc() {
  const calls = [];
  const images = [];
  let page = 1;
  let pages = 1;
  let font = "helvetica";
  return {
    calls, images,
    internal: { pageSize: { getWidth: () => 595.28, getHeight: () => PAGE_H } },
    setFont(name) { font = name; }, setFontSize() {}, setTextColor() {}, setFillColor() {}, setDrawColor() {}, setLineWidth() {},
    rect() {}, line() {},
    text(t, x, y, opts) { calls.push({ page, t: Array.isArray(t) ? t.join(" ") : String(t), x, y, font, opts: opts || {} }); },
    getTextWidth(s) { return String(s).length * 5; },
    splitTextToSize(s, w) {
      const per = Math.max(1, Math.floor(w / 5));
      const out = [];
      for (let i = 0; i < s.length; i += per) out.push(s.slice(i, i + per));
      return out.length ? out : [""];
    },
    addPage() { pages++; page = pages; },
    getNumberOfPages() { return pages; },
    setPage(n) { page = n; },
    getFontSize() { return 10; },
    getTextColor() { return "#112233"; },
    addImage(data, type, x, y, w, h) { images.push({ page, data, type, x, y, w, h }); },
  };
}

test("text no loaded font can show becomes a question mark", () => {
  assert.equal(sofiPdfText("Café – ok · “x”", {}), "Café – ok · “x”");
  assert.equal(sofiPdfText("PT 日本 Ltd", {}), "PT ?? Ltd", "CJK font not loaded");
  assert.equal(sofiPdfText("PT 日本 Ltd", FONTS), "PT 日本 Ltd");
  assert.equal(sofiPdfText("x " + ARABIC, {}), "x ???? ?????", "Arabic font not loaded: each letter becomes ?, the space stays");
  assert.equal(sofiPdfText("x " + ARABIC, FONTS), "x " + ARABIC);
  assert.equal(sofiPdfText("Thai ไทย and 😀", FONTS), "Thai ??? and ??", "no font for these scripts, an emoji is two UTF-16 units");
  assert.equal(sofiPdfText(null, FONTS), "");
});

test("text is cut into runs by script; spaces, digits and brackets follow their neighbour", () => {
  assert.deepEqual(json(sofiTextRuns("PT Alpha")), [{ script: "latin", text: "PT Alpha" }]);
  assert.deepEqual(json(sofiTextRuns("PT 石油 (中国) Ltd")), [
    { script: "latin", text: "PT " }, { script: "cjk", text: "石油 (中国) " }, { script: "latin", text: "Ltd" },
  ]);
  assert.deepEqual(json(sofiTextRuns("شركة النفط 123 PT")), [
    { script: "arabic", text: "شركة النفط 123 " }, { script: "latin", text: "PT" },
  ]);
  assert.deepEqual(json(sofiTextRuns("123 (45)")), [{ script: "latin", text: "123 (45)" }], "only neutral characters");
  assert.deepEqual(json(sofiTextRuns("(中)")), [{ script: "cjk", text: "(中)" }], "a leading bracket joins the run after it");
  assert.deepEqual(json(sofiTextRuns("")), []);
});

test("a report needs a font only when its text has that script", () => {
  const data = summaryData(1);
  data.groups[0].name = "PT 石油";
  assert.deepEqual(json(sofiReportScripts(sofiReportModel(input({ data })))), { cjk: true, arabic: false });
  const note = { text: ARABIC, writtenBy: "Dr Sari", writtenAt: "2026-10-28 17:00" };
  assert.deepEqual(json(sofiReportScripts(sofiReportModel(input({ note })))), { cjk: false, arabic: true });
  assert.deepEqual(json(sofiReportScripts(sofiReportModel(input()))), { cjk: false, arabic: false });
});

test("CJK text, which has no spaces, wraps between characters and Latin wraps at spaces", () => {
  const doc = fakeDoc(); // every character is 5 wide, so 100 wide holds 20
  const lines = sofiWrapText(doc, FONTS, "石".repeat(45), 100);
  assert.deepEqual(json(lines.map((l) => l.length)), [20, 20, 5]);
  assert.deepEqual(json(sofiWrapText(doc, FONTS, "one two three four five six seven eight nine ten", 100)), [
    "one two three four", "five six seven eight", "nine ten",
  ]);
  assert.deepEqual(json(sofiWrapText(doc, FONTS, "first\nsecond", 100)), ["first", "second"], "a new line stays a new line");
  assert.deepEqual(json(sofiWrapText(doc, FONTS, "x".repeat(50), 100)).map((l) => l.length), [20, 20, 10], "one long word is cut, never looped on");
  assert.deepEqual(json(sofiWrapText(doc, FONTS, "", 100)), [""]);
});

test("each run is drawn in its own font: Latin in Helvetica, CJK character by character, Arabic shaped", () => {
  const data = summaryData(2);
  data.groups[0].name = "PT 石油 (中国)";
  const doc = fakeDoc();
  sofiReportDraw(doc, sofiReportModel(input({ data, note: { text: "Catatan " + ARABIC, writtenBy: "Dr Sari", writtenAt: "2026-10-28 17:00" } })), FONTS);
  const cjk = doc.calls.filter((c) => c.font === "CJK");
  assert.ok(cjk.every((c) => c.t.length === 1), "CJK is drawn one character per call, so a missing glyph cannot take the rest of the run with it");
  assert.equal(cjk.map((c) => c.t).join(""), "石油 (中国)");
  const latinBeforeCjk = doc.calls.find((c) => c.t === "PT " && c.font === "helvetica");
  assert.ok(latinBeforeCjk, "the Latin start of the name stays Helvetica");
  const arabic = doc.calls.filter((c) => c.font === "Arab");
  assert.equal(arabic.length, 1);
  assert.equal(arabic[0].t, ARABIC);
  assert.equal(arabic[0].opts.lang, "ar", "Arabic must be drawn with lang ar, or it is neither shaped nor reversed");
  assert.ok(doc.calls.some((c) => c.t.startsWith("Catatan") && c.font === "helvetica"));
  const x = (t) => doc.calls.find((c) => c.t === t).x;
  assert.ok(x(ARABIC) > x("Catatan "), "runs follow each other left to right");
});

test("without the fonts the same report still draws, with ? for the characters", () => {
  const data = summaryData(1);
  data.groups[0].name = "PT 石油";
  const doc = fakeDoc();
  sofiReportDraw(doc, sofiReportModel(input({ data })), {});
  assert.ok(!doc.calls.some((c) => c.font === "CJK"));
  assert.ok(doc.calls.some((c) => c.t === "PT ??"));
});

test("bar segments stack low, moderate, high by share of the maximum", () => {
  assert.deepEqual(json(sofiReportSegments({ low: 1, moderate: 2, high: 1 }, 4, 100)), [
    { level: "low", x: 0, w: 25 }, { level: "moderate", x: 25, w: 50 }, { level: "high", x: 75, w: 25 },
  ]);
  assert.deepEqual(json(sofiReportSegments({ low: 0, moderate: 0, high: 2 }, 4, 100)), [{ level: "high", x: 0, w: 50 }]);
  assert.deepEqual(json(sofiReportSegments({ low: 3, moderate: 3, high: 3 }, 6, 100)).map((s) => s.w), [50, 50], "never wider than the bar");
  assert.deepEqual(json(sofiReportSegments({ low: 1 }, 0, 100)), []);
});

test("the model carries the period, the percentages and the doctor's note", () => {
  const m = sofiReportModel(input({ note: { text: "Review the rest schedule.", writtenBy: "Dr Budi", writtenAt: "2026-10-28 17:00" } }));
  assert.equal(m.periodLine, "Period: Round 1 · 2026-10-05 00:00 – 2026-10-12 00:00 (GMT+9)");
  assert.equal(m.generatedLine, "Generated 2026-10-29 08:30 (GMT+9) by Dr Sari");
  assert.deepEqual(json(m.levels.map((l) => [l.label, l.count, l.pct])), [["Low", 1, 33], ["Moderate", 0, 0], ["High", 2, 67]]);
  assert.equal(m.groupsTitle, "Surveys by company");
  assert.equal(m.note.byline, "Written by Dr Budi, 2026-10-28 17:00 (GMT+9)");
});

test("the model for all periods has no period dates, and a blank group is shown as not stated", () => {
  const data = summaryData(1);
  data.groups[0].name = "";
  const m = sofiReportModel(input({ data, periodLabel: "" }));
  assert.equal(m.periodLine, "All periods");
  assert.equal(m.groups[0].name, "Not stated");
  assert.equal(m.note, null, "no note, no note section");
  assert.equal(sofiReportModel(input({ note: { text: "", writtenBy: "x", writtenAt: "y" } })).note, null, "a cleared note is not printed");
});

test("a choice with no dates is not labelled as a GMT+9 period", () => {
  const m = sofiReportModel(input({ periodLabel: "Before periods were scheduled" }));
  assert.equal(m.periodLine, "Before periods were scheduled");
  assert.equal(sofiReportFileName("Before periods were scheduled"), "sofi-report-all-periods.pdf");
});

test("the file name is built from the period's dates", () => {
  assert.equal(sofiReportFileName("2026-10-19 00:00 – 2026-10-26 00:00"), "sofi-report-2026-10-19-to-2026-10-26.pdf");
  assert.equal(sofiReportFileName("Round 1 · 2026-10-05 00:00 – 2026-10-12 00:00"), "sofi-report-2026-10-05-to-2026-10-12.pdf");
  assert.equal(sofiReportFileName(""), "sofi-report-all-periods.pdf");
});

test("a long report flows onto more pages and every page gets its footer", () => {
  const doc = fakeDoc();
  sofiReportDraw(doc, sofiReportModel(input({ data: summaryData(20), note: { text: "x".repeat(900), writtenBy: "Dr Budi", writtenAt: "2026-10-28 17:00" } })));
  const pages = doc.getNumberOfPages();
  assert.ok(pages > 1, "20 groups plus a long note must not fit on one page, got " + pages);
  for (let p = 1; p <= pages; p++) {
    assert.ok(doc.calls.some((c) => c.page === p && c.t === `Page ${p} of ${pages}`), "page " + p + " has no page number");
  }
  // Nothing but the footer is printed in the footer zone: the page numbers and the provisional-bands note.
  const zone = doc.calls.filter((c) => c.y > PAGE_H - 50 && !/^Page /.test(c.t));
  const footerText = zone.map((c) => c.t).join(" ").replace(/\s+/g, " ");
  assert.ok(footerText.startsWith("Result bands are provisional") && footerText.endsWith("not a diagnosis."), footerText);
  assert.ok(doc.calls.some((c) => c.t === "Doctor's note"));
  assert.ok(doc.calls.some((c) => c.t.startsWith("Written by Dr Budi")));
});

test("a short report without a note is one page and has no note heading", () => {
  const doc = fakeDoc();
  sofiReportDraw(doc, sofiReportModel(input()));
  assert.equal(doc.getNumberOfPages(), 1);
  assert.ok(!doc.calls.some((c) => c.t === "Doctor's note"));
  assert.ok(doc.calls.some((c) => c.t.includes("Round 1")), "the period is printed");
});

// The browser draws text no PDF font can show (Thai, emoji, ...) onto a canvas and hands it over as
// a picture. The stub stands in for that: 4 wide per character, a picture named after its text.
const RASTER = {
  measure: (text) => Array.from(text).length * 4,
  image: (text, size, style, rgb) => ({ data: "IMG:" + text + "|" + style + "|" + rgb.join(","), w: Array.from(text).length * 4, h: size * 1.4, ascent: size }),
};

test("with a rasteriser, Thai and emoji are kept instead of becoming ?", () => {
  assert.equal(sofiPdfText("Thai ไทย and 😀", { raster: RASTER }), "Thai ไทย and 😀");
  assert.equal(sofiPdfText("Thai ไทย and 😀", FONTS), "Thai ??? and ??", "no rasteriser: still ?");
  assert.deepEqual(json(sofiTextRuns("Hi ไทย 😀 ok")), [
    { script: "latin", text: "Hi " }, { script: "other", text: "ไทย 😀 " }, { script: "latin", text: "ok" },
  ]);
});

test("a run of other text is drawn as one picture in the current colour, between Latin text", () => {
  const data = summaryData(1);
  data.groups[0].name = "PT สวัสดี Ltd";
  const doc = fakeDoc();
  sofiReportDraw(doc, sofiReportModel(input({ data })), { raster: RASTER });
  const img = doc.images.filter((i) => i.data.startsWith("IMG:สวัสดี"));
  assert.equal(img.length, 1);
  assert.match(img[0].data, /\|normal\|17,34,51$/, "style and the colour of the text around it");
  assert.equal(img[0].type, "PNG");
  const before = doc.calls.find((c) => c.t === "PT ");
  const after = doc.calls.find((c) => c.t === "Ltd");
  assert.ok(before.x < img[0].x && img[0].x < after.x, "left to right");
  assert.ok(Math.abs(after.x - (img[0].x + img[0].w)) < 0.01, "the next run starts where the picture ends");
  assert.ok(Math.abs(before.y - (img[0].y + 10)) < 0.01, "the picture sits on the same baseline");
});

test("without a rasteriser the same name prints with ?", () => {
  const data = summaryData(1);
  data.groups[0].name = "PT สวัสดี Ltd";
  const doc = fakeDoc();
  sofiReportDraw(doc, sofiReportModel(input({ data })), FONTS);
  assert.equal(doc.images.length, 0);
  assert.ok(doc.calls.some((c) => c.t === "PT ????? Ltd" || c.t === "PT ?????? Ltd"));
});

test("a long unbroken run is wrapped but a mark stays with its letter", () => {
  const doc = fakeDoc();
  const thai = "กิ".repeat(30); // letter + combining vowel, 4 wide each in the stub; 100 wide holds 25 letters... 
  const lines = sofiWrapText(doc, { raster: RASTER }, thai, 100);
  assert.ok(lines.length > 1);
  assert.ok(lines.every((l) => !/^[\u0e31\u0e34-\u0e3a\u0e47-\u0e4e]/.test(l)), "no line starts with a combining mark");
});

test("a name typed with a combining accent is composed, so it prints as one letter", () => {
  assert.equal(sofiPdfText("José Peña", {}), "José Peña", "e + combining acute becomes é, not ?");
});

test("Arabic is measured as shaped, the way jsPDF draws it", () => {
  const doc = fakeDoc();
  doc.processArabic = (s) => "#".repeat(Math.ceil(s.length / 2)); // shaping joins letters: fewer glyphs
  assert.equal(sofiTextWidth(doc, FONTS, ARABIC), Math.ceil(ARABIC.length / 2) * 5);
  const plain = fakeDoc(); // an older jsPDF without processArabic: measure the text as it is
  assert.equal(sofiTextWidth(plain, FONTS, ARABIC), ARABIC.length * 5);
});

test("the report carries the average weeks on site of each result", () => {
  const m = sofiReportModel(input());
  assert.deepEqual(json(m.weeksOnSite.map((w) => [w.label, w.average, w.note])), [
    ["Low", "12.5", "+2 over 1 year"],
    ["Moderate", "—", ""],
    ["High", "30.0", ""],
  ]);
  const old = summaryData(1);
  delete old.weeksOnSite; // a summary from before this field existed still draws
  assert.deepEqual(json(sofiReportModel(input({ data: old })).weeksOnSite.map((w) => w.average)), ["—", "—", "—"]);
});
