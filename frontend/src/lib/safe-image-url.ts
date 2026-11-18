// The schemes an image address may have: a picked file's blob:, an inline
// image, the API on another origin; a path is the wallet's own.
const SAFE_IMAGE_URL = /^(blob:|data:image\/|https?:\/\/|\/(?!\/))/i;

// uri when it can only load an image, else "" (none).
export function safeImageUrl(uri: string): string {
  return SAFE_IMAGE_URL.test(uri) ? uri : "";
}
