// The Drug & Alcohol consent and result form as a PDF (A4, two pages, plus a
// photo page when photos were added). Ported from buildPdf() in
// forms/da-consent-result-form-*.html so the printed certificate looks the same;
// the difference is that it reads a plain `values` object (da-form-core.js)
// instead of the page, and a signature is the cursive first name picture made
// by da-form.js. Needs jsPDF (/static/vendor/jspdf.min.js, MIT) and da-form-core.js.

const DA_PDF_MAX_BYTES = 1000000; // decimal MB, so it is under 1 MB by either definition
// When the first build is over the limit, photos are re-encoded from the
// originals at these [scale, jpeg quality] steps until the PDF fits.
const DA_PDF_PHOTO_STEPS = [[0.9, 0.55], [0.8, 0.5], [0.7, 0.45], [0.6, 0.4], [0.5, 0.35], [0.4, 0.3]];

function daCheckedMark(doc, x, y, on) {
  doc.setDrawColor(0, 0, 0);
  doc.setLineWidth(0.6);
  doc.rect(x, y, 7, 7);
  if (on) {
    doc.setLineWidth(1);
    doc.line(x + 1.2, y + 1.2, x + 5.8, y + 5.8);
    doc.line(x + 5.8, y + 1.2, x + 1.2, y + 5.8);
  }
}

// The red "SIGNED <date>" stamp. Designed in millimetres, so U converts to the
// document's own unit (points here).
function daDrawSignedStamp(doc, cx, cy, angleDeg) {
  const rad = (angleDeg * Math.PI) / 180;
  const cos = Math.cos(rad);
  const sin = Math.sin(rad);
  const RED = [178, 30, 30];
  const U = 72 / 25.4 / doc.internal.scaleFactor;
  const corners = (w, h) =>
    [[-w / 2, -h / 2], [w / 2, -h / 2], [w / 2, h / 2], [-w / 2, h / 2]].map((p) => [cx + p[0] * cos - p[1] * sin, cy + p[0] * sin + p[1] * cos]);
  doc.setDrawColor(...RED);
  for (const [w, h, line] of [[65, 20, 1.0], [58, 14, 0.5]]) {
    const c = corners(w * U, h * U);
    doc.setLineWidth(line * U);
    doc.lines(
      [
        [c[1][0] - c[0][0], c[1][1] - c[0][1]],
        [c[2][0] - c[1][0], c[2][1] - c[1][1]],
        [c[3][0] - c[2][0], c[3][1] - c[2][1]],
        [c[0][0] - c[3][0], c[0][1] - c[3][1]],
      ],
      c[0][0], c[0][1], [1, 1], "S", true,
    );
  }
  // Text centred on the rotated baseline: doc.text's angle + align:"center" does
  // not centre against the rotated baseline, so the anchor is worked out by hand.
  const stampText = (text, dy) => {
    const px = cx - dy * U * sin;
    const py = cy + dy * U * cos;
    const tw = doc.getTextWidth(text);
    doc.text(text, px - (tw / 2) * cos, py - (tw / 2) * sin, { angle: -angleDeg });
  };
  doc.setTextColor(...RED);
  doc.setFont("helvetica", "bold");
  doc.setFontSize(20);
  stampText("SIGNED", 0.5);
  doc.setFont("helvetica", "normal");
  doc.setFontSize(7.5);
  stampText(new Date().toLocaleDateString("en-GB", { day: "2-digit", month: "short", year: "numeric" }), 4.5);
  doc.setTextColor(0, 0, 0);
  doc.setDrawColor(0, 0, 0);
}

// Builds the document. `sigs` is { donor: { png, stamp }, ... }, `photos` a list
// of { dataUrl, w, h }, `logo` a PNG data URL (or "" for none) and `docId` the
// record's unique id, printed under the header.
function daBuildPdf(values, sigs, photos, logo, docId) {
  const doc = new window.jspdf.jsPDF({ unit: "pt", format: "a4", orientation: "portrait", compress: true });
  const PW = doc.internal.pageSize.getWidth();
  const PH = doc.internal.pageSize.getHeight();
  const M = 30;
  const W = PW - 2 * M;
  const NAVY = [31, 56, 100];
  const NAVY_LIGHT = [220, 230, 241];
  const GRID = [185, 196, 208];
  const GREY = [90, 90, 90];
  const v = (key) => String((values || {})[key] || "");

  function setText(size, style, color) {
    doc.setFont("helvetica", style || "normal");
    doc.setFontSize(size);
    doc.setTextColor(...(color || [0, 0, 0]));
  }
  function box(x, y, w, h) {
    doc.setDrawColor(...GRID);
    doc.setLineWidth(0.5);
    doc.rect(x, y, w, h, "S");
  }
  function labelCell(x, y, w, h, label, sub) {
    doc.setFillColor(...NAVY_LIGHT);
    doc.rect(x, y, w, h, "F");
    box(x, y, w, h);
    setText(8, "bold", NAVY);
    doc.text(label, x + 4, y + (sub ? h / 2 - 1 : h / 2 + 3));
    if (sub) {
      setText(6.8, "italic", GREY);
      doc.text(sub, x + 4, y + h / 2 + 8);
    }
  }
  function valueCell(x, y, w, h, value) {
    box(x, y, w, h);
    setText(8.5);
    doc.text(String(value || ""), x + 4, y + h / 2 + 3);
  }
  function navyBar(x, y, w, h, text, size) {
    doc.setFillColor(...NAVY);
    doc.rect(x, y, w, h, "F");
    setText(size, "bold", [255, 255, 255]);
    doc.text(text, x + w / 2, y + h / 2 + 2.5, { align: "center" });
  }
  function checkItem(x, y, on, text, sub) {
    daCheckedMark(doc, x, y - 6, on);
    setText(8.2);
    doc.text(text, x + 10, y);
    let nx = x + 10 + doc.getTextWidth(text);
    if (sub) {
      setText(7, "italic", GREY);
      doc.text(" " + sub, nx, y);
      nx += doc.getTextWidth(" " + sub);
    }
    return nx + 12;
  }
  function sectionHead(yTop, letter, title, sub) {
    const baseline = yTop + 9;
    setText(10.5, "bold", NAVY);
    doc.text(letter + "  " + title, M, baseline);
    const tw = doc.getTextWidth(letter + "  " + title);
    setText(8.5, "italic", GREY);
    doc.text(" / " + sub, M + tw, baseline);
    doc.setDrawColor(...NAVY);
    doc.setLineWidth(1.2);
    doc.line(M, baseline + 4, PW - M, baseline + 4);
    return baseline + 10;
  }
  function subHead(y, text) {
    setText(9, "bold", NAVY);
    doc.text(text, M, y + 2);
    return y + 6;
  }
  function ensureRoom(curY, need) {
    if (curY + need > PH - M) {
      doc.addPage();
      return M + 6;
    }
    return curY;
  }
  function sigCell(x, y, w, h, key) {
    box(x, y, w, h);
    const sig = (sigs || {})[key];
    if (!sig) return;
    const props = doc.getImageProperties(sig.png);
    const s = Math.min((w - 10) / props.width, (h - 8) / props.height);
    const iw = props.width * s;
    const ih = props.height * s;
    const top = y + (h - ih) / 2;
    doc.addImage(sig.png, "PNG", x + (w - iw) / 2, top, iw, ih, undefined, "FAST");
    if (sig.stamp) {
      setText(6.2, "bold", [200, 30, 30]);
      doc.text(sig.stamp, x + w / 2, top + ih * 0.9, { align: "center" });
    }
  }

  // Header: logo | title | form ID
  let y = 26;
  const FORM_W = 104;
  const titleX = M + 60 + (W - 60 - FORM_W) / 2;
  doc.setDrawColor(...GRID);
  doc.setLineWidth(0.6);
  doc.rect(M, y, W, 66, "S");
  if (logo) doc.addImage(logo, "PNG", M + 12, y + 8, 36, 46);
  setText(12, "bold", NAVY);
  doc.text("DRUG & ALCOHOL TEST", titleX, y + 22, { align: "center" });
  doc.text("CONSENT & RESULT FORM", titleX, y + 40, { align: "center" });
  setText(8, "italic", GREY);
  doc.text("Formulir Persetujuan & Hasil Tes Narkoba dan Alkohol", titleX, y + 52, { align: "center" });
  setText(8.5, "bold");
  doc.text("PT. Layar Nusantara Gas", titleX, y + 62, { align: "center" });
  doc.rect(M + W - FORM_W - 8, y + 6, FORM_W, 50, "S");
  setText(7.5);
  doc.text("Form No: F-02", M + W - FORM_W - 2, y + 16);
  doc.text("Rev: 00", M + W - FORM_W - 2, y + 27);
  doc.text("Ref: WI-HSE-___", M + W - FORM_W - 2, y + 38);
  setText(7.5, "bold", [179, 38, 30]);
  doc.text("CONFIDENTIAL / RAHASIA", M + W - FORM_W - 2, y + 50);
  setText(7, "normal", [120, 120, 120]);
  doc.text("Document ID: " + (docId || ""), M, y + 76);

  // A. Donor information
  y = sectionHead(y + 86, "A.", "Donor information", "Data donor");
  const c1 = 104;
  const v1 = 166;
  const v2 = W - c1 - v1 - c1;
  labelCell(M, y, c1, 22, "Name", "Nama");
  valueCell(M + c1, y, v1, 22, v("donorName"));
  labelCell(M + c1 + v1, y, c1, 22, "Employee ID", "NIK");
  valueCell(M + c1 + v1 + c1, y, v2, 22, v("donorNik"));
  y += 22;
  labelCell(M, y, c1, 22, "Company / Dept.", "Perusahaan / Dept.");
  valueCell(M + c1, y, v1, 22, v("donorCompany"));
  labelCell(M + c1 + v1, y, c1, 22, "Position", "Jabatan");
  valueCell(M + c1 + v1 + c1, y, v2, 22, v("donorPosition"));
  y += 22;
  labelCell(M, y, c1, 26, "Test type", "Jenis tes");
  box(M + c1, y, W - c1, 26);
  const tt = v("testType");
  const idOf = (value) => DA_TEST_TYPES.find((t) => t.value === value);
  let tx = M + c1 + 8;
  for (const value of ["random", "pre", "post"]) {
    const t = idOf(value);
    tx = checkItem(tx, y + 11, tt === value, t.en, "(" + t.id + ")");
  }
  checkItem(M + c1 + 8, y + 22, tt === "suspicion", idOf("suspicion").en, "(" + idOf("suspicion").id + ")");
  checkItem(M + c1 + 180, y + 22, tt === "return", idOf("return").en, "(" + idOf("return").id + ")");
  y += 32;

  // B. Consent
  const consentColW = W / 2 - 14;
  const BULLET = 9;
  const EN_SIZE = 8.2, EN_LH = 9.2, ID_SIZE = 7.6, ID_LH = 8.4, GAP = 4;
  function measureCol(list) {
    let h = 0;
    const blocks = list.map((it) => {
      setText(EN_SIZE);
      const enLines = doc.splitTextToSize(it.en, consentColW - BULLET);
      setText(ID_SIZE, "italic");
      const idLines = doc.splitTextToSize(it.id, consentColW - BULLET);
      h += enLines.length * EN_LH + idLines.length * ID_LH + GAP;
      return { enLines, idLines };
    });
    return { blocks, h };
  }
  const left = measureCol(DA_CONSENT_ITEMS.filter((_, i) => i % 2 === 0));
  const right = measureCol(DA_CONSENT_ITEMS.filter((_, i) => i % 2 === 1));
  const consentH = Math.max(left.h, right.h) + 10;
  y = ensureRoom(y + 4, 19 + consentH + 14 + 44 + 22);
  y = sectionHead(y, "B.", "Consent", "Persetujuan");
  box(M, y, W, consentH);
  function drawCol(x, measured) {
    let cy = y + 9;
    for (const b of measured.blocks) {
      setText(EN_SIZE);
      doc.text("•", x, cy);
      doc.text(b.enLines, x + BULLET, cy);
      cy += b.enLines.length * EN_LH;
      setText(ID_SIZE, "italic", GREY);
      doc.text(b.idLines, x + BULLET, cy);
      cy += b.idLines.length * ID_LH + GAP;
    }
  }
  drawCol(M + 5, left);
  drawCol(M + W / 2 + 5, right);
  y += consentH;
  const sw = W / 3;
  navyBar(M, y, sw, 14, "Donor's signature / Tanda tangan donor", 7.8);
  navyBar(M + sw, y, sw, 14, "Witness' signature / Tanda tangan saksi", 7.8);
  navyBar(M + sw * 2, y, sw, 14, "Date / Time / Tanggal / Jam", 7.8);
  y += 14;
  sigCell(M, y, sw, 44, "donor");
  sigCell(M + sw, y, sw, 44, "witness");
  valueCell(M + sw * 2, y, sw, 44, daFormatDateTime(v("consentDt")));
  y += 44;
  valueCell(M, y, sw, 16, "Name / Nama: " + v("donorName"));
  valueCell(M + sw, y, sw, 16, "Name / Nama: " + v("witnessName"));
  box(M + sw * 2, y, sw, 16);
  y += 22;

  // C. Medication
  y = ensureRoom(y, 19 + 14 + 14 + 2 * 18);
  y = sectionHead(y, "C.", "Medication in the past 30 days", "Obat yang dikonsumsi 30 hari terakhir");
  checkItem(M + 4, y + 2, !!(values || {}).medNone, "I have NOT taken any medicine in the past 30 days", "Saya TIDAK mengonsumsi obat dalam 30 hari terakhir");
  y += 14;
  const mc = [26, 170, 100, 110, W - 26 - 170 - 100 - 110];
  const heads = ["No", "Medicine name / Nama obat", "Dose / freq. / Dosis", "Reason / Alasan", "Prescribed? / Resep dokter?"];
  let mx = M;
  heads.forEach((h, i) => {
    navyBar(mx, y, mc[i], 14, h, 7);
    mx += mc[i];
  });
  y += 14;
  for (const n of [1, 2]) {
    let rx = M;
    [String(n), v("med" + n + "Name"), v("med" + n + "Dose"), v("med" + n + "Reason")].forEach((text, i) => {
      box(rx, y, mc[i], 18);
      setText(8.2);
      doc.text(text, rx + 4, y + 12);
      rx += mc[i];
    });
    box(rx, y, mc[4], 18);
    const presc = v("med" + n + "Presc");
    const px = checkItem(rx + 6, y + 12, presc === "yes", "Yes");
    checkItem(px, y + 12, presc === "no", "No");
    y += 18;
  }

  // Page 2: test result and onward
  doc.addPage();
  y = M + 6;
  y = sectionHead(y, "D.", "Test result", "Hasil tes");
  y = subHead(y, "D1. Breath alcohol / Alkohol napas");
  labelCell(M, y, 110, 20, "Device / Serial No.", "Alat / No. Seri");
  valueCell(M + 110, y, 150, 20, v("breathDevice"));
  labelCell(M + 260, y, 120, 20, "Calibration valid until", "Kalibrasi berlaku s/d");
  valueCell(M + 380, y, W - 380, 20, daFormatDateLong(v("breathCalib")));
  y += 20;
  const bc = [120, 60, 110, 110, W - 400];
  let bx = M;
  ["Test / Tes", "Time / Jam", "Reading (BAC %) / Hasil", "Mouthpiece new & sealed", "Result / Hasil"].forEach((h, i) => {
    navyBar(bx, y, bc[i], 16, h, 6.8);
    bx += bc[i];
  });
  y += 16;
  for (const [n, en, id] of [[1, "1st test", "Tes pertama"], [2, "Confirmation (+15 min)", "Konfirmasi (+15 menit)"]]) {
    let cx = M;
    box(cx, y, bc[0], 20);
    setText(8, "bold");
    doc.text(en, cx + 4, y + 9);
    setText(6.8, "italic", GREY);
    doc.text(id, cx + 4, y + 17);
    cx += bc[0];
    box(cx, y, bc[1], 20);
    setText(8.2);
    doc.text(v("b" + n + "Time"), cx + 4, y + 13);
    cx += bc[1];
    box(cx, y, bc[2], 20);
    doc.text(v("b" + n + "Read"), cx + 4, y + 13);
    cx += bc[2];
    box(cx, y, bc[3], 20);
    daCheckedMark(doc, cx + bc[3] / 2 - 3.5, y + 6.5, !!values["b" + n + "Mouth"]);
    cx += bc[3];
    box(cx, y, bc[4], 20);
    const res = v("b" + n + "Res");
    const next = checkItem(cx + 6, y + 13, res === "neg", "Negative");
    checkItem(next - 4, y + 13, res === "pos", "Positive");
    y += 20;
  }
  y = ensureRoom(y + 8, 6 + 16 + 14 + 2 * 16);
  y = subHead(y, "D2. Urine drug screen / Skrining narkoba urin");
  setText(8, "bold");
  doc.text("Specimen check:", M, y + 10);
  let sx = checkItem(M + 72, y + 10, !!values.specTemp, "Temperature 32–38 °C", "Suhu 32–38 °C");
  sx = checkItem(sx - 4, y + 10, !!values.specColour, "Colour acceptable", "Warna dapat diterima");
  setText(8, "bold");
  doc.text("Temp: " + v("specTempVal") + " °C", sx, y + 10);
  y += 16;
  const pc = [W - 6 * 70, 70, 70, 70, 70, 70, 70];
  let px2 = M;
  ["Panel / Parameter", "AMP", "MET", "THC", "COC", "MOP", "BZO"].forEach((label, i) => {
    navyBar(px2, y, pc[i], 14, label, 7.5);
    px2 += pc[i];
  });
  y += 14;
  for (const [label, field] of [["Negative / Negatif", "pneg"], ["Non-negative / Non-negatif", "pnonneg"]]) {
    let rx = M;
    box(rx, y, pc[0], 16);
    setText(7.8, "bold");
    doc.text(label, rx + 4, y + 10.5);
    rx += pc[0];
    DA_PANELS.forEach((p, i) => {
      box(rx, y, pc[i + 1], 16);
      daCheckedMark(doc, rx + pc[i + 1] / 2 - 3.5, y + 4.5, !!(values[field] || {})[p]);
      rx += pc[i + 1];
    });
    y += 16;
  }
  y = ensureRoom(y + 8, 6 + 52 + 30 + 32 + 4 + 14 + 40 + 16);
  y = subHead(y, "D3. Conclusion / Kesimpulan");
  const fr = v("finalRes");
  labelCell(M, y, 110, 52, "Final result", "Hasil akhir");
  box(M + 110, y, W - 110, 52);
  checkItem(M + 118, y + 12, fr === "neg", "NEGATIVE – fit to work", "NEGATIF – layak bekerja");
  checkItem(M + 118, y + 26, fr === "pos", "POSITIVE / NON-NEGATIVE – not fit to work", "POSITIF / NON-NEGATIF – tidak layak kerja");
  checkItem(M + 118, y + 40, fr === "lab", "Sample sent to laboratory for confirmation", "Sampel dikirim ke laboratorium untuk konfirmasi");
  daDrawSignedStamp(doc, PW - M - 100, y + 26, -16);
  y += 52;
  labelCell(M, y, 110, 18, "Remarks", "Catatan");
  valueCell(M + 110, y, W - 110, 18, v("remarks"));
  y += 30;
  setText(6.8);
  for (const ln of doc.splitTextToSize("I certify that I collected the specimen(s) from the donor above; to the best of my knowledge they were not substituted or adulterated.", W)) {
    doc.text(ln, M, y);
    y += 8;
  }
  setText(6.8, "italic", GREY);
  for (const ln of doc.splitTextToSize("Saya menyatakan telah mengambil spesimen dari donor di atas dan sepengetahuan saya spesimen tidak ditukar atau dicampur.", W)) {
    doc.text(ln, M, y);
    y += 8;
  }
  y += 4;
  ["Collector / Petugas", "Witness (Security) / Saksi", "Donor / Donor"].forEach((h, i) => navyBar(M + i * sw, y, sw, 14, h, 7.5));
  y += 14;
  sigCell(M, y, sw, 40, "collector");
  sigCell(M + sw, y, sw, 40, "security");
  sigCell(M + 2 * sw, y, sw, 40, "ack");
  y += 40;
  valueCell(M, y, sw, 16, "Name / Nama: " + v("collectorName"));
  valueCell(M + sw, y, sw, 16, "Name / Nama: " + v("securityName"));
  valueCell(M + 2 * sw, y, sw, 16, "Name / Nama: " + v("ackName"));

  // Photos, only when at least one was added: two per row, as on the offline form.
  if (photos && photos.length) {
    doc.addPage();
    y = M + 6;
    setText(11, "bold", NAVY);
    doc.text("Photos / Foto", M, y + 4);
    doc.setDrawColor(...NAVY);
    doc.setLineWidth(1.2);
    doc.line(M, y + 9, PW - M, y + 9);
    y += 20;
    const cw = (W - 12) / 2;
    const ch = 165;
    photos.forEach((ph, i) => {
      const col = i % 2;
      if (col === 0 && y + ch > PH - 50) {
        doc.addPage();
        y = M;
      }
      const x = M + col * (cw + 12);
      box(x, y, cw, ch);
      const s = Math.min((cw - 8) / ph.w, (ch - 8) / ph.h);
      doc.addImage(ph.dataUrl, "JPEG", x + (cw - ph.w * s) / 2, y + (ch - ph.h * s) / 2, ph.w * s, ph.h * s);
      if (col === 1) y += ch + 12;
    });
  }

  const total = doc.internal.getNumberOfPages();
  for (let pg = 1; pg <= total; pg++) {
    doc.setPage(pg);
    setText(6.8, "normal", [110, 110, 110]);
    doc.text("F-02 | Medical record – keep confidential in clinic / Rekam medis – disimpan rahasia di klinik", PW / 2, PH - 18, { align: "center" });
  }
  return doc;
}

// A photo again as a smaller JPEG, or null when it cannot be decoded.
function daReencodeJpeg(photo, scale, quality) {
  return new Promise((resolve) => {
    const img = new Image();
    img.onload = () => {
      const w = Math.max(1, Math.round(img.width * scale));
      const h = Math.max(1, Math.round(img.height * scale));
      const canvas = document.createElement("canvas");
      canvas.width = w;
      canvas.height = h;
      canvas.getContext("2d").drawImage(img, 0, 0, w, h);
      resolve({ dataUrl: canvas.toDataURL("image/jpeg", quality), w, h });
    };
    img.onerror = () => resolve(null);
    img.src = photo.dataUrl;
  });
}

// The PDF as a Blob under DA_PDF_MAX_BYTES. Photos are what make a PDF big, so
// when the first build is over the limit they are re-encoded smaller, from the
// originals each time (re-compressing a compressed JPEG compounds the loss), and
// the PDF is built again. The Blob measured is the one returned: calling
// doc.output() twice on one document gives a different, larger file.
async function daPdfBlob(values, sigs, photos, logo, docId) {
  let blob = daBuildPdf(values, sigs, photos, logo, docId).output("blob");
  if (blob.size <= DA_PDF_MAX_BYTES || !photos.length) return blob;
  for (const [scale, quality] of DA_PDF_PHOTO_STEPS) {
    const smaller = [];
    for (const photo of photos) smaller.push((await daReencodeJpeg(photo, scale, quality)) || photo);
    blob = daBuildPdf(values, sigs, smaller, logo, docId).output("blob");
    if (blob.size <= DA_PDF_MAX_BYTES) break;
  }
  return blob;
}
