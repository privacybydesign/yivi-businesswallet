import type { TFunction } from "i18next";
import type { ProofingLegalRetention } from "../api/identity-proofing";

// Mirrors maxLegalRetentionMonths and maxLegalReasonLength in
// backend/internal/proofing/legal_retention.go.
export const MAX_LEGAL_RETENTION_MONTHS = 600;
export const MAX_LEGAL_REASON_CHARS = 500;

const MONTHS_PER_YEAR = 12;

export const LEGAL_RETENTION_UNITS = ["years", "months"] as const;

export type LegalRetentionUnit = (typeof LEGAL_RETENTION_UNITS)[number];

// The form of Settings › Data and privacy: the org states its legal period in
// years or months, the API counts months. A switched-off draft keeps what was
// typed, so switching it back on restores it.
export interface LegalRetentionDraft {
  enabled: boolean;
  amount: string;
  unit: LegalRetentionUnit;
  reasonEn: string;
  reasonNl: string;
}

export type LegalRetentionField = "amount" | "reasonEn" | "reasonNl";

// A period of whole years reads in years, any other in months.
export function legalPeriod(months: number): {
  count: number;
  unit: LegalRetentionUnit;
} {
  if (months % MONTHS_PER_YEAR === 0) {
    return { count: months / MONTHS_PER_YEAR, unit: "years" };
  }
  return { count: months, unit: "months" };
}

// A period as a person reads it: "5 years", "18 months".
export function legalPeriodLabel(months: number, t: TFunction): string {
  const { count, unit } = legalPeriod(months);
  return t(`identityProofing.legalRetention.period.${unit}`, { count });
}

export function legalDraftFrom(
  legal: ProofingLegalRetention | null,
): LegalRetentionDraft {
  if (legal === null) {
    return {
      enabled: false,
      amount: "1",
      unit: "years",
      reasonEn: "",
      reasonNl: "",
    };
  }
  const { count, unit } = legalPeriod(legal.months);
  return {
    enabled: true,
    amount: String(count),
    unit,
    reasonEn: legal.reasonEn,
    reasonNl: legal.reasonNl,
  };
}

// The months a draft states, or null when the amount is not a whole number
// of 1 to MAX_LEGAL_RETENTION_MONTHS months.
export function legalDraftMonths(draft: LegalRetentionDraft): number | null {
  const amount = draft.amount.trim();
  if (!/^\d+$/.test(amount)) return null;
  const months =
    Number(amount) * (draft.unit === "years" ? MONTHS_PER_YEAR : 1);
  return months >= 1 && months <= MAX_LEGAL_RETENTION_MONTHS ? months : null;
}

function reasonInvalid(reason: string): boolean {
  const length = [...reason.trim()].length;
  return length === 0 || length > MAX_LEGAL_REASON_CHARS;
}

// The fields a switched-on draft cannot be saved with; none when it is off.
export function legalDraftInvalid(
  draft: LegalRetentionDraft,
): LegalRetentionField[] {
  if (!draft.enabled) return [];
  const invalid: LegalRetentionField[] = [];
  if (legalDraftMonths(draft) === null) invalid.push("amount");
  if (reasonInvalid(draft.reasonEn)) invalid.push("reasonEn");
  if (reasonInvalid(draft.reasonNl)) invalid.push("reasonNl");
  return invalid;
}

// What the API takes for a valid draft: the legal retention, or null to
// remove it.
export function legalDraftUpdate(
  draft: LegalRetentionDraft,
): ProofingLegalRetention | null {
  const months = draft.enabled ? legalDraftMonths(draft) : null;
  if (months === null) return null;
  return {
    months,
    reasonEn: draft.reasonEn.trim(),
    reasonNl: draft.reasonNl.trim(),
  };
}

// Whether saving the draft would change the saved legal retention.
export function legalDraftDirty(
  draft: LegalRetentionDraft,
  saved: ProofingLegalRetention | null,
): boolean {
  if (!draft.enabled) return saved !== null;
  const months = legalDraftMonths(draft);
  return (
    saved === null ||
    months !== saved.months ||
    draft.reasonEn.trim() !== saved.reasonEn ||
    draft.reasonNl.trim() !== saved.reasonNl
  );
}

// The reason in the language a person reads: Dutch for a Dutch page, else
// English. language is i18n's, e.g. "nl" or "nl-NL".
export function legalReasonIn(
  reason: { en: string; nl: string },
  language: string,
): string {
  return language.toLowerCase().startsWith("nl") ? reason.nl : reason.en;
}
