// Malaria screening form on the Medical Record page (Screening > Malaria).
//
// The server decides everything that matters (age, company, position, region
// names, the cleaned head-of-household name, the slide code). This file only
// helps the person fill the form in and shows what the server saved. The pure
// helpers at the top are also loaded by Node tests (web/malaria.test.js).

const MALARIA_MAX_WEIGHT_KG = 150;
const PATIENT_MATCH_LIMIT = 20;

// Same rule as the server's CleanHeadOfHousehold (internal/emr/rules.go): keep
// letters, spaces, '.', ''' and '-'; collapse spaces; Title Case. Used while
// typing so the person sees what will be saved; the server still decides.
function cleanHeadOfHousehold(text, { final = false } = {}) {
  let kept = String(text).replace(/[^\p{L} .'-]/gu, "").replace(/ {2,}/g, " ");
  if (final) kept = kept.trim();
  else kept = kept.replace(/^ /, "");
  return kept
    .split(" ")
    .map((word) =>
      word.toLowerCase().replace(/(^|-)(\p{L})/gu, (_, lead, letter) => lead + letter.toUpperCase()),
    )
    .join(" ");
}

// Keeps what is typed in the weight box to digits and at most one decimal point
// with one digit after it.
function sanitizeWeightText(text) {
  const cleaned = String(text).replace(",", ".").replace(/[^0-9.]/g, "");
  const [whole, ...fraction] = cleaned.split(".");
  if (fraction.length === 0) return whole;
  return whole + "." + fraction.join("").slice(0, 1);
}

// Returns the weight in kg when it is valid (more than 0, at most 150), else null.
function parseWeightKg(text) {
  if (!/^\d+(\.\d)?$/.test(String(text))) return null;
  const kg = Number(text);
  return kg > 0 && kg <= MALARIA_MAX_WEIGHT_KG ? kg : null;
}

// Patients whose name contains every word typed, best (starts-with) first,
// capped so the dropdown stays short.
function matchPatients(patients, query) {
  const words = String(query).toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return [];
  const scored = [];
  for (const patient of patients) {
    const name = patient.name.toLowerCase();
    if (!words.every((word) => name.includes(word))) continue;
    scored.push({ patient, rank: name.startsWith(words[0]) ? 0 : 1 });
  }
  scored.sort((a, b) => a.rank - b.rank || a.patient.name.localeCompare(b.patient.name));
  return scored.slice(0, PATIENT_MATCH_LIMIT).map((s) => s.patient);
}

// The ISO 8601 week of a YYYY-MM-DD day as "2026-W43", or "" when it is not a
// real date. Same answer as the server's ISOWeek (internal/emr/rules.go); the
// server computes the stored/reported value, this is only for the form.
function isoWeekLabel(iso) {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(String(iso));
  if (!m) return "";
  const [year, month, day] = [Number(m[1]), Number(m[2]), Number(m[3])];
  const date = new Date(Date.UTC(year, month - 1, day));
  if (date.getUTCFullYear() !== year || date.getUTCMonth() !== month - 1 || date.getUTCDate() !== day) return "";
  const weekday = date.getUTCDay() || 7; // Monday = 1 ... Sunday = 7
  date.setUTCDate(date.getUTCDate() + 4 - weekday); // the Thursday of this week decides the week-year
  const weekYear = date.getUTCFullYear();
  const week = Math.ceil(((date - Date.UTC(weekYear, 0, 1)) / 86400000 + 1) / 7);
  return weekYear + "-W" + String(week).padStart(2, "0");
}

// Width of a dashboard bar as a percentage of the longest bar. A bar that is not
// empty is always at least 2% wide so it stays visible next to a much bigger one.
function barPercent(count, max) {
  if (!(count > 0) || !(max > 0)) return 0;
  return Math.max(2, Math.round((count / max) * 100));
}

// The positive results, in the order the dashboard lists them.
const SPECIES = ["Pv", "Pf", "Po", "Mixed"];

// Stacked bar pieces for one region's positives: one {key, x, width} per species
// that has cases, laid end to end from the left, each sized like barPercent.
function speciesSegments(species, max) {
  const segments = [];
  let x = 0;
  for (const key of SPECIES) {
    const width = barPercent((species || {})[key], max);
    if (width === 0) continue;
    segments.push({ key, x, width: Math.min(width, 100 - x) });
    x += width;
  }
  return segments;
}

// "Pv 2, Pf 1" for the species that have cases; "" when there are none.
function speciesLabel(species) {
  return SPECIES.filter((key) => (species || {})[key] > 0).map((key) => key + " " + species[key]).join(", ");
}

// The label/value pairs shown when a row in the table is opened, in reading
// order. Every field of the record the summary table leaves out is here.
function malariaDetailFields(row) {
  const smear = row.examType === "BloodSmear" ? "Blood smear" : row.examType;
  return [
    ["Test date", row.testDate],
    ["Week of the year", row.week],
    ["Slide initial", row.slideInitial],
    ["Name", row.patientName],
    ["Age", row.ageYears === null || row.ageYears === undefined ? "" : row.ageYears + " years"],
    ["Weight", row.weightKg + " kg"],
    ["Head of household", row.headIsPatient ? row.headOfHousehold + " (same as the patient)" : row.headOfHousehold],
    ["Regency", row.regency],
    ["District", row.district],
    ["Village", row.village],
    ["Work position", row.workPosition],
    ["Company", row.company],
    ["Examiner", row.examiner],
    ["Screening purpose", row.purpose || ""],
    ["Test", smear],
    ["Result", row.result || "Pending"],
    ["Recorded at (UTC)", String(row.recordedAt || "").replace("T", " ")],
  ];
}

// The line under a patient's name in the dropdown, enough to tell homonyms apart.
function patientHint(patient) {
  return [patient.dateOfBirth, patient.companyName].filter(Boolean).join(" · ");
}

// A small x inside a searchable box that empties it. The box's label must be
// positioned (see .mal-clear in app.css). Returns a function that shows or hides
// the x; call it whenever the box's text is changed from code, since typing is
// already watched here. onClear does the real clearing; focus then returns to the
// box so the list opens again for a new search.
function addClearButton(input, onClear) {
  const holder = input.closest("label") || input.parentElement;
  const button = document.createElement("button");
  button.type = "button";
  button.className = "mal-clear";
  button.hidden = true;
  button.textContent = "×";
  button.setAttribute("aria-label", "Clear " + (holder.querySelector("span")?.textContent || "this field").toLowerCase());
  button.addEventListener("mousedown", (event) => event.preventDefault()); // keep the box focused
  button.addEventListener("click", () => {
    onClear();
    sync();
    input.focus();
  });
  holder.append(button);
  function sync() {
    button.hidden = input.disabled || input.value === "";
  }
  input.addEventListener("input", sync);
  sync();
  return sync;
}

// The highlighted row after an arrow key: down from nothing goes to the first row,
// up from nothing to the last, and either way wraps around. -1 when there are no rows.
function nextIndex(active, delta, length) {
  if (length <= 0) return -1;
  if (active < 0) return delta > 0 ? 0 : length - 1;
  return (active + delta + length) % length;
}

const REGION_LIST_LIMIT = 100;
const REMOTE_RESULT_LIMIT = 50; // what the server returns for one search

// Regions whose name contains every word typed, in the order given (the API
// sends them sorted by name). An empty query keeps everything.
function filterRegions(items, query) {
  const words = String(query).toLowerCase().split(/\s+/).filter(Boolean);
  return items.filter((item) => {
    const name = item.name.toLowerCase();
    return words.every((word) => name.includes(word));
  });
}

// A searchable dropdown over a text input: type to narrow the list, arrow keys
// and Enter or a click to choose. It holds the chosen item itself, so editing the
// text afterwards un-chooses it. Text left unchosen is cleared when the box loses
// focus, like a plain select that cannot hold a value that is not in its list.
//
// With `remote` (an async function from the typed text to a list) the choices
// come from the server as the person types instead of from a list already held:
// used for districts, of which there are over seven thousand. It waits for
// `minChars` letters, and an answer that arrives after the text has changed again
// is dropped.
function createSearchPicker({ input, list, onChange, remote: initialRemote = null, minChars = 2 }) {
  let remote = initialRemote;
  let items = [];
  let selected = null;
  let shown = [];
  let active = -1;
  let showSeq = 0;
  const syncClear = addClearButton(input, () => {
    const was = selected;
    selected = null;
    input.value = "";
    if (was) onChange(null);
  });

  function hide() {
    list.style.display = "none";
    input.setAttribute("aria-expanded", "false");
    active = -1;
  }

  function highlight(index) {
    active = index;
    [...list.querySelectorAll("button[data-index]")].forEach((button, i) => {
      button.classList.toggle("active", i === index);
      if (i === index) button.scrollIntoView({ block: "nearest" });
    });
  }

  function note(text) {
    const el = document.createElement("div");
    el.className = "app-tree-empty";
    el.textContent = text;
    list.append(el);
  }

  function open() {
    list.style.display = "";
    input.setAttribute("aria-expanded", "true");
  }

  async function show() {
    const seq = ++showSeq;
    let matches;
    if (remote) {
      // A chosen value is left alone when the box is focused; typing un-chooses it.
      if (selected) return hide();
      const text = input.value.trim();
      if (text.length < minChars) {
        shown = [];
        list.replaceChildren();
        note("Type at least " + minChars + " letters");
        open();
        return highlight(-1);
      }
      try {
        matches = await remote(text);
      } catch {
        matches = [];
      }
      if (seq !== showSeq) return;
    } else {
      // With a value already chosen, opening the box shows the whole list again.
      matches = filterRegions(items, selected ? "" : input.value);
    }
    shown = matches.slice(0, REGION_LIST_LIMIT);
    list.replaceChildren();
    shown.forEach((item, i) => {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "app-program-item";
      button.setAttribute("role", "option");
      button.dataset.index = String(i);
      const name = document.createElement("span");
      name.className = "app-program-item-title";
      name.textContent = item.name;
      button.append(name);
      if (item.path) {
        const where = document.createElement("small");
        where.className = "mal-hint";
        where.textContent = item.path;
        button.append(where);
      }
      list.append(button);
    });
    if (shown.length === 0) note("No match");
    if (!remote && matches.length > shown.length) note(matches.length - shown.length + " more, keep typing to narrow the list");
    if (remote && matches.length >= REMOTE_RESULT_LIMIT) note("Showing the first " + matches.length + "; add a regency or province word to narrow");
    open();
    highlight(shown.length > 0 ? 0 : -1);
  }

  function choose(item) {
    selected = item;
    input.value = item.name;
    syncClear();
    hide();
    onChange(item);
  }

  input.addEventListener("input", () => {
    if (selected) {
      selected = null;
      onChange(null);
    }
    show();
  });
  input.addEventListener("focus", () => {
    if (input.disabled) return;
    input.select();
    show();
  });
  input.addEventListener("blur", () => {
    hide();
    if (!selected) input.value = "";
    syncClear();
  });
  input.addEventListener("keydown", (event) => {
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      if (list.style.display === "none") return show();
      highlight(nextIndex(active, event.key === "ArrowDown" ? 1 : -1, shown.length));
    } else if (event.key === "Enter" && list.style.display !== "none") {
      event.preventDefault(); // Enter picks; it must not submit the form
      if (shown[active]) choose(shown[active]);
    } else if (event.key === "Tab" && list.style.display !== "none" && !selected && shown[active]) {
      choose(shown[active]); // Tab picks the highlighted row and moves on as usual
    } else if (event.key === "Escape") {
      hide();
    }
  });
  list.addEventListener("mousedown", (event) => {
    event.preventDefault(); // keep focus so the blur handler does not clear the text first
    const button = event.target.closest("button[data-index]");
    if (button) choose(shown[Number(button.dataset.index)]);
  });

  return {
    setItems(next) {
      items = next;
      selected = null;
      input.value = "";
      syncClear();
    },
    clear() {
      items = [];
      selected = null;
      input.value = "";
      syncClear();
    },
    // Empties the box like the x does: a chosen value is un-chosen, and onChange
    // runs so the boxes that depend on it are emptied too.
    reset() {
      const was = selected;
      selected = null;
      input.value = "";
      syncClear();
      if (was) onChange(null);
    },
    setEnabled(on, placeholder) {
      input.disabled = !on;
      input.placeholder = placeholder;
      syncClear();
    },
    get code() {
      return selected ? selected.code : "";
    },
    get item() {
      return selected;
    },
    // Shows an item as chosen without calling onChange: used when another box
    // decides this one's value (the district decides the regency).
    set(item) {
      selected = item;
      input.value = item ? item.name : "";
      syncClear();
    },
    // Switches between searching the server and filtering the loaded list.
    useRemote(fn) {
      remote = fn;
    },
    useLocal() {
      remote = null;
    },
  };
}

function initMalariaForm() {
  const form = document.getElementById("malaria-form");
  const els = {
    date: document.getElementById("mal-date"),
    patient: document.getElementById("mal-patient"),
    list: document.getElementById("mal-patient-list"),
    week: document.getElementById("mal-week"),
    age: document.getElementById("mal-age"),
    position: document.getElementById("mal-position"),
    company: document.getElementById("mal-company"),
    slidePreview: document.getElementById("mal-slide-preview"),
    search: document.getElementById("mal-search"),
    exportLink: document.getElementById("mal-export"),
    from: document.getElementById("mal-from"),
    to: document.getElementById("mal-to"),
    filterReset: document.getElementById("mal-filter-reset"),
    newButton: document.getElementById("mal-new"),
    cancelButtons: [document.getElementById("mal-cancel-top"), document.getElementById("mal-cancel")],
    tabs: [...document.querySelectorAll("#mr-panel-malaria .app-tab")],
    views: {
      table: document.getElementById("mal-view-table"),
      dashboard: document.getElementById("mal-view-dashboard"),
      reference: document.getElementById("mal-view-reference"),
    },
    dashFilter: document.getElementById("mal-dash-filter"),
    dashFrom: document.getElementById("mal-dash-from"),
    dashTo: document.getElementById("mal-dash-to"),
    dashBy: document.getElementById("mal-dash-by"),
    dashSpecies: document.getElementById("mal-dash-species"),
    speciesTiles: document.getElementById("mal-species-tiles"),
    legendAll: document.getElementById("mal-legend-all"),
    legendType: document.getElementById("mal-legend-type"),
    dashReset: document.getElementById("mal-dash-reset"),
    dashError: document.getElementById("mal-dash-error"),
    dashBoard: document.getElementById("mal-dash-board"),
    stats: {
      total: document.getElementById("mal-stat-total"),
      patients: document.getElementById("mal-stat-patients"),
      positive: document.getElementById("mal-stat-positive"),
      pending: document.getElementById("mal-stat-pending"),
      species: Object.fromEntries(SPECIES.map((key) => [key, document.getElementById("mal-stat-" + key)])),
    },
    chartTitle: document.getElementById("mal-chart-title"),
    chartHint: document.getElementById("mal-chart-hint"),
    bars: document.getElementById("mal-bars"),
    chartEmpty: document.getElementById("mal-chart-empty"),
    weight: document.getElementById("mal-weight"),
    head: document.getElementById("mal-head"),
    sameAsPatient: document.getElementById("mal-head-same"),
    regency: document.getElementById("mal-regency"),
    regencyList: document.getElementById("mal-regency-list"),
    district: document.getElementById("mal-district"),
    districtList: document.getElementById("mal-district-list"),
    village: document.getElementById("mal-village"),
    villageList: document.getElementById("mal-village-list"),
    purpose: document.getElementById("mal-purpose"),
    examType: document.getElementById("mal-exam"),
    result: document.getElementById("mal-result"),
    message: document.getElementById("mal-message"),
    slide: document.getElementById("mal-slide"),
    slideCode: document.getElementById("mal-slide-code"),
    body: document.getElementById("mal-body"),
    empty: document.getElementById("mal-empty"),
  };
  const RESULTS = ["Negative", "Pv", "Pf", "Po", "Mixed"];
  let patients = null; // loaded on first use
  let chosen = null; // the patient picked in the dropdown
  let loaded = false;

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
    els.message.textContent = text;
    els.message.classList.toggle("is-error", Boolean(isError));
  }

  // ---- patient picker -----------------------------------------------------
  async function ensurePatients() {
    if (patients) return patients;
    patients = await getJSON("/api/patients");
    return patients;
  }

  let patientActive = -1; // highlighted row in the patient list

  function closeList() {
    els.list.style.display = "none";
    els.patient.setAttribute("aria-expanded", "false");
    patientActive = -1;
  }

  function highlightPatient(index) {
    patientActive = index;
    [...els.list.querySelectorAll("button[data-id]")].forEach((button, i) => {
      button.classList.toggle("active", i === index);
      if (i === index) button.scrollIntoView({ block: "nearest" });
    });
  }

  function highlightedPatient() {
    const button = els.list.querySelectorAll("button[data-id]")[patientActive];
    return button && (patients || []).find((p) => String(p.patientId) === button.dataset.id);
  }

  const NO_VALUE = "—";
  function showPatientValues(info) {
    els.age.textContent = info && info.ageYears !== null ? info.ageYears + " years" : NO_VALUE;
    els.position.textContent = (info && info.workPosition) || NO_VALUE;
    els.company.textContent = (info && info.company) || NO_VALUE;
  }

  // The code saving would give this patient on this date (the server picks it;
  // it is final when saved). Shown bold in capitals so it can be copied onto the
  // slide. A late answer for an earlier patient or date is ignored.
  let previewSeq = 0;
  async function showSlidePreview() {
    const seq = ++previewSeq;
    if (!chosen || !els.date.value) {
      els.slidePreview.textContent = NO_VALUE;
      return;
    }
    try {
      const body = await getJSON(
        "/api/malaria-screenings/slide-initial?patientId=" + chosen.patientId + "&testDate=" + encodeURIComponent(els.date.value),
      );
      if (seq === previewSeq) els.slidePreview.textContent = body.slideInitial;
    } catch {
      if (seq === previewSeq) els.slidePreview.textContent = NO_VALUE;
    }
  }

  function showWeek() {
    els.week.textContent = isoWeekLabel(els.date.value) || NO_VALUE;
  }

  // Sets the date: the real date input holds YYYY-MM-DD, and date-dmy.js
  // redraws the dd/mm/yyyy box when it hears an input event.
  function setDate(iso) {
    els.date.value = iso;
    els.date.dispatchEvent(new Event("input", { bubbles: true }));
    showWeek();
  }

  async function showMatches() {
    chosen = null;
    showPatientValues(null);
    showSlidePreview();
    updateHeadForPatient();
    const query = els.patient.value;
    if (!query.trim()) return closeList();
    let all;
    try {
      all = await ensurePatients();
    } catch (err) {
      return say(err.message, true);
    }
    const matches = matchPatients(all, query);
    els.list.replaceChildren();
    for (const patient of matches) {
      const item = document.createElement("button");
      item.type = "button";
      item.className = "app-program-item";
      item.setAttribute("role", "option");
      item.dataset.id = String(patient.patientId);
      const name = document.createElement("span");
      name.className = "app-program-item-title";
      name.textContent = patient.name;
      const hint = document.createElement("small");
      hint.className = "mal-hint";
      hint.textContent = patientHint(patient);
      item.append(name, hint);
      els.list.append(item);
    }
    if (matches.length === 0) {
      const none = document.createElement("div");
      none.className = "app-tree-empty";
      none.textContent = "No patient with that name";
      els.list.append(none);
    }
    els.list.style.display = "";
    els.patient.setAttribute("aria-expanded", "true");
    highlightPatient(matches.length > 0 ? 0 : -1);
  }

  async function choosePatient(patient) {
    chosen = patient;
    els.patient.value = patient.name;
    syncPatientClear();
    closeList();
    updateHeadForPatient();
    showSlidePreview();
    await showContext();
  }

  async function showContext() {
    if (!chosen) return;
    try {
      const info = await getJSON(
        "/api/malaria-screenings/patient-context?patientId=" + chosen.patientId + "&testDate=" + encodeURIComponent(els.date.value),
      );
      showPatientValues(info);
    } catch (err) {
      showPatientValues(null);
      say(err.message, true);
    }
  }

  const syncPatientClear = addClearButton(els.patient, () => {
    els.patient.value = "";
    showMatches();
  });
  els.patient.addEventListener("input", showMatches);
  els.patient.addEventListener("focus", () => els.patient.value.trim() && !chosen && showMatches());
  els.list.addEventListener("mousedown", async (event) => {
    event.preventDefault(); // keep focus so the blur handler does not close the list first
    const item = event.target.closest("button[data-id]");
    if (!item) return;
    const patient = (patients || []).find((p) => String(p.patientId) === item.dataset.id);
    if (patient) await choosePatient(patient);
  });
  // Text that is not a chosen patient is cleared when the box loses focus, like
  // the region boxes: only a patient from the list can be entered.
  els.patient.addEventListener("blur", () => {
    closeList();
    if (!chosen) els.patient.value = "";
    syncPatientClear();
  });
  els.patient.addEventListener("keydown", (event) => {
    const open = els.list.style.display !== "none";
    if (event.key === "Escape") closeList();
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      if (!open) return chosen ? undefined : showMatches();
      const count = els.list.querySelectorAll("button[data-id]").length;
      highlightPatient(nextIndex(patientActive, event.key === "ArrowDown" ? 1 : -1, count));
    } else if (event.key === "Enter" && open) {
      event.preventDefault(); // Enter picks; it must not submit the form
      const patient = highlightedPatient();
      if (patient) choosePatient(patient);
    } else if (event.key === "Tab" && open && !chosen) {
      const patient = highlightedPatient();
      if (patient) choosePatient(patient); // and focus moves on as usual
    }
  });
  els.date.addEventListener("change", () => {
    showWeek();
    showSlidePreview();
    showContext();
  });

  // ---- head of household --------------------------------------------------
  function updateHeadForPatient() {
    const same = els.sameAsPatient.checked;
    els.head.disabled = same;
    if (same) els.head.value = chosen ? cleanHeadOfHousehold(chosen.name, { final: true }) : "";
  }
  els.sameAsPatient.addEventListener("change", () => {
    if (!els.sameAsPatient.checked) els.head.value = "";
    updateHeadForPatient();
  });
  els.head.addEventListener("input", () => {
    const cleaned = cleanHeadOfHousehold(els.head.value);
    if (cleaned !== els.head.value) els.head.value = cleaned;
  });
  els.head.addEventListener("blur", () => {
    els.head.value = cleanHeadOfHousehold(els.head.value, { final: true });
  });

  // ---- weight -------------------------------------------------------------
  els.weight.addEventListener("input", () => {
    const cleaned = sanitizeWeightText(els.weight.value);
    if (cleaned !== els.weight.value) els.weight.value = cleaned;
  });

  // ---- region -------------------------------------------------------------
  // Two ways in. Choose a regency first and the district search is limited to its
  // districts; or skip it and search any district in Indonesia on the server (each
  // result shows its regency and province), and the regency fills itself in from
  // the district. The village is searched among the chosen district's villages and
  // stays disabled until a district is chosen.
  const searchAnyDistrict = (text) =>
    getJSON("/api/regions?level=3&limit=" + REMOTE_RESULT_LIMIT + "&q=" + encodeURIComponent(text));
  const villagePicker = createSearchPicker({ input: els.village, list: els.villageList, onChange() {} });
  const districtPicker = createSearchPicker({
    input: els.district,
    list: els.districtList,
    remote: searchAnyDistrict,
    onChange(item) {
      villagePicker.clear();
      villagePicker.setEnabled(false, "Choose a district first");
      if (!item) return;
      // Found without choosing a regency (or in another one): the regency follows
      // the district. Local lists carry no parentCode, but there it already matches.
      if (item.parentCode && item.parentCode !== regencyPicker.code) {
        regencyPicker.set({ code: item.parentCode, name: String(item.path).split(", ")[0] });
      }
      loadChildren(villagePicker, districtPicker, item.code, "Search village…");
    },
  });
  const regencyPicker = createSearchPicker({
    input: els.regency,
    list: els.regencyList,
    onChange(item) {
      districtPicker.clear();
      villagePicker.clear();
      villagePicker.setEnabled(false, "Choose a district first");
      if (item) {
        districtPicker.useLocal();
        loadChildren(districtPicker, regencyPicker, item.code, "Search district…");
      } else {
        districtPicker.useRemote(searchAnyDistrict);
        districtPicker.setEnabled(true, "Search any district…");
      }
    },
  });

  // Loads the areas under parentCode into picker. A slow answer for a parent that
  // has since been changed is dropped.
  async function loadChildren(picker, parentPicker, parentCode, placeholder) {
    picker.setEnabled(false, "Loading…");
    try {
      const items = await getJSON("/api/regions?parent=" + encodeURIComponent(parentCode));
      if (parentPicker.code !== parentCode) return;
      picker.setItems(items);
      picker.setEnabled(items.length > 0, items.length > 0 ? placeholder : "Nothing listed");
    } catch (err) {
      if (parentPicker.code === parentCode) picker.setEnabled(false, placeholder);
      say(err.message, true);
    }
  }

  // ---- saving -------------------------------------------------------------
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    say("");
    els.slide.hidden = true;
    const weightKg = parseWeightKg(els.weight.value);
    if (!els.date.value) return say("Enter the test date as dd/mm/yyyy.", true);
    if (!chosen) return say("Choose a patient from the list.", true);
    if (weightKg === null) return say("Weight must be a number above 0 and at most 150 kg.", true);
    if (!els.sameAsPatient.checked && !cleanHeadOfHousehold(els.head.value, { final: true })) {
      return say("Enter the head of household, or tick Sama dengan pasien.", true);
    }
    if (!districtPicker.code || !villagePicker.code) {
      return say("Choose the district and the village.", true);
    }
    const payload = {
      patientId: chosen.patientId,
      testDate: els.date.value,
      weightKg,
      headOfHousehold: els.head.value,
      headIsPatient: els.sameAsPatient.checked,
      regencyCode: regencyPicker.code,
      districtCode: districtPicker.code,
      villageCode: villagePicker.code,
      purposeId: Number(els.purpose.value),
      examType: els.examType.value,
      result: els.result.value || null,
    };
    const submit = form.querySelector('button[type="submit"]');
    submit.disabled = true;
    try {
      const saved = await getJSON("/api/malaria-screenings", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      els.slideCode.textContent = saved.slideInitial;
      els.slide.hidden = false;
      dashboardStale = true;
      resetForm(); // blank form, ready for the next patient; Cancel goes back to the table
      say("Saved for " + saved.patientName + ".");
      await loadRows();
    } catch (err) {
      say(err.message, true);
    } finally {
      submit.disabled = false;
    }
  });

  // The patient, weight, head of household and result boxes, empty.
  function clearPatientFields() {
    chosen = null;
    els.patient.value = "";
    syncPatientClear();
    showPatientValues(null);
    showSlidePreview();
    els.weight.value = "";
    els.sameAsPatient.checked = false;
    els.head.value = "";
    els.head.disabled = false;
    els.result.value = "";
    els.patient.focus();
  }

  // Everything back to a blank form, today's date: after a save and on Cancel.
  function resetForm() {
    clearPatientFields();
    els.purpose.value = "1"; // On arrival
    els.examType.value = "RDT";
    regencyPicker.reset(); // also empties the district and the village
    districtPicker.reset();
    villagePicker.reset();
    setDate(clinicToday());
    say("");
  }

  // ---- two modes: the table (search, export, rows) or the form ----
  // "New malaria exam" switches to the form and hides the toolbar and the table;
  // Cancel switches back. Saving clears the form but stays in form mode.
  function openForm() {
    els.views.table.classList.add("is-form-mode");
    form.hidden = false;
    els.patient.focus();
  }

  function closeForm() {
    resetForm();
    els.slide.hidden = true;
    form.hidden = true;
    els.views.table.classList.remove("is-form-mode");
  }
  els.newButton.addEventListener("click", openForm);
  for (const button of els.cancelButtons) button.addEventListener("click", closeForm);

  // ---- tabs: Table | Dashboard ---------------------------------------------
  let dashboardStale = true; // set when a screening is saved or changed

  function showView(name) {
    for (const tab of els.tabs) {
      const on = tab.dataset.view === name;
      tab.classList.toggle("active", on);
      tab.setAttribute("aria-selected", String(on));
    }
    for (const [view, el] of Object.entries(els.views)) el.hidden = view !== name;
    if (name === "dashboard" && dashboardStale) loadDashboard();
    // The Reference tab (reference-tab.js) is loaded each time it is shown, so it is never stale.
    if (name === "reference") window.loadReference?.(document.getElementById("mal-reference"), "malaria");
  }
  for (const tab of els.tabs) tab.addEventListener("click", () => showView(tab.dataset.view));

  // ---- dashboard ----------------------------------------------------------
  const BY_LABEL = { regency: "regency", district: "district", village: "village" };

  function svgBar(percentAll, positives) {
    const ns = "http://www.w3.org/2000/svg";
    const svg = document.createElementNS(ns, "svg");
    svg.setAttribute("viewBox", "0 0 100 12");
    svg.setAttribute("preserveAspectRatio", "none");
    svg.setAttribute("aria-hidden", "true");
    // positives: [{cls, x, width}] drawn over the all-screenings bar
    for (const piece of [{ cls: "mal-bar-all", x: 0, width: percentAll }, ...positives]) {
      if (piece.width <= 0) continue;
      const rect = document.createElementNS(ns, "rect");
      rect.setAttribute("class", piece.cls);
      rect.setAttribute("x", String(piece.x));
      rect.setAttribute("y", "0");
      rect.setAttribute("width", String(piece.width));
      rect.setAttribute("height", "12");
      svg.append(rect);
    }
    return svg;
  }

  function drawDashboard(data) {
    els.stats.total.textContent = String(data.total);
    els.stats.patients.textContent = String(data.patients);
    els.stats.positive.textContent = String(data.positive);
    els.stats.pending.textContent = String(data.pending);
    const byType = els.dashSpecies.value === "type";
    els.speciesTiles.hidden = !byType;
    els.legendAll.hidden = byType;
    els.legendType.hidden = !byType;
    for (const key of SPECIES) els.stats.species[key].textContent = String((data.species || {})[key] || 0);
    els.chartTitle.textContent = "Screenings by " + BY_LABEL[data.by];
    els.chartHint.textContent = data.moreRegions > 0
      ? "Showing the " + data.regions.length + " busiest; " + data.moreRegions + " more not shown."
      : "";
    els.bars.replaceChildren();
    els.chartEmpty.hidden = data.regions.length > 0;
    const max = Math.max(0, ...data.regions.map((r) => r.count));
    const wrap = document.createElement("div");
    wrap.className = "dash-bars";
    for (const region of data.regions) {
      const row = document.createElement("div");
      row.className = "dash-bar-row";
      const label = document.createElement("span");
      label.textContent = region.name;
      label.title = region.name;
      const count = document.createElement("b");
      const detail = byType ? speciesLabel(region.species) : region.positive > 0 ? region.positive + " positive" : "";
      count.textContent = detail ? region.count + " (" + detail + ")" : String(region.count);
      const positives = byType
        ? speciesSegments(region.species, max).map((s) => ({ cls: "mal-bar-" + s.key, x: s.x, width: s.width }))
        : [{ cls: "mal-bar-positive", x: 0, width: barPercent(region.positive, max) }];
      row.append(label, svgBar(barPercent(region.count, max), positives), count);
      wrap.append(row);
    }
    if (data.regions.length > 0) els.bars.append(wrap);
  }

  async function loadDashboard() {
    const params = new URLSearchParams({ by: els.dashBy.value });
    if (els.dashFrom.value) params.set("from", els.dashFrom.value);
    if (els.dashTo.value) params.set("to", els.dashTo.value);
    els.dashError.hidden = true;
    try {
      drawDashboard(await getJSON("/api/malaria-screenings/summary?" + params));
      dashboardStale = false;
    } catch (err) {
      els.dashError.textContent = err.message;
      els.dashError.hidden = false;
    }
  }

  els.dashFilter.addEventListener("submit", (event) => {
    event.preventDefault();
    loadDashboard();
  });
  els.dashBy.addEventListener("change", loadDashboard);
  els.dashSpecies.addEventListener("change", loadDashboard);
  els.dashReset.addEventListener("click", () => {
    for (const input of [els.dashFrom, els.dashTo]) {
      input.value = "";
      input.dispatchEvent(new Event("input", { bubbles: true })); // redraws the dd/mm/yyyy box
    }
    els.dashBy.value = "regency";
    els.dashSpecies.value = "all";
    loadDashboard();
  });

  // ---- list ---------------------------------------------------------------
  function cell(text, className) {
    const td = document.createElement("td");
    td.textContent = text === null || text === undefined ? "" : String(text);
    if (className) td.className = className;
    return td;
  }

  function resultCell(row) {
    const td = document.createElement("td");
    if (row.result) {
      td.textContent = row.result;
      return td;
    }
    const select = document.createElement("select");
    select.setAttribute("aria-label", "Set the result for " + row.patientName);
    select.append(new Option("Pending", ""));
    for (const value of RESULTS) select.append(new Option(value, value));
    select.addEventListener("change", async () => {
      if (!select.value) return;
      select.disabled = true;
      try {
        await getJSON("/api/malaria-screenings/" + row.malariaId + "/result", {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ result: select.value }),
        });
        dashboardStale = true;
        await loadRows();
      } catch (err) {
        say(err.message, true);
        select.disabled = false;
        select.value = "";
      }
    });
    td.append(select);
    return td;
  }


  // ---- opening a row ------------------------------------------------------
  // Clicking a Test ID opens the rest of that record in a row below it (the same
  // way the checkup table opens a visit), and the ID turns bold red; clicking it
  // again closes it. One row is open at a time. The open row is remembered by
  // record number so it stays open when the list is redrawn (a result was set, a
  // search was typed).
  const TABLE_COLUMNS = 8;
  let openMalariaId = null;

  function detailRow(row) {
    const tr = document.createElement("tr");
    tr.className = "detail-row";
    const td = document.createElement("td");
    td.colSpan = TABLE_COLUMNS;
    const grid = document.createElement("div");
    grid.className = "ro-grid";
    for (const [label, value] of malariaDetailFields(row)) {
      const field = document.createElement("label");
      const caption = document.createElement("span");
      caption.textContent = label;
      const shown = document.createElement("div");
      shown.className = "ro-value";
      shown.textContent = value === "" || value === null || value === undefined ? "—" : String(value);
      field.append(caption, shown);
      grid.append(field);
    }
    td.append(grid);
    tr.append(td);
    return tr;
  }

  function closeOpenRow() {
    els.body.querySelector("tr.detail-row")?.remove();
    els.body.querySelector("a.is-expanded-link")?.classList.remove("is-expanded-link");
    openMalariaId = null;
  }

  function openRow(tr, link, row) {
    closeOpenRow();
    link.classList.add("is-expanded-link");
    tr.after(detailRow(row));
    openMalariaId = row.malariaId;
  }

  function testIdCell(tr, row) {
    const td = document.createElement("td");
    const link = document.createElement("a");
    link.href = "#";
    link.textContent = row.testId;
    link.addEventListener("click", (event) => {
      event.preventDefault();
      if (openMalariaId === row.malariaId) closeOpenRow();
      else openRow(tr, link, row);
    });
    td.append(link);
    return [td, link];
  }

  // The table shows the summary columns only; the Excel export has every column.
  // Both follow the name typed in the search box and the From/To dates; with all
  // three empty the export is every record.
  function searchQuery() {
    const params = new URLSearchParams();
    const text = els.search.value.trim();
    if (text) params.set("q", text);
    if (els.from.value) params.set("from", els.from.value);
    if (els.to.value) params.set("to", els.to.value);
    const query = params.toString();
    return query ? "?" + query : "";
  }

  async function loadRows() {
    els.exportLink.href = "/api/malaria-screenings.xlsx" + searchQuery();
    const searching = els.search.value.trim() !== "";
    try {
      const rows = await getJSON("/api/malaria-screenings" + searchQuery());
      const reopen = openMalariaId;
      els.body.replaceChildren();
      openMalariaId = null;
      for (const row of rows) {
        const tr = document.createElement("tr");
        const [idCell, link] = testIdCell(tr, row);
        tr.append(
          idCell,
          cell(row.testDate),
          cell(row.slideInitial, "mal-slide-cell"),
          cell(row.patientName),
          cell(row.workPosition),
          cell(row.company),
          cell(row.examType === "BloodSmear" ? "Blood smear" : row.examType),
          resultCell(row),
        );
        els.body.append(tr);
        if (row.malariaId === reopen) openRow(tr, link, row);
      }
      els.empty.textContent = searching ? "No malaria screenings match this name." : "No malaria screenings yet.";
      els.empty.hidden = rows.length > 0;
    } catch (err) {
      say(err.message, true);
    }
  }

  // Search as you type, waiting for a short pause so each keystroke is not a request.
  const SEARCH_DELAY_MS = 250;
  let searchTimer = null;
  addClearButton(els.search, () => {
    els.search.value = "";
    loadRows();
  });
  els.search.addEventListener("input", () => {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(loadRows, SEARCH_DELAY_MS);
  });
  els.search.addEventListener("keydown", (event) => {
    if (event.key === "Enter") event.preventDefault();
  });
  for (const input of [els.from, els.to]) input.addEventListener("change", loadRows);
  els.filterReset.addEventListener("click", () => {
    els.search.value = "";
    for (const input of [els.from, els.to]) {
      input.value = "";
      input.dispatchEvent(new Event("input", { bubbles: true })); // redraws the dd/mm/yyyy box
    }
    loadRows();
  });

  // emr.js calls this whenever the Malaria view is shown.
  window.loadMalaria = async function () {
    if (!els.date.value) setDate(clinicToday());
    if (!loaded) {
      loaded = true;
      districtPicker.setEnabled(true, "Search any district…");
      villagePicker.setEnabled(false, "Choose a district first");
      regencyPicker.setEnabled(false, "Loading…");
      try {
        regencyPicker.setItems(await getJSON("/api/regions?level=2"));
        regencyPicker.setEnabled(true, "Search regency…");
      } catch (err) {
        loaded = false; // try again the next time the page is shown
        say(err.message, true);
      }
    }
    await loadRows();
  };
}

if (typeof document !== "undefined") initMalariaForm();
