import { isDataRequest } from "../api/identity-proofing";
import type { Me } from "../api/auth";
import type {
  AddressReviewState,
  ProofingRequest,
  ProofingResult,
  ProofingReviewInput,
  ReviewItemCounts,
  StepReviewKind,
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
export const REVIEW_TABS = [
  "all",
  "checks",
  "steps",
  "data",
  "closed",
] as const;
export type ReviewTab = (typeof REVIEW_TABS)[number];
export const DEFAULT_REVIEW_TAB: ReviewTab = "all";

export function reviewTabFrom(raw: string | null): ReviewTab {
  return REVIEW_TABS.find((tab) => tab === raw) ?? DEFAULT_REVIEW_TAB;
}

// The steps a reviewer decides, in the order the review walks through them:
// the Verzekeringsbericht, the diplomas, then the address.
const STEP_REVIEW_ORDER = ["employment", "diplomas", "address"] as const;

// Where a step's review stands; undefined for none.
export function reviewOfStep(
  request: ProofingRequest,
  step: StepReviewKind,
): AddressReviewState | undefined {
  switch (step) {
    case "employment":
      return request.employmentReview;
    case "diplomas":
      return request.diplomasReview;
    case "address":
      return request.addressReview;
  }
}

// When a step's review was decided or lapsed.
function stepReviewedAt(
  request: ProofingRequest,
  step: StepReviewKind,
): string | undefined {
  switch (step) {
    case "employment":
      return request.employmentReviewedAt;
    case "diplomas":
      return request.diplomasReviewedAt;
    case "address":
      return request.addressReviewedAt;
  }
}

// The step a request is reviewed for, apart from the check itself, which is
// decided: the first waiting in STEP_REVIEW_ORDER, else the first decided.
// Undefined for none.
export function stepReviewOf(
  request: ProofingRequest,
): StepReviewKind | undefined {
  if (request.flowKind !== "identity" || request.status === "needs_review") {
    return undefined;
  }
  return (
    STEP_REVIEW_ORDER.find((s) => reviewOfStep(request, s) === "pending") ??
    STEP_REVIEW_ORDER.find((s) => reviewOfStep(request, s) !== undefined)
  );
}

// What a reviewer decides on an identity check's session, in the order the
// review walks through it: the identity, then its steps (STEP_REVIEW_ORDER).
// Only what waits.
export type ReviewPart = "identity" | StepReviewKind;

export function waitingParts(request: ProofingRequest): ReviewPart[] {
  if (request.flowKind !== "identity") return [];
  return [
    ...(request.status === "needs_review" ? (["identity"] as const) : []),
    ...STEP_REVIEW_ORDER.filter((s) => reviewOfStep(request, s) === "pending"),
  ];
}

// What a reviewer decided on an identity check's session, in the same order.
export function decidedParts(request: ProofingRequest): ReviewPart[] {
  if (request.flowKind !== "identity") return [];
  const identityDecided =
    request.identityReviewedAt !== undefined &&
    request.status !== "needs_review";
  return [
    ...(identityDecided ? (["identity"] as const) : []),
    ...STEP_REVIEW_ORDER.filter((s) => {
      const state = reviewOfStep(request, s);
      return state !== undefined && state !== "pending";
    }),
  ];
}

// A session's review as steps: the parts decided, then those that wait, and
// the index of the one the page shows (the first waiting; past the end once
// closed). Empty for a data request.
export function reviewSteps(request: ProofingRequest): {
  parts: ReviewPart[];
  current: number;
} {
  const decided = decidedParts(request);
  return {
    parts: [...decided, ...waitingParts(request)],
    current: decided.length,
  };
}

// Whether a session's review is over: something was decided and nothing waits.
export function isClosedSession(request: ProofingRequest): boolean {
  return waitingParts(request).length === 0 && decidedParts(request).length > 0;
}

// The label of each part, as the Reviews page names it.
export const REVIEW_PART_KEYS = {
  identity: "identityProofing.reviews.identityCheck",
  employment: "identityProofing.reviews.employmentCheck",
  diplomas: "identityProofing.reviews.diplomasCheck",
  address: "identityProofing.reviews.addressCheck",
} as const satisfies Record<ReviewPart, string>;

// The part the review goes on with once decided was decided, if any.
export function nextPart(
  request: ProofingRequest,
  decided: ReviewPart,
): ReviewPart | undefined {
  return waitingParts(request).find((part) => part !== decided);
}

// Whether the request waits for a decision on its self-declared address
// alone: the check itself is decided.
export function isAddressReview(request: ProofingRequest): boolean {
  return stepReviewOf(request) === "address";
}

// Where the step's review a request is reviewed for stands.
export function stepReviewState(
  request: ProofingRequest,
): AddressReviewState | undefined {
  const step = stepReviewOf(request);
  return step === undefined ? undefined : reviewOfStep(request, step);
}

// Whether the check itself waits for a decision. A purged one has no person
// left to decide on.
function isInReview(request: ProofingRequest): boolean {
  return request.status === "needs_review" && request.purgedAt === undefined;
}

// Whether the request waits for a decision: in review, or one of its steps is.
function isWaiting(request: ProofingRequest): boolean {
  return (
    isInReview(request) ||
    (STEP_REVIEW_ORDER.some((s) => reviewOfStep(request, s) === "pending") &&
      request.purgedAt === undefined)
  );
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
  steps: number;
  data: number;
}

// The tabs' counts: every session once, those whose identity waits, those
// with a step waiting (a session with both is in each), and data requests.
export function reviewCounts(waiting: ReviewItemCounts): ReviewCounts {
  return {
    all: waiting.sessions,
    checks: waiting.counts.identity,
    steps: waiting.stepSessions,
    data: waiting.counts.data_request,
  };
}

// How many items wait, of every kind.
export function waitingCount(waiting: ReviewItemCounts): number {
  return reviewCounts(waiting).all;
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
      return waiting.filter((r) => waitingParts(r).includes("identity"));
    case "steps":
      return waiting.filter((r) =>
        waitingParts(r).some((part) => part !== "identity"),
      );
    case "data":
      return waiting.filter((r) => isDataRequest(r.flowKind));
    case "closed":
      return [];
  }
}

// The key, under a step's review texts, of what became of its decided review.
export const STEP_DECIDED_KEYS = {
  confirmed: "confirmed",
  rejected: "rejected",
  lapsed: "lapsed",
} as const;

// Whether a reviewer confirmed or rejected the step the request is reviewed
// for (its address, its Verzekeringsbericht), or nobody did in time.
export function isDecidedStep(request: ProofingRequest): boolean {
  const state = stepReviewState(request);
  return state !== undefined && state !== "pending";
}

// When a closed request closed: its last part was decided or lapsed, or it
// ended.
export function closedAt(request: ProofingRequest): string {
  const decided = decidedParts(request)
    .map((part) =>
      part === "identity"
        ? request.identityReviewedAt
        : stepReviewedAt(request, part),
    )
    .filter((when): when is string => when !== undefined)
    .sort((a, b) => Date.parse(b) - Date.parse(a));
  return decided[0] ?? request.completedAt ?? request.createdAt;
}

// What was closed, the latest first: the sessions whose review is over (its
// identity, its steps, each decided), and the data requests that ended,
// whoever ended them: a reviewer, or no one (the engine rejected the person,
// the review lapsed, the org paused, the law keeps the data). A data request
// carries no mark of which, so the page claims no decision and shows each
// one's error code.
export function closedRequests(
  requests: readonly ProofingRequest[],
): ProofingRequest[] {
  return requests
    .filter(
      (r) =>
        isClosedSession(r) ||
        (isDataRequest(r.flowKind) &&
          (r.status === "approved" || r.status === "rejected")),
    )
    .sort((a, b) => Date.parse(closedAt(b)) - Date.parse(closedAt(a)));
}

// Until when a data request in review can be answered: its review lapses then
// (REVIEW_LAPSED, purge_at), a calendar month after it went there (askedAt).
// A step review lapses at its own due time. Absent for an identity check, or
// once decided.
export function answerBy(request: ProofingRequest): string | undefined {
  if (isDataRequest(request.flowKind)) {
    return isWaiting(request) ? request.purgeAt : undefined;
  }
  return stepReviewOf(request) === undefined
    ? undefined
    : request.stepReviewDueAt;
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

// Why a kept Verzekeringsbericht went to review
// (proofing.ReviewNoCurrentEmployment, ReviewCurrentBenefit,
// ReviewCurrentOtherIncome): it lists no current job, or a current UWV
// benefit or other income beside one.
export const WALLET_REVIEW_REASONS = [
  "no_current_employment",
  "current_benefit",
  "current_other_income",
] as const;
export type WalletReviewReason = (typeof WALLET_REVIEW_REASONS)[number];

export function walletReviewReason(
  request: Pick<ProofingRequest, "reviewReason">,
): WalletReviewReason | undefined {
  return WALLET_REVIEW_REASONS.find(
    (reason) => reason === request.reviewReason,
  );
}

// Why an address waits: it differs from the registered address (BRP), it is
// the registered one on a flow that reviews every address, or no register
// confirms it.
export type AddressReviewWhy = "addressMismatch" | "addressAlways" | "address";

export function addressReviewWhy(
  request: Pick<ProofingRequest, "address">,
): AddressReviewWhy {
  switch (request.address?.verification) {
    case "registered_mismatch":
      return "addressMismatch";
    case "registered":
      return "addressAlways";
    default:
      return "address";
  }
}

// Why a Verzekeringsbericht waits: what it lists, else its flow reviewing
// every statement.
export function employmentReviewWhy(
  request: Pick<ProofingRequest, "reviewReason" | "employmentMode">,
): WalletReviewReason | "employmentAlways" {
  const reason = walletReviewReason(request);
  if (reason !== undefined) {
    return reason;
  }
  return request.employmentMode === "always_review"
    ? "employmentAlways"
    : "no_current_employment";
}

// Why an identity check waits: its flow sending every session to review;
// undefined is the engine's doubt.
export type CheckReviewReason = "alwaysReview";

export function checkReviewReason(
  request: Pick<ProofingRequest, "flowAlwaysReview">,
): CheckReviewReason | undefined {
  return request.flowAlwaysReview === true ? "alwaysReview" : undefined;
}

// Why the reader may not decide an identity check: they sent it, or it is
// about them. Another reviewer decides it (the backend's ownReview, 403
// own_review), unless the backend lets the reader (reviewsOwn: a platform
// admin on a dev stack). A data request is the person's own ask: whoever sent
// its link may decide it.
export type OwnReview = "sent" | "subject";

export function ownReview(
  request: Pick<
    ProofingRequest,
    "flowKind" | "requestedById" | "subjectUserId"
  >,
  reader: Pick<Me, "id" | "reviewsOwn"> | null | undefined,
): OwnReview | undefined {
  if (reader == null || reader.reviewsOwn || isDataRequest(request.flowKind)) {
    return undefined;
  }
  if (request.requestedById === reader.id) return "sent";
  if (request.subjectUserId === reader.id) return "subject";
  return undefined;
}

export interface CheckDecisionDraft {
  choice: ProofingReviewInput["decision"] | undefined;
  rejectReason: RejectReasonKey;
  reason: string;
}

type ReasonProblem = "reason" | "reasonTooLong";
type CheckDecisionProblem = "choice" | ReasonProblem;

// What keeps a decision's reason from being sent: none, or too long.
export function reasonProblem(reason: string): ReasonProblem | undefined {
  const trimmed = reason.trim();
  if (trimmed === "") return "reason";
  if ([...trimmed].length > REVIEW_REASON_MAX_CHARS) return "reasonTooLong";
  return undefined;
}

// What keeps a decision from being sent yet, in the order the form shows it.
export function checkDecisionProblems(
  draft: CheckDecisionDraft,
): CheckDecisionProblem[] {
  const problems: CheckDecisionProblem[] = [];
  if (draft.choice === undefined) problems.push("choice");
  const reason = reasonProblem(draft.reason);
  if (reason !== undefined) problems.push(reason);
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
