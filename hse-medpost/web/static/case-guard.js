// Keeps free-text fields out of ALL CAPS as the user types (caps lock, etc).
// Rewrites the whole value in place on every keystroke, so casing is fixed
// live instead of only being corrected on submit.
function toProperCase(str) {
  return str.replace(/[A-Za-z]+/g, (word) => word[0].toUpperCase() + word.slice(1).toLowerCase());
}

function toSentenceCase(str) {
  let result = "";
  let capitalizeNext = true;
  // True once a sentence-ending char is seen, until a following space turns
  // it into a real capitalize-next boundary. Without this, a period/question
  // mark glued directly to the next character — as in a URL, domain, or
  // query string ("ayahaif.my.id", "?tab=dashboard") — would force a
  // capital where there's no sentence break at all.
  let boundaryPending = false;
  for (const ch of str) {
    if (/[A-Za-z]/.test(ch)) {
      result += capitalizeNext ? ch.toUpperCase() : ch.toLowerCase();
      capitalizeNext = false;
      boundaryPending = false;
    } else {
      result += ch;
      if (ch === "\n") {
        capitalizeNext = true;
        boundaryPending = false;
      } else if (/[.!?]/.test(ch)) {
        boundaryPending = true;
      } else if (/\s/.test(ch)) {
        if (boundaryPending) capitalizeNext = true;
        boundaryPending = false;
      } else {
        boundaryPending = false;
      }
    }
  }
  return result;
}

// Drop a leading run of spaces and collapse repeats, but keep one trailing
// space so the next word can still be typed. Blur trims that leftover space.
function collapseSpaces(str) {
  return str.replace(/^\s+/, "").replace(/\s{2,}/g, " ");
}

// Title style for a person name, the same rule as titleName in
// internal/mcusubmit/sanitize.go and the /mcu-submit page: a word starts
// after a space, hyphen or dot. After an apostrophe the next letter is
// capital only behind a one-letter start (O'Neil, D'Angelo), so Ma'ruf and
// As'adul keep their lower case.
function titleName(str) {
  let out = "";
  let upperNext = true;
  let lettersInWord = 0;
  for (const ch of str) {
    if (/\p{L}/u.test(ch)) {
      out += upperNext ? ch.toUpperCase() : ch.toLowerCase();
      upperNext = false;
      lettersInWord++;
    } else if (ch === "'") {
      out += ch;
      upperNext = lettersInWord === 1;
    } else if (/\p{M}/u.test(ch)) {
      out += ch;
    } else {
      out += ch; // space, hyphen or dot: a new word starts
      upperNext = true;
      lettersInWord = 0;
    }
  }
  return out;
}

// Person names: letters, combining marks, space, apostrophe, hyphen and dot
// (O'Connor, As'adul, Nur-Aini, M.Yusuf), single spaces, title case. Curly
// apostrophes from a phone keyboard become the plain one.
function sanitizeName(str) {
  return titleName(collapseSpaces(str.replace(/[‘’]/g, "'").replace(/[^\p{L}\p{M}\s'.\-]/gu, "")));
}

// Work positions: same title case and space cleanup, and digits stay
// (Welder 3g becomes Welder 3G).
function sanitizePosition(str) {
  return toProperCase(collapseSpaces(str.replace(/[^\p{L}\p{N}\s]/gu, "")));
}

function guardCase(el, transform) {
  if (!el) return;
  const rewrite = (trimEnd) => {
    const { selectionStart, selectionEnd } = el;
    let next = transform(el.value);
    if (trimEnd) next = next.trim();
    if (next === el.value) return;
    el.value = next;
    if (!trimEnd && selectionStart !== null) el.setSelectionRange(selectionStart, selectionEnd);
  };
  el.addEventListener("input", () => rewrite(false));
  el.addEventListener("blur", () => rewrite(true));
}
