// Suggests the Topic and Hazard while the finding is being described. The server does the matching
// (api/classify.go); this script only asks it and fills the two dropdowns. A choice the person has
// made themselves is never overwritten or cleared.
//
//   - From 2 words on, the suggestion is worked out again after every pause in typing.
//   - Fewer than 2 words (blank, or cut down to one word) resets the suggestion: what this script
//     filled in is cleared, because it was worked out from text that is no longer there.
//   - A new text that matches nothing clears the old suggestion too, so it never goes stale.
//
//   attachSuggest({ description, topic, hazard, url, hint })
(function () {
  "use strict";

  var MIN_WORDS = 2;
  var DELAY_MS = 700;

  window.attachSuggest = function (opts) {
    var desc = opts.description, topic = opts.topic, hazard = opts.hazard;
    if (!desc || !topic || !hazard) { return; }
    var timer = null;
    var latest = 0;

    // Values that are already there (an edit page, or a form shown again) count as chosen.
    [topic, hazard].forEach(function (sel) {
      if (sel.value !== "") { sel.dataset.touched = "1"; }
      sel.addEventListener("change", function () { sel.dataset.touched = "1"; });
    });

    // Choosing a topic by hand also moves the hazard, unless the hazard was chosen by hand too.
    function followTopic() {
      var opt = topic.options[topic.selectedIndex];
      var h = opt ? opt.getAttribute("data-hazard") : "";
      if (h && hazard.dataset.touched !== "1") { hazard.value = h; hazard.dispatchEvent(new Event("combo:sync")); }
    }
    topic.addEventListener("change", followTopic);

    function say(text) { if (opts.hint) { opts.hint.textContent = text; } }

    function words(text) { return text.split(/\s+/).filter(Boolean).length; }

    // Puts a suggested value in a dropdown, or clears what an earlier suggestion put there. A
    // dropdown the person chose from themselves (touched) is left exactly as it is.
    function fill(sel, value) {
      if (sel.dataset.touched === "1") { return; }
      sel.value = value;
      sel.dispatchEvent(new Event("combo:sync"));
    }

    // Forgets the suggestion: both fields go back to empty unless the person chose them.
    function reset() {
      latest++; // an answer still on its way belongs to text that is gone
      fill(topic, "");
      fill(hazard, "");
      if (topic.value !== "") { followTopic(); } // a topic the person chose keeps its own hazard
      topic.dispatchEvent(new Event("suggest:applied")); // a page may show more than the two fields
      say("");
    }

    function ask() {
      var text = desc.value.trim();
      if (words(text) < MIN_WORDS) { reset(); return; }
      var ticket = ++latest;
      fetch(opts.url, {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ text: text })
      }).then(function (res) {
        return res.ok ? res.json() : null;
      }).then(function (s) {
        if (!s || ticket !== latest) { return; } // an older answer arrived late
        if (!s.topicId) { reset(); return; }
        // A topic with no hazard category (PPE, ...) suggests no hazard: the hazard field is cleared.
        fill(topic, String(s.topicId));
        fill(hazard, s.hazardId ? String(s.hazardId) : "");
        topic.dispatchEvent(new Event("suggest:applied")); // a page may show more than the two fields
        say((opts.label || "Disarankan / Suggested") + ": " + s.topic + (s.hazard ? " — " + s.hazard : ""));
      }).catch(function () { /* the suggestion is optional */ });
    }

    desc.addEventListener("input", function () {
      clearTimeout(timer);
      timer = setTimeout(ask, DELAY_MS);
    });
    desc.addEventListener("blur", function () { clearTimeout(timer); ask(); });
  };
})();
