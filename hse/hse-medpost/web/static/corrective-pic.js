// The PIC dropdown: a searchable list of employees (the Patient table), picked and never typed. Only a
// listed employee can be chosen: typing alone leaves the field invalid, and the server re-checks by looking
// the picked id up (it ignores any name sent from the page).
//
// Keyboard: Down/Up move through the results (wrapping), Home/End jump to the first/last, Enter picks the
// highlighted row, Escape closes the list (and only the list: the dialog stays open), Tab leaves the field.
// Screen readers get the WAI-ARIA combobox pattern: role=combobox on the input, role=listbox / option on the
// list, aria-activedescendant for the highlighted row, and a polite status line for the result count.
// Employee names are written with textContent only, never innerHTML, so a name cannot inject markup.
//
// Needs corrective-core.js (caNextIndex, caEmployeeDetail, CA_PIC_MIN_CHARS).
var CA_PIC_SEARCH_DELAY_MS = 200;
var caPicCounter = 0;

// host: an empty element to build the control in.
// returns { getId, isValid, hasPick, setSaved, clear, focus, validate }
function caCreatePicPicker(host, options) {
  options = options || {};
  const uid = "ca-pic-" + ++caPicCounter;
  host.textContent = "";
  host.classList.add("ca-pic");

  const input = document.createElement("input");
  input.type = "text";
  input.id = uid;
  input.autocomplete = "off";
  input.spellcheck = false;
  input.placeholder = "Type a name or employee ID";
  input.setAttribute("role", "combobox");
  input.setAttribute("aria-autocomplete", "list");
  input.setAttribute("aria-expanded", "false");
  input.setAttribute("aria-controls", uid + "-list");
  if (options.label) input.setAttribute("aria-label", options.label);

  const clearBtn = document.createElement("button");
  clearBtn.type = "button";
  clearBtn.className = "ca-pic-clear";
  clearBtn.setAttribute("aria-label", "Clear the selected employee");
  clearBtn.textContent = "×";
  clearBtn.hidden = true;

  const list = document.createElement("div");
  list.id = uid + "-list";
  list.className = "company-typeahead ca-pic-list";
  list.setAttribute("role", "listbox");
  list.hidden = true;

  const status = document.createElement("div");
  status.className = "ca-sr-only";
  status.setAttribute("role", "status");
  status.setAttribute("aria-live", "polite");

  host.append(input, clearBtn, list, status);

  let pickedId = 0; // the id sent to the server; 0 = nothing newly picked
  let savedText = ""; // the PIC already stored on a finding being edited, kept when nothing new is picked
  let results = [];
  let active = -1;
  let timer = null;
  let latest = 0; // the newest search; a slower earlier answer must not overwrite it

  function setValidity() {
    // A saved PIC (edit mode) or a fresh pick is valid. Anything else (empty, or text typed but not
    // picked) is not, which stops the form from submitting a name that was never chosen.
    if (pickedId || (savedText && input.value === savedText)) {
      input.setCustomValidity("");
    } else if (input.value.trim() === "") {
      input.setCustomValidity(options.required === false ? "" : "Pick the person in charge.");
    } else {
      input.setCustomValidity("Pick an employee from the list.");
    }
    clearBtn.hidden = input.value === "";
  }

  function closeList() {
    list.hidden = true;
    input.setAttribute("aria-expanded", "false");
    input.removeAttribute("aria-activedescendant");
    active = -1;
  }

  function highlight(index) {
    active = index;
    list.querySelectorAll('[role="option"]').forEach((row, i) => {
      row.setAttribute("aria-selected", i === index ? "true" : "false");
      row.classList.toggle("ca-pic-active", i === index);
      if (i === index) {
        input.setAttribute("aria-activedescendant", row.id);
        row.scrollIntoView({ block: "nearest" });
      }
    });
    if (index < 0) input.removeAttribute("aria-activedescendant");
  }

  function pick(employee) {
    pickedId = employee.id;
    savedText = "";
    input.value = employee.name + (employee.employeeId ? " (" + employee.employeeId + ")" : "");
    closeList();
    setValidity();
    if (options.onChange) options.onChange();
  }

  function render() {
    list.textContent = "";
    results.forEach((employee, i) => {
      const row = document.createElement("div");
      row.id = uid + "-opt-" + i;
      row.className = "app-program-item ca-pic-option";
      row.setAttribute("role", "option");
      row.setAttribute("aria-selected", "false");
      const name = document.createElement("span");
      name.className = "app-program-item-title";
      name.textContent = employee.name;
      const meta = document.createElement("span");
      meta.className = "app-program-item-meta";
      meta.textContent = caEmployeeDetail(employee);
      row.append(name, meta);
      // mousedown, not click: click fires after the input has lost focus, which would close the list first.
      row.addEventListener("mousedown", (e) => {
        e.preventDefault();
        pick(employee);
      });
      list.append(row);
    });
    if (!results.length) {
      const none = document.createElement("div");
      none.className = "app-program-item-meta ca-pic-none";
      none.textContent = "No matching employee";
      list.append(none);
    }
    list.hidden = false;
    input.setAttribute("aria-expanded", "true");
    status.textContent = results.length ? results.length + (results.length === 1 ? " employee found" : " employees found") : "No matching employee";
    highlight(-1);
  }

  async function search() {
    const text = input.value.trim();
    if (text.length < CA_PIC_MIN_CHARS) {
      closeList();
      return;
    }
    const mine = ++latest;
    try {
      const res = await fetch("/api/corrective-actions/employees?q=" + encodeURIComponent(text), { credentials: "same-origin" });
      if (res.status === 401) {
        location.href = "/";
        return;
      }
      const data = res.ok ? await res.json() : [];
      if (mine !== latest) return;
      results = Array.isArray(data) ? data : [];
    } catch (err) {
      if (mine !== latest) return;
      results = [];
    }
    render();
  }

  input.addEventListener("input", () => {
    // Editing the text throws the pick away: the field is only valid again after choosing from the list.
    pickedId = 0;
    if (savedText && input.value !== savedText) savedText = "";
    setValidity();
    clearTimeout(timer);
    timer = setTimeout(search, CA_PIC_SEARCH_DELAY_MS);
  });

  input.addEventListener("keydown", (e) => {
    const open = !list.hidden;
    if (["ArrowDown", "ArrowUp", "Home", "End"].includes(e.key)) {
      if (!open && (e.key === "ArrowDown" || e.key === "ArrowUp")) {
        e.preventDefault();
        search();
        return;
      }
      if (!open || !results.length) return;
      e.preventDefault();
      highlight(caNextIndex(active, results.length, e.key));
    } else if (e.key === "Enter" && open && active >= 0) {
      e.preventDefault(); // pick, do not submit the form
      pick(results[active]);
    } else if (e.key === "Escape" && open) {
      e.preventDefault(); // closes the list only; a second Escape closes the dialog
      e.stopPropagation();
      closeList();
    } else if (e.key === "Tab") {
      closeList();
    }
  });

  input.addEventListener("blur", () => {
    clearTimeout(timer);
    closeList();
  });

  clearBtn.addEventListener("click", () => {
    api.clear();
    input.focus();
  });

  const api = {
    // The picked employee's id, or 0 when the PIC was not newly picked (edit mode keeps the saved one).
    getId: () => pickedId,
    // True when there is a fresh pick or an unchanged saved PIC.
    hasPick: () => pickedId > 0 || (savedText !== "" && input.value === savedText),
    isValid: () => input.checkValidity(),
    validate: () => input.reportValidity(),
    // Edit mode: show the stored PIC without needing a new pick.
    setSaved(name, employeeId) {
      pickedId = 0;
      savedText = name + (employeeId ? " (" + employeeId + ")" : "");
      input.value = savedText;
      closeList();
      setValidity();
    },
    clear() {
      pickedId = 0;
      savedText = "";
      input.value = "";
      closeList();
      setValidity();
      if (options.onChange) options.onChange();
    },
    focus: () => input.focus(),
  };
  setValidity();
  return api;
}
