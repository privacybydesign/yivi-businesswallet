import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import type { ProofingRequest } from "../api/identity-proofing";
import { Card } from "../ui";

// A link that looks like Button's primary/md and secondary/md: Button renders
// only <button>.
const LINK_BASE =
  "rounded-yivi font-display inline-flex h-9 items-center justify-center px-3.5 text-[13.5px] font-semibold whitespace-nowrap transition-colors duration-150";
export const PRIMARY_LINK = `${LINK_BASE} bg-primary text-primary-fg hover:bg-primary-hover`;
export const SECONDARY_LINK = `${LINK_BASE} border-line-strong bg-surface text-ink hover:bg-surface-3 border`;
const REVIEW_SECTION = "flex flex-col gap-4 p-5";
export const REVIEW_HEADING = "font-display text-ink text-[18px] font-semibold";
export const REVIEW_ERROR = "text-error text-[12.5px]";
export const REVIEW_FIELD_LABEL = "text-ink text-[13.5px] font-semibold";
export const REVIEW_HINT = "text-ink-soft text-[12.5px]";
// Mirrors the Input base, so the reason reads as the same field.
export const REVIEW_TEXTAREA =
  "rounded-yivi border-line-strong bg-surface text-ink focus:border-ink focus:ring-ink/10 min-h-24 w-full border px-3 py-2 text-[13.5px] outline-none focus:ring-3";

// A titled box of the review page.
export function ReviewSection({
  id,
  title,
  hint,
  children,
}: {
  id: string;
  title: string;
  hint?: string;
  children: React.ReactNode;
}): React.JSX.Element {
  return (
    <Card className={REVIEW_SECTION} aria-labelledby={id} role="region">
      <div className="flex flex-col gap-1">
        <h2 id={id} className={REVIEW_HEADING}>
          {title}
        </h2>
        {hint && <p className={REVIEW_HINT}>{hint}</p>}
      </div>
      {children}
    </Card>
  );
}

// Term and value pairs, as the review pages show what a person proved.
export function ReviewFacts({
  rows,
}: {
  rows: readonly { label: string; value: string }[];
}): React.JSX.Element {
  return (
    <dl className="grid gap-x-4 gap-y-2.5 text-[13.5px] sm:grid-cols-[10rem_1fr]">
      {rows.map(({ label, value }) => (
        <React.Fragment key={label}>
          <dt className="text-ink-soft">{label}</dt>
          <dd>{value}</dd>
        </React.Fragment>
      ))}
    </dl>
  );
}

// Says a request no longer waits: its page stays readable after the decision.
export function ClosedNote({
  request,
}: {
  request: ProofingRequest;
}): React.JSX.Element {
  const { t } = useTranslation();
  const key =
    request.status === "approved" || request.status === "rejected"
      ? request.status
      : "other";
  return (
    <output className="border-line bg-surface-2 text-ink rounded-yivi block border px-4 py-3 text-[13.5px]">
      {t(`identityProofing.reviews.notWaiting.${key}`)}
    </output>
  );
}

export function BackToReviews({ to }: { to: string }): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <Link
      to={to}
      className="text-link self-center text-[13.5px] font-semibold underline"
    >
      {t("identityProofing.reviews.back")}
    </Link>
  );
}
