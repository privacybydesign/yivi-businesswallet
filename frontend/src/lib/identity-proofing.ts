import type { TFunction } from "i18next";
import { ApiError } from "../api/http";
import type {
  ProofingFlow,
  ProofingFlowSelection,
  ProofingFlowSpec,
} from "../api/identity-proofing";
import { errorCode } from "./api-error";

// Identity proofing request statuses as the backend reports them
// (internal/proofing). Values travel as plain strings, so an unknown one renders
// neutrally rather than breaking the page.
export type ProofingStatusTone = "default" | "green" | "amber" | "red" | "blue";

const TONES: Record<string, ProofingStatusTone> = {
  pending: "blue",
  in_progress: "blue",
  approved: "green",
  rejected: "red",
  needs_review: "amber",
  expired: "default",
};

// Statuses the proofing service may still change: the recipient is somewhere in
// the flow, or a reviewer at the service has not decided yet.
const LIVE_STATUSES = new Set(["pending", "in_progress", "needs_review"]);

export function isProofingLive(status: string): boolean {
  return LIVE_STATUSES.has(status);
}

export function proofingStatusTone(status: string): ProofingStatusTone {
  return TONES[status] ?? "default";
}

export function proofingStatusLabel(status: string, t: TFunction): string {
  switch (status) {
    case "pending":
      return t("identityProofing.status.pending");
    case "in_progress":
      return t("identityProofing.status.inProgress");
    case "approved":
      return t("identityProofing.status.approved");
    case "rejected":
      return t("identityProofing.status.rejected");
    case "needs_review":
      return t("identityProofing.status.needsReview");
    case "expired":
      return t("identityProofing.status.expired");
    default:
      return status;
  }
}

// The flow editor follows the identity-proofing-service's own admin page and
// its flow validation (flow.Validate), so the wallet can only build a flow the
// service accepts:
//
//   - Steps: document_capture (the document scan: vcmrtd reads the MRZ with the
//     camera to derive the chip access key) and nfc_read (the NFC chip read) are
//     always submitted together, so they toggle as a pair; face_verification is
//     one step covering selfie, liveness and face match.
//   - Checks: nfc.passive_auth is mandatory with nfc_read and face.match with
//     face_verification, so those are locked on; nfc.chip_auth and face.liveness
//     are optional; a check without its step is unavailable. A threshold exists
//     only for face.match.
//   - Requested data: each item is available only when the step that produces
//     it is in the flow, and cleared otherwise; none is forced on.
//   - Assurance: substantial needs (nfc.passive_auth or nfc.chip_auth) plus
//     face.match; high is always refused today. The service explains a refusal.
export const STEP_DOCUMENT_CAPTURE = "document_capture";
export const STEP_NFC_READ = "nfc_read";
export const STEP_FACE_VERIFICATION = "face_verification";
export const CHECK_PASSIVE_AUTH = "nfc.passive_auth";
export const CHECK_CHIP_AUTH = "nfc.chip_auth";
export const CHECK_FACE_MATCH = "face.match";
export const CHECK_LIVENESS = "face.liveness";

// The requested data the service's admin page offers, and the step that
// produces each: the document scan gives the document and holder (dg1), the
// chip read its extras, photo and check results, the face step its selfie and
// biometrics.
export const REQUESTED_ATTRIBUTES = [
  { value: "dg1", step: STEP_DOCUMENT_CAPTURE },
  { value: "dg11", step: STEP_NFC_READ },
  { value: "dg2", step: STEP_NFC_READ },
  { value: "chip_checks", step: STEP_NFC_READ },
  { value: "selfie", step: STEP_FACE_VERIFICATION },
  { value: "biometrics", step: STEP_FACE_VERIFICATION },
] as const;

export const ASSURANCE_LEVELS = ["low", "substantial", "high"] as const;

export const BSN_POLICIES = ["retrieve", "mask", "omit"] as const;

// "" inherits the tenant's policy at the proofing service.
export type Inherit<T extends string> = "" | T;
export type Tristate = "" | "true" | "false";

export interface ProofingFlowDraft {
  name: string;
  // document_capture and nfc_read, always together.
  documentAndChip: boolean;
  faceVerification: boolean;
  chipAuthentication: boolean;
  liveness: boolean;
  // A decimal 0..1 as typed; "" is pass/fail only.
  faceMatchThreshold: string;
  requestedAttributes: ReadonlySet<string>;
  // Comma-separated lists as typed; empty accepts any.
  acceptedDocumentTypes: string;
  acceptedIssuingCountries: string;
  assuranceLevel: Inherit<(typeof ASSURANCE_LEVELS)[number]>;
  bsnPolicy: Inherit<(typeof BSN_POLICIES)[number]>;
  blurFace: Tristate;
  blurBsn: Tristate;
  // Whole seconds as typed; "" or 0 is no override.
  retentionSeconds: string;
  // Settings the service's editor does not show, carried over unchanged from
  // the version being edited.
  carried: Pick<
    ProofingFlowSpec,
    "legalBasis" | "processingPurpose" | "assuranceTiers"
  >;
}

// The steps a draft sends, in the service's order.
export function draftSteps(draft: ProofingFlowDraft): string[] {
  const steps: string[] = [];
  if (draft.documentAndChip) {
    steps.push(STEP_DOCUMENT_CAPTURE, STEP_NFC_READ);
  }
  if (draft.faceVerification) {
    steps.push(STEP_FACE_VERIFICATION);
  }
  return steps;
}

// Whether a requested-data item's step is in the draft.
export function attributeAvailable(
  draft: ProofingFlowDraft,
  value: string,
): boolean {
  const attr = REQUESTED_ATTRIBUTES.find((a) => a.value === value);
  return attr !== undefined && draftSteps(draft).includes(attr.step);
}

// A new flow starts like the service's editor: every step and every requested
// data item on, the mandatory checks on.
export function emptyFlowDraft(): ProofingFlowDraft {
  return {
    name: "",
    documentAndChip: true,
    faceVerification: true,
    chipAuthentication: false,
    liveness: false,
    faceMatchThreshold: "",
    requestedAttributes: new Set(REQUESTED_ATTRIBUTES.map((a) => a.value)),
    acceptedDocumentTypes: "",
    acceptedIssuingCountries: "",
    assuranceLevel: "",
    bsnPolicy: "",
    blurFace: "",
    blurBsn: "",
    retentionSeconds: "",
    carried: {},
  };
}

function oneOf<T extends string>(
  values: readonly T[],
  value: string | undefined,
): Inherit<T> {
  return values.includes(value as T) ? (value as T) : "";
}

function tristate(value: boolean | undefined): Tristate {
  return value === undefined ? "" : value ? "true" : "false";
}

// The editor state for a new version of an existing flow.
export function draftFromFlow(flow: ProofingFlow): ProofingFlowDraft {
  const checks = new Set(flow.requiredChecks ?? []);
  const threshold = flow.checkThresholds?.[CHECK_FACE_MATCH];
  return {
    name: flow.name,
    documentAndChip:
      flow.steps.includes(STEP_DOCUMENT_CAPTURE) ||
      flow.steps.includes(STEP_NFC_READ),
    faceVerification: flow.steps.includes(STEP_FACE_VERIFICATION),
    chipAuthentication: checks.has(CHECK_CHIP_AUTH),
    liveness: checks.has(CHECK_LIVENESS),
    faceMatchThreshold: threshold === undefined ? "" : String(threshold),
    requestedAttributes: new Set(flow.requestedAttributes ?? []),
    acceptedDocumentTypes: (flow.acceptedDocumentTypes ?? []).join(", "),
    acceptedIssuingCountries: (flow.acceptedIssuingCountries ?? []).join(", "),
    assuranceLevel: oneOf(ASSURANCE_LEVELS, flow.requiredAssuranceLevel),
    bsnPolicy: oneOf(BSN_POLICIES, flow.bsnPolicy),
    blurFace: tristate(flow.blurFace),
    blurBsn: tristate(flow.blurBsn),
    retentionSeconds: flow.retentionOverrideSeconds
      ? String(flow.retentionOverrideSeconds)
      : "",
    carried: {
      legalBasis: flow.legalBasis,
      processingPurpose: flow.processingPurpose,
      assuranceTiers: flow.assuranceTiers,
    },
  };
}

function list(raw: string): string[] {
  return raw
    .split(",")
    .map((item) => item.trim())
    .filter((item) => item !== "");
}

// A draft the editor cannot send, with the field to fix; null when valid. The
// proofing service checks the rest and explains its own refusal.
export type FlowDraftError =
  | "name"
  | "steps"
  | "faceMatchThreshold"
  | "retentionSeconds";

export function flowDraftError(
  draft: ProofingFlowDraft,
): FlowDraftError | null {
  if (draft.name.trim() === "") {
    return "name";
  }
  if (draftSteps(draft).length === 0) {
    return "steps";
  }
  if (draft.faceVerification && draft.faceMatchThreshold.trim() !== "") {
    const value = Number(draft.faceMatchThreshold);
    if (!Number.isFinite(value) || value < 0 || value > 1) {
      return "faceMatchThreshold";
    }
  }
  if (draft.retentionSeconds.trim() !== "") {
    const seconds = Number(draft.retentionSeconds);
    if (!Number.isInteger(seconds) || seconds < 0) {
      return "retentionSeconds";
    }
  }
  return null;
}

// The request body for a draft, as the service's editor builds it. Call only
// on a draft flowDraftError accepts.
export function flowSpecFromDraft(draft: ProofingFlowDraft): ProofingFlowSpec {
  const requiredChecks: string[] = [];
  if (draft.documentAndChip) {
    requiredChecks.push(CHECK_PASSIVE_AUTH);
    if (draft.chipAuthentication) {
      requiredChecks.push(CHECK_CHIP_AUTH);
    }
  }
  if (draft.faceVerification) {
    requiredChecks.push(CHECK_FACE_MATCH);
    if (draft.liveness) {
      requiredChecks.push(CHECK_LIVENESS);
    }
  }
  const spec: ProofingFlowSpec = {
    ...draft.carried,
    name: draft.name.trim(),
    steps: draftSteps(draft),
    requiredChecks,
    requestedAttributes: REQUESTED_ATTRIBUTES.map((a) => a.value).filter(
      (value) =>
        draft.requestedAttributes.has(value) &&
        attributeAvailable(draft, value),
    ),
    acceptedDocumentTypes: list(draft.acceptedDocumentTypes),
    acceptedIssuingCountries: list(draft.acceptedIssuingCountries).map(
      (country) => country.toUpperCase(),
    ),
  };
  if (draft.faceVerification && draft.faceMatchThreshold.trim() !== "") {
    spec.checkThresholds = {
      [CHECK_FACE_MATCH]: Number(draft.faceMatchThreshold),
    };
  }
  if (draft.assuranceLevel !== "") {
    spec.requiredAssuranceLevel = draft.assuranceLevel;
  }
  if (draft.bsnPolicy !== "") {
    spec.bsnPolicy = draft.bsnPolicy;
  }
  if (draft.blurFace !== "") {
    spec.blurFace = draft.blurFace === "true";
  }
  if (draft.blurBsn !== "") {
    spec.blurBsn = draft.blurBsn === "true";
  }
  const retention = Number(draft.retentionSeconds);
  if (draft.retentionSeconds.trim() !== "" && retention > 0) {
    spec.retentionOverrideSeconds = retention;
  }
  return spec;
}

// The newest request sent to each member, keyed by user id: what the member
// table shows as their proofing status. Requests arrive newest first.
export function latestRequestByMember<
  T extends { subjectUserId?: string | undefined },
>(requests: readonly T[]): Map<string, T> {
  const out = new Map<string, T>();
  for (const request of requests) {
    if (request.subjectUserId && !out.has(request.subjectUserId)) {
      out.set(request.subjectUserId, request);
    }
  }
  return out;
}

// The flows a member may send a request on, and the one the form starts on:
// the admin's default, else the first available (a default the proofing
// service no longer lists is simply absent).
export function sendableFlows<T extends { allowed: boolean; default: boolean }>(
  flows: readonly T[],
): { sendable: T[]; initial: T | undefined } {
  return withInitial(flows.filter((flow) => flow.allowed));
}

export function assignedFlows<
  T extends { assigned: boolean; default: boolean },
>(flows: readonly T[]): { sendable: T[]; initial: T | undefined } {
  return withInitial(flows.filter((flow) => flow.assigned));
}

function withInitial<T extends { default: boolean }>(
  sendable: T[],
): { sendable: T[]; initial: T | undefined } {
  return {
    sendable,
    initial: sendable.find((flow) => flow.default) ?? sendable[0],
  };
}

export function editedFlowSelection(
  flows: readonly { id: string }[],
  ticked: ReadonlySet<string>,
  defaultId: string,
  saved: ProofingFlowSelection,
): { selection: ProofingFlowSelection; dirty: boolean } {
  const flowIds = flows.filter((f) => ticked.has(f.id)).map((f) => f.id);
  const defaultFlowId = ticked.has(defaultId) ? defaultId : (flowIds[0] ?? "");
  const dirty =
    saved.flowIds.length !== flowIds.length ||
    saved.flowIds.some((id) => !ticked.has(id)) ||
    saved.defaultFlowId !== defaultFlowId;
  return { selection: { flowIds, defaultFlowId }, dirty };
}

export function requestSubject(request: {
  subjectName: string;
  subjectEmail: string;
  proofedName?: string | undefined;
}): { name: string; verifiedAs: string | undefined } {
  const given = request.subjectName.trim();
  const proofed = request.proofedName?.trim() ?? "";
  const name = given || proofed || request.subjectEmail;
  return {
    name,
    verifiedAs: proofed !== "" && proofed !== name ? proofed : undefined,
  };
}

// The backend's own message for an error whose text is meant for the reader:
// invalid input, or the proofing service's explanation of why it refused a flow.
function serverMessage(error: unknown): string | null {
  if (
    error instanceof ApiError &&
    typeof error.body === "object" &&
    error.body !== null &&
    "error" in error.body &&
    typeof error.body.error === "string"
  ) {
    return error.body.error;
  }
  return null;
}

// Why the proofing service did not approve a session, from its errorCode. An
// unknown code is shown as is, so a new one still says something.
export function proofingRejectionReason(code: string, t: TFunction): string {
  switch (code) {
    case "DOCUMENT_TYPE_NOT_ACCEPTED":
      return t("identityProofing.rejectionReasons.documentTypeNotAccepted");
    case "DOCUMENT_COUNTRY_NOT_ACCEPTED":
      return t("identityProofing.rejectionReasons.documentCountryNotAccepted");
    case "FACE_STEP_NOT_COMPLETED":
      return t("identityProofing.rejectionReasons.faceStepNotCompleted");
    case "FACE_NO_MATCH":
      return t("identityProofing.rejectionReasons.faceNoMatch");
    case "DOC_TAMPERED":
      return t("identityProofing.rejectionReasons.docTampered");
    case "CHIP_CLONE_DETECTED":
      return t("identityProofing.rejectionReasons.chipCloneDetected");
    case "DOC_EXPIRED":
      return t("identityProofing.rejectionReasons.docExpired");
    default:
      return code;
  }
}

// The copy an identity proofing API error shows, keyed on its stable code.
export function proofingErrorMessage(error: unknown, t: TFunction): string {
  switch (errorCode(error)) {
    case "no_encryption_key":
      return t("identityProofing.errors.noEncryptionKey");
    case "flow_not_found":
      return t("identityProofing.errors.flowNotFound");
    case "flow_not_completable":
      return t("identityProofing.errors.flowNotCompletable");
    case "flow_not_allowed":
      return t("identityProofing.errors.flowNotAllowed");
    case "member_not_found":
      return t("identityProofing.errors.memberNotFound");
    case "customer_not_found":
      return t("identityProofing.errors.customerNotFound");
    case "customer_exists":
      return t("identityProofing.errors.customerExists");
    case "flow_not_assigned":
      return t("identityProofing.errors.flowNotAssigned");
    case "invalid_input":
    case "rejected_by_provider":
      return serverMessage(error) ?? t("identityProofing.errors.generic");
    default:
      return t("identityProofing.errors.generic");
  }
}
