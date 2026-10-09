import { describe, expect, it } from "vitest";
import { modalDismissal } from "./modal-dismiss";

describe("modalDismissal", () => {
  const onClose = (): void => {};

  it("closes a dismissible modal", () => {
    expect(modalDismissal({ dismissible: true, onClose })).toBe(onClose);
  });

  // A one-time secret (SecretReveal) must not vanish by a stray click.
  it("keeps a non-dismissible modal open", () => {
    expect(modalDismissal({ dismissible: false, onClose })).toBeUndefined();
  });
});
