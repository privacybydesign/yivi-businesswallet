import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "./http";
import { vogOutcomeFromError } from "./organization";
import type { Organization } from "./organization";

// withAbsoluteLogos turns each org's API-relative logo path into an absolute URL
// so an <img> loads it from the API origin even when the SPA is served
// elsewhere. absoluteApiUrl reads VITE_API_BASE_URL at module load, so re-import
// the module per base to exercise the prefixing.
async function loadWithBase(base: string) {
  vi.resetModules();
  vi.stubEnv("VITE_API_BASE_URL", base);
  return import("./organization");
}

function org(overrides: Partial<Organization>): Organization {
  return {
    id: "org-1",
    name: "Acme",
    slug: "acme",
    kvkNumber: "",
    euid: "",
    digitalAddress: "",
    status: "active",
    bootstrappedAt: "",
    ...overrides,
  };
}

afterEach(() => {
  vi.unstubAllEnvs();
  vi.resetModules();
});

describe("withAbsoluteLogos", () => {
  it("leaves the relative path unchanged when no API base is set", async () => {
    const { withAbsoluteLogos } = await loadWithBase("");
    const path = "/api/v1/orgs/acme/theme/logo?v=1700000000";
    expect(withAbsoluteLogos([org({ logoUri: path })])[0].logoUri).toBe(path);
  });

  it("prefixes the API base when the SPA is served elsewhere", async () => {
    const { withAbsoluteLogos } = await loadWithBase(
      "https://api.example.test",
    );
    expect(
      withAbsoluteLogos([
        org({ logoUri: "/api/v1/orgs/acme/theme/logo?v=1" }),
      ])[0].logoUri,
    ).toBe("https://api.example.test/api/v1/orgs/acme/theme/logo?v=1");
  });

  it("leaves an org without a logo untouched", async () => {
    const { withAbsoluteLogos } = await loadWithBase(
      "https://api.example.test",
    );
    const [result] = withAbsoluteLogos([org({})]);
    expect(result.logoUri).toBeUndefined();
  });

  it("does not mutate the input orgs", async () => {
    const { withAbsoluteLogos } = await loadWithBase(
      "https://api.example.test",
    );
    const input = org({ logoUri: "/api/v1/orgs/acme/theme/logo?v=1" });
    withAbsoluteLogos([input]);
    expect(input.logoUri).toBe("/api/v1/orgs/acme/theme/logo?v=1");
  });
});

describe("vogOutcomeFromError", () => {
  it("recovers a rejected VOG's outcome from a 422 body", () => {
    const error = new ApiError(422, "Unprocessable Entity", "/x", {
      result: "rejected",
      rejectionReason: "too_old",
    });
    expect(vogOutcomeFromError(error)).toEqual({
      result: "rejected",
      rejectionReason: "too_old",
    });
  });

  it("returns null for a status other than 422", () => {
    const error = new ApiError(500, "Internal Server Error", "/x", {
      result: "rejected",
      rejectionReason: "too_old",
    });
    expect(vogOutcomeFromError(error)).toBeNull();
  });

  it("returns null when the 422 body does not match the outcome shape", () => {
    const error = new ApiError(422, "Unprocessable Entity", "/x", {
      error: "invalid_body",
      code: "invalid_body",
    });
    expect(vogOutcomeFromError(error)).toBeNull();
  });

  it("returns null for a non-ApiError", () => {
    expect(vogOutcomeFromError(new Error("network down"))).toBeNull();
  });
});
