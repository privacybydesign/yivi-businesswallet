import { describe, expect, it } from "vitest";
import {
  MAX_DOCUMENT_BYTES,
  documentIsTooLarge,
  pdfIsEncrypted,
} from "./signing-document";

describe("documentIsTooLarge", () => {
  it("accepts a document over the 10 MiB cap the backend used to enforce", () => {
    expect(documentIsTooLarge({ size: 12 * 1024 * 1024 })).toBe(false);
  });

  it("rejects a document over the upload cap but accepts one exactly at it", () => {
    expect(documentIsTooLarge({ size: MAX_DOCUMENT_BYTES + 1 })).toBe(true);
    expect(documentIsTooLarge({ size: MAX_DOCUMENT_BYTES })).toBe(false);
  });
});

function pdf(text: string): Uint8Array {
  return new TextEncoder().encode(text);
}

describe("pdfIsEncrypted", () => {
  const body = "%PDF-1.4\n1 0 obj\n<</Type/Catalog>>\nendobj\n";

  it("finds the encryption dictionary a trailer names", () => {
    const text = `${body}xref\n0 2\ntrailer\n<</Size 2/Root 1 0 R/Encrypt 5 0 R>>\nstartxref\n${body.length}\n%%EOF\n`;
    expect(pdfIsEncrypted(pdf(text))).toBe(true);
  });

  it("finds it in a cross-reference stream's dictionary", () => {
    const text = `${body}7 0 obj\n<</Type/XRef/Size 8/Encrypt 5 0 R>>\nstream\nxx\nendstream\nendobj\nstartxref\n${body.length}\n%%EOF\n`;
    expect(pdfIsEncrypted(pdf(text))).toBe(true);
  });

  it("is not fooled by the word elsewhere in a plain document", () => {
    const withWord = `${body}2 0 obj\n(see /Encrypt)\nendobj\n`;
    const text = `${withWord}xref\n0 3\ntrailer\n<</Size 3/Root 1 0 R>>\nstartxref\n${withWord.length}\n%%EOF\n`;
    expect(pdfIsEncrypted(pdf(text))).toBe(false);
  });

  it("says no for what is not a PDF", () => {
    expect(pdfIsEncrypted(pdf("hello"))).toBe(false);
  });
});
