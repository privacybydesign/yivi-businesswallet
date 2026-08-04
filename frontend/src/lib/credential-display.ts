// A held/received credential is indexed only by its verifiable-credential type
// (VCT) — often an issuer URL such as
// "https://veramo-issuer.openid4vc.staging.yivi.app/vct/nl-yivi-supplier". That is
// not a name a person should read in the wallet, and the backend does not yet
// surface the SD-JWT VC type-metadata display name, so this module maps known VCTs
// to friendly names and humanises the rest.

// KNOWN_CREDENTIAL_NAMES maps a VCT (or its final path segment) to a display name.
// Both the full VCT and its last "/"-segment are looked up, so a credential is
// recognised regardless of which issuer host prefixes it.
const KNOWN_CREDENTIAL_NAMES: Record<string, string> = {
  "nl-yivi-supplier": "Approved supplier",
  "nl.kvk.registration": "KVK registration",
  "eaa.received.stub": "Demo supplier credential",
};

// lastSegment strips a URL/URN prefix down to the identifying tail: the part after
// the final "/" (for issuer-URL VCTs), else the whole string.
function lastSegment(vct: string): string {
  const slash = vct.lastIndexOf("/");
  return slash === -1 ? vct : vct.slice(slash + 1);
}

// humanize turns a VCT tail like "nl-yivi-supplier" or "nl.kvk.registration" into
// spaced, sentence-cased text as a readable fallback for unknown credentials.
function humanize(segment: string): string {
  const words = segment
    .replace(/[-_.]+/g, " ")
    .trim()
    .replace(/\s+/g, " ");
  if (words === "") {
    return segment;
  }
  return words.charAt(0).toUpperCase() + words.slice(1);
}

// credentialDisplayName returns a human-readable name for a credential VCT: a
// curated name when the VCT (or its final segment) is known, otherwise a
// humanised fallback so the raw URL is never shown as a title.
export function credentialDisplayName(vct: string): string {
  const trimmed = vct.trim();
  if (trimmed === "") {
    return vct;
  }
  const segment = lastSegment(trimmed);
  return (
    KNOWN_CREDENTIAL_NAMES[trimmed] ??
    KNOWN_CREDENTIAL_NAMES[segment] ??
    humanize(segment)
  );
}
