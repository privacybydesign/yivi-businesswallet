const UNIVERSAL_LINK_PREFIX = "https://open.yivi.app/-/openid4vp?";

// yiviUniversalLink rewrites an OpenID4VP openid4vp:// deeplink into a Yivi
// universal link, which opens the wallet on this device and is scannable as a
// QR from another.
export function yiviUniversalLink(walletLink: string): string {
  const query = walletLink.split("?")[1] ?? "";
  return `${UNIVERSAL_LINK_PREFIX}${query}`;
}
