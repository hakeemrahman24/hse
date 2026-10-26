// Dashboard charts are inline SVG. The summary PDF is drawn on the server
// (internal/entrymcu/dashboard_pdf.go) so the Orange Pi 3B does not parse a
// chart or PDF library in the browser.

let dashAnalytics = null;
let dashChartType = "bar";
let dashStatusView = "combined";
let dashDatesReady = false;
let dashApplied = { from: "", to: "" };

function dashISO(d) {
  const month = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${d.getFullYear()}-${month}-${day}`;
}

function dashThisMonth() {
  const today = clinicToday();
  return { from: `${today.slice(0, 8)}01`, to: today };
}

function dashSafeToken(s) {
  const trimmed = String(s || "").trim();
  if (!trimmed) return "na";
  let out = "";
  let gap = false;
  for (const ch of trimmed) {
    if (/[A-Za-z0-9_-]/.test(ch)) {
      if (gap && out) out += "_";
      gap = false;
      out += ch;
    } else {
      gap = true;
    }
  }
  return out || "na";
}

function dashPdfFilename(from, to) {
  return `MCU_dashboard_${dashSafeToken(from)}_${dashSafeToken(to)}.pdf`;
}

function dashInput(form, name) {
  return form?.elements?.namedItem(name);
}

function ensureDashDates() {
  if (dashDatesReady) return;
  dashDatesReady = true;
  const form = document.getElementById("report-filter");
  const from = dashInput(form, "from");
  const to = dashInput(form, "to");
  if (!from || !to || from.value || to.value) return;
  const range = dashThisMonth();
  from.value = range.from;
  to.value = range.to;
}

function emptyDash() {
  return {
    total: 0,
    closedCount: 0,
    closedNeedFollowUpExcludedCount: 0,
    renewedExcludedCount: 0,
    supersededExcludedCount: 0,
    closedNoResultCriteriaCount: 0,
    statusRows: [],
    purposeRows: [],
    resultCriteriaRows: [],
    resultCriteriaTotal: 0,
    companies: [],
    companyPivot: [],
    companyStatusRows: {},
  };
}

function dashView() {
  const analytics = dashAnalytics || emptyDash();
  const company = document.getElementById("dash-company")?.value || "";
  const perCompany = dashChartType === "pie" && dashStatusView === "perCompany";
  if (!company || perCompany) return { ...analytics, company: "", perCompany };
  const block = (analytics.companyPivot || []).find((item) => item.company === company);
  const statusRows = (analytics.companyStatusRows || {})[company] || [];
  const resultRows = block?.resultCriteriaRows || [];
  const closed = statusRows.filter((row) => row.key === "Close").reduce((sum, row) => sum + row.count, 0);
  const resultTotal = resultRows.reduce((sum, row) => sum + row.count, 0);
  return {
    ...analytics,
    total: block?.total || 0,
    closedCount: closed,
    closedNoResultCriteriaCount: block?.closedNoResultCriteriaCount || 0,
    statusRows,
    purposeRows: block?.purposeRows || [],
    resultCriteriaRows: resultRows,
    resultCriteriaTotal: resultTotal,
    companyPivot: block ? [block] : [],
    companies: block ? [company] : [],
    company,
    perCompany: false,
  };
}

let dashCompanyNames = [];

function setDashCompany(name) {
  const hidden = document.getElementById("dash-company");
  const input = document.getElementById("dash-company-input");
  const next = name && dashCompanyNames.includes(name) ? name : "";
  if (hidden) hidden.value = next;
  if (input) input.value = next;
}

function fillDashCompanies(companies) {
  dashCompanyNames = companies || [];
  setDashCompany(document.getElementById("dash-company")?.value || "");
}

function dashCompanyMatches(query) {
  const q = query.trim().toLowerCase();
  const names = dashCompanyNames.filter((name) => !q || name.toLowerCase().includes(q));
  return { all: !q || "all companies".includes(q), names: names.slice(0, 20) };
}

function syncDashCompanyFromInput() {
  const input = document.getElementById("dash-company-input");
  const typed = (input?.value || "").trim();
  if (!typed) {
    setDashCompany("");
    return true;
  }
  const exact = dashCompanyNames.find((name) => name.toLowerCase() === typed.toLowerCase());
  if (!exact) return false;
  setDashCompany(exact);
  return true;
}

function renderDashCompanyList(query) {
  const box = document.getElementById("dash-company-typeahead");
  if (!box) return;
  const { all, names } = dashCompanyMatches(query);
  const rows = [];
  if (all) rows.push(`<button type="button" class="app-program-item" data-dash-company="">All companies</button>`);
  for (const name of names) {
    rows.push(`<button type="button" class="app-program-item" data-dash-company="${escapeHtml(name)}">${escapeHtml(name)}</button>`);
  }
  if (!rows.length) {
    box.hidden = true;
    box.innerHTML = "";
    return;
  }
  box.innerHTML = rows.join("");
  box.hidden = false;
  box.querySelectorAll("[data-dash-company]").forEach((btn) => {
    btn.addEventListener("click", () => {
      setDashCompany(btn.dataset.dashCompany || "");
      box.hidden = true;
    });
  });
}

function attachDashCompanyTypeahead() {
  const input = document.getElementById("dash-company-input");
  const box = document.getElementById("dash-company-typeahead");
  if (!input || !box) return;
  let highlighted = -1;

  function choices() {
    return Array.from(box.querySelectorAll("[data-dash-company]"));
  }

  function highlight(index) {
    const els = choices();
    if (!els.length) {
      highlighted = -1;
      return;
    }
    highlighted = (index + els.length) % els.length;
    els.forEach((el, i) => el.classList.toggle("active", i === highlighted));
    els[highlighted].scrollIntoView({ block: "nearest" });
  }

  function commit(index) {
    const els = choices();
    if (!els.length) return false;
    const btn = els[index >= 0 ? index : 0];
    setDashCompany(btn.dataset.dashCompany || "");
    box.hidden = true;
    highlighted = -1;
    return true;
  }

  input.addEventListener("input", () => {
    highlighted = -1;
    renderDashCompanyList(input.value);
    if (!input.value.trim()) setDashCompany("");
  });
  input.addEventListener("focus", () => renderDashCompanyList(input.value));
  input.addEventListener("keydown", (e) => {
    if (box.hidden) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      highlight(highlighted + 1);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      highlight(highlighted - 1);
    } else if (e.key === "Enter") {
      e.preventDefault();
      commit(highlighted);
    } else if (e.key === "Escape") {
      box.hidden = true;
      highlighted = -1;
    }
  });
  document.addEventListener("click", (e) => {
    if (!box.contains(e.target) && e.target !== input) box.hidden = true;
  });
}

attachDashCompanyTypeahead();

function updateDashToggles() {
  document.getElementById("dash-toggle-bar")?.classList.toggle("is-on", dashChartType === "bar");
  document.getElementById("dash-toggle-pie")?.classList.toggle("is-on", dashChartType === "pie");
  const showStatus = dashChartType === "pie";
  const statusToggle = document.getElementById("dash-status-view");
  const statusRule = document.getElementById("dash-status-rule");
  if (statusToggle) statusToggle.hidden = !showStatus;
  if (statusRule) statusRule.hidden = !showStatus;
  document.getElementById("dash-toggle-combined")?.classList.toggle("is-on", dashStatusView !== "perCompany");
  document.getElementById("dash-toggle-per-company")?.classList.toggle("is-on", dashStatusView === "perCompany");
  const companyField = document.getElementById("dash-company-field");
  if (companyField) companyField.hidden = dashChartType === "pie" && dashStatusView === "perCompany";
  syncDashReset();
}

function syncDashReset() {
  const form = document.getElementById("report-filter");
  const from = dashInput(form, "from")?.value || "";
  const to = dashInput(form, "to")?.value || "";
  const range = dashThisMonth();
  document.getElementById("report-this-month")?.classList.toggle("is-on", from === range.from && to === range.to);
  const btn = document.getElementById("dash-reset");
  if (!btn) return;
  const on = !!(from || to);
  btn.classList.toggle("is-filtered", on);
  btn.setAttribute("aria-pressed", on ? "true" : "false");
}

function setDashChartType(type) {
  dashChartType = type === "pie" ? "pie" : "bar";
  if (dashChartType !== "pie") dashStatusView = "combined";
  updateDashToggles();
  renderDashCharts();
  updateDashPdfLinks();
}

function setDashStatusView(mode) {
  dashStatusView = mode === "perCompany" ? "perCompany" : "combined";
  if (dashStatusView === "perCompany") setDashCompany("");
  updateDashToggles();
  renderDashCharts();
  updateDashPdfLinks();
}

function updateDashPdfLinks() {
  const open = document.getElementById("dash-pdf-open");
  const download = document.getElementById("dash-pdf-download");
  if (!open || !download) return;
  const view = dashView();
  const show = (dashAnalytics?.total || 0) > 0 && view.total > 0;
  if (!show) {
    open.hidden = true;
    download.hidden = true;
    return;
  }
  const params = new URLSearchParams();
  if (dashApplied.from) params.set("from", dashApplied.from);
  if (dashApplied.to) params.set("to", dashApplied.to);
  if (view.company) params.set("company", view.company);
  params.set("chart", dashChartType);
  const file = dashPdfFilename(dashApplied.from, dashApplied.to);
  const base = `/api/mcu-reports/summary.pdf?${params.toString()}`;
  open.hidden = true;
  download.hidden = false;
  open.href = base;
  download.href = `${base}&download=1`;
  download.download = file;
  download.title = file;
}

function sumDash(rows) {
  return (rows || []).reduce((sum, row) => sum + row.count, 0);
}

function dashSwatchRows(rows, total, withShare) {
  return (rows || [])
    .map((row) => {
      const share = total ? `${((row.count / total) * 100).toFixed(1)}%` : "0.0%";
      const shareCell = withShare ? `<td class="num">${share}</td>` : "";
      return `<tr><td><span class="dash-swatch" style="background:${escapeHtml(row.color)}"></span>${escapeHtml(row.label)}</td><td class="num">${row.count}</td>${shareCell}</tr>`;
    })
    .join("");
}

function barChartSVG(rows) {
  const max = Math.max(...rows.map((row) => row.count), 1);
  const body = rows
    .map((row) => {
      const pct = Math.max(2, Math.round((row.count / max) * 100));
      return `<div class="dash-bar-row"><span title="${escapeHtml(row.label)}">${escapeHtml(row.label)}</span><svg viewBox="0 0 100 12" preserveAspectRatio="none" aria-hidden="true"><rect x="0" y="0" width="${pct}" height="12" fill="${escapeHtml(row.color)}"></rect></svg><b>${row.count}</b></div>`;
    })
    .join("");
  return `<div class="dash-bars">${body}</div>`;
}

function pieChartSVG(rows) {
  const total = sumDash(rows);
  if (!total) return "";
  if (rows.length === 1) {
    return `<svg class="dash-pie" viewBox="0 0 100 100" role="img"><circle cx="50" cy="50" r="46" fill="${escapeHtml(rows[0].color)}"></circle></svg>`;
  }
  let angle = -Math.PI / 2;
  const paths = rows
    .map((row) => {
      const sweep = (row.count / total) * Math.PI * 2;
      const start = angle;
      const end = angle + sweep;
      angle = end;
      const x1 = 50 + 46 * Math.cos(start);
      const y1 = 50 + 46 * Math.sin(start);
      const x2 = 50 + 46 * Math.cos(end);
      const y2 = 50 + 46 * Math.sin(end);
      const large = sweep > Math.PI ? 1 : 0;
      return `<path d="M 50 50 L ${x1.toFixed(2)} ${y1.toFixed(2)} A 46 46 0 ${large} 1 ${x2.toFixed(2)} ${y2.toFixed(2)} Z" fill="${escapeHtml(row.color)}"></path>`;
    })
    .join("");
  return `<svg class="dash-pie" viewBox="0 0 100 100" role="img">${paths}</svg>`;
}

function chartMarkup(rows) {
  if (!rows?.length) return `<p class="app-empty">No data.</p>`;
  return dashChartType === "pie" ? pieChartSVG(rows) : barChartSVG(rows);
}

function chartLegend(rows) {
  if (!rows?.length) return "";
  return `<div class="dash-legend">${rows.map((row) => `<span><i style="background:${escapeHtml(row.color)}"></i>${escapeHtml(row.label)}</span>`).join("")}</div>`;
}

function chartStage(rows) {
  const chart = chartMarkup(rows);
  if (dashChartType !== "pie" || !rows?.length) return chart;
  return `<div class="dash-chart-stage">${chart}${chartLegend(rows)}</div>`;
}

function statusCount(rows, key) {
  return (rows || []).filter((row) => row.key === key).reduce((sum, row) => sum + row.count, 0);
}

function statusPivotMarkup(analytics, company) {
  const names = company ? [company] : analytics.companies || [];
  const blocks = names
    .map((name) => {
      const rows = (analytics.companyStatusRows || {})[name] || [];
      const open = statusCount(rows, "Open");
      const close = statusCount(rows, "Close");
      const other = rows.filter((row) => row.key !== "Open" && row.key !== "Close").reduce((sum, row) => sum + row.count, 0);
      return { name, open, close, other, total: open + close + other };
    })
    .filter((block) => block.total > 0);
  if (!blocks.length) {
    return `<p class="app-empty">No Open or Close rows for this range.</p>`;
  }
  const showOther = blocks.some((block) => block.other > 0);
  const otherHead = showOther ? `<th class="num">Other</th>` : "";
  const head = `<tr><th>Company</th><th class="num">Open</th><th class="num">Close</th>${otherHead}<th class="num">Total</th></tr>`;
  const body = blocks
    .map((block) => {
      const otherCell = showOther ? `<td class="num">${block.other}</td>` : "";
      return `<tr><td>${escapeHtml(block.name)}</td><td class="num">${block.open}</td><td class="num">${block.close}</td>${otherCell}<td class="num">${block.total}</td></tr>`;
    })
    .join("");
  const sums = blocks.reduce(
    (acc, block) => ({
      open: acc.open + block.open,
      close: acc.close + block.close,
      other: acc.other + block.other,
      total: acc.total + block.total,
    }),
    { open: 0, close: 0, other: 0, total: 0 },
  );
  const share = (part) => (sums.total ? `${((part / sums.total) * 100).toFixed(1)}%` : "0.0%");
  const otherShare = showOther ? `<td class="num">${share(sums.other)}</td>` : "";
  let foot = "";
  if (blocks.length > 1) {
    const otherCell = showOther ? `<td class="num">${sums.other}</td>` : "";
    foot += `<tr class="dash-pivot-grand"><td>Total</td><td class="num">${sums.open}</td><td class="num">${sums.close}</td>${otherCell}<td class="num">${sums.total}</td></tr>`;
  }
  foot += `<tr class="dash-pivot-share"><td>Share of total</td><td class="num">${share(sums.open)}</td><td class="num">${share(sums.close)}</td>${otherShare}<td class="num">${share(sums.total)}</td></tr>`;
  return `<div class="dash-pivot-wrap"><table class="dash-pivot-table"><thead>${head}</thead><tbody>${body}${foot}</tbody></table></div>`;
}

function sectionCard(variant, title, extra, body) {
  return `<article class="dash-chart-card dash-chart-card--${variant}"><h3>${title}</h3>${extra || ""}${body}</article>`;
}

function perCompanyCharts(blocks) {
  const cards = blocks
    .filter((block) => sumDash(block.rows))
    .map(
      (block) =>
        `<div class="dash-per-company-card"><h4>${escapeHtml(block.name)}</h4><p>${sumDash(block.rows)} records</p>${pieChartSVG(block.rows)}</div>`,
    )
    .join("");
  return `<div class="dash-per-company-grid">${cards}</div>`;
}

function companyLegendTable(blocks, label) {
  const groups = blocks
    .filter((block) => sumDash(block.rows))
    .map((block) => {
      const total = sumDash(block.rows);
      return `<tbody><tr class="dash-pivot-company"><td colspan="3">${escapeHtml(block.name)}</td></tr>${dashSwatchRows(block.rows, total, true)}</tbody>`;
    })
    .join("");
  return `<table class="dash-detail-table"><thead><tr><th>${label}</th><th class="num">Count</th><th class="num">Share</th></tr></thead>${groups}</table>`;
}

function legendTable(rows, label, total) {
  return `<table class="dash-detail-table"><thead><tr><th>${label}</th><th class="num">Count</th><th class="num">Share</th></tr></thead><tbody>${dashSwatchRows(rows, total, true)}</tbody></table>`;
}

function sectionSplit(left, right, stacked) {
  const rightClass = stacked ? "dash-section-right dash-section-right--stack" : "dash-section-right";
  return `<div class="dash-section-split"><div class="dash-section-left">${left}</div><div class="${rightClass}">${right}</div></div>`;
}

function renderDashCharts() {
  const host = document.getElementById("dash-charts");
  const empty = document.getElementById("dash-empty");
  if (!host || !empty) return;
  const analytics = dashAnalytics || emptyDash();
  const view = dashView();
  if (!analytics.total || !view.total) {
    host.hidden = true;
    host.innerHTML = "";
    empty.hidden = false;
    empty.textContent = "No records in this review date range.";
    return;
  }
  empty.hidden = true;
  host.hidden = false;
  const who = view.company ? ` · ${escapeHtml(view.company)}` : "";
  const companies = view.perCompany ? analytics.companies || [] : [];
  const statusBlocks = companies.map((name) => ({ name, rows: (analytics.companyStatusRows || {})[name] || [] }));
  const purposeBlocks = (view.perCompany ? analytics.companyPivot : view.companyPivot) || [];
  const purposeGroups = purposeBlocks.map((block) => ({ name: block.company, rows: block.purposeRows || [] }));
  const resultGroups = purposeBlocks.map((block) => ({ name: block.company, rows: block.resultCriteriaRows || [] }));
  const purposeTotal = sumDash(view.purposeRows);
  const resultTotal = view.resultCriteriaTotal || sumDash(view.resultCriteriaRows);
  const statusRight = view.perCompany ? perCompanyCharts(statusBlocks) : chartStage(view.statusRows);
  const purposeLeft = view.perCompany ? companyLegendTable(purposeGroups, "Purpose") : legendTable(view.purposeRows, "Purpose", purposeTotal);
  const purposeRight = view.perCompany ? perCompanyCharts(purposeGroups) : chartStage(view.purposeRows);
  const resultLeft = view.perCompany ? companyLegendTable(resultGroups, "Result") : legendTable(view.resultCriteriaRows, "Result", resultTotal);
  const resultRight = view.perCompany ? perCompanyCharts(resultGroups) : chartStage(view.resultCriteriaRows);
  host.innerHTML = [
    `<p class="dash-cohort">${view.total} record${view.total === 1 ? "" : "s"}${who}</p>`,
    sectionCard("status", "Open / Close review", "", sectionSplit(statusPivotMarkup(analytics, view.company), statusRight, view.perCompany)),
    sectionCard("wide", "Purpose", "", sectionSplit(purposeLeft, purposeRight, view.perCompany)),
    sectionCard("wide", "Review result", "", sectionSplit(resultLeft, resultRight, view.perCompany)),
  ].join("");
}

async function loadReport() {
  const form = document.getElementById("report-filter");
  if (!form) return;
  ensureDashDates();
  const from = dashInput(form, "from")?.value || "";
  const to = dashInput(form, "to")?.value || "";
  const err = document.getElementById("dash-error");
  const apply = document.getElementById("dash-apply");
  if (err) {
    err.hidden = true;
    err.textContent = "";
  }
  if (from && to && from > to) {
    if (err) {
      err.hidden = false;
      err.textContent = "Review from must be on or before review to.";
    }
    return;
  }
  if (apply) apply.disabled = true;
  const params = new URLSearchParams();
  if (from) params.set("from", from);
  if (to) params.set("to", to);
  try {
    const res = await fetch(`/api/mcu-reports?${params.toString()}`, { credentials: "same-origin" });
    if (res.status === 401 || res.status === 403) return;
    if (!res.ok) {
      if (err) {
        err.hidden = false;
        err.textContent = "Could not load the dashboard.";
      }
      return;
    }
    const report = await res.json();
    dashApplied = { from, to };
    dashAnalytics = report.dashboard || emptyDash();
    fillDashCompanies(dashAnalytics.companies || []);
    updateDashToggles();
    renderDashCharts();
    updateDashPdfLinks();
  } finally {
    if (apply) apply.disabled = false;
  }
}

document.getElementById("report-filter")?.addEventListener("input", () => syncDashReset());
document.getElementById("report-filter")?.addEventListener("submit", (e) => {
  e.preventDefault();
  if (!syncDashCompanyFromInput()) {
    renderDashCompanyList(document.getElementById("dash-company-input")?.value || "");
    return;
  }
  loadReport();
});
document.getElementById("report-this-month")?.addEventListener("click", () => {
  const form = document.getElementById("report-filter");
  const from = dashInput(form, "from");
  const to = dashInput(form, "to");
  if (!from || !to) return;
  if (!syncDashCompanyFromInput()) {
    renderDashCompanyList(document.getElementById("dash-company-input")?.value || "");
    return;
  }
  const range = dashThisMonth();
  from.value = range.from;
  to.value = range.to;
  dashDatesReady = true;
  loadReport();
});
document.getElementById("dash-reset")?.addEventListener("click", () => {
  const form = document.getElementById("report-filter");
  const from = dashInput(form, "from");
  const to = dashInput(form, "to");
  if (from) from.value = "";
  if (to) to.value = "";
  setDashCompany("");
  dashDatesReady = true;
  loadReport();
});
document.getElementById("dash-toggle-bar")?.addEventListener("click", () => setDashChartType("bar"));
document.getElementById("dash-toggle-pie")?.addEventListener("click", () => setDashChartType("pie"));
document.getElementById("dash-toggle-combined")?.addEventListener("click", () => setDashStatusView("combined"));
document.getElementById("dash-toggle-per-company")?.addEventListener("click", () => setDashStatusView("perCompany"));
