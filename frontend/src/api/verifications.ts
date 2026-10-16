import { z } from "zod";
import { request } from "./http";

// Verifications: the requester side of a business-wallet-to-business-wallet
// disclosure. An admin defines templates (which credential type and claims to
// ask a holder for); a member starts a check from one, shows the resulting
// link as a QR, and polls until the holder has presented. The backend grades
// the disclosure against the organization's own issuance ledger. See
// .ai/features/verification-templates.md.

export const VERIFICATION_STATUSES = [
  "pending",
  "completed",
  "expired",
] as const;
export type VerificationStatus = (typeof VERIFICATION_STATUSES)[number];

// The checks a completed verification reports, in the order the result card
// lists them. Mirrors the Check* constants in backend/internal/verification.
export const VERIFICATION_CHECKS = [
  "verified",
  "not_expired",
  "issued_here",
  "not_revoked",
] as const;
export type VerificationCheckName = (typeof VERIFICATION_CHECKS)[number];

export const verificationTemplateSchema = z.object({
  id: z.string(),
  organizationId: z.string(),
  name: z.string(),
  vct: z.string(),
  claims: z.array(z.string()),
  purpose: z.string(),
  createdAt: z.string(),
  updatedAt: z.string(),
});
export type VerificationTemplate = z.infer<typeof verificationTemplateSchema>;

export interface VerificationTemplateInput {
  name: string;
  vct: string;
  claims: string[];
  purpose: string;
}

export const verificationCheckSchema = z.object({
  name: z.string(),
  passed: z.boolean(),
  detail: z.string().optional(),
});
export type VerificationCheck = z.infer<typeof verificationCheckSchema>;

export const verificationSchema = z.object({
  id: z.string(),
  organizationId: z.string(),
  templateId: z.string().optional(),
  templateName: z.string(),
  vct: z.string(),
  status: z.enum(VERIFICATION_STATUSES),
  startedByUserId: z.string().optional(),
  claims: z.record(z.string(), z.string()).optional(),
  checks: z.array(verificationCheckSchema).optional(),
  valid: z.boolean().optional(),
  completedAt: z.string().optional(),
  expiresAt: z.string(),
  createdAt: z.string(),
  // Only while pending: the openid4vp:// deeplink and the https form of the
  // same invocation into this deployment's /openid4vp entry.
  walletLink: z.string().optional(),
  browserLink: z.string().optional(),
});
export type Verification = z.infer<typeof verificationSchema>;

function base(slug: string): string {
  return `/api/v1/orgs/${encodeURIComponent(slug)}/verifications`;
}

export function getVerificationTemplates(
  slug: string,
  signal?: AbortSignal,
): Promise<VerificationTemplate[]> {
  return request(`${base(slug)}/templates`, {
    schema: z.array(verificationTemplateSchema),
    signal,
  });
}

export function createVerificationTemplate(
  slug: string,
  input: VerificationTemplateInput,
  signal?: AbortSignal,
): Promise<VerificationTemplate> {
  return request(`${base(slug)}/templates`, {
    schema: verificationTemplateSchema,
    method: "POST",
    body: input,
    signal,
  });
}

export function deleteVerificationTemplate(
  slug: string,
  templateId: string,
  signal?: AbortSignal,
): Promise<void> {
  return request(`${base(slug)}/templates/${encodeURIComponent(templateId)}`, {
    schema: z.void(),
    method: "DELETE",
    signal,
  });
}

export function getVerifications(
  slug: string,
  signal?: AbortSignal,
): Promise<Verification[]> {
  return request(base(slug), {
    schema: z.array(verificationSchema),
    signal,
  });
}

export function startVerification(
  slug: string,
  templateId: string,
  signal?: AbortSignal,
): Promise<Verification> {
  return request(base(slug), {
    schema: verificationSchema,
    method: "POST",
    body: { templateId },
    signal,
  });
}

export function getVerification(
  slug: string,
  id: string,
  signal?: AbortSignal,
): Promise<Verification> {
  return request(`${base(slug)}/${encodeURIComponent(id)}`, {
    schema: verificationSchema,
    signal,
  });
}
