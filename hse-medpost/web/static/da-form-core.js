// Drug & Alcohol consent and result form, typed on the Medical Record page
// (Screening > Drug and Alcohol > New drug & alcohol screening). This file holds
// the pure parts: what the form's answers look like, the record sent to the
// server, the check before saving, and the signature wording. da-form-pdf.js
// draws the PDF and da-form.js wires the page. Node tests load this file
// (web/da-form.test.js).
//
// The answers follow forms/da-consent-result-form-*.html, the offline version,
// so a record typed here has the same columns as one uploaded from there.

const DA_FORM_VERSION = "web1";
const DA_PANELS = ["amp", "met", "thc", "coc", "mop", "bzo"];
const DA_MONTHS = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];
const DA_MONTHS_SHORT = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

// The five signatures, in the order they appear on the form, with the answer
// that holds the signer's name.
const DA_SIGNATURES = [
  { key: "donor", nameKey: "donorName", label: "Donor" },
  { key: "witness", nameKey: "witnessName", label: "Witness" },
  { key: "collector", nameKey: "collectorName", label: "Collector" },
  { key: "security", nameKey: "securityName", label: "Witness (Security)" },
  { key: "ack", nameKey: "ackName", label: "Donor (acknowledged result)" },
];

// Answers the form will not save without (the signature is a click, checked
// separately). `label` is what the person sees in the message.
const DA_REQUIRED = [
  { key: "donorName", label: "Donor name" },
  { key: "donorCompany", label: "Company" },
  { key: "donorPosition", label: "Position" },
  { key: "testType", label: "Test type" },
  { key: "consentDt", label: "Consent date and time" },
];

const DA_TEST_TYPES = [
  { value: "random", en: "Random", id: "Acak" },
  { value: "pre", en: "Pre-employment", id: "Pra-kerja" },
  { value: "post", en: "Post-incident", id: "Pasca-insiden" },
  { value: "suspicion", en: "Reasonable suspicion", id: "Kecurigaan" },
  { value: "return", en: "Return to duty", id: "Kembali bekerja" },
];

// The ten consent statements, English then Indonesian, as printed on the form.
const DA_CONSENT_ITEMS = [
  { en: "Voluntary. I was told the purpose of this test, and I give consent voluntarily, while conscious, with full understanding, and without coercion.", id: "Sukarela. Saya telah dijelaskan tujuan tes ini dan memberikan persetujuan secara sukarela, dalam keadaan sadar, memahami isi formulir ini, dan tanpa paksaan." },
  { en: "Specimen. I agree to give breath, urine and/or blood specimens to the company's appointed medical team.", id: "Spesimen. Saya setuju memberikan spesimen napas, urin dan/atau darah kepada tim medis yang ditunjuk perusahaan." },
  { en: "Authentic. My specimen is my own, not diluted, mixed or swapped.", id: "Keaslian. Spesimen saya adalah milik saya, tidak diencerkan, dicampur atau ditukar." },
  { en: "Release. Results may be given to Management, HR, my supervisor, related contractor and, where required by law, the authorities.", id: "Pengungkapan. Hasil dapat diberikan kepada Manajemen, HR, atasan, kontraktor terkait dan, bila diwajibkan hukum, pihak berwajib." },
  { en: "Confidentiality. My test results and health data are kept confidential and used only for this program.", id: "Kerahasiaan. Hasil tes dan data kesehatan saya dijaga kerahasiaannya dan hanya dipakai untuk program ini." },
  { en: "Right to confirm. If screening is non-negative, I may request a confirmation test at an accredited laboratory.", id: "Hak konfirmasi. Jika skrining non-negatif, saya berhak meminta tes konfirmasi di laboratorium terakreditasi." },
  { en: "Refusal. Refusing, delaying without valid reason, or tampering is treated as a positive result under company regulation.", id: "Penolakan. Menolak, menunda tanpa alasan sah, atau mengakali tes diperlakukan sebagai hasil positif sesuai peraturan perusahaan." },
  { en: "Medication. The medicine I declare below is complete and true.", id: "Obat. Obat yang saya nyatakan di bawah ini lengkap dan benar." },
  { en: "Understanding. I have read, or had read to me, this form and had the chance to ask questions.", id: "Pemahaman. Saya telah membaca atau dibacakan formulir ini dan diberi kesempatan bertanya." },
  { en: "Condition at signing. If I cannot sign because of my condition, the medical team will record this and the test will follow company regulation.", id: "Kondisi saat penandatanganan. Jika saya tidak mampu menandatangani karena kondisi saya, tim medis akan mencatatnya dan tes mengikuti peraturan perusahaan." },
];

function daPad2(n) {
  return String(n).padStart(2, "0");
}

// The form with nothing answered. Panels hold one true/false per drug.
function daBlankValues() {
  const panel = () => Object.fromEntries(DA_PANELS.map((p) => [p, false]));
  return {
    donorName: "", donorNik: "", donorCompany: "", donorPosition: "", testType: "", consentDt: "",
    witnessName: "", medNone: false,
    med1Name: "", med1Dose: "", med1Reason: "", med1Presc: "",
    med2Name: "", med2Dose: "", med2Reason: "", med2Presc: "",
    breathDevice: "", breathCalib: "",
    b1Time: "", b1Read: "", b1Mouth: false, b1Res: "",
    b2Time: "", b2Read: "", b2Mouth: false, b2Res: "",
    specTemp: false, specColour: false, specTempVal: "",
    pneg: panel(), pnonneg: panel(),
    finalRes: "", remarks: "",
    collectorName: "", securityName: "", ackName: "",
  };
}

// What stops a save, as the labels of the missing answers; [] when it can be
// saved. `signed` says which signatures have been approved.
function daMissing(values, signed) {
  const missing = DA_REQUIRED.filter((r) => !String(values[r.key] || "").trim()).map((r) => r.label);
  if (!(signed || {}).donor) missing.push("Donor signature");
  return missing;
}

// The first word of a name, as it is written in the signature: "budi santoso"
// signs as "Budi". A leading title ("dr.", "H.") is skipped.
function daFirstName(name) {
  const titles = new Set(["dr", "dr.", "drg", "drg.", "ir", "ir.", "h", "h.", "hj", "hj.", "mr", "mr.", "mrs", "mrs.", "ns", "ns."]);
  const words = String(name || "").trim().split(/\s+/).filter(Boolean);
  const word = words.find((w) => !titles.has(w.toLowerCase())) || words[0] || "";
  return word.charAt(0).toUpperCase() + word.slice(1);
}

// "07 Oct 2026, 08:55 UTC+9" in the browser's own zone: the stamp printed on a signature.
function daStampText(date) {
  const off = -date.getTimezoneOffset();
  const abs = Math.abs(off);
  const zone = "UTC" + (off >= 0 ? "+" : "-") + Math.floor(abs / 60) + (abs % 60 ? ":" + daPad2(abs % 60) : "");
  return (
    daPad2(date.getDate()) + " " + DA_MONTHS_SHORT[date.getMonth()] + " " + date.getFullYear() +
    ", " + daPad2(date.getHours()) + ":" + daPad2(date.getMinutes()) + " " + zone
  );
}

// "07 October 2026" for a YYYY-MM-DD day; anything else comes back unchanged.
function daFormatDateLong(text) {
  const parts = String(text || "").split("-");
  if (parts.length !== 3) return text || "";
  const month = DA_MONTHS[Number(parts[1]) - 1];
  if (!month) return text;
  return daPad2(Number(parts[2])) + " " + month + " " + parts[0];
}

// "07 October 2026 08:55" for a datetime-local value; "" when it is not one.
function daFormatDateTime(text) {
  if (!text || !String(text).includes("T")) return "";
  const [day, time] = String(text).split("T");
  return daFormatDateLong(day) + " " + time.slice(0, 5);
}

// A datetime-local value (YYYY-MM-DDTHH:MM) for a moment, in the browser's own zone.
function daLocalDateTime(date) {
  return (
    date.getFullYear() + "-" + daPad2(date.getMonth() + 1) + "-" + daPad2(date.getDate()) +
    "T" + daPad2(date.getHours()) + ":" + daPad2(date.getMinutes())
  );
}

// The record the server stores: the same keys as the offline form's upload row.
// The donor signs under the donor's name, so donor_sign_name is the donor name.
function daRecord(values, id, now) {
  const text = (key) => String(values[key] || "").trim();
  const panel = (map) => Object.fromEntries(DA_PANELS.map((p) => [p, !!(map || {})[p]]));
  return {
    id,
    created_at: now.toISOString(),
    form_version: DA_FORM_VERSION,
    donor_name: text("donorName"),
    donor_nik: text("donorNik"),
    donor_company: text("donorCompany"),
    donor_position: text("donorPosition"),
    consent_at: text("consentDt"),
    donor_sign_name: text("donorName"),
    witness_name: text("witnessName"),
    test_type: text("testType"),
    med_none: !!values.medNone,
    med1_name: text("med1Name"),
    med1_dose: text("med1Dose"),
    med1_reason: text("med1Reason"),
    med1_prescribed: text("med1Presc"),
    med2_name: text("med2Name"),
    med2_dose: text("med2Dose"),
    med2_reason: text("med2Reason"),
    med2_prescribed: text("med2Presc"),
    breath_device: text("breathDevice"),
    breath_calibration_until: text("breathCalib"),
    breath1_time: text("b1Time"),
    breath1_reading: text("b1Read"),
    breath1_mouthpiece_sealed: !!values.b1Mouth,
    breath1_result: text("b1Res"),
    breath2_time: text("b2Time"),
    breath2_reading: text("b2Read"),
    breath2_mouthpiece_sealed: !!values.b2Mouth,
    breath2_result: text("b2Res"),
    specimen_temp_ok: !!values.specTemp,
    specimen_colour_ok: !!values.specColour,
    specimen_temp_value: text("specTempVal"),
    panel_negative: panel(values.pneg),
    panel_nonnegative: panel(values.pnonneg),
    final_result: text("finalRes"),
    remarks: text("remarks"),
    collector_name: text("collectorName"),
    security_name: text("securityName"),
    ack_name: text("ackName"),
  };
}

// The name for the PDF file: da-consent-result-<donor>-<yyyymmdd-hhmmss>.pdf
function daPdfName(donorName, date) {
  const slug = String(donorName || "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 40);
  const stamp =
    date.getFullYear() + daPad2(date.getMonth() + 1) + daPad2(date.getDate()) + "-" +
    daPad2(date.getHours()) + daPad2(date.getMinutes()) + daPad2(date.getSeconds());
  return "da-consent-result" + (slug ? "-" + slug : "") + "-" + stamp + ".pdf";
}
