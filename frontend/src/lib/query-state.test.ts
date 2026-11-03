import { describe, expect, it } from "vitest";
import { blockingError } from "./query-state";

const failed = new Error("down");

describe("blockingError", () => {
  it("is null while every read is loading or loaded", () => {
    expect(blockingError({ data: undefined, error: null })).toBeNull();
    expect(blockingError({ data: [], error: null })).toBeNull();
  });

  it("keeps a read that loaded before its refetch failed", () => {
    expect(blockingError({ data: [], error: failed })).toBeNull();
  });

  it("is the error of a read with nothing loaded", () => {
    expect(
      blockingError(
        { data: {}, error: null },
        { data: undefined, error: failed },
      ),
    ).toBe(failed);
  });
});
