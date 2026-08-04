import { describe, expect, it } from "vitest";
import {
  fingerprint,
  subscriptionsDiffer,
  toggleChannel,
} from "./notification-subscriptions";
import type { Subscriptions } from "./notification-subscriptions";

describe("fingerprint / subscriptionsDiffer", () => {
  it("is order-independent across events and channels", () => {
    const a: Subscriptions = {
      "attestation.issued": ["email", "slack"],
      "membership.invited": ["email"],
    };
    const b: Subscriptions = {
      "membership.invited": ["email"],
      "attestation.issued": ["slack", "email"],
    };
    expect(fingerprint(a)).toBe(fingerprint(b));
    expect(subscriptionsDiffer(a, b)).toBe(false);
  });

  it("treats an empty channel list as no subscription", () => {
    expect(subscriptionsDiffer({ "a.b": [] }, {})).toBe(false);
  });

  it("detects a genuine difference", () => {
    expect(
      subscriptionsDiffer({ "a.b": ["email"] }, { "a.b": ["slack"] }),
    ).toBe(true);
  });
});

describe("toggleChannel", () => {
  it("adds a channel in canonical order", () => {
    const next = toggleChannel({ "a.b": ["slack"] }, "a.b", "email");
    // NOTIFICATION_CHANNELS order is email, slack, msteams.
    expect(next["a.b"]).toEqual(["email", "slack"]);
  });

  it("removes a channel and drops the event when it empties", () => {
    const next = toggleChannel({ "a.b": ["email"] }, "a.b", "email");
    expect(next).toEqual({});
  });

  it("preserves the channels it is not toggling through a rebuild", () => {
    // The save is a full replacement of the document, so a channel the admin did
    // not touch has to come back out of the rebuild — including one this build
    // renders no column for, whose subscription would otherwise be dropped.
    const next = toggleChannel({ "a.b": ["msteams"] }, "a.b", "email");
    expect(next["a.b"]).toEqual(["email", "msteams"]);
  });

  it("does not mutate its input", () => {
    const subs: Subscriptions = { "a.b": ["email"] };
    toggleChannel(subs, "a.b", "slack");
    expect(subs).toEqual({ "a.b": ["email"] });
  });
});
