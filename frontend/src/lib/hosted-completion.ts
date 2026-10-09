// How a hosted proofing page picks its language and hands its subject back
// once the session settles. Pure, so the rules are unit-tested.

import type { OrgTheme } from "../api/theme";
import {
  DEFAULT_LANGUAGE,
  isSupportedLanguage,
  resolveInitialLanguage,
  type Language,
} from "../i18n/language";

// The page's language, among those the flow offers (none listed: every
// supported one): the session's, then the browser's first offered preference,
// then the default, else the first offered. A choice stored for the wallet
// itself does not count: the subject is not a wallet user.
export function hostedLanguage(
  session: string | undefined,
  preferred: readonly string[],
  offered: readonly string[] = [],
): Language {
  const offers = (value: Language): boolean =>
    offered.length === 0 || offered.includes(value);
  if (isSupportedLanguage(session) && offers(session)) {
    return session;
  }
  for (const preference of preferred) {
    const base = resolveInitialLanguage(null, [preference]);
    if (preference.toLowerCase().split("-")[0] === base && offers(base)) {
      return base;
    }
  }
  if (offers(DEFAULT_LANGUAGE)) {
    return DEFAULT_LANGUAGE;
  }
  return offered.find(isSupportedLanguage) ?? DEFAULT_LANGUAGE;
}

// The message an embedding page receives once the session settles.
export interface HostedCompletedMessage {
  type: "proofing.completed";
  session: string;
  status: string;
}

export function completedMessage(
  session: string,
  status: string,
): HostedCompletedMessage {
  return { type: "proofing.completed", session, status };
}

// The redirect with the session and its status added, keeping the customer's
// own query. The backend accepted the redirect's origin when the session was
// created; an unparsable one is undefined, so the page shows its own done screen.
export function completionRedirect(
  redirectUrl: string,
  message: HostedCompletedMessage,
): string | undefined {
  let url: URL;
  try {
    url = new URL(redirectUrl);
  } catch {
    return undefined;
  }
  if (url.protocol !== "https:" && url.protocol !== "http:") {
    return undefined;
  }
  url.searchParams.set("session", message.session);
  url.searchParams.set("status", message.status);
  return url.toString();
}

// What a page posts to: its parent window when it is framed.
export interface MessageTarget {
  postMessage(message: unknown, targetOrigin: string): void;
}

// Posts the completion to each of the customer's origins, never "*": the
// browser delivers it only when the embedding page is on that origin, so no
// other page learns the outcome.
export function postCompletion(
  parent: MessageTarget,
  origins: readonly string[],
  message: HostedCompletedMessage,
): void {
  for (const origin of origins) {
    parent.postMessage(message, origin);
  }
}

// The origins an admin typed, one per line: trimmed, blank lines dropped. The
// backend checks each one and normalises it.
export function originLines(text: string): string[] {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line !== "");
}

const HEX_COLOR = /^#[0-9a-fA-F]{6}$/;

// The hosted page's theme: the customer's colour as the brand colour, the rest
// the default look; null (the default look) without a valid colour. Applied
// through applyOrgTheme, which keeps text on it readable.
export function hostedTheme(primaryColor: string): OrgTheme | null {
  if (!HEX_COLOR.test(primaryColor)) {
    return null;
  }
  return {
    configured: true,
    primaryColor,
    accentColor: "",
    textColor: "",
    surfaceColor: "",
    borderColor: "",
    linkColor: "",
    successColor: "",
    warningColor: "",
    errorColor: "",
    sidebarColor: "",
    topbarColor: "",
    fontFamily: "",
    logoUri: "",
  };
}
