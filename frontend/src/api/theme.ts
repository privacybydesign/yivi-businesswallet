import { z } from "zod";
import { request } from "./http";

// Per-organization theming so the wallet reflects the tenant's identity. The
// colours are CSS hex strings ("" when unset); the frontend maps them onto its
// design tokens at runtime and renders logoUri in place of the default wordmark.
// Reads are open to any member; writes are org-admin only.
export const themeSchema = z.object({
  configured: z.boolean(),
  primaryColor: z.string(),
  accentColor: z.string(),
  logoUri: z.string(),
  updatedAt: z.string().optional(),
});

export type OrgTheme = z.infer<typeof themeSchema>;

export interface OrgThemeInput {
  primaryColor: string;
  accentColor: string;
  logoUri: string;
}

function base(slug: string): string {
  return `/api/v1/orgs/${encodeURIComponent(slug)}`;
}

export function getOrgTheme(
  slug: string,
  signal?: AbortSignal,
): Promise<OrgTheme> {
  return request(`${base(slug)}/theme`, { schema: themeSchema, signal });
}

export function updateOrgTheme(
  slug: string,
  input: OrgThemeInput,
  signal?: AbortSignal,
): Promise<OrgTheme> {
  return request(`${base(slug)}/theme`, {
    schema: themeSchema,
    method: "PUT",
    body: input,
    signal,
  });
}
