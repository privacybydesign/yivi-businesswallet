import { z } from "zod";
import { request } from "./http";

// One grant of authority in the organization's mandate register. `status` is
// derived by the backend on every read rather than stored, so a mandate that
// expired since the last fetch already reads as expired here.
export const mandateSchema = z.object({
  id: z.string(),
  organizationId: z.string(),
  type: z.string(),
  status: z.string(),
  grantorUserId: z.string().nullable(),
  grantorName: z.string().nullable(),
  granteeUserId: z.string(),
  granteeName: z.string(),
  scope: z.string(),
  scopeDepartmentId: z.string().nullable(),
  scopeDepartmentName: z.string().nullable(),
  parentMandateId: z.string().nullable(),
  validFrom: z.string(),
  validUntil: z.string().nullable(),
  revokedAt: z.string().nullable(),
  revokedByUserId: z.string().nullable(),
  revocationReason: z.string().nullable(),
  createdAt: z.string(),
});

export type Mandate = z.infer<typeof mandateSchema>;

const mandateListSchema = z.array(mandateSchema);

// The caller's own basis of authority in this organization. `mayGrant` mirrors
// the backend's RequireMandateAuthority gate; the other three explain it, so the
// screen can say why an action is missing instead of only hiding it.
export const mandateAuthoritySchema = z.object({
  mayGrant: z.boolean(),
  legalRepresentative: z.boolean(),
  fullMandate: z.boolean(),
  jointAuthority: z.boolean(),
});

export type MandateAuthority = z.infer<typeof mandateAuthoritySchema>;

export interface GrantMandateInput {
  type: string;
  granteeUserId: string;
  scope: string;
  departmentId?: string;
  validFrom?: string;
  validUntil?: string;
}

export interface RevokeMandateInput {
  // Absent revokes now. A future instant closes the validity window on that date
  // instead, so the mandate stays active until then and expires on its own.
  effectiveAt?: string;
  reason?: string;
}

// The register carries ended mandates too: a revoked one that vanished from the
// list would hide the revocation.
export function getMandates(
  slug: string,
  signal?: AbortSignal,
): Promise<Mandate[]> {
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}/mandates`, {
    schema: mandateListSchema,
    signal,
  });
}

export function getMandateAuthority(
  slug: string,
  signal?: AbortSignal,
): Promise<MandateAuthority> {
  return request(
    `/api/v1/orgs/${encodeURIComponent(slug)}/mandates/authority`,
    {
      schema: mandateAuthoritySchema,
      signal,
    },
  );
}

export function grantMandate(
  slug: string,
  input: GrantMandateInput,
  signal?: AbortSignal,
): Promise<Mandate> {
  return request(`/api/v1/orgs/${encodeURIComponent(slug)}/mandates`, {
    schema: mandateSchema,
    method: "POST",
    body: input,
    signal,
  });
}

// Revoking answers with every mandate the revocation reached: the target plus the
// descendants the cascade took with it.
export function revokeMandate(
  slug: string,
  mandateId: string,
  input: RevokeMandateInput,
  signal?: AbortSignal,
): Promise<Mandate[]> {
  return request(
    `/api/v1/orgs/${encodeURIComponent(slug)}/mandates/${encodeURIComponent(mandateId)}/revoke`,
    {
      schema: mandateListSchema,
      method: "POST",
      body: input,
      signal,
    },
  );
}
