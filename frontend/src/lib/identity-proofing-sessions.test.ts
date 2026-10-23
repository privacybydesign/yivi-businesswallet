import { describe, expect, it } from "vitest";
import i18n from "../i18n";
import type { AuditEvent } from "../api/organization";
import {
  dataRequestKindOf,
  imageSource,
  isCheckOutcome,
  requestTarget,
  sendFormDecision,
  sendKindFlows,
  showsSessionIdentity,
  timelineActor,
  verifySearch,
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

const unlinked = {
  id: "unlinked",
  requestedAttributes: [],
  diplomaMode: "off" as const,
  hostedEnabled: false,
};

const base = {
  sendable: [plain, matching, diplomas, unlinked],
  initial: plain,
  picked: "",
  channel: "email" as const,
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

  it("can check for one person only on a flow reading the identity", () => {
    expect(sendFormDecision({ ...base, picked: "plain" }).matchable).toBe(
      false,
    );
    expect(sendFormDecision({ ...base, picked: "matching" }).matchable).toBe(
      true,
    );
  });

  it("keeps the channel for a flow that can be mailed", () => {
    expect(sendFormDecision(base).delivery).toBe("email");
    expect(sendFormDecision({ ...base, channel: "hosted" }).delivery).toBe(
      "hosted",
    );
  });

  it("runs a flow not sent as a link on screen, neither mailed nor linked", () => {
    for (const channel of ["email", "hosted", "on_screen"] as const) {
      const d = sendFormDecision({ ...base, picked: "unlinked", channel });
      expect(d.linkable).toBe(false);
      expect(d.delivery).toBe("on_screen");
    }
    expect(sendFormDecision(base).linkable).toBe(true);
  });
});

describe("what the send form sends", () => {
  const identity = { id: "check", completable: true, default: true };
  const notHere = { id: "photo", completable: false, default: false };
  const access = {
    id: "see",
    completable: true,
    default: false,
    kind: "data_access",
  };
  const erasure = {
    id: "delete",
    completable: true,
    default: false,
    kind: "data_erasure",
  };

  it("sends an identity check on a flow, a data request on its own flow", () => {
    expect(sendKindFlows("identity", [identity, notHere], undefined)).toEqual({
      sendable: [identity],
      initial: identity,
    });
    expect(
      sendKindFlows("data_erasure", [identity], [access, erasure]),
    ).toEqual({ sendable: [erasure], initial: erasure });
    expect(sendKindFlows("data_access", [identity], undefined)).toEqual({
      sendable: [],
      initial: undefined,
    });
  });

  it("names the flow of an identity check, the kind of a data request", () => {
    expect(requestTarget("identity", "check")).toEqual({ flowId: "check" });
    expect(requestTarget("data_access", "see")).toEqual({
      flowKind: "data_access",
    });
  });

  it("opens the on-screen page on the flow, or on the data request", () => {
    expect(verifySearch("identity", "check")).toBe("flow=check");
    expect(verifySearch("data_erasure", "delete")).toBe("kind=data_erasure");
    expect(dataRequestKindOf("data_access")).toBe("data_access");
    expect(dataRequestKindOf("identity")).toBeUndefined();
    expect(dataRequestKindOf(null)).toBeUndefined();
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
