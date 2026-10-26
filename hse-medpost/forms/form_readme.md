# HSE Forms — Readme and Template

Written for: developers adding a new form to `forms/` and reviewers checking one.

These are standalone, single-file HTML forms. Each file holds its own CSS, markup, JavaScript, and a vendored copy of jsPDF, so a form opens in a browser without a server. The first five forms below are the template for any new one; F-01A and F-01B extend that template with a built-in randomizer / digital ballot draw (see [Randomizer and ballot-draw forms](#randomizer-and-ballot-draw-forms-f-01a-f-01b)).

## Verification methodology

Match verification depth to the risk of the change — don't render a full PDF to check a wording fix, and don't trust a wording-level check for anything that touches layout.

**Text/content edits** (question wording, translations, labels, static copy) — a syntax check plus an exact-match occurrence count is enough:
1. Run `node --check` on the `<script>` block containing the change — catches a broken string (stray quote/bracket) before it ships.
2. Before replacing, confirm the old string occurs exactly once in the file (e.g. `content.count(old) == 1` in a short Python/Node snippet) so the edit can't silently land in the wrong place or double-replace. Most question text appears **twice** — once in the on-screen `<span class="qid">`/`<span class="qen">` (or `<div class="qid">`/`<div class="qen">`) markup, once in the PDF-side array (`PDF_SECTIONS` or equivalent) used to draw the PDF table — both occurrences need the same check and the same edit.
3. No live render needed at this tier — the risk is "wrong word," not "broken layout," and the occurrence-count check already proves the edit landed exactly where intended.

**Layout/visual edits** (stamp geometry, box positioning, CSS spacing, page-break behavior, anything that changes how something *looks* rather than what it *says*) — still need live verification, because jsPDF coordinate math and `@media print` pagination can't be reliably eyeballed from source:
1. Launch headless Chromium with a remote-debugging port and drive it over CDP (no `puppeteer`/`playwright` dependency needed — Node's built-in `fetch`/`WebSocket` are enough for `Page.navigate`, `Runtime.evaluate`, `Page.captureScreenshot`, `Page.printToPDF`).
2. Call the form's own PDF-building function directly (`buildPdf()` / `buildInspectionPDF()` / `drawSignedStamp()` / `stampPage()`) instead of reimplementing the geometry, so the test exercises the real code path, not a model of it.
3. Convert the resulting PDF to PNG with `pdftoppm` and inspect it (or diff output byte-sizes across variants that are expected to be visually identical, as a cheap first-pass check).
4. For anything touching page breaks or print layout, also render via CDP `Page.printToPDF` — this exercises `@media print` separately from the jsPDF path. The two pipelines are independent; a fix verified in one does not imply the other is fixed (see the Not Observed / Not Applicable rows added to food-inspection-form: the jsPDF path had a page-break guard from the start, but `@media print` had zero `break-inside` protection anywhere in the form and could tear the block across two pages until fixed separately).

### Verify it yourself

You don't need a dev environment to double-check either kind of change:
- **Content changes**: open the form in a browser, read the updated question/label on screen, then click **Save PDF** and check the saved file too — the on-screen text and the PDF text come from two separate places in the code, so checking only one can miss a half-applied edit.
- **Layout changes**: same as above, but look at spacing/alignment rather than wording — compare the changed element against neighboring rows for position, size, and corner/border style.
- **Page-break-sensitive changes**: fill the form out fully (or close to it) and print it to PDF directly from the browser (Ctrl/Cmd+P → Save as PDF) — check nothing splits awkwardly across a page. This is a different code path from the "Save PDF" button and needs its own check.

## Files in this folder

| File | Form | Expiry (`FORM_EXPIRY_DATE`) |
|---|---|---|
| `attendance-list-form-v280227-191026.html` | Attendance List — signature rows, up to 8 photos | 2027-02-28 |
| `camp-inspection-form-v280227-191026.html` | Camp Hygiene, Sanitation and Food Safety Inspection — scored checklist, sections, recommendations | 2027-02-28 |
| `da-consent-result-form-v280227-191026.html` | F-02 Drug & Alcohol Consent & Result — test-type radio, consent and result sections, signatures | 2027-02-28 |
| `da-reasonable-suspicion-checklist-v280227-171026.html` | F-04 Reasonable Suspicion Checklist — 8-category observation grid, decision & notification, 3-person signature block | 2027-02-28 |
| `da-positive-refusal-report-v280227-171026.html` | F-03 Positive Result / Refusal Report — event details, positive vs. refusal panels, immediate actions & HR follow-up, 4-person signature block | 2027-02-28 |
| `food-inspection-form-v280227-191026.html` | Food Stuff Incoming Inspection — scored checklist, sections, signatures | 2027-02-28 |
| `fatigue-scoring-form-v280227-201026.html` | Fatigue Scoring (IKT-LNG form-OH-007) — sleep/wake score X/Y/Z, control measure bands, signatures | 2027-02-28 |
| `da-monthly-randomization-record-v280227-191026.html` | F-01A Monthly Randomization Record — built-in randomizer draws the arrival/operator test Sundays and a 4-of-pool operator draw, signatures, up to 4 photos | 2027-02-28 |
| `da-arrival-ballot-draw-record-v280227-191026.html` | F-01B Arrival Ballot Draw Record — built-in digital ballot draw without replacement over a 27-row draw log, signatures, up to 4 photos | 2027-02-28 |

## Naming and versioning

- File name: `<kebab-name>-form-v<ddmmyy>-<ddmmyy>.html`: the expiry date, then the date `update-validity.py` was run. The script also accepts older names (`-v<YYYYMMDD>` with or without a suffix). The on-page label stays `Form v<YYYYMMDD>`, computed from `FORM_EXPIRY_DATE`.
- The date in the name equals `FORM_EXPIRY_DATE` inside the file. All current forms expire 2027-02-28.
- The page shows `Form v<YYYYMMDD>`, taken from `FORM_EXPIRY_DATE` (`#formVersion`, plus `#formVersionHeader` on phones).
- When a form is revised, save it as a new file with a new date. Do not edit a deployed version in place, because staff may still have it open. Rename only the forms you changed (plain `mv`, last segment = today, ddmmyy), not all of them, and fix the name in this readme's table and in `online-upload-plan.md`. Same-day re-edits keep the name. (`update-validity.py` renames every form; use it only for an expiry change.)

## Shared behaviour

1. **Expiry gate.** The first `<script>` in each file sets `FORM_EXPIRY_DATE`. Once the local date is past it, the page is replaced with "Form Unavailable" ("This form was only able to be used until <date>"), signed "Thank You - dr. Arif Rahman Hakim". The check runs before any other code.
2. **Status bar.** A sticky navy bar at the top shows online or offline (`#netDot`, `#netText`). The form keeps working offline, and data stays on the device.
3. **Header.** Title in English and Bahasa Indonesia, with a logo on most forms. The logo is embedded as a base64 `LOGO_B64` data URL in the attendance, drug and food forms; the camp form has no logo variable. On phones (≤640 px) the logo and title share one row.
4. **Meta fields.** A block of labelled underline inputs (`.meta-field`, ids listed in `META_IDS`). Two columns on desktop, one on phones. Topic and place fields are title-cased on blur.
5. **Required-field validation.** `validateRequired()` checks every id in `REQUIRED_IDS`.
   - Before a field is touched: pale yellow background (a hint only, via `:required:invalid`).
   - After it is touched and left empty: red underline (`.field-missing`).
   - Radio groups follow the same pattern on their label (`.field-missing` / `.hint-missing`).
   - The **Save PDF** button stays disabled until `validateRequired()` passes.
6. **Draft autosave.** `collectDraft()` and `saveDraft()` write the whole form to `localStorage` under one key per form. The drug form uses `f02ConsentDraft_v1`. `loadDraft()` runs on load, and saves are debounced on each input. Reset clears the draft and the fields.
7. **Signature pads.** Each signature box (`.sig-cursive`, placeholder from `data-placeholder`) opens a modal pad (`.sigpad-overlay`) with a canvas and an image-upload option. The result is stored as a PNG data URL and drawn into the PDF. The camp and food forms mirror a signature into other sections where the paper form repeats it (`mirrorSignatures`).
8. **Photos.** Up to 8 image slots (`photoinput-N`, `photopreview-N`, `photoremove-N`). Each image is resized in the browser (800px, JPEG 0.6) before it is stored in the draft. The PDF may re-encode them smaller to stay under 1 MB; see "PDF size limit". The stored copy is never changed. (Note: F-03 and F-04 omit photo slots as the source paper forms do not include a photo section.)
9. **Save PDF.** `buildPdf()` (attendance, drug, F-03, F-04) or `buildInspectionPDF()` (camp, food) lays out the PDF with jsPDF. Forms with photos call it through `buildPdfUnderLimit()`, which returns the finished Blob that is saved as is (see "PDF size limit"). `saveFile()` uses the File System Access API when available and falls back to a normal download. The file name uses the timestamp pattern `<base-filename>-yyyymmdd-hhmmss.pdf`, where the timestamp is the save time (e.g., `attendance-list-20270228-143025.pdf`). The base filename is the HTML file's name without the `-v<ddmmyy>-<ddmmyy>` suffix.
10. **Signed stamp.** After a successful save, a rotated red "SIGNED" stamp appears on the form (`.signed-stamp`, `.signed-stamp.show`) — a DOM-only, on-screen indicator. Separately, every form's generated PDF carries its own rotated, double-bordered red "SIGNED" stamp (with the save date) on every page; see [PDF signed stamp](#pdf-signed-stamp-template) for the shared shape.
11. **Signature font.** Signatures use Great Vibes from Google Fonts, which every form links. Offline, the browser falls back to Segoe Script, Brush Script MT, then generic cursive.
12. **Mobile layout.** Breakpoints at 640 px and 620 px. Question tables become stacked cards labelled Q / Score / Comment. Inputs use 16 px text to stop iOS zoom. The save bar is capped at 20% of viewport height.
13. **Print.** `@media print` hides the status bar and save bar.

## PDF size limit (< 1 MB)

Every form that carries photos (attendance, camp, food, consent-result, fatigue-scoring, F-01A, F-01B) keeps its saved PDF under `PDF_MAX_BYTES` = 1,000,000 bytes (decimal MB). Positive-refusal and reasonable-suspicion have no photos, and their text-only PDFs are far below the limit, so they have no limiter.

- `buildPdfUnderLimit(buildFn, store)` builds the PDF once. If the blob is over the limit it re-encodes every photo in `store` (`photoData`, or `photos` in fatigue) from the **stored originals** at the next step of `PDF_PHOTO_STEPS` (`[scale, jpeg quality]`: 0.9/0.55 down to 0.4/0.3) and rebuilds until it fits. Re-encoding from the originals each step avoids compounding JPEG loss.
- It returns the **Blob**, and callers save that blob directly. Do not call `doc.output()` again afterwards: a second `output()` on the same jsPDF doc returns a larger file (+6% food, +37% camp measured), so the size checked would no longer be the size saved.
- The stored photos and the draft are never altered. The store is swapped for the one build and restored in `finally`. Photos on screen and in drafts stay full quality.
- Photos are the only thing it shrinks. If a PDF is still over the limit after the last step (not seen: 8 per-pixel-noise 800px photos, ~1.5 MB, shrink to ~830 KB), it saves anyway and logs `console.warn`.
- Food and camp also now construct jsPDF with `compress:true` (camp's text-only PDF was ~290 KB uncompressed).
- Verification (2026-10-19): headless Chromium, 8 noisy photos per form, blob written to disk and measured; fatigue driven through the real `savePdf()` with `saveFile` stubbed. F-01A/B draw at most 4 photos and were already ~795 KB unshrunk.

## Scored checklists (camp and food forms)

- Each row has an id, English and Indonesian question text, and a score or rating input.
- Ratings use S / U / N / N-A / N-O (`.rate-input`; `U` shows in red).
- Sections collapse (`.section-toggle`, `.chevron`), and a section can be gated by a `section-gate` control.
- Totals are recalculated by `recalcSection` / `recalcGrandTotal` and `recalcOverall`.
- **PDF tables must not split a row across pages.** The food and camp forms' `autoTable` calls set `rowPageBreak:"avoid"` (fixed 2026-10-19; jsPDF's default `"auto"` cut Frozen Food Q4 in half, and on camp clipped rows at the top of several pages). Because the question text is hand-drawn in `didDrawCell`, a split row also leaves clipped text.
- **Stamp size depends on the doc unit.** Food and camp forms' stamps were ~2.8x too small until `drawSignedStamp` gained the `U` conversion (see PDF signed stamp). When placing it, allow for the rotated box's ~193pt-wide bounding box: food and camp use `cx = PAGE_W - MARGIN - 100`.

## Randomizer and ballot-draw forms (F-01A, F-01B)

F-01A and F-01B follow the shared behaviour above (expiry gate, status bar, draft autosave, photos, save flow) but replace manual data entry with an in-form randomizer, and diverge from the template in a few specific ways:

1. **Signature auto-stamp, not a typed date field.** Neither form has a separate "date" input next to a signature. The moment ink is captured, `sigDates[key] = formatSignStamp(new Date())` stamps a single line — `dd mmm yyyy, HH:mm UTC±N` — in bold red, overlapping the lower edge of the signature image itself, both on screen and in the PDF. `DRAFT_KEY`: `f01aDraft_v1` (F-01A), `f01bDraft_v1` (F-01B).
2. **Name fields are sanitized, not free text.** Every person-name field (`NAME_IDS`, plus the per-row Name columns) is restricted to letters and spaces, auto Title-Cased, and has repeated spaces collapsed as you type (`sanitizeNameTyping` / `sanitizeNameFinal`). Company/Dept and "replaced by" fields are left free text.
3. **F-01A — Monthly Randomization Record.**
   - Section B's Randomize button draws one Sunday (week 1–5) for the arrival test and a different Sunday for the operator test, from the selected test month. It is a one-shot action per month value: the button disables itself once a result exists and only re-enables if the month changes or the form is Reset (`updateRandomizeBtnState`).
   - Section C's operator Randomize button draws 4 unique pool numbers (not names — staff cross-reference the numbers against the physical roster) from "Total names in pool", with the same one-shot-per-pool-size lock.
   - The operator table reflows into label/value cards on phones (≤640 px) instead of a horizontal-scroll table.
   - Method selector defaults to "Built-in randomizer (this form)"; "Ballot draw" is the only other option.
4. **F-01B — Arrival Ballot Draw Record.**
   - Each of the 27 draw rows gets a "Draw" button (never printed in the PDF — the PDF reads the underlying data, not the DOM). It is only shown for rows `1..arriving` (from "Arriving employees") and only enabled once that row's Name **and** Company/Dept are filled.
   - The first draw builds a pool of exactly 4 "YES" + `(arriving − 4)` "NO" tokens, shuffles it, and locks "Arriving employees" (read-only) so the pool can't drift mid-draw. Each click removes one token from the pool without replacement — once every arriving employee has drawn, the YES count is always exactly 4, by construction.
   - YES/NO radios are `disabled` from creation: the only way to set a row's result is the Draw button (no manual override). The Time column is reused for the auto-stamp (two lines: date, then time + timezone) instead of a separate column.
5. **Audit page is always printed, not optional.** Both forms unconditionally add a page 2 to the PDF — `doc.addPage()` runs regardless of whether photos are attached — stating the method used, per-row/per-draw timestamps, and results, followed by up to 4 photos on the same page (paginating further only if space runs out).
6. **F-01B's PDF tables never split a row across pages.** The draw-record table and the per-employee audit log both set `rowPageBreak: "avoid"` in `buildPdf()`, so autoTable pushes a whole row to the next page rather than cutting it mid-row. F-01A and F-01B draw the PDF stamp via an inlined `stampPage(doc, pageNum, PW, stampDate)` instead of the shared `drawSignedStamp()` (see below) — same shape and sizing as the canonical template, but `cx`/`cy` hardcoded to the page's top-right corner instead of taken as parameters.

## PDF signed stamp (template)

Every form draws a rotated, double-bordered red "SIGNED" stamp (plus the save date) on every page of the generated PDF, once per `doc.getNumberOfPages()` after the PDF is otherwise complete, via a `drawSignedStamp(doc, cx, cy, angleDeg)` function (or, for F-01A/F-01B, an inlined `stampPage()` with hardcoded placement — see below). **All 9 forms share the same shape and sizing numbers** (retrofitted 2026-10-12) — but see the unit audit below, because those numbers are millimetres and most forms' PDFs are in points — the version below, originally tuned on F-03, is the canonical template for every form, new and existing:

```js
function drawSignedStamp(doc, cx, cy, angleDeg) {
  var rad = angleDeg * Math.PI / 180, cos = Math.cos(rad), sin = Math.sin(rad);
  var RED = [178, 30, 30];
  // Designed in mm, but most forms' jsPDF docs are unit:"pt". U converts mm to
  // the doc's own unit; without it the box is ~2.8x too small for the 20pt text.
  var U = (72 / 25.4) / doc.internal.scaleFactor;
  function corners(w, h) {
    return [[-w / 2, -h / 2], [w / 2, -h / 2], [w / 2, h / 2], [-w / 2, h / 2]].map(function (p) {
      return [cx + p[0] * cos - p[1] * sin, cy + p[0] * sin + p[1] * cos];
    });
  }
  doc.setDrawColor.apply(doc, RED);
  [[65, 20, 1.0], [58, 14, 0.5]].forEach(function (r) {
    var c = corners(r[0] * U, r[1] * U);
    doc.setLineWidth(r[2] * U);
    doc.lines([
      [c[1][0] - c[0][0], c[1][1] - c[0][1]],
      [c[2][0] - c[1][0], c[2][1] - c[1][1]],
      [c[3][0] - c[2][0], c[3][1] - c[2][1]],
      [c[0][0] - c[3][0], c[0][1] - c[3][1]]
    ], c[0][0], c[0][1], [1, 1], "S", true);
  });
  // places text centered on the rotated baseline, at local (box-frame) offset
  // (0, dy) from the stamp center — doc.text's angle+align:"center" combo does
  // NOT center against the rotated baseline, so we compute the anchor by hand.
  // Note: doc.text's angle rotates directly in PDF's Y-up space, while the box
  // corners above are rotated in JS Y-down space and then y-flipped by jsPDF —
  // that flip inverts the visible rotation, so the text angle must be negated
  // to turn the same way as the box (verified against the raw PDF content stream).
  function stampText(text, dy) {
    var px = cx - dy * U * sin, py = cy + dy * U * cos;
    var tw = doc.getTextWidth(text);
    doc.text(text, px - (tw / 2) * cos, py - (tw / 2) * sin, { angle: -angleDeg });
  }
  doc.setTextColor.apply(doc, RED);
  doc.setFont("helvetica", "bold");
  doc.setFontSize(20);
  stampText("SIGNED", 0.5);
  doc.setFont("helvetica", "normal");
  doc.setFontSize(7.5);
  var dateText = new Date().toLocaleDateString("en-GB", { day: "2-digit", month: "short", year: "numeric" });
  stampText(dateText, 4.5);
  doc.setTextColor(0, 0, 0);
  doc.setDrawColor(0, 0, 0);
}
```

**Unit audit (2026-10-19).** The stamp numbers only produce a 65x20mm stamp in a `unit:"mm"` doc.
- `mm` docs, correct as is: positive-refusal, reasonable-suspicion.
- `pt` docs, fixed with the `U` conversion: food, camp (both have free space below the signatures, stamp at `PAGE_W - MARGIN - 100`).
- `pt` docs, fixed with the `U` conversion, **full-size stamp accepted by the project owner despite overlapping content**: attendance (centered over the table, covers some name/company cells), consent-result (`PW - M - 100`, covers the Final-result checkbox text in D3), fatigue-scoring (`M + W - 100`, spills slightly out of the Decision box), F-01A / F-01B (`cx = PW - 110, cy = 62`, covers the title area and, on continuation pages, table header/rows). If overlap becomes a problem, shrink the stamp or move it rather than reverting `U`.
- F-01A has two `autoTable` calls with no `rowPageBreak`; only F-01B sets `"avoid"`. Attendance's single `autoTable` has none either. Not checked for visible splits.

Only `cx`, `cy`, and `angleDeg` are placement — pass whatever position/tilt suits the new form's page, but copy the shape, sizing, and offsets unchanged:

- Double border, all units mm (multiplied by `U` at draw time, so the stamp is the same physical size whether the doc is `pt` or `mm`; `cx`/`cy` are still in the doc's own unit): outer box 65×20, inner box 58×14 (3.5mm gap on every side), line widths 1.0 / 0.5.
- Text: "SIGNED" at font size 20 bold, baseline 0.5mm below center; the save date (`DD Mon YYYY`) at font size 7.5 normal, baseline 4.5mm below center. These two `dy` offsets are a deliberately solved pair, not independent knobs — see below.
- `{ angle: -angleDeg }` in `stampText` (negated, not `angleDeg` plain) is load-bearing. The box corners are rotated in JS screen space and then y-flipped by jsPDF when drawn as lines; `doc.text`'s own `angle` option rotates directly in the PDF's un-flipped space. Passing `angleDeg` straight through makes the box and the text spin in opposite directions — confirmed by rendering the real PDF content stream, not by eyeballing it. Do not "simplify" this back.

**Why `dy = 0.5` and `4.5`, not round numbers.** The two text lines are vertically centered as *one block* inside the inner box, not independently centered — "SIGNED" has no descender (all-caps), the date line has an ascender, and Helvetica's cap-height is `fontSize * 0.718 * 0.3528`mm. Solving for a 2mm visual gap between the two lines, centered inside the 14mm-tall inner box, gives `dy("SIGNED") ≈ 0.5` and `dy(date) ≈ 4.5` — not `-half` / `+half` of the box height, and not symmetric around 0. If the font sizes or the inner box height change, re-derive these two offsets together (cap-height formula above) and re-render through the real-jsPDF + pdftoppm verification harness (see [Verification methodology](#verification-methodology) — this is a layout edit, not a text edit, so the occurrence-count-only check does not apply here) rather than eyeballing new round numbers.

F-01A and F-01B (`monthly-randomization-record`, `arrival-ballot-draw-record`) inline the stamp as `stampPage(doc, pageNum, PW, stampDate)`, with `cx = PW - 70, cy = 54, angleDeg = -16` fixed inside the function instead of taken as parameters. The box/font/offset values inside `stampPage` were brought into line with the canonical template above (outer/inner box expressed as half-width/half-height via `drawRect(hw, hh)`: `drawRect(32.5, 10)` then `drawRect(29, 7)` at line widths 1.0/0.5, font 20/7.5, dy 0.5/4.5) — only the placement (`cx`/`cy`/`angleDeg`) and the hardcoded top-right position remain form-specific.

## HTML on-screen signed stamp (template)

Separately from the PDF, a DOM-only "SIGNED" indicator appears on the live page after a successful save (`.signed-stamp`, toggled visible via `.signed-stamp.show`). This CSS block is shared verbatim across every form except F-01A/F-01B (which have no on-screen save-confirmation stamp) and is the template for new forms:

```css
.signed-stamp {
  display: none; position: relative; width: 150px; margin: 18px auto 0;
  text-align: center; transform: rotate(-12deg);
  border: 3px double #b21e1e; border-radius: 6px; padding: 8px 6px;
  color: #b21e1e; font-weight: 800; font-size: 20px; letter-spacing: 2px;
  opacity: 0; transition: opacity 0.4s ease, transform 0.4s ease;
}
.signed-stamp.show { display: block; opacity: 0.9; }
.signed-stamp .stamp-date { display: block; font-size: 9px; font-weight: 600; letter-spacing: 1px; margin-top: 2px; }
```

This on-screen version was left unchanged in this pass — it reads fine at its current proportions (20px "SIGNED" in a 150px box plus `padding: 8px 6px`, versus the PDF stamp's old 12pt-in-65mm ratio that prompted the PDF retune). It does not need to match the PDF stamp's exact font/box ratio; it's a different medium (DOM flow vs. a rotated vector overlay on a printed page) and was never reported as hard to read.

## Adding a new form

Start from the attendance list form (simplest), or the camp form if you need scored sections.

1. Copy the file to `<name>-form-v<ddmmyy>-<ddmmyy>.html`. Set `FORM_EXPIRY_DATE`, `<title>`, and the `#formVersion` text.
2. Replace the title and bilingual headings. Keep the header, status bar, and meta-field structure.
3. Define the fields. Add required ids to `REQUIRED_IDS`. Add text fields to `META_IDS` when they should trigger draft save and the required check on input. If a checklist/radio row's label contains a nested free-text `<input>` (e.g. an inline "Name:"/"Time:" field, or an "Other: ___" reason field), size the row's own checkbox/radio with a child combinator (`.row-class > input`), never a bare descendant selector (`.row-class input`) — the descendant form also matches the nested text input and silently forces it to the checkbox's tiny `flex-basis`, which overrides any `width` set on it regardless of specificity or `!important` (see Cautions below).
4. Give the draft a `DRAFT_KEY` unique to the form (for example `<name>Draft_v1`). Never reuse another form's key.
5. Keep the expiry gate, status bar, draft autosave, signature pads, photo slots, and save flow unchanged unless the form needs something different.
6. Update `buildPdf()` for the new fields. Implement `pdfName()` to return `<base-filename>-yyyymmdd-hhmmss.pdf` using the save-time timestamp (the base filename is the HTML file's name without the `-v<ddmmyy>-<ddmmyy>` suffix).
7. Test in a browser:
   - expiry gate: set `FORM_EXPIRY_DATE` to a past date and confirm "Form Unavailable" appears;
   - required colours, and Save stays disabled until the form is valid;
   - draft restore after reload;
   - PDF output, including signatures and photos;
   - phone width, with no horizontal page scroll.
8. Check for script errors with a headless browser: no uncaught exceptions on load.
9. Add the new file to the table at the top of this readme.

## Cautions

- The vendored jsPDF script is about 450–500 KB per form. Do not edit it. To update it, copy a newer build into every form.
- The `FORM_EXPIRY_DATE` line must stay the first script in the file, so the gate runs before anything else.
- Keep every user-visible label bilingual (English / Bahasa Indonesia), matching the printed forms.
- Store signatures and photos as data URLs, not external files, so the form works offline.
- The only external resource is the Google Fonts stylesheet for the signature font. Do not add other network calls.
- Never style the tick-checkbox of a checklist row with a bare descendant selector like `.checkbox-item input { flex: 0 0 15px; ... }`. If the row's label also contains nested text `<input>`s (e.g. a "Name:"/"Time:" inline field), that selector matches those too and silently forces them to the checkbox's tiny `flex-basis`, which overrides any `width` you set on them (not a specificity problem — `flex-basis` wins over `width` on a flex item whenever it isn't `auto`, regardless of `!important`). `getComputedStyle(el).flexBasis` will show the leaked value when this happens. Scope the rule to the checkbox itself with a child combinator instead: `.checkbox-item > input`.
