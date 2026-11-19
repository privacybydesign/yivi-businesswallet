const WEB_PROTOCOLS = new Set(["http:", "https:"]);

// The host a "Return to ..." link names, for the site that sent a signee to
// the signing page. Null when returnUrl is absent or is not an absolute web
// address: the link is then not offered, so a javascript: or relative value
// can never become something the signee clicks.
export function returnLinkHost(returnUrl: string | undefined): string | null {
  if (returnUrl === undefined || !URL.canParse(returnUrl)) return null;
  const url = new URL(returnUrl);
  return WEB_PROTOCOLS.has(url.protocol) && url.hostname !== ""
    ? url.hostname
    : null;
}
