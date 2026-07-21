import { z } from "zod";
import { request } from "./http";

// The value types an attribute may declare, mirroring the backend's
// SupportedAttributeTypes allow-list. The schema editor offers these as a
// dropdown; the backend rejects anything else on write.
export const SUPPORTED_ATTRIBUTE_TYPES = [
  "string",
  "integer",
  "number",
  "boolean",
  "date",
] as const;

export type AttestationAttributeType =
  (typeof SUPPORTED_ATTRIBUTE_TYPES)[number];

// One entry of an SD-JWT VC type metadata `display` array: a BCP-47 language
// tag paired with the text a wallet shows for that language. The credential
// display carries a `name`; a claim's display carries a `label`.
export const localizedNameSchema = z.object({
  lang: z.string(),
  name: z.string(),
});

export type LocalizedName = z.infer<typeof localizedNameSchema>;

export const localizedLabelSchema = z.object({
  lang: z.string(),
  label: z.string(),
});

export type LocalizedLabel = z.infer<typeof localizedLabelSchema>;

// A single attribute declared by an attestation schema: the disclosure key, a
// human label, the value type, whether it must be present when issuing, and
// optional per-language labels.
export const attestationAttributeSchema = z.object({
  key: z.string(),
  label: z.string(),
  type: z.string(),
  required: z.boolean(),
  display: z.array(localizedLabelSchema).optional(),
});

export type AttestationAttribute = z.infer<typeof attestationAttributeSchema>;

// Whether a schema/template describes a credential about a natural person or an
// organization. Drives how the issue wizard collects a recipient and how the
// credential is delivered (e-mail vs. QERDS).
export const attestationSubjectTypeSchema = z.enum([
  "natural_person",
  "organization",
]);

export type AttestationSubjectType = z.infer<
  typeof attestationSubjectTypeSchema
>;

// A credential schema: the shape of an attestation (its VCT + attributes),
// independent of any issuance defaults.
export const attestationSchemaSchema = z.object({
  id: z.string(),
  organizationId: z.string(),
  vct: z.string(),
  displayName: z.string(),
  credentialConfigId: z.string(),
  attributes: z.array(attestationAttributeSchema),
  display: z.array(localizedNameSchema).optional(),
  subjectType: attestationSubjectTypeSchema,
  qualified: z.boolean(),
  status: z.string(),
  createdAt: z.string(),
  updatedAt: z.string(),
});

export type AttestationSchema = z.infer<typeof attestationSchemaSchema>;

const attestationSchemaListSchema = z.array(attestationSchemaSchema);

// A template pairs a schema with issuance defaults (validity, key material,
// prefilled attributes) and is enriched server-side with the schema's fields.
export const attestationTemplateSchema = z.object({
  id: z.string(),
  organizationId: z.string(),
  schemaId: z.string(),
  name: z.string(),
  defaultAttributes: z.record(z.string(), z.string()).optional(),
  validitySeconds: z.number().optional(),
  keyMaterialId: z.string().optional(),
  status: z.string(),
  createdAt: z.string(),
  updatedAt: z.string(),
  vct: z.string(),
  displayName: z.string(),
  attributes: z.array(attestationAttributeSchema),
  subjectType: attestationSubjectTypeSchema,
  qualified: z.boolean(),
  issuedCount: z.number(),
});

export type AttestationTemplate = z.infer<typeof attestationTemplateSchema>;

const attestationTemplateListSchema = z.array(attestationTemplateSchema);

// Signing key material used to issue attestations: either wallet-managed or a
// qualified certificate held by a provider.
export const attestationKeySchema = z.object({
  id: z.string(),
  organizationId: z.string(),
  kind: z.string(),
  label: z.string(),
  providerRef: z.string().optional(),
  status: z.string(),
  createdAt: z.string(),
  updatedAt: z.string(),
});

export type AttestationKey = z.infer<typeof attestationKeySchema>;

const attestationKeyListSchema = z.array(attestationKeySchema);

// A ledger entry for an issued attestation, reconciled server-side on read.
export const issuedAttestationSchema = z.object({
  id: z.string(),
  organizationId: z.string(),
  templateId: z.string().optional(),
  schemaVct: z.string(),
  recipientKind: z.string(),
  recipientUserId: z.string().optional(),
  recipientRef: z.string(),
  attributes: z.record(z.string(), z.string()),
  qualified: z.boolean(),
  status: z.string(),
  issuedByUserId: z.string().optional(),
  claimedAt: z.string().optional(),
  expiresAt: z.string().optional(),
  revokedAt: z.string().optional(),
  createdAt: z.string(),
  updatedAt: z.string(),
});

export type IssuedAttestation = z.infer<typeof issuedAttestationSchema>;

const issuedAttestationListSchema = z.array(issuedAttestationSchema);

// A credential the organization HOLDS (the "Received" facet). The claims live in
// the holder engine; this is the thin org-scoped index over it.
export const heldAttestationSchema = z.object({
  id: z.string(),
  organizationId: z.string(),
  credentialRef: z.string(),
  vct: z.string(),
  issuer: z.string(),
  source: z.enum(["qerds", "openid4vci", "bootstrap"]),
  sourceMessageId: z.string().optional(),
  receivedAt: z.string(),
  createdAt: z.string(),
});

export type HeldAttestation = z.infer<typeof heldAttestationSchema>;

const heldAttestationListSchema = z.array(heldAttestationSchema);

// One disclosed attribute of a held credential: its payload key, the issuer
// metadata display label (empty when the credential carries no label — the UI
// falls back to the key), and the value (any JSON type, rendered generically).
export const heldAttributeSchema = z.object({
  key: z.string(),
  label: z.string(),
  value: z.unknown(),
});

export type HeldAttribute = z.infer<typeof heldAttributeSchema>;

// The detail view of a held credential: its index metadata plus the disclosed
// attributes read from the holder engine, display-ordered and labelled server-side.
export const heldAttestationClaimsSchema = z.object({
  id: z.string(),
  vct: z.string(),
  issuer: z.string(),
  issuerName: z.string(),
  source: z.string(),
  receivedAt: z.string(),
  attributes: z.array(heldAttributeSchema),
});

export type HeldAttestationClaims = z.infer<typeof heldAttestationClaimsSchema>;

// The response to a POST issue: the ledger entry plus the wallet offer link
// (and an optional transaction code the recipient must enter).
export const issueResultSchema = issuedAttestationSchema.extend({
  offerUri: z.string(),
  txCode: z.string().optional(),
});

export type IssueResult = z.infer<typeof issueResultSchema>;

// The public view of an offered attestation, keyed by an opaque claim token.
// Served without a slug or authentication so a recipient can claim via a link.
export const attestationClaimSchema = z.object({
  status: z.string(),
  offerUri: z.string(),
  txCode: z.string().optional(),
  organizationName: z.string(),
  credentialName: z.string(),
});

export type AttestationClaim = z.infer<typeof attestationClaimSchema>;

export interface AttestationSchemaInput {
  vct: string;
  displayName: string;
  credentialConfigId: string;
  attributes: AttestationAttribute[];
  display?: LocalizedName[];
  subjectType: AttestationSubjectType;
  qualified: boolean;
  status?: string;
}

export interface AttestationSchemaUpdate {
  displayName: string;
  credentialConfigId: string;
  attributes: AttestationAttribute[];
  display?: LocalizedName[];
  subjectType: AttestationSubjectType;
  qualified: boolean;
  status: string;
}

export interface AttestationTemplateInput {
  schemaId: string;
  name: string;
  defaultAttributes?: Record<string, string>;
  validitySeconds?: number;
  keyMaterialId?: string;
}

export interface AttestationTemplateUpdate {
  name: string;
  defaultAttributes?: Record<string, string>;
  validitySeconds?: number;
  keyMaterialId?: string;
  status: string;
}

export interface AttestationKeyInput {
  kind: string;
  label: string;
  providerRef?: string;
}

export interface IssueAttestationInput {
  templateId: string;
  recipient: { kind: string; userId?: string; ref: string };
  attributes: Record<string, string>;
}

export function getHeldAttestations(
  slug: string,
  signal?: AbortSignal,
): Promise<HeldAttestation[]> {
  return request(`${base(slug)}/held`, {
    schema: heldAttestationListSchema,
    signal,
  });
}

export function getHeldAttestationClaims(
  slug: string,
  heldId: string,
  signal?: AbortSignal,
): Promise<HeldAttestationClaims> {
  return request(`${base(slug)}/held/${encodeURIComponent(heldId)}/claims`, {
    schema: heldAttestationClaimsSchema,
    signal,
  });
}

export function deleteHeldAttestation(
  slug: string,
  heldId: string,
  signal?: AbortSignal,
): Promise<void> {
  return request(`${base(slug)}/held/${encodeURIComponent(heldId)}`, {
    schema: z.void(),
    method: "DELETE",
    signal,
  });
}

function base(slug: string): string {
  return `/api/v1/orgs/${encodeURIComponent(slug)}/attestations`;
}

export function getAttestationSchemas(
  slug: string,
  signal?: AbortSignal,
): Promise<AttestationSchema[]> {
  return request(`${base(slug)}/schemas`, {
    schema: attestationSchemaListSchema,
    signal,
  });
}

export function getAttestationSchema(
  slug: string,
  schemaId: string,
  signal?: AbortSignal,
): Promise<AttestationSchema> {
  return request(`${base(slug)}/schemas/${encodeURIComponent(schemaId)}`, {
    schema: attestationSchemaSchema,
    signal,
  });
}

export function createAttestationSchema(
  slug: string,
  input: AttestationSchemaInput,
  signal?: AbortSignal,
): Promise<AttestationSchema> {
  return request(`${base(slug)}/schemas`, {
    schema: attestationSchemaSchema,
    method: "POST",
    body: input,
    signal,
  });
}

export function updateAttestationSchema(
  slug: string,
  schemaId: string,
  input: AttestationSchemaUpdate,
  signal?: AbortSignal,
): Promise<AttestationSchema> {
  return request(`${base(slug)}/schemas/${encodeURIComponent(schemaId)}`, {
    schema: attestationSchemaSchema,
    method: "PATCH",
    body: input,
    signal,
  });
}

// The Veramo issuer GitOps config generated from a schema: the metadata fragment
// (keyed by credential config id, merged into conf/metadata/<instance>.json) and
// a VCT document. Committing this to the issuer's ops repo is what makes the
// schema's translations show in a wallet — the issuer's runtime config API is
// disabled in the deployment, so display is provisioned by files. The inner
// documents are opaque here (passed through verbatim for the operator to copy).
export const attestationIssuerConfigSchema = z.object({
  credentialConfigId: z.string(),
  metadata: z.record(z.string(), z.unknown()),
  vct: z.unknown(),
});

export type AttestationIssuerConfig = z.infer<
  typeof attestationIssuerConfigSchema
>;

export function getAttestationSchemaIssuerConfig(
  slug: string,
  schemaId: string,
  signal?: AbortSignal,
): Promise<AttestationIssuerConfig> {
  return request(
    `${base(slug)}/schemas/${encodeURIComponent(schemaId)}/issuer-config`,
    {
      schema: attestationIssuerConfigSchema,
      signal,
    },
  );
}

export function deleteAttestationSchema(
  slug: string,
  schemaId: string,
  signal?: AbortSignal,
): Promise<void> {
  return request(`${base(slug)}/schemas/${encodeURIComponent(schemaId)}`, {
    schema: z.void(),
    method: "DELETE",
    signal,
  });
}

export function getAttestationTemplates(
  slug: string,
  signal?: AbortSignal,
): Promise<AttestationTemplate[]> {
  return request(`${base(slug)}/templates`, {
    schema: attestationTemplateListSchema,
    signal,
  });
}

export function getAttestationTemplate(
  slug: string,
  templateId: string,
  signal?: AbortSignal,
): Promise<AttestationTemplate> {
  return request(`${base(slug)}/templates/${encodeURIComponent(templateId)}`, {
    schema: attestationTemplateSchema,
    signal,
  });
}

export function createAttestationTemplate(
  slug: string,
  input: AttestationTemplateInput,
  signal?: AbortSignal,
): Promise<AttestationTemplate> {
  return request(`${base(slug)}/templates`, {
    schema: attestationTemplateSchema,
    method: "POST",
    body: input,
    signal,
  });
}

export function updateAttestationTemplate(
  slug: string,
  templateId: string,
  input: AttestationTemplateUpdate,
  signal?: AbortSignal,
): Promise<AttestationTemplate> {
  return request(`${base(slug)}/templates/${encodeURIComponent(templateId)}`, {
    schema: attestationTemplateSchema,
    method: "PATCH",
    body: input,
    signal,
  });
}

export function deleteAttestationTemplate(
  slug: string,
  templateId: string,
  signal?: AbortSignal,
): Promise<void> {
  return request(`${base(slug)}/templates/${encodeURIComponent(templateId)}`, {
    schema: z.void(),
    method: "DELETE",
    signal,
  });
}

export function getAttestationKeys(
  slug: string,
  signal?: AbortSignal,
): Promise<AttestationKey[]> {
  return request(`${base(slug)}/keys`, {
    schema: attestationKeyListSchema,
    signal,
  });
}

export function createAttestationKey(
  slug: string,
  input: AttestationKeyInput,
  signal?: AbortSignal,
): Promise<AttestationKey> {
  return request(`${base(slug)}/keys`, {
    schema: attestationKeySchema,
    method: "POST",
    body: input,
    signal,
  });
}

export function suspendAttestationKey(
  slug: string,
  keyId: string,
  signal?: AbortSignal,
): Promise<AttestationKey> {
  return request(`${base(slug)}/keys/${encodeURIComponent(keyId)}/suspend`, {
    schema: attestationKeySchema,
    method: "POST",
    signal,
  });
}

export function revokeAttestationKey(
  slug: string,
  keyId: string,
  signal?: AbortSignal,
): Promise<AttestationKey> {
  return request(`${base(slug)}/keys/${encodeURIComponent(keyId)}/revoke`, {
    schema: attestationKeySchema,
    method: "POST",
    signal,
  });
}

export function getIssuedAttestations(
  slug: string,
  signal?: AbortSignal,
): Promise<IssuedAttestation[]> {
  return request(base(slug), {
    schema: issuedAttestationListSchema,
    signal,
  });
}

export function getIssuedAttestation(
  slug: string,
  issuedId: string,
  signal?: AbortSignal,
): Promise<IssuedAttestation> {
  return request(`${base(slug)}/${encodeURIComponent(issuedId)}`, {
    schema: issuedAttestationSchema,
    signal,
  });
}

export function issueAttestation(
  slug: string,
  input: IssueAttestationInput,
  signal?: AbortSignal,
): Promise<IssueResult> {
  return request(base(slug), {
    schema: issueResultSchema,
    method: "POST",
    body: input,
    signal,
  });
}

export function revokeIssuedAttestation(
  slug: string,
  issuedId: string,
  signal?: AbortSignal,
): Promise<IssuedAttestation> {
  return request(`${base(slug)}/${encodeURIComponent(issuedId)}/revoke`, {
    schema: issuedAttestationSchema,
    method: "POST",
    signal,
  });
}

// Public, unauthenticated: fetches an offered attestation by its claim token.
export function getAttestationClaim(
  token: string,
  signal?: AbortSignal,
): Promise<AttestationClaim> {
  return request(`/api/v1/attestations/claim/${encodeURIComponent(token)}`, {
    schema: attestationClaimSchema,
    signal,
  });
}
