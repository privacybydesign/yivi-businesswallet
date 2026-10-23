import { describe, expect, it } from "vitest";
import {
  canCloseDecline,
  checkEndReason,
  completedMessage,
  completionRedirect,
  hostedHandBack,
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

  it("adds the session and status, keeping the redirect's query", () => {
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

describe("hostedHandBack", () => {
  const settled = {
    sessionId: "ps_abc",
    status: "approved",
    handedBack: false,
    dataRequest: false,
    framed: false,
    redirectUrl: undefined,
  };
  const redirectUrl = "https://portal.example/done";

  it("hands back once", () => {
    expect(hostedHandBack(settled)).toEqual({
      message: completedMessage("ps_abc", "approved"),
      post: false,
      redirectTo: undefined,
    });
    expect(hostedHandBack({ ...settled, handedBack: true })).toBeUndefined();
  });

  it("posts the outcome only when framed", () => {
    expect(hostedHandBack({ ...settled, framed: true })?.post).toBe(true);
  });

  it("redirects with the outcome when the session has a redirect", () => {
    expect(hostedHandBack({ ...settled, redirectUrl })?.redirectTo).toBe(
      "https://portal.example/done?session=ps_abc&status=approved",
    );
  });

  it("posts and redirects when framed with a redirect", () => {
    const handBack = hostedHandBack({ ...settled, framed: true, redirectUrl });
    expect(handBack?.post).toBe(true);
    expect(handBack?.redirectTo).toBeDefined();
  });

  // "This check has ended" says what to do next: a redirect would replace
  // it at once, as if the check were done.
  it("keeps a check that ran out on its own page, telling an embedder", () => {
    const handBack = hostedHandBack({
      ...settled,
      status: "expired",
      framed: true,
      redirectUrl,
    });
    expect(handBack?.redirectTo).toBeUndefined();
    expect(handBack?.post).toBe(true);
    expect(handBack?.message.status).toBe("expired");
  });

  it("stays on the page for an unusable redirect", () => {
    expect(
      hostedHandBack({ ...settled, redirectUrl: "javascript:alert(1)" })
        ?.redirectTo,
    ).toBeUndefined();
  });

  it("skips a data request in review, which is not answered yet", () => {
    expect(
      hostedHandBack({
        ...settled,
        status: "needs_review",
        dataRequest: true,
      }),
    ).toBeUndefined();
    expect(
      hostedHandBack({ ...settled, status: "needs_review" })?.message.status,
    ).toBe("needs_review");
  });

  it("hands a settled data request back without redirecting away", () => {
    const handBack = hostedHandBack({
      ...settled,
      dataRequest: true,
      framed: true,
      redirectUrl,
    });
    expect(handBack?.post).toBe(true);
    expect(handBack?.redirectTo).toBeUndefined();
  });
});

describe("postCompletion", () => {
  it("posts only to the org's origins, never to any origin", () => {
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
  it("brands the page in the org's colour only", () => {
    const theme = hostedTheme(undefined, "#0a7c59");
    expect(theme.primaryColor).toBe("#0a7c59");
    expect(theme.accentColor).toBe("");
  });

  it("keeps the white-label design's colour without a valid one", () => {
    for (const invalid of ["", "red", "#0a7c5"]) {
      const theme = hostedTheme(undefined, invalid);
      expect(theme.configured).toBe(true);
      expect(theme.primaryColor).toBe("");
    }
  });

  it("takes the session's theme, header and accent included", () => {
    const theme = hostedTheme(
      {
        primaryColor: "#036bd2",
        secondaryColor: "#5412c7",
        textColor: "#101e1e",
        surfaceColor: "#fbfbfc",
        borderColor: "#eaebee",
        linkColor: "#036bd2",
        successColor: "#2a9066",
        warningColor: "",
        errorColor: "",
        headerColor: "#101e1e",
        fontFamily: "",
      },
      "",
    );
    expect(theme.primaryColor).toBe("#036bd2");
    expect(theme.accentColor).toBe("#5412c7");
    expect(theme.topbarColor).toBe("#101e1e");
    expect(theme.surfaceColor).toBe("#fbfbfc");
  });

  it("puts the branding's own colour, a flow's, over the session's theme", () => {
    const sessionTheme = {
      primaryColor: "#036bd2",
      secondaryColor: "#5412c7",
      textColor: "",
      surfaceColor: "",
      borderColor: "",
      linkColor: "",
      successColor: "",
      warningColor: "",
      errorColor: "",
      headerColor: "#101e1e",
      fontFamily: "",
    };
    const theme = hostedTheme(sessionTheme, "#22337a");
    expect(theme.primaryColor).toBe("#22337a");
    expect(theme.accentColor).toBe("#5412c7");
    expect(theme.topbarColor).toBe("#101e1e");
    expect(hostedTheme(sessionTheme, "red").primaryColor).toBe("#036bd2");
  });
});

describe("canCloseDecline", () => {
  it("keeps the dialog open while the decline is in flight", () => {
    expect(canCloseDecline("pending")).toBe(false);
  });

  it("closes it before the decline and after it failed", () => {
    expect(canCloseDecline("idle")).toBe(true);
    expect(canCloseDecline("error")).toBe(true);
  });
});

describe("checkEndReason", () => {
  it("names the lifetime the started session ran with", () => {
    expect(checkEndReason({ started: true, sessionTtlSeconds: 120 })).toEqual({
      kind: "timedOut",
      minutes: 2,
    });
  });

  it("says in time for a session started before its lifetime was kept", () => {
    expect(checkEndReason({ started: true })).toEqual({
      kind: "timedOut",
      minutes: undefined,
    });
  });

  it("says a link that ran out was never started", () => {
    expect(checkEndReason({ started: false })).toEqual({ kind: "notStarted" });
    expect(checkEndReason({})).toEqual({ kind: "notStarted" });
  });
});
