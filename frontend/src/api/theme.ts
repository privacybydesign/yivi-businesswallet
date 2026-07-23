import { z } from "zod";
import { absoluteApiUrl, request } from "./http";

// Per-organization theming so the wallet reflects the tenant's identity. The
// colours are CSS hex strings ("" when unset); the frontend maps them onto its
// design tokens at runtime. logoUri is the API path serving the uploaded logo
// ("" when none), rendered in place of the default wordmark. Reads are open to
// any member; writes are org-admin only.
export const themeSchema = z.object({
  configured: z.boolean(),
  primaryColor: z.string(),
  accentColor: z.string(),
  textColor: z.string(),
  surfaceColor: z.string(),
  borderColor: z.string(),
  linkColor: z.string(),
  successColor: z.string(),
  warningColor: z.string(),
  errorColor: z.string(),
  logoUri: z.string(),
  updatedAt: z.string().optional(),
});

export type OrgTheme = z.infer<typeof themeSchema>;

// The palette colours a tenant can set, beyond the logo. Each is a CSS hex
// string ("" when unset); the frontend maps them onto its design tokens. Kept in
// step with the backend SettingsInput form fields.
export const THEME_COLOR_FIELDS = [
  "primaryColor",
  "accentColor",
  "textColor",
  "surfaceColor",
  "borderColor",
  "linkColor",
  "successColor",
  "warningColor",
  "errorColor",
] as const;

export type ThemeColorField = (typeof THEME_COLOR_FIELDS)[number];

// The logo change to apply when saving: a File uploads a new logo, "remove"
// clears the current one, and "keep" leaves it untouched (so colours can be
// changed on their own).
export type LogoChange = File | "keep" | "remove";

export type OrgThemeInput = Record<ThemeColorField, string> & {
  logo: LogoChange;
};

function base(slug: string): string {
  return `/api/v1/orgs/${encodeURIComponent(slug)}`;
}

// The backend returns the logo as a path on the API; make it absolute so an
// <img> loads it from the API origin even when the SPA is served elsewhere.
function withAbsoluteLogo(theme: OrgTheme): OrgTheme {
  return theme.logoUri
    ? { ...theme, logoUri: absoluteApiUrl(theme.logoUri) }
    : theme;
}

export async function getOrgTheme(
  slug: string,
  signal?: AbortSignal,
): Promise<OrgTheme> {
  const theme = await request(`${base(slug)}/theme`, {
    schema: themeSchema,
    signal,
  });
  return withAbsoluteLogo(theme);
}

export async function updateOrgTheme(
  slug: string,
  input: OrgThemeInput,
  signal?: AbortSignal,
): Promise<OrgTheme> {
  const form = new FormData();
  for (const field of THEME_COLOR_FIELDS) {
    form.append(field, input[field]);
  }
  if (input.logo instanceof File) {
    form.append("logo", input.logo);
  } else if (input.logo === "remove") {
    form.append("removeLogo", "true");
  }
  const theme = await request(`${base(slug)}/theme`, {
    schema: themeSchema,
    method: "PUT",
    body: form,
    signal,
  });
  return withAbsoluteLogo(theme);
}
