// Compresses photos client-side right after the user picks them, so large
// camera photos don't get uploaded at full size. Files already under 500KB
// are left untouched; anything bigger is re-encoded as JPEG, backing off
// quality until it lands under 1MB (or we hit the quality floor).
const PHOTO_MIN_BYTES = 500 * 1024;
const PHOTO_MAX_BYTES = 1024 * 1024;
const PHOTO_MIN_QUALITY = 0.4;

async function compressImage(file, maxBytes = PHOTO_MAX_BYTES, minQuality = PHOTO_MIN_QUALITY) {
  const bitmap = await createImageBitmap(file);
  const canvas = document.createElement("canvas");
  canvas.width = bitmap.width;
  canvas.height = bitmap.height;
  canvas.getContext("2d").drawImage(bitmap, 0, 0);

  let quality = 0.92;
  let blob = await new Promise((resolve) => canvas.toBlob(resolve, "image/jpeg", quality));
  while (blob && blob.size > maxBytes && quality > minQuality) {
    quality -= 0.1;
    blob = await new Promise((resolve) => canvas.toBlob(resolve, "image/jpeg", quality));
  }
  return blob;
}

function compressPhotosInput(input, statusEl) {
  if (!input) return;
  input.addEventListener("change", async () => {
    const files = Array.from(input.files || []);
    if (!files.length) return;
    if (statusEl) statusEl.textContent = "Compressing photos…";
    const endBusy = typeof beginBusy === "function" ? beginBusy() : () => {};

    try {
      const dt = new DataTransfer();
      for (const file of files) {
        if (!file.type.startsWith("image/") || file.size <= PHOTO_MIN_BYTES) {
          dt.items.add(file);
          continue;
        }
        try {
          const blob = await compressImage(file);
          const name = file.name.replace(/\.[^.]+$/, "") + ".jpg";
          dt.items.add(blob.size < file.size ? new File([blob], name, { type: "image/jpeg" }) : file);
        } catch {
          dt.items.add(file);
        }
      }
      input.files = dt.files;
      if (statusEl) statusEl.textContent = "";
    } finally {
      endBusy();
    }
  });
}
