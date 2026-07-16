import { z } from "zod";
import { request } from "./http";

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
