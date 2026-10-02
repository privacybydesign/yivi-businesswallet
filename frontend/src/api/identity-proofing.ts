import { z } from "zod";
import type { Language } from "../i18n/language";
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

// Whether a flow has the diploma step: once their identity is approved, its
// subject must add their DUO diploma extracts (the PDFs from mijn.duo.nl),
// checked by the wallet.
export const DIPLOMA_MODES = ["off", "required"] as const;
export const diplomaModeSchema = z.enum(DIPLOMA_MODES);
export type DiplomaMode = z.infer<typeof diplomaModeSchema>;

// A DUO diploma extract a session holds: what DUO printed about the
// qualification, checked against DUO's signature and the proofed identity.
export const proofingDiplomaSchema = z.object({
  documentType: z.string(),
  qualification: z.string(),
  profiles: z.array(z.string()),
  institution: z.string(),
  placeOfIssue: z.string(),
  dateAwarded: z.string(),
  nlqfLevel: z.string().optional(),
  eqfLevel: z.string().optional(),
  // The number duo.nl/diplomacontrole checks.
  documentNumber: z.string(),
  signedAt: z.string().optional(),
  addedAt: z.string(),
});

export type ProofingDiploma = z.infer<typeof proofingDiplomaSchema>;

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
  // "regula", "engine", or absent for the proofing service's default.
  faceProvider: z.string().optional(),
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
  // A flow that matches the face without reading the chip: every session on
  // it carries the customer's own photo, sent through the customer API.
  // Absent on a single version (created, edited, a version list).
  needsReferencePhoto: z.boolean().optional(),
  // The admin made it available to members; default is the one the request
  // form preselects. A member's list only holds allowed flows.
  allowed: z.boolean(),
  default: z.boolean(),
  // Absent on a single version (created, edited, a version list): the flow
  // list carries it.
  diplomaMode: diplomaModeSchema.optional(),
});

export type ProofingFlow = z.infer<typeof proofingFlowSchema>;

// A test key's sessions run scripted in the org's sandbox.
export const proofingModeSchema = z.enum(["live", "test"]);

export type ProofingMode = z.infer<typeof proofingModeSchema>;

export const proofingRequestSchema = z.object({
  id: z.string(),
  requestedByName: z.string(),
  // Set for a request the customer's backend created with one of its keys.
  apiKeyName: z.string().optional(),
  mode: proofingModeSchema,
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
  // When the session's personal data is due to be purged; purgedAt once it was.
  purgeAt: z.string().optional(),
  purgedAt: z.string().optional(),
  // Whether the session asks for diploma extracts, the ones it holds, and
  // until when the subject can add one (absent until approved).
  diplomaMode: diplomaModeSchema,
  diplomas: z.array(proofingDiplomaSchema),
  diplomasUntil: z.string().optional(),
  // A request for one known person: only subjectName, born on the date it
  // was sent with, is approved (IDENTITY_MISMATCH otherwise).
  expectedSubject: z.boolean(),
});

export type ProofingRequest = z.infer<typeof proofingRequestSchema>;

// mailSent is false when the org's mail could not be sent: the request stands,
// but the recipient never got its link. deepLink is an on-screen Idem session's
// vcmrtd link, the QR code the page shows, until deepLinkExpiresAt; absent for
// a mailed or Yivi request.
export const proofingSentSchema = proofingRequestSchema.extend({
  mailSent: z.boolean(),
  deepLink: z.string().optional(),
  deepLinkExpiresAt: z.string().optional(),
  // A hosted request's link to the customer's page, for the member to hand
  // the person; valid 72 hours.
  hostedUrl: z.string().optional(),
});

export type ProofingSent = z.infer<typeof proofingSentSchema>;

// The body of a new flow and of a new version of one: every setting the
// proofing service takes. Omitted optional fields inherit the tenant's policy.
export interface ProofingFlowSpec {
  name: string;
  steps: string[];
  requestedAttributes?: string[];
  faceProvider?: string;
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

// The app the subject proofs with, and how the session reaches them: mailed
// (the default) or shown on the sender's screen. A Yivi session runs its face
// check in the browser showing its QR, so it is on-screen only.
export const PROOFING_METHODS = ["idem_app", "yivi_app"] as const;
export type ProofingMethod = (typeof PROOFING_METHODS)[number];
export type ProofingChannel = "email" | "on_screen" | "hosted";

export type ProofingRequestInput =
  | { userId: string; flowId: string }
  | {
      customerId: string;
      email: string;
      name: string;
      // YYYY-MM-DD: with name, only that person can pass.
      birthDate?: string;
      flowId: string;
      method?: ProofingMethod;
      channel?: ProofingChannel;
    };

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
  // Leaves the "powered by" line off the customer's hosted pages.
  hidePoweredBy: z.boolean(),
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
  // Where a hosted page may send its subject back to and be embedded on.
  allowedRedirectOrigins: z.array(z.string()),
  // Live requests need an unrevoked live API key.
  hasLiveKey: z.boolean(),
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
  allowedRedirectOrigins?: string[];
}

export const proofingApiKeySchema = z.object({
  id: z.string(),
  name: z.string(),
  prefix: z.string(),
  mode: proofingModeSchema,
  // What the key may call; results:read reads verified identities.
  scopes: z.array(z.string()),
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
  // The customer's own endpoint it went to; absent for the wallet's default.
  endpointUrl: z.string().optional(),
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
  hidePoweredBy: boolean;
  logo?: File;
  removeLogo?: boolean;
}

export const proofingCustomerFlowSchema = proofingFlowSchema
  .omit({ allowed: true, default: true })
  .extend({ assigned: z.boolean(), default: z.boolean() });

export type ProofingCustomerFlow = z.infer<typeof proofingCustomerFlowSchema>;

// Whether an org's identity proofing is paused, and by whom: a platform
// admin's pause the org's admin cannot lift.
export const proofingPauseSchema = z.object({
  organizationId: z.string(),
  paused: z.boolean(),
  platformPausedAt: z.string().optional(),
  orgPausedAt: z.string().optional(),
});

export type ProofingPause = z.infer<typeof proofingPauseSchema>;

export function getProofingPause(
  slug: string,
  signal?: AbortSignal,
): Promise<ProofingPause> {
  return request(`${base(slug)}/pause`, {
    schema: proofingPauseSchema,
    signal,
  });
}

// The org admin's own switch.
export function setProofingPause(
  slug: string,
  paused: boolean,
  signal?: AbortSignal,
): Promise<ProofingPause> {
  return request(`${base(slug)}/pause`, {
    schema: proofingPauseSchema,
    method: "PUT",
    body: { paused },
    signal,
  });
}

// Every paused org, for the platform admin.
export function listProofingPauses(
  signal?: AbortSignal,
): Promise<ProofingPause[]> {
  return request("/api/v1/admin/identity-proofing/pauses", {
    schema: z
      .object({ pauses: z.array(proofingPauseSchema) })
      .transform((page) => page.pauses),
    signal,
  });
}

// The platform admin's pause of one org.
export function setPlatformProofingPause(
  orgId: string,
  paused: boolean,
  signal?: AbortSignal,
): Promise<ProofingPause> {
  return request(
    `/api/v1/admin/organizations/${encodeURIComponent(orgId)}/identity-proofing/pause`,
    { schema: proofingPauseSchema, method: "PUT", body: { paused }, signal },
  );
}

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

// How a flow's hosted page behaves: whether links may be made for it, the
// languages it offers (empty: every one), and how it ends.
export const proofingFlowHostedSchema = z.object({
  enabled: z.boolean(),
  locales: z.array(z.string()),
  completion: z.enum(["redirect", "done"]),
});

export type ProofingFlowHosted = z.infer<typeof proofingFlowHostedSchema>;

export function getProofingFlowHosted(
  slug: string,
  flowId: string,
  signal?: AbortSignal,
): Promise<ProofingFlowHosted> {
  return request(`${flowBase(slug, flowId)}/hosted`, {
    schema: proofingFlowHostedSchema,
    signal,
  });
}

export function saveProofingFlowHosted(
  slug: string,
  flowId: string,
  settings: ProofingFlowHosted,
  signal?: AbortSignal,
): Promise<ProofingFlowHosted> {
  return request(`${flowBase(slug, flowId)}/hosted`, {
    schema: proofingFlowHostedSchema,
    method: "PUT",
    body: settings,
    signal,
  });
}

export const proofingFlowDiplomasSchema = z.object({
  diplomaMode: diplomaModeSchema,
});

export function saveProofingFlowDiplomas(
  slug: string,
  flowId: string,
  diplomaMode: DiplomaMode,
  signal?: AbortSignal,
): Promise<DiplomaMode> {
  return request(`${flowBase(slug, flowId)}/diplomas`, {
    schema: proofingFlowDiplomasSchema,
    method: "PUT",
    body: { diplomaMode },
    signal,
  }).then((r) => r.diplomaMode);
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

// language is the sender's wallet language: the mail's, and the Idem app's.
export function createProofingRequest(
  slug: string,
  input: ProofingRequestInput,
  language: Language,
  signal?: AbortSignal,
): Promise<ProofingSent> {
  return request(`${base(slug)}/requests`, {
    schema: proofingSentSchema,
    method: "POST",
    body: { ...input, language },
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
  form.set("hidePoweredBy", String(input.hidePoweredBy));
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
  mode: ProofingMode,
  signal?: AbortSignal,
): Promise<CreatedProofingApiKey> {
  return request(`${customerBase(slug, customerId)}/api-keys`, {
    schema: createdProofingApiKeySchema,
    method: "POST",
    body: { name, mode },
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

// A face image in a result: its bytes base64, of a type a browser renders.
const proofingImageSchema = z.object({
  mimeType: z.string(),
  data: z.string(),
});

export type ProofingImage = z.infer<typeof proofingImageSchema>;

// One request's timeline: its audit events, oldest first.
// A settled customer request's result, as an admin reads it in the wallet:
// the identity, the document's photo and the selfie only for an approval.
// Every read is audited.
export const proofingResultSchema = z.object({
  status: z.string(),
  assuranceLevel: z.string().optional(),
  eidasLevel: z.string().optional(),
  errorCode: z.string().optional(),
  verifiedAt: z.string().optional(),
  identity: z
    .object({
      givenName: z.string().optional(),
      familyName: z.string().optional(),
      birthDate: z.string().optional(),
      nationality: z.string().optional(),
    })
    .optional(),
  evidence: z.array(
    z.object({
      type: z.string(),
      documentType: z.string().optional(),
      issuingState: z.string().optional(),
      expiryDate: z.string().optional(),
      passiveAuth: z.string().optional(),
      activeAuth: z.string().optional(),
      faceMatch: z.number().optional(),
      liveness: z.string().optional(),
    }),
  ),
  photo: proofingImageSchema.optional(),
  selfie: proofingImageSchema.optional(),
  // The customer's own photo the selfie was matched against, for a flow
  // without the chip read.
  referencePhoto: proofingImageSchema.optional(),
  documentImage: proofingImageSchema.optional(),
  documentImageBack: proofingImageSchema.optional(),
});

export type ProofingResult = z.infer<typeof proofingResultSchema>;

export function getProofingRequestResult(
  slug: string,
  requestId: string,
  signal?: AbortSignal,
): Promise<ProofingResult> {
  return request(
    `${base(slug)}/requests/${encodeURIComponent(requestId)}/result`,
    { schema: proofingResultSchema, signal },
  );
}

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

export function getProofingRequest(
  slug: string,
  requestId: string,
  signal?: AbortSignal,
): Promise<ProofingRequest> {
  return request(`${base(slug)}/requests/${encodeURIComponent(requestId)}`, {
    schema: proofingRequestSchema,
    signal,
  });
}

// The OpenID4VP disclosure an on-screen Yivi request asks for. walletLink is
// the openid4vp:// request the Yivi app opens.
export const proofingYiviStartSchema = z.object({
  walletLink: z.string(),
  expiresAt: z.string(),
});

export type ProofingYiviStart = z.infer<typeof proofingYiviStartSchema>;

// Where a verify page's session lives: an org's request a member shows on
// screen, or a hosted link its subject opened (no login: the token is it).
export type VerifyTarget =
  | { kind: "request"; slug: string; requestId: string }
  | { kind: "hosted"; token: string };

function verifyPath(target: VerifyTarget): string {
  return target.kind === "request"
    ? `${base(target.slug)}/requests/${encodeURIComponent(target.requestId)}`
    : `/api/v1/proof/${encodeURIComponent(target.token)}`;
}

// How far a verify page's session got: the part of a request, or of a hosted
// link, the page follows. linkExpiresAt is when it can no longer go on.
export interface ProofingProgress {
  status: string;
  errorCode?: string;
  linkExpiresAt: string;
  // Until when the subject can add diploma extracts, once approved.
  diplomasUntil?: string;
}

export function startProofingYivi(
  target: VerifyTarget,
  signal?: AbortSignal,
): Promise<ProofingYiviStart> {
  return request(`${verifyPath(target)}/yivi/start`, {
    schema: proofingYiviStartSchema,
    method: "POST",
    signal,
  });
}

// A fresh Idem app link for a running session: a new claim once the first
// lapsed, or a handover once the app that held the session left.
export const proofingClaimLinkSchema = z.object({
  deepLink: z.string(),
  expiresAt: z.string(),
});

export type ProofingClaimLink = z.infer<typeof proofingClaimLinkSchema>;

// Where a running on-screen Idem request's phone is: no phone scanned yet, the
// app holds the session, or it was closed and a claim link hands it over.
export const proofingAppSchema = z.object({
  app: z.enum(["waiting", "connected", "away"]),
});

export type ProofingApp = z.infer<typeof proofingAppSchema>["app"];

export function getProofingApp(
  slug: string,
  requestId: string,
  signal?: AbortSignal,
): Promise<z.infer<typeof proofingAppSchema>> {
  return request(
    `${base(slug)}/requests/${encodeURIComponent(requestId)}/app`,
    { schema: proofingAppSchema, signal },
  );
}

export function newProofingClaimLink(
  target: VerifyTarget,
  signal?: AbortSignal,
): Promise<ProofingClaimLink> {
  return request(`${verifyPath(target)}/claim-link`, {
    schema: proofingClaimLinkSchema,
    method: "POST",
    signal,
  });
}

// done is false while the subject has not finished in the Yivi app; ok false
// ended the session and code says why, ok true moves on to the face check.
export const proofingYiviDisclosureSchema = z.object({
  done: z.boolean(),
  ok: z.boolean(),
  code: z.string().optional(),
  stableFrames: z.number().optional(),
  maxAttempts: z.number().optional(),
});

export type ProofingYiviDisclosure = z.infer<
  typeof proofingYiviDisclosureSchema
>;

export function getProofingYiviDisclosure(
  target: VerifyTarget,
  signal?: AbortSignal,
): Promise<ProofingYiviDisclosure> {
  return request(`${verifyPath(target)}/yivi/disclosure`, {
    schema: proofingYiviDisclosureSchema,
    signal,
  });
}

// One live camera frame scored against the disclosed photo. decision is
// "pending", "approved" or "rejected".
export const proofingFaceVerdictSchema = z.object({
  faceDetected: z.boolean(),
  matched: z.boolean(),
  consecutive: z.number(),
  stableFrames: z.number(),
  attempts: z.number(),
  maxAttempts: z.number(),
  decision: z.string(),
});

export type ProofingFaceVerdict = z.infer<typeof proofingFaceVerdictSchema>;

export function submitProofingFaceFrame(
  target: VerifyTarget,
  image: string,
  signal?: AbortSignal,
): Promise<ProofingFaceVerdict> {
  return request(`${verifyPath(target)}/yivi/face`, {
    schema: proofingFaceVerdictSchema,
    method: "POST",
    body: { image },
    signal,
  });
}

// A hosted link's progress: its status and, until it settles, when the link
// (not started) or its session (started) ends.
export const hostedProgressSchema = z.object({
  status: z.string(),
  errorCode: z.string().optional(),
  method: z.string().optional(),
  linkExpiresAt: z.string(),
  started: z.boolean(),
  // Until when the subject can add a diploma extract; absent while they cannot.
  diplomasUntil: z.string().optional(),
});

export type HostedProgress = z.infer<typeof hostedProgressSchema>;

// The hosted page: who asks, what the flow collects, the progress, and how
// the page hands its subject back once settled.
export const hostedProofingSchema = hostedProgressSchema.extend({
  // The customer's id for the session, handed back on completion.
  sessionId: z.string(),
  // Absent shows the page's own done screen.
  redirectUrl: z.string().optional(),
  // The only origins the page posts its completion message to.
  embedOrigins: z.array(z.string()),
  // en or nl; absent leaves the browser's.
  language: z.string().optional(),
  // The languages the flow's page offers; empty is every one.
  locales: z.array(z.string()),
  customer: z.object({
    name: z.string(),
    branding: proofingCustomerBrandingSchema,
    dataRetentionDays: z.number(),
  }),
  flow: z.object({
    name: z.string(),
    requiredAssuranceLevel: z.string().optional(),
    requestedAttributes: z.array(z.string()),
    yiviAvailable: z.boolean(),
    diplomaMode: diplomaModeSchema,
  }),
  // The diploma extracts the subject added.
  diplomas: z.array(proofingDiplomaSchema),
});

export type HostedProofing = z.infer<typeof hostedProofingSchema>;

export const hostedStartSchema = hostedProgressSchema.extend({
  deepLink: z.string().optional(),
});

export type HostedStart = z.infer<typeof hostedStartSchema>;

export function getHostedProofing(
  token: string,
  signal?: AbortSignal,
): Promise<HostedProofing> {
  return request(verifyPath({ kind: "hosted", token }), {
    schema: hostedProofingSchema,
    signal,
  });
}

export function getHostedProofingStatus(
  token: string,
  signal?: AbortSignal,
): Promise<HostedProgress> {
  return request(`${verifyPath({ kind: "hosted", token })}/status`, {
    schema: hostedProgressSchema,
    signal,
  });
}

export function startHostedProofing(
  token: string,
  method: ProofingMethod,
  signal?: AbortSignal,
): Promise<HostedStart> {
  return request(`${verifyPath({ kind: "hosted", token })}/start`, {
    schema: hostedStartSchema,
    method: "POST",
    body: { method },
    signal,
  });
}

// The subject declined what is collected: cancels the link before it started.
export function declineHostedProofing(
  token: string,
  signal?: AbortSignal,
): Promise<HostedProgress> {
  return request(`${verifyPath({ kind: "hosted", token })}/decline`, {
    schema: hostedProgressSchema,
    method: "POST",
    signal,
  });
}

// What became of one uploaded file: kept, or refused with a reason
// (not_a_diploma, signature_invalid, holder_mismatch, duplicate).
export const diplomaVerdictSchema = z.object({
  fileName: z.string(),
  accepted: z.boolean(),
  diploma: proofingDiplomaSchema.optional(),
  reason: z.string().optional(),
  failedCheck: z.string().optional(),
});

export type DiplomaVerdict = z.infer<typeof diplomaVerdictSchema>;

// An upload checks a signature per file and may load the EU trusted lists.
const DIPLOMA_UPLOAD_TIMEOUT_MS = 120_000;

// Uploads DUO diploma extracts on a verify page, after an approved identity.
export function uploadProofingDiplomas(
  target: VerifyTarget,
  files: File[],
  signal?: AbortSignal,
): Promise<DiplomaVerdict[]> {
  const form = new FormData();
  for (const file of files) {
    form.append("file", file);
  }
  return request(`${verifyPath(target)}/diplomas`, {
    schema: z.array(diplomaVerdictSchema),
    method: "POST",
    body: form,
    timeoutMs: DIPLOMA_UPLOAD_TIMEOUT_MS,
    signal,
  });
}

// An administrator's decision on a request under review; reason is required.
export interface ProofingReviewInput {
  decision: "approve" | "reject";
  reason: string;
}

export function decideProofingReview(
  slug: string,
  requestId: string,
  input: ProofingReviewInput,
  signal?: AbortSignal,
): Promise<ProofingRequest> {
  return request(
    `${base(slug)}/requests/${encodeURIComponent(requestId)}/review`,
    { schema: proofingRequestSchema, method: "POST", body: input, signal },
  );
}
