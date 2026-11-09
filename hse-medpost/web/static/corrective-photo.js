// Shrinks a photo in the browser before it is uploaded, so the server receives (and stores) a picture under
// 600 KB. The server checks the same limit again; this exists so a 12 MB phone photo is not sent over the
// clinic's connection first, and so the picture is turned upright here (a re-encode on the server drops the
// camera's rotation tag).
//
// Needs corrective-core.js. Throws an Error with a message fit to show the user.
async function caReducePhoto(file) {
  if (!caIsAcceptedPhotoType(file.type)) {
    throw new Error("“" + file.name + "” is not a JPEG or PNG photo.");
  }
  if (!caNeedsReduction(file.type, file.size)) return file;

  const bitmap = await caDecodeUpright(file);
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
