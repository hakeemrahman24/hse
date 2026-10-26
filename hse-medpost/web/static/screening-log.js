// Medical Record > Screening > Drug and Alcohol / Fatigue: each shows a Table tab
// and a Dashboard tab, the same layout as the malaria page. The two tests share
// this file; SCREENING_KINDS says how they differ. emr.js calls
// window.loadScreening(kind) when one of them is opened. Loaded after malaria.js,
// whose barPercent and addClearButton it reuses, and after clinic-date.js.
//
// The data comes from GET /api/screening-log (rows), /api/screening-log.xlsx
// (export) and /api/screening-log/summary (dashboard), all filtered by the date
// the test was done and, for the table and export, by a name search.

// How each test differs. `summary` are the table's columns, named as the API's
// headers; every other column is in the row's expanded detail. `results` are the
// outcomes the dashboard counts, in display order, with the key the API uses.
// `alert` are the outcomes worth spelling out next to a bar.
const SCREENING_KINDS = {
  alcohol: {
    noun: "tests",
    doneLabel: "Tests done",
    summary: ["Test date", "Name", "Company", "Position", "Test type", "Final result"],
    resultHeader: "Final result",
    results: [
      { key: "negative", label: "Negative" },
      { key: "positive", label: "Positive" },
      { key: "lab", label: "Sent to lab" },
      { key: "other", label: "Not stated" },
    ],
    alert: ["positive", "lab"],
    groups: [
      { value: "company", label: "Company" },
      { value: "position", label: "Position" },
      { value: "testType", label: "Test type" },
    ],
    emptyText: "No alcohol tests match this filter.",
  },
  fatigue: {
    noun: "assessments",
    doneLabel: "Assessments done",
    summary: ["Exam date", "Name", "Company", "Position", "Risk level", "Score total"],
    resultHeader: "Risk level",
    results: [
      { key: "low", label: "Low" },
      { key: "mild", label: "Mild" },
      { key: "medium", label: "Medium" },
      { key: "high", label: "High" },
      { key: "other", label: "Not stated" },
    ],
    alert: ["mild", "medium", "high"],
    groups: [
      { value: "company", label: "Company" },
      { value: "position", label: "Position" },
      { value: "examiner", label: "Examiner" },
    ],
    emptyText: "No fatigue assessments match this filter.",
  },
};

const TEST_TYPE_LABELS = {
  random: "Random",
  pre: "Pre-employment",
  post: "Post-incident",
  suspicion: "Reasonable suspicion",
  return: "Return to duty",
};

// What the forms store, and the outcome each value means. Case and spaces do not
// matter. The server counts the same way.
const RESULT_CODES = {
  alcohol: { neg: "negative", negative: "negative", pos: "positive", positive: "positive", lab: "lab", laboratory: "lab" },
  fatigue: { low: "low", mild: "mild", medium: "medium", high: "high" },
};

// Which outcome a stored value is; a blank or unknown value is "other".
function screeningResultKey(kind, raw) {
  const codes = RESULT_CODES[kind] || {};
  return codes[String(raw || "").trim().toLowerCase()] || "other";
}

// The outcome spelled out ("pos" is "Positive"); an unknown value is shown as typed.
function screeningResultLabel(kind, raw) {
  const key = screeningResultKey(kind, raw);
  if (key === "other") return String(raw || "").trim();
  return SCREENING_KINDS[kind].results.find((r) => r.key === key).label;
}

// A stored UTC timestamp (YYYY-MM-DDTHH:MM:SS) as the clinic's local "YYYY-MM-DD HH:MM"
// (Asia/Jayapura). Anything that is not such a timestamp is returned as it is.
function clinicDateTime(stamp) {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})$/.exec(String(stamp || ""));
  if (!m) return String(stamp || "");
  const instant = new Date(Date.UTC(+m[1], +m[2] - 1, +m[3], +m[4], +m[5], +m[6]));
  const parts = new Intl.DateTimeFormat("en-CA", {
    timeZone: "Asia/Jayapura",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23",
  }).formatToParts(instant);
  const part = (type) => parts.find((p) => p.type === type).value;
  return part("year") + "-" + part("month") + "-" + part("day") + " " + part("hour") + ":" + part("minute");
}

// One table or detail value, as shown. The alcohol test date is a UTC timestamp,
// shown in the clinic's time; results and test types are spelled out; any other
// stored timestamp is marked as UTC.
function screeningDisplayValue(kind, header, raw) {
  const value = raw === null || raw === undefined ? "" : String(raw);
  if (value === "") return "";
  if (kind === "alcohol" && header === "Test date") return clinicDateTime(value);
  if (header === SCREENING_KINDS[kind].resultHeader) return screeningResultLabel(kind, value);
  if (header === "Test type") return TEST_TYPE_LABELS[value.trim().toLowerCase()] || value;
  if (/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}$/.test(value)) return value.replace("T", " ") + " UTC";
  return value;
}

// The value of one summary column of one row, as shown. A fatigue record whose exam
// date was left blank shows the day it was created instead (the same fallback the
// server uses to filter and sort), so the first column, which opens the row, is
// never empty.
function screeningCellValue(kind, headers, row, header) {
  const raw = row[headers.indexOf(header)];
  if (kind === "fatigue" && header === "Exam date" && String(raw || "").trim() === "") {
    return String(row[headers.indexOf("Created at")] || "").slice(0, 10);
  }
  return screeningDisplayValue(kind, header, raw);
}

// The label/value pairs shown when a row is opened: every column the table leaves out.
function screeningDetailFields(kind, headers, row) {
  const summary = SCREENING_KINDS[kind].summary;
  const fields = [];
  headers.forEach((header, i) => {
    if (!summary.includes(header)) fields.push([header, screeningDisplayValue(kind, header, row[i])]);
  });
  return fields;
}

// A group's name on the chart: a test type spelled out, a blank as "(not stated)".
function screeningGroupName(by, name) {
  if (name === "" || name === null || name === undefined) return "(not stated)";
  return by === "testType" ? TEST_TYPE_LABELS[String(name).trim().toLowerCase()] || name : name;
}

// Pieces of one stacked bar: one {key, x, width} per outcome that has cases, laid
// end to end from the left in the order of `keys`, sized against the longest bar.
function screeningSegments(results, keys, max) {
  const segments = [];
  let x = 0;
  for (const key of keys) {
    const width = barPercent((results || {})[key], max);
    if (width === 0) continue;
    segments.push({ key, x, width: Math.min(width, 100 - x) });
    x += width;
  }
  return segments;
}

// "Positive 1, Sent to lab 2" for the outcomes worth spelling out that have cases.
function screeningAlertLabel(kind, results) {
  const config = SCREENING_KINDS[kind];
  return config.alert
    .filter((key) => (results || {})[key] > 0)
    .map((key) => config.results.find((r) => r.key === key).label + " " + results[key])
    .join(", ");
}

function initScreeningLog() {
  const panel = document.getElementById("mr-panel-screening");
  if (!panel) return;
  const $ = (id) => document.getElementById(id);
  const els = {
    tabs: [$("scr-tab-table"), $("scr-tab-sofi"), $("scr-tab-dashboard")],
    views: { table: $("scr-view-table"), sofi: $("scr-view-sofi"), dashboard: $("scr-view-dashboard") },
    search: $("scr-search"),
    from: $("scr-from"),
    to: $("scr-to"),
    reset: $("scr-filter-reset"),
    exportLink: $("scr-export"),
    head: $("scr-head"),
    body: $("scr-body"),
    empty: $("scr-empty"),
    error: $("scr-error"),
    dashFilter: $("scr-dash-filter"),
    dashFrom: $("scr-dash-from"),
    dashTo: $("scr-dash-to"),
    dashBy: $("scr-dash-by"),
    dashReset: $("scr-dash-reset"),
    dashError: $("scr-dash-error"),
    stats: $("scr-stats"),
    chartTitle: $("scr-chart-title"),
    chartHint: $("scr-chart-hint"),
    bars: $("scr-bars"),
    chartEmpty: $("scr-chart-empty"),
    legend: $("scr-legend"),
  };

  let kind = null; // "alcohol" or "fatigue"
  let headers = [];
  let rows = [];
  let openIndex = null;
  let tableSeq = 0;
  let dashboardStale = true;

  async function getJSON(url) {
    const response = await fetch(url, { credentials: "same-origin" });
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

  function filterParams(from, to, withSearch) {
    const params = new URLSearchParams({ test: kind });
    if (withSearch && els.search.value.trim()) params.set("q", els.search.value.trim());
    if (from.value) params.set("from", from.value);
    if (to.value) params.set("to", to.value);
    return params;
  }

  // ---- tabs ----------------------------------------------------------------
  function showView(name) {
    for (const tab of els.tabs) {
      const on = tab.dataset.view === name;
      tab.classList.toggle("active", on);
      tab.setAttribute("aria-selected", String(on));
    }
    for (const [view, el] of Object.entries(els.views)) el.hidden = view !== name;
    if (name === "dashboard" && dashboardStale) loadDashboard();
  }
  for (const tab of els.tabs) tab.addEventListener("click", () => showView(tab.dataset.view));

  // ---- table ---------------------------------------------------------------
  function cell(text) {
    const td = document.createElement("td");
    td.textContent = text;
    return td;
  }

  function resultCell(header, raw) {
    const td = document.createElement("td");
    const span = document.createElement("span");
    span.className = "scr-result scr-result--" + screeningResultKey(kind, raw);
    span.textContent = screeningDisplayValue(kind, header, raw) || "Not stated";
    td.append(span);
    return td;
  }

  function detailRow(index) {
    const tr = document.createElement("tr");
    tr.className = "detail-row";
    const td = document.createElement("td");
    td.colSpan = SCREENING_KINDS[kind].summary.length;
    const grid = document.createElement("div");
    grid.className = "ro-grid";
    for (const [label, value] of screeningDetailFields(kind, headers, rows[index])) {
      const field = document.createElement("label");
      const caption = document.createElement("span");
      caption.textContent = label;
      const shown = document.createElement("div");
      shown.className = "ro-value";
      shown.textContent = value === "" ? "—" : value;
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
    openIndex = null;
  }

  // The first column is a link: it opens the rest of the record in a row below,
  // turning bold red while open, and closes it again on a second click.
  function firstCell(tr, header, index) {
    const td = document.createElement("td");
    const link = document.createElement("a");
    link.href = "#";
    link.textContent = screeningCellValue(kind, headers, rows[index], header);
    link.addEventListener("click", (event) => {
      event.preventDefault();
      if (openIndex === index) return closeOpenRow();
      closeOpenRow();
      link.classList.add("is-expanded-link");
      tr.after(detailRow(index));
      openIndex = index;
    });
    td.append(link);
    return td;
  }

  function drawTable() {
    const config = SCREENING_KINDS[kind];
    const columns = config.summary.map((header) => headers.indexOf(header));
    const headRow = document.createElement("tr");
    for (const header of config.summary) {
      const th = document.createElement("th");
      th.textContent = header;
      headRow.append(th);
    }
    els.head.replaceChildren(headRow);
    els.body.replaceChildren();
    openIndex = null;
    rows.forEach((row, index) => {
      const tr = document.createElement("tr");
      config.summary.forEach((header, c) => {
        if (c === 0) tr.append(firstCell(tr, header, index));
        else if (header === config.resultHeader) tr.append(resultCell(header, row[columns[c]]));
        else tr.append(cell(screeningCellValue(kind, headers, row, header)));
      });
      els.body.append(tr);
    });
    els.empty.textContent = els.search.value.trim() ? "No " + config.noun + " match this name and these dates." : config.emptyText;
    els.empty.hidden = rows.length > 0;
  }

  async function loadTable() {
    const seq = ++tableSeq;
    const params = filterParams(els.from, els.to, true);
    els.exportLink.href = "/api/screening-log.xlsx?" + params;
    els.error.hidden = true;
    try {
      const data = await getJSON("/api/screening-log?" + params);
      if (seq !== tableSeq) return; // a newer request has already replaced this one
      headers = data.headers;
      rows = data.rows;
      drawTable();
    } catch (err) {
      els.error.textContent = err.message;
      els.error.hidden = false;
    }
  }

  const SEARCH_DELAY_MS = 250;
  let searchTimer = null;
  addClearButton(els.search, () => {
    els.search.value = "";
    loadTable();
  });
  els.search.addEventListener("input", () => {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(loadTable, SEARCH_DELAY_MS);
  });
  els.search.addEventListener("keydown", (event) => {
    if (event.key === "Enter") event.preventDefault();
  });
  for (const input of [els.from, els.to]) input.addEventListener("change", loadTable);

  function clearDates(...inputs) {
    for (const input of inputs) {
      input.value = "";
      input.dispatchEvent(new Event("input", { bubbles: true })); // redraws the dd/mm/yyyy box
    }
  }
  els.reset.addEventListener("click", () => {
    els.search.value = "";
    clearDates(els.from, els.to);
    loadTable();
  });

  // ---- dashboard -----------------------------------------------------------
  function svgStack(segments) {
    const ns = "http://www.w3.org/2000/svg";
    const svg = document.createElementNS(ns, "svg");
    svg.setAttribute("viewBox", "0 0 100 12");
    svg.setAttribute("preserveAspectRatio", "none");
    svg.setAttribute("aria-hidden", "true");
    for (const piece of segments) {
      const rect = document.createElementNS(ns, "rect");
      rect.setAttribute("class", "scr-bar-" + piece.key);
      rect.setAttribute("x", String(piece.x));
      rect.setAttribute("y", "0");
      rect.setAttribute("width", String(piece.width));
      rect.setAttribute("height", "12");
      svg.append(rect);
    }
    return svg;
  }

  function tile(label, value, className) {
    const box = document.createElement("div");
    box.className = "mal-stat" + (className ? " " + className : "");
    const caption = document.createElement("span");
    caption.textContent = label;
    const number = document.createElement("strong");
    number.textContent = String(value);
    box.append(caption, number);
    return box;
  }

  // The parts of the dashboard that depend on which test it is: the "Chart by"
  // choices and the legend.
  function setUpDashboard() {
    const config = SCREENING_KINDS[kind];
    els.dashBy.replaceChildren(
      ...config.groups.map((g) => {
        const option = document.createElement("option");
        option.value = g.value;
        option.textContent = g.label;
        return option;
      }),
    );
    els.legend.replaceChildren();
    for (const result of config.results) {
      const swatch = document.createElement("i");
      swatch.className = "mal-swatch scr-swatch--" + result.key;
      els.legend.append(swatch, " " + result.label + " ");
    }
  }

  function drawDashboard(data) {
    const config = SCREENING_KINDS[kind];
    els.stats.replaceChildren(
      tile(config.doneLabel, data.total),
      tile("People", data.people),
      // "Not stated" (a blank or unknown result) gets a tile only when there is one.
      ...config.results
        .filter((r) => r.key !== "other" || data.results.other > 0)
        .map((r) => tile(r.label, data.results[r.key] || 0, "scr-stat--" + r.key)),
    );
    const group = config.groups.find((g) => g.value === data.by);
    const noun = config.noun.charAt(0).toUpperCase() + config.noun.slice(1);
    els.chartTitle.textContent = noun + " by " + group.label.toLowerCase();
    els.chartHint.textContent = data.moreGroups > 0 ? "Showing the " + data.groups.length + " busiest; " + data.moreGroups + " more not shown." : "";
    els.bars.replaceChildren();
    els.chartEmpty.textContent = "No " + config.noun + " in this period.";
    els.chartEmpty.hidden = data.groups.length > 0;
    const max = Math.max(0, ...data.groups.map((g) => g.count));
    const keys = config.results.map((r) => r.key);
    const wrap = document.createElement("div");
    wrap.className = "dash-bars";
    for (const g of data.groups) {
      const row = document.createElement("div");
      row.className = "dash-bar-row";
      const label = document.createElement("span");
      const name = screeningGroupName(data.by, g.name);
      label.textContent = name;
      label.title = name;
      const count = document.createElement("b");
      const alert = screeningAlertLabel(kind, g.results);
      count.textContent = alert ? g.count + " (" + alert + ")" : String(g.count);
      row.append(label, svgStack(screeningSegments(g.results, keys, max)), count);
      wrap.append(row);
    }
    if (data.groups.length > 0) els.bars.append(wrap);
  }

  async function loadDashboard() {
    const params = filterParams(els.dashFrom, els.dashTo, false);
    params.set("by", els.dashBy.value);
    els.dashError.hidden = true;
    try {
      drawDashboard(await getJSON("/api/screening-log/summary?" + params));
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
  els.dashReset.addEventListener("click", () => {
    clearDates(els.dashFrom, els.dashTo);
    els.dashBy.selectedIndex = 0;
    loadDashboard();
  });

  // da-form.js calls this after it saves a record: the table shows the new row
  // and the dashboard is drawn again the next time it is opened.
  window.refreshScreening = function () {
    dashboardStale = true;
    return kind ? loadTable() : Promise.resolve();
  };

  // emr.js calls this whenever a test's view is shown. Switching to the other
  // test starts it clean: no search, no dates, the Table tab.
  window.loadScreening = function (nextKind) {
    if (!SCREENING_KINDS[nextKind]) return Promise.resolve();
    if (nextKind !== kind) {
      kind = nextKind;
      // Fatigue's first tab holds the sleep-score records and it also gets a SOFI tab;
      // Drug and Alcohol keeps a plain Table.
      els.tabs[0].textContent = kind === "fatigue" ? "Sleep Score" : "Table";
      $("scr-tab-sofi").hidden = kind !== "fatigue"; // by id, not by position: the tab list order must not matter
      els.search.value = "";
      clearDates(els.from, els.to, els.dashFrom, els.dashTo);
      setUpDashboard();
      dashboardStale = true;
      showView("table");
    }
    return loadTable();
  };
}

// Without a document (the Node tests load this file to reach the helpers above)
// there is nothing to wire up.
if (typeof document !== "undefined") initScreeningLog();
