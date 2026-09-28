import { z } from "zod";
import { request } from "./http";
import { auditEventSchema } from "./organization";
import type { AuditEvent } from "./organization";

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
  // Set for a request the customer's backend created with one of its keys.
  apiKeyName: z.string().optional(),
  subjectUserId: z.string().optional(),
  customerId: z.string().optional(),
  customerName: z.string().optional(),
  subjectName: z.string(),
  subjectEmail: z.string(),
  proofedName: z.string().optional(),
  flowId: z.string(),
  flowName: z.string(),
  flowVersion: z.number().optional(),
  // The app the subject used ("idem_app", "yivi_app", "browser"); absent while
  // nobody opened the session.
  method: z.string().optional(),
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

// A paused customer takes no new request; requests already sent run out.
export const PROOFING_CUSTOMER_STATUSES = ["active", "paused"] as const;

// How a customer's webhook endpoint has been answering. State is a plain
// string: an unknown one renders neutrally.
export const webhookHealthSchema = z.object({
  state: z.string(),
  lastStatusCode: z.number().optional(),
  failingSince: z.string().optional(),
  pendingRetries: z.number(),
});

export type WebhookHealth = z.infer<typeof webhookHealthSchema>;

// Empty strings fall back: the customer's name, the org's colour, no logo.
export const proofingCustomerBrandingSchema = z.object({
  displayName: z.string(),
  primaryColor: z.string(),
  supportContact: z.string(),
  privacyUrl: z.string(),
  logoUri: z.string(),
});

export type ProofingCustomerBranding = z.infer<
  typeof proofingCustomerBrandingSchema
>;

export const proofingCustomerSchema = z.object({
  id: z.string(),
  name: z.string(),
  flowIds: z.array(z.string()),
  defaultFlowId: z.string().optional(),
  status: z.enum(PROOFING_CUSTOMER_STATUSES),
  pausedAt: z.string().optional(),
  sessionTtlSeconds: z.number(),
  dataRetentionDays: z.number(),
  webhook: webhookHealthSchema,
  branding: proofingCustomerBrandingSchema,
  createdAt: z.string(),
  updatedAt: z.string(),
});

export type ProofingCustomer = z.infer<typeof proofingCustomerSchema>;

// A customer's requests on one flow since `since`, by outcome; what the outcome
// counts leave is still pending or in progress. As last reconciled by a request
// list read.
export const proofingStatsRowSchema = z.object({
  customerId: z.string(),
  flowId: z.string(),
  sessions: z.number(),
  approved: z.number(),
  rejected: z.number(),
  needsReview: z.number(),
  expired: z.number(),
});

export type ProofingStatsRow = z.infer<typeof proofingStatsRowSchema>;

export const proofingStatsSchema = z.object({
  since: z.string(),
  rows: z.array(proofingStatsRowSchema),
});

export type ProofingStats = z.infer<typeof proofingStatsSchema>;

// An absent field is left as it is.
export interface ProofingCustomerUpdate {
  name?: string;
  paused?: boolean;
  sessionTtlSeconds?: number;
  dataRetentionDays?: number;
}

export const proofingApiKeySchema = z.object({
  id: z.string(),
  name: z.string(),
  prefix: z.string(),
  createdAt: z.string(),
  lastUsedAt: z.string().optional(),
  revokedAt: z.string().optional(),
});

export type ProofingApiKey = z.infer<typeof proofingApiKeySchema>;

// The one answer that carries the key's secret.
export const createdProofingApiKeySchema = proofingApiKeySchema.extend({
  secret: z.string(),
});

export type CreatedProofingApiKey = z.infer<typeof createdProofingApiKeySchema>;

// A customer's endpoint; secret is set only in the answer that created it or
// rotated it.
export const proofingWebhookSchema = z.object({
  configured: z.boolean(),
  url: z.string().optional(),
  events: z.array(z.string()),
  secretLast4: z.string().optional(),
  secret: z.string().optional(),
  health: webhookHealthSchema,
  availableEvents: z.array(z.string()),
  maxAttempts: z.number(),
});

export type ProofingWebhook = z.infer<typeof proofingWebhookSchema>;

export const webhookDeliverySchema = z.object({
  id: z.string(),
  event: z.string(),
  sessionId: z.string().optional(),
  status: z.string(),
  attempts: z.number(),
  lastStatusCode: z.number().optional(),
  lastError: z.string().optional(),
  lastAttemptAt: z.string().optional(),
  deliveredAt: z.string().optional(),
  createdAt: z.string(),
});

export type WebhookDelivery = z.infer<typeof webhookDeliverySchema>;

// A branding save: the text fields, and a new logo file or its removal
// (neither keeps it).
export interface ProofingBrandingInput {
  displayName: string;
  primaryColor: string;
  supportContact: string;
  privacyUrl: string;
  logo?: File;
  removeLogo?: boolean;
}

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

// The org's customer requests of the last 30 days, counted per customer and flow.
export function getProofingStats(
  slug: string,
  signal?: AbortSignal,
): Promise<ProofingStats> {
  return request(`${base(slug)}/stats`, {
    schema: proofingStatsSchema,
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

export function updateProofingCustomer(
  slug: string,
  customerId: string,
  update: ProofingCustomerUpdate,
  signal?: AbortSignal,
): Promise<ProofingCustomer> {
  return request(customerBase(slug, customerId), {
    schema: proofingCustomerSchema,
    method: "PATCH",
    body: update,
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

export function removeProofingCustomer(
  slug: string,
  customerId: string,
  signal?: AbortSignal,
): Promise<void> {
  return request(customerBase(slug, customerId), {
    schema: z.void(),
    method: "DELETE",
    signal,
  });
}

export function saveProofingCustomerBranding(
  slug: string,
  customerId: string,
  input: ProofingBrandingInput,
  signal?: AbortSignal,
): Promise<ProofingCustomer> {
  const form = new FormData();
  form.set("displayName", input.displayName);
  form.set("primaryColor", input.primaryColor);
  form.set("supportContact", input.supportContact);
  form.set("privacyUrl", input.privacyUrl);
  if (input.logo) {
    form.set("logo", input.logo);
  } else if (input.removeLogo) {
    form.set("removeLogo", "true");
  }
  return request(`${customerBase(slug, customerId)}/branding`, {
    schema: proofingCustomerSchema,
    method: "PUT",
    body: form,
    signal,
  });
}

export function getProofingApiKeys(
  slug: string,
  customerId: string,
  signal?: AbortSignal,
): Promise<ProofingApiKey[]> {
  return request(`${customerBase(slug, customerId)}/api-keys`, {
    schema: z.array(proofingApiKeySchema),
    signal,
  });
}

export function createProofingApiKey(
  slug: string,
  customerId: string,
  name: string,
  signal?: AbortSignal,
): Promise<CreatedProofingApiKey> {
  return request(`${customerBase(slug, customerId)}/api-keys`, {
    schema: createdProofingApiKeySchema,
    method: "POST",
    body: { name },
    signal,
  });
}

export function revokeProofingApiKey(
  slug: string,
  customerId: string,
  keyId: string,
  signal?: AbortSignal,
): Promise<ProofingApiKey> {
  return request(
    `${customerBase(slug, customerId)}/api-keys/${encodeURIComponent(keyId)}`,
    { schema: proofingApiKeySchema, method: "DELETE", signal },
  );
}

function webhookBase(slug: string, customerId: string): string {
  return `${customerBase(slug, customerId)}/webhook`;
}

export function getProofingWebhook(
  slug: string,
  customerId: string,
  signal?: AbortSignal,
): Promise<ProofingWebhook> {
  return request(webhookBase(slug, customerId), {
    schema: proofingWebhookSchema,
    signal,
  });
}

export function saveProofingWebhook(
  slug: string,
  customerId: string,
  input: { url: string; events: string[] },
  signal?: AbortSignal,
): Promise<ProofingWebhook> {
  return request(webhookBase(slug, customerId), {
    schema: proofingWebhookSchema,
    method: "PUT",
    body: input,
    signal,
  });
}

export function removeProofingWebhook(
  slug: string,
  customerId: string,
  signal?: AbortSignal,
): Promise<void> {
  return request(webhookBase(slug, customerId), {
    schema: z.void(),
    method: "DELETE",
    signal,
  });
}

export function rotateProofingWebhookSecret(
  slug: string,
  customerId: string,
  signal?: AbortSignal,
): Promise<ProofingWebhook> {
  return request(`${webhookBase(slug, customerId)}/rotate-secret`, {
    schema: proofingWebhookSchema,
    method: "POST",
    body: {},
    signal,
  });
}

export function sendProofingWebhookTest(
  slug: string,
  customerId: string,
  signal?: AbortSignal,
): Promise<void> {
  return request(`${webhookBase(slug, customerId)}/test`, {
    schema: z.void(),
    method: "POST",
    body: {},
    signal,
  });
}

export function getProofingWebhookDeliveries(
  slug: string,
  customerId: string,
  signal?: AbortSignal,
): Promise<WebhookDelivery[]> {
  return request(`${webhookBase(slug, customerId)}/deliveries`, {
    schema: z.array(webhookDeliverySchema),
    signal,
  });
}

// One request's timeline: its audit events, oldest first.
export function getProofingRequestEvents(
  slug: string,
  requestId: string,
  signal?: AbortSignal,
): Promise<AuditEvent[]> {
  return request(
    `${base(slug)}/requests/${encodeURIComponent(requestId)}/events`,
    {
      schema: z
        .object({ events: z.array(auditEventSchema) })
        .transform((page) => page.events),
      signal,
    },
  );
}
