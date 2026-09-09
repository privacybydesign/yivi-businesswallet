import { describe, expect, it } from "vitest";
import i18n from "../i18n";
import {
  identityStatusHint,
  identityStatusLabel,
  identityStatusTone,
  memberTypeLabel,
  needsIdentityBanner,
  requestableIdentity,
} from "./identity-status";

const t = i18n.getFixedT("en");
const formatDate = (iso: string) => iso.slice(0, 10);

// The statuses the backend derives (internal/organization/organization.go).
const STATUSES = ["never", "verified", "due_soon", "overdue", "requested"];

describe("identityStatusLabel", () => {
  it.each(STATUSES)("translates %s", (status) => {
    const label = identityStatusLabel(status, t);
    expect(label).not.toBe(status);
    expect(label.startsWith("members.")).toBe(false);
  });

  // A status this build does not know must read as itself rather than silently
  // borrowing another status's copy.
  it("falls back to the raw value for an unknown status", () => {
    expect(identityStatusLabel("suspended", t)).toBe("suspended");
  });
});

describe("identityStatusTone", () => {
  it("gives overdue the error tone and verified the success tone", () => {
    expect(identityStatusTone("overdue")).toBe("red");
    expect(identityStatusTone("verified")).toBe("green");
    expect(identityStatusTone("due_soon")).toBe("amber");
  });

  it("is neutral for an unknown status", () => {
    expect(identityStatusTone("suspended")).toBe("default");
  });
});

describe("identityStatusHint", () => {
  const member = {
    identityStatus: "overdue",
    identityDueAt: "2026-08-01T00:00:00Z",
    identityRequestedAt: "2026-09-01T00:00:00Z",
    identityVerifiedAt: "2025-08-01T00:00:00Z",
  };

  it("names the deadline when the member is due or overdue", () => {
    expect(identityStatusHint(member, t, formatDate)).toContain("2026-08-01");
    expect(
      identityStatusHint(
        { ...member, identityStatus: "due_soon" },
        t,
        formatDate,
      ),
    ).toContain("2026-08-01");
  });

  it("names when the request was made", () => {
    expect(
      identityStatusHint(
        { ...member, identityStatus: "requested" },
        t,
        formatDate,
      ),
    ).toContain("2026-09-01");
  });

  it("names when identity was last proved", () => {
    expect(
      identityStatusHint(
        { ...member, identityStatus: "verified" },
        t,
        formatDate,
      ),
    ).toContain("2025-08-01");
  });

  // A member with a status but no date behind it gets no hint rather than a
  // sentence with a blank in it.
  it("is empty when the date behind the status is missing", () => {
    expect(
      identityStatusHint(
        { ...member, identityDueAt: null, identityStatus: "overdue" },
        t,
        formatDate,
      ),
    ).toBe("");
  });
});

describe("requestableIdentity", () => {
  it("admits an active member who is not already asked", () => {
    expect(
      requestableIdentity({ status: "active", identityStatus: "overdue" }),
    ).toBe(true);
  });

  it("refuses a pending invitation, which has no membership to re-identify", () => {
    expect(
      requestableIdentity({ status: "invited", identityStatus: "never" }),
    ).toBe(false);
  });

  it("refuses a member who has already been asked", () => {
    expect(
      requestableIdentity({ status: "active", identityStatus: "requested" }),
    ).toBe(false);
  });
});

describe("memberTypeLabel", () => {
  it("translates both member types", () => {
    expect(memberTypeLabel("employee", t)).toBe("Employee");
    expect(memberTypeLabel("external", t)).toBe("External");
  });

  it("falls back to the raw value for an unknown type", () => {
    expect(memberTypeLabel("volunteer", t)).toBe("volunteer");
  });
});

describe("needsIdentityBanner", () => {
  it.each(["requested", "due_soon", "overdue"])(
    "interrupts the member when their identity is %s",
    (status) => {
      expect(needsIdentityBanner({ status })).toBe(true);
    },
  );

  // "never" is every membership's starting state, so a banner on it would page
  // everybody at once; "verified" has nothing to act on.
  it.each(["never", "verified"])(
    "stays quiet when the status is %s",
    (status) => {
      expect(needsIdentityBanner({ status })).toBe(false);
    },
  );

  it("stays quiet for a caller with no membership", () => {
    expect(needsIdentityBanner(undefined)).toBe(false);
  });
});
