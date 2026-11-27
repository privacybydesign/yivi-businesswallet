import { z } from "zod";
import { request } from "./http";

// The wallet unit the Business Wallet Provider attests (WE BUILD CS-05). A
// revoked unit makes every attestation issued to it read invalid.
export const WALLET_UNIT_REVOKE_REASONS = [
  "owner_request",
  "key_compromise",
] as const;

export type WalletUnitRevokeReason =
  (typeof WALLET_UNIT_REVOKE_REASONS)[number];

// The backend sets "cessation" when the owner's EBWOID is revoked, and
// "provider_delisted" while we as provider are off the trusted list; an admin
// chooses neither. A cessation unit is never replaced, a delisted one is replaced
// by the backend once we are back on the list.
export const WALLET_UNIT_REVOKED_REASONS = [
  ...WALLET_UNIT_REVOKE_REASONS,
  "cessation",
  "provider_delisted",
] as const;

export const walletUnitSchema = z.object({
  id: z.string(),
  status: z.enum(["active", "revoked"]),
  revokedReason: z.enum(WALLET_UNIT_REVOKED_REASONS).optional(),
  revokedAt: z.string().optional(),
  createdAt: z.string(),
});

export type WalletUnit = z.infer<typeof walletUnitSchema>;

// issued is false until the first attestation; summary is then absent.
export const walletUnitViewSchema = z.object({
  issued: z.boolean(),
  summary: z
    .object({
      unit: walletUnitSchema,
      issuedCount: z.number(),
      lastIssuedAt: z.string().optional(),
    })
    .optional(),
  // Set only when the org's holder keys get a key attestation (SKA): the
  // key storage levels it states.
  keyAttestation: z.object({ levels: z.array(z.string()) }).optional(),
});

export type WalletUnitView = z.infer<typeof walletUnitViewSchema>;

function base(slug: string): string {
  return `/api/v1/orgs/${encodeURIComponent(slug)}/wallet-unit`;
}

export function getWalletUnit(
  slug: string,
  signal?: AbortSignal,
): Promise<WalletUnitView> {
  return request(base(slug), { schema: walletUnitViewSchema, signal });
}

export function revokeWalletUnit(
  slug: string,
  reason: WalletUnitRevokeReason,
  signal?: AbortSignal,
): Promise<WalletUnit> {
  return request(`${base(slug)}/revoke`, {
    schema: walletUnitSchema,
    method: "POST",
    body: { reason },
    signal,
  });
}

export function activateWalletUnit(
  slug: string,
  signal?: AbortSignal,
): Promise<WalletUnit> {
  return request(`${base(slug)}/activate`, {
    schema: walletUnitSchema,
    method: "POST",
    signal,
  });
}
