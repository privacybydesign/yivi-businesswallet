import { isDataRequest } from "../api/identity-proofing";
import type {
  ProofingRequest,
  ProofingResult,
  ProofingReviewInput,
} from "../api/identity-proofing";
import type { TFunction } from "i18next";
import {
  REVIEW_REASON_MAX_CHARS,
  documentKindOf,
  requestSubject,
} from "./identity-proofing";
import { issuingCountryName } from "./issuing-countries";

// The Reviews page of an org, or the review page of one of its requests.
export function reviewPath(slug: string, requestId?: string): string {
  const reviews = `/${slug}/identity-proofing/reviews`;
  return requestId === undefined ? reviews : `${reviews}/${requestId}`;
}

// The person's name, or a word for one whose data was purged.
export function personName(request: ProofingRequest, t: TFunction): string {
  return (
    requestSubject(request).name || t("identityProofing.sessions.purgedSubject")
  );
}

// Who asked, and when; a purged request has no name left to say.
export function askedLine(
  request: ProofingRequest,
  when: string,
  t: TFunction,
): string {
  const { name } = requestSubject(request);
  return name === ""
    ? t("identityProofing.reviews.dataRequest.askedPurged", { date: when })
    : t("identityProofing.reviews.dataRequest.asked", { name, date: when });
}

// The Reviews page's views: what waits, narrowed to one kind, and what is
// closed.
export const REVIEW_TABS = ["all", "checks", "data", "closed"] as const;
export type ReviewTab = (typeof REVIEW_TABS)[number];
export const DEFAULT_REVIEW_TAB: ReviewTab = "all";

export function reviewTabFrom(raw: string | null): ReviewTab {
  return REVIEW_TABS.find((tab) => tab === raw) ?? DEFAULT_REVIEW_TAB;
}

// Whether the request waits for a decision: a purged one has no person left
// to decide on.
function isWaiting(request: ProofingRequest): boolean {
  return request.status === "needs_review" && request.purgedAt === undefined;
}

// When it went to review: the person was proven, or the check ended its steps.
export function waitingSince(request: ProofingRequest): string {
  return request.completedAt ?? request.createdAt;
}

// When a data request was asked, as its page says: while it waits, when it
// reached review, the moment its month to answer runs from (answerBy). A
// decision overwrites that moment, so a closed one says when it was made.
export function askedAt(request: ProofingRequest): string {
  return isWaiting(request) ? waitingSince(request) : request.createdAt;
}

function sinceMillis(request: ProofingRequest): number {
  return Date.parse(waitingSince(request));
}

// What waits, the longest first.
export function waitingOldestFirst(
  requests: readonly ProofingRequest[],
): ProofingRequest[] {
  return requests
    .filter(isWaiting)
    .sort((a, b) => sinceMillis(a) - sinceMillis(b));
}

interface ReviewCounts {
  all: number;
  checks: number;
  data: number;
}

export function reviewCounts(
  waiting: readonly ProofingRequest[],
): ReviewCounts {
  const data = waiting.filter((r) => isDataRequest(r.flowKind)).length;
  return { all: waiting.length, checks: waiting.length - data, data };
}

// The requests a tab lists; Closed has its own list, so no waiting ones.
export function waitingOfTab(
  tab: ReviewTab,
  waiting: readonly ProofingRequest[],
): ProofingRequest[] {
  switch (tab) {
    case "all":
      return [...waiting];
    case "checks":
      return waiting.filter((r) => !isDataRequest(r.flowKind));
    case "data":
      return waiting.filter((r) => isDataRequest(r.flowKind));
    case "closed":
      return [];
  }
}

// The data requests that ended, the latest first, whoever ended them: a
// reviewer, or no one (the engine rejected the person, the review lapsed, the
// org paused, the law keeps the data). The list carries no mark of which, so
// the page claims no decision and shows each one's error code. Identity checks
// are not listed: none goes to review yet.
export function closedRequests(
  requests: readonly ProofingRequest[],
): ProofingRequest[] {
  return requests
    .filter(
      (r) =>
        isDataRequest(r.flowKind) &&
        (r.status === "approved" || r.status === "rejected"),
    )
    .sort(
      (a, b) =>
        Date.parse(b.completedAt ?? b.createdAt) -
        Date.parse(a.completedAt ?? a.createdAt),
    );
}

// Until when a data request in review can be answered: its review lapses then
// (REVIEW_LAPSED, purge_at), a calendar month after it went there (askedAt).
// Absent for a check, or once decided.
export function answerBy(request: ProofingRequest): string | undefined {
  return isDataRequest(request.flowKind) && isWaiting(request)
    ? request.purgeAt
    : undefined;
}

// What a reviewer may find wrong with a check, and the error code the
// rejection carries: the reviewer's own codes (reviewerRejectCodes in
// backend/internal/proofing/review.go), never one the wallet sets for another
// reason, which a later data request would skip or the person misread.
// "other" sends none: the service then uses MANUAL_REVIEW_REJECTED.
export const REJECT_REASONS = [
  { key: "someoneElse", errorCode: "REVIEW_NOT_HOLDER" },
  { key: "altered", errorCode: "REVIEW_ALTERED" },
  { key: "notAsked", errorCode: "EVIDENCE_NOT_AS_REQUESTED" },
  { key: "other", errorCode: undefined },
] as const;
export type RejectReasonKey = (typeof REJECT_REASONS)[number]["key"];
export const DEFAULT_REJECT_REASON: RejectReasonKey = "someoneElse";

// Why the reader may not decide an identity check: they sent it, or it is
// about them. Another reviewer decides it (the backend's ownReview, 403
// own_review). A data request is the person's own ask: whoever sent its link
// may decide it.
export type OwnReview = "sent" | "subject";

export function ownReview(
  request: Pick<
    ProofingRequest,
    "flowKind" | "requestedById" | "subjectUserId"
  >,
  userId: string | undefined,
): OwnReview | undefined {
  if (userId === undefined || isDataRequest(request.flowKind)) return undefined;
  if (request.requestedById === userId) return "sent";
  if (request.subjectUserId === userId) return "subject";
  return undefined;
}

export interface CheckDecisionDraft {
  choice: ProofingReviewInput["decision"] | undefined;
  rejectReason: RejectReasonKey;
  reason: string;
}

type CheckDecisionProblem = "choice" | "reason" | "reasonTooLong";

// What keeps a decision from being sent yet, in the order the form shows it.
export function checkDecisionProblems(
  draft: CheckDecisionDraft,
): CheckDecisionProblem[] {
  const problems: CheckDecisionProblem[] = [];
  if (draft.choice === undefined) problems.push("choice");
  if (draft.reason.trim() === "") problems.push("reason");
  else if ([...draft.reason.trim()].length > REVIEW_REASON_MAX_CHARS) {
    problems.push("reasonTooLong");
  }
  return problems;
}

// The body of a decision with no problems: a rejection names why, an
// approval has no code.
export function checkDecisionInput(
  draft: CheckDecisionDraft & { choice: ProofingReviewInput["decision"] },
): ProofingReviewInput {
  const input: ProofingReviewInput = {
    decision: draft.choice,
    reason: draft.reason.trim(),
  };
  if (draft.choice !== "reject") return input;

  const code = REJECT_REASONS.find(
    (reason) => reason.key === draft.rejectReason,
  )?.errorCode;
  if (code !== undefined) input.errorCode = code;
  return input;
}

// The result of one automatic check, as the review page lists it.
type AutomaticOutcome = "passed" | "failed" | "notPerformed";

export interface AutomaticCheck {
  key: "chipGenuine" | "chipNotCopy" | "livePerson" | "faceMatch";
  outcome: AutomaticOutcome | undefined;
  // The face match's score, 0 to 1.
  score?: number;
}

type Evidence = ProofingResult["evidence"][number];

// The service's words for a check's outcome; one it does not know has no
// outcome here and is left out rather than guessed.
function automaticOutcome(
  value: string | undefined,
): AutomaticOutcome | undefined {
  switch (value) {
    case "valid":
    case "passed":
      return "passed";
    case "invalid":
    case "failed":
      return "failed";
    case "not_performed":
      return "notPerformed";
    default:
      return undefined;
  }
}

// The checks the engine ran on the document and the face, those it reported.
// The face match carries its score, not an outcome.
export function automaticChecks(
  evidence: Evidence | undefined,
): AutomaticCheck[] {
  if (!evidence) return [];
  const reported: AutomaticCheck[] = [
    { key: "chipGenuine", outcome: automaticOutcome(evidence.passiveAuth) },
    { key: "chipNotCopy", outcome: automaticOutcome(evidence.activeAuth) },
    { key: "livePerson", outcome: automaticOutcome(evidence.liveness) },
  ];
  const checks = reported.filter((check) => check.outcome !== undefined);

  if (evidence.faceMatch !== undefined) {
    checks.push({
      key: "faceMatch",
      outcome: undefined,
      score: evidence.faceMatch,
    });
  }
  return checks;
}

// How a person was proven, beyond the document: the chip read, and a live
// face match.
type ProvenWithPart = "chip" | "faceMatch";

export function provenWithParts(
  evidence: Evidence | undefined,
): ProvenWithPart[] {
  if (!evidence) return [];
  const parts: ProvenWithPart[] = [];
  if (evidence.passiveAuth !== undefined) parts.push("chip");
  if (evidence.faceMatch !== undefined) parts.push("faceMatch");
  return parts;
}

// The document a result was proven with, as "Passport, Netherlands"; a result
// without one says so, and one the wallet cannot name shows its raw code.
export function evidenceDocumentLabel(
  evidence: ProofingResult["evidence"][number] | undefined,
  locale: string,
  t: TFunction,
): string | undefined {
  if (evidence === undefined) return undefined;
  if (evidence.type === "reference_photo") {
    return t("identityProofing.sessions.identity.noDocument");
  }

  const kind = evidence.documentType
    ? documentKindOf(evidence.documentType)
    : undefined;
  const label = [
    kind
      ? t(`identityProofingFlows.editor.documents.${kind}`)
      : evidence.documentType,
    evidence.issuingState
      ? issuingCountryName(evidence.issuingState, locale, t)
      : undefined,
  ]
    .filter(Boolean)
    .join(", ");

  return label === "" ? undefined : label;
}
