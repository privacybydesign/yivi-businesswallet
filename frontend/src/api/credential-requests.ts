import { z } from "zod";
import { request } from "./http";

// Org-to-org credential requests over QERDS (#271). Outbound: this
// organization, as relying party, asks another organization's business wallet
// for credentials and receives the verified answer. Incoming: requests another
// organization sent here, waiting on an admin to approve (present) or decline.
// See .ai/features/oid4vp-over-qerds.md.

export const OUTBOUND_STATUSES = [
  "sent",
  "completed",
  "failed",
  "expired",
] as const;
export type OutboundStatus = (typeof OUTBOUND_STATUSES)[number];

const credentialRequestSchema = z.object({
  id: z.string(),
  vct: z.string(),
  claims: z.array(z.string()),
});

const disclosedCredentialSchema = z.object({
  queryId: z.string(),
  vct: z.string(),
  issuer: z.string(),
  claims: z.record(z.string(), z.unknown()),
});
export type DisclosedCredential = z.infer<typeof disclosedCredentialSchema>;

export const outboundRequestSchema = z.object({
  id: z.string(),
  sender: z.string(),
  recipient: z.string(),
  status: z.enum(OUTBOUND_STATUSES),
  failureReason: z.string().optional(),
  fetched: z.boolean(),
  credentials: z.array(credentialRequestSchema),
  disclosed: z.array(disclosedCredentialSchema),
  createdAt: z.string(),
  expiresAt: z.string(),
  respondedAt: z.string().optional(),
});
export type OutboundRequest = z.infer<typeof outboundRequestSchema>;

export interface SendCredentialRequest {
  from?: string;
  recipient: string;
  credentials: { vct: string; claims: string[] }[];
}

function outboundPath(slug: string): string {
  return `/api/v1/orgs/${encodeURIComponent(slug)}/openid4vp/outbound`;
}

export function getOutboundRequests(
  slug: string,
  signal?: AbortSignal,
): Promise<OutboundRequest[]> {
  return request(outboundPath(slug), {
    schema: z.array(outboundRequestSchema),
    signal,
  });
}

export function sendCredentialRequest(
  slug: string,
  body: SendCredentialRequest,
  signal?: AbortSignal,
): Promise<OutboundRequest> {
  return request(outboundPath(slug), {
    schema: outboundRequestSchema,
    method: "POST",
    body,
    signal,
  });
}

// The trust scheme's catalogue: credential types issuers on this deployment
// designed, to pick a request from.
export const credentialTypeSchema = z.object({
  vct: z.string(),
  name: z.string(),
  issuer: z.string(),
  attributes: z.array(z.object({ key: z.string(), label: z.string() })),
});
export type CredentialType = z.infer<typeof credentialTypeSchema>;

export function getCredentialTypes(
  slug: string,
  signal?: AbortSignal,
): Promise<CredentialType[]> {
  return request(
    `/api/v1/orgs/${encodeURIComponent(slug)}/openid4vp/credential-types`,
    { schema: z.array(credentialTypeSchema), signal },
  );
}

export const incomingRequestSchema = z.object({
  id: z.string(),
  verifier: z.string(),
  expiresAt: z.string(),
  // What an approval would share: per credential query, the acceptable types
  // and the claim paths.
  credentials: z.array(
    z.object({ vcts: z.array(z.string()), claims: z.array(z.string()) }),
  ),
});
export type IncomingRequest = z.infer<typeof incomingRequestSchema>;

function incomingPath(slug: string): string {
  return `/api/v1/orgs/${encodeURIComponent(slug)}/openid4vp/requests`;
}

export function getIncomingRequests(
  slug: string,
  signal?: AbortSignal,
): Promise<IncomingRequest[]> {
  return request(incomingPath(slug), {
    schema: z.array(incomingRequestSchema),
    signal,
  });
}

const approveSchema = z.object({
  status: z.string(),
  redirectUri: z.string().optional(),
});

export function approveIncomingRequest(
  slug: string,
  id: string,
  signal?: AbortSignal,
): Promise<void> {
  return request(`${incomingPath(slug)}/${encodeURIComponent(id)}/approve`, {
    schema: approveSchema,
    method: "POST",
    signal,
  }).then(() => undefined);
}

export function declineIncomingRequest(
  slug: string,
  id: string,
  signal?: AbortSignal,
): Promise<void> {
  return request(`${incomingPath(slug)}/${encodeURIComponent(id)}/decline`, {
    schema: z.void(),
    method: "POST",
    signal,
  });
}
