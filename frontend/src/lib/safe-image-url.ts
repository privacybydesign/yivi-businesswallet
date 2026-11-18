// The schemes an image address may have: a picked file's blob:, an inline
// image, the API on another origin; a path is the wallet's own.
const SAFE_IMAGE_URL = /^(blob:|data:image\/|https?:\/\/|\/(?!\/))/i;

// uri when it can only load an image, URL-encoded, else "" (none). The
// encoding leaves the addresses allowed here as they are.
export function safeImageUrl(uri: string): string {
  return SAFE_IMAGE_URL.test(uri) ? encodeURI(uri) : "";
}
