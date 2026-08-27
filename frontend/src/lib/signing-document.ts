// Client-side check on a chosen signing document, so a PDF the API would refuse is
// caught before it is uploaded rather than after the whole file has gone up. Kept in
// step with the backend, which enforces the same cap authoritatively (maxUploadBytes
// in backend/internal/signing/handler.go).
export const MAX_DOCUMENT_BYTES = 25 * 1024 * 1024;

export function documentIsTooLarge(file: { size: number }): boolean {
  return file.size > MAX_DOCUMENT_BYTES;
}
