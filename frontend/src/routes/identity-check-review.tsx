import { useState } from "react";
import { useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import { useMeQuery } from "../api/auth.queries";
import {
  useDecideReviewMutation,
  useProofingRequestResultQuery,
} from "../api/identity-proofing.queries";
import type { ProofingRequest } from "../api/identity-proofing";
import { usePercentFormatter } from "../lib/format-percent";
import { useWhenFormatter } from "../lib/format-when";
import {
  ageOverFact,
  assuranceLevelLabel,
  proofingErrorMessage,
  proofingMethodLabel,
  requestSubject,
  REVIEW_REASON_MAX_CHARS,
} from "../lib/identity-proofing";
import { issuingCountryName } from "../lib/issuing-countries";
import { toast } from "../lib/toast";
import {
  DEFAULT_REJECT_REASON,
  REJECT_REASONS,
  automaticChecks,
  checkDecisionInput,
  checkDecisionProblems,
  evidenceDocumentLabel,
  ownReview,
  reviewPath,
} from "../lib/proofing-reviews";
import type {
  AutomaticCheck,
  CheckDecisionDraft,
  OwnReview,
  RejectReasonKey,
} from "../lib/proofing-reviews";
import { Button, Icon, Select, StatusText, TopBar } from "../ui";
import {
  BackToReviews,
  ClosedNote,
  REVIEW_ERROR,
  REVIEW_FIELD_LABEL,
  REVIEW_HINT,
  REVIEW_TEXTAREA,
  ReviewFacts,
  ReviewSection,
} from "./proofing-review-parts";

const WARNING_ICON_SIZE = 18;
// The colour of an automatic check's outcome.
const OUTCOME_TONES = {
  passed: "green",
  failed: "red",
  notPerformed: "default",
} as const;
// The submit button's label per decision, before one is picked too.
const SUBMIT_KEYS = {
  approve: "approveSubmit",
  reject: "rejectSubmit",
} as const;

// An identity check that waits for a person: why, what the engine checked by
// itself, what the chip holds, and the decision.
export function IdentityCheckReview({
  slug,
  request,
}: Readonly<{
  slug: string;
  request: ProofingRequest;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const me = useMeQuery();
  const waiting = request.status === "needs_review";
  // A check the reader sent or that is about them is another reviewer's.
  const own = ownReview(request, me.data?.id);
  const readable =
    request.purgedAt === undefined &&
    (waiting || request.status === "approved" || request.status === "rejected");
  const result = useProofingRequestResultQuery(slug, request.id, readable);
  const subject = requestSubject(request);
  const subtitle = [
    subject.name,
    `${request.flowName}${
      request.flowVersion === undefined
        ? ""
        : `, ${t("identityProofingFlows.versionShort", { version: request.flowVersion })}`
    }`,
    proofingMethodLabel(request.method, t),
    formatWhen(request.createdAt),
  ]
    .filter((part) => part !== "" && part !== "—")
    .join(" · ");

  return (
    <>
      <TopBar
        title={t("identityProofing.reviews.check.title")}
        subtitle={subtitle}
      />
      <div className="flex flex-wrap items-start gap-6 p-4 sm:p-8">
        <div className="flex min-w-0 flex-[1_1_34rem] flex-col gap-6">
          {waiting ? <WhyItWaits /> : <ClosedNote request={request} />}
          <AutomaticChecks result={result} />
          <ChipData request={request} result={result} />
        </div>
        {waiting &&
          (own ? (
            <OwnReviewNote slug={slug} own={own} />
          ) : (
            <CheckDecision slug={slug} request={request} name={subject.name} />
          ))}
      </div>
    </>
  );
}

function WhyItWaits(): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <section
      aria-labelledby="review-why"
      className="border-warning/40 bg-warning-bg text-warning-fg rounded-yivi flex gap-3 border px-[18px] py-4"
    >
      <Icon
        name="warning"
        size={WARNING_ICON_SIZE}
        className="mt-0.5 shrink-0"
      />
      <div className="flex flex-col gap-1 text-[13.5px]">
        <h2 id="review-why" className="text-[14px] font-semibold">
          {t("identityProofing.reviews.check.why.title")}
        </h2>
        <p>{t("identityProofing.reviews.check.why.body")}</p>
      </div>
    </section>
  );
}

type Result = ReturnType<typeof useProofingRequestResultQuery>;

// The rows of a section that reads the result, or why they are not there.
function ResultState({
  result,
}: Readonly<{ result: Result }>): React.JSX.Element | null {
  const { t } = useTranslation();
  if (result.isPending && result.fetchStatus === "idle") return null;
  if (result.isPending) {
    return <p className={REVIEW_HINT}>{t("common.loading")}</p>;
  }
  if (result.isError) {
    return (
      <p role="alert" className={REVIEW_ERROR}>
        {proofingErrorMessage(result.error, t)}
      </p>
    );
  }
  return null;
}

function AutomaticChecks({
  result,
}: Readonly<{ result: Result }>): React.JSX.Element {
  const { t } = useTranslation();
  const checks = automaticChecks(result.data?.evidence.at(0));
  return (
    <ReviewSection
      id="review-auto"
      title={t("identityProofing.reviews.check.auto.title")}
    >
      <ResultState result={result} />
      {result.data &&
        (checks.length === 0 ? (
          <p className={REVIEW_HINT}>
            {t("identityProofing.reviews.check.auto.none")}
          </p>
        ) : (
          <ul className="divide-line border-line divide-y border-t">
            {checks.map((check) => (
              <li
                key={check.key}
                className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 py-2.5 text-[13.5px]"
              >
                <span>
                  {t(`identityProofing.reviews.check.auto.${check.key}`)}
                </span>
                <CheckOutcome check={check} />
              </li>
            ))}
          </ul>
        ))}
    </ReviewSection>
  );
}

function CheckOutcome({
  check,
}: Readonly<{ check: AutomaticCheck }>): React.JSX.Element {
  const { t } = useTranslation();
  const formatPercent = usePercentFormatter();
  if (check.score !== undefined) {
    return <span className="font-semibold">{formatPercent(check.score)}</span>;
  }
  const outcome = check.outcome ?? "notPerformed";
  return (
    <StatusText tone={OUTCOME_TONES[outcome]}>
      {t(`identityProofing.reviews.check.auto.${outcome}`)}
    </StatusText>
  );
}

function ChipData({
  request,
  result,
}: Readonly<{
  request: ProofingRequest;
  result: Result;
}>): React.JSX.Element {
  const { t, i18n } = useTranslation();
  const { identity } = result.data ?? {};
  const evidence = result.data?.evidence.at(0);
  const none = "—";
  const document = evidenceDocumentLabel(evidence, i18n.language, t) ?? none;
  const ageOver = ageOverFact(identity, t);
  const level = request.eidasLevel
    ? t("identityProofing.reviews.check.data.levelOnceApproved", {
        level: assuranceLevelLabel(request.eidasLevel, t),
      })
    : none;

  return (
    <ReviewSection
      id="review-data"
      title={t("identityProofing.reviews.check.data.title")}
      hint={t("identityProofing.sessions.identity.audited")}
    >
      <ResultState result={result} />
      {result.data && (
        <ReviewFacts
          rows={[
            {
              label: t("identityProofing.sessions.identity.name"),
              value:
                [identity?.givenName, identity?.familyName]
                  .filter(Boolean)
                  .join(" ") || none,
            },
            {
              label: t("identityProofing.sessions.identity.birthDate"),
              value: identity?.birthDate || none,
            },
            {
              label: t("identityProofing.sessions.identity.nationality"),
              value: identity?.nationality
                ? issuingCountryName(identity.nationality, i18n.language, t)
                : none,
            },
            ...(ageOver ? [ageOver] : []),
            {
              label: t("identityProofing.sessions.identity.document"),
              value: document,
            },
            {
              label: t("identityProofing.reviews.check.data.expires"),
              value: evidence?.expiryDate || none,
            },
            {
              label: t("identityProofing.reviews.check.data.level"),
              value: level,
            },
          ]}
        />
      )}
    </ReviewSection>
  );
}

// In place of the decision on a check the reader may not decide.
function OwnReviewNote({
  slug,
  own,
}: Readonly<{
  slug: string;
  own: OwnReview;
}>): React.JSX.Element {
  const { t } = useTranslation();
  return (
    <div className="border-line bg-surface rounded-yivi shadow-card flex min-w-72 flex-[0_1_21rem] flex-col gap-4 border p-5">
      <h2 className="font-display text-ink text-[18px] font-semibold">
        {t("identityProofing.reviews.check.decision.title")}
      </h2>
      <output className="block text-[13.5px]">
        {t(`identityProofing.reviews.check.own.${own}`)}
      </output>
      <BackToReviews to={reviewPath(slug)} />
    </div>
  );
}

// Approve, or reject with what is wrong, and always a reason the audit log keeps.
function CheckDecision({
  slug,
  request,
  name,
}: Readonly<{
  slug: string;
  request: ProofingRequest;
  name: string;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const decide = useDecideReviewMutation(slug, request.id);
  const [draft, setDraft] = useState<CheckDecisionDraft>({
    choice: undefined,
    rejectReason: DEFAULT_REJECT_REASON,
    reason: "",
  });
  const [touched, setTouched] = useState(false);
  const problems = checkDecisionProblems(draft);
  const idPrefix = `review-${request.id}`;

  function submit(event: React.SyntheticEvent): void {
    event.preventDefault();
    setTouched(true);
    if (draft.choice === undefined || problems.length > 0) return;

    const decision = draft.choice;
    decide.mutate(checkDecisionInput({ ...draft, choice: decision }), {
      onSuccess: () => {
        toast.success(
          t(
            decision === "approve"
              ? "identityProofing.reviews.check.decision.approved"
              : "identityProofing.reviews.check.decision.rejected",
          ),
        );
        void navigate(reviewPath(slug));
      },
    });
  }

  const submitKey =
    draft.choice === undefined ? "submit" : SUBMIT_KEYS[draft.choice];

  return (
    <form
      onSubmit={submit}
      noValidate
      aria-labelledby={`${idPrefix}-title`}
      className="border-line bg-surface rounded-yivi shadow-card flex min-w-72 flex-[0_1_21rem] flex-col gap-4 border p-5"
    >
      <h2
        id={`${idPrefix}-title`}
        className="font-display text-ink text-[18px] font-semibold"
      >
        {t("identityProofing.reviews.check.decision.title")}
      </h2>
      <fieldset className="flex flex-col gap-2.5">
        <legend className={`${REVIEW_FIELD_LABEL} mb-2`}>
          {t("identityProofing.reviews.check.decision.question", { name })}
        </legend>
        {(["approve", "reject"] as const).map((choice) => (
          <label
            key={choice}
            className="flex items-center gap-2.5 text-[13.5px]"
          >
            <input
              type="radio"
              name={`${idPrefix}-choice`}
              value={choice}
              checked={draft.choice === choice}
              onChange={() => setDraft({ ...draft, choice })}
              className="h-4 w-4"
            />
            {t(`identityProofing.reviews.check.decision.${choice}`)}
          </label>
        ))}
        {touched && problems.includes("choice") && (
          <p className={REVIEW_ERROR}>
            {t("identityProofing.reviews.check.decision.choiceRequired")}
          </p>
        )}
      </fieldset>
      {draft.choice === "reject" && (
        <div className="flex flex-col gap-1.5">
          <label htmlFor={`${idPrefix}-wrong`} className={REVIEW_FIELD_LABEL}>
            {t("identityProofing.reviews.check.decision.wrong")}
          </label>
          <Select
            id={`${idPrefix}-wrong`}
            value={draft.rejectReason}
            onChange={(event) =>
              setDraft({
                ...draft,
                rejectReason: event.target.value as RejectReasonKey,
              })
            }
          >
            {REJECT_REASONS.map(({ key }) => (
              <option key={key} value={key}>
                {t(
                  `identityProofing.reviews.check.decision.wrongOptions.${key}`,
                )}
              </option>
            ))}
          </Select>
        </div>
      )}
      <div className="flex flex-col gap-1.5">
        <label htmlFor={`${idPrefix}-reason`} className={REVIEW_FIELD_LABEL}>
          {t("identityProofing.reviews.check.decision.reason")}
        </label>
        <textarea
          id={`${idPrefix}-reason`}
          value={draft.reason}
          aria-invalid={touched && problems.some((p) => p !== "choice")}
          aria-describedby={`${idPrefix}-reason-hint`}
          onChange={(event) =>
            setDraft({ ...draft, reason: event.target.value })
          }
          className={REVIEW_TEXTAREA}
        />
        <p id={`${idPrefix}-reason-hint`} className={REVIEW_HINT}>
          {t("identityProofing.reviews.check.decision.reasonHint")}
        </p>
        {touched && problems.includes("reason") && (
          <p className={REVIEW_ERROR}>
            {t("identityProofing.sessions.review.reasonRequired")}
          </p>
        )}
        {problems.includes("reasonTooLong") && (
          <p className={REVIEW_ERROR}>
            {t("identityProofing.sessions.review.reasonTooLong", {
              max: REVIEW_REASON_MAX_CHARS,
            })}
          </p>
        )}
      </div>
      {decide.isError && (
        <p role="alert" className={REVIEW_ERROR}>
          {proofingErrorMessage(decide.error, t)}
        </p>
      )}
      <Button type="submit" size="lg" loading={decide.isPending}>
        {t(`identityProofing.reviews.check.decision.${submitKey}`)}
      </Button>
      <BackToReviews to={reviewPath(slug)} />
    </form>
  );
}
