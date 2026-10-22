import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import i18n from "../i18n";
import {
  MAX_LISTED_REQUESTS,
  listIsCapped,
  listedCount,
} from "./listed-requests";

const t = i18n.getFixedT("en");

describe("listed counts", () => {
  it("mirrors the backend's cap on a list read", () => {
    const source = readFileSync(
      fileURLToPath(
        new URL(
          "../../../backend/internal/proofing/proofing.go",
          import.meta.url,
        ),
      ),
      "utf8",
    );
    const backend = /const maxListedRequests = (\d+)/.exec(source)?.[1];
    expect(Number(backend)).toBe(MAX_LISTED_REQUESTS);
  });

  it("knows a full list may have left some out", () => {
    expect(listIsCapped(new Array<number>(MAX_LISTED_REQUESTS - 1))).toBe(
      false,
    );
    expect(listIsCapped(new Array<number>(MAX_LISTED_REQUESTS))).toBe(true);
  });

  it.each(["en", "nl"])("shows a capped count as at least it in %s", (lng) => {
    const translate = i18n.getFixedT(lng);
    expect(listedCount(MAX_LISTED_REQUESTS, true, translate)).toBe("200+");
    expect(listedCount(12, false, translate)).toBe("12");
  });

  it("never shows a capped count as the whole number", () => {
    expect(listedCount(150, true, t)).toBe("150+");
  });
});
