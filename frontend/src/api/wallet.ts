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

// enrollWallet registers a business wallet (organization) for a KVK number under
// the chosen slug, as the logged-in user. The backend consults the KVK (mocked in
// dev) and, if the caller is a listed representative, creates the organization
// (named from the register) and makes them an owner in one step.
export function enrollWallet(
  input: { kvkNumber: string; slug: string },
  signal?: AbortSignal,
): Promise<WalletEnrollment> {
  return request("/api/v1/wallet", {
    schema: walletEnrollmentSchema,
    method: "POST",
    body: input,
    signal,
  });
}

// Public self-service registration: no account required. The session-start URL is
// POSTed by the IdentityDisclosure component to authenticate the registrant via
// their wallet; registerWallet then exchanges the disclosure for a wallet + login.
export const REGISTER_SESSION_URL = "/api/v1/register/session";

export function registerWallet(
  input: { disclosureToken: string; kvkNumber: string; slug: string },
  signal?: AbortSignal,
): Promise<WalletEnrollment> {
  return request("/api/v1/register", {
    schema: walletEnrollmentSchema,
    method: "POST",
    body: input,
    signal,
  });
}
