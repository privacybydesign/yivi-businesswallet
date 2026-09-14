import { describe, expect, it } from "vitest";
import { isIdentityRejection, needsIdentityFirst } from "./vog-page";

describe("needsIdentityFirst", () => {
  it("asks for identity when the org detail says there is no date of birth", () => {
    expect(needsIdentityFirst({ needsIdentity: true }, [])).toBe(true);
  });

  it("asks for identity when a check was refused for that reason", () => {
    expect(
      needsIdentityFirst({ needsIdentity: false }, [null, "no_date_of_birth"]),
    ).toBe(true);
  });

  it("does not ask otherwise, including with no own state at all", () => {
    expect(needsIdentityFirst({ needsIdentity: false }, [null])).toBe(false);
    expect(needsIdentityFirst(undefined, [])).toBe(false);
    expect(needsIdentityFirst(null, ["identity_mismatch"])).toBe(false);
  });
});

describe("isIdentityRejection", () => {
  it("recognises the re-identification codes", () => {
    for (const code of [
      "email_mismatch",
      "name_mismatch",
      "credential_too_old",
      "disclosure_failed",
    ]) {
      expect(isIdentityRejection(code)).toBe(true);
    }
  });

  it("leaves VOG outcomes and unknown codes alone", () => {
    expect(isIdentityRejection("vog_credential_not_accepted")).toBe(false);
    expect(isIdentityRejection(null)).toBe(false);
  });
});
