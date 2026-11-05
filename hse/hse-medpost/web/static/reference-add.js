// "Add reference" dialog for the Reference tab (admins only; the server enforces
// that, the button is just hidden for everyone else). Two ways in, as decided in
// plan/emr-plan.md section 11:
//
//   Pick existing - show a document that is already in the library in this tab.
//                   Nothing is uploaded, so there is no second copy to keep in step.
//   Upload new    - a new file or web link; it is added to the library and to this tab.
//
// Uses the helpers in reference-tab.js (refEl, referenceRequest, referenceFileProblem, ...),
// which must be loaded first. Text from the server goes on the page with textContent only.

const REFERENCE_CATEGORY_ORDER = ["Forms", "Procedure", "SocializationMaterial", "Template"];

// One label above a control. Wrapping the control in the <label> ties them together for
// screen readers and makes a click on the text focus the field.
function referenceField(labelText, control) {
  return refEl("label", { class: "ref-field" }, refEl("span", { text: labelText }), control);
}

function referenceLibraryMeta(item) {
  const parts = [referenceTypeLabel(item.documentsType)];
  if (item.program) parts.push(item.program);
  parts.push(item.hasFile ? "File" : "Link");
  if (item.updatedDate) parts.push("Updated " + referenceDate(item.updatedDate));
  return parts.join(" · ");
}

// ---- Pick existing --------------------------------------------------------

function referencePickRow(ctx, item) {
  const add = refEl("button", {
    type: "button",
    class: "btn-primary ref-btn",
    text: item.inSection ? "Already here" : "Add",
    disabled: item.inSection,
  });
  add.addEventListener("click", async () => {
    add.disabled = true;
    const res = await referenceRequest(`/api/reference/${ctx.section}/pick`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ documentId: item.documentId }),
    });
    if (res.ok) return ctx.finish(`Added "${item.title}" to this tab.`);
    add.disabled = false;
    ctx.fail(referenceMessage(res, "Could not add it."));
  });
  const text = refEl("div", { class: "ref-main" }, refEl("strong", { text: item.title }), refEl("p", { class: "ref-meta", text: referenceLibraryMeta(item) }));
  return refEl("li", { class: "ref-item" + (item.inSection ? " ref-item--muted" : "") }, text, add);
}

function referencePickPanel(ctx) {
  let items = [];
  const list = refEl("ul", { class: "ref-list ref-pick-list" });
  const search = refEl("input", { type: "search", class: "ref-search", placeholder: "Search the library", "aria-label": "Search the library" });
  const render = () => {
    const shown = referenceFilterItems(items, search.value);
    list.replaceChildren(...(shown.length ? shown.map((item) => referencePickRow(ctx, item)) : [refEl("li", { class: "app-empty", text: "Nothing in the library matches." })]));
  };
  search.addEventListener("input", render);
  referenceRequest(`/api/reference/${ctx.section}/library`).then((res) => {
    if (!res.ok) return ctx.fail(referenceMessage(res, "Could not load the library."));
    items = res.body.items || [];
    render();
  });
  return refEl("div", { class: "ref-panel" }, search, list);
}

// ---- Upload new -----------------------------------------------------------

async function referenceLoadPrograms(select) {
  const res = await referenceRequest("/api/procedures");
  if (!res.ok || !Array.isArray(res.body)) return;
  for (const p of res.body) select.append(refEl("option", { value: String(p.procId), text: p.title || p.healthProcedure }));
}

function referenceNewFields() {
  const category = refEl("select", {}, REFERENCE_CATEGORY_ORDER.map((type) => refEl("option", { value: type, text: referenceTypeLabel(type) })));
  const program = refEl("select", {}, refEl("option", { value: "", text: "Choose a program" }));
  referenceLoadPrograms(program);
  const kindFile = refEl("input", { type: "radio", name: "ref-kind", value: "file", checked: true });
  const kindLink = refEl("input", { type: "radio", name: "ref-kind", value: "link" });
  return {
    category,
    program,
    kindFile,
    kindLink,
    title: refEl("input", { type: "text", maxlength: "200", required: true }),
    notes: refEl("textarea", { rows: "2", maxlength: "500" }),
    file: refEl("input", { type: "file", accept: REFERENCE_FILE_ACCEPT }),
    link: refEl("input", { type: "url", placeholder: "https://" }),
  };
}

// Builds what the server expects, or returns { problem } when something is missing.
// The server checks all of this again; here it only saves a round trip.
function referenceNewFormData(f) {
  const isFile = f.kindFile.checked;
  const isTemplate = f.category.value === "Template";
  if (!f.title.value.trim()) return { problem: "Enter a title." };
  if (!isTemplate && !f.program.value) return { problem: "Choose a program (or pick the Template category for a site-wide document)." };
  if (isFile && referenceFileProblem(f.file.files[0])) return { problem: referenceFileProblem(f.file.files[0]) };
  if (!isFile && !referenceSafeHref(f.link.value)) return { problem: "Enter a web address starting with http:// or https://." };
  const form = new FormData();
  form.append("kind", isFile ? "file" : "link");
  form.append("documentsType", f.category.value);
  form.append("documentTitle", f.title.value.trim());
  form.append("notes", f.notes.value.trim());
  if (!isTemplate) form.append("procId", f.program.value);
  if (isFile) form.append("file", f.file.files[0]);
  else form.append("formLink", referenceSafeHref(f.link.value));
  return { form };
}

function referenceNewPanel(ctx) {
  const f = referenceNewFields();
  const programField = referenceField("Program", f.program);
  const fileField = referenceField("File (PDF, DOCX, XLSX, PNG or JPG, up to 10 MB)", f.file);
  const linkField = referenceField("Web address", f.link);
  const submit = refEl("button", { type: "submit", class: "btn-primary", text: "Add to this tab" });
  const sync = () => {
    programField.hidden = f.category.value === "Template"; // a Template belongs to no program
    fileField.hidden = !f.kindFile.checked;
    linkField.hidden = f.kindFile.checked;
  };
  for (const el of [f.category, f.kindFile, f.kindLink]) el.addEventListener("change", sync);
  sync();

  const form = refEl(
    "form",
    { class: "ref-panel ref-form", novalidate: true },
    refEl("div", { class: "ref-kind" }, refEl("label", {}, f.kindFile, " File"), refEl("label", {}, f.kindLink, " Web link")),
    referenceField("Category", f.category),
    programField,
    referenceField("Title", f.title),
    fileField,
    linkField,
    referenceField("Note (optional)", f.notes),
    submit,
  );
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const built = referenceNewFormData(f);
    if (built.problem) return ctx.fail(built.problem);
    submit.disabled = true;
    const res = await referenceRequest(`/api/reference/${ctx.section}/documents`, { method: "POST", body: built.form });
    if (res.ok) return ctx.finish(`Added "${f.title.value.trim()}" to this tab.`);
    submit.disabled = false;
    ctx.fail(referenceMessage(res, "Could not add it."));
  });
  return form;
}

// ---- the dialog -----------------------------------------------------------

window.openReferenceAdd = function (section, onDone) {
  const errorEl = refEl("p", { class: "ref-status ref-status--error", role: "alert" });
  const dialog = refEl("dialog", { class: "ref-dialog", "aria-labelledby": "ref-dialog-title" });
  const ctx = {
    section,
    fail: (text) => {
      errorEl.textContent = text;
    },
    finish: (message) => {
      dialog.close();
      onDone(message);
    },
  };
  const panels = { pick: referencePickPanel(ctx), new: referenceNewPanel(ctx) };
  const tabs = {};
  const show = (name) => {
    errorEl.textContent = "";
    for (const key of Object.keys(panels)) {
      panels[key].hidden = key !== name;
      tabs[key].classList.toggle("active", key === name);
      tabs[key].setAttribute("aria-selected", String(key === name));
    }
  };
  for (const [key, label] of [["pick", "Pick existing"], ["new", "Upload new"]]) {
    tabs[key] = refEl("button", { type: "button", class: "app-tab", role: "tab", text: label, onclick: () => show(key) });
  }
  const close = refEl("button", { type: "button", class: "btn-ghost ref-btn", text: "Close", onclick: () => dialog.close() });
  dialog.append(
    refEl("div", { class: "ref-dialog-head" }, refEl("h2", { id: "ref-dialog-title", text: "Add reference · " + referenceSectionLabel(section) }), close),
    refEl("div", { class: "app-tabs", role: "tablist" }, tabs.pick, tabs.new),
    panels.pick,
    panels.new,
    errorEl,
  );
  dialog.addEventListener("close", () => dialog.remove());
  document.body.append(dialog);
  show("pick");
  dialog.showModal();
};
