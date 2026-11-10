import { useTranslation } from "react-i18next";
import * as React from "react";
import type { ProofingRequest } from "../api/identity-proofing";
import { useCheckSealsAgainMutation } from "../api/identity-proofing.queries";
import { proofingErrorMessage } from "../lib/identity-proofing";
import {
  SEAL_REFUSAL_KEYS,
  sealCheckState,
  sealChecksOf,
  uncheckedSeals,
} from "../lib/proofing-reviews";
import { toast } from "../lib/toast";
import { Button, Icon, StatusText } from "../ui";
import { REVIEW_ERROR, REVIEW_HINT } from "./proofing-review-parts";

const STATE_TONES = {
  waiting: "amber",
  kept: "green",
  refused: "red",
  notChecked: "default",
} as const;

// A step's files whose seal could not be checked at upload, as the trust
// lists or the revocation server were unreachable: each by name with what
// became of it, and while any waits, the reviewer checks them again before
// the step can be accepted.
export function UncheckedSeals({
  slug,
  request,
  kind,
  canCheck,
}: Readonly<{
  slug: string;
  request: ProofingRequest;
  kind: "diplomas" | "employment";
  canCheck: boolean;
}>): React.JSX.Element | null {
  const { t } = useTranslation();
  const check = useCheckSealsAgainMutation(slug, request.id, kind);
  const files = sealChecksOf(request, kind);
  const waiting = uncheckedSeals(request, kind);
  if (files.length === 0) return null;

  return (
    <div className="bg-tile rounded-yivi flex flex-col gap-3 px-4 py-3">
      <p className="text-ink flex items-start gap-2 text-[13.5px] font-semibold">
        <Icon name="time" className="text-tile-fg mt-0.5 shrink-0" />
        {t("identityProofing.reviews.seal.files", { count: files.length })}
      </p>
      <ul className="flex flex-col gap-2">
        {files.map((file, i) => {
          const state = sealCheckState(file.outcome);
          const reasonKey = SEAL_REFUSAL_KEYS[file.outcome];
          return (
            <li key={`${file.fileName}-${i}`} className="flex flex-col gap-0.5">
              <span className="flex flex-wrap items-center justify-between gap-x-3">
                <span className="text-ink min-w-0 grow basis-44 truncate text-[13px]">
                  {file.fileName}
                </span>
                <StatusText tone={STATE_TONES[state]}>
                  {t(`identityProofing.reviews.seal.states.${state}`)}
                </StatusText>
              </span>
              {state === "refused" && (
                <span className={REVIEW_HINT}>
                  {reasonKey === undefined ? file.outcome : t(reasonKey)}
                </span>
              )}
            </li>
          );
        })}
      </ul>
      {waiting > 0 && (
        <p className={REVIEW_HINT}>
          {t("identityProofing.reviews.seal.unchecked", { count: waiting })}
        </p>
      )}
      {waiting > 0 && canCheck && (
        <div>
          <Button
            variant="secondary"
            loading={check.isPending}
            onClick={() =>
              check.mutate(undefined, {
                onSuccess: () =>
                  toast.success(t("identityProofing.reviews.seal.checked")),
              })
            }
          >
            {t("identityProofing.reviews.seal.checkAgain")}
          </Button>
        </div>
      )}
      {check.isError && (
        <p role="alert" className={REVIEW_ERROR}>
          {proofingErrorMessage(check.error, t)}
        </p>
      )}
    </div>
  );
}
