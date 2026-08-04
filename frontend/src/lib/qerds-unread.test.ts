import { describe, expect, it } from "vitest";
import { pruneSeen, unreadInboundIds } from "./qerds-unread";

describe("unreadInboundIds", () => {
  it("returns inbound ids not present in the seen set", () => {
    expect(unreadInboundIds(["a", "b", "c"], ["b"])).toEqual(["a", "c"]);
  });

  it("returns nothing when every inbound id has been seen", () => {
    expect(unreadInboundIds(["a", "b"], ["a", "b", "x"])).toEqual([]);
  });

  it("treats an empty seen set as everything unread", () => {
    expect(unreadInboundIds(["a", "b"], [])).toEqual(["a", "b"]);
  });

  it("preserves the inbound order", () => {
    expect(unreadInboundIds(["c", "a", "b"], ["a"])).toEqual(["c", "b"]);
  });
});

describe("pruneSeen", () => {
  it("drops seen ids no longer in the inbox", () => {
    expect(pruneSeen(["a", "b", "gone"], ["a", "b", "c"])).toEqual(["a", "b"]);
  });

  it("keeps the seen order and never invents ids", () => {
    expect(pruneSeen(["b", "a"], ["a", "b", "c"])).toEqual(["b", "a"]);
  });

  it("returns an empty set when the inbox is empty", () => {
    expect(pruneSeen(["a", "b"], [])).toEqual([]);
  });
});
