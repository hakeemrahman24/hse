// Topic suggestions on the SOC form (plan v8). While the reporter types what they saw, the server
// returns up to three hazard topics; they appear as buttons. Nothing is chosen for the reporter:
// a topic is set only by tapping a button or by using the full list below it. "Lainnya / tidak ada
// yang cocok" clears the topic, and the report then goes to HSE review as "topic not picked".
//
//   attachTopicSuggest({ description, topic, chips, none, hint, url, texts })
//
// description is one text box or a list of them (the wizard has one per path); the suggestion is
// worked out from the one the reporter typed in last, or from the one passed to .ask(el). The
// function returns { ask(el), clear() }.
//
// texts: { suggested, nothing } - the sentences shown in the hint (already translated).
(function () {
  "use strict";

  var MIN_LETTERS = 3;  // plan v8, spec section 8: start after 3 typed letters
  var DELAY_MS = 500;

  window.attachTopicSuggest = function (opts) {
    var descs = [].concat(opts.description || []).filter(Boolean);
    var topic = opts.topic, chips = opts.chips, none = opts.none;
    if (!descs.length || !topic || !chips) { return; }
    var desc = descs[0]; // the text box the suggestion is worked out from
    var texts = opts.texts || {};
    var timer = null;
    var latest = 0;

    function say(text) { if (opts.hint) { opts.hint.textContent = text; } }

    function letters(text) { return (text.match(/[A-Za-zÀ-ɏ]/g) || []).length; }

    // The pressed state always follows the real value of the dropdown, so a pick made in the full
    // list un-presses the buttons and vice versa.
    function syncPressed() {
      Array.prototype.forEach.call(chips.children, function (b) {
        b.setAttribute("aria-pressed", String(b.getAttribute("data-topic") === topic.value && topic.value !== ""));
      });
      if (none) { none.setAttribute("aria-pressed", String(topic.value === "" && none.getAttribute("data-chosen") === "1")); }
    }

    function setTopic(value, viaNone) {
      topic.value = value;
      if (none) { none.setAttribute("data-chosen", viaNone ? "1" : "0"); }
      topic.dispatchEvent(new Event("combo:sync"));
      topic.dispatchEvent(new Event("change")); // the page moves the hazard along with the topic
      syncPressed();
    }

    function render(list) {
      chips.textContent = "";
      list.forEach(function (s) {
        var b = document.createElement("button");
        b.type = "button";
        b.className = "topic-chip";
        b.setAttribute("data-topic", String(s.topicId));
        b.textContent = s.label || s.topic;
        b.addEventListener("click", function () { setTopic(String(s.topicId), false); });
        chips.appendChild(b);
      });
      syncPressed();
    }

    function clear() {
      latest++; // an answer still on its way belongs to text that is gone
      render([]);
      say("");
    }

    function ask() {
      var text = desc.value.trim();
      if (letters(text) < MIN_LETTERS) { clear(); return; }
      var ticket = ++latest;
      fetch(opts.url, {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ text: text })
      }).then(function (res) {
        return res.ok ? res.json() : null;
      }).then(function (data) {
        if (!data || ticket !== latest) { return; } // an older answer arrived late
        var list = data.suggestions || [];
        render(list);
        say(list.length ? (texts.suggested || "") : (texts.nothing || ""));
      }).catch(function () { /* the suggestion is optional: the full list still works */ });
    }

    if (none) {
      none.addEventListener("click", function () { setTopic("", true); });
    }
    topic.addEventListener("change", function () {
      if (topic.value !== "" && none) { none.setAttribute("data-chosen", "0"); }
      syncPressed();
    });
    descs.forEach(function (d) {
      d.addEventListener("input", function () {
        desc = d;
        clearTimeout(timer);
        timer = setTimeout(ask, DELAY_MS);
      });
      d.addEventListener("blur", function () { desc = d; clearTimeout(timer); ask(); });
    });
    if (desc.value.trim() !== "") { ask(); } // a form shown again after an error, or an edit page
    return {
      ask: function (el) { if (el) { desc = el; } clearTimeout(timer); ask(); },
      clear: clear
    };
  };
})();
