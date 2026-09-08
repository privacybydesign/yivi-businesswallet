import { describe, expect, it } from "vitest";
import { ApiError } from "../api/http";
import i18n from "../i18n";
import { reidentifyError } from "./reidentify-error";

const t = i18n.getFixedT("en");

function apiError(code: string, status = 409): ApiError {
  return new ApiError(status, "Conflict", "/api/v1/reidentify/x/complete", {
    code,
  });
}

describe("reidentifyError", () => {
  // Each backend code (organization.mapReverifyError) has to reach copy that
  // tells the member what to do, not a generic failure.
  it.each([
    ["reidentify_link_not_found", "reidentify.errors.linkTitle"],
    ["email_mismatch", "reidentify.errors.emailTitle"],
    ["name_mismatch", "reidentify.errors.nameTitle"],
    ["credential_too_old", "reidentify.errors.staleTitle"],
  ])("maps %s to its own copy", (code, titleKey) => {
    const content = reidentifyError(apiError(code), t);
    expect(content.title).toBe(t(titleKey as "reidentify.errors.linkTitle"));
    expect(content.body).not.toBe("");
  });

  it("falls back to the generic message for an unmapped failure", () => {
    const content = reidentifyError(new Error("boom"), t);
    expect(content.title).toBe(t("reidentify.errors.genericTitle"));
    expect(content.body).toContain("boom");
  });
});
