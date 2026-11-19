import { describe, expect, it } from "vitest";
import { MAX_DOCUMENT_BYTES, documentIsTooLarge } from "./signing-document";

describe("documentIsTooLarge", () => {
  it("accepts a document over the 10 MiB cap the backend used to enforce", () => {
    expect(documentIsTooLarge({ size: 12 * 1024 * 1024 })).toBe(false);
  });

  it("rejects a document over the upload cap but accepts one exactly at it", () => {
    expect(documentIsTooLarge({ size: MAX_DOCUMENT_BYTES + 1 })).toBe(true);
    expect(documentIsTooLarge({ size: MAX_DOCUMENT_BYTES })).toBe(false);
  });
});
