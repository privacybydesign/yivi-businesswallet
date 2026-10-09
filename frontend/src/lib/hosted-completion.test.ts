import { describe, expect, it } from "vitest";
import {
  completedMessage,
  completionRedirect,
  hostedLanguage,
  hostedTheme,
  originLines,
  postCompletion,
  type MessageTarget,
} from "./hosted-completion";

describe("hostedLanguage", () => {
  it("takes the session's language first", () => {
    expect(hostedLanguage("nl", ["en-US"])).toBe("nl");
  });

  it("falls back to the browser's first supported preference", () => {
    expect(hostedLanguage(undefined, ["de-DE", "nl-BE", "en"])).toBe("nl");
    expect(hostedLanguage("fr", ["en-GB"])).toBe("en");
  });

  it("keeps to the languages the flow offers", () => {
    expect(hostedLanguage("en", ["en-US"], ["nl"])).toBe("nl");
    expect(hostedLanguage(undefined, ["de", "nl-NL"], ["nl"])).toBe("nl");
    expect(hostedLanguage("nl", [], ["en", "nl"])).toBe("nl");
    expect(hostedLanguage(undefined, ["nl"], ["en"])).toBe("en");
  });

  it("ends at the default", () => {
    expect(hostedLanguage(undefined, ["de", "fr"])).toBe("en");
    expect(hostedLanguage(undefined, [])).toBe("en");
  });
});

describe("completionRedirect", () => {
  const done = completedMessage("ps_abc", "approved");

  it("adds the session and status, keeping the customer's query", () => {
    expect(completionRedirect("https://portal.example/done?ref=42", done)).toBe(
      "https://portal.example/done?ref=42&session=ps_abc&status=approved",
    );
  });

  it("overwrites a session or status the redirect already carried", () => {
    expect(
      completionRedirect(
        "https://portal.example/done?status=forged&session=x",
        done,
      ),
    ).toBe("https://portal.example/done?status=approved&session=ps_abc");
  });

  it("refuses what is not an http(s) URL", () => {
    expect(completionRedirect("javascript:alert(1)", done)).toBeUndefined();
    expect(completionRedirect("/relative", done)).toBeUndefined();
  });
});

describe("postCompletion", () => {
  it("posts only to the customer's origins, never to any origin", () => {
    const posted: [unknown, string][] = [];
    const parent: MessageTarget = {
      postMessage: (message, origin) => posted.push([message, origin]),
    };
    const done = completedMessage("ps_abc", "rejected");
    postCompletion(
      parent,
      ["https://portal.example", "http://localhost:3000"],
      done,
    );
    expect(posted).toEqual([
      [done, "https://portal.example"],
      [done, "http://localhost:3000"],
    ]);
    expect(posted.some(([, origin]) => origin === "*")).toBe(false);
  });

  it("posts nothing without origins", () => {
    const posted: string[] = [];
    postCompletion(
      { postMessage: (_, origin) => posted.push(origin) },
      [],
      completedMessage("ps_abc", "approved"),
    );
    expect(posted).toEqual([]);
  });
});

describe("originLines", () => {
  it("reads one origin per line, trimmed, without blank lines", () => {
    expect(
      originLines("  https://portal.example \n\n http://localhost:3000\n"),
    ).toEqual(["https://portal.example", "http://localhost:3000"]);
    expect(originLines("   ")).toEqual([]);
  });
});

describe("hostedTheme", () => {
  it("brands the page in the customer's colour only", () => {
    const theme = hostedTheme("#0a7c59");
    expect(theme?.primaryColor).toBe("#0a7c59");
    expect(theme?.accentColor).toBe("");
  });

  it("keeps the default look without a valid colour", () => {
    expect(hostedTheme("")).toBeNull();
    expect(hostedTheme("red")).toBeNull();
    expect(hostedTheme("#0a7c5")).toBeNull();
  });
});
