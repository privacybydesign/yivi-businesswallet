import { describe, expect, it } from "vitest";
import { resolveSubjectSource } from "./attestations";

describe("resolveSubjectSource", () => {
  const member = {
    kind: "natural_person" as const,
    member: {
      givenNames: "Anna",
      lastName: "de Vries",
      preferredName: "Ann",
      email: "anna@example.eu",
      phone: "+31600000000",
      role: "admin",
      jobTitle: "Engineering Lead",
      departmentName: "Platform",
    },
  };

  it("resolves each member field token", () => {
    expect(resolveSubjectSource("member.givenNames", member)).toBe("Anna");
    expect(resolveSubjectSource("member.lastName", member)).toBe("de Vries");
    expect(resolveSubjectSource("member.preferredName", member)).toBe("Ann");
    expect(resolveSubjectSource("member.email", member)).toBe(
      "anna@example.eu",
    );
    expect(resolveSubjectSource("member.phone", member)).toBe("+31600000000");
    expect(resolveSubjectSource("member.role", member)).toBe("admin");
    expect(resolveSubjectSource("member.jobTitle", member)).toBe(
      "Engineering Lead",
    );
    expect(resolveSubjectSource("member.department", member)).toBe("Platform");
  });

  it("composes fullName from given + last name", () => {
    expect(resolveSubjectSource("member.fullName", member)).toBe(
      "Anna de Vries",
    );
  });

  it("returns an empty string for a missing/null member field", () => {
    const sparse = {
      kind: "natural_person" as const,
      member: { givenNames: "Bob", lastName: null, phone: null },
    };
    expect(resolveSubjectSource("member.lastName", sparse)).toBe("");
    expect(resolveSubjectSource("member.phone", sparse)).toBe("");
    // fullName trims away the empty last name.
    expect(resolveSubjectSource("member.fullName", sparse)).toBe("Bob");
  });

  it("returns an empty string for an org token on a member subject", () => {
    expect(resolveSubjectSource("org.kvkNumber", member)).toBe("");
  });

  const org = {
    kind: "organization" as const,
    org: {
      name: "City of Utrecht (address book)",
      legalName: "Gemeente Utrecht",
      kvkNumber: "12345678",
      euid: "NL.KVK.12345678",
      address: "utrecht@qerds.eu",
    },
  };

  it("resolves each org field token", () => {
    // org.name prefers the legal name over the address-book display name.
    expect(resolveSubjectSource("org.name", org)).toBe("Gemeente Utrecht");
    expect(resolveSubjectSource("org.kvkNumber", org)).toBe("12345678");
    expect(resolveSubjectSource("org.euid", org)).toBe("NL.KVK.12345678");
    expect(resolveSubjectSource("org.digitalAddress", org)).toBe(
      "utrecht@qerds.eu",
    );
  });

  it("falls back to the display name when no legal name is set", () => {
    const noLegal = {
      kind: "organization" as const,
      org: { name: "Utrecht", address: "utrecht@qerds.eu" },
    };
    expect(resolveSubjectSource("org.name", noLegal)).toBe("Utrecht");
  });

  it("returns an empty string for an unknown token", () => {
    expect(resolveSubjectSource("member.unknown", member)).toBe("");
    expect(resolveSubjectSource("org.unknown", org)).toBe("");
  });
});
