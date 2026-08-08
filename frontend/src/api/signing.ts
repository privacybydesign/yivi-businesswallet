import { z } from "zod";
import { ApiError, request, requestBlob } from "./http";

// The qualified co-signing workflow: the business wallet is the RP-centric
// Signature Creation Application. Each member links a signing credential once (in
// the "My credential" tab), then a request routes a PDF to one or more member
// signers; each signs via an OID4VP + CSC ceremony that hands the browser off to
// the QTSP authorization server and returns here. When all have signed, the
// document is delivered to the recipient over QERDS or email.

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
  qerds: "qerds",
  email: "email",
} as const;
export type RecipientChannel =
  (typeof RECIPIENT_CHANNEL)[keyof typeof RECIPIENT_CHANNEL];

export const SIGNER_STATUS = {
  pending: "pending",
  signed: "signed",
  failed: "failed",
} as const;

export const DELIVERY_STATUS = {
  notRequested: "not_requested",
  pending: "pending",
  delivered: "delivered",
  failed: "failed",
} as const;

export const signerSchema = z.object({
  userId: z.string(),
  name: z.string(),
  email: z.string(),
  order: z.number(),
  status: z.string(),
  signedAt: z.string().optional(),
});
export type Signer = z.infer<typeof signerSchema>;

export const signingRequestSchema = z.object({
  id: z.string(),
  status: z.string(),
  filename: z.string(),
  mode: z.string(),
  createdBy: z.string(),
  createdByName: z.string(),
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
});
export type SigningStart = z.infer<typeof signingStartSchema>;

const createdSchema = z.object({ id: z.string() });

// NewSigningRequest is the create-request form payload.
export interface NewSigningRequest {
  document: File;
  signerIds: string[];
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
// and returns the new request id. It does not sign — signers sign later.
export function createSigningRequest(
  slug: string,
  input: NewSigningRequest,
): Promise<{ id: string }> {
  const form = new FormData();
  form.append("document", input.document);
  for (const id of input.signerIds) form.append("signerIds", id);
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
