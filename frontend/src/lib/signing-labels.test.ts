import { describe, expect, it } from "vitest";
import type { TFunction } from "i18next";
import { requestedByLabel } from "./signing-labels";

// Stands in for i18next: the key, and the name it was given.
const t = ((key: string, options?: { name?: string }) =>
  options?.name === undefined ? key : `${key}:${options.name}`) as TFunction;

describe("requestedByLabel", () => {
  it("names the member who made the request", () => {
    expect(
      requestedByLabel(t, {
        createdVia: "member",
        createdByName: "Ada Lovelace",
      }),
    ).toBe("signing.requestedBy:Ada Lovelace");
  });

  it("names the API for a request made over it, which has no creator", () => {
    expect(requestedByLabel(t, { createdVia: "api", createdByName: "" })).toBe(
      "signing.requestedViaApi",
    );
  });
});
