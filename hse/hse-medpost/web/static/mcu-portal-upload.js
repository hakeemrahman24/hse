// New batch upload with progress. A plain form post shows nothing while a big scan travels to the
// Pi and then sits in Ghostscript, and on a slow link that looks exactly like a hang. Sending the
// form with XMLHttpRequest lets the page show two phases: "uploading" (a real percentage) and
// "checking" (the server rebuilds the PDF, which takes a little while for a large scan). Without JavaScript the form still posts normally.
//
// The words come from the hidden #up-msgs block in the page, not from this file, so the server's
// language dictionary translates them like the rest of the page.
(function () {
  'use strict';
  var form = document.querySelector('form[action$="/batch/add"]');
  var fileInput = document.getElementById('file');
  var box = document.getElementById('up-msgs');
  if (!form || !fileInput || !box || !window.XMLHttpRequest || !window.FormData) return;

  var MAX_BYTES = 10 * 1024 * 1024; // the server's limit (MaxUploadBytes)
  var TIMEOUT_MS = 5 * 60 * 1000;
  var progress = document.getElementById('up-progress');
  var status = document.getElementById('up-status');
  var button = form.querySelector('button[type="submit"]');

  function msg(key) {
    var el = box.querySelector('[data-m="' + key + '"]');
    return el ? el.textContent.trim() : '';
  }
  function say(text, isError) {
    status.textContent = text;
    status.hidden = !text;
    status.classList.toggle('mp-bad', !!isError);
  }
  function busy(on) {
    button.disabled = on;
    progress.hidden = !on;
    if (!on) progress.removeAttribute('value');
  }

  // A file over the limit is refused here, before any upload, with the same message the server
  // would give. The portal does not shrink files any more (owner, 2026-11-09): the person splits or
  // rescans a file that is too big.
  fileInput.addEventListener('change', function () {
    var f = fileInput.files && fileInput.files[0];
    say('', false);
    busy(false);
    button.disabled = false;
    if (f && f.size > MAX_BYTES) {
      say(msg('toobig'), true);
      button.disabled = true;
    }
  });

  form.addEventListener('submit', function (e) {
    var f = fileInput.files && fileInput.files[0];
    if (!f || f.size > MAX_BYTES || button.disabled) { e.preventDefault(); return; }
    e.preventDefault();
    var xhr = new XMLHttpRequest();
    xhr.open('POST', form.action);
    xhr.timeout = TIMEOUT_MS;
    busy(true);
    progress.max = 100;
    progress.value = 0;
    say(msg('uploading') + ' 0%', false);

    xhr.upload.onprogress = function (ev) {
      if (!ev.lengthComputable) return;
      var pct = Math.min(99, Math.round((ev.loaded / ev.total) * 100));
      progress.value = pct;
      say(msg('uploading') + ' ' + pct + '%', false);
    };
    // The bytes are all sent: from here the server works on the file. An indeterminate bar (no
    // value) shows that something is still happening.
    xhr.upload.onload = function () {
      progress.removeAttribute('value');
      say(msg('checking'), false);
    };
    xhr.onload = function () {
      // A gateway error means the connection was cut while the server was still busy; the file
      // may have been stored, so say so instead of showing Cloudflare's error page.
      if (xhr.status === 502 || xhr.status === 504 || xhr.status === 524 || xhr.status === 0) {
        busy(false);
        say(msg('cut'), true);
        return;
      }
      // The form answers with a redirect to the review page on success (followed by the browser,
      // so responseURL is the new address) and with the form page plus a message on a refusal.
      if (xhr.status >= 200 && xhr.status < 300 && xhr.responseURL && xhr.responseURL !== form.action) {
        window.location.assign(xhr.responseURL);
        return;
      }
      document.open();
      document.write(xhr.responseText);
      document.close();
    };
    xhr.onerror = xhr.ontimeout = function () {
      busy(false);
      say(msg('cut'), true);
    };
    xhr.send(new FormData(form));
  });
})();
