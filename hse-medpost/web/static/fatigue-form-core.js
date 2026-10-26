// Fatigue scoring form, typed on the Medical Record page (Screening > Fatigue >
// New fatigue screening). The pure parts: the scoring rules, the risk bands, the
// check before saving and the record sent to the server. fatigue-form-pdf.js
// draws the PDF and fatigue-form.js wires the page. Node tests load this file
// (web/fatigue-form.test.js). The rules are those of the offline form
// (forms/fatigue-scoring-form-*.html, IKT-LNG form-OH-007).

const FA_FORM_VERSION = "web1";
const FA_THRESH_X = 6;
const FA_THRESH_Y = 12;
const FA_THRESH_Z = 14;
const FA_POINTS_X = 4;
const FA_POINTS_Y = 2;
const FA_POINTS_Z = 1;

// Allowed range of each sleep / wake hours answer: [min, max].
const FA_HOUR_RANGES = { x: [0, 20], y: [0, 30], z: [2, 24] };
const FA_BP_RANGES = { sys: [60, 250], dia: [40, 150], pulse: [30, 220] };

// Risk bands. A total of exactly 3 is Mild; Medium starts above 3.
const FA_BANDS = [
  { risk: "Low", points: "0 points", color: "#2e7d32", tint: "#e8f5e9", id: "Tidak ada tindakan kecuali ada gejala fatigue seperti lengah, terdistraksi, dll", en: "No action required unless the person exhibits fatigue symptoms e.g. forgetfulness, distraction" },
  { risk: "Mild", points: "1-3 points", color: "#b28704", tint: "#fff8e1", id: "Dicatat dan Swa-monitor dengan pengurangan resiko seperti rotasi tugas, konsumsi kafein dalam jumlah wajar, Swa rutin cek", en: "Document and work with self monitored risk reduction measures e.g. task rotation, strategic use of caffeine, self-check routines" },
  { risk: "Medium", points: "More than 3 to 7 points", color: "#e65100", tint: "#fff3e0", id: "Dicatat dan supervisi lokal dan bekerja dengan pengawasan eksternal dengan mengurangi resiko berupa re-alokasi tugas, tidur siang, peningkatan pengawasan", en: "Document and work with local supervisor and externally monitored risk reduction measures e.g. task re-allocation, napping, increased supervisory monitoring" },
  { risk: "High", points: "8 or more points", color: "#c62828", tint: "#ffebee", id: "Dicatat dan dihentikan dari safety-critical tasks. Direkomendasikan tidak bekerja untuk istirahat sampai tercapai batas ambang tidur dan bangun", en: "Document and do not engage in safety-critical tasks. Better not recommence work until sufficiently rested as per sleep and wake threshold values" },
];

// The two signatures, with the answer holding the signer's name.
const FA_SIGNATURES = [
  { key: "worker", nameKey: "name", label: "Worker" },
  { key: "assessor", nameKey: "examiner", label: "Examiner" },
];

const FA_RELEASE_EN = "I voluntarily consent to the release of this fatigue assessment (blood pressure, pulse, sleep/wake hours and fatigue score) to PT. Layar Nusantara Gas and its authorised HSE personnel, solely for fitness-for-work and safety purposes on the project.";
const FA_RELEASE_ID = "Saya dengan sukarela menyetujui pemberian hasil penilaian kelelahan ini (tekanan darah, nadi, jam tidur/bangun dan skor kelelahan) kepada PT. Layar Nusantara Gas dan petugas HSE yang berwenang, semata-mata untuk keperluan kelayakan kerja dan keselamatan pada proyek.";

const faRound1 = (v) => Math.round(v * 10) / 10;
const faPointsX = (x) => faRound1(x < FA_THRESH_X ? (FA_THRESH_X - x) * FA_POINTS_X : 0);
const faPointsY = (y) => faRound1(y < FA_THRESH_Y ? (FA_THRESH_Y - y) * FA_POINTS_Y : 0);
const faPointsZ = (z) => faRound1(z > FA_THRESH_Z ? (z - FA_THRESH_Z) * FA_POINTS_Z : 0);

// The points for each answer and their total, or null until all three hours are given.
function faScore(x, y, z) {
  if ([x, y, z].some((v) => v === null || v === undefined || Number.isNaN(v))) return null;
  const px = faPointsX(x);
  const py = faPointsY(y);
  const pz = faPointsZ(z);
  return { x: px, y: py, z: pz, total: faRound1(px + py + pz) };
}

// Index into FA_BANDS for a total score.
function faBandIndex(total) {
  if (total <= 0) return 0;
  if (total <= 3) return 1;
  if (total <= 7) return 2;
  return 3;
}

// Blood pressure: "high" above 160 / 100, "elevated" above 140 / 90, else "".
function faBpLevel(sys, dia) {
  if ((sys !== null && sys > 160) || (dia !== null && dia > 100)) return "high";
  if ((sys !== null && sys > 140) || (dia !== null && dia > 90)) return "elevated";
  return "";
}

// "Budi  santoso" -> "Budi Santoso".
function faCleanName(value) {
  return String(value || "")
    .trim()
    .replace(/\s+/g, " ")
    .split(" ")
    .map((w) => (w ? w.charAt(0).toUpperCase() + w.slice(1).toLowerCase() : w))
    .join(" ");
}

// A number box's text as a number, or null when blank.
function faNumber(text) {
  const t = String(text === null || text === undefined ? "" : text).trim();
  return t === "" ? null : Number(t);
}

function faBlankValues() {
  return {
    examiner: "", name: "", company: "", position: "", date: "", time: "",
    sys: null, dia: null, pulse: null, healthy: "", medicine: "", medicineName: "",
    x: null, y: null, z: null, remarks: "",
  };
}

// What is wrong or missing, as messages; [] when the form can be saved.
// `signed` says which signatures were approved.
function faProblems(values, signed) {
  const problems = [];
  const required = [["examiner", "Examiner"], ["name", "Worker name"], ["company", "Company"], ["position", "Position"]];
  for (const [key, label] of required) if (!String(values[key] || "").trim()) problems.push(label);
  const hours = [["x", "X (slept in the last 24 hours)"], ["y", "Y (slept in the last 48 hours)"], ["z", "Z (awake since waking up)"]];
  for (const [key, label] of hours) if (values[key] === null) problems.push(label);
  const body = [["sys", "Systolic BP"], ["dia", "Diastolic BP"], ["pulse", "Pulse"]];
  for (const [key, label] of body) if (values[key] === null) problems.push(label);
  if (!values.healthy) problems.push("Feeling healthy today?");
  if (!values.medicine) problems.push("Taking medicine?");
  if (!(signed || {}).worker) problems.push("Worker signature");
  if (!(signed || {}).assessor) problems.push("Examiner signature");
  if (problems.length > 0) return problems;
  return faRangeProblems(values);
}

// Out-of-range answers and the X/Y inconsistency, as messages; [] when fine.
function faRangeProblems(values) {
  const out = [];
  for (const [key, [min, max]] of Object.entries(FA_HOUR_RANGES)) {
    const v = values[key];
    if (v !== null && (Number.isNaN(v) || v < min || v > max)) out.push(key.toUpperCase() + " must be a number from " + min + " to " + max);
  }
  if (values.x !== null && values.y !== null && values.y < values.x) out.push("The 48-hour value (Y) cannot be less than the 24-hour value (X)");
  for (const [key, [min, max]] of Object.entries(FA_BP_RANGES)) {
    const v = values[key];
    if (v !== null && (Number.isNaN(v) || v < min || v > max)) out.push("Check the " + { sys: "systolic", dia: "diastolic", pulse: "pulse" }[key] + " value (" + min + " to " + max + ")");
  }
  return out;
}

// The record the server stores: the same keys as the offline form's upload row.
function faRecord(values, id, now) {
  const score = faScore(values.x, values.y, values.z);
  const band = score ? FA_BANDS[faBandIndex(score.total)] : null;
  const text = (key) => String(values[key] || "").trim();
  return {
    id,
    created_at: now.toISOString(),
    form_version: FA_FORM_VERSION,
    examiner: faCleanName(values.examiner),
    name: faCleanName(values.name),
    company: text("company"),
    position: text("position"),
    date: text("date"),
    time: text("time"),
    bp_systolic: values.sys,
    bp_diastolic: values.dia,
    pulse: values.pulse,
    feels_healthy: values.healthy,
    takes_medicine: values.medicine,
    medicine_name: text("medicineName"),
    sleep_24h: values.x,
    sleep_48h: values.y,
    awake_hours: values.z,
    score_24h: score ? score.x : null,
    score_48h: score ? score.y : null,
    score_awake: score ? score.z : null,
    score_total: score ? score.total : null,
    risk_level: band ? band.risk : null,
    risk_points: band ? band.points : null,
    recommendation_en: band ? band.en : null,
    recommendation_id: band ? band.id : null,
    remarks: text("remarks"),
  };
}

// fatigue-scoring-<worker>-<yyyymmdd-hhmmss>.pdf
function faPdfName(workerName, date) {
  const slug = String(workerName || "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 40);
  const p = (n) => String(n).padStart(2, "0");
  const stamp = date.getFullYear() + p(date.getMonth() + 1) + p(date.getDate()) + "-" + p(date.getHours()) + p(date.getMinutes()) + p(date.getSeconds());
  return "fatigue-scoring" + (slug ? "-" + slug : "") + "-" + stamp + ".pdf";
}
