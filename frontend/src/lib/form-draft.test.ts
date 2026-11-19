import { describe, expect, it } from "vitest";
import { draftChanged } from "./form-draft";

describe("draftChanged", () => {
  const initial = {
    host: "smtp.example",
    port: "587",
    enabled: true,
    secret: "",
  };

  it("is false while every field still holds what the form opened with", () => {
    expect(draftChanged({ ...initial }, initial)).toBe(false);
  });

  it("is true once any field differs, a typed secret included", () => {
    expect(draftChanged({ ...initial, enabled: false }, initial)).toBe(true);
    expect(draftChanged({ ...initial, secret: "s3cret" }, initial)).toBe(true);
  });

  it("is false again when an edit is undone", () => {
    const draft = { ...initial, host: "other" };
    draft.host = initial.host;
    expect(draftChanged(draft, initial)).toBe(false);
  });
});
