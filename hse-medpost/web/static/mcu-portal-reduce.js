// Makes a big checkup PDF smaller in the browser, before it is uploaded (owner, 2026-11-06: files
// over 15 MB are first reduced to about 6-8 MB instead of being refused).
//
// How: pdf.js draws every page onto a canvas, each page is saved as a JPEG, and jsPDF puts the JPEGs
// back into a new PDF of the same page sizes. That is the only way a browser can shrink a scan, and
// it has one cost the caller must tell the person about: the new PDF holds pictures of the pages,
// so its text can no longer be selected or searched. (The server rebuilds every upload anyway.)
//
// Both libraries are loaded only when a file is too big, from /static/vendor, because the portal's
// security policy allows scripts from its own site only. pdf.js runs its work in a worker file for
// the same reason (a blob worker would be blocked).
(function () {
  'use strict';
  var HARD_MAX = 8 * 1024 * 1024; // stop as soon as the pages add up to this
  var AIM = 7 * 1024 * 1024; // when a try is too big, aim the next one here
  var START_DPI = 200; // start sharp: fine print on lab sheets must stay readable
  var MIN_DPI = 100;
  var START_QUALITY = 0.82;
  var MIN_QUALITY = 0.5;
  var TRIES = 4;

  function loadScript(src) {
    return new Promise(function (resolve, reject) {
      var s = document.createElement('script');
      s.src = src;
      s.onload = resolve;
      s.onerror = function () { reject(new Error('could not load ' + src)); };
      document.head.appendChild(s);
    });
  }
  function loadLibraries() {
    var chain = Promise.resolve();
    if (!window.pdfjsLib) chain = chain.then(function () { return loadScript('/static/vendor/pdf.min.js?v=1'); });
    if (!window.jspdf) chain = chain.then(function () { return loadScript('/static/vendor/jspdf.min.js'); });
    return chain.then(function () {
      window.pdfjsLib.GlobalWorkerOptions.workerSrc = '/static/vendor/pdf.worker.min.js?v=1';
    });
  }
  function toJpeg(canvas, quality) {
    return new Promise(function (resolve, reject) {
      canvas.toBlob(function (b) { b ? resolve(b) : reject(new Error('no image')); }, 'image/jpeg', quality);
    });
  }

  // One try: every page at one resolution and quality. Pages are kept as JPEG bytes (small), and the
  // canvas is released after each page so a phone is not holding 30 full-size bitmaps.
  async function renderPages(pdf, dpi, quality, onPage) {
    var scale = dpi / 72, pages = [], total = 0;
    for (var i = 1; i <= pdf.numPages; i++) {
      var page = await pdf.getPage(i);
      var size = page.getViewport({ scale: 1 }); // PDF points: the page size the new file keeps
      var vp = page.getViewport({ scale: scale });
      var canvas = document.createElement('canvas');
      canvas.width = Math.ceil(vp.width);
      canvas.height = Math.ceil(vp.height);
      var ctx = canvas.getContext('2d');
      ctx.fillStyle = '#fff'; // JPEG has no transparency: an unpainted page would turn black
      ctx.fillRect(0, 0, canvas.width, canvas.height);
      await page.render({ canvasContext: ctx, viewport: vp }).promise;
      var bytes = new Uint8Array(await (await toJpeg(canvas, quality)).arrayBuffer());
      pages.push({ bytes: bytes, w: size.width, h: size.height });
      total += bytes.length;
      canvas.width = canvas.height = 0;
      page.cleanup();
      onPage(i, pdf.numPages);
    }
    return { pages: pages, total: total };
  }

  function buildPdf(pages) {
    var JsPDF = window.jspdf.jsPDF;
    var doc = null;
    pages.forEach(function (p, i) {
      var orient = p.w > p.h ? 'l' : 'p';
      if (i === 0) doc = new JsPDF({ unit: 'pt', format: [p.w, p.h], orientation: orient, compress: true });
      else doc.addPage([p.w, p.h], orient);
      doc.addImage(p.bytes, 'JPEG', 0, 0, p.w, p.h, undefined, 'NONE');
    });
    return doc.output('blob');
  }

  // reduce(file, onPage) resolves with a smaller File. It rejects when the file cannot be read
  // (damaged, password-protected) or has no pages; the caller then shows the "too big" message.
  async function reduce(file, onPage) {
    await loadLibraries();
    var data = new Uint8Array(await file.arrayBuffer());
    // isEvalSupported off: the portal's policy forbids eval, and pdf.js would otherwise try it.
    var pdf = await window.pdfjsLib.getDocument({ data: data, isEvalSupported: false }).promise;
    if (!pdf.numPages) throw new Error('no pages');
    var dpi = START_DPI, quality = START_QUALITY, best = null;
    for (var attempt = 0; attempt < TRIES; attempt++) {
      var r = await renderPages(pdf, dpi, quality, onPage);
      if (!best || r.total < best.total) best = r;
      if (r.total <= HARD_MAX) break;
      // JPEG size grows roughly with the pixel count, so aim the next try with a square root.
      dpi = Math.max(MIN_DPI, Math.floor(dpi * Math.sqrt(AIM / r.total) * 0.95));
      quality = Math.max(MIN_QUALITY, quality - 0.08);
    }
    var blob = buildPdf(best.pages);
    return new File([blob], file.name.replace(/\.pdf$/i, '') + '-reduced.pdf', { type: 'application/pdf' });
  }

  window.McuPdfReduce = { run: reduce };
})();
