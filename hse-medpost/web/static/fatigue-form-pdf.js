// The fatigue scoring sheet as a PDF: one A4 page per worker, plus a photo page
// when photos were added. Ported from drawWorkerPage() in
// forms/fatigue-scoring-form-*.html so the printed sheet looks the same. Reads a
// plain record (see faPdfRecord in fatigue-form.js), and borrows the stamp, the
// photo re-encoder and the size limit from da-form-pdf.js, which loads first.

const FA_NAVY = [31, 56, 100];
const FA_NAVY_LIGHT = [220, 230, 241];
const FA_GRID = [185, 196, 208];
const FA_MUTED = [90, 90, 90];

function faHexRgb(hex) {
  const n = parseInt(hex.slice(1), 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

// `rec`: { examiner, name, company, position, date, time, sys, dia, pulse, healthy, medicine,
// medicineName, x, y, z, score, risk, remarks, signatures: {worker, assessor} (PNG data URLs),
// sigDates: {worker, assessor} }. `photos` as for daBuildPdf.
function faBuildPdf(rec, photos, logo, docId) {
  const doc = new window.jspdf.jsPDF({ unit: "pt", format: "a4", orientation: "portrait", compress: true });
  const PW = doc.internal.pageSize.getWidth();
  const PH = doc.internal.pageSize.getHeight();
  const M = 30;
  const W = PW - 2 * M;
  let y = 24;

  function setText(size, style, color) {
    doc.setFont("helvetica", style || "normal");
    doc.setFontSize(size);
    doc.setTextColor(...(color || [0, 0, 0]));
  }
  function box(x, yy, w, h, fill) {
    if (fill) {
      doc.setFillColor(...fill);
      doc.rect(x, yy, w, h, "F");
    }
    doc.setDrawColor(...FA_GRID);
    doc.setLineWidth(0.5);
    doc.rect(x, yy, w, h, "S");
  }
  function labelCell(x, yy, w, h, label, sub) {
    box(x, yy, w, h, FA_NAVY_LIGHT);
    setText(7.6, "bold", FA_NAVY);
    doc.text(label, x + 4, yy + h / 2 + 2.5);
    if (sub) {
      const lw = doc.getTextWidth(label + " ");
      setText(6.4, "italic", FA_MUTED);
      doc.text(sub, x + 4 + lw, yy + h / 2 + 2.5);
    }
  }
  function valueCell(x, yy, w, h, value, color, bold) {
    box(x, yy, w, h);
    setText(8, bold ? "bold" : "normal", color || [0, 0, 0]);
    doc.text(String(value || ""), x + 4, yy + h / 2 + 2.5);
  }
  function sectionHead(yy, letter, title, sub) {
    setText(9.5, "bold", FA_NAVY);
    doc.text(letter + "  " + title, M, yy + 8);
    const tw = doc.getTextWidth(letter + "  " + title);
    setText(7.5, "italic", FA_MUTED);
    doc.text(" / " + sub, M + tw, yy + 8);
    doc.setDrawColor(...FA_NAVY);
    doc.setLineWidth(1);
    doc.line(M, yy + 11, PW - M, yy + 11);
    return yy + 16;
  }
  function headRow(x, yy, widths, heads, h) {
    let cx = x;
    heads.forEach((text, i) => {
      doc.setFillColor(...FA_NAVY);
      doc.rect(cx, yy, widths[i], h, "F");
      setText(6.8, "bold", [255, 255, 255]);
      const lines = doc.splitTextToSize(text, widths[i] - 6);
      doc.text(lines, cx + widths[i] / 2, yy + h / 2 - (lines.length - 1) * 3 + 2.5, { align: "center" });
      cx += widths[i];
    });
    return yy + h;
  }

  // Header
  const FORM_W = 128;
  const titleX = M + 60 + (W - 60 - FORM_W) / 2;
  box(M, y, W, 54);
  if (logo) doc.addImage(logo, "PNG", M + 10, y + 6, 32, 42);
  setText(11.5, "bold", FA_NAVY);
  doc.text("FATIGUE SCORING", titleX, y + 20, { align: "center" });
  setText(7.5, "italic", FA_MUTED);
  doc.text("Penilaian Kelelahan", titleX, y + 32, { align: "center" });
  setText(8, "bold");
  doc.text("PT. Layar Nusantara Gas", titleX, y + 44, { align: "center" });
  doc.setDrawColor(...FA_GRID);
  doc.rect(M + W - FORM_W - 8, y + 6, FORM_W, 42, "S");
  setText(7);
  doc.text("Form No: IKT-LNG form-OH-007", M + W - FORM_W - 2, y + 17);
  doc.text("Rev: 00", M + W - FORM_W - 2, y + 28);
  doc.text("Doc ID: " + String(docId || "").slice(0, 8), M + W - FORM_W - 2, y + 39);
  y += 54;

  // A. Worker information
  y = sectionHead(y + 6, "A.", "Worker information", "Informasi pekerja");
  const PAIR = W / 2;
  const LW = 110;
  const pair = (x, yy, label, sub, value) => {
    labelCell(x, yy, LW, 16, label, sub);
    valueCell(x + LW, yy, PAIR - LW, 16, value);
  };
  pair(M, y, "Pemeriksa", "Examiner", rec.examiner);
  pair(M + PAIR, y, "Name", "Nama", rec.name);
  y += 16;
  pair(M, y, "Date", "Tanggal", daFormatDateLong(rec.date));
  pair(M + PAIR, y, "Company", "Perusahaan", rec.company);
  y += 16;
  pair(M, y, "Time", "Jam", rec.time);
  pair(M + PAIR, y, "Position", "Jabatan", rec.position);
  y += 16 + 6;

  // B. Physical examination
  y = sectionHead(y, "B.", "Physical examination", "Pemeriksaan fisik");
  const LBL = 210;
  const bp = faBpLevel(rec.sys, rec.dia);
  const bpColors = { high: "#c62828", elevated: "#e65100" };
  const yesNo = (v) => (v === "Y" ? "Yes / Ya" : v === "N" ? "No / Tidak" : "");
  const exam = [
    ["Blood pressure (BP) / Tekanan darah", rec.sys !== null && rec.dia !== null ? rec.sys + " / " + rec.dia + " mmHg" : "", bp ? faHexRgb(bpColors[bp]) : null, Boolean(bp)],
    ["Pulse / Nadi", rec.pulse !== null ? rec.pulse + " bpm" : ""],
    ["Feeling healthy today? / Merasa sehat hari ini?", yesNo(rec.healthy)],
    ["Taking medicine? / Minum obat?", yesNo(rec.medicine) + (rec.medicine === "Y" && rec.medicineName ? " - " + rec.medicineName : "")],
  ];
  for (const [label, value, color, bold] of exam) {
    labelCell(M, y, LBL, 16, label);
    valueCell(M + LBL, y, W - LBL, 16, value, color, bold);
    y += 16;
  }
  y += 6;

  // C. Sleep and wake score
  y = sectionHead(y, "C.", "Sleep and wake score", "Skor jam tidur dan bangun");
  const cw = [200, 62, 175, 48, 50];
  y = headRow(M, y, cw, ["Sleep / wake hours / Jam tidur / bangun", "Threshold / Ambang", "Calculation / Rumus", "Actual (hour) / Aktual (jam)", "Score / Skor"], 26);
  const s = rec.score || { x: "—", y: "—", z: "—", total: "—" };
  const rows = [
    { label: "X = Hours slept in the last 24 hours", sub: "Jumlah jam tidur dalam 24 jam terakhir", th: FA_THRESH_X, calc: "Add 4 points for every hour below threshold", calcId: "Tambah 4 poin untuk tiap 1 jam di bawah nilai ambang", actual: rec.x, score: s.x },
    { label: "Y = Hours slept in the last 48 hours", sub: "Jumlah jam tidur dalam 48 jam terakhir", th: FA_THRESH_Y, calc: "Add 2 points for every hour below threshold", calcId: "Tambah 2 poin untuk tiap 1 jam di bawah nilai ambang", actual: rec.y, score: s.y },
    { label: "Z = Hours awake since you woke up", sub: "Jumlah jam setelah bangun", th: FA_THRESH_Z, calc: "Add 1 point for every hour of wakefulness greater than threshold", calcId: "Tambah 1 poin untuk tiap 1 jam di atas nilai ambang", actual: rec.z, score: s.z },
  ];
  for (const r of rows) {
    const rh = 32;
    let cx = M;
    const calcLines = doc.splitTextToSize(r.calc, cw[2] - 8).length;
    box(cx, y, cw[0], rh);
    setText(7.4, "bold");
    doc.text(doc.splitTextToSize(r.label, cw[0] - 8), cx + 4, y + 10);
    setText(6.2, "italic", FA_MUTED);
    doc.text(r.sub, cx + 4, y + 25);
    cx += cw[0];
    box(cx, y, cw[1], rh);
    setText(9, "bold");
    doc.text(String(r.th), cx + cw[1] / 2, y + rh / 2 + 3, { align: "center" });
    cx += cw[1];
    box(cx, y, cw[2], rh);
    setText(6.4);
    doc.text(doc.splitTextToSize(r.calc, cw[2] - 8), cx + 4, y + 9);
    setText(6, "italic", FA_MUTED);
    doc.text(doc.splitTextToSize(r.calcId, cw[2] - 8), cx + 4, y + 9 + calcLines * 7 + 3);
    cx += cw[2];
    box(cx, y, cw[3], rh);
    setText(8.5, "bold");
    doc.text(r.actual === null || r.actual === undefined ? "" : String(r.actual), cx + cw[3] / 2, y + rh / 2 + 3, { align: "center" });
    cx += cw[3];
    box(cx, y, cw[4], rh);
    setText(8.5, "bold", FA_NAVY);
    doc.text(String(r.score), cx + cw[4] / 2, y + rh / 2 + 3, { align: "center" });
    y += rh;
  }
  box(M, y, W - cw[4], 18, FA_NAVY_LIGHT);
  setText(7.8, "bold", FA_NAVY);
  doc.text("Total fatigue score (X+Y+Z) / Total skor kelelahan", M + 6, y + 12);
  box(M + W - cw[4], y, cw[4], 18);
  setText(9, "bold", FA_NAVY);
  doc.text(String(s.total), M + W - cw[4] / 2, y + 12.5, { align: "center" });
  y += 18 + 6;

  // D. Control measures
  y = sectionHead(y, "D.", "Control measures", "Tindakan berikutnya");
  const bw = [110, W - 110];
  y = headRow(M, y, bw, ["Risk / Risiko", "Action / Tindakan"], 14);
  const activeIdx = rec.risk ? FA_BANDS.findIndex((b) => b.risk === rec.risk.level) : -1;
  FA_BANDS.forEach((b, i) => {
    const on = i === activeIdx;
    setText(6.4, "bold");
    const idLines = doc.splitTextToSize(b.id, bw[1] - 10);
    setText(5.9, "italic");
    const enLines = doc.splitTextToSize("(" + b.en + ")", bw[1] - 10);
    const bh = Math.max(24, 8 + idLines.length * 7.5 + enLines.length * 6.5 + 4);
    box(M, y, bw[0], bh, on ? FA_NAVY_LIGHT : undefined);
    box(M + bw[0], y, bw[1], bh, on ? FA_NAVY_LIGHT : undefined);
    setText(7.6, "bold", on ? faHexRgb(b.color) : [0, 0, 0]);
    doc.text(doc.splitTextToSize(b.risk + " (" + b.points + ")", bw[0] - 8), M + 4, y + bh / 2 - 1);
    setText(6.4, "bold");
    doc.text(idLines, M + bw[0] + 4, y + 8);
    setText(5.9, "italic", FA_MUTED);
    doc.text(enLines, M + bw[0] + 4, y + 8 + idLines.length * 7.5 + 1);
    y += bh;
  });
  y += 6;

  // E. Decision and remarks
  y = sectionHead(y, "E.", "Decision", "Keputusan");
  const dh = 56;
  labelCell(M, y, 110, dh, "Control measure", "Tindakan");
  box(M + 110, y, W - 110, dh);
  setText(9, "bold", rec.risk ? faHexRgb(rec.risk.color) : FA_NAVY);
  doc.text("Risk level: " + (rec.risk ? rec.risk.level : "—"), M + 118, y + 20);
  setText(8);
  doc.text("Total fatigue score: " + s.total + " point(s)", M + 118, y + 34);
  if (rec.risk) daDrawSignedStamp(doc, M + W - 100, y + dh / 2, -16);
  y += dh;
  labelCell(M, y, 110, 30, "Remarks", "Catatan");
  box(M + 110, y, W - 110, 30);
  setText(8);
  doc.text(doc.splitTextToSize(rec.remarks || "", W - 118).slice(0, 2), M + 114, y + 12);
  y += 30 + 8;

  // F. Signatures
  y = sectionHead(y, "F.", "Signatures", "Tanda tangan");
  setText(7);
  const relEn = doc.splitTextToSize(FA_RELEASE_EN, W - 8);
  setText(6.8, "italic", FA_MUTED);
  const relId = doc.splitTextToSize(FA_RELEASE_ID, W - 8);
  const noteH = (relEn.length + relId.length) * 8 + 8;
  box(M, y, W, noteH);
  setText(7);
  doc.text(relEn, M + 4, y + 9);
  setText(6.8, "italic", FA_MUTED);
  doc.text(relId, M + 4, y + 9 + relEn.length * 8);
  y += noteH + 4;
  const sw = W / 2;
  doc.setFillColor(...FA_NAVY);
  doc.rect(M, y, sw, 13, "F");
  doc.rect(M + sw, y, sw, 13, "F");
  setText(7, "bold", [255, 255, 255]);
  doc.text("Worker / Pekerja", M + sw / 2, y + 9, { align: "center" });
  doc.text("Pemeriksa / Examiner", M + sw * 1.5, y + 9, { align: "center" });
  y += 13;
  const SIG_H = 40;
  FA_SIGNATURES.forEach((def, i) => {
    const x = M + i * sw;
    box(x, y, sw, SIG_H);
    const data = (rec.signatures || {})[def.key];
    if (!data) return;
    const props = doc.getImageProperties(data);
    const sc = Math.min((sw - 10) / props.width, (SIG_H - 8) / props.height);
    const iw = props.width * sc;
    const ih = props.height * sc;
    const imgY = y + (SIG_H - ih) / 2;
    doc.addImage(data, "PNG", x + (sw - iw) / 2, imgY, iw, ih, undefined, "FAST");
    const stamp = (rec.sigDates || {})[def.key];
    if (stamp) {
      setText(6, "bold", [200, 30, 30]);
      doc.text(stamp, x + sw / 2, imgY + ih * 0.9, { align: "center" });
    }
  });
  y += SIG_H;
  valueCell(M, y, sw, 15, "Worker name / Nama pekerja: " + rec.name);
  valueCell(M + sw, y, sw, 15, "Pemeriksa / Examiner: " + rec.examiner);
  y += 15;
  setText(6.5, "italic", FA_MUTED);
  doc.text("Fatigue scoring · web entry " + FA_FORM_VERSION, M, y + 12);

  // Photos, only when at least one was added: two per row.
  if (photos && photos.length) {
    doc.addPage();
    y = M + 6;
    setText(11, "bold", FA_NAVY);
    doc.text("Photos / Foto", M, y + 4);
    doc.setDrawColor(...FA_NAVY);
    doc.setLineWidth(1.2);
    doc.line(M, y + 9, PW - M, y + 9);
    y += 20;
    const cellW = (W - 12) / 2;
    const cellH = 165;
    photos.forEach((ph, i) => {
      const col = i % 2;
      if (col === 0 && y + cellH > PH - 50) {
        doc.addPage();
        y = M;
      }
      const x = M + col * (cellW + 12);
      box(x, y, cellW, cellH);
      const sc = Math.min((cellW - 8) / ph.w, (cellH - 8) / ph.h);
      doc.addImage(ph.dataUrl, "JPEG", x + (cellW - ph.w * sc) / 2, y + (cellH - ph.h * sc) / 2, ph.w * sc, ph.h * sc);
      if (col === 1) y += cellH + 12;
    });
  }
  return doc;
}

// The PDF as a Blob under DA_PDF_MAX_BYTES, shrinking photos from the originals
// until it fits (see daPdfBlob for why the measured Blob is the one returned).
async function faPdfBlob(rec, photos, logo, docId) {
  let blob = faBuildPdf(rec, photos, logo, docId).output("blob");
  if (blob.size <= DA_PDF_MAX_BYTES || !photos.length) return blob;
  for (const [scale, quality] of DA_PDF_PHOTO_STEPS) {
    const smaller = [];
    for (const photo of photos) smaller.push((await daReencodeJpeg(photo, scale, quality)) || photo);
    blob = faBuildPdf(rec, smaller, logo, docId).output("blob");
    if (blob.size <= DA_PDF_MAX_BYTES) break;
  }
  return blob;
}
