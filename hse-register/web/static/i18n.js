// Text for the language of the page. The public SOC pages mark <html data-i18n="single"> and carry
// their language in <html lang>: they show that one language (Indonesian or English). Everywhere
// else (the staff pages) the page has no such mark and a text keeps showing both
// "Indonesian / English", as it always did.
//
//   i18n("Tutup", "Close")
(function () {
  "use strict";
  var root = document.documentElement;
  window.i18n = function (id, en) {
    if (root.getAttribute("data-i18n") !== "single") { return id + " / " + en; }
    return (root.lang || "").toLowerCase().indexOf("en") === 0 ? en : id;
  };
})();
