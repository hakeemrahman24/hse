// Fatigue scoring form on the Medical Record page. The "New fatigue screening"
// button in the Fatigue table switches the page to this form (toolbar and table
// hidden, as for the malaria and drug & alcohol forms); Cancel switches back.
// Save stores the record (POST /api/screening-log/fatigue) and clears the form;
// Save PDF makes the one-page sheet of the offline form, under 1 MB.
//
// Loaded after da-form.js, whose helpers it reuses (daSignatureImage, daResizePhoto,
// daLoadScript, daReadAsDataUrl, daStampText, daFirstName, daLocalDateTime) along
// with malaria.js (createSearchPicker, matchPatients, patientHint) and date-dmy.js.
// The scoring rules and the record are in fatigue-form-core.js.

function initFatigueForm() {
  const form = document.getElementById("fa-form");
  if (!form) return;
  const $ = (id) => document.getElementById(id);
  const view = $("scr-view-table");
  const newButton = $("scr-new-fatigue");
  const message = $("fa-msg");
  const buttons = { save: $("fa-save"), pdf: $("fa-pdf") };
  const NUMBER_KEYS = ["sys", "dia", "pulse", "x", "y", "z"];

  let recordId = "";
  let baseline = "";
  let busy = false;
  let sigs = {}; // key -> { png, stamp, name }
  let photos = [];
  let patients = null;
  let logo = null;
  const pickers = {};

  async function getJSON(url, options) {
    const response = await fetch(url, { credentials: "same-origin", ...options });
    let body = null;
    try {
      body = await response.json();
    } catch {
      /* an empty or non-JSON body is reported by status below */
    }
    if (response.status === 401) {
      location.href = "/";
      throw new Error("Signed out");
    }
    if (!response.ok) throw new Error((body && body.error) || "Something went wrong (" + response.status + ")");
    return body;
  }

  function say(text, isError) {
    message.textContent = text;
    message.classList.toggle("is-error", Boolean(isError));
  }

  const newId = () => (window.crypto && crypto.randomUUID ? crypto.randomUUID() : Date.now().toString(36) + "-" + Math.random().toString(36).slice(2, 10));
  const text = (key) => $("fa-" + key).value.trim();
  const radio = (name) => {
    const el = form.querySelector('input[name="fa-' + name + '"]:checked');
    return el ? el.value : "";
  };

  function readValues() {
    const values = faBlankValues();
    for (const key of ["examiner", "name", "company", "position", "date", "time", "medicineName", "remarks"]) values[key] = text(key);
    for (const key of NUMBER_KEYS) values[key] = faNumber($("fa-" + key).value);
    values.healthy = radio("healthy");
    values.medicine = radio("medicine");
    return values;
  }

  const signedMap = () => Object.fromEntries(Object.keys(sigs).map((k) => [k, true]));
  const snapshot = () => JSON.stringify([readValues(), Object.keys(sigs), photos.length]);

  // ---- live scores and the risk band -------------------------------------------
  for (const [i, band] of FA_BANDS.entries()) {
    const tr = document.createElement("tr");
    tr.id = "fa-band-" + i;
    const left = document.createElement("td");
    const name = document.createElement("b");
    name.textContent = band.risk;
    name.style.color = band.color;
    left.append(name, document.createElement("br"), Object.assign(document.createElement("i"), { textContent: band.points }));
    const right = document.createElement("td");
    right.append(document.createTextNode(band.id), document.createElement("br"), Object.assign(document.createElement("i"), { textContent: band.en }));
    tr.append(left, right);
    $("fa-band-body").append(tr);
  }

  function renderScores() {
    const values = readValues();
    const score = faScore(values.x, values.y, values.z);
    const show = (id, value) => {
      $(id).textContent = value === null || value === undefined ? "—" : String(value);
    };
    show("fa-score-x", score && score.x);
    show("fa-score-y", score && score.y);
    show("fa-score-z", score && score.z);
    show("fa-score-total", score && score.total);
    const index = score ? faBandIndex(score.total) : -1;
    const label = $("fa-band-label");
    label.textContent = index < 0 ? "—" : FA_BANDS[index].risk + " (" + FA_BANDS[index].points + ")";
    label.style.color = index < 0 ? "" : FA_BANDS[index].color;
    $("fa-recommendation").textContent = index < 0 ? "" : "Recommendation: " + FA_BANDS[index].en + " / Rekomendasi: " + FA_BANDS[index].id;
    FA_BANDS.forEach((band, i) => {
      const row = $("fa-band-" + i);
      row.classList.toggle("is-active", i === index);
      row.style.background = i === index ? band.tint : "";
    });
    const bp = faBpLevel(values.sys, values.dia);
    for (const id of ["fa-sys", "fa-dia"]) {
      $(id).classList.toggle("fa-bp-high", bp === "high");
      $(id).classList.toggle("fa-bp-elevated", bp === "elevated");
    }
    for (const [key, [min, max]] of Object.entries(FA_HOUR_RANGES)) {
      const v = values[key];
      $("fa-" + key).classList.toggle("da-missing", v !== null && (Number.isNaN(v) || v < min || v > max));
    }
  }

  // ---- patient pickers: examiner and worker ------------------------------------
  async function ensurePatients() {
    if (!patients) patients = await getJSON("/api/patients");
    return patients;
  }

  function onNameChosen(nameKey, item) {
    for (const def of FA_SIGNATURES) if (def.nameKey === nameKey && sigs[def.key]) withdraw(def.key);
    if (nameKey !== "name" || !item) return;
    $("fa-company").value = item.patient.companyName || "";
    $("fa-position").value = item.patient.workPosition || "";
  }

  for (const key of ["examiner", "name"]) {
    pickers[key] = createSearchPicker({
      input: $("fa-" + key),
      list: $("fa-" + key + "-list"),
      minChars: 1,
      remote: async (query) => matchPatients(await ensurePatients(), query).map((patient) => ({ name: patient.name, path: patientHint(patient), patient })),
      onChange: (item) => onNameChosen(key, item),
    });
  }

  // ---- signatures: a click approves, the first name in cursive ----------------
  function paintSignature(key) {
    const holder = form.querySelector('.da-sign[data-sign="' + key + '"]');
    const button = holder.querySelector(".da-sign-btn");
    const sig = sigs[key];
    button.classList.toggle("is-approved", Boolean(sig));
    button.textContent = sig ? daFirstName(sig.name) : "Click to approve";
    button.title = sig ? "Click to withdraw the approval" : "";
    holder.querySelector(".da-sign-stamp").textContent = sig ? "Approved " + sig.stamp : "";
  }

  function withdraw(key) {
    delete sigs[key];
    paintSignature(key);
  }

  async function toggleSignature(key) {
    if (sigs[key]) return withdraw(key);
    const def = FA_SIGNATURES.find((d) => d.key === key);
    const name = text(def.nameKey);
    if (!name) {
      $("fa-" + def.nameKey).focus();
      return say("Choose the " + def.label.toLowerCase() + "'s name from the list first.", true);
    }
    try {
      await document.fonts.load("700 48px " + DA_SIGNATURE_FONT, daFirstName(name));
    } catch {
      /* the picture then uses the fallback cursive font */
    }
    sigs[key] = { png: daSignatureImage(daFirstName(name)), stamp: daStampText(new Date()), name };
    paintSignature(key);
    form.querySelector('.da-sign-btn[data-key="' + key + '"]').classList.remove("da-missing");
    say("");
  }

  for (const button of form.querySelectorAll(".da-sign-btn")) button.addEventListener("click", () => toggleSignature(button.dataset.key));

  // ---- photos (kept in the PDF only) ------------------------------------------
  function drawPhotos() {
    const list = $("fa-photo-list");
    list.replaceChildren();
    photos.forEach((photo, index) => {
      const cell = document.createElement("div");
      cell.className = "da-photo";
      const img = document.createElement("img");
      img.src = photo.dataUrl;
      img.alt = "Photo " + (index + 1);
      const remove = document.createElement("button");
      remove.type = "button";
      remove.textContent = "×";
      remove.setAttribute("aria-label", "Remove photo " + (index + 1));
      remove.addEventListener("click", () => {
        photos = photos.filter((_, i) => i !== index);
        drawPhotos();
      });
      cell.append(img, remove);
      list.append(cell);
    });
    $("fa-photo-add").disabled = photos.length >= DA_MAX_PHOTOS;
  }

  $("fa-photo-add").addEventListener("click", () => $("fa-photo-input").click());
  $("fa-photo-input").addEventListener("change", async (event) => {
    const files = [...event.target.files].slice(0, DA_MAX_PHOTOS - photos.length);
    event.target.value = "";
    try {
      const added = [];
      for (const file of files) added.push(await daResizePhoto(file));
      photos = [...photos, ...added];
      drawPhotos();
    } catch (err) {
      say(err.message, true);
    }
  });

  // ---- saving ------------------------------------------------------------------
  const MISSING_BOX = { Examiner: "examiner", "Worker name": "name", Company: "company", Position: "position", "Systolic BP": "sys", "Diastolic BP": "dia", Pulse: "pulse", "Feeling healthy today?": "healthy", "Taking medicine?": "medicine" };

  function markMissing(problems) {
    for (const el of form.querySelectorAll(".da-missing")) el.classList.remove("da-missing");
    let first = null;
    const mark = (el) => {
      el.classList.add("da-missing");
      first = first || el;
    };
    for (const problem of problems) {
      if (MISSING_BOX[problem]) mark($("fa-" + MISSING_BOX[problem]));
      else if (problem.startsWith("X ")) mark($("fa-x"));
      else if (problem.startsWith("Y ")) mark($("fa-y"));
      else if (problem.startsWith("Z ")) mark($("fa-z"));
      else if (problem === "Worker signature") mark(form.querySelector('.da-sign-btn[data-key="worker"]'));
      else if (problem === "Examiner signature") mark(form.querySelector('.da-sign-btn[data-key="assessor"]'));
    }
    if (first) first.scrollIntoView({ block: "center" });
  }
  const clearMissing = (event) => event.target.closest(".da-missing")?.classList.remove("da-missing");
  form.addEventListener("input", (event) => {
    clearMissing(event);
    renderScores();
  });
  form.addEventListener("change", clearMissing);

  function validValues() {
    const values = readValues();
    const problems = faProblems(values, signedMap());
    markMissing(problems);
    if (problems.length > 0) {
      const ranges = problems.every((p) => /must be a number|cannot be less|Check the/.test(p));
      say((ranges ? "Please check: " : "Still needed: ") + problems.join("; ") + ".", true);
      return null;
    }
    return values;
  }

  function setBusy(on) {
    busy = on;
    buttons.save.disabled = on;
    buttons.pdf.disabled = on;
  }

  // The record the PDF draws: the answers, the score, the band and the signatures.
  function pdfRecord(values) {
    const score = faScore(values.x, values.y, values.z);
    const band = score ? FA_BANDS[faBandIndex(score.total)] : null;
    return {
      ...values,
      examiner: faCleanName(values.examiner),
      name: faCleanName(values.name),
      score,
      risk: band ? { level: band.risk, points: band.points, color: band.color } : null,
      signatures: Object.fromEntries(Object.entries(sigs).map(([k, s]) => [k, s.png])),
      sigDates: Object.fromEntries(Object.entries(sigs).map(([k, s]) => [k, s.stamp])),
    };
  }

  async function makePdf(values) {
    if (!window.jspdf) await daLoadScript("/static/vendor/jspdf.min.js");
    if (logo === null) {
      try {
        logo = await daReadAsDataUrl(await (await fetch("/static/da-logo.png", { credentials: "same-origin" })).blob());
      } catch {
        logo = ""; // the sheet is still valid without the logo
      }
    }
    return faPdfBlob(pdfRecord(values), photos, logo, recordId);
  }

  function download(blob, name) {
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = name;
    document.body.append(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 4000);
  }

  const kilobytes = (blob) => Math.max(1, Math.round(blob.size / 1000)) + " KB";

  async function savePdf() {
    const values = validValues();
    if (!values || busy) return;
    setBusy(true);
    say("Building the PDF…");
    try {
      const blob = await makePdf(values);
      download(blob, faPdfName(values.name, new Date()));
      say("PDF saved (" + kilobytes(blob) + ").");
    } catch (err) {
      say("Could not build the PDF: " + err.message, true);
    } finally {
      setBusy(false);
    }
  }

  async function save() {
    const values = validValues();
    if (!values || busy) return;
    setBusy(true);
    say("Saving…");
    try {
      let note = "";
      if ($("fa-save-pdf").checked) {
        const blob = await makePdf(values);
        download(blob, faPdfName(values.name, new Date()));
        note = " PDF downloaded (" + kilobytes(blob) + ").";
      }
      await getJSON("/api/screening-log/fatigue", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(faRecord(values, recordId, new Date())),
      });
      const keepExaminer = pickers.examiner.item;
      resetForm(); // blank form for the next worker
      if (keepExaminer) pickers.examiner.set(keepExaminer); // the examiner stays, as on the offline form
      baseline = snapshot();
      say("Saved for " + faCleanName(values.name) + "." + note);
      if (window.refreshScreening) window.refreshScreening();
    } catch (err) {
      say(err.message, true);
    } finally {
      setBusy(false);
    }
  }

  // ---- the form's two modes ----------------------------------------------------
  function resetForm() {
    form.reset();
    syncDateDisplays(form);
    for (const picker of Object.values(pickers)) picker.reset();
    for (const key of Object.keys(sigs)) withdraw(key);
    photos = [];
    drawPhotos();
    markMissing([]);
    $("fa-save-pdf").checked = true;
    const now = new Date();
    $("fa-date").value = daLocalDateTime(now).slice(0, 10);
    $("fa-date").dispatchEvent(new Event("input", { bubbles: true })); // redraws the dd/mm/yyyy box
    $("fa-time").value = daLocalDateTime(now).slice(11, 16);
    recordId = newId();
    $("fa-docid").textContent = recordId;
    renderScores();
    baseline = snapshot();
    say("");
  }

  const isDirty = () => snapshot() !== baseline;

  function openForm() {
    resetForm();
    view.classList.add("is-form-mode");
    form.hidden = false;
    $("fa-examiner").focus();
  }

  function closeForm(askFirst) {
    if (askFirst && isDirty() && !window.confirm("Discard what was typed in this form?")) return;
    resetForm();
    form.hidden = true;
    view.classList.remove("is-form-mode");
  }

  newButton.addEventListener("click", openForm);
  $("fa-cancel").addEventListener("click", () => closeForm(true));
  $("fa-cancel-top").addEventListener("click", () => closeForm(true));
  $("fa-reset").addEventListener("click", () => {
    if (!isDirty() || window.confirm("Clear everything typed in this form?")) resetForm();
  });
  buttons.save.addEventListener("click", save);
  buttons.pdf.addEventListener("click", savePdf);
  form.addEventListener("submit", (event) => event.preventDefault());

  // The button is for Fatigue only; switching tests closes the form.
  const showScreening = window.loadScreening;
  window.loadScreening = function (kind) {
    const isFatigue = kind === "fatigue";
    newButton.hidden = !isFatigue;
    if (!isFatigue && !form.hidden) closeForm(false);
    return showScreening ? showScreening(kind) : Promise.resolve();
  };
}

// Without a document (the Node tests load fatigue-form-core.js, not this file) there is nothing to wire up.
if (typeof document !== "undefined") initFatigueForm();
