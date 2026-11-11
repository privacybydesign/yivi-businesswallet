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

export const walletUnitSchema = z.object({
  id: z.string(),
  status: z.enum(["active", "revoked"]),
  revokedReason: z.enum(WALLET_UNIT_REVOKE_REASONS).optional(),
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
