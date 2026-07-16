import { z } from "zod";
import { request } from "./http";

// A provisioned wallet instance for an organization (GET /orgs/{slug}/wallet).
export const walletInstanceSchema = z.object({
  id: z.string(),
  status: z.string(),
  kvkNumber: z.string(),
  digitalAddress: z.string(),
  organizationId: z.string().optional(),
  organizationSlug: z.string().optional(),
  legalName: z.string().optional(),
  euid: z.string().optional(),
  rejectReason: z.string().optional(),
  bootstrappedAt: z.string().optional(),
  createdAt: z.string(),
  updatedAt: z.string(),
});
export type WalletInstance = z.infer<typeof walletInstanceSchema>;

export function getOrgWallet(
  slug: string,
  signal?: AbortSignal,
): Promise<WalletInstance> {
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}/wallet`, {
    schema: walletInstanceSchema,
    signal,
  });
}

export const walletEnrollmentSchema = z.object({
  status: z.string(),
  organizationSlug: z.string(),
  legalName: z.string(),
  kvkNumber: z.string(),
  representationKind: z.string().optional(),
  representationAuthority: z.string().optional(),
});
export type WalletEnrollment = z.infer<typeof walletEnrollmentSchema>;

// enrollWallet registers a business wallet for a KVK number as the logged-in user.
// The backend consults the KVK (mocked in dev) and, if the caller is a listed
// representative, creates the organization and makes them an owner in one step.
export function enrollWallet(
  kvkNumber: string,
  signal?: AbortSignal,
): Promise<WalletEnrollment> {
  return request("/api/v1/wallet", {
    schema: walletEnrollmentSchema,
    method: "POST",
    body: { kvkNumber },
    signal,
  });
}

// Public self-service registration: no account required. The session-start URL is
// POSTed by the IdentityDisclosure component to authenticate the registrant via
// their wallet; registerWallet then exchanges the disclosure for a wallet + login.
export const REGISTER_SESSION_URL = "/api/v1/register/session";

export function registerWallet(
  disclosureToken: string,
  kvkNumber: string,
  signal?: AbortSignal,
): Promise<WalletEnrollment> {
  return request("/api/v1/register", {
    schema: walletEnrollmentSchema,
    method: "POST",
    body: { disclosureToken, kvkNumber },
    signal,
  });
}
