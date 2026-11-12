// Shrinks a photo in the browser before it is uploaded, so the server receives (and stores) a picture under
// 600 KB. The server checks the same limit again; this exists so a 12 MB phone photo is not sent over the
// clinic's connection first, and so the picture is turned upright here (a re-encode on the server drops the
// camera's rotation tag).
//
// A PDF is accepted too: its first page is drawn with pdf.js and stored as the photo, so a signed report
// or a receipt can stand in as the After "photo" that closing a finding needs. The PDF itself is not kept;
// to keep the file, attach it under Documents.
//
// Needs corrective-core.js. Throws an Error with a message fit to show the user.
async function caReducePhoto(file) {
  const isPdf = caIsPdfFile(file);
  if (!isPdf && !caIsAcceptedPhotoType(file.type)) {
    throw new Error("“" + file.name + "” is not a JPEG or PNG photo or a PDF.");
  }
  if (!isPdf && !caNeedsReduction(file.type, file.size)) return file;

  const bitmap = isPdf ? await caRenderPdfFirstPage(file) : await caDecodeUpright(file);
  try {
    for (let side = CA_MAX_PHOTO_SIDE; side >= CA_MIN_PHOTO_SIDE; side = caNextSide(side)) {
      const size = caPhotoTarget(bitmap.width, bitmap.height, side);
      const canvas = document.createElement("canvas");
      canvas.width = size.width;
      canvas.height = size.height;
      const ctx = canvas.getContext("2d");
      // JPEG has no transparency: paint white first so a transparent PNG does not turn black.
      ctx.fillStyle = "#fff";
      ctx.fillRect(0, 0, size.width, size.height);
      ctx.drawImage(bitmap, 0, 0, size.width, size.height);
      for (const quality of caQualitySteps()) {
        const blob = await new Promise((resolve) => canvas.toBlob(resolve, "image/jpeg", quality));
        if (blob && blob.size <= CA_MAX_PHOTO_BYTES) {
          return new File([blob], caJpegName(file.name), { type: "image/jpeg" });
        }
      }
    }
  } finally {
    if (bitmap.close) bitmap.close();
  }
  throw new Error("“" + file.name + "” could not be reduced below 600 KB.");
}

// createImageBitmap with imageOrientation "from-image" applies the camera's rotation, so the photo is
// stored upright. Older browsers reject the option object: retry without it.
async function caDecodeUpright(file) {
  try {
    return await createImageBitmap(file, { imageOrientation: "from-image" });
  } catch (err) {
    try {
      return await createImageBitmap(file);
    } catch (err2) {
      throw new Error("“" + file.name + "” could not be read as a photo.");
    }
  }
}

function caJpegName(name) {
  const stem = String(name || "photo").replace(/\.[^.]+$/, "") || "photo";
  return stem + ".jpg";
}

function caLoadScript(src) {
  return new Promise((resolve, reject) => {
    const tag = document.createElement("script");
    tag.src = src;
    tag.onload = resolve;
    tag.onerror = () => reject(new Error("could not load " + src));
    document.head.append(tag);
  });
}

// Draws page 1 of a PDF onto a white canvas whose longer edge is CA_MAX_PHOTO_SIDE, and returns the canvas
// (drawImage accepts it like a bitmap, so the shrink loop above treats both the same). pdf.js is loaded
// only when someone picks a PDF; its files are already served from /static/vendor for the MCU portal.
async function caRenderPdfFirstPage(file) {
  let page;
  try {
    if (!window.pdfjsLib) await caLoadScript("/static/vendor/pdf.min.js?v=1");
    window.pdfjsLib.GlobalWorkerOptions.workerSrc = "/static/vendor/pdf.worker.min.js?v=1";
    // isEvalSupported off: pdf.js would otherwise try to compile fonts with eval.
    const pdf = await window.pdfjsLib.getDocument({ data: await file.arrayBuffer(), isEvalSupported: false }).promise;
    page = await pdf.getPage(1);
  } catch (err) {
    throw new Error("“" + file.name + "” could not be read as a PDF.");
  }
  const natural = page.getViewport({ scale: 1 });
  const viewport = page.getViewport({ scale: CA_MAX_PHOTO_SIDE / Math.max(natural.width, natural.height) });
  const canvas = document.createElement("canvas");
  canvas.width = Math.ceil(viewport.width);
  canvas.height = Math.ceil(viewport.height);
  const ctx = canvas.getContext("2d");
  ctx.fillStyle = "#fff"; // an unpainted PDF page is transparent and would turn black as a JPEG
  ctx.fillRect(0, 0, canvas.width, canvas.height);
  try {
    await page.render({ canvasContext: ctx, viewport }).promise;
  } catch (err) {
    throw new Error("“" + file.name + "” could not be drawn as a picture.");
  }
  return canvas;
}
