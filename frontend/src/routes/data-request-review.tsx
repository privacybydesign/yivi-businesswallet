import { useEffect, useState } from "react";
import { useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import * as React from "react";
import type { UseQueryResult } from "@tanstack/react-query";
import {
  useDecideReviewMutation,
  useProofingDataMatchesQuery,
  useProofingRequestResultQuery,
} from "../api/identity-proofing.queries";
import { isDataRequest, proofingDataExportUrl } from "../api/identity-proofing";
import type {
  ProofingDataMatch,
  ProofingRequest,
  ProofingResult,
} from "../api/identity-proofing";
import {
  useDateFormatter,
  useDayMonthFormatter,
  useWhenFormatter,
} from "../lib/format-when";
import {
  assuranceLevelLabel,
  matchTickedByDefault,
  proofingErrorMessage,
  proofingStatusLabel,
  requestSubject,
  REVIEW_REASON_MAX_CHARS,
  reviewReasonTooLong,
  reviewSubmitStep,
} from "../lib/identity-proofing";
import {
  answerBy,
  askedAt,
  askedLine,
  evidenceDocumentLabel,
  provenWithParts,
  reviewPath,
} from "../lib/proofing-reviews";
import { toast } from "../lib/toast";
import { Button, ConfirmDialog, StatusText, TopBar } from "../ui";
import {
  BackToReviews,
  ClosedNote,
  REVIEW_ERROR,
  REVIEW_FIELD_LABEL,
  REVIEW_HEADING,
  REVIEW_HINT,
  REVIEW_TEXTAREA,
  ReviewFacts,
  ReviewSection,
} from "./proofing-review-parts";

const HINT = "text-ink-soft text-[12px]";
const LINK_BUTTON =
  "bg-ink text-surface inline-flex h-8 items-center rounded-md px-3 text-[13px] font-semibold transition-opacity hover:opacity-90";

// What a data request asks for: the data held of the person, or its deletion.
type DataRequestKind = "data_access" | "data_erasure";

// DataRequestNote says a session is a person asking for their data or its
// erasure, in the sessions list.
export function DataRequestNote({
  request,
}: Readonly<{
  request: Pick<ProofingRequest, "flowKind">;
}>): React.JSX.Element | null {
  const { t } = useTranslation();
  if (!isDataRequest(request.flowKind)) {
    return null;
  }
  return (
    <div className={HINT}>
      {t(`identityProofingFlows.kinds.${request.flowKind}.title`)}
    </div>
  );
}

// A data request's page: who asks and how surely they were proven, the org's
// checks of that person, and, while it waits, the decision. Deciding takes the
// ticked checks: a "delete my data" request deletes them, a "see my data"
// request opens their data for download.
export function DataRequestReview({
  slug,
  kind,
  request,
}: Readonly<{
  slug: string;
  kind: DataRequestKind;
  request: ProofingRequest;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const formatDay = useDayMonthFormatter();
  const matches = useProofingDataMatchesQuery(slug, request.id);
  const due = answerBy(request);
  const subtitle = [
    askedLine(request, formatWhen(askedAt(request)), t),
    due &&
      t("identityProofing.reviews.dataRequest.answerBy", {
        date: formatDay(due),
      }),
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <>
      <TopBar
        title={t(`identityProofingFlows.kinds.${kind}.title`)}
        subtitle={subtitle}
      />
      {matches.isPending ? (
        <p className="text-ink-soft p-4 text-[14px] sm:p-8">
          {t("common.loading")}
        </p>
      ) : matches.isError ? (
        <p role="alert" className={`${REVIEW_ERROR} p-4 sm:p-8`}>
          {proofingErrorMessage(matches.error, t)}
        </p>
      ) : (
        <ReviewBody
          slug={slug}
          kind={kind}
          request={request}
          matches={matches.data}
        />
      )}
    </>
  );
}

// The page once the checks are found: it holds which of them are ticked, which
// the list and the decision both read.
function ReviewBody({
  slug,
  kind,
  request,
  matches,
}: Readonly<{
  slug: string;
  kind: DataRequestKind;
  request: ProofingRequest;
  matches: ProofingDataMatch[];
}>): React.JSX.Element {
  const { t } = useTranslation();
  const waiting = request.status === "needs_review";
  const readable =
    request.purgedAt === undefined &&
    (waiting || request.status === "approved" || request.status === "rejected");
  const result = useProofingRequestResultQuery(slug, request.id, readable);
  // The matches taken without a tick start ticked (matchTickedByDefault); the
  // reviewer unticks what must be kept and ticks any other of the person's. One
  // an automatic erasure deleted already stays ticked: it is gone.
  // A check the law makes the org keep cannot be deleted: it is never ticked.
  const [ticked, setTicked] = useState<string[]>(() =>
    matches
      .filter(
        (m) =>
          m.purgedAt !== undefined ||
          (matchTickedByDefault(request.flowKind, m.level) &&
            m.heldUntil === undefined),
      )
      .map((m) => m.requestId),
  );

  function toggle(requestId: string): void {
    setTicked((current) =>
      current.includes(requestId)
        ? current.filter((x) => x !== requestId)
        : [...current, requestId],
    );
  }

  return (
    <div className="flex flex-wrap items-start gap-6 p-4 sm:p-8">
      <div className="flex min-w-0 flex-[1_1_34rem] flex-col gap-6">
        {!waiting && <ClosedNote request={request} />}
        <WhoAsks request={request} result={result} />
        <ReviewSection
          id="review-found"
          title={t("identityProofing.reviews.dataRequest.found.title")}
          hint={t(
            waiting
              ? `identityProofing.reviews.dataRequest.found.${kind}`
              : "identityProofing.reviews.dataRequest.found.closed",
          )}
        >
          <MatchList
            matches={matches}
            selection={
              waiting
                ? {
                    ticked,
                    onToggle: toggle,
                    tickable: (m) => m.purgedAt === undefined,
                    erasure: kind === "data_erasure",
                  }
                : undefined
            }
          />
        </ReviewSection>
      </div>
      {waiting ? (
        <DataDecision
          slug={slug}
          kind={kind}
          request={request}
          ticked={ticked}
          kept={matches.length - ticked.length}
        />
      ) : (
        <ClosedAside slug={slug} request={request} />
      )}
    </div>
  );
}

function WhoAsks({
  request,
  result,
}: Readonly<{
  request: ProofingRequest;
  result: UseQueryResult<ProofingResult, Error>;
}>): React.JSX.Element {
  const { t, i18n } = useTranslation();
  const none = "—";
  const { identity } = result.data ?? {};
  const evidence = result.data?.evidence.at(0);
  const document = evidenceDocumentLabel(evidence, i18n.language, t);
  const how = provenWithParts(evidence).map((part) =>
    t(`identityProofing.reviews.dataRequest.who.${part}`),
  );
  const provenWith = [document, how.join(", ")].filter(Boolean).join(" · ");
  // An identity not read yet: a disabled read is not loading.
  const loading = result.isPending && result.fetchStatus !== "idle";

  return (
    <ReviewSection
      id="review-who"
      title={t("identityProofing.reviews.dataRequest.who.title")}
      hint={t("identityProofing.sessions.identity.audited")}
    >
      {loading && <p className={REVIEW_HINT}>{t("common.loading")}</p>}
      {result.isError && (
        <p role="alert" className={REVIEW_ERROR}>
          {proofingErrorMessage(result.error, t)}
        </p>
      )}
      {!loading && !result.isError && (
        <ReviewFacts
          rows={[
            {
              label: t("identityProofing.sessions.identity.name"),
              value:
                [identity?.givenName, identity?.familyName]
                  .filter(Boolean)
                  .join(" ") ||
                requestSubject(request).name ||
                none,
            },
            {
              label: t("identityProofing.sessions.identity.birthDate"),
              value: identity?.birthDate || none,
            },
            {
              label: t("identityProofing.reviews.dataRequest.who.provenWith"),
              value: provenWith || none,
            },
            {
              label: t("identityProofing.reviews.dataRequest.who.level"),
              value: request.eidasLevel
                ? assuranceLevelLabel(request.eidasLevel, t)
                : none,
            },
          ]}
        />
      )}
    </ReviewSection>
  );
}

interface MatchSelection {
  ticked: string[];
  onToggle: (requestId: string) => void;
  tickable: (match: ProofingDataMatch) => boolean;
  erasure: boolean;
}

// The checks found for the person. With a selection each can be ticked, without
// it each shows what was decided.
function MatchList({
  matches,
  selection,
}: Readonly<{
  matches: ProofingDataMatch[];
  selection?: MatchSelection;
}>): React.JSX.Element {
  const { t } = useTranslation();
  if (matches.length === 0) {
    return (
      <p className={REVIEW_HINT}>
        {t("identityProofing.reviews.dataRequest.found.none")}
      </p>
    );
  }
  return (
    <>
      <ul className="divide-line border-line divide-y border-y">
        {matches.map((match) => (
          <MatchRow key={match.requestId} match={match} selection={selection} />
        ))}
      </ul>
      {selection && matches.some(isUnfinished) && (
        <p className={REVIEW_HINT}>
          {t(
            selection.erasure
              ? "identityProofing.reviews.dataRequest.found.unfinishedErasure"
              : "identityProofing.reviews.dataRequest.found.unfinished",
          )}
        </p>
      )}
      {selection && matches.some((m) => m.heldUntil) && (
        <p className={REVIEW_HINT}>
          {t("identityProofing.reviews.dataRequest.found.held")}
        </p>
      )}
    </>
  );
}

// A check that never finished is matched on what was typed, not on an identity.
function isUnfinished(match: ProofingDataMatch): boolean {
  return match.level === "email" || match.level === "name";
}

const LEVEL_TONES = {
  strong: "green",
  probable: "amber",
  email: "default",
  name: "default",
} as const;

function MatchRow({
  match,
  selection,
}: Readonly<{
  match: ProofingDataMatch;
  selection?: MatchSelection;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const formatDate = useDateFormatter();
  const checkboxId = `data-match-${match.requestId}`;
  const detail = [
    proofingStatusLabel(match.status, t),
    formatDate(match.completedAt ?? match.createdAt),
    match.eidasLevel && assuranceLevelLabel(match.eidasLevel, t),
  ]
    .filter(Boolean)
    .join(" · ");
  const body = (
    <>
      <span className="text-ink font-semibold">{match.flowName}</span>
      <span className={REVIEW_HINT}>{detail}</span>
    </>
  );

  // A status keeps to one line, so the statuses wrap below the flow name once
  // it would shrink under basis-44, as on a phone.
  return (
    <li className="flex flex-wrap items-start gap-x-3 gap-y-1.5 py-3">
      {selection && (
        <input
          id={checkboxId}
          type="checkbox"
          className="mt-1 h-4 w-4"
          checked={selection.ticked.includes(match.requestId)}
          disabled={!selection.tickable(match) || match.heldUntil !== undefined}
          onChange={() => selection.onToggle(match.requestId)}
        />
      )}
      {selection ? (
        <label
          htmlFor={checkboxId}
          className="flex min-w-0 grow basis-44 flex-col gap-0.5 text-[13.5px]"
        >
          {body}
        </label>
      ) : (
        <div className="flex min-w-0 grow basis-44 flex-col gap-0.5 text-[13.5px]">
          {body}
        </div>
      )}
      <span className="ml-auto flex flex-wrap justify-end gap-1.5">
        <StatusText tone={LEVEL_TONES[match.level]}>
          {t(
            `identityProofing.reviews.dataRequest.found.levels.${match.level}`,
          )}
        </StatusText>
        {match.heldUntil && (
          <StatusText tone="amber">
            {t("identityProofing.reviews.dataRequest.found.heldUntil", {
              date: formatDate(match.heldUntil),
            })}
          </StatusText>
        )}
        {!selection && <MatchDecision match={match} />}
      </span>
    </li>
  );
}

function MatchDecision({
  match,
}: Readonly<{
  match: ProofingDataMatch;
}>): React.JSX.Element | null {
  const { t } = useTranslation();
  if (match.purgedAt) {
    return (
      <StatusText>
        {t("identityProofing.reviews.dataRequest.found.erased")}
      </StatusText>
    );
  }
  if (match.approved === undefined) {
    return null;
  }
  return (
    <StatusText>
      {match.approved
        ? t("identityProofing.reviews.dataRequest.found.approved")
        : t("identityProofing.reviews.dataRequest.found.kept")}
    </StatusText>
  );
}

// What a decided request leaves to do: an approved "see my data" request's
// data to download, and the way back.
function ClosedAside({
  slug,
  request,
}: Readonly<{
  slug: string;
  request: ProofingRequest;
}>): React.JSX.Element {
  return (
    <div className="flex min-w-72 flex-[0_1_21rem] flex-col gap-4">
      {request.status === "approved" && request.dataExportUntil && (
        <Download
          slug={slug}
          requestId={request.id}
          until={request.dataExportUntil}
        />
      )}
      <BackToReviews to={reviewPath(slug)} />
    </div>
  );
}

// The decision on a request that waits: a summary of what it will do, a reason
// the audit log keeps, and approve or refuse.
function DataDecision({
  slug,
  kind,
  request,
  ticked,
  kept,
}: Readonly<{
  slug: string;
  kind: DataRequestKind;
  request: ProofingRequest;
  ticked: string[];
  kept: number;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const decide = useDecideReviewMutation(slug, request.id);
  const [reason, setReason] = useState("");
  const [touched, setTouched] = useState(false);
  // An erasure cannot be undone: approving it is confirmed with its count.
  const [confirmingErasure, setConfirmingErasure] = useState(false);
  const missing = reason.trim() === "";
  const tooLong = reviewReasonTooLong(reason);
  const fieldId = `data-request-reason-${request.id}`;

  function submit(decision: "approve" | "reject"): void {
    setTouched(true);
    if (missing || tooLong) return;

    if (
      reviewSubmitStep({ decision, kind, confirming: confirmingErasure }) ===
      "confirm"
    ) {
      setConfirmingErasure(true);
      return;
    }

    setConfirmingErasure(false);
    decide.mutate(
      {
        decision,
        reason: reason.trim(),
        ...(decision === "approve" ? { requestIds: ticked } : {}),
      },
      {
        onSuccess: () => {
          toast.success(
            t(
              decision === "approve"
                ? "identityProofing.reviews.dataRequest.decision.approved"
                : "identityProofing.reviews.dataRequest.decision.rejected",
            ),
          );
          void navigate(reviewPath(slug));
        },
      },
    );
  }

  return (
    <>
      {confirmingErasure && (
        <ConfirmDialog
          title={t(
            "identityProofing.reviews.dataRequest.decision.confirmTitle",
            { count: ticked.length },
          )}
          message={t(
            "identityProofing.reviews.dataRequest.decision.confirmMessage",
          )}
          confirmLabel={t(
            `identityProofing.reviews.dataRequest.decision.approve_${kind}`,
            { count: ticked.length },
          )}
          onConfirm={() => submit("approve")}
          onClose={() => setConfirmingErasure(false)}
        />
      )}
      <div className="border-line bg-surface rounded-yivi shadow-card flex min-w-72 flex-[0_1_21rem] flex-col gap-4 border p-5">
        <h2 className={REVIEW_HEADING}>
          {t("identityProofing.reviews.dataRequest.decision.title")}
        </h2>
        <p className="text-[13.5px]">
          {t(`identityProofing.reviews.dataRequest.decision.${kind}`, {
            count: ticked.length,
          })}{" "}
          {kept > 0 &&
            t("identityProofing.reviews.dataRequest.decision.kept", {
              count: kept,
            })}
        </p>
        <div className="flex flex-col gap-1.5">
          <label htmlFor={fieldId} className={REVIEW_FIELD_LABEL}>
            {t("identityProofing.reviews.dataRequest.decision.reason")}
          </label>
          <textarea
            id={fieldId}
            value={reason}
            aria-invalid={(touched && missing) || tooLong}
            aria-describedby={`${fieldId}-hint`}
            onChange={(event) => setReason(event.target.value)}
            className={REVIEW_TEXTAREA}
          />
          <p id={`${fieldId}-hint`} className={REVIEW_HINT}>
            {t("identityProofing.reviews.dataRequest.decision.reasonHint")}
          </p>
          {touched && missing && (
            <p className={REVIEW_ERROR}>
              {t("identityProofing.sessions.review.reasonRequired")}
            </p>
          )}
          {tooLong && (
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
        <Button
          size="lg"
          loading={decide.isPending && decide.variables?.decision === "approve"}
          disabled={decide.isPending}
          onClick={() => submit("approve")}
        >
          {t(`identityProofing.reviews.dataRequest.decision.approve_${kind}`, {
            count: ticked.length,
          })}
        </Button>
        <Button
          size="lg"
          variant="secondary"
          loading={decide.isPending && decide.variables?.decision === "reject"}
          disabled={decide.isPending}
          onClick={() => submit("reject")}
        >
          {t("identityProofing.reviews.dataRequest.decision.refuse")}
        </Button>
        <BackToReviews to={reviewPath(slug)} />
      </div>
    </>
  );
}

// Download is an approved "see my data" request's data, for the admin to hand
// the person, while it can be downloaded.
// The longest delay setTimeout takes (2^31 - 1 ms, about 24.8 days); a
// window that closes later is not waited for, the page is reloaded long before.
const MAX_TIMEOUT_MS = 2_147_483_647;

// Whether the moment has passed, flipping when it does while the page is open.
function useHasPassed(iso: string): boolean {
  const deadline = Date.parse(iso);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const remaining = deadline - Date.now();
    if (remaining <= 0 || remaining > MAX_TIMEOUT_MS) return;
    const timer = setTimeout(() => setNow(Date.now()), remaining);
    return () => clearTimeout(timer);
  }, [deadline]);
  return deadline <= now;
}

// The export of an approved access request, downloadable until its window
// closes; after that the backend refuses it, so the button goes too.
function Download({
  slug,
  requestId,
  until,
}: Readonly<{
  slug: string;
  requestId: string;
  until: string;
}>): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const closed = useHasPassed(until);
  if (closed) {
    return (
      <p className={HINT}>
        {t("identityProofing.sessions.dataRequest.downloadClosed", {
          date: formatWhen(until),
        })}
      </p>
    );
  }
  return (
    <div className="flex flex-wrap items-center gap-3">
      <a
        href={proofingDataExportUrl(slug, requestId)}
        download
        className={LINK_BUTTON}
      >
        {t("identityProofing.sessions.dataRequest.download")}
      </a>
      <span className={HINT}>
        {t("identityProofing.sessions.dataRequest.downloadUntil", {
          date: formatWhen(until),
        })}
      </span>
    </div>
  );
}
