import { z } from "zod";
import { request } from "./http";

// Identity proofing through the identity-proofing-service (IPS): one IPS tenant
// per organization (provisioned on its first use), flows defined by org admins
// who choose which of them members may use, and e-mailed requests any member
// can send. Only a request's outcome and assurance level come back, never the
// document data the proofing session saw.
//
// Step, check and status values are plain strings rather than zod enums: IPS is
// still adding steps and checks, and an unknown value must not break the page.

const assuranceTierSchema = z.object({
  level: z.string(),
  minPercent: z.number(),
});

export type ProofingAssuranceTier = z.infer<typeof assuranceTierSchema>;

// A flow version as the proofing service stores it. The id is stable across
// versions; exactly one version is active, the one new requests run.
export const proofingFlowSchema = z.object({
  id: z.string(),
  version: z.number(),
  active: z.boolean(),
  name: z.string(),
  steps: z.array(z.string()),
  requestedAttributes: z.array(z.string()).optional(),
  selfieLocation: z.string().optional(),
  acceptedDocumentTypes: z.array(z.string()).optional(),
  acceptedIssuingCountries: z.array(z.string()).optional(),
  requiredChecks: z.array(z.string()).optional(),
  checkThresholds: z.record(z.string(), z.number()).optional(),
  requiredAssuranceLevel: z.string().optional(),
  bsnPolicy: z.string().optional(),
  blurFace: z.boolean().optional(),
  blurBsn: z.boolean().optional(),
  retentionOverrideSeconds: z.number().optional(),
  assuranceTiers: z.array(assuranceTierSchema).optional(),
  legalBasis: z.string().optional(),
  processingPurpose: z.string().optional(),
  createdAt: z.string(),
  // False for a flow a recipient cannot finish with only the vcmrtd app.
  completable: z.boolean(),
  // The admin made it available to members; default is the one the request
  // form preselects. A member's list only holds allowed flows.
  allowed: z.boolean(),
  default: z.boolean(),
});

export type ProofingFlow = z.infer<typeof proofingFlowSchema>;

export const proofingMemberSchema = z.object({
  userId: z.string(),
  name: z.string(),
  email: z.string(),
  role: z.string(),
  memberType: z.string(),
  externalOrganisation: z.string().optional(),
});

export type ProofingMember = z.infer<typeof proofingMemberSchema>;

export const proofingRequestSchema = z.object({
  id: z.string(),
  requestedByName: z.string(),
  subjectUserId: z.string().optional(),
  subjectName: z.string(),
  subjectEmail: z.string(),
  flowId: z.string(),
  flowName: z.string(),
  flowVersion: z.number().optional(),
  status: z.string(),
  assuranceLevel: z.string().optional(),
  eidasLevel: z.string().optional(),
  errorCode: z.string().optional(),
  linkExpiresAt: z.string(),
  createdAt: z.string(),
  completedAt: z.string().optional(),
});

export type ProofingRequest = z.infer<typeof proofingRequestSchema>;

// mailSent is false when the org's mail could not be sent: the request stands,
// but the member never got its short-lived link.
export const proofingSentSchema = proofingRequestSchema.extend({
  mailSent: z.boolean(),
});

export type ProofingSent = z.infer<typeof proofingSentSchema>;

export const proofingLinkSchema = z.object({
  organizationName: z.string(),
  subjectName: z.string(),
  flowName: z.string(),
  status: z.string(),
  linkExpiresAt: z.string(),
});

export type ProofingLink = z.infer<typeof proofingLinkSchema>;

export const proofingStartSchema = z.object({
  status: z.string(),
  deepLink: z.string().optional(),
  claimExpiresAt: z.string().optional(),
});

export type ProofingStart = z.infer<typeof proofingStartSchema>;

// The body of a new flow and of a new version of one: every setting the
// proofing service takes. Omitted optional fields inherit the tenant's policy.
export interface ProofingFlowSpec {
  name: string;
  steps: string[];
  requestedAttributes?: string[];
  acceptedDocumentTypes?: string[];
  acceptedIssuingCountries?: string[];
  requiredChecks?: string[];
  checkThresholds?: Record<string, number>;
  requiredAssuranceLevel?: string;
  bsnPolicy?: string;
  blurFace?: boolean;
  blurBsn?: boolean;
  retentionOverrideSeconds?: number;
  assuranceTiers?: ProofingAssuranceTier[];
  legalBasis?: string;
  processingPurpose?: string;
}

export interface ProofingFlowSelection {
  flowIds: string[];
  defaultFlowId: string;
}

export interface ProofingRequestInput {
  userId: string;
  flowId: string;
}

function base(slug: string): string {
  return `/api/v1/orgs/${encodeURIComponent(slug)}/identity-proofing`;
}

function linkBase(token: string): string {
  return `/api/v1/identity-proofing/${encodeURIComponent(token)}`;
}

export function getProofingFlows(
  slug: string,
  signal?: AbortSignal,
): Promise<ProofingFlow[]> {
  return request(`${base(slug)}/flows`, {
    schema: z.array(proofingFlowSchema),
    signal,
  });
}

export function createProofingFlow(
  slug: string,
  spec: ProofingFlowSpec,
  signal?: AbortSignal,
): Promise<ProofingFlow> {
  return request(`${base(slug)}/flows`, {
    schema: proofingFlowSchema,
    method: "POST",
    body: spec,
    signal,
  });
}

function flowBase(slug: string, flowId: string): string {
  return `${base(slug)}/flows/${encodeURIComponent(flowId)}`;
}

export function getProofingFlowVersions(
  slug: string,
  flowId: string,
  signal?: AbortSignal,
): Promise<ProofingFlow[]> {
  return request(`${flowBase(slug, flowId)}/versions`, {
    schema: z.array(proofingFlowSchema),
    signal,
  });
}

// Editing a flow saves the next version, which becomes the active one.
export function createProofingFlowVersion(
  slug: string,
  flowId: string,
  spec: ProofingFlowSpec,
  signal?: AbortSignal,
): Promise<ProofingFlow> {
  return request(`${flowBase(slug, flowId)}/versions`, {
    schema: proofingFlowSchema,
    method: "POST",
    body: spec,
    signal,
  });
}

export function activateProofingFlowVersion(
  slug: string,
  flowId: string,
  version: number,
  signal?: AbortSignal,
): Promise<ProofingFlow> {
  return request(`${flowBase(slug, flowId)}/versions/${version}/activate`, {
    schema: proofingFlowSchema,
    method: "POST",
    body: {},
    signal,
  });
}

export function getProofingMembers(
  slug: string,
  signal?: AbortSignal,
): Promise<ProofingMember[]> {
  return request(`${base(slug)}/members`, {
    schema: z.array(proofingMemberSchema),
    signal,
  });
}

export function setProofingFlowSelection(
  slug: string,
  selection: ProofingFlowSelection,
  signal?: AbortSignal,
): Promise<ProofingFlow[]> {
  return request(`${base(slug)}/flow-selection`, {
    schema: z.array(proofingFlowSchema),
    method: "PUT",
    body: selection,
    signal,
  });
}

export function getProofingRequests(
  slug: string,
  signal?: AbortSignal,
): Promise<ProofingRequest[]> {
  return request(`${base(slug)}/requests`, {
    schema: z.array(proofingRequestSchema),
    signal,
  });
}

export function createProofingRequest(
  slug: string,
  input: ProofingRequestInput,
  signal?: AbortSignal,
): Promise<ProofingSent> {
  return request(`${base(slug)}/requests`, {
    schema: proofingSentSchema,
    method: "POST",
    body: input,
    signal,
  });
}

export function getProofingLink(
  token: string,
  signal?: AbortSignal,
): Promise<ProofingLink> {
  return request(linkBase(token), { schema: proofingLinkSchema, signal });
}

export function startProofing(
  token: string,
  signal?: AbortSignal,
): Promise<ProofingStart> {
  return request(`${linkBase(token)}/session`, {
    schema: proofingStartSchema,
    method: "POST",
    body: {},
    signal,
  });
}
