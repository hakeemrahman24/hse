// Name and position fields on /mcu-submit (<input data-name>, <input data-position>): when the
// person leaves the box or submits, the text is cleaned, extra spaces are collapsed, and it is
// written in title style, so they see what will be saved. A name keeps letters, space, apostrophe,
// hyphen and dot; a position also keeps digits and ' - . / & , ( ). This mirrors SanitizeName and
// SanitizePosition in internal/mcusubmit/sanitize.go; the server runs the same rule again and is
// the one that counts.
(function () {
  'use strict';

  // Title style for one already-cleaned string: a word starts after a space, hyphen or dot. After an
  // apostrophe the next letter is capital only behind a one-letter start (O'Neil, not Ma'Ruf).
  function title(kept) {
    var out = '';
    var upperNext = true;
    var lettersInWord = 0;
    // Spread into code points so a letter outside the basic plane is not cut in half.
    Array.from(kept).forEach(function (ch) {
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
        out += ch;           // space, hyphen or dot: a new word starts
        upperNext = true;
        lettersInWord = 0;
      }
    });
    return out;
  }

  function cleanName(raw) {
    return title(raw.normalize('NFC').replace(/[‘’]/g, "'")
      .replace(/[^\p{L}\p{M}\s'.\-]/gu, '')
      .replace(/\s+/g, ' ').trim());
  }

  // A position is title-cased word by word; a word typed as 2 to 4 capitals is an acronym (HSE, IT)
  // and stays as typed.
  function cleanPosition(raw) {
    var kept = raw.normalize('NFC').replace(/[‘’]/g, "'")
      .replace(/[^\p{L}\p{M}\p{Nd}\s'.\/&,()\-]/gu, '')
      .replace(/\s+/g, ' ').trim();
    return kept.replace(/[\p{L}\p{M}']+/gu, function (word) {
      var letters = Array.from(word);
      var isAcronym = letters.length >= 2 && letters.length <= 4 &&
        letters.every(function (c) { return /\p{Lu}/u.test(c); });
      return isAcronym ? word : title(word);
    });
  }

  var inputs = document.querySelectorAll('input[data-name], input[data-position]');
  Array.prototype.forEach.call(inputs, function (input) {
    var clean = input.hasAttribute('data-position') ? cleanPosition : cleanName;
    function tidy() { input.value = clean(input.value); }
    input.addEventListener('blur', tidy);
    if (input.form) input.form.addEventListener('submit', tidy);
  });
})();
