import { z } from "zod";
import { request } from "./http";

// Identity proofing through the identity-proofing-service (IPS): one IPS tenant
// per organization (provisioned on its first use), flows defined by org admins
// who choose which of them members may use, the org's customers (no login of
// their own) with the flows assigned to each, and e-mailed requests any member
// can send, to a member or to a customer's subject. Only a request's outcome and
// assurance level come back, plus, for a customer's approved subject, the name
// on their document for a limited time; never other document data.
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

export const proofingRequestSchema = z.object({
  id: z.string(),
  requestedByName: z.string(),
  subjectUserId: z.string().optional(),
  customerId: z.string().optional(),
  customerName: z.string().optional(),
  subjectName: z.string(),
  subjectEmail: z.string(),
  proofedName: z.string().optional(),
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
// but the recipient never got its link.
export const proofingSentSchema = proofingRequestSchema.extend({
  mailSent: z.boolean(),
});

export type ProofingSent = z.infer<typeof proofingSentSchema>;

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

export type ProofingRequestInput =
  | { userId: string; flowId: string }
  | { customerId: string; email: string; name: string; flowId: string };

export const proofingCustomerSchema = z.object({
  id: z.string(),
  name: z.string(),
  flowIds: z.array(z.string()),
  defaultFlowId: z.string().optional(),
  createdAt: z.string(),
  updatedAt: z.string(),
});

export type ProofingCustomer = z.infer<typeof proofingCustomerSchema>;

export const proofingCustomerFlowSchema = proofingFlowSchema
  .omit({ allowed: true, default: true })
  .extend({ assigned: z.boolean(), default: z.boolean() });

export type ProofingCustomerFlow = z.infer<typeof proofingCustomerFlowSchema>;

function base(slug: string): string {
  return `/api/v1/orgs/${encodeURIComponent(slug)}/identity-proofing`;
}

// The org's customers live beside proofing, not under it.
function customersBase(slug: string): string {
  return `/api/v1/orgs/${encodeURIComponent(slug)}/customers`;
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

// customerId narrows the list to the requests sent for that customer.
export function getProofingRequests(
  slug: string,
  customerId?: string,
  signal?: AbortSignal,
): Promise<ProofingRequest[]> {
  const query = customerId
    ? `?customerId=${encodeURIComponent(customerId)}`
    : "";
  return request(`${base(slug)}/requests${query}`, {
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

function customerBase(slug: string, customerId: string): string {
  return `${customersBase(slug)}/${encodeURIComponent(customerId)}`;
}

export function getProofingCustomers(
  slug: string,
  signal?: AbortSignal,
): Promise<ProofingCustomer[]> {
  return request(customersBase(slug), {
    schema: z.array(proofingCustomerSchema),
    signal,
  });
}

export function createProofingCustomer(
  slug: string,
  name: string,
  signal?: AbortSignal,
): Promise<ProofingCustomer> {
  return request(customersBase(slug), {
    schema: proofingCustomerSchema,
    method: "POST",
    body: { name },
    signal,
  });
}

export function getProofingCustomer(
  slug: string,
  customerId: string,
  signal?: AbortSignal,
): Promise<ProofingCustomer> {
  return request(customerBase(slug, customerId), {
    schema: proofingCustomerSchema,
    signal,
  });
}

export function renameProofingCustomer(
  slug: string,
  customerId: string,
  name: string,
  signal?: AbortSignal,
): Promise<ProofingCustomer> {
  return request(customerBase(slug, customerId), {
    schema: proofingCustomerSchema,
    method: "PATCH",
    body: { name },
    signal,
  });
}

export function getProofingCustomerFlows(
  slug: string,
  customerId: string,
  signal?: AbortSignal,
): Promise<ProofingCustomerFlow[]> {
  return request(`${customerBase(slug, customerId)}/flows`, {
    schema: z.array(proofingCustomerFlowSchema),
    signal,
  });
}

export function setProofingCustomerFlows(
  slug: string,
  customerId: string,
  selection: ProofingFlowSelection,
  signal?: AbortSignal,
): Promise<ProofingCustomerFlow[]> {
  return request(`${customerBase(slug, customerId)}/flow-selection`, {
    schema: z.array(proofingCustomerFlowSchema),
    method: "PUT",
    body: selection,
    signal,
  });
}
