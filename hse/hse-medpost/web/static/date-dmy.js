// dd/mm/yyyy date boxes, shared by the checkup page (mcu.js) and the Medical
// Record page (malaria.js). A box is a .date-dmy wrapper holding a visible
// text input (.date-dmy-text) and the real <input type="date">; the text is
// what people type, the date input holds YYYY-MM-DD and is what forms read.
// Moved here unchanged from mcu.js on 2026-10-20 so both pages look and
// behave the same.

// Visible date boxes show dd/mm/yyyy. The named date input keeps YYYY-MM-DD,
// which is what the patient and checkup records store.
function isoToDmy(iso) {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(String(iso || ""));
  if (!m) return "";
  return `${m[3]}/${m[2]}/${m[1]}`;
}

function dmyToIso(text) {
  const m = /^(\d{1,2})\/(\d{1,2})\/(\d{4})$/.exec(String(text || "").trim());
  if (!m) return "";
  const day = Number(m[1]);
  const month = Number(m[2]);
  const year = Number(m[3]);
  const dt = new Date(year, month - 1, day);
  if (dt.getFullYear() !== year || dt.getMonth() !== month - 1 || dt.getDate() !== day) return "";
  return `${String(year).padStart(4, "0")}-${String(month).padStart(2, "0")}-${String(day).padStart(2, "0")}`;
}

function formatDateTyping(raw) {
  const trimmed = String(raw || "").trim();
  const iso = /^(\d{4})-(\d{2})-(\d{2})$/.exec(trimmed);
  if (iso) return `${iso[3]}/${iso[2]}/${iso[1]}`;
  const digits = trimmed.replace(/\D/g, "").slice(0, 8);
  let out = digits.slice(0, 2);
  if (digits.length > 2) out += "/" + digits.slice(2, 4);
  if (digits.length > 4) out += "/" + digits.slice(4);
  return out;
}

function dateDisplayFor(native) {
  return native?.closest(".date-dmy")?.querySelector(".date-dmy-text") || null;
}

function syncDateDisplay(native) {
  const text = dateDisplayFor(native);
  if (!text) return;
  text.value = isoToDmy(native.value);
  text.setCustomValidity("");
}

function syncDateDisplays(root) {
  root?.querySelectorAll(".date-dmy input[type='date']").forEach(syncDateDisplay);
}

function commitDateDisplay(native) {
  const text = dateDisplayFor(native);
  if (!text) return true;
  const raw = text.value.trim();
  if (!raw) {
    native.value = "";
    text.setCustomValidity("");
    return true;
  }
  const iso = dmyToIso(raw);
  if (!iso) {
    text.setCustomValidity("Use dd/mm/yyyy");
    return false;
  }
  text.setCustomValidity("");
  text.value = isoToDmy(iso);
  if (native.value !== iso) {
    native.value = iso;
    native.dispatchEvent(new Event("change", { bubbles: true }));
  }
  return true;
}

function bindDateDisplays(root) {
  root.querySelectorAll(".date-dmy input[type='date']").forEach((native) => {
    if (native.dataset.dmyBound === "1") return;
    native.dataset.dmyBound = "1";
    const text = dateDisplayFor(native);
    if (!text) return;
    text.addEventListener("input", () => {
      const next = formatDateTyping(text.value);
      if (text.value !== next) {
        const atEnd = text.selectionStart === text.value.length;
        text.value = next;
        if (atEnd) text.setSelectionRange(next.length, next.length);
      }
      const iso = dmyToIso(text.value);
      if (text.value === "" || iso) {
        if (native.value !== iso) {
          native.value = iso;
          native.dispatchEvent(new Event("change", { bubbles: true }));
        }
      }
    });
    text.addEventListener("blur", () => {
      if (!commitDateDisplay(native) && text.value.trim()) text.reportValidity();
    });
    native.addEventListener("input", () => syncDateDisplay(native));
    native.addEventListener("change", () => syncDateDisplay(native));
  });
  root.querySelectorAll("form").forEach((form) => {
    if (!form.querySelector(".date-dmy") || form.dataset.dmySubmit === "1") return;
    form.dataset.dmySubmit = "1";
    form.addEventListener("submit", (e) => {
      let bad = null;
      form.querySelectorAll(".date-dmy input[type='date']").forEach((native) => {
        if (!commitDateDisplay(native) && !bad) bad = dateDisplayFor(native);
      });
      if (!bad) return;
      e.preventDefault();
      e.stopImmediatePropagation();
      bad.reportValidity();
    }, true);
  });
}

bindDateDisplays(document);
