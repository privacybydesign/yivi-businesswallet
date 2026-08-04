import { useEffect } from "react";
import { useSearchParams } from "react-router";
import { applyCachedOrgTheme, clearOrgTheme } from "./theme";

// usePreAuthOrgTheme applies an org's cached palette on the slug-free pre-auth
// screens (login/register) when the URL carries a `?org=<slug>` hint, so a
// branded deep link keeps the tenant's colours before the user signs in. The
// theme endpoint is member-gated, so this is cache-only — it replays the palette
// written to localStorage on a prior authenticated visit on this device; a
// first-time visitor simply keeps the default Yivi look (graceful fallback). The
// logo endpoint is likewise member-gated, so only colours (not the logo) apply
// pre-auth. The theme is cleared on unmount so it never leaks into the
// authenticated shell, which re-applies from the live query.
export function usePreAuthOrgTheme(): void {
  const [params] = useSearchParams();
  const slug = params.get("org") ?? "";
  useEffect(() => {
    if (!slug) {
      return;
    }
    applyCachedOrgTheme(slug);
    return () => clearOrgTheme();
  }, [slug]);
}
