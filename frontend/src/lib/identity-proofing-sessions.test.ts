import { describe, expect, it } from "vitest";
import i18n from "../i18n";
import type { AuditEvent } from "../api/organization";
import {
  imageSource,
  isCheckOutcome,
  sendFormDecision,
  showsSessionIdentity,
  timelineActor,
} from "./identity-proofing";

const t = i18n.getFixedT("en");

const plain = {
  id: "plain",
  requestedAttributes: [],
  diplomaMode: "off" as const,
};
const matching = {
  id: "matching",
  requestedAttributes: ["dg1"],
  diplomaMode: "off" as const,
};
const diplomas = {
  id: "diplomas",
  requestedAttributes: [],
  diplomaMode: "required" as const,
};

const base = {
  sendable: [plain, matching, diplomas],
  initial: plain,
  picked: "",
  channel: "email" as const,
  email: "anna@example.org",
  name: "",
  birthDate: "",
};

describe("sendFormDecision", () => {
  it("falls back to the default for a pick no longer sendable", () => {
    expect(sendFormDecision({ ...base, picked: "gone" }).flowId).toBe("plain");
    expect(sendFormDecision({ ...base, picked: "matching" }).flowId).toBe(
      "matching",
    );
  });

  it("runs a flow asking for diplomas on screen, never by mail", () => {
    const d = sendFormDecision({ ...base, picked: "diplomas" });
    expect(d.mailable).toBe(false);
    expect(d.delivery).toBe("on_screen");
  });

  it("needs an address for a mail, and a valid one if given otherwise", () => {
    expect(sendFormDecision({ ...base, email: "" }).emailMissing).toBe(true);
    expect(
      sendFormDecision({ ...base, channel: "on_screen", email: "" })
        .emailMissing,
    ).toBe(false);
    expect(
      sendFormDecision({ ...base, channel: "on_screen", email: "nope" })
        .emailMissing,
    ).toBe(true);
  });

  it("checks for one person only on a flow reading the identity, then needs the name", () => {
    expect(
      sendFormDecision({ ...base, birthDate: "1990-04-12" }).expectedBirthDate,
    ).toBe("");
    const d = sendFormDecision({
      ...base,
      picked: "matching",
      birthDate: "1990-04-12",
    });
    expect(d.expectedBirthDate).toBe("1990-04-12");
    expect(d.nameMissing).toBe(true);
    expect(
      sendFormDecision({
        ...base,
        picked: "matching",
        birthDate: "1990-04-12",
        name: "Anna",
      }).nameMissing,
    ).toBe(false);
  });
});

describe("showsSessionIdentity", () => {
  it("shows an admin the identity of a session with an outcome, never a purged one", () => {
    expect(
      showsSessionIdentity(
        { status: "needs_review", purgedAt: undefined },
        true,
      ),
    ).toBe(true);
    expect(
      showsSessionIdentity({ status: "approved", purgedAt: undefined }, false),
    ).toBe(false);
    expect(
      showsSessionIdentity({ status: "pending", purgedAt: undefined }, true),
    ).toBe(false);
    expect(
      showsSessionIdentity(
        { status: "approved", purgedAt: "2026-10-09T10:00:00Z" },
        true,
      ),
    ).toBe(false);
  });
});

describe("session detail helpers", () => {
  it("knows the check outcomes", () => {
    expect(isCheckOutcome("valid")).toBe(true);
    expect(isCheckOutcome("maybe")).toBe(false);
  });

  it("makes an image a data URL", () => {
    expect(imageSource({ mimeType: "image/png", data: "AAA" })).toBe(
      "data:image/png;base64,AAA",
    );
  });

  it("names a timeline event's actor", () => {
    const event = (over: Partial<AuditEvent>): AuditEvent => ({
      id: "e",
      occurredAt: "2026-10-09T10:00:00Z",
      action: "identity_proofing.requested",
      targetType: "identity_proofing_request",
      targetId: "r",
      metadata: {},
      actor: null,
      ...over,
    });
    expect(
      timelineActor(
        event({
          actor: {
            userId: "u",
            givenNames: "Sam",
            lastName: "Smit",
            preferredName: null,
            avatarUri: "",
          },
        }),
        {},
        t,
        [],
      ),
    ).toBe("Sam Smit");
    expect(
      timelineActor(event({}), { apiKeyName: "backend" }, t, []),
    ).toContain("backend");
    expect(
      timelineActor(event({ action: "identity_proofing.approved" }), {}, t, []),
    ).toBe(t("auditLog.system"));
    expect(
      timelineActor(event({ action: "identity_proofing.approved" }), {}, t, [
        "detail",
      ]),
    ).toBeNull();
  });
});
