import { z } from "zod";
import { ApiError, request, requestBlob } from "./http";

// The qualified-document-signing ceremony: the business wallet is the RP-centric
// Signature Creation Application. A one-time "link credential" step caches the
// signing certificate, then each document is signed via an OID4VP + CSC ceremony
// that hands the browser off to the QTSP authorization server and returns here.

const NOT_FOUND_STATUS = 404;

export const linkedCredentialSchema = z.object({
  credentialId: z.string(),
  keyAlgo: z.string(),
  updatedAt: z.string(),
});
export type LinkedCredential = z.infer<typeof linkedCredentialSchema>;

// Request status strings mirror the backend (internal/signing).
export const SIGNING_STATUS = {
  awaitingAuthorization: "awaiting_authorization",
  completed: "completed",
  failed: "failed",
} as const;

export const signingRequestSchema = z.object({
  id: z.string(),
  status: z.string(),
  filename: z.string(),
  credentialId: z.string(),
  error: z.string().optional(),
  createdAt: z.string(),
  completedAt: z.string().optional(),
});
export type SigningRequest = z.infer<typeof signingRequestSchema>;

// StartLink/StartSign both return the URL to send the browser to; signing also
// returns the created request id so the page can track it on return.
export const signingStartSchema = z.object({
  requestId: z.string().optional(),
  authorizeUrl: z.string(),
});
export type SigningStart = z.infer<typeof signingStartSchema>;

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

export function createSigningRequest(
  slug: string,
  document: File,
): Promise<SigningStart> {
  const form = new FormData();
  form.append("document", document);
  return request(`${base(slug)}/requests`, {
    schema: signingStartSchema,
    method: "POST",
    body: form,
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
