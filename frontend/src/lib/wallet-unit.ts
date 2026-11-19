import type { WalletUnitView } from "../api/wallet-unit";

export type WalletUnitState = "notIssued" | "active" | "revoked";

export const WALLET_UNIT_TONE: Record<
  WalletUnitState,
  "default" | "green" | "red"
> = {
  notIssued: "default",
  active: "green",
  revoked: "red",
};

// walletUnitState is what the badge shows for the org's latest wallet unit.
export function walletUnitState(view: WalletUnitView): WalletUnitState {
  if (!view.issued || !view.summary) return "notIssued";
  return view.summary.unit.status === "revoked" ? "revoked" : "active";
}
