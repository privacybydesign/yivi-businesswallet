import { z } from "zod";
import type { Language } from "../i18n/language";
import { absoluteApiUrl, request } from "./http";
import { auditEventSchema } from "./organization";
import type { AuditEvent } from "./organization";

// Identity proofing: flows defined by org admins, who choose which members may
// use them and which is the default, the org's settings, API keys and webhook
// endpoint, and requests any member can send, to a member or an external
// person. Only a request's outcome and assurance level come back, plus, for an
// approved external person, the name on their document for a limited time.
//
// Step, check and status values are plain strings rather than zod enums, so a
// value the backend adds never breaks the page. The few closed sets that stay
// enums fall back (.catch) to their most conservative value on one they do not
// know, for the same reason.

const assuranceTierSchema = z.object({
  level: z.string(),
  minPercent: z.number(),
});

export type ProofingAssuranceTier = z.infer<typeof assuranceTierSchema>;

// Whether a flow has the diploma step: once their identity is approved, its
// subject must add their DUO diploma extracts (the PDFs from mijn.duo.nl),
// checked by the wallet.
export const DIPLOMA_MODES = ["off", "required"] as const;
// An unknown mode reads as required: the page then asks for diplomas rather
// than silently skipping a step the backend enforces.
const diplomaModeSchema = z.enum(DIPLOMA_MODES).catch("required");

// Whether a flow has the address step: once their identity is approved, its
// subject enters their postal address. It is what they declare, not read off
// a document.
const ADDRESS_MODES = ["off", "required"] as const;
// An unknown mode reads as required, as diplomaModeSchema.
const addressModeSchema = z.enum(ADDRESS_MODES).catch("required");
export type AddressMode = z.infer<typeof addressModeSchema>;

// How an address was checked when it was kept: found in the Dutch address
// register, Dutch but the register could not be asked, or outside the
// Netherlands. An address kept before addresses were checked has none.
const ADDRESS_VERIFICATIONS = ["verified", "unchecked", "unsupported"] as const;
export type AddressVerification = (typeof ADDRESS_VERIFICATIONS)[number];

// The postal address a subject entered.
const proofingAddressSchema = z.object({
  street: z.string(),
  houseNumber: z.string(),
  postalCode: z.string(),
  city: z.string(),
  country: z.string(),
  verification: z.enum(ADDRESS_VERIFICATIONS).optional().catch(undefined),
});

export type ProofingAddress = z.infer<typeof proofingAddressSchema>;
// What the address step sends; the backend sets the verification.
export type ProofingAddressInput = Omit<ProofingAddress, "verification">;

// An address as the Dutch address register writes it.
const registerAddressSchema = z.object({
  street: z.string(),
  houseNumber: z.string(),
  postalCode: z.string(),
  city: z.string(),
});

export type RegisterAddress = z.infer<typeof registerAddressSchema>;

// What a flow's sessions are for: an identity check, or an external person
// asking for the data held of them ("see my data") or for its erasure
// ("delete my data"). A data request goes to review once the person is
// proven, with the org's sessions of that person. The data requests are every
// org's own flows (DATA_REQUEST_KINDS), not flows an admin sets up: they are
// never in the flow list, and the backend has them at data-request-flows.
export const DATA_REQUEST_KINDS = ["data_access", "data_erasure"] as const;
export type DataRequestKind = (typeof DATA_REQUEST_KINDS)[number];
export const FLOW_KINDS = ["identity", ...DATA_REQUEST_KINDS] as const;
const flowKindSchema = z.enum(FLOW_KINDS).catch("identity");
export type FlowKind = z.infer<typeof flowKindSchema>;

export function isDataRequest(
  kind: FlowKind | undefined,
): kind is DataRequestKind {
  return kind === "data_access" || kind === "data_erasure";
}
export type DiplomaMode = z.infer<typeof diplomaModeSchema>;

// A DUO diploma extract a session holds: what DUO printed about the
// qualification, checked against DUO's signature and the proofed identity.
const proofingDiplomaSchema = z.object({
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

// An extract as the hosted page lists it: only what the subject needs to
// recognise it, never its document number, since the link's token alone
// opens the page.
const hostedDiplomaSchema = proofingDiplomaSchema.pick({
  qualification: true,
  institution: true,
  dateAwarded: true,
  nlqfLevel: true,
});

export type HostedDiploma = z.infer<typeof hostedDiplomaSchema>;

// Why each requested item is collected, by item and then language: what the
// person reads before they start.
const attributeReasonsSchema = z.record(
  z.string(),
  z.record(z.string(), z.string()),
);

export type AttributeReasons = z.infer<typeof attributeReasonsSchema>;

// A flow version as the proofing service stores it. The id is stable across
// versions; exactly one version is active, the one new requests run.
const proofingFlowSchema = z.object({
  id: z.string(),
  version: z.number(),
  active: z.boolean(),
  name: z.string(),
  steps: z.array(z.string()),
  requestedAttributes: z.array(z.string()).optional(),
  ageOver: z.number().optional(),
  // Absent for a version saved before reasons existed.
  attributeReasons: attributeReasonsSchema.optional(),
  selfieLocation: z.string().optional(),
  // "regula", "engine", or absent for the proofing service's default.
  faceProvider: z.string().optional(),
  acceptedDocumentTypes: z.array(z.string()).optional(),
  acceptedIssuingCountries: z.array(z.string()).optional(),
  requiredChecks: z.array(z.string()).optional(),
  checkThresholds: z.record(z.string(), z.number()).optional(),
  requiredAssuranceLevel: z.string().optional(),
  bsnPolicy: z.string().optional(),
  keepFacePhotos: z.boolean().optional(),
  blurBsn: z.boolean().optional(),
  retentionOverrideSeconds: z.number().optional(),
  sessionTtlSeconds: z.number().optional(),
  assuranceTiers: z.array(assuranceTierSchema).optional(),
  legalBasis: z.string().optional(),
  processingPurpose: z.string().optional(),
  createdAt: z.string(),
  // False for a flow a recipient cannot finish with only the vcmrtd app.
  completable: z.boolean(),
  // A flow that matches the face without reading the chip: every session on
  // it carries the sender's own photo, sent through the API.
  // Absent on a single version (created, edited, a version list).
  needsReferencePhoto: z.boolean().optional(),
  // The admin made it available to members; default is the org's one default,
  // the one a request form preselects, whoever may use it. A member's list
  // only holds allowed flows.
  allowed: z.boolean(),
  default: z.boolean(),
  // The most days an external person's data is kept on this flow: the flow's
  // retention, else the org's, plus the engine's extra day. Absent on a single
  // version.
  retentionDays: z.number().optional(),
  // Absent on a single version (created, edited, a version list): the flow
  // list carries it.
  diplomaMode: diplomaModeSchema.optional(),
  // Absent on a single version, as diplomaMode.
  addressMode: addressModeSchema.optional(),
  // Absent on a single version, as diplomaMode.
  kind: flowKindSchema.optional(),
  // The flow's "Allow sending this flow as a link" switch: off, the send
  // form offers only Show on this screen. Absent on a single version, as
  // diplomaMode.
  hostedEnabled: z.boolean().optional(),
});

export type ProofingFlow = z.infer<typeof proofingFlowSchema>;

const proofingRequestSchema = z.object({
  id: z.string(),
  // The member who sent it; absent for one the API created, and once that
  // member's account is gone.
  requestedById: z.string().optional(),
  requestedByName: z.string(),
  // Set for a request the org's software created with one of its keys.
  apiKeyName: z.string().optional(),
  subjectUserId: z.string().optional(),
  // Whether the request went to an external person, not a member.
  external: z.boolean(),
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
  // Whether the session asks for the postal address, the one entered, and
  // until when the subject can enter it (absent until approved, and once
  // entered).
  addressMode: addressModeSchema,
  address: proofingAddressSchema.optional(),
  addressUntil: z.string().optional(),
  // A request for one known person: only subjectName, born on the date it
  // was sent with, is approved (IDENTITY_MISMATCH otherwise).
  expectedSubject: z.boolean(),
  // A data request goes to review once the person is proven; an approved
  // "see my data" request's data downloads until dataExportUntil.
  flowKind: flowKindSchema,
  dataExportUntil: z.string().optional(),
});

export type ProofingRequest = z.infer<typeof proofingRequestSchema>;

// mailSent is false when the org's mail could not be sent: the request stands,
// but the recipient never got its link. deepLink is an on-screen Idem session's
// vcmrtd link, the QR code the page shows, until deepLinkExpiresAt; absent for
// a mailed or Yivi request.
const proofingSentSchema = proofingRequestSchema.extend({
  mailSent: z.boolean(),
  deepLink: z.string().optional(),
  deepLinkExpiresAt: z.string().optional(),
  // A hosted request's link to the hosted page, for the member to hand the
  // person; valid 72 hours.
  hostedUrl: z.string().optional(),
});

export type ProofingSent = z.infer<typeof proofingSentSchema>;

// The body of a new flow and of a new version of one: every setting the
// proofing service takes. Omitted optional fields inherit the tenant's policy.
export interface ProofingFlowSpec {
  name: string;
  steps: string[];
  requestedAttributes?: string[];
  ageOver?: number;
  attributeReasons?: AttributeReasons;
  faceProvider?: string;
  acceptedDocumentTypes?: string[];
  acceptedIssuingCountries?: string[];
  requiredChecks?: string[];
  checkThresholds?: Record<string, number>;
  requiredAssuranceLevel?: string;
  bsnPolicy?: string;
  keepFacePhotos?: boolean;
  blurBsn?: boolean;
  retentionOverrideSeconds?: number;
  sessionTtlSeconds?: number;
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
export type ProofingMethod = "idem_app" | "yivi_app";
export type ProofingChannel = "email" | "on_screen" | "hosted";

// An external person's request names its flow, or for a data request its
// kind: it runs on the org's own flow of that kind.
export type ProofingRequestInput =
  | { userId: string; flowId: string }
  | ({
      email: string;
      name: string;
      // YYYY-MM-DD: with name, only that person can pass.
      birthDate?: string;
      method?: ProofingMethod;
      channel?: ProofingChannel;
    } & ({ flowId: string } | { flowKind: DataRequestKind }));

// How the org's "delete my data" requests are decided once the person is
// proven: by a reviewer, or automatically (the sure matches erased at once, the
// unsure ones left to a reviewer). What the law makes the org keep is its legal
// retention. ErasureHandling in backend/internal/proofing.
export const ERASURE_HANDLINGS = ["review", "automatic"] as const;
export type ErasureHandling = (typeof ERASURE_HANDLINGS)[number];

// How the org's webhook endpoint has been answering. State is a plain string:
// an unknown one renders neutrally.
const webhookHealthSchema = z.object({
  state: z.string(),
  lastStatusCode: z.number().optional(),
  failingSince: z.string().optional(),
  pendingRetries: z.number(),
});

export type WebhookHealth = z.infer<typeof webhookHealthSchema>;

// The rest of the org's proofing white-label, the same colours as the org
// theme has (bar its sidebar); empty keeps the design's own.
export const PROOFING_THEME_COLOR_FIELDS = [
  "textColor",
  "surfaceColor",
  "borderColor",
  "linkColor",
  "successColor",
  "warningColor",
  "errorColor",
  "topbarColor",
] as const;

export type ProofingThemeColorField =
  (typeof PROOFING_THEME_COLOR_FIELDS)[number];

// Empty strings fall back: in the mail, the org's name, the org's colour, no logo.
const proofingBrandingSchema = z.object({
  displayName: z.string(),
  primaryColor: z.string(),
  // The Idem app's second colour; empty keeps the design's.
  secondaryColor: z.string(),
  textColor: z.string(),
  surfaceColor: z.string(),
  borderColor: z.string(),
  linkColor: z.string(),
  successColor: z.string(),
  warningColor: z.string(),
  errorColor: z.string(),
  topbarColor: z.string(),
  // A CSS font-family list, as the org theme's.
  fontFamily: z.string(),
  supportContact: z.string(),
  privacyUrl: z.string(),
  logoUri: z.string(),
  // Leaves the "powered by" line off the hosted pages.
  hidePoweredBy: z.boolean(),
});

export type ProofingBranding = z.infer<typeof proofingBrandingSchema>;

// How long the law makes the org keep the data of every identity check,
// counted from the check's end, and why, in the languages the person's page
// speaks.
const proofingLegalRetentionSchema = z.object({
  months: z.number(),
  reasonEn: z.string(),
  reasonNl: z.string(),
});

export type ProofingLegalRetention = z.infer<
  typeof proofingLegalRetentionSchema
>;

// What the org set once for all its flows. Its session lifetime and data
// retention are fixed defaults, which a flow overrides.
const proofingSettingsSchema = z.object({
  sessionTtlSeconds: z.number(),
  dataRetentionDays: z.number(),
  erasureHandling: z.enum(ERASURE_HANDLINGS).catch("review"),
  // Null without one.
  legalRetention: proofingLegalRetentionSchema.nullable(),
  branding: proofingBrandingSchema,
  // Where a hosted page may send its subject back to and be embedded on.
  allowedRedirectOrigins: z.array(z.string()),
});

export type ProofingSettings = z.infer<typeof proofingSettingsSchema>;

// The external requests of the last 30 days on one flow, by outcome; what the
// outcome counts leave is still pending or in progress. As last reconciled by a
// request list read.
const proofingStatsRowSchema = z.object({
  flowId: z.string(),
  sessions: z.number(),
  approved: z.number(),
  rejected: z.number(),
  needsReview: z.number(),
  expired: z.number(),
  // Withdrawn before they finished; not counted under expired.
  cancelled: z.number(),
});

export type ProofingStatsRow = z.infer<typeof proofingStatsRowSchema>;

const proofingStatsSchema = z.object({
  since: z.string(),
  rows: z.array(proofingStatsRowSchema),
});

export type ProofingStats = z.infer<typeof proofingStatsSchema>;

// An absent field is left as it is. The session lifetime and data retention
// are not the org's to change.
export interface ProofingSettingsUpdate {
  erasureHandling?: ErasureHandling;
  // Null removes it.
  legalRetention?: ProofingLegalRetention | null;
  allowedRedirectOrigins?: string[];
}

const proofingApiKeySchema = z.object({
  id: z.string(),
  name: z.string(),
  prefix: z.string(),
  // What the key may call; results:read reads verified identities.
  scopes: z.array(z.string()),
  // Absent once the user who created the key is gone.
  createdByName: z.string().optional(),
  createdAt: z.string(),
  lastUsedAt: z.string().optional(),
  revokedAt: z.string().optional(),
});

export type ProofingApiKey = z.infer<typeof proofingApiKeySchema>;

// The one answer that carries the key's secret.
const createdProofingApiKeySchema = proofingApiKeySchema.extend({
  secret: z.string(),
});

export type CreatedProofingApiKey = z.infer<typeof createdProofingApiKeySchema>;

// The org's endpoint; secret is set only in the answer that created it or
// rotated it.
const proofingWebhookSchema = z.object({
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

const webhookDeliverySchema = z.object({
  id: z.string(),
  event: z.string(),
  sessionId: z.string().optional(),
  // The org's endpoint it went to.
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
export interface ProofingBrandingInput extends Record<
  ProofingThemeColorField,
  string
> {
  displayName: string;
  primaryColor: string;
  secondaryColor: string;
  fontFamily: string;
  supportContact: string;
  privacyUrl: string;
  hidePoweredBy: boolean;
  logo?: File;
  removeLogo?: boolean;
}

// A flow's own branding over the org's: not custom is the org's, with empty
// fields. Each field it leaves empty, and no logo, is the org's.
const proofingFlowBrandingSchema = z.object({
  custom: z.boolean(),
  displayName: z.string(),
  primaryColor: z.string(),
  supportContact: z.string(),
  privacyUrl: z.string(),
  logoUri: z.string(),
});

export type ProofingFlowBranding = z.infer<typeof proofingFlowBrandingSchema>;

export type ProofingFlowBrandingInput = Pick<
  ProofingBrandingInput,
  | "displayName"
  | "primaryColor"
  | "supportContact"
  | "privacyUrl"
  | "logo"
  | "removeLogo"
> & { custom: boolean };

// Whether an org's identity proofing is paused by a platform admin, and by
// whom.
const proofingPausedBySchema = z.object({
  userId: z.string(),
  name: z.string(),
});

const proofingPauseSchema = z.object({
  organizationId: z.string(),
  paused: z.boolean(),
  platformPausedAt: z.string().optional(),
  platformPausedBy: proofingPausedBySchema.optional(),
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

export function getProofingFlows(
  slug: string,
  signal?: AbortSignal,
): Promise<ProofingFlow[]> {
  return request(`${base(slug)}/flows`, {
    schema: z.array(proofingFlowSchema),
    signal,
  });
}

// The org's See my data and Delete my data flows, in that order: admin-only.
// The first read creates them.
export function getDataRequestFlows(
  slug: string,
  signal?: AbortSignal,
): Promise<ProofingFlow[]> {
  return request(`${base(slug)}/data-request-flows`, {
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
const proofingFlowHostedSchema = z.object({
  enabled: z.boolean(),
  locales: z.array(z.string()),
  completion: z.enum(["redirect", "done"]).catch("done"),
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

const proofingFlowDiplomasSchema = z.object({
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

const proofingFlowAddressSchema = z.object({
  addressMode: addressModeSchema,
});

export function saveProofingFlowAddress(
  slug: string,
  flowId: string,
  addressMode: AddressMode,
  signal?: AbortSignal,
): Promise<AddressMode> {
  return request(`${flowBase(slug, flowId)}/address`, {
    schema: proofingFlowAddressSchema,
    method: "PUT",
    body: { addressMode },
    signal,
  }).then((r) => r.addressMode);
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

// The org's external requests of the last 30 days, counted per flow.
export function getProofingStats(
  slug: string,
  signal?: AbortSignal,
): Promise<ProofingStats> {
  return request(`${base(slug)}/stats`, {
    schema: proofingStatsSchema,
    signal,
  });
}

// MAX_LISTED_REQUESTS is the most requests one list read returns, the newest
// (maxListedRequests in backend/internal/proofing): a list that long may have
// been cut off, so a count read from it is only a lower bound.
export const MAX_LISTED_REQUESTS = 200;

// The org's requests, newest first and capped by the backend after the
// narrowing: to those sent to one member, to those sent to members or to
// external people, to those sent on one flow, to those held in one status (in
// review: the longest in review first), or to the data requests.
export function getProofingRequests(
  slug: string,
  filter: {
    subjectUserId?: string;
    subjectKind?: "member" | "external";
    flowId?: string;
    status?: "needs_review" | "approved" | "rejected";
    flowKind?: "data_request";
  },
  signal?: AbortSignal,
): Promise<ProofingRequest[]> {
  const params = new URLSearchParams();
  if (filter.subjectUserId) params.set("subjectUserId", filter.subjectUserId);
  if (filter.subjectKind) params.set("subjectKind", filter.subjectKind);
  if (filter.flowId) params.set("flowId", filter.flowId);
  if (filter.status) params.set("status", filter.status);
  if (filter.flowKind) params.set("flowKind", filter.flowKind);
  const query = params.size > 0 ? `?${params.toString()}` : "";
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

export function getProofingSettings(
  slug: string,
  signal?: AbortSignal,
): Promise<ProofingSettings> {
  return request(`${base(slug)}/settings`, {
    schema: proofingSettingsSchema,
    signal,
  });
}

export function updateProofingSettings(
  slug: string,
  update: ProofingSettingsUpdate,
  signal?: AbortSignal,
): Promise<ProofingSettings> {
  return request(`${base(slug)}/settings`, {
    schema: proofingSettingsSchema,
    method: "PATCH",
    body: update,
    signal,
  });
}

export function saveProofingBranding(
  slug: string,
  input: ProofingBrandingInput,
  signal?: AbortSignal,
): Promise<ProofingSettings> {
  const form = new FormData();
  form.set("displayName", input.displayName);
  form.set("primaryColor", input.primaryColor);
  form.set("secondaryColor", input.secondaryColor);
  for (const field of PROOFING_THEME_COLOR_FIELDS) {
    form.set(field, input[field]);
  }
  form.set("fontFamily", input.fontFamily);
  form.set("supportContact", input.supportContact);
  form.set("privacyUrl", input.privacyUrl);
  form.set("hidePoweredBy", String(input.hidePoweredBy));
  setLogoFields(form, input);
  return request(`${base(slug)}/settings/branding`, {
    schema: proofingSettingsSchema,
    method: "PUT",
    body: form,
    signal,
  });
}

function setLogoFields(
  form: FormData,
  input: { logo?: File; removeLogo?: boolean },
): void {
  if (input.logo) {
    form.set("logo", input.logo);
  } else if (input.removeLogo) {
    form.set("removeLogo", "true");
  }
}

export function getProofingFlowBranding(
  slug: string,
  flowId: string,
  signal?: AbortSignal,
): Promise<ProofingFlowBranding> {
  return request(`${flowBase(slug, flowId)}/branding`, {
    schema: proofingFlowBrandingSchema,
    signal,
  });
}

export function saveProofingFlowBranding(
  slug: string,
  flowId: string,
  input: ProofingFlowBrandingInput,
  signal?: AbortSignal,
): Promise<ProofingFlowBranding> {
  const form = new FormData();
  form.set("custom", String(input.custom));
  form.set("displayName", input.displayName);
  form.set("primaryColor", input.primaryColor);
  form.set("supportContact", input.supportContact);
  form.set("privacyUrl", input.privacyUrl);
  if (input.custom) {
    setLogoFields(form, input);
  }
  return request(`${flowBase(slug, flowId)}/branding`, {
    schema: proofingFlowBrandingSchema,
    method: "PUT",
    body: form,
    signal,
  });
}

export function getProofingApiKeys(
  slug: string,
  signal?: AbortSignal,
): Promise<ProofingApiKey[]> {
  return request(`${base(slug)}/api-keys`, {
    schema: z.array(proofingApiKeySchema),
    signal,
  });
}

export function createProofingApiKey(
  slug: string,
  name: string,
  signal?: AbortSignal,
): Promise<CreatedProofingApiKey> {
  return request(`${base(slug)}/api-keys`, {
    schema: createdProofingApiKeySchema,
    method: "POST",
    body: { name },
    signal,
  });
}

export function revokeProofingApiKey(
  slug: string,
  keyId: string,
  signal?: AbortSignal,
): Promise<ProofingApiKey> {
  return request(`${base(slug)}/api-keys/${encodeURIComponent(keyId)}`, {
    schema: proofingApiKeySchema,
    method: "DELETE",
    signal,
  });
}

function webhookBase(slug: string): string {
  return `${base(slug)}/webhook`;
}

export function getProofingWebhook(
  slug: string,
  signal?: AbortSignal,
): Promise<ProofingWebhook> {
  return request(webhookBase(slug), {
    schema: proofingWebhookSchema,
    signal,
  });
}

export function saveProofingWebhook(
  slug: string,
  input: { url: string; events: string[] },
  signal?: AbortSignal,
): Promise<ProofingWebhook> {
  return request(webhookBase(slug), {
    schema: proofingWebhookSchema,
    method: "PUT",
    body: input,
    signal,
  });
}

export function removeProofingWebhook(
  slug: string,
  signal?: AbortSignal,
): Promise<void> {
  return request(webhookBase(slug), {
    schema: z.void(),
    method: "DELETE",
    signal,
  });
}

export function rotateProofingWebhookSecret(
  slug: string,
  signal?: AbortSignal,
): Promise<ProofingWebhook> {
  return request(`${webhookBase(slug)}/rotate-secret`, {
    schema: proofingWebhookSchema,
    method: "POST",
    body: {},
    signal,
  });
}

export function sendProofingWebhookTest(
  slug: string,
  signal?: AbortSignal,
): Promise<void> {
  return request(`${webhookBase(slug)}/test`, {
    schema: z.void(),
    method: "POST",
    body: {},
    signal,
  });
}

export function getProofingWebhookDeliveries(
  slug: string,
  signal?: AbortSignal,
): Promise<WebhookDelivery[]> {
  return request(`${webhookBase(slug)}/deliveries`, {
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
// A settled external request's result, as an admin reads it in the wallet:
// the identity, the document's photo and the selfie only for an approval.
// Every read is audited.
const proofingResultSchema = z.object({
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
      // Only whether the holder is age or older, for a flow that asks it.
      ageOver: z.object({ age: z.number(), over: z.boolean() }).optional(),
    })
    .optional(),
  evidence: z.array(
    z.object({
      type: z.string(),
      documentType: z.string().optional(),
      documentKind: z.string().optional(),
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
  // The sender's own photo the selfie was matched against, for a flow
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

// A data request's matches: the org's sessions whose proofed identity is
// the person's, strong when proven with the same document, probable on name
// and date of birth alone. approved is the reviewer's choice, once decided.
const proofingDataMatchSchema = z.object({
  requestId: z.string(),
  sessionId: z.string(),
  flowName: z.string(),
  status: z.string(),
  method: z.string().optional(),
  assuranceLevel: z.string().optional(),
  eidasLevel: z.string().optional(),
  createdAt: z.string(),
  completedAt: z.string().optional(),
  purgedAt: z.string().optional(),
  // email and name: an unfinished session sent to the same address, or with
  // the person's name typed in, with no proofed identity to match on; never
  // ticked by default.
  // An unknown level reads as the weakest, never ticked by default.
  level: z.enum(["strong", "probable", "email", "name"]).catch("name"),
  approved: z.boolean().optional(),
  // When the org's legal retention lets an erasure take the session; absent
  // when none applies or it passed.
  heldUntil: z.string().optional(),
});

export type ProofingDataMatch = z.infer<typeof proofingDataMatchSchema>;

const proofingDataMatchesSchema = z.object({
  flowKind: flowKindSchema,
  matches: z.array(proofingDataMatchSchema),
});

export function getProofingDataMatches(
  slug: string,
  requestId: string,
  signal?: AbortSignal,
): Promise<ProofingDataMatch[]> {
  return request(
    `${base(slug)}/requests/${encodeURIComponent(requestId)}/data-matches`,
    { schema: proofingDataMatchesSchema, signal },
  ).then((r) => r.matches);
}

// An approved "see my data" request's data, as a JSON attachment the browser
// downloads: for an admin to hand the person, or for the person on their link.
export function proofingDataExportUrl(slug: string, requestId: string): string {
  return absoluteApiUrl(
    `${base(slug)}/requests/${encodeURIComponent(requestId)}/data-export`,
  );
}

export function hostedDataExportUrl(token: string): string {
  return absoluteApiUrl(`${verifyPath({ kind: "hosted", token })}/data-export`);
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
const proofingYiviStartSchema = z.object({
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
  // A hosted data request's: until when approved data downloads, and how many
  // sessions an approved erasure kept.
  dataExportUntil?: string;
  dataMatchesKept?: number;
  // Until when the subject can enter their address, once approved and while
  // they have not.
  addressUntil?: string;
  // What the law kept of an erasure, on a hosted link.
  legalRetention?: HostedLegalRetention;
  // A hosted link's: whether its session was started, and the lifetime it
  // was created with.
  started?: boolean;
  sessionTtlSeconds?: number;
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
const proofingClaimLinkSchema = z.object({
  deepLink: z.string(),
  expiresAt: z.string(),
});

export type ProofingClaimLink = z.infer<typeof proofingClaimLinkSchema>;

// Where a running on-screen Idem request's phone is: no phone scanned yet, the
// app holds the session, or it was closed and a claim link hands it over.
const proofingAppSchema = z.object({
  app: z.enum(["waiting", "connected", "away"]).catch("waiting"),
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

// done is false while the subject has not finished in the Yivi app. Once done,
// ok true approved the session, ok false rejected it and code says why.
const proofingYiviDisclosureSchema = z.object({
  done: z.boolean(),
  ok: z.boolean(),
  code: z.string().optional(),
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

const hostedLegalRetentionSchema = z.object({
  // How many of the person's sessions the law holds.
  sessions: z.number(),
  until: z.string(),
  reason: z.object({ en: z.string(), nl: z.string() }),
});

export type HostedLegalRetention = z.infer<typeof hostedLegalRetentionSchema>;

// A hosted link's progress: its status and, until it settles, when the link
// (not started) or its session (started) ends.
const hostedProgressSchema = z.object({
  status: z.string(),
  errorCode: z.string().optional(),
  method: z.string().optional(),
  linkExpiresAt: z.string(),
  started: z.boolean(),
  // The lifetime the started session was created with, which the page names
  // when it ends unfinished; absent before a start, and for a session started
  // before the wallet kept it.
  sessionTtlSeconds: z.number().optional(),
  // Until when the subject can add a diploma extract; absent while they cannot.
  diplomasUntil: z.string().optional(),
  // Until when the subject can enter their address; absent while they cannot,
  // and once they did (addressGiven).
  addressUntil: z.string().optional(),
  addressGiven: z.boolean(),
  // Until when an approved "see my data" request's data downloads.
  dataExportUntil: z.string().optional(),
  // How many sessions an approved erasure kept, not deleted.
  dataMatchesKept: z.number().optional(),
  // What the org's legal retention kept of a "delete my data" request: it
  // declined the request, or an approved one left those sessions. Its reason
  // in each language, and when all of it can go.
  legalRetention: hostedLegalRetentionSchema.optional(),
});

export type HostedProgress = z.infer<typeof hostedProgressSchema>;

// The hosted page: who asks, what the flow collects, the progress, and how
// the page hands its subject back once settled.
const hostedThemeSchema = z.object({
  primaryColor: z.string(),
  secondaryColor: z.string(),
  textColor: z.string(),
  surfaceColor: z.string(),
  borderColor: z.string(),
  linkColor: z.string(),
  successColor: z.string(),
  warningColor: z.string(),
  errorColor: z.string(),
  headerColor: z.string(),
  fontFamily: z.string(),
});

export type HostedTheme = z.infer<typeof hostedThemeSchema>;

const hostedProofingSchema = hostedProgressSchema.extend({
  // The API's id for the session, handed back on completion.
  sessionId: z.string(),
  // Absent shows the page's own done screen.
  redirectUrl: z.string().optional(),
  // The only origins the page posts its completion message to.
  embedOrigins: z.array(z.string()),
  // en or nl; absent leaves the browser's.
  language: z.string().optional(),
  // The languages the flow's page offers; empty is every one.
  locales: z.array(z.string()),
  organization: z.object({
    name: z.string(),
    branding: proofingBrandingSchema,
    dataRetentionDays: z.number(),
  }),
  // What the person is asked for: no flow name or assurance level, which are
  // the org's internal labels.
  flow: z.object({
    requestedAttributes: z.array(z.string()),
    // Empty for a flow saved without reasons.
    attributeReasons: attributeReasonsSchema,
    yiviAvailable: z.boolean(),
    diplomaMode: diplomaModeSchema,
    addressMode: addressModeSchema,
    kind: flowKindSchema,
    // The most days the session's data is kept, as the page tells the subject.
    retentionDays: z.number(),
  }),
  // The diploma extracts the subject added.
  diplomas: z.array(hostedDiplomaSchema),
  // The look the page takes, the one the Idem app shows the session in: the
  // org's proofing look when it has one, else its theme. "" keeps the page's
  // own.
  theme: hostedThemeSchema.optional(),
});

export type HostedProofing = z.infer<typeof hostedProofingSchema>;

const hostedStartSchema = hostedProgressSchema.extend({
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
const diplomaVerdictSchema = z.object({
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

// Every Dutch address at a postal code and house number typed on a verify
// page, while it takes the address. Not retried: the next keystroke asks again.
export function lookUpAddress(
  target: VerifyTarget,
  postalCode: string,
  houseNumber: string,
  signal?: AbortSignal,
): Promise<RegisterAddress[]> {
  const params = new URLSearchParams({ postalCode, houseNumber });
  return request(`${verifyPath(target)}/address/lookup?${params.toString()}`, {
    schema: z.object({ addresses: z.array(registerAddressSchema) }),
    maxRetries: 0,
    signal,
  }).then((r) => r.addresses);
}

// Keeps the postal address entered on a verify page, after an approved
// identity; the backend answers 204.
export async function submitProofingAddress(
  target: VerifyTarget,
  address: ProofingAddressInput,
  signal?: AbortSignal,
): Promise<void> {
  await request(`${verifyPath(target)}/address`, {
    schema: z.unknown(),
    method: "POST",
    body: address,
    signal,
  });
}

// An administrator's decision on a request under review; reason is required.
// errorCode names what a reviewer found wrong (REJECT_REASONS;
// MANUAL_REVIEW_REJECTED when absent).
// requestIds are a data request's matched sessions an approval takes (every
// match when absent).
export interface ProofingReviewInput {
  decision: "approve" | "reject";
  reason: string;
  errorCode?: string;
  requestIds?: string[];
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
