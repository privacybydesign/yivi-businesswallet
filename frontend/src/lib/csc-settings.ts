import { CSC_PROVIDER_KIND_BUILT_IN } from "../api/csc";
import type { CscSettingsInput } from "../api/csc";

// What the settings form holds for the signing provider.
export interface CscSettingsForm {
  enabled: boolean;
  providerKind: string;
  baseUrl: string;
  clientId: string;
  clientSecret: string;
}

// The wallet's own signing service has no endpoint or OAuth client to enter.
export function isBuiltInCscKind(providerKind: string): boolean {
  return providerKind === CSC_PROVIDER_KIND_BUILT_IN;
}

// A remote provider with no endpoint cannot be reached, so enabling one waits
// for a URL; the built-in service needs none.
export function canEnableCsc(providerKind: string, baseUrl: string): boolean {
  return isBuiltInCscKind(providerKind) || baseUrl.trim() !== "";
}

// The base URL the form starts with. The built-in service reads back as this
// deployment's own URL, which is not a value to carry over to a remote kind.
export function initialCscBaseUrl(
  providerKind: string,
  baseUrl: string,
): string {
  return isBuiltInCscKind(providerKind) ? "" : baseUrl;
}

// What saving the form sends. The built-in service stores no endpoint, client
// or secret, so none is sent for it.
export function cscSettingsInput(form: CscSettingsForm): CscSettingsInput {
  if (isBuiltInCscKind(form.providerKind)) {
    return {
      enabled: form.enabled,
      providerKind: form.providerKind,
      baseUrl: "",
      clientId: "",
    };
  }
  const typedSecret = form.clientSecret.trim();
  return {
    enabled: canEnableCsc(form.providerKind, form.baseUrl) && form.enabled,
    providerKind: form.providerKind,
    baseUrl: form.baseUrl.trim(),
    clientId: form.clientId.trim(),
    // Blank keeps the stored secret; a typed value replaces it.
    clientSecret: typedSecret === "" ? null : typedSecret,
  };
}
