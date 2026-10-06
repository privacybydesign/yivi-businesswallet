import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import * as React from "react";
import {
  useDecideProofingReviewMutation,
  useProofingDataMatchesQuery,
} from "../api/identity-proofing.queries";
import { isDataRequest, proofingDataExportUrl } from "../api/identity-proofing";
import type {
  ProofingDataMatch,
  ProofingRequest,
} from "../api/identity-proofing";
import { useWhenFormatter } from "../lib/format-when";
import {
  proofingErrorMessage,
  proofingStatusLabel,
  REVIEW_REASON_MAX_CHARS,
  reviewReasonTooLong,
  reviewSubmitStep,
} from "../lib/identity-proofing";
import { Button, ConfirmDialog, Tag } from "../ui";

const HINT = "text-ink-soft text-[12px]";
const ERROR = "text-error text-[12.5px]";
const LINK_BUTTON =
  "bg-ink text-surface inline-flex h-8 items-center rounded-md px-3 text-[13px] font-semibold transition-opacity hover:opacity-90";

// DataRequestTag marks a session that is a person asking for their data or
// its erasure, in the sessions list.
export function DataRequestTag({
  request,
}: {
  request: Pick<ProofingRequest, "flowKind">;
}): React.JSX.Element | null {
  const { t } = useTranslation();
  if (!isDataRequest(request.flowKind)) {
    return null;
  }
  return (
    <Tag tone="blue">
      {t(`identityProofingFlows.kinds.${request.flowKind}.title`)}
    </Tag>
  );
}

// A data request's part of its session's panel: what the person asks for,
// the customer's sessions of that person, and, while it awaits review, the
// decision. Who the person is and how surely they were proven (name, date of
// birth, document, face match, liveness, eIDAS level) are the panel's own rows.
// Approving takes the ticked sessions: a "delete my data" request erases them,
// a "see my data" request opens their data for download.
export function DataRequestPanel({
  slug,
  request,
}: {
  slug: string;
  request: ProofingRequest;
}): React.JSX.Element {
  const { t } = useTranslation();
  const open = request.status === "needs_review";
  const matches = useProofingDataMatchesQuery(slug, request.id);
  const kind = request.flowKind as "data_access" | "data_erasure";

  return (
    <div className="border-line mt-4 flex flex-col gap-3 border-t pt-4">
      <div>
        <h3 className="text-ink text-[14px] font-bold">
          {t(`customers.sessions.dataRequest.${kind}.title`)}
        </h3>
        <p className="text-ink-soft text-[13px]">
          {t(`customers.sessions.dataRequest.${kind}.hint`)}
        </p>
      </div>
      {matches.isPending ? (
        <p className={HINT}>{t("common.loading")}</p>
      ) : matches.isError ? (
        <p role="alert" className={ERROR}>
          {proofingErrorMessage(matches.error, t)}
        </p>
      ) : open ? (
        <Review slug={slug} request={request} matches={matches.data} />
      ) : (
        <>
          <MatchList matches={matches.data} />
          {request.status === "approved" && request.dataExportUntil && (
            <Download
              slug={slug}
              requestId={request.id}
              until={request.dataExportUntil}
            />
          )}
        </>
      )}
    </div>
  );
}

function Review({
  slug,
  request,
  matches,
}: {
  slug: string;
  request: ProofingRequest;
  matches: ProofingDataMatch[];
}): React.JSX.Element {
  const { t } = useTranslation();
  const decide = useDecideProofingReviewMutation(slug, request.id);
  // A match proven with the same document starts ticked; the reviewer
  // unticks what must be kept. A match on name and date of birth alone, or on
  // the e-mail address, starts unticked: only the reviewer's tick takes it.
  const [ticked, setTicked] = useState<string[]>(() =>
    matches.filter((m) => m.level === "strong").map((m) => m.requestId),
  );
  const [reason, setReason] = useState("");
  const [touched, setTouched] = useState(false);
  const missing = reason.trim() === "";
  const tooLong = reviewReasonTooLong(reason);
  const fieldId = `data-request-reason-${request.id}`;
  const kind = request.flowKind as "data_access" | "data_erasure";

  function toggle(requestId: string): void {
    setTicked((current) =>
      current.includes(requestId)
        ? current.filter((x) => x !== requestId)
        : [...current, requestId],
    );
  }

  // An erasure cannot be undone: approving it is confirmed with its count.
  const [confirmingErasure, setConfirmingErasure] = useState(false);

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
    decide.mutate({
      decision,
      reason: reason.trim(),
      ...(decision === "approve" ? { requestIds: ticked } : {}),
    });
  }

  return (
    <>
      {confirmingErasure && (
        <ConfirmDialog
          title={t(
            "customers.sessions.dataRequest.data_erasure.confirm.title",
            {
              count: ticked.length,
            },
          )}
          message={t(
            "customers.sessions.dataRequest.data_erasure.confirm.message",
          )}
          confirmLabel={t(
            "customers.sessions.dataRequest.data_erasure.approve",
            {
              count: ticked.length,
            },
          )}
          onConfirm={() => submit("approve")}
          onClose={() => setConfirmingErasure(false)}
        />
      )}
      <MatchList matches={matches} selection={{ ticked, onToggle: toggle }} />
      <label
        htmlFor={fieldId}
        className="text-ink-soft text-[12px] font-semibold"
      >
        {t("customers.sessions.review.reason")}
      </label>
      <textarea
        id={fieldId}
        value={reason}
        aria-invalid={(touched && missing) || tooLong}
        placeholder={t("customers.sessions.dataRequest.reasonPlaceholder")}
        onChange={(event) => setReason(event.target.value)}
        className="rounded-yivi border-line-strong bg-surface text-ink focus:border-ink focus:ring-ink/10 min-h-16 w-full border px-3 py-2 text-[13.5px] outline-none focus:ring-3"
      />
      {touched && missing && (
        <p className={ERROR}>{t("customers.sessions.review.reasonRequired")}</p>
      )}
      {tooLong && (
        <p className={ERROR}>
          {t("customers.sessions.review.reasonTooLong", {
            max: REVIEW_REASON_MAX_CHARS,
          })}
        </p>
      )}
      {decide.isError && (
        <p role="alert" className={ERROR}>
          {proofingErrorMessage(decide.error, t)}
        </p>
      )}
      <div className="flex flex-wrap gap-2">
        <Button
          size="sm"
          icon="valid"
          loading={decide.isPending && decide.variables?.decision === "approve"}
          disabled={decide.isPending}
          onClick={() => submit("approve")}
        >
          {t(`customers.sessions.dataRequest.${kind}.approve`, {
            count: ticked.length,
          })}
        </Button>
        <Button
          size="sm"
          variant="secondary"
          icon="invalid"
          loading={decide.isPending && decide.variables?.decision === "reject"}
          disabled={decide.isPending}
          onClick={() => submit("reject")}
        >
          {t("customers.sessions.review.reject")}
        </Button>
      </div>
    </>
  );
}

interface MatchSelection {
  ticked: string[];
  onToggle: (requestId: string) => void;
}

// MatchList lists the matched sessions: those found on the person's identity,
// then apart those sent to their e-mail address that never finished. With a
// selection they can be ticked, without it each shows the reviewer's decision.
function MatchList({
  matches,
  selection,
}: {
  matches: ProofingDataMatch[];
  selection?: MatchSelection;
}): React.JSX.Element {
  const { t } = useTranslation();
  if (matches.length === 0) {
    return (
      <p className="text-ink-soft text-[13px]">
        {t("customers.sessions.dataRequest.noMatches")}
      </p>
    );
  }
  const identified = matches.filter((m) => m.level !== "email");
  const emailed = matches.filter((m) => m.level === "email");
  return (
    <div className="flex flex-col gap-1.5">
      {identified.length > 0 && (
        <>
          <p className="text-ink text-[13px] font-semibold">
            {t("customers.sessions.dataRequest.matches", {
              count: identified.length,
            })}
          </p>
          <MatchRows matches={identified} selection={selection} />
          {selection && (
            <p className={HINT}>
              {t("customers.sessions.dataRequest.untickHint")}
            </p>
          )}
        </>
      )}
      {emailed.length > 0 && (
        <>
          <p className="text-ink mt-2 text-[13px] font-semibold">
            {t("customers.sessions.dataRequest.emailMatches", {
              count: emailed.length,
            })}
          </p>
          <MatchRows matches={emailed} selection={selection} />
          {selection && (
            <p className={HINT}>
              {t("customers.sessions.dataRequest.emailMatchesHint")}
            </p>
          )}
        </>
      )}
    </div>
  );
}

const LEVEL_TONES = {
  strong: "green",
  probable: "amber",
  email: "default",
} as const;

function MatchRows({
  matches,
  selection,
}: {
  matches: ProofingDataMatch[];
  selection?: MatchSelection;
}): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  return (
    <ul className="border-line divide-line bg-surface divide-y rounded-lg border">
      {matches.map((m) => {
        const checkboxId = `data-match-${m.requestId}`;
        return (
          <li key={m.requestId} className="flex items-start gap-3 px-3 py-2.5">
            {selection && (
              <input
                id={checkboxId}
                type="checkbox"
                className="mt-1 h-4 w-4"
                checked={selection.ticked.includes(m.requestId)}
                onChange={() => selection.onToggle(m.requestId)}
              />
            )}
            <label
              htmlFor={selection ? checkboxId : undefined}
              className="flex min-w-0 flex-1 flex-col gap-0.5 text-[13px]"
            >
              <span className="flex flex-wrap items-center gap-2">
                <span className="font-mono text-[12px]">{m.sessionId}</span>
                <Tag tone={LEVEL_TONES[m.level]}>
                  {t(`customers.sessions.dataRequest.levels.${m.level}`)}
                </Tag>
                {!selection && <MatchDecision match={m} />}
              </span>
              <span className="text-ink-soft text-[12px]">
                {[
                  m.flowName,
                  formatWhen(m.createdAt),
                  proofingStatusLabel(m.status, t),
                  m.eidasLevel,
                ]
                  .filter(Boolean)
                  .join(" · ")}
              </span>
            </label>
          </li>
        );
      })}
    </ul>
  );
}

function MatchDecision({
  match,
}: {
  match: ProofingDataMatch;
}): React.JSX.Element | null {
  const { t } = useTranslation();
  if (match.purgedAt) {
    return <Tag>{t("customers.sessions.dataRequest.erased")}</Tag>;
  }
  if (match.approved === undefined) {
    return null;
  }
  return (
    <Tag tone={match.approved ? "blue" : "default"}>
      {match.approved
        ? t("customers.sessions.dataRequest.approved")
        : t("customers.sessions.dataRequest.kept")}
    </Tag>
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
}: {
  slug: string;
  requestId: string;
  until: string;
}): React.JSX.Element {
  const { t } = useTranslation();
  const formatWhen = useWhenFormatter();
  const closed = useHasPassed(until);
  if (closed) {
    return (
      <p className={HINT}>
        {t("customers.sessions.dataRequest.downloadClosed", {
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
        {t("customers.sessions.dataRequest.download")}
      </a>
      <span className={HINT}>
        {t("customers.sessions.dataRequest.downloadUntil", {
          date: formatWhen(until),
        })}
      </span>
    </div>
  );
}
