// "Make a PDF smaller" tool on the New batch page (owner, 2026-11-09). The portal itself no longer
// shrinks files: anything over 10 MB is refused. This tool lets the person shrink a big scan on their
// own device first, then either use the result for the upload or save it.
//
// How: pdf.js draws every page onto a canvas, each page is saved as a JPEG, and jsPDF puts the JPEGs
// back into a new PDF of the same page sizes. That is the only way a browser can shrink a scan, and
// it has one cost the page tells the person about: the new PDF holds pictures of the pages, so its
// text can no longer be selected or searched. Nothing is sent to the server by this tool.
//
// pdf.js and jsPDF are loaded only when the person presses "Make smaller", from /static/vendor,
// because the portal's security policy allows scripts from its own site only. pdf.js runs its work
// in a worker file for the same reason (a blob worker would be blocked).
//
// The words come from the hidden #cp-msgs block in the page, so the server's language dictionary
// translates them like the rest of the page.
(function () {
  'use strict';
  var details = document.getElementById('cp-tool');
  var mainInput = document.getElementById('file');
  var input = document.getElementById('cp-file');
  var targetSel = document.getElementById('cp-target');
  var runBtn = document.getElementById('cp-run');
  var progress = document.getElementById('cp-progress');
  var status = document.getElementById('cp-status');
  var done = document.getElementById('cp-done');
  var useBtn = document.getElementById('cp-use');
  var saveBtn = document.getElementById('cp-save');
  var box = document.getElementById('cp-msgs');
  if (!details || !mainInput || !input || !runBtn || !box || !window.DataTransfer) return;

  var MB = 1024 * 1024;
  var LIMIT = 10 * MB; // the portal's upload limit (MaxUploadBytes)
  var START_DPI = 200; // start sharp: fine print on lab sheets must stay readable
  var MIN_DPI = 72;
  var START_QUALITY = 0.82;
  var MIN_QUALITY = 0.5;
  var PAGE_TRIES = 4; // draws of one page at fewer and fewer pixels
  var MIN_BUDGET = 20 * 1024; // never ask a page for less than this

  var result = null; // the smaller File, once there is one
  var runToken = 0; // choosing another file cancels the result of an older run

  function msg(key) {
    var el = box.querySelector('[data-m="' + key + '"]');
    return el ? el.textContent.trim() : '';
  }
  function mb(bytes) { return (bytes / MB).toFixed(1); }
  function say(text, isError) {
    status.textContent = text;
    status.hidden = !text;
    status.classList.toggle('mp-bad', !!isError);
  }
  function busy(on) {
    runBtn.disabled = on;
    progress.hidden = !on;
    if (!on) progress.removeAttribute('value');
  }
  function resetResult() {
    result = null;
    done.hidden = true;
  }

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

  // Encodes ONE page so that it fits its share of the size budget. It draws the page once, then
  // lowers the JPEG quality on that same drawing (cheap, no second render); only when the lowest
  // quality is still too big does it draw again with fewer pixels. The smallest result is kept.
  async function encodePage(page, budget) {
    var size = page.getViewport({ scale: 1 }); // PDF points: the page size the new file keeps
    var dpi = START_DPI, quality = START_QUALITY, best = null;
    for (var attempt = 0; attempt < PAGE_TRIES; attempt++) {
      var vp = page.getViewport({ scale: dpi / 72 });
      var canvas = document.createElement('canvas');
      canvas.width = Math.ceil(vp.width);
      canvas.height = Math.ceil(vp.height);
      var ctx = canvas.getContext('2d');
      ctx.fillStyle = '#fff'; // JPEG has no transparency: an unpainted page would turn black
      ctx.fillRect(0, 0, canvas.width, canvas.height);
      await page.render({ canvasContext: ctx, viewport: vp }).promise;
      var q = quality, bytes;
      for (;;) {
        bytes = new Uint8Array(await (await toJpeg(canvas, q)).arrayBuffer());
        if (!best || bytes.length < best.bytes.length) best = { bytes: bytes, w: size.width, h: size.height };
        if (bytes.length <= budget || q <= MIN_QUALITY) break;
        q = Math.max(MIN_QUALITY, q - 0.1);
      }
      canvas.width = canvas.height = 0; // release the bitmap before the next page
      if (best.bytes.length <= budget || dpi <= MIN_DPI) break;
      // Still too big at the lowest quality: JPEG size follows the pixel count, so aim with a root.
      dpi = Math.max(MIN_DPI, Math.floor(dpi * Math.sqrt(budget / bytes.length) * 0.95));
      quality = 0.7;
    }
    return best;
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

  // One pass over the pages, so the progress bar only ever moves forward (an earlier version redid the
  // whole file when the first try was too big, and the bar jumped back, which confused people). Each
  // page gets an equal share of what is left of the size aimed at (a little under the chosen size,
  // because the PDF around the pictures adds a few KB), so a light page leaves room for a heavy one.
  async function reduce(file, targetBytes, token, onPage) {
    await loadLibraries();
    var data = new Uint8Array(await file.arrayBuffer());
    // isEvalSupported off: the portal's policy forbids eval, and pdf.js would otherwise try it.
    var pdf = await window.pdfjsLib.getDocument({ data: data, isEvalSupported: false }).promise;
    var count = pdf.numPages;
    if (!count) throw new Error('no pages');
    var aim = targetBytes * 0.92, used = 0, pages = [];
    for (var i = 1; i <= count; i++) {
      if (token !== runToken) throw new Error('cancelled');
      var page = await pdf.getPage(i);
      var enc = await encodePage(page, Math.max(MIN_BUDGET, (aim - used) / (count - i + 1)));
      pages.push(enc);
      used += enc.bytes.length;
      page.cleanup();
      onPage(i, count);
    }
    var blob = buildPdf(pages);
    return new File([blob], file.name.replace(/\.pdf$/i, '') + '-smaller.pdf', { type: 'application/pdf' });
  }

  async function run() {
    var f = input.files && input.files[0];
    if (!f) { say(msg('choose'), true); return; }
    var token = ++runToken;
    resetResult();
    busy(true);
    progress.max = 100;
    progress.value = 0;
    say(msg('working'), false);
    try {
      var small = await reduce(f, Number(targetSel.value) * MB, token, function (page, pages) {
        if (token !== runToken) return;
        progress.value = Math.round((page / pages) * 100);
        say(msg('working') + ' ' + page + '/' + pages, false);
      });
      if (token !== runToken) return;
      busy(false);
      if (small.size >= f.size) { say(msg('nogain'), true); return; }
      if (small.size > LIMIT) { say(msg('still').replace('{to}', mb(small.size)), true); return; }
      result = small;
      say(msg('done').replace('{from}', mb(f.size)).replace('{to}', mb(small.size)), false);
      done.hidden = false;
    } catch (e) {
      if (token !== runToken) return; // a newer choice replaced this run
      busy(false);
      say(msg('fail'), true);
    }
  }

  // Put the smaller file into the upload form's file field, as if the person had chosen it, and let
  // the upload script check it (its change handler enables the button again).
  function useResult() {
    if (!result) return;
    var dt = new DataTransfer();
    dt.items.add(result);
    mainInput.files = dt.files;
    mainInput.dispatchEvent(new Event('change', { bubbles: true }));
    say(msg('used'), false);
  }

  // Save the result to the device with a temporary link; the address is released straight after.
  function saveResult() {
    if (!result) return;
    var url = URL.createObjectURL(result);
    var a = document.createElement('a');
    a.href = url;
    a.download = result.name;
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(function () { URL.revokeObjectURL(url); }, 10000);
  }

  input.addEventListener('change', function () {
    runToken++; // cancel a run that is still going
    busy(false);
    resetResult();
    say('', false);
  });
  runBtn.addEventListener('click', run);
  useBtn.addEventListener('click', useResult);
  saveBtn.addEventListener('click', saveResult);

  // When the file chosen for the upload is too big, open this tool with that file already in it, so
  // the next step is one button. (The upload script shows the "too big" message at the same time.)
  mainInput.addEventListener('change', function () {
    var f = mainInput.files && mainInput.files[0];
    if (!f || f.size <= LIMIT || f === result) return;
    var dt = new DataTransfer();
    dt.items.add(f);
    input.files = dt.files;
    resetResult();
    details.open = true;
  });
})();
