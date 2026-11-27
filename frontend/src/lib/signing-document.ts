// Client-side check on a chosen signing document, so a PDF the API would refuse is
// caught before it is uploaded rather than after the whole file has gone up. Kept in
// step with the backend, which enforces the same cap authoritatively (maxUploadBytes
// in backend/internal/signing/handler.go).
export const MAX_DOCUMENT_BYTES = 25 * 1024 * 1024;

export function documentIsTooLarge(file: { size: number }): boolean {
  return file.size > MAX_DOCUMENT_BYTES;
}

const STARTXREF = "startxref";
const ENCRYPT_KEY = "/Encrypt";

// pdfIsEncrypted reports whether a PDF's trailer names an encryption dictionary,
// as that of a document protected against editing does even when it opens without
// a password. The backend refuses such a document (ErrEncryptedPDF in
// backend/internal/signing): a signature appended to it would not show in every
// reader. Only the trailer is read, not the whole file for the word: the trailer
// is the dictionary after the last cross-reference table, or the dictionary of the
// cross-reference stream that the end of the file points at.
export function pdfIsEncrypted(bytes: Uint8Array): boolean {
  const text = new TextDecoder("latin1").decode(bytes);
  const pointer = text.lastIndexOf(STARTXREF);
  if (pointer < 0) return false;
  const offset = Number.parseInt(text.slice(pointer + STARTXREF.length), 10);
  if (!Number.isInteger(offset) || offset < 0 || offset >= pointer) {
    return false;
  }
  if (text.startsWith("xref", offset)) {
    const trailer = text.lastIndexOf("trailer", pointer);
    return (
      trailer > offset && text.slice(trailer, pointer).includes(ENCRYPT_KEY)
    );
  }
  const stream = text.indexOf("stream", offset);
  return stream > offset && text.slice(offset, stream).includes(ENCRYPT_KEY);
}
