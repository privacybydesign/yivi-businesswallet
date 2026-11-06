import type { ProofingRequest, ProofingStep } from "../api/identity-proofing";

// How a session's extra step reads, the same words for every step.
export type PartState =
  | "not_started"
  | "open"
  | "to_review"
  | "done"
  | "accepted"
  | "rejected"
  | "lapsed"
  | "not_provided";

export const PART_TONES = {
  not_started: "default",
  open: "default",
  to_review: "amber",
  done: "green",
  accepted: "green",
  rejected: "red",
  lapsed: "amber",
  not_provided: "default",
} as const satisfies Record<PartState, string>;

// The steps the wallet names; another one reads as its kind.
export const PART_KINDS = ["address", "diplomas", "employment"] as const;
export type PartKind = (typeof PART_KINDS)[number];

export function isPartKind(kind: string): kind is PartKind {
  return (PART_KINDS as readonly string[]).includes(kind);
}

const REVIEW_LAPSED = "REVIEW_LAPSED";

export function partState(step: ProofingStep): PartState {
  switch (step.state) {
    case "open":
    case "not_started":
    case "not_provided":
      return step.state;
    case "in_review":
      return "to_review";
    case "done":
      return step.reviewed ? "accepted" : "done";
    case "rejected":
      return step.code === REVIEW_LAPSED ? "lapsed" : "rejected";
    default:
      return "not_started";
  }
}

// Where a session stands as a whole: its identity and every step.
export type SessionSummary =
  | "needs_review"
  | "in_progress"
  | "incomplete"
  | "complete";

export const SUMMARY_TONES = {
  needs_review: "amber",
  in_progress: "default",
  incomplete: "red",
  complete: "green",
} as const satisfies Record<SessionSummary, string>;

// undefined for a session without steps, or whose identity check did not
// pass: its status says it all then.
export function sessionSummary(
  request: Pick<ProofingRequest, "status" | "steps">,
): SessionSummary | undefined {
  const steps = (request.steps ?? []).map(partState);
  if (steps.length === 0) return undefined;
  if (request.status === "needs_review" || steps.includes("to_review")) {
    return "needs_review";
  }
  if (request.status !== "approved") return undefined;
  if (steps.some((s) => s === "open" || s === "not_started")) {
    return "in_progress";
  }
  if (steps.every((s) => s === "done" || s === "accepted")) return "complete";
  return "incomplete";
}

// The states of the steps a summary is about: what waits, what did not pass,
// what is still open. None for a complete session.
const SUMMARY_PARTS = {
  needs_review: ["to_review"],
  in_progress: ["open", "not_started"],
  incomplete: ["rejected", "lapsed", "not_provided"],
  complete: [],
} as const satisfies Record<SessionSummary, readonly PartState[]>;

// The kinds of the steps summary is about, in the order the page asks for them.
export function summaryKinds(
  request: Pick<ProofingRequest, "steps">,
  summary: SessionSummary,
): string[] {
  const states: readonly PartState[] = SUMMARY_PARTS[summary];
  return (request.steps ?? [])
    .filter((step) => states.includes(partState(step)))
    .map((step) => step.kind);
}
