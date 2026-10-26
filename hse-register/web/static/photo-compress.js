// Compresses photos in the browser as soon as they are picked, so a phone does not upload a
// 6 MB camera picture over site Wi-Fi. Rules (same for "before" and "after" photos):
//   - at most 4 photos per post,
//   - every photo is shrunk to 500 KB or less.
// The server enforces the same limits again (api/photos.go), so this script is for speed and
// convenience, not for security.
(function () {
  "use strict";

  // Language-aware text; without i18n.js (the staff pages) it stays "Indonesian / English".
  var t = window.i18n || function (id, en) { return id + " / " + en; };

  var MAX_BYTES = 500 * 1024;
  var MAX_SIDE = 2000;
  var QUALITIES = [0.85, 0.75, 0.65, 0.55, 0.45];

  function toBlob(canvas, quality) {
    return new Promise(function (resolve) { canvas.toBlob(resolve, "image/jpeg", quality); });
  }

  // Shrinks one picture: first to MAX_SIDE, then lower quality, then smaller size, until it fits.
  async function compress(file) {
    var bitmap = await createImageBitmap(file);
    var scale = Math.min(1, MAX_SIDE / Math.max(bitmap.width, bitmap.height));
    var w = Math.max(1, Math.round(bitmap.width * scale));
    var h = Math.max(1, Math.round(bitmap.height * scale));
    for (var attempt = 0; attempt < 8; attempt++) {
      var canvas = document.createElement("canvas");
      canvas.width = w;
      canvas.height = h;
      var ctx = canvas.getContext("2d");
      ctx.fillStyle = "#fff"; // JPEG has no transparency
      ctx.fillRect(0, 0, w, h);
      ctx.drawImage(bitmap, 0, 0, w, h);
      for (var i = 0; i < QUALITIES.length; i++) {
        var blob = await toBlob(canvas, QUALITIES[i]);
        if (blob && blob.size <= MAX_BYTES) { return blob; }
      }
      w = Math.max(1, Math.round(w * 0.75));
      h = Math.max(1, Math.round(h * 0.75));
    }
    throw new Error("too large");
  }

  function kb(n) { return Math.round(n / 1024) + " KB"; }

  function ready(n) { return n + " " + t("foto siap", "photo(s) ready"); }

  // One shared pop-up to look at a photo full size.
  var lightbox;
  function view(src, alt) {
    if (!lightbox) {
      lightbox = document.createElement("dialog");
      lightbox.className = "photo-lightbox";
      var big = document.createElement("img");
      var close = document.createElement("button");
      close.type = "button";
      close.textContent = t("Tutup", "Close");
      close.addEventListener("click", function () { lightbox.close(); });
      lightbox.addEventListener("click", function (e) { if (e.target === lightbox) { lightbox.close(); } });
      lightbox.appendChild(big);
      lightbox.appendChild(close);
      document.body.appendChild(lightbox);
    }
    var img = lightbox.querySelector("img");
    img.src = src;
    img.alt = alt;
    if (typeof lightbox.showModal === "function") { lightbox.showModal(); } else { window.open(src, "_blank"); }
  }

  // ---- Camera ----
  // "Take photo" opens the camera where the device has one. On a phone or tablet (touch screen) it
  // opens the phone's own camera app through a second file input with capture; on a laptop or
  // desktop it opens a small window with the live webcam (getUserMedia, which browsers allow only on
  // https or localhost). Where neither works the button is not shown. Either way the picture goes
  // through the same compression as a picked file.
  var IS_TOUCH = !!(window.matchMedia && window.matchMedia("(pointer: coarse)").matches);
  var CAN_STREAM = !!(navigator.mediaDevices && navigator.mediaDevices.getUserMedia && window.isSecureContext);
  var CAN_CAMERA = IS_TOUCH || CAN_STREAM;

  var cameraDialog;
  // Shows the live camera and resolves with a JPEG File, or null when the user cancels or the camera
  // cannot be opened (the message goes to say()).
  function webcamShot(say) {
    return new Promise(function (resolve) {
      var stream = null, done = false;
      if (!cameraDialog) {
        cameraDialog = document.createElement("dialog");
        cameraDialog.className = "photo-camera";
        cameraDialog.innerHTML = '<video playsinline autoplay muted></video><div class="photo-camera-bar">' +
          '<button type="button" class="btn-primary" data-act="shoot"></button>' +
          '<button type="button" class="btn-ghost" data-act="cancel"></button></div>';
        document.body.appendChild(cameraDialog);
      }
      var video = cameraDialog.querySelector("video");
      var shoot = cameraDialog.querySelector('[data-act="shoot"]');
      var cancel = cameraDialog.querySelector('[data-act="cancel"]');
      shoot.textContent = t("Ambil foto", "Take photo");
      cancel.textContent = t("Batal", "Cancel");
      shoot.disabled = true;

      function finish(file) {
        if (done) { return; }
        done = true;
        if (stream) { stream.getTracks().forEach(function (tr) { tr.stop(); }); }
        video.srcObject = null;
        shoot.onclick = cancel.onclick = null;
        cameraDialog.onclose = null;
        if (cameraDialog.open) { cameraDialog.close(); }
        resolve(file);
      }
      cancel.onclick = function () { finish(null); };
      cameraDialog.onclose = function () { finish(null); };
      shoot.onclick = function () {
        if (!video.videoWidth) { return; }
        var c = document.createElement("canvas");
        c.width = video.videoWidth;
        c.height = video.videoHeight;
        c.getContext("2d").drawImage(video, 0, 0);
        c.toBlob(function (blob) {
          finish(blob ? new File([blob], "camera-" + Date.now() + ".jpg", { type: "image/jpeg" }) : null);
        }, "image/jpeg", 0.92);
      };

      navigator.mediaDevices.getUserMedia({ video: { facingMode: "environment" }, audio: false }).then(function (st) {
        if (done) { st.getTracks().forEach(function (tr) { tr.stop(); }); return; }
        stream = st;
        video.srcObject = st;
        shoot.disabled = false;
      }).catch(function () {
        say(t("Kamera tidak bisa dibuka. Periksa izin kamera, atau pilih foto dari galeri.",
          "The camera could not be opened. Check the camera permission, or pick a photo from the gallery."));
        finish(null);
      });
      if (typeof cameraDialog.showModal === "function") { cameraDialog.showModal(); }
    });
  }

  // The "Take photo" button under the boxes; null where there is no camera route.
  function cameraButton(state) {
    if (!CAN_CAMERA) { return null; }
    var btn = document.createElement("button");
    btn.type = "button";
    btn.className = "photo-camera-btn btn-ghost";
    btn.textContent = "\ud83d\udcf7 " + t("Ambil foto", "Take photo");
    var cap = null;
    if (IS_TOUCH) {
      // The phone's camera app. This input is not part of the form (no name): its picture is handed
      // to the main input's pipeline, which is what the form sends.
      cap = document.createElement("input");
      cap.type = "file";
      cap.accept = "image/*";
      cap.setAttribute("capture", "environment");
      cap.className = "photo-input-hidden";
      cap.tabIndex = -1;
      cap.setAttribute("aria-hidden", "true");
      cap.addEventListener("change", function () {
        var f = Array.prototype.slice.call(cap.files || []);
        cap.value = "";
        if (f.length) { state.addFiles(f); }
      });
    }
    btn.addEventListener("click", async function () {
      if (cap) { cap.click(); return; }
      var file = await webcamShot(state.say);
      if (file) { state.addFiles([file]); }
    });
    var wrap = document.createElement("div");
    wrap.className = "photo-camera-row";
    wrap.appendChild(btn);
    if (cap) { wrap.appendChild(cap); }
    state.camBtn = btn;
    return wrap;
  }

  // The photo boxes: always "max" small boxes. A filled box shows the thumbnail (click it to see the
  // photo full size) and a button to take it out; an empty box is a button that opens the picker.
  function render(state) {
    var el = state.previewEl;
    el.textContent = "";
    if (state.camBtn) { state.camBtn.disabled = state.busy || state.picked.length >= state.max; }
    for (var i = 0; i < state.max; i++) {
      var item = state.picked[i];
      if (!item) {
        var add = document.createElement("button");
        add.type = "button";
        add.className = "photo-slot photo-slot--empty";
        add.textContent = "+";
        add.disabled = state.busy;
        add.setAttribute("aria-label", t("Tambah foto", "Add a photo"));
        add.addEventListener("click", function () { state.input.click(); });
        el.appendChild(add);
        continue;
      }
      el.appendChild(filledSlot(state, item, i));
    }
  }

  function filledSlot(state, item, index) {
    var fig = document.createElement("figure");
    fig.className = "photo-slot";
    var look = document.createElement("button");
    look.type = "button";
    look.className = "photo-slot-view";
    look.setAttribute("aria-label", t("Lihat foto", "View photo") + " " + (index + 1));
    var img = document.createElement("img");
    img.alt = t("Foto", "Photo") + " " + (index + 1);
    img.src = item.url;
    look.appendChild(img);
    look.addEventListener("click", function () { view(item.url, img.alt); });
    var del = document.createElement("button");
    del.type = "button";
    del.className = "photo-slot-remove";
    del.textContent = "\u00d7";
    del.setAttribute("aria-label", t("Hapus foto", "Remove photo") + " " + (index + 1));
    del.addEventListener("click", function () {
      URL.revokeObjectURL(item.url);
      state.picked.splice(state.picked.indexOf(item), 1);
      sync(state);
      state.say(state.picked.length ? ready(state.picked.length) + "." : state.label);
    });
    fig.appendChild(look);
    fig.appendChild(del);
    return fig;
  }

  // The form sends what is in the input, so the input always holds exactly the photos in the boxes.
  function sync(state) {
    var out = new DataTransfer();
    state.picked.forEach(function (p) { out.items.add(p.file); });
    state.input.files = out.files;
    render(state);
  }

  window.attachPhotoCompress = function (input, statusEl, previewEl) {
    var max = parseInt(input.dataset.max || "4", 10);
    if (!previewEl) {
      previewEl = document.createElement("div");
      previewEl.className = "photo-preview";
      input.insertAdjacentElement("afterend", previewEl);
    }
    var state = { input: input, previewEl: previewEl, max: max, picked: [], busy: false,
      label: statusEl ? statusEl.textContent : "",
      say: function (msg) { if (statusEl) { statusEl.textContent = msg; } } };
    if (max <= 0) { return; }
    input.classList.add("photo-input-hidden"); // the boxes below are the picker now
    previewEl.classList.add("photo-slots");
    render(state);

    // Adds picked or photographed files: compresses each one and puts it in the next free box.
    state.addFiles = async function (files) {
      var room = max - state.picked.length;
      var note = "";
      if (files.length > room) {
        files = files.slice(0, Math.max(0, room));
        note = " " + t("Hanya " + max + " foto yang bisa dipakai.", "Only " + max + " photos can be used.");
      }
      if (!files.length) { state.say(note.trim()); return; }
      state.busy = true;
      sync(state);
      state.say(t("Mengompres foto\u2026", "Compressing photos\u2026"));
      var sizes = [];
      for (var i = 0; i < files.length; i++) {
        var f = files[i], file = f;
        try {
          var blob = await compress(f);
          file = new File([blob], f.name.replace(/\.[^.]+$/, "") + ".jpg", { type: "image/jpeg" });
          sizes.push(kb(blob.size));
        } catch (e) {
          // The browser could not read this picture (for example an unsupported format). Send it
          // as it is; the server will accept it only if it is a valid JPEG or PNG within its limits.
          sizes.push(kb(f.size) + " (" + t("belum dikompres", "not compressed") + ")");
        }
        state.picked.push({ file: file, url: URL.createObjectURL(file) });
      }
      state.busy = false;
      sync(state);
      state.say(ready(state.picked.length) + (sizes.length ? ": " + sizes.join(", ") : "") + "." + note);
    };

    var cam = cameraButton(state);
    if (cam) { previewEl.insertAdjacentElement("afterend", cam); render(state); }

    input.addEventListener("change", function () {
      var files = Array.prototype.slice.call(input.files || []);
      if (!files.length) { sync(state); return; } // picker cancelled: keep what is in the boxes
      state.addFiles(files);
    });
  };
})();
