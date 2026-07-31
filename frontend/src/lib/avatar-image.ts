import { AVATAR_TARGET_DIMENSION, MAX_AVATAR_BYTES } from "../api/avatar";

// JPEG quality for the downscaled portrait. 0.85 is the usual "no visible loss at
// this size" setting; the backend re-encodes at the same quality.
const JPEG_QUALITY = 0.85;

const JPEG_TYPE = "image/jpeg";

export interface Dimensions {
  width: number;
  height: number;
}

// scaleToFit returns the size that fits `source` inside a max-by-max box while
// keeping its aspect ratio. An image already inside the box is returned
// unchanged, so a small photo is never blown up; sides round to at least 1 pixel.
export function scaleToFit(source: Dimensions, max: number): Dimensions {
  const longest = Math.max(source.width, source.height);
  if (longest <= max) {
    return { width: source.width, height: source.height };
  }
  const factor = max / longest;
  return {
    width: Math.max(1, Math.round(source.width * factor)),
    height: Math.max(1, Math.round(source.height * factor)),
  };
}

// Why preparing failed: "unreadable" means the browser could not decode the
// picked file as an image, "tooLarge" that the downscaled result is still over the
// size the API accepts.
export type PrepareAvatarResult =
  | { ok: true; photo: Blob }
  | { ok: false; reason: "unreadable" | "tooLarge" };

// prepareAvatar turns a picked file into the JPEG the API stores: decoded by the
// browser, downscaled to AVATAR_TARGET_DIMENSION and re-encoded. Going through a
// canvas is what makes an ordinary phone photo uploadable at all — it is several
// megabytes and thousands of pixels wide, both above what the API accepts — and
// re-encoding drops the file's metadata on the way, so the location the photo was
// taken at never leaves the device. The server re-encodes again and enforces the
// same limits, because a request need not come from this code.
export async function prepareAvatar(file: File): Promise<PrepareAvatarResult> {
  let bitmap: ImageBitmap;
  try {
    // Without from-image the EXIF orientation is ignored and a portrait taken in
    // landscape hold ends up on its side.
    bitmap = await createImageBitmap(file, { imageOrientation: "from-image" });
  } catch {
    return { ok: false, reason: "unreadable" };
  }

  try {
    const size = scaleToFit(bitmap, AVATAR_TARGET_DIMENSION);
    const canvas = document.createElement("canvas");
    canvas.width = size.width;
    canvas.height = size.height;
    const context = canvas.getContext("2d");
    if (!context) {
      return { ok: false, reason: "unreadable" };
    }
    context.drawImage(bitmap, 0, 0, size.width, size.height);

    const photo = await new Promise<Blob | null>((resolve) =>
      canvas.toBlob(resolve, JPEG_TYPE, JPEG_QUALITY),
    );
    if (!photo) {
      return { ok: false, reason: "unreadable" };
    }
    if (photo.size > MAX_AVATAR_BYTES) {
      return { ok: false, reason: "tooLarge" };
    }
    return { ok: true, photo };
  } finally {
    bitmap.close();
  }
}
