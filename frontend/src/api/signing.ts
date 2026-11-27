import { z } from "zod";
import { ApiError, absoluteApiUrl, request, requestBlob } from "./http";
import { callbackPath, ceremonyEnd } from "../lib/signing-authorization-start";
import type { CeremonyEnd } from "../lib/signing-authorization-start";

// The co-signing workflow: the business wallet is the RP-centric Signature
// Creation Application. Each signer links a signing credential once (a member in
// the "My credential" tab, an external signee behind their invitation link), then
// a request routes a PDF to its signers; each signs via an OID4VP + CSC ceremony
// that hands the browser off to the signing provider's authorization server (the
// built-in service's own page, or a remote QTSP) and returns here. When all have signed, the document is delivered to the recipient over QERDS
// or email.

const NOT_FOUND_STATUS = 404;

export const linkedCredentialSchema = z.object({
  credentialId: z.string(),
  keyAlgo: z.string(),
  updatedAt: z.string(),
});
export type LinkedCredential = z.infer<typeof linkedCredentialSchema>;

// Request statuses mirror the backend (internal/signing).
export const SIGNING_STATUS = {
  awaitingSignatures: "awaiting_signatures",
  completed: "completed",
  failed: "failed",
  declined: "declined",
} as const;

// Signing modes, recipient channels, per-signer and delivery statuses — all mirror
// the backend. Kept as plain string maps (not zod enums) so a new backend value
// never breaks response parsing.
export const SIGNING_MODE = {
  parallel: "parallel",
  sequential: "sequential",
} as const;
export type SigningMode = (typeof SIGNING_MODE)[keyof typeof SIGNING_MODE];

export const RECIPIENT_CHANNEL = {
  none: "none",
  // Mails the finished document to everyone who signed it; names no recipient.
  signers: "signers",
  qerds: "qerds",
  email: "email",
} as const;
export type RecipientChannel =
  (typeof RECIPIENT_CHANNEL)[keyof typeof RECIPIENT_CHANNEL];

export const SIGNER_STATUS = {
  pending: "pending",
  signed: "signed",
  failed: "failed",
  declined: "declined",
} as const;

export const DELIVERY_STATUS = {
  notRequested: "not_requested",
  pending: "pending",
  delivered: "delivered",
  failed: "failed",
} as const;

// A signer is either an internal org member or an external signee; only the first
// has a userId, so the signer row's own id is what identifies either.
export const SIGNER_KIND = {
  internal: "internal",
  external: "external",
} as const;
export type SignerKind = (typeof SIGNER_KIND)[keyof typeof SIGNER_KIND];

// Where a signer's visible marks land. A signature placement becomes the visible
// appearance of that signer's own PAdES signature (so there is at most one); a paraph
// is their initials on one page, and "on every page" is sent as one placement per
// page. The rectangle is in PDF user-space points with the origin at the page's
// bottom-left — the space the backend stores and stamps in.
export const PLACEMENT_KIND = {
  signature: "signature",
  paraph: "paraph",
} as const;
export type PlacementKind =
  (typeof PLACEMENT_KIND)[keyof typeof PLACEMENT_KIND];

// How a request was made: by a member in the wallet, or by the organization's
// own software over the signing API. An API request has no creator.
export const CREATED_VIA = {
  member: "member",
  api: "api",
} as const;

export const placementSchema = z.object({
  kind: z.string(),
  page: z.number(),
  x: z.number(),
  y: z.number(),
  width: z.number(),
  height: z.number(),
});
export type Placement = Omit<z.infer<typeof placementSchema>, "kind"> & {
  kind: PlacementKind;
};

export const signerSchema = z.object({
  id: z.string(),
  kind: z.string(),
  userId: z.string().optional(),
  name: z.string(),
  email: z.string(),
  order: z.number(),
  status: z.string(),
  placements: z.array(placementSchema).nullable().default([]),
  signedAt: z.string().optional(),
  declinedAt: z.string().optional(),
  declineReason: z.string().optional(),
});
export type Signer = z.infer<typeof signerSchema>;

export const signingRequestSchema = z.object({
  id: z.string(),
  status: z.string(),
  filename: z.string(),
  mode: z.string(),
  createdBy: z.string(),
  createdByName: z.string(),
  createdVia: z.string().default(CREATED_VIA.member),
  // For an API request: the site its signees are sent back to when done.
  returnUrl: z.string().optional(),
  recipientChannel: z.string(),
  recipientName: z.string().optional(),
  recipientAddress: z.string().optional(),
  message: z.string().optional(),
  deliveryStatus: z.string(),
  deliveryError: z.string().optional(),
  error: z.string().optional(),
  signers: z.array(signerSchema).nullable().default([]),
  createdAt: z.string(),
  completedAt: z.string().optional(),
});
export type SigningRequest = z.infer<typeof signingRequestSchema>;

const signingRequestListSchema = z.object({
  requests: z.array(signingRequestSchema).nullable().default([]),
});

const signingRequestPageSchema = z.object({
  requests: z.array(signingRequestSchema).nullable().default([]),
  nextCursor: z.string(),
});
export type SigningRequestPage = z.infer<typeof signingRequestPageSchema>;

// startLink / startSign both return the URL to send the browser to; signing also
// returns the request id being signed so the page can track it on return.
export const signingStartSchema = z.object({
  requestId: z.string().optional(),
  authorizeUrl: z.string(),
  // True when the authorization is the wallet's own signing service's: the page
  // shows the Yivi step itself (SigningAuthorizationDialog) instead of sending
  // the browser to authorizeUrl.
  authorizeInApp: z.boolean().optional(),
});
export type SigningStart = z.infer<typeof signingStartSchema>;

// What the ceremony's callback answers a page that asks for JSON: where it
// would have sent the browser, whose query says how the ceremony ended.
const ceremonyCallbackSchema = z.object({ redirectUrl: z.string() });

// finishSigningCeremony ends a ceremony from the page that started it, with the
// redirect of its decided authorization, and reports how it ended. It rejects
// for a redirect that is not the ceremony's callback, which the browser has to
// follow itself. The callback can be answered only once, so it is never retried.
export async function finishSigningCeremony(
  redirectUrl: string,
): Promise<CeremonyEnd> {
  const path = callbackPath(redirectUrl);
  if (path === undefined) {
    throw new Error("the redirect is not the signing ceremony's callback");
  }
  const { redirectUrl: destination } = await request(path, {
    schema: ceremonyCallbackSchema,
    maxRetries: 0,
  });
  return ceremonyEnd(destination);
}

const createdSchema = z.object({ id: z.string() });

// SignerSelection is one chosen signer on the create form. Members and external
// signees share one ordered list, because that order is the sequential signing order
// and two separate lists could not express an order across both.
export type SignerSelection =
  | {
      kind: typeof SIGNER_KIND.internal;
      userId: string;
      placements: Placement[];
    }
  | {
      kind: typeof SIGNER_KIND.external;
      email: string;
      name: string;
      placements: Placement[];
    };

// NewSigningRequest is the create-request form payload.
export interface NewSigningRequest {
  document: File;
  signers: SignerSelection[];
  mode: SigningMode;
  recipientChannel: RecipientChannel;
  recipientAddress: string;
  recipientName: string;
  message: string;
}

function base(slug: string): string {
  return `/api/v1/orgs/${encodeURIComponent(slug)}/signing`;
}

export const signingAvailabilitySchema = z.object({ available: z.boolean() });
export type SigningAvailability = z.infer<typeof signingAvailabilitySchema>;

// getSigningAvailability reports whether a signing provider is configured for the
// org. It is member-safe (unlike the admin-only CSC settings), so it gates the
// signing nav item for every member who can actually use the feature.
export function getSigningAvailability(
  slug: string,
  signal?: AbortSignal,
): Promise<SigningAvailability> {
  return request(`${base(slug)}/availability`, {
    schema: signingAvailabilitySchema,
    signal,
  });
}

// getSigningCredential returns null when no credential is linked yet (the backend
// answers 404), so "not linked" is a normal state the UI renders, not an error.
export async function getSigningCredential(
  slug: string,
  signal?: AbortSignal,
): Promise<LinkedCredential | null> {
  try {
    return await request(`${base(slug)}/credential`, {
      schema: linkedCredentialSchema,
      signal,
    });
  } catch (error) {
    if (error instanceof ApiError && error.status === NOT_FOUND_STATUS) {
      return null;
    }
    throw error;
  }
}

export function linkSigningCredential(slug: string): Promise<SigningStart> {
  return request(`${base(slug)}/credential/link`, {
    schema: signingStartSchema,
    method: "POST",
  });
}

// createSigningRequest uploads a PDF plus the selected signers, mode and recipient,
// and returns the new request id. It does not sign — signers sign later. Each signer
// goes over as its own JSON `signers` value, so one list carries both kinds in the
// chosen order.
export function createSigningRequest(
  slug: string,
  input: NewSigningRequest,
): Promise<{ id: string }> {
  const form = new FormData();
  form.append("document", input.document);
  for (const signer of input.signers)
    form.append("signers", JSON.stringify(signer));
  form.append("mode", input.mode);
  form.append("recipientChannel", input.recipientChannel);
  form.append("recipientAddress", input.recipientAddress);
  form.append("recipientName", input.recipientName);
  form.append("message", input.message);
  return request(`${base(slug)}/requests`, {
    schema: createdSchema,
    method: "POST",
    body: form,
  });
}

// startSignRequest begins the acting user's signing ceremony for a request.
export function startSignRequest(
  slug: string,
  id: string,
): Promise<SigningStart> {
  return request(`${base(slug)}/requests/${encodeURIComponent(id)}/sign`, {
    schema: signingStartSchema,
    method: "POST",
  });
}

// declineSignRequest lets a pending signer refuse to sign a request, with an
// optional reason. It stops the request outright rather than starting a
// ceremony, so there is no authorize URL to hand off to.
export function declineSignRequest(
  slug: string,
  id: string,
  reason: string,
): Promise<void> {
  return request(`${base(slug)}/requests/${encodeURIComponent(id)}/decline`, {
    schema: z.void(),
    method: "POST",
    body: { reason },
  });
}

export function getSigningRequest(
  slug: string,
  id: string,
  signal?: AbortSignal,
): Promise<SigningRequest> {
  return request(`${base(slug)}/requests/${encodeURIComponent(id)}`, {
    schema: signingRequestSchema,
    signal,
  });
}

// getPendingSigningRequests lists the documents awaiting the caller's signature.
export async function getPendingSigningRequests(
  slug: string,
  signal?: AbortSignal,
): Promise<SigningRequest[]> {
  const page = await request(`${base(slug)}/requests/pending`, {
    schema: signingRequestListSchema,
    signal,
  });
  return page.requests ?? [];
}

// listSigningRequests returns the org-wide signing-request history, cursor-paginated.
export function listSigningRequests(
  slug: string,
  cursor?: string,
  signal?: AbortSignal,
): Promise<SigningRequestPage> {
  const query = cursor ? `?cursor=${encodeURIComponent(cursor)}` : "";
  return request(`${base(slug)}/requests${query}`, {
    schema: signingRequestPageSchema,
    signal,
  });
}

// The external-signee flow. An external signee has no membership and no session, so
// every call here is keyed by the one-time invitation token from their mail; the
// backend resolves it to a single signer row and takes nothing else from the caller.
function externalBase(token: string): string {
  return `/api/v1/signing/external/${encodeURIComponent(token)}`;
}

export const externalSigningSchema = z.object({
  orgName: z.string(),
  filename: z.string(),
  signerName: z.string(),
  signerEmail: z.string(),
  message: z.string().optional(),
  status: z.string(),
  signerStatus: z.string(),
  mode: z.string(),
  signerCount: z.number(),
  signedCount: z.number(),
  hasCredential: z.boolean(),
  // The request is waiting for this signee: Sign is offered, and gets them a
  // certificate first when they have none.
  yourTurn: z.boolean().optional(),
  canSign: z.boolean(),
  createdAt: z.string(),
  // The site that sent the signee here, to offer a way back once they are
  // done; it already carries the request id and status.
  returnUrl: z.string().optional(),
});
export type ExternalSigning = z.infer<typeof externalSigningSchema>;

// The stream that tells the external signee's page when its request moves: a
// Server-Sent Events URL, for an EventSource.
export function externalSigningEventsUrl(token: string): string {
  return absoluteApiUrl(`${externalBase(token)}/events`);
}

export function getExternalSigning(
  token: string,
  signal?: AbortSignal,
): Promise<ExternalSigning> {
  return request(externalBase(token), {
    schema: externalSigningSchema,
    signal,
  });
}

// linkExternalCredential starts the external signee's one-off credential-link
// ceremony (the same wallet ceremony a member runs from the "My credential" tab).
export function linkExternalCredential(token: string): Promise<SigningStart> {
  return request(`${externalBase(token)}/credential/link`, {
    schema: signingStartSchema,
    method: "POST",
  });
}

export function startExternalSign(token: string): Promise<SigningStart> {
  return request(`${externalBase(token)}/sign`, {
    schema: signingStartSchema,
    method: "POST",
  });
}

// declineExternalSign is declineSignRequest's mirror for an external signee.
export function declineExternalSign(
  token: string,
  reason: string,
): Promise<void> {
  return request(`${externalBase(token)}/decline`, {
    schema: z.void(),
    method: "POST",
    body: { reason },
  });
}

// externalDocumentUrl is the document the signee is being asked to sign, served
// inline so they can read it in the browser before signing.
export function externalDocumentUrl(token: string): string {
  return absoluteApiUrl(`${externalBase(token)}/document`);
}

// getExternalDocument fetches that document for the viewer on the signing page.
export async function getExternalDocument(
  token: string,
  signal?: AbortSignal,
): Promise<Blob> {
  const { blob } = await requestBlob(`${externalBase(token)}/document`, {
    signal,
  });
  return blob;
}

// getSigningDocumentPreview fetches the document a member is asked to sign, as
// it stands now, for the viewer they read it in before signing.
export async function getSigningDocumentPreview(
  slug: string,
  id: string,
  signal?: AbortSignal,
): Promise<Blob> {
  const { blob } = await requestBlob(
    `${base(slug)}/requests/${encodeURIComponent(id)}/preview`,
    { signal },
  );
  return blob;
}

// downloadSignedDocument fetches the signed PDF and triggers a browser save,
// using the server-suggested filename when present.
export async function downloadSignedDocument(
  slug: string,
  id: string,
  fallbackName: string,
): Promise<void> {
  const { blob, filename } = await requestBlob(
    `${base(slug)}/requests/${encodeURIComponent(id)}/document`,
  );
  const objectUrl = URL.createObjectURL(blob);
  try {
    const link = document.createElement("a");
    link.href = objectUrl;
    link.download = filename ?? fallbackName;
    document.body.appendChild(link);
    link.click();
    link.remove();
  } finally {
    URL.revokeObjectURL(objectUrl);
  }
}
