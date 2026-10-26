// Navigation for the Medical Record page (/emr).
//
// The sidebar has four sections: Outpatient, Inpatient, Screening, Wellness,
// each with a small triangle that expands and collapses it, like the programs
// in the Activity Log's sidebar. Only Screening has sub-headers so far: Drug and
// Alcohol and Fatigue show their results as a Table and a Dashboard
// (screening-log.js, /api/screening-log); Malaria is an entry form plus its list
// (malaria.js); the other three sections are placeholders until their tables
// are built.
//
// The URL hash is the single source of truth ("#screening/fatigue"). A click
// only changes the hash; the hashchange handler redraws the page. That makes
// the browser's back button, a reload and a copied link all land on the same
// view without any extra state to keep in sync.

const MEDICAL_RECORD_SECTIONS = ["outpatient", "inpatient", "screening", "wellness"];
const SCREENING_SUBS = ["alcohol", "fatigue", "malaria"];
// The sub-headers that already have uploaded results to list.
const SCREENING_LOGS = ["alcohol", "fatigue"];
const DEFAULT_SCREENING_SUB = "alcohol";

// parseMedicalRecordRoute turns "#screening/fatigue" into
// { section: "screening", sub: "fatigue" }, or null when no section is chosen.
// Only Screening has sub-headers, and an unknown one falls back to Alcohol, so
// a stale or mistyped link still opens a working view.
function parseMedicalRecordRoute(hash) {
  const [section, sub] = String(hash || "").replace(/^#/, "").split("/");
  if (!MEDICAL_RECORD_SECTIONS.includes(section)) return null;
  if (section !== "screening") return { section, sub: null };
  return { section, sub: SCREENING_SUBS.includes(sub) ? sub : DEFAULT_SCREENING_SUB };
}

function medicalRecordHasMalariaForm(route) {
  return route !== null && route.section === "screening" && route.sub === "malaria";
}

// True when the route is a screening whose results screening-log.js can list.
function medicalRecordHasTestLog(route) {
  return route !== null && route.section === "screening" && SCREENING_LOGS.includes(route.sub);
}

function medicalRecordHash(route) {
  return "#" + route.section + (route.sub ? "/" + route.sub : "");
}

// What the main panel says for each view. Plain text only; it is written with
// textContent, never as HTML.
const MEDICAL_RECORD_TEXT = {
  prompt: { title: "Medical Record", sub: "Choose a record type on the left." },
  outpatient: { title: "Outpatient", sub: "Page still under development." },
  inpatient: { title: "Inpatient", sub: "Page still under development." },
  wellness: { title: "Wellness", sub: "Page still under development." },
  alcohol: { title: "Screening · Drug and Alcohol", sub: "Drug and alcohol test results uploaded from the consent and result form." },
  fatigue: { title: "Screening · Fatigue", sub: "Fatigue scoring results uploaded from the fatigue scoring form." },
  malaria: { title: "Screening · Malaria", sub: "Enter a malaria test. The slide code to write on the blood slide appears after saving." },
};

// The heading is drawn in two colours, like the "hse medpost" logo: the first
// part dark, the rest in the accent colour. "Screening · Fatigue" splits at the
// dot (section, then sub-header); "Medical Record" after its first word; a single
// word stays in one colour. Returns [main, accent], accent "" when there is none.
function medicalRecordTitleParts(title) {
  const text = String(title);
  const dot = text.indexOf(" · ");
  if (dot >= 0) return [text.slice(0, dot + 2), text.slice(dot + 3)];
  const space = text.indexOf(" ");
  if (space >= 0) return [text.slice(0, space), text.slice(space + 1)];
  return [text, ""];
}

// medicalRecordTextKey picks the entry of MEDICAL_RECORD_TEXT for a route.
function medicalRecordTextKey(route) {
  if (!route) return "prompt";
  return route.sub || route.section;
}

let currentMedicalRecordLabel = "Medical Record";

// The presence banner shows what each signed-in person is looking at; the page
// passes this function to startPresence so the label follows the view.
function medicalRecordPresenceLabel() {
  return currentMedicalRecordLabel;
}

function initMedicalRecordPage() {
  const nav = document.getElementById("mr-nav");
  const title = document.getElementById("mr-title");
  const sub = document.getElementById("mr-sub");
  const screening = document.getElementById("mr-panel-screening");
  const malaria = document.getElementById("mr-panel-malaria");
  const label = document.getElementById("mr-label");

  // Writes the heading as two spans (textContent only, never HTML).
  function setTitle(text) {
    const [main, accent] = medicalRecordTitleParts(text);
    const mainEl = document.createElement("span");
    mainEl.className = "mr-title-main";
    mainEl.textContent = main;
    title.replaceChildren(mainEl);
    if (accent) {
      const accentEl = document.createElement("span");
      accentEl.className = "mr-title-accent";
      accentEl.textContent = accent;
      title.append(" ", accentEl);
    }
  }

  const anySectionOpen = () => MEDICAL_RECORD_SECTIONS.some((s) => !document.getElementById("mr-children-" + s).hidden);

  // The sidebar title toggles everything: if any section is open it closes them
  // all, otherwise it opens them all. It has no triangle of its own; aria-expanded
  // tells screen readers the state.
  function refreshLabel() {
    label.setAttribute("aria-expanded", String(anySectionOpen()));
  }

  // Every section expands and collapses on its own triangle. A section with
  // nothing inside yet says so, the way an Activity Log program with no
  // sub-headers does.
  function setOpen(section, open) {
    const toggle = nav.querySelector(`[data-toggle="${section}"]`);
    const children = document.getElementById("mr-children-" + section);
    children.hidden = !open;
    toggle.setAttribute("aria-expanded", String(open));
    toggle.textContent = open ? "▾" : "▸";
    refreshLabel();
  }

  function render() {
    const route = parseMedicalRecordRoute(location.hash);
    const text = MEDICAL_RECORD_TEXT[medicalRecordTextKey(route)];
    setTitle(text.title);
    sub.textContent = text.sub;
    currentMedicalRecordLabel = text.title;

    // A section row is active for its whole section; a sub-header row only for
    // its own sub. So Screening and the open sub-header are highlighted together.
    for (const el of nav.querySelectorAll("[data-section]")) {
      const inSection = route !== null && el.dataset.section === route.section;
      el.classList.toggle("active", el.dataset.sub ? inSection && el.dataset.sub === route.sub : inSection);
    }
    if (route) setOpen(route.section, true);

    // Only a sub-header with results to list shows the table. Every other view
    // is just the heading and its one line of text.
    screening.hidden = !medicalRecordHasTestLog(route);
    if (medicalRecordHasTestLog(route)) window.loadScreening?.(route.sub);
    malaria.hidden = !medicalRecordHasMalariaForm(route);
    if (medicalRecordHasMalariaForm(route)) window.loadMalaria?.();
  }

  nav.addEventListener("click", (e) => {
    const toggle = e.target.closest("[data-toggle]");
    if (toggle) {
      const children = document.getElementById("mr-children-" + toggle.dataset.toggle);
      setOpen(toggle.dataset.toggle, children.hidden);
      return;
    }
    const target = e.target.closest("[data-section]");
    if (!target) return;
    const route = parseMedicalRecordRoute("#" + target.dataset.section + (target.dataset.sub ? "/" + target.dataset.sub : ""));
    // A section title toggles its sub-headers: the first click opens it and shows
    // its view, the next click collapses it (and leaves the view as it is). A
    // sub-header row inside never collapses anything; it just opens its view.
    const isSectionTitle = !target.dataset.sub;
    if (isSectionTitle && !document.getElementById("mr-children-" + route.section).hidden) {
      setOpen(route.section, false);
      return;
    }
    location.hash = medicalRecordHash(route);
    // Clicking a section whose view is already open changes no hash, so
    // nothing redraws; open it here in case its triangle had closed it.
    setOpen(route.section, true);
  });
  function toggleAllSections() {
    const open = !anySectionOpen();
    for (const section of MEDICAL_RECORD_SECTIONS) setOpen(section, open);
  }
  label.addEventListener("click", toggleAllSections);
  label.addEventListener("keydown", (e) => {
    if (e.key !== "Enter" && e.key !== " ") return;
    e.preventDefault();
    toggleAllSections();
  });
  window.addEventListener("hashchange", render);
  render();
}

// Without a document (the Node tests load this file to reach the route helpers)
// there is nothing to wire up.
if (typeof document !== "undefined") initMedicalRecordPage();
