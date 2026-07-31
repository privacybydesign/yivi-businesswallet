import { describe, expect, it } from "vitest";
import {
  ACCEPTED_AVATAR_TYPES,
  MAX_AVATAR_BYTES,
  avatarFileProblem,
} from "./avatar-file";

describe("avatarFileProblem", () => {
  it("accepts every supported photo format", () => {
    for (const type of ACCEPTED_AVATAR_TYPES) {
      expect(avatarFileProblem({ type, size: 1024 })).toBeNull();
    }
  });

  it("rejects a format the backend cannot decode", () => {
    expect(avatarFileProblem({ type: "image/svg+xml", size: 1024 })).toBe(
      "type",
    );
    expect(avatarFileProblem({ type: "application/pdf", size: 1024 })).toBe(
      "type",
    );
    expect(avatarFileProblem({ type: "", size: 1024 })).toBe("type");
  });

  it("rejects an empty file", () => {
    expect(avatarFileProblem({ type: "image/png", size: 0 })).toBe("empty");
  });

  it("rejects a file over the upload cap but accepts one exactly at it", () => {
    expect(
      avatarFileProblem({ type: "image/jpeg", size: MAX_AVATAR_BYTES + 1 }),
    ).toBe("size");
    expect(
      avatarFileProblem({ type: "image/jpeg", size: MAX_AVATAR_BYTES }),
    ).toBeNull();
  });
});
