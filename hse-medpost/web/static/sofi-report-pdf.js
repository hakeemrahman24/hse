// The SOFI Fatigue Survey report as a PDF (Medical Record > Screening > Fatigue > Dashboard).
// One report is one test period (a /sofi window) or "All periods". It prints what the dashboard
// shows: the summary, the Low / Moderate / High counts, the result by dimension and by company or
// position, and the doctor's note saved for the period. All numbers come from the dashboard's own
// data (GET /api/sofi-log/summary), so the PDF cannot disagree with the screen.
//
// Scripts. jsPDF's built-in Helvetica only has Latin. A company name or the doctor's note can also
// be Chinese, Japanese, Korean or Arabic, so two fonts can be added on demand (sofi-report.js
// fetches them only when the text needs them): Noto Sans CJK and Noto Sans Arabic, from
// static/fonts/. Text is cut into runs by script and each run is drawn in its own font:
//   - Latin and Latin-1: Helvetica.
//   - CJK: one character per call. A character the font lacks then shows as an empty box and
//     cannot make jsPDF drop the rest of the run, which it does for a missing glyph.
//   - Arabic: the whole run with {lang: "ar"}, which makes jsPDF shape the letters and reverse
//     them right to left. The page layout stays left to right.
//   - Anything else (Thai, emoji, ...): when the browser supplies a rasteriser (fonts.raster, made in
//     sofi-report.js) the run is drawn on a canvas, where the browser's own text engine shapes it, and
//     placed as a picture. It is not selectable text. Without one, it becomes "?".
// A script whose font could not be loaded becomes "?" too.
//
// Only functions are declared here (no DOM), so web/sofi-report.test.js can load the file and
// check the pure parts and the page layout against a fake jsPDF document. sofi-report.js (the
// buttons) loads jsPDF and the fonts on first use and calls sofiBuildReportPdf().

const SOFI_RPT_LEVELS = ["low", "moderate", "high"];
const SOFI_RPT_LABEL = { low: "Low", moderate: "Moderate", high: "High" };
// The same colours as the dashboard's bars (.scr-bar-low / -moderate / -high in app.css).
const SOFI_RPT_RGB = { low: [46, 125, 50], moderate: [217, 119, 6], high: [198, 40, 40] };
const SOFI_RPT_ACCENT = [26, 122, 107];
const SOFI_RPT_MUTED = [90, 90, 90];
const SOFI_RPT_GRID = [185, 196, 208];
const SOFI_RPT_PROVISIONAL =
  "Result bands are provisional until the doctor approves them: dimension Low 0-10, Moderate 11-20, High 21-30; " +
  "total Low 0-50, Moderate 51-100, High 101-150. This is a screening result, not a diagnosis.";

const SOFI_RPT_MARGIN = 40;
const SOFI_RPT_FOOTER = 60; // space kept free at the bottom of every page for the footer
const SOFI_RPT_NAME_W = 150; // width of the name column beside a bar
const SOFI_RPT_VALUE_W = 90; // width of the value text after a bar

const SOFI_RPT_ARABIC_RE = /[؀-ۿݐ-ݿࢠ-ࣿﭐ-﷿ﹰ-﻿]/;
// Han, kana, bopomofo, hangul and the full-width forms. The font holds the characters of the
// GB 2312, JIS X 0208 and KS X 1001 sets; a rarer one is drawn as an empty box.
const SOFI_RPT_CJK_RE = /[⺀-鿿가-힯豈-﫿＀-￯]/;
// Punctuation that Helvetica (WinAnsi) has and that takes the script of its neighbours.
const SOFI_RPT_NEUTRAL_PUNCT = "–—‘’“”•…";

const SOFI_RPT_JOINER_RE = /^[\p{M}\u200d\ufe0f]/u;

// "latin", "cjk", "arabic", "neutral" (space, digits, punctuation: follows its neighbours) or
// "other" (no font for it). ch is one UTF-16 unit, so an emoji is two "other" units.
function sofiCharScript(ch) {
  const c = ch.charCodeAt(0);
  if (c <= 0x7e) { return /[A-Za-z]/.test(ch) ? "latin" : "neutral"; }
  if (c >= 0xa0 && c <= 0xff) { return "latin"; }
  if (SOFI_RPT_ARABIC_RE.test(ch)) { return "arabic"; }
  if (SOFI_RPT_CJK_RE.test(ch)) { return "cjk"; }
  return SOFI_RPT_NEUTRAL_PUNCT.indexOf(ch) >= 0 ? "neutral" : "other";
}

// fonts: { cjk, arabic } are the names of the fonts that were registered (absent = not loaded).
// Any character with no font to show it becomes "?".
function sofiPdfText(s, fonts) {
  const have = fonts || {};
  let out = "";
  // NFC first: "e" + a combining accent is one letter (é) in Helvetica, but two characters otherwise
  for (const ch of String(s == null ? "" : s).normalize("NFC")) {
    // for...of walks code points; an astral one (emoji) is two UTF-16 units, each replaced by "?"
    if (have.raster && sofiCharScript(ch[0]) === "other") { out += ch; continue; }
    for (let i = 0; i < ch.length; i++) {
      const unit = ch[i];
      const script = sofiCharScript(unit);
      const missing = (script === "other" && !have.raster) || (script === "cjk" && !have.cjk) || (script === "arabic" && !have.arabic);
      out += missing ? "?" : unit;
    }
  }
  return out;
}

// Cuts text into runs of one script: [{script, text}]. A neutral character joins the run before it
// (or the one after it when it starts the text), so "PT 石油 (中国) Ltd" is three runs.
function sofiTextRuns(text) {
  const chars = Array.from(String(text));
  const scripts = chars.map(sofiCharScript);
  let previous = null;
  for (let i = 0; i < scripts.length; i++) {
    if (scripts[i] === "neutral") {
      if (previous) { scripts[i] = previous; }
    } else {
      previous = scripts[i];
    }
  }
  let next = "latin";
  for (let i = scripts.length - 1; i >= 0; i--) {
    if (scripts[i] === "neutral") { scripts[i] = next; } else { next = scripts[i]; }
  }
  const runs = [];
  chars.forEach(function (ch, i) {
    const last = runs[runs.length - 1];
    if (last && last.script === scripts[i]) { last.text += ch; } else { runs.push({ script: scripts[i], text: ch }); }
  });
  return runs;
}

// Which on-demand fonts this report's text needs, so sofi-report.js fetches only those.
function sofiReportScripts(m) {
  const parts = [m.title, m.subtitle, m.periodLine, m.generatedLine, m.summary, m.groupsTitle];
  m.dimensions.forEach(function (d) { parts.push(d.name); });
  m.groups.forEach(function (g) { parts.push(g.name); });
  if (m.note) { parts.push(m.note.text, m.note.byline); }
  const need = { cjk: false, arabic: false };
  for (const ch of parts.join("")) {
    const script = sofiCharScript(ch[0]);
    if (script === "cjk") { need.cjk = true; }
    if (script === "arabic") { need.arabic = true; }
  }
  return need;
}

// The text colour jsPDF holds ("#rrggbb") as [r, g, b], for the pictures of rasterised text.
function sofiTextRgb(doc) {
  const hex = String(doc.getTextColor()).replace("#", "");
  return [0, 2, 4].map(function (i) { return parseInt(hex.slice(i, i + 2), 16) || 0; });
}

function sofiSetRunFont(doc, fonts, script, style) {
  if (script === "latin") {
    doc.setFont("helvetica", style || "normal");
  } else {
    doc.setFont(fonts[script], "normal"); // the added fonts have no bold
  }
}

// Width of an Arabic run as jsPDF draws it: doc.text with lang "ar" first joins the letters into their
// shaped forms (processArabic), and those glyphs are not as wide as the plain letters.
function sofiArabicWidth(doc, text) {
  return doc.getTextWidth(typeof doc.processArabic === "function" ? doc.processArabic(text) : text);
}

// Width of text in the current font size, run by run.
function sofiTextWidth(doc, fonts, text, style) {
  let w = 0;
  sofiTextRuns(text).forEach(function (run) {
    sofiSetRunFont(doc, fonts, run.script, style);
    if (run.script === "other") {
      w += fonts.raster.measure(run.text, doc.getFontSize(), style);
    } else if (run.script === "cjk") {
      for (const ch of run.text) { w += doc.getTextWidth(ch); }
    } else {
      w += run.script === "arabic" ? sofiArabicWidth(doc, run.text) : doc.getTextWidth(run.text);
    }
  });
  return w;
}

// Draws text at (x, y) run by run. align "right" puts the end of the text at x.
function sofiDrawText(doc, fonts, text, x, y, style, align) {
  const clean = sofiPdfText(text, fonts);
  let cx = align === "right" ? x - sofiTextWidth(doc, fonts, clean, style) : x;
  sofiTextRuns(clean).forEach(function (run) {
    sofiSetRunFont(doc, fonts, run.script, style);
    if (run.script === "other") {
      // y is the baseline; the picture reports how far its top is above it
      const img = fonts.raster.image(run.text, doc.getFontSize(), style, sofiTextRgb(doc));
      doc.addImage(img.data, "PNG", cx, y - img.ascent, img.w, img.h);
      cx += img.w;
    } else if (run.script === "cjk") {
      for (const ch of run.text) {
        doc.text(ch, cx, y);
        cx += doc.getTextWidth(ch);
      }
    } else if (run.script === "arabic") {
      doc.text(run.text, cx, y, { lang: "ar" });
      cx += sofiArabicWidth(doc, run.text);
    } else {
      doc.text(run.text, cx, y);
      cx += doc.getTextWidth(run.text);
    }
  });
}

// A line is broken at spaces, and between any two CJK characters (they have no spaces).
function sofiWrapTokens(paragraph) {
  const tokens = [];
  let word = "";
  for (const ch of paragraph) {
    if (sofiCharScript(ch[0]) === "cjk") {
      if (word) { tokens.push(word); word = ""; }
      tokens.push(ch);
    } else {
      word += ch;
      if (ch === " ") { tokens.push(word); word = ""; }
    }
  }
  if (word) { tokens.push(word); }
  return tokens;
}

// A single word wider than the line is cut where it no longer fits.
function sofiBreakWord(doc, fonts, token, width, style) {
  const parts = [];
  let current = "";
  for (const ch of token) {
    // a combining mark, joiner or variation selector stays with the letter before it
    if (current && !SOFI_RPT_JOINER_RE.test(ch) && sofiTextWidth(doc, fonts, current + ch, style) > width) {
      parts.push(current);
      current = ch;
    } else {
      current += ch;
    }
  }
  if (current) { parts.push(current); }
  return parts;
}

// Lines for text that must fit width. Replaces jsPDF's splitTextToSize, which is measured in one
// font and cannot break CJK text. A new line in the text stays a new line.
function sofiWrapText(doc, fonts, text, width, style) {
  const lines = [];
  sofiPdfText(text, fonts).split(/\r?\n/).forEach(function (paragraph) {
    if (paragraph === "") { lines.push(""); return; }
    let line = "";
    function add(token) {
      const joined = line + token;
      if (line === "" || sofiTextWidth(doc, fonts, joined.replace(/\s+$/, ""), style) <= width) {
        line = joined;
        return;
      }
      lines.push(line.replace(/\s+$/, ""));
      line = "";
      if (sofiTextWidth(doc, fonts, token.replace(/\s+$/, ""), style) > width) {
        const parts = sofiBreakWord(doc, fonts, token, width, style);
        parts.slice(0, -1).forEach(function (p) { lines.push(p); });
        line = parts[parts.length - 1];
      } else {
        line = token;
      }
    }
    sofiWrapTokens(paragraph).forEach(function (token) {
      if (line === "" && sofiTextWidth(doc, fonts, token.replace(/\s+$/, ""), style) > width) {
        const parts = sofiBreakWord(doc, fonts, token, width, style);
        parts.slice(0, -1).forEach(function (p) { lines.push(p); });
        line = parts[parts.length - 1];
        return;
      }
      add(token);
    });
    lines.push(line.replace(/\s+$/, ""));
  });
  return lines;
}

// One stacked bar: a segment per band, as wide as its share of max, never wider than the bar.
function sofiReportSegments(results, max, width) {
  const out = [];
  let x = 0;
  SOFI_RPT_LEVELS.forEach(function (level) {
    const count = (results && results[level]) || 0;
    const w = max > 0 ? (count / max) * width : 0;
    const clamped = Math.min(w, width - x);
    if (clamped <= 0) { return; }
    out.push({ level: level, x: x, w: clamped });
    x += w;
  });
  return out;
}

function sofiReportPct(count, total) {
  return total > 0 ? Math.round((count * 100) / total) : 0;
}

// input: { data (the dashboard's summary JSON), summary (the written summary), periodLabel ("" for
// all periods), note ({text, writtenBy, writtenAt} or null), generatedBy, generatedAt (GMT+9 text) }.
function sofiReportModel(input) {
  const d = input.data;
  const by = d.by === "position" ? "position" : "company";
  const noteText = input.note && input.note.text ? input.note.text.trim() : "";
  return {
    title: "SOFI Fatigue Survey report",
    subtitle: "Swedish Occupational Fatigue Inventory",
    // A real period is labelled by its dates; "Before periods were scheduled" has none.
    periodLine: !input.periodLabel ? "All periods" : /\d{4}-\d{2}-\d{2}/.test(input.periodLabel) ? "Period: " + input.periodLabel + " (GMT+9)" : input.periodLabel,
    generatedLine: "Generated " + input.generatedAt + " (GMT+9) by " + input.generatedBy,
    summary: input.summary || "",
    surveys: d.total,
    people: d.people,
    levels: SOFI_RPT_LEVELS.map(function (key) {
      const count = (d.results && d.results[key]) || 0;
      return { key: key, label: SOFI_RPT_LABEL[key], count: count, pct: sofiReportPct(count, d.total) };
    }),
    // Average weeks on site per overall result; "—" when nobody gave a number of weeks, and a note
    // when some chose "more than 1 year" (they are not in the average).
    weeksOnSite: SOFI_RPT_LEVELS.map(function (key) {
      const s = (d.weeksOnSite && d.weeksOnSite[key]) || { average: 0, counted: 0, overYear: 0 };
      return {
        key: key,
        label: SOFI_RPT_LABEL[key],
        average: s.counted > 0 ? Number(s.average).toFixed(1) : "—",
        note: s.overYear > 0 ? "+" + s.overYear + " over 1 year" : "",
      };
    }),
    dimensions: d.dimensions.map(function (x) {
      return { name: x.en, average: x.average, max: x.max, results: x.results };
    }),
    groupsTitle: "Surveys by " + by,
    groups: d.groups.map(function (g) {
      return { name: g.name || "Not stated", count: g.count, high: (g.results && g.results.high) || 0, results: g.results };
    }),
    moreGroups: d.moreGroups || 0,
    note: noteText
      ? {
          text: noteText,
          byline: "Written by " + (input.note.writtenBy || "—") + (input.note.writtenAt ? ", " + input.note.writtenAt + " (GMT+9)" : ""),
        }
      : null,
  };
}

// "sofi-report-2026-10-19-to-2026-10-26.pdf" from the period label (its first two dates).
function sofiReportFileName(periodLabel) {
  const dates = String(periodLabel || "").match(/\d{4}-\d{2}-\d{2}/g);
  if (!dates || dates.length < 2) { return "sofi-report-all-periods.pdf"; }
  return "sofi-report-" + dates[0] + "-to-" + dates[1] + ".pdf";
}

// Shortens a name so it fits the column, ending in "..." when it had to be cut.
function sofiReportFit(doc, fonts, text, width) {
  const chars = Array.from(sofiPdfText(text, fonts));
  if (sofiTextWidth(doc, fonts, chars.join(""), "normal") <= width) { return chars.join(""); }
  while (chars.length > 1 && sofiTextWidth(doc, fonts, chars.join("") + "...", "normal") > width) { chars.pop(); }
  return chars.join("") + "...";
}

// Draws the report on doc (a jsPDF document, or the test's fake) and returns it. fonts: the names
// of the registered CJK and Arabic fonts ({cjk, arabic}); a script without a font prints as "?".
function sofiReportDraw(doc, m, fonts) {
  const have = fonts || {};
  const PW = doc.internal.pageSize.getWidth();
  const PH = doc.internal.pageSize.getHeight();
  const M = SOFI_RPT_MARGIN;
  const W = PW - 2 * M;
  let y = M;
  let style = "normal";

  function setText(size, textStyle, color) {
    style = textStyle || "normal";
    doc.setFont("helvetica", style);
    doc.setFontSize(size);
    doc.setTextColor.apply(doc, color || [0, 0, 0]);
  }
  function put(str, x, yy, align) {
    sofiDrawText(doc, have, str, x, yy, style, align);
  }
  // Starts a new page when the next h points would run into the footer.
  function ensure(h) {
    if (y + h > PH - SOFI_RPT_FOOTER) {
      doc.addPage();
      y = M;
    }
  }
  function paragraph(str, size, textStyle, color) {
    setText(size, textStyle, color);
    const lineHeight = size * 1.4;
    sofiWrapText(doc, have, str, W, style).forEach(function (line) {
      ensure(lineHeight);
      put(line, M, y);
      y += lineHeight;
    });
  }
  function heading(label) {
    ensure(48); // the heading never sits alone at the bottom of a page
    y += 14;
    setText(12, "bold", SOFI_RPT_ACCENT);
    put(label, M, y);
    y += 5;
    doc.setDrawColor.apply(doc, SOFI_RPT_GRID);
    doc.setLineWidth(0.6);
    doc.line(M, y, M + W, y);
    y += 14;
  }
  function bars(rows, max, valueText) {
    const barX = M + SOFI_RPT_NAME_W;
    const barW = W - SOFI_RPT_NAME_W - SOFI_RPT_VALUE_W;
    rows.forEach(function (r) {
      ensure(18);
      setText(9, "normal");
      put(sofiReportFit(doc, have, r.name, SOFI_RPT_NAME_W - 8), M, y + 9);
      sofiReportSegments(r.results, max, barW).forEach(function (s) {
        doc.setFillColor.apply(doc, SOFI_RPT_RGB[s.level]);
        doc.rect(barX + s.x, y, s.w, 10, "F");
      });
      setText(9, "bold");
      put(valueText(r), M + W, y + 9, "right");
      y += 18;
    });
  }
  function legend() {
    ensure(24);
    y += 4;
    let x = M;
    SOFI_RPT_LEVELS.forEach(function (level) {
      doc.setFillColor.apply(doc, SOFI_RPT_RGB[level]);
      doc.rect(x, y, 8, 8, "F");
      setText(8, "normal", SOFI_RPT_MUTED);
      put(SOFI_RPT_LABEL[level], x + 12, y + 7);
      x += 70;
    });
    y += 16;
  }

  setText(10, "bold", SOFI_RPT_ACCENT);
  put("hse medpost", M, y);
  y += 24;
  setText(20, "bold");
  put(m.title, M, y);
  y += 16;
  setText(9, "normal", SOFI_RPT_MUTED);
  put(m.subtitle, M, y);
  y += 20;
  paragraph(m.periodLine, 11, "bold");
  paragraph(m.generatedLine, 8, "normal", SOFI_RPT_MUTED);

  if (m.summary) {
    heading("Summary");
    paragraph(m.summary, 10);
  }

  heading("Results");
  paragraph(m.surveys + (m.surveys === 1 ? " survey" : " surveys") + " from " + m.people + (m.people === 1 ? " person" : " people") + " (overall result of each survey).", 9, "normal", SOFI_RPT_MUTED);
  y += 4;
  ensure(56);
  const gap = 10;
  const tileW = (W - 2 * gap) / 3;
  m.levels.forEach(function (level, i) {
    const x = M + i * (tileW + gap);
    doc.setDrawColor.apply(doc, SOFI_RPT_GRID);
    doc.setLineWidth(0.5);
    doc.rect(x, y, tileW, 48, "S");
    setText(8, "bold", SOFI_RPT_MUTED);
    put(level.label.toUpperCase(), x + 10, y + 14);
    setText(22, "bold", SOFI_RPT_RGB[level.key]);
    put(String(level.count), x + 10, y + 38);
    setText(10, "normal", SOFI_RPT_MUTED);
    put(level.pct + "%", x + tileW - 10, y + 38, "right");
  });
  y += 56;

  heading("Average weeks on site");
  paragraph("By the overall result of each survey. People who chose \"more than 1 year\" are not in the average.", 8, "normal", SOFI_RPT_MUTED);
  y += 4;
  ensure(56);
  m.weeksOnSite.forEach(function (w, i) {
    const x = M + i * (tileW + gap);
    doc.setDrawColor.apply(doc, SOFI_RPT_GRID);
    doc.setLineWidth(0.5);
    doc.rect(x, y, tileW, 48, "S");
    setText(8, "bold", SOFI_RPT_MUTED);
    put(w.label.toUpperCase(), x + 10, y + 14);
    setText(22, "bold", SOFI_RPT_RGB[w.key]);
    put(w.average, x + 10, y + 38);
    setText(8, "normal", SOFI_RPT_MUTED);
    put(w.note, x + tileW - 10, y + 38, "right");
  });
  y += 56;

  heading("Result by dimension");
  paragraph("Surveys in each band, and the average score out of 30.", 8, "normal", SOFI_RPT_MUTED);
  y += 2;
  bars(m.dimensions, m.surveys, function (r) { return "avg " + Number(r.average).toFixed(1) + " / " + r.max; });
  legend();

  heading(m.groupsTitle);
  if (m.moreGroups > 0) {
    paragraph("Showing the " + m.groups.length + " busiest; " + m.moreGroups + " more not shown.", 8, "normal", SOFI_RPT_MUTED);
    y += 2;
  }
  const busiest = m.groups.reduce(function (max, g) { return Math.max(max, g.count); }, 0);
  bars(m.groups, busiest, function (g) { return g.high > 0 ? g.count + " (High " + g.high + ")" : String(g.count); });
  legend();

  if (m.note) {
    heading("Doctor's note");
    paragraph(m.note.text, 10);
    y += 2;
    paragraph(m.note.byline, 8, "normal", SOFI_RPT_MUTED);
  }

  const pages = doc.getNumberOfPages();
  for (let p = 1; p <= pages; p++) {
    doc.setPage(p);
    setText(7, "normal", SOFI_RPT_MUTED);
    sofiWrapText(doc, have, SOFI_RPT_PROVISIONAL, W, style).forEach(function (line, i) {
      put(line, M, PH - 44 + i * 9);
    });
    put("Page " + p + " of " + pages, PW - M, PH - 18, "right");
  }
  return doc;
}

// fontData: { cjk, arabic }: the font files as base64, or null for one that is not needed or could
// not be loaded. Each is registered under a name and passed on to the drawing.
// raster: the rasteriser for text no font covers (see sofi-report.js), or omitted.
function sofiBuildReportPdf(model, fontData, raster) {
  const doc = new window.jspdf.jsPDF({ unit: "pt", format: "a4", orientation: "portrait", compress: true });
  const data = fontData || {};
  const fonts = raster ? { raster: raster } : {};
  if (data.cjk) {
    doc.addFileToVFS("NotoSansCJK-Report.ttf", data.cjk);
    doc.addFont("NotoSansCJK-Report.ttf", "NotoCJK", "normal");
    fonts.cjk = "NotoCJK";
  }
  if (data.arabic) {
    doc.addFileToVFS("NotoSansArabic-Regular.ttf", data.arabic);
    doc.addFont("NotoSansArabic-Regular.ttf", "NotoArabic", "normal");
    fonts.arabic = "NotoArabic";
  }
  return sofiReportDraw(doc, model, fonts);
}
