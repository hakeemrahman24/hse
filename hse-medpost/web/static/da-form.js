// Drug & Alcohol consent and result form on the Medical Record page. The
// "New drug & alcohol screening" button in the Drug and Alcohol table switches
// the page to this form (the toolbar and table are hidden, as on the malaria
// page); Cancel switches back. Save stores the record (POST
// /api/screening-log/alcohol) and clears the form for the next person; Save PDF
// makes the same certificate as the offline form, under 1 MB.
//
// Loaded after malaria.js (createSearchPicker, matchPatients, patientHint),
// screening-log.js, da-form-core.js and da-form-pdf.js. The pure parts are in
// da-form-core.js; the PDF is drawn by da-form-pdf.js, with jsPDF loaded the
// first time a PDF is needed.

const DA_MAX_PHOTOS = 8;
const DA_PHOTO_MAX_SIDE = 800;
const DA_PHOTO_QUALITY = 0.6;
const DA_SIGNATURE_WIDTH = 360;
const DA_SIGNATURE_HEIGHT = 110;
const DA_SIGNATURE_INK = "#0066ff";
const DA_SIGNATURE_FONT = '"Dancing Script"';

// The first name in cursive as a transparent PNG. It sits in the upper part of
// the picture; the PDF prints the approval time in the space under it.
function daSignatureImage(firstName) {
  const canvas = document.createElement("canvas");
  canvas.width = DA_SIGNATURE_WIDTH;
  canvas.height = DA_SIGNATURE_HEIGHT;
  const ctx = canvas.getContext("2d");
  let size = 72;
  ctx.font = "700 " + size + "px " + DA_SIGNATURE_FONT + ", cursive";
  while (size > 24 && ctx.measureText(firstName).width > DA_SIGNATURE_WIDTH - 24) {
    size -= 4;
    ctx.font = "700 " + size + "px " + DA_SIGNATURE_FONT + ", cursive";
  }
  ctx.fillStyle = DA_SIGNATURE_INK;
  ctx.textAlign = "center";
  ctx.textBaseline = "middle";
  ctx.fillText(firstName, DA_SIGNATURE_WIDTH / 2, DA_SIGNATURE_HEIGHT * 0.45);
  return canvas.toDataURL("image/png");
}

function daLoadScript(src) {
  return new Promise((resolve, reject) => {
    const script = document.createElement("script");
    script.src = src;
    script.onload = resolve;
    script.onerror = () => reject(new Error("Could not load " + src));
    document.head.append(script);
  });
}

function daReadAsDataUrl(blob) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result);
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(blob);
  });
}

// A photo file as an 800px JPEG, like the offline form keeps it.
async function daResizePhoto(file) {
  const source = await daReadAsDataUrl(file);
  const img = await new Promise((resolve, reject) => {
    const el = new Image();
    el.onload = () => resolve(el);
    el.onerror = () => reject(new Error("That file is not a picture."));
    el.src = source;
  });
  const scale = Math.min(1, DA_PHOTO_MAX_SIDE / Math.max(img.width, img.height));
  const w = Math.round(img.width * scale);
  const h = Math.round(img.height * scale);
  const canvas = document.createElement("canvas");
  canvas.width = w;
  canvas.height = h;
  canvas.getContext("2d").drawImage(img, 0, 0, w, h);
  return { dataUrl: canvas.toDataURL("image/jpeg", DA_PHOTO_QUALITY), w, h };
}

function initDaForm() {
  const form = document.getElementById("da-form");
  if (!form) return;
  const $ = (id) => document.getElementById(id);
  const view = $("scr-view-table");
  const newButton = $("scr-new");
  const message = $("da-msg");
  const buttons = { save: $("da-save"), pdf: $("da-pdf") };

  let recordId = "";
  let baseline = "";
  let busy = false;
  let sigs = {}; // key -> { png, stamp, name }
  let photos = [];
  let patients = null; // loaded on first use
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

  // ---- reading and writing the answers ---------------------------------------
  const text = (key) => $("da-" + key).value.trim();
  const checked = (key) => $("da-" + key).checked;
  const radio = (name) => {
    const el = form.querySelector('input[name="da-' + name + '"]:checked');
    return el ? el.value : "";
  };

  function readValues() {
    const values = daBlankValues();
    for (const key of ["donorName", "donorNik", "donorCompany", "donorPosition", "consentDt", "witnessName", "med1Name", "med1Dose", "med1Reason", "med2Name", "med2Dose", "med2Reason", "breathDevice", "breathCalib", "b1Time", "b1Read", "b2Time", "b2Read", "specTempVal", "remarks", "collectorName", "securityName", "ackName"]) {
      values[key] = text(key);
    }
    for (const key of ["medNone", "b1Mouth", "b2Mouth", "specTemp", "specColour"]) values[key] = checked(key);
    for (const name of ["testType", "med1Presc", "med2Presc", "b1Res", "b2Res", "finalRes"]) values[name] = radio(name);
    for (const panel of DA_PANELS) {
      values.pneg[panel] = $("da-pneg-" + panel).checked;
      values.pnonneg[panel] = $("da-pnonneg-" + panel).checked;
    }
    return values;
  }

  const signedMap = () => Object.fromEntries(Object.keys(sigs).map((k) => [k, true]));
  const snapshot = () => JSON.stringify([readValues(), Object.keys(sigs), photos.length]);

  // ---- patient pickers: donor, witness, collector, security, acknowledged ---------
  async function ensurePatients() {
    if (!patients) patients = await getJSON("/api/patients");
    return patients;
  }

  function onNameChosen(nameKey, item) {
    // A signature shows the signer's first name, so changing the name withdraws it.
    for (const def of DA_SIGNATURES) if (def.nameKey === nameKey && sigs[def.key]) withdraw(def.key);
    if (nameKey !== "donorName" || !item) return;
    const patient = item.patient;
    $("da-donorNik").value = patient.employeeId || patient.personalId || "";
    $("da-donorCompany").value = patient.companyName || "";
    $("da-donorPosition").value = patient.workPosition || "";
    if (!pickers.ackName.item) pickers.ackName.set(item); // the donor acknowledges the result
  }

  for (const def of DA_SIGNATURES) {
    const key = def.nameKey;
    pickers[key] = createSearchPicker({
      input: $("da-" + key),
      list: $("da-" + key + "-list"),
      minChars: 1,
      remote: async (query) => matchPatients(await ensurePatients(), query).map((patient) => ({ name: patient.name, path: patientHint(patient), patient })),
      onChange: (item) => onNameChosen(key, item),
    });
  }

  // ---- signatures: a click approves, the first name in cursive ----------------
  function paintSignature(key) {
    const holder = form.querySelector('.da-sign[data-sign="' + key + '"]');
    const button = holder.querySelector(".da-sign-btn");
    const stamp = holder.querySelector(".da-sign-stamp");
    const sig = sigs[key];
    button.classList.toggle("is-approved", Boolean(sig));
    button.textContent = sig ? daFirstName(sig.name) : "Click to approve";
    button.title = sig ? "Click to withdraw the approval" : "";
    stamp.textContent = sig ? "Approved " + sig.stamp : "";
  }

  function withdraw(key) {
    delete sigs[key];
    paintSignature(key);
  }

  async function toggleSignature(key) {
    if (sigs[key]) return withdraw(key);
    const def = DA_SIGNATURES.find((d) => d.key === key);
    const name = text(def.nameKey);
    if (!name) {
      $("da-" + def.nameKey).focus();
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

  // ---- photos (kept in the PDF only, as on the offline form) -----------------
  function drawPhotos() {
    const list = $("da-photo-list");
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
    $("da-photo-add").disabled = photos.length >= DA_MAX_PHOTOS;
  }

  $("da-photo-add").addEventListener("click", () => $("da-photo-input").click());
  $("da-photo-input").addEventListener("change", async (event) => {
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

  // ---- static parts: the consent statements and the panel rows ----------------
  for (const item of DA_CONSENT_ITEMS) {
    const li = document.createElement("li");
    const cut = item.en.indexOf(". ") + 1;
    const title = document.createElement("b");
    title.textContent = item.en.slice(0, cut);
    const id = document.createElement("i");
    id.textContent = item.id;
    li.append(title, document.createTextNode(item.en.slice(cut)), id);
    $("da-consent").append(li);
  }
  for (const [row, label, sub] of [["pneg", "Negative", "Negatif"], ["pnonneg", "Non-negative", "Non-negatif"]]) {
    const tr = document.createElement("tr");
    const th = document.createElement("th");
    th.append(document.createTextNode(label + " "), Object.assign(document.createElement("i"), { textContent: sub }));
    tr.append(th);
    for (const panel of DA_PANELS) {
      const td = document.createElement("td");
      const box = document.createElement("input");
      box.type = "checkbox";
      box.id = "da-" + row + "-" + panel;
      box.setAttribute("aria-label", label + " " + panel.toUpperCase());
      td.append(box);
      tr.append(td);
    }
    $("da-panel-body").append(tr);
  }

  // ---- saving ----------------------------------------------------------------
  function markMissing(labels) {
    for (const el of form.querySelectorAll(".da-missing")) el.classList.remove("da-missing");
    let first = null;
    for (const required of DA_REQUIRED) {
      if (!labels.includes(required.label)) continue;
      const el = $("da-" + required.key);
      el.classList.add("da-missing");
      first = first || el;
    }
    if (labels.includes("Donor signature")) {
      const button = form.querySelector('.da-sign-btn[data-key="donor"]');
      button.classList.add("da-missing");
      first = first || button;
    }
    if (first) first.scrollIntoView({ block: "center" });
  }
  // An answer given takes the red outline off the box, or off the group (test type) it is in.
  const clearMissing = (event) => event.target.closest(".da-missing")?.classList.remove("da-missing");
  form.addEventListener("input", clearMissing);
  form.addEventListener("change", clearMissing);

  // The answers if the form can be saved; otherwise says what is missing.
  function validValues() {
    const values = readValues();
    const missing = daMissing(values, signedMap());
    markMissing(missing);
    if (missing.length > 0) {
      say("Still needed: " + missing.join(", ") + ".", true);
      return null;
    }
    return values;
  }

  function setBusy(on) {
    busy = on;
    buttons.save.disabled = on;
    buttons.pdf.disabled = on;
  }

  async function makePdf(values) {
    if (!window.jspdf) await daLoadScript("/static/vendor/jspdf.min.js");
    if (logo === null) {
      try {
        logo = await daReadAsDataUrl(await (await fetch("/static/da-logo.png", { credentials: "same-origin" })).blob());
      } catch {
        logo = ""; // the certificate is still valid without the logo
      }
    }
    return daPdfBlob(values, sigs, photos, logo, recordId);
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
      download(blob, daPdfName(values.donorName, new Date()));
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
      if ($("da-save-pdf").checked) {
        const blob = await makePdf(values);
        download(blob, daPdfName(values.donorName, new Date()));
        note = " PDF downloaded (" + kilobytes(blob) + ").";
      }
      await getJSON("/api/screening-log/alcohol", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(daRecord(values, recordId, new Date())),
      });
      resetForm(); // blank form, ready for the next person; Cancel goes back to the table
      say("Saved for " + values.donorName + "." + note);
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
    syncDateDisplays(form); // redraws the dd/mm/yyyy box
    for (const picker of Object.values(pickers)) picker.reset();
    for (const key of Object.keys(sigs)) withdraw(key);
    photos = [];
    drawPhotos();
    markMissing([]);
    $("da-save-pdf").checked = true;
    $("da-consentDt").value = daLocalDateTime(new Date());
    recordId = newId();
    $("da-docid").textContent = recordId;
    baseline = snapshot();
    say("");
  }

  const isDirty = () => snapshot() !== baseline;

  function openForm() {
    resetForm();
    view.classList.add("is-form-mode");
    form.hidden = false;
    $("da-donorName").focus();
  }

  function closeForm(askFirst) {
    if (askFirst && isDirty() && !window.confirm("Discard what was typed in this form?")) return;
    resetForm();
    form.hidden = true;
    view.classList.remove("is-form-mode");
  }

  newButton.addEventListener("click", openForm);
  $("da-cancel").addEventListener("click", () => closeForm(true));
  $("da-cancel-top").addEventListener("click", () => closeForm(true));
  $("da-reset").addEventListener("click", () => {
    if (!isDirty() || window.confirm("Clear everything typed in this form?")) resetForm();
  });
  buttons.save.addEventListener("click", save);
  buttons.pdf.addEventListener("click", savePdf);
  form.addEventListener("submit", (event) => event.preventDefault());

  // The button is for Drug and Alcohol only; the fatigue test has its own form.
  // Switching tests closes the form, so it is never left open over the wrong table.
  const showScreening = window.loadScreening;
  window.loadScreening = function (kind) {
    const isAlcohol = kind === "alcohol";
    newButton.hidden = !isAlcohol;
    if (!isAlcohol && !form.hidden) closeForm(false);
    return showScreening ? showScreening(kind) : Promise.resolve();
  };
}

// Without a document (the Node tests load da-form-core.js, not this file) there is nothing to wire up.
if (typeof document !== "undefined") initDaForm();
