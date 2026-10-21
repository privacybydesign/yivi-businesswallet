import type { TFunction } from "i18next";
import { ApiError } from "../api/http";
import { isDataRequest } from "../api/identity-proofing";
import type {
  AddressMode,
  AddressVerification,
  DiplomaMode,
  FlowKind,
  ProofingChannel,
  ProofingFlow,
  ProofingFlowSelection,
  ProofingFlowSpec,
  ProofingImage,
  ProofingRequest,
  RegisterAddress,
} from "../api/identity-proofing";
import type { AuditEvent } from "../api/organization";
import { errorCode } from "./api-error";
import { auditActorLabel } from "./audit-event";
import { fullName } from "./name";

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

// Statuses the recipient may still change by acting: worth polling and keeping a
// session on screen for. needs_review waits on a reviewer, not on the recipient.
const LIVE_STATUSES = new Set(["pending", "in_progress"]);

export function isProofingLive(status: string): boolean {
  return LIVE_STATUSES.has(status);
}

// Statuses a poll never recovers from: the caller may not read this session
// (403), or it is gone (404). Polling on would only repeat the refusal.
const FINAL_POLL_STATUSES: readonly number[] = [403, 404];

// Whether a verify page keeps polling its progress: until the first read, and
// while the session is live, unless a read was refused for good.
export function progressPollContinues(
  progress: { status: string } | undefined,
  error: Error | null,
): boolean {
  if (refusedForGood(error)) {
    return false;
  }
  return progress === undefined || isProofingLive(progress.status);
}

// Whether a verify page keeps re-reading a session in review, slowly: a
// person decides it, or its customer's erasure setting seconds after it got
// there, while the subject may still be watching.
export function reviewPollContinues(
  progress: { status: string } | undefined,
  error: Error | null,
): boolean {
  return !refusedForGood(error) && progress?.status === "needs_review";
}

function refusedForGood(error: Error | null): boolean {
  return (
    error instanceof ApiError && FINAL_POLL_STATUSES.includes(error.status)
  );
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
    case "cancelled":
      return t("identityProofing.status.cancelled");
    default:
      return status;
  }
}

// The flow editor follows the engine's flow validation (flow.Validate), so it
// can only build a flow the engine accepts:
//
//   - Steps: document_capture (the document scan: vcmrtd reads the MRZ with the
//     camera to derive the chip access key) and nfc_read (the NFC chip read) are
//     always submitted together, so they toggle as a pair; document_photo (a
//     photo of the document's printed page) stands on its own and is listed
//     right after document_capture, as the Idem app takes it: the MRZ scan
//     photographs the side it reads; face_verification is one step covering
//     selfie, liveness and face match.
//   - Checks: nfc.passive_auth is mandatory with nfc_read and face.match with
//     face_verification, so those are locked on; nfc.chip_auth and face.liveness
//     are optional; a check without its step is unavailable. A threshold exists
//     only for face.match.
//   - Requested data: each item is available only when the step that produces
//     it is in the flow, and cleared otherwise; none is forced on.
//   - Assurance (flow.LevelRequirements): low needs nfc.passive_auth;
//     substantial also nfc.chip_auth, face.match and face.liveness with Regula.
//     The level is only a minimum and turns nothing on: a draft whose settings
//     cannot reach it is refused. High is never reachable. The engine explains
//     a refusal.
export const STEP_DOCUMENT_CAPTURE = "document_capture";
export const STEP_NFC_READ = "nfc_read";
export const STEP_DOCUMENT_PHOTO = "document_photo";
export const STEP_FACE_VERIFICATION = "face_verification";
export const CHECK_PASSIVE_AUTH = "nfc.passive_auth";
export const CHECK_CHIP_AUTH = "nfc.chip_auth";
export const CHECK_FACE_MATCH = "face.match";
export const CHECK_LIVENESS = "face.liveness";

// The requested data the service's admin page offers, and the step that
// produces each: the document scan gives the document and holder (dg1), the
// chip read the age check, its extras, photo and check results, the face step
// its selfie and biometrics.
export const REQUESTED_ATTRIBUTES = [
  { value: "dg1", step: STEP_DOCUMENT_CAPTURE },
  { value: "age_over", step: STEP_NFC_READ },
  { value: "dg11", step: STEP_NFC_READ },
  { value: "dg2", step: STEP_NFC_READ },
  { value: "chip_checks", step: STEP_NFC_READ },
  { value: "document_image", step: STEP_DOCUMENT_PHOTO },
  { value: "selfie", step: STEP_FACE_VERIFICATION },
  { value: "biometrics", step: STEP_FACE_VERIFICATION },
] as const;

// The requested data of a flow that releases nothing but the outcome
// (proofingengine.attrOutcomeOnly): an empty list releases everything the
// steps collect, so a flow with no data ticked sends this instead.
export const OUTCOME_ONLY = "outcome_only";

// The eIDAS levels the proofing engine can claim (flow.LevelRequirements);
// high is not among them.
export const ASSURANCE_LEVELS = ["low", "substantial"] as const;
export type AssuranceLevel = (typeof ASSURANCE_LEVELS)[number];

function isAssuranceLevel(value: string): value is AssuranceLevel {
  return (ASSURANCE_LEVELS as readonly string[]).includes(value);
}

// An eIDAS level in words; one the wallet does not know is shown as is.
export function assuranceLevelLabel(level: string, t: TFunction): string {
  return isAssuranceLevel(level)
    ? t(`identityProofingFlows.assuranceLevels.${level}`)
    : level;
}

export const BSN_POLICIES = ["retrieve", "mask", "omit"] as const;

// What verifies the face step; a new flow starts on the first, Regula. The
// wallet runs no face engine of its own, so there is no "engine", and the
// proofing engine verifies faces with Regula only, so Iris is not offered.
export const FACE_PROVIDERS = ["regula"] as const;
export type FaceProvider = (typeof FACE_PROVIDERS)[number];

// The service's steps that capture the face (proofing.faceSteps).
const FACE_STEPS = ["face_verification", "selfie", "liveness", "face_match"];
// The selfie location that takes the face in the browser
// (proofing.selfieLocationBrowser).
const SELFIE_LOCATION_BROWSER = "browser";

// The eIDAS level a Yivi session reaches (proofing.yiviEIDASLevel).
const YIVI_EIDAS_LEVEL = "low";

// Whether the Yivi app can run a flow (proofing.yiviAppAvailable): unless it
// photographs the document, which only the Idem app does, matches the face
// against a customer's reference photo (proofing.NeedsReferencePhoto: a face
// step without the chip read, outside the browser), where the Yivi app
// matches against its credential's photo instead, or requires a level above
// what a Yivi session reaches.
export function yiviAppAvailable(flow: {
  steps: readonly string[];
  selfieLocation?: string | undefined;
  requiredAssuranceLevel?: string | undefined;
}): boolean {
  const needsReferencePhoto =
    flow.steps.some((step) => FACE_STEPS.includes(step)) &&
    !flow.steps.includes(STEP_NFC_READ) &&
    flow.selfieLocation !== SELFIE_LOCATION_BROWSER;
  const levelMet =
    !flow.requiredAssuranceLevel ||
    flow.requiredAssuranceLevel === YIVI_EIDAS_LEVEL;
  return (
    !flow.steps.includes(STEP_DOCUMENT_PHOTO) &&
    !needsReferencePhoto &&
    levelMet
  );
}

// "" is the proofing service's default: omit for the BSN policy, off for the
// covers.
export type Inherit<T extends string> = "" | T;
export type Tristate = "" | "true" | "false";

export interface ProofingFlowDraft {
  name: string;
  // document_capture and nfc_read, always together.
  documentAndChip: boolean;
  documentPhoto: boolean;
  faceVerification: boolean;
  chipAuthentication: boolean;
  liveness: boolean;
  faceProvider: FaceProvider;
  // A decimal 0..1 as typed; "" is pass/fail only.
  faceMatchThreshold: string;
  requestedAttributes: ReadonlySet<string>;
  // Whole years as typed, the age the age check answers for.
  ageOver: string;
  // Comma-separated lists as typed; empty accepts any.
  acceptedDocumentTypes: string;
  acceptedIssuingCountries: string;
  assuranceLevel: Inherit<AssuranceLevel>;
  bsnPolicy: Inherit<(typeof BSN_POLICIES)[number]>;
  keepFacePhotos: Tristate;
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

// The steps a draft sends, in the order the Idem app runs them: the document
// photo with the document scan, before the chip read.
export function draftSteps(draft: ProofingFlowDraft): string[] {
  const steps: string[] = [];
  if (draft.documentAndChip) {
    steps.push(STEP_DOCUMENT_CAPTURE);
  }
  if (draft.documentPhoto) {
    steps.push(STEP_DOCUMENT_PHOTO);
  }
  if (draft.documentAndChip) {
    steps.push(STEP_NFC_READ);
  }
  if (draft.faceVerification) {
    steps.push(STEP_FACE_VERIFICATION);
  }
  return steps;
}

// The draft settings a level needs, as the proofing engine requires them
// (flow.LevelRequirements): low a chip read whose data verifies; substantial
// also the copy check and a live face matched by Regula against the chip photo.
type LevelRequirement = Partial<
  Pick<
    ProofingFlowDraft,
    | "documentAndChip"
    | "chipAuthentication"
    | "faceVerification"
    | "liveness"
    | "faceProvider"
  >
>;

const LEVEL_REQUIREMENTS: Record<AssuranceLevel, LevelRequirement> = {
  low: { documentAndChip: true },
  substantial: {
    documentAndChip: true,
    chipAuthentication: true,
    faceVerification: true,
    liveness: true,
    faceProvider: "regula",
  },
};

// What a draft's assurance level needs; nothing without a level.
export function levelRequirement(
  level: Inherit<AssuranceLevel>,
): LevelRequirement {
  return level === "" ? {} : LEVEL_REQUIREMENTS[level];
}

// Whether the draft's own settings can reach its required level. The level
// changes no setting: a draft that cannot reach it is not saved.
export function reachesAssuranceLevel(draft: ProofingFlowDraft): boolean {
  return Object.entries(levelRequirement(draft.assuranceLevel)).every(
    ([setting, value]) => draft[setting as keyof LevelRequirement] === value,
  );
}

// Whether a requested-data item's step is in the draft.
export function attributeAvailable(
  draft: ProofingFlowDraft,
  value: string,
): boolean {
  const attr = REQUESTED_ATTRIBUTES.find((a) => a.value === value);
  return attr !== undefined && draftSteps(draft).includes(attr.step);
}

// A new flow starts like the service's editor: every step the Idem app runs
// and every requested data item on, the mandatory checks on. The document
// photo starts off: the app cannot take it yet.
export function emptyFlowDraft(): ProofingFlowDraft {
  return {
    name: "",
    documentAndChip: true,
    documentPhoto: false,
    faceVerification: true,
    chipAuthentication: false,
    liveness: false,
    faceProvider: FACE_PROVIDERS[0],
    faceMatchThreshold: "",
    // The age check releases less than the document data it would sit next
    // to, so it starts off.
    requestedAttributes: new Set(
      REQUESTED_ATTRIBUTES.map((a) => a.value).filter(
        (value) => value !== ATTRIBUTE_AGE_OVER,
      ),
    ),
    ageOver: String(DEFAULT_AGE_OVER),
    acceptedDocumentTypes: "",
    acceptedIssuingCountries: "",
    assuranceLevel: "",
    bsnPolicy: "",
    keepFacePhotos: "",
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

// A flow as any list returns it (the org's, or a customer's), without the
// list's own selection flags: what the editor needs to seed a new version.
export type EditableFlow = Omit<ProofingFlow, "allowed" | "default">;

// The editor state for a new version of an existing flow, as stored.
export function draftFromFlow(flow: EditableFlow): ProofingFlowDraft {
  const checks = new Set(flow.requiredChecks ?? []);
  const threshold = flow.checkThresholds?.[CHECK_FACE_MATCH];
  const draft: ProofingFlowDraft = {
    name: flow.name,
    documentAndChip:
      flow.steps.includes(STEP_DOCUMENT_CAPTURE) ||
      flow.steps.includes(STEP_NFC_READ),
    documentPhoto: flow.steps.includes(STEP_DOCUMENT_PHOTO),
    faceVerification: flow.steps.includes(STEP_FACE_VERIFICATION),
    chipAuthentication: checks.has(CHECK_CHIP_AUTH),
    liveness: checks.has(CHECK_LIVENESS),
    faceProvider: oneOf(FACE_PROVIDERS, flow.faceProvider) || FACE_PROVIDERS[0],
    faceMatchThreshold: threshold === undefined ? "" : String(threshold),
    requestedAttributes: new Set(flow.requestedAttributes ?? []),
    ageOver: String(flow.ageOver ?? DEFAULT_AGE_OVER),
    acceptedDocumentTypes: (flow.acceptedDocumentTypes ?? []).join(", "),
    acceptedIssuingCountries: (flow.acceptedIssuingCountries ?? []).join(", "),
    assuranceLevel: oneOf(ASSURANCE_LEVELS, flow.requiredAssuranceLevel),
    bsnPolicy: oneOf(BSN_POLICIES, flow.bsnPolicy),
    keepFacePhotos: tristate(flow.keepFacePhotos),
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
  return draft;
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
  | "identity"
  | "assuranceLevel"
  | "issuingCountries"
  | "faceMatchThreshold"
  | "ageOver"
  | "retentionSeconds";

// An issuing country as a flow takes it: three letters (ICAO 9303). Whether
// a country has the code is the engine's check (flow.ValidIssuingCountry).
const ISSUING_COUNTRY = /^[A-Z]{3}$/;

// requiresIdentity is identityRequired for the flow's kind and diplomas.
export function flowDraftError(
  draft: ProofingFlowDraft,
  requiresIdentity = false,
): FlowDraftError | null {
  if (draft.name.trim() === "") {
    return "name";
  }
  if (draftSteps(draft).length === 0) {
    return "steps";
  }
  if (
    requiresIdentity &&
    !(
      draft.requestedAttributes.has(ATTRIBUTE_DOCUMENT) &&
      attributeAvailable(draft, ATTRIBUTE_DOCUMENT)
    )
  ) {
    return "identity";
  }
  if (!reachesAssuranceLevel(draft)) {
    return "assuranceLevel";
  }
  if (
    !list(draft.acceptedIssuingCountries).every((country) =>
      ISSUING_COUNTRY.test(country.toUpperCase()),
    )
  ) {
    return "issuingCountries";
  }
  if (draft.faceVerification && draft.faceMatchThreshold.trim() !== "") {
    const value = Number(draft.faceMatchThreshold);
    if (!Number.isFinite(value) || value < 0 || value > 1) {
      return "faceMatchThreshold";
    }
  }
  if (asksAge(draft)) {
    const age = Number(draft.ageOver);
    if (!Number.isInteger(age) || age < 1 || age > MAX_AGE_OVER) {
      return "ageOver";
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
  const requestedAttributes: string[] = REQUESTED_ATTRIBUTES.map(
    (a) => a.value,
  ).filter(
    (value) =>
      draft.requestedAttributes.has(value) && attributeAvailable(draft, value),
  );
  const spec: ProofingFlowSpec = {
    ...draft.carried,
    name: draft.name.trim(),
    steps: draftSteps(draft),
    requiredChecks,
    requestedAttributes:
      requestedAttributes.length > 0 ? requestedAttributes : [OUTCOME_ONLY],
    // Uppercased to match the MRZ's capitals; the engine ignores case for
    // document types (drivers_license), compares countries exactly.
    acceptedDocumentTypes: list(draft.acceptedDocumentTypes).map((type) =>
      type.toUpperCase(),
    ),
    acceptedIssuingCountries: list(draft.acceptedIssuingCountries).map(
      (country) => country.toUpperCase(),
    ),
  };
  if (draft.faceVerification) {
    spec.faceProvider = draft.faceProvider;
  }
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
  if (draft.keepFacePhotos !== "") {
    spec.keepFacePhotos = draft.keepFacePhotos === "true";
  }
  if (draft.blurBsn !== "") {
    spec.blurBsn = draft.blurBsn === "true";
  }
  const retention = Number(draft.retentionSeconds);
  if (draft.retentionSeconds.trim() !== "" && retention > 0) {
    spec.retentionOverrideSeconds = retention;
  }
  if (asksAge(draft)) {
    spec.ageOver = Number(draft.ageOver);
  }
  return spec;
}

// Whether the draft releases the age check, which needs an age.
export function asksAge(draft: ProofingFlowDraft): boolean {
  return (
    draft.requestedAttributes.has(ATTRIBUTE_AGE_OVER) &&
    attributeAvailable(draft, ATTRIBUTE_AGE_OVER)
  );
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

// The engine's bound on a reviewer's reason, in characters (code points, as
// the backend counts them): an emoji is one, not the two UTF-16 units a
// textarea's maxLength would count.
export const REVIEW_REASON_MAX_CHARS = 500;

export function reviewReasonTooLong(reason: string): boolean {
  return [...reason.trim()].length > REVIEW_REASON_MAX_CHARS;
}

// What submitting a data request's decision does next. Approving an erasure
// cannot be undone, so it first asks the reviewer to confirm how many sessions
// it erases; submitting from that confirmation then decides.
export type ReviewSubmitStep = "confirm" | "decide";

export function reviewSubmitStep({
  decision,
  kind,
  confirming,
}: {
  decision: "approve" | "reject";
  kind: FlowKind;
  confirming: boolean;
}): ReviewSubmitStep {
  return decision === "approve" && kind === "data_erasure" && !confirming
    ? "confirm"
    : "decide";
}

// Whether a match of level starts ticked on a data request of kind, as
// proofing.approvedMatches takes it without a tick: for an erasure, a session
// whose name and date of birth were read off a verified chip (the same
// document or another); for "see my data", only one proven with the same
// document. The reviewer ticks any other that is the person's.
export function matchTickedByDefault(kind: FlowKind, level: string): boolean {
  return (
    level === "strong" || (kind === "data_erasure" && level === "probable")
  );
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

// A Dutch postal code: four digits, the first not a zero, and two letters, as
// the backend's addresslookup reads it.
const DUTCH_POSTAL_CODE = /^([1-9]\d{3}) ?([A-Za-z]{2})$/;
// The number a house number starts with, before any letter or addition.
const HOUSE_NUMBER = /^(\d+)/;

// What a Dutch address is looked up by: the compact postal code and the
// number the house number starts with.
export type AddressLookup = { postalCode: string; houseNumber: string };

// The lookup the typed postal code and house number make, or null while they
// are not yet a Dutch postal code and a house number.
export function addressLookup(
  postalCode: string,
  houseNumber: string,
): AddressLookup | null {
  const code = DUTCH_POSTAL_CODE.exec(postalCode.trim());
  const number = HOUSE_NUMBER.exec(houseNumber.trim());
  if (code === null || number === null) return null;
  return {
    postalCode: `${code[1]}${code[2].toUpperCase()}`,
    houseNumber: number[1],
  };
}

// A house number as compared with the register's: case, spaces and
// punctuation left out, as the backend compares them.
function houseNumberKey(houseNumber: string): string {
  return houseNumber.toLowerCase().replaceAll(/[^\p{L}\p{N}]/gu, "");
}

// The register's address the typed house number is, letter and addition
// included, or undefined when it is none of them.
export function matchingAddress(
  found: readonly RegisterAddress[],
  houseNumber: string,
): RegisterAddress | undefined {
  const key = houseNumberKey(houseNumber);
  return found.find((a) => houseNumberKey(a.houseNumber) === key);
}

// How a kept address was checked, under it in a session's detail; one kept
// before addresses were checked reads as declared.
export function addressVerificationNote(
  verification: AddressVerification | undefined,
  t: TFunction,
): string {
  switch (verification) {
    case "verified":
      return t("customers.sessions.address.verified");
    case "unchecked":
      return t("customers.sessions.address.unchecked");
    case "unsupported":
      return t("customers.sessions.address.unsupported");
    case undefined:
      return t("customers.sessions.address.declared");
  }
}

// Why the proofing service did not approve a session, from its errorCode. An
// unknown code is shown as is, so a new one still says something.
export function proofingRejectionReason(code: string, t: TFunction): string {
  const key = REJECTION_REASON_KEYS[code as ProofingRejectionCode];
  return key === undefined
    ? t("identityProofing.rejectionReasons.unknown", { code })
    : t(`identityProofing.rejectionReasons.${key}`);
}

// The copy of every code a session is rejected with: the engine's failed
// checks and the wallet's own decisions (a review, a lapse, a pause, a legal
// duty). The test holds it to the backend's codes, both ways.
const REJECTION_REASON_KEYS = {
  DOCUMENT_TYPE_NOT_ACCEPTED: "documentTypeNotAccepted",
  DOCUMENT_COUNTRY_NOT_ACCEPTED: "documentCountryNotAccepted",
  FACE_STEP_NOT_COMPLETED: "faceStepNotCompleted",
  FACE_NO_MATCH: "faceNoMatch",
  REFERENCE_NO_FACE: "referenceNoFace",
  PHOTO_MISSING: "photoMissing",
  LIVENESS_FAILED: "livenessFailed",
  DOC_TAMPERED: "docTampered",
  CHIP_CLONE_DETECTED: "chipCloneDetected",
  DOC_EXPIRED: "docExpired",
  ASSURANCE_NOT_MET: "assuranceNotMet",
  IDENTITY_MISMATCH: "identityMismatch",
  MANUAL_REVIEW_REJECTED: "manualReviewRejected",
  REVIEW_LAPSED: "reviewLapsed",
  LEGAL_RETENTION: "legalRetention",
  ORG_PAUSED: "orgPaused",
} as const;

export type ProofingRejectionCode = keyof typeof REJECTION_REASON_KEYS;

export const PROOFING_REJECTION_CODES = Object.keys(
  REJECTION_REASON_KEYS,
) as ProofingRejectionCode[];

// Why a data request whose subject did prove who they are was refused: a
// reviewer refused it, nobody answered it, proofing was paused, or the
// customer must keep the data. The subject's page then says the request was
// refused, not that they were not verified.
export type DataRequestRefusal = "reviewer" | "lapsed" | "paused" | "law";

const DATA_REQUEST_REFUSALS: Partial<
  Record<ProofingRejectionCode, DataRequestRefusal>
> = {
  MANUAL_REVIEW_REJECTED: "reviewer",
  REVIEW_LAPSED: "lapsed",
  ORG_PAUSED: "paused",
  LEGAL_RETENTION: "law",
};

// The refusal a data request's error code stands for; undefined for a failed
// identity check, which the outcome explains itself.
export function dataRequestRefusal(
  errorCode: string | undefined,
): DataRequestRefusal | undefined {
  return errorCode === undefined
    ? undefined
    : DATA_REQUEST_REFUSALS[errorCode as ProofingRejectionCode];
}

// The stages a verify page's stepper shows: what is collected, the app (when
// the subject has a choice), its session, then the address and the diploma
// upload (when the flow has those steps).
export type VerifyStage =
  | "overview"
  | "method"
  | "session"
  | "address"
  | "diplomas";

export function verifyStages({
  appChoice,
  address = false,
  diplomas,
}: {
  appChoice: boolean;
  address?: boolean;
  diplomas: boolean;
}): VerifyStage[] {
  return [
    "overview",
    ...(appChoice ? (["method"] as const) : []),
    "session",
    ...(address ? (["address"] as const) : []),
    ...(diplomas ? (["diplomas"] as const) : []),
  ];
}

// The stage a verify page is at: the address or diploma step while it is
// open, else the page's own step.
export function currentStage({
  step,
  inAddress,
  inDiplomas,
}: {
  step: "overview" | "method" | "session";
  inAddress: boolean;
  inDiplomas: boolean;
}): VerifyStage {
  if (inAddress) {
    return "address";
  }
  if (inDiplomas) {
    return "diplomas";
  }
  return step;
}

// What a verify page's running session shows: once an approved session asks
// for them, the address step and then the diploma step until the subject is
// done with each, else the outcome once settled, the expiry once an unstarted
// session's link ran out, or the session itself.
export type SessionView =
  | "address"
  | "diplomas"
  | "outcome"
  | "expired"
  | "running";

export function sessionView({
  status,
  addressMode,
  addressUntil,
  addressDone,
  diplomaMode,
  diplomasUntil,
  diplomasDone,
  secondsLeft,
}: {
  status: string;
  addressMode: AddressMode;
  addressUntil: string | undefined;
  addressDone: boolean;
  diplomaMode: DiplomaMode;
  diplomasUntil: string | undefined;
  diplomasDone: boolean;
  secondsLeft: number;
}): SessionView {
  if (!isProofingLive(status)) {
    if (addressMode !== "off" && addressUntil !== undefined && !addressDone) {
      return "address";
    }
    return diplomaMode !== "off" && diplomasUntil !== undefined && !diplomasDone
      ? "diplomas"
      : "outcome";
  }
  return secondsLeft === 0 && status === "pending" ? "expired" : "running";
}

// Whether a request on flow can be mailed (proofing.ErrDiplomasNeedPage,
// ErrAddressNeedsPage): a flow that asks for diplomas or the address has them
// given on the page that runs the session. No flow picked yet is mailable.
export function sendableByMail(
  flow: { diplomaMode?: DiplomaMode; addressMode?: AddressMode } | undefined,
): boolean {
  return (
    (flow?.diplomaMode ?? "off") === "off" &&
    (flow?.addressMode ?? "off") === "off"
  );
}

// The document data a flow releases: the holder's name and date of birth.
export const ATTRIBUTE_DOCUMENT = "dg1";

// The age check: only whether the holder is the flow's age or older, derived
// from the chip's date of birth (flow.AttributeAgeOver).
export const ATTRIBUTE_AGE_OVER = "age_over";
// The age a new age check answers for, and the highest the service takes
// (flow.MaxAgeOver).
const DEFAULT_AGE_OVER = 18;
export const MAX_AGE_OVER = 120;
// The attribute each one cannot be requested with: the age check replaces the
// document data (flow.validateAgeOver).
export const EXCLUSIVE_ATTRIBUTES: Readonly<Record<string, string>> = {
  [ATTRIBUTE_AGE_OVER]: ATTRIBUTE_DOCUMENT,
  [ATTRIBUTE_DOCUMENT]: ATTRIBUTE_AGE_OVER,
};

// Whether flow's result carries the holder's name and date of birth (the
// document data, dg1), which a request for one known person is matched
// against (proofing.readsIdentity). The service hands a flow back listing
// what it releases.
export function readsIdentity(
  flow: { requestedAttributes?: string[] } | undefined,
): boolean {
  return flow?.requestedAttributes?.includes(ATTRIBUTE_DOCUMENT) ?? false;
}

// Whether a flow of kind with diplomas must read the name and date of birth
// (proofing.identityRequired): a data request's other sessions are found by
// them, and a required diploma's holder is checked against them.
export function identityRequired(
  kind: FlowKind,
  diplomas: DiplomaMode,
): boolean {
  return isDataRequest(kind) || diplomas !== "off";
}

// Why an uploaded diploma extract was not kept, from its verdict's reason.
// An unknown reason is shown as is.
export function diplomaRejectionReason(reason: string, t: TFunction): string {
  switch (reason) {
    case "not_a_diploma":
      return t("identityProofing.diplomas.reasons.notADiploma");
    case "signature_invalid":
      return t("identityProofing.diplomas.reasons.signatureInvalid");
    case "holder_mismatch":
      return t("identityProofing.diplomas.reasons.holderMismatch");
    case "duplicate":
      return t("identityProofing.diplomas.reasons.duplicate");
    default:
      return reason;
  }
}

// Whether the diploma step can end: once an extract is held, or the time to
// add one is up.
export function diplomaStepDone(held: number, secondsLeft: number): boolean {
  return held > 0 || secondsLeft === 0;
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
    case "customer_paused":
      return t("identityProofing.errors.customerPaused");
    case "customer_has_open_reviews":
      return t("identityProofing.errors.customerHasOpenReviews");
    case "customer_sessions_left":
      return t("identityProofing.errors.customerSessionsLeft");
    case "proofing_paused":
      return t("identityProofing.errors.proofingPaused");
    case "customer_no_api_key":
      return t("identityProofing.errors.customerNoApiKey");
    case "api_key_not_found":
      return t("identityProofing.errors.apiKeyNotFound");
    case "session_not_found":
      return t("identityProofing.errors.sessionNotFound");
    case "not_under_review":
      return t("identityProofing.errors.notUnderReview");
    case "reference_photo_required":
      return t("identityProofing.errors.referencePhotoRequired");
    case "organization_not_found":
      return t("identityProofing.errors.organizationNotFound");
    case "webhook_not_found":
      return t("identityProofing.errors.webhookNotFound");
    case "link_started":
      return t("identityProofing.errors.linkStarted");
    case "session_over":
      return t("identityProofing.errors.sessionOver");
    case "device_active":
      return t("identityProofing.errors.deviceActive");
    case "method_unavailable":
      return t("identityProofing.errors.methodUnavailable");
    case "diplomas_need_page":
      return t("identityProofing.errors.diplomasNeedPage");
    case "hosted_disabled":
      return t("identityProofing.errors.hostedDisabled");
    case "diplomas_closed":
      return t("identityProofing.errors.diplomasClosed");
    case "diplomas_not_asked":
      return t("identityProofing.errors.diplomasNotAsked");
    case "address_needs_page":
      return t("identityProofing.errors.addressNeedsPage");
    case "address_closed":
      return t("identityProofing.errors.addressClosed");
    case "address_not_asked":
      return t("identityProofing.errors.addressNotAsked");
    case "address_not_found":
      return t("identityProofing.errors.addressNotFound");
    case "flow_no_identity":
      return t("identityProofing.errors.flowNoIdentity");
    case "data_flow_for_member":
      return t("identityProofing.errors.dataFlowForMember");
    case "export_unavailable":
      return t("identityProofing.errors.exportUnavailable");
    // An admin by role whose mandate was withdrawn: the backend refuses every
    // admin call, though the role still shows the admin's tabs.
    case "mandate_withdrawn":
      return t("identityProofing.errors.mandateWithdrawn");
    // too_large is a diploma upload's, payload_too_large a branding logo's.
    case "too_large":
    case "payload_too_large":
      return t("identityProofing.errors.tooLarge");
    case "too_many_files":
    case "invalid_input":
    case "rejected_by_provider":
      return serverMessage(error) ?? t("identityProofing.errors.generic");
    default:
      return t("identityProofing.errors.generic");
  }
}

// What a hosted link's subject is told when a call fails. The wallet's copy
// speaks to its members ("an admin can resume it", "pick another flow"), so
// whatever the subject cannot act on reads as the link not working for now.
export function hostedErrorMessage(error: unknown, t: TFunction): string {
  switch (errorCode(error)) {
    case null:
    case "rate_limited":
      return t("identityProofing.errors.generic");
    case "link_started":
    case "device_active":
    case "diplomas_closed":
    case "export_unavailable":
    case "too_large":
    case "payload_too_large":
    case "too_many_files":
    case "invalid_input":
    case "rejected_by_provider":
      return proofingErrorMessage(error, t);
    case "session_over":
      return t("proofLink.errors.sessionOver");
    case "method_unavailable":
      return t("proofLink.errors.methodUnavailable");
    default:
      return t("proofLink.errors.unavailable");
  }
}

// The error message a verify page shows: the subject's copy on a hosted link,
// the members' on the on-screen page.
export function verifyErrorMessage(
  target: { kind: "request" | "hosted" },
  error: unknown,
  t: TFunction,
): string {
  return target.kind === "hosted"
    ? hostedErrorMessage(error, t)
    : proofingErrorMessage(error, t);
}

// The worker makes a due delivery's first send within seconds; a failing one
// retries with backoff for about a day.
const DELIVERY_FIRST_SEND_POLL_MS = 5_000;
const DELIVERY_RETRY_POLL_MS = 60_000;

// How often a customer's webhook deliveries are re-read: soon while one waits
// for its first send, slowly while one retries, not at all once none is
// pending.
export function deliveriesPollInterval(
  deliveries: readonly { status: string; attempts: number }[] | undefined,
): number | false {
  const pending = (deliveries ?? []).filter((d) => d.status === "pending");
  if (pending.length === 0) return false;
  return pending.some((d) => d.attempts === 0)
    ? DELIVERY_FIRST_SEND_POLL_MS
    : DELIVERY_RETRY_POLL_MS;
}

// The outcome counts of a set of stats rows, summed.
export interface ProofingTotals {
  sessions: number;
  approved: number;
  rejected: number;
  needsReview: number;
  expired: number;
  cancelled: number;
}

const NO_SESSIONS: ProofingTotals = {
  sessions: 0,
  approved: 0,
  rejected: 0,
  needsReview: 0,
  expired: 0,
  cancelled: 0,
};

export function sumProofingStats(
  rows: readonly ProofingTotals[],
): ProofingTotals {
  return rows.reduce(
    (sum, row) => ({
      sessions: sum.sessions + row.sessions,
      approved: sum.approved + row.approved,
      rejected: sum.rejected + row.rejected,
      needsReview: sum.needsReview + row.needsReview,
      expired: sum.expired + row.expired,
      cancelled: sum.cancelled + row.cancelled,
    }),
    NO_SESSIONS,
  );
}

// Stats rows summed per key: per customer, or per flow of one customer.
export function proofingStatsBy<T extends ProofingTotals>(
  rows: readonly T[],
  key: (row: T) => string,
): Map<string, ProofingTotals> {
  const groups = new Map<string, T[]>();
  for (const row of rows) {
    const k = key(row);
    groups.set(k, [...(groups.get(k) ?? []), row]);
  }
  return new Map([...groups].map(([k, group]) => [k, sumProofingStats(group)]));
}

export function noProofingSessions(): ProofingTotals {
  return { ...NO_SESSIONS };
}

// The share of sessions verified, 0..1; undefined with no sessions, which a
// percentage cannot express.
export function verifiedShare(totals: ProofingTotals): number | undefined {
  return totals.sessions === 0 ? undefined : totals.approved / totals.sessions;
}

// The customers whose name holds the search, ignoring case and surrounding
// space; every customer for an empty search.
export function searchCustomers<T extends { name: string }>(
  customers: readonly T[],
  search: string,
): T[] {
  const needle = search.trim().toLocaleLowerCase();
  if (needle === "") {
    return [...customers];
  }
  return customers.filter((c) => c.name.toLocaleLowerCase().includes(needle));
}

// A requested-data value the wallet has copy for; the backend may add others.
export type RequestedAttribute = (typeof REQUESTED_ATTRIBUTES)[number]["value"];

export function isRequestedAttribute(
  value: string,
): value is RequestedAttribute {
  return REQUESTED_ATTRIBUTES.some((a) => a.value === value);
}

// A flow step the wallet has copy for.
export type ProofingStep =
  | typeof STEP_DOCUMENT_CAPTURE
  | typeof STEP_NFC_READ
  | typeof STEP_DOCUMENT_PHOTO
  | typeof STEP_FACE_VERIFICATION;

const PROOFING_STEPS: readonly string[] = [
  STEP_DOCUMENT_CAPTURE,
  STEP_NFC_READ,
  STEP_DOCUMENT_PHOTO,
  STEP_FACE_VERIFICATION,
];

export function isProofingStep(value: string): value is ProofingStep {
  return PROOFING_STEPS.includes(value);
}

// The Sessions tab's filters: every session, or one outcome; "review" is the
// review queue. Pending and in progress show under "all" only.
export const SESSION_FILTERS = [
  "all",
  "review",
  "verified",
  "failed",
  "expired",
] as const;
export type SessionFilter = (typeof SESSION_FILTERS)[number];

const FILTER_STATUS: Record<Exclude<SessionFilter, "all">, string> = {
  review: "needs_review",
  verified: "approved",
  failed: "rejected",
  expired: "expired",
};

export function matchesSessionFilter(
  status: string,
  filter: SessionFilter,
): boolean {
  return filter === "all" || FILTER_STATUS[filter] === status;
}

export function sessionFilterCounts(
  requests: readonly { status: string }[],
): Record<SessionFilter, number> {
  const count = (filter: SessionFilter): number =>
    requests.filter((r) => matchesSessionFilter(r.status, filter)).length;
  return {
    all: requests.length,
    review: count("review"),
    verified: count("verified"),
    failed: count("failed"),
    expired: count("expired"),
  };
}

const MS_PER_SECOND = 1000;
const SECONDS_PER_MINUTE = 60;
const SECONDS_PAD = 2;

// How long a finished session ran, in whole seconds: from the send to its
// outcome, or to its expiry for one nobody finished. Undefined while it runs.
export function sessionDurationSeconds(request: {
  status: string;
  createdAt: string;
  completedAt?: string | undefined;
  linkExpiresAt: string;
}): number | undefined {
  const end =
    request.completedAt ??
    (request.status === "expired" ? request.linkExpiresAt : undefined);
  if (end === undefined) {
    return undefined;
  }
  const ms = Date.parse(end) - Date.parse(request.createdAt);
  return Number.isFinite(ms)
    ? Math.max(0, Math.round(ms / MS_PER_SECOND))
    : undefined;
}

// "m:ss", as a stopwatch reads.
export function formatDuration(seconds: number): string {
  const minutes = Math.floor(seconds / SECONDS_PER_MINUTE);
  const rest = String(seconds % SECONDS_PER_MINUTE).padStart(SECONDS_PAD, "0");
  return `${minutes}:${rest}`;
}

// Whole seconds from now until expiresAt, never below zero: what an on-screen
// session's countdown shows.
export function secondsUntil(expiresAt: string, nowMs: number): number {
  const ms = Date.parse(expiresAt) - nowMs;
  return Number.isFinite(ms) ? Math.max(0, Math.ceil(ms / MS_PER_SECOND)) : 0;
}

// The short form of a request id shown in tables: enough to tell rows apart
// and to find the full id in the audit log.
const SHORT_ID_LENGTH = 8;

export function shortRequestId(id: string): string {
  return id.replaceAll("-", "").slice(0, SHORT_ID_LENGTH);
}

// The session settings an admin may pick for a customer, mirroring
// SessionTTLOptions and DataRetentionDayOptions in backend/internal/proofing
// (identity-proofing.test.ts holds the two together).
export const SESSION_TTL_OPTIONS_SECONDS = [120, 300, 600] as const;
export const DATA_RETENTION_DAY_OPTIONS = [7, 30, 90, 180, 365] as const;

const SECONDS_IN_MINUTE = 60;

export function ttlMinutes(seconds: number): number {
  return Math.round(seconds / SECONDS_IN_MINUTE);
}

// How a customer reads in the lists: paused, or active and needing attention
// because its webhook endpoint is failing, not set up without a live API key,
// or plainly active.
export type CustomerDisplayStatus =
  | "active"
  | "paused"
  | "needs_attention"
  | "setup_needed";

export function customerDisplayStatus(customer: {
  status: string;
  webhook: { state: string };
  hasApiKey: boolean;
}): CustomerDisplayStatus {
  if (customer.status === "paused") {
    return "paused";
  }
  if (!customer.hasApiKey) {
    return "setup_needed";
  }
  return customer.webhook.state === "failing" ? "needs_attention" : "active";
}

// A delivery answered 2xx.
const HTTP_SUCCESS_CLASS = 2;
const HTTP_STATUS_CLASS_DIVISOR = 100;

export function isSuccessStatus(code: number): boolean {
  return Math.floor(code / HTTP_STATUS_CLASS_DIVISOR) === HTTP_SUCCESS_CLASS;
}

// The text colour readable on a #rrggbb fill (WCAG relative luminance): the
// branding preview mirrors what the mail shell does to its button label.
const LUMINANCE_THRESHOLD = 0.179;
const HEX_COLOR = /^#[0-9a-fA-F]{6}$/;
const CHANNEL_MAX = 255;
const SRGB_LINEAR_LIMIT = 0.03928;
const SRGB_LINEAR_DIVISOR = 12.92;
const SRGB_OFFSET = 0.055;
const SRGB_SCALE = 1.055;
const SRGB_GAMMA = 2.4;
const LUMINANCE_WEIGHTS = [0.2126, 0.7152, 0.0722] as const;
const HEX_RADIX = 16;
const HEX_PAIR = 2;

export function isHexColor(value: string): boolean {
  return HEX_COLOR.test(value);
}

export function readableTextOn(hex: string): "#ffffff" | "#000000" {
  if (!isHexColor(hex)) {
    return "#ffffff";
  }
  const luminance = LUMINANCE_WEIGHTS.reduce((sum, weight, i) => {
    const start = 1 + i * HEX_PAIR;
    const c =
      parseInt(hex.slice(start, start + HEX_PAIR), HEX_RADIX) / CHANNEL_MAX;
    const linear =
      c <= SRGB_LINEAR_LIMIT
        ? c / SRGB_LINEAR_DIVISOR
        : ((c + SRGB_OFFSET) / SRGB_SCALE) ** SRGB_GAMMA;
    return sum + weight * linear;
  }, 0);
  return luminance > LUMINANCE_THRESHOLD ? "#000000" : "#ffffff";
}

// The app a subject proofed with, as the backend reports it (proofingprovider
// Method). No method is a session nobody opened; an unknown one shows as is.
export function proofingMethodLabel(
  method: string | undefined,
  t: TFunction,
): string {
  switch (method) {
    case "idem_app":
      return t("identityProofing.methods.idemApp");
    case "yivi_app":
      return t("identityProofing.methods.yiviApp");
    case "browser":
      return t("identityProofing.methods.browser");
    case undefined:
    case "":
      return "—";
    default:
      return method;
  }
}

// What a session timeline shows under an event: the app used, the assurance
// reached and why it failed, read from the event's "after" snapshot. The
// status change itself is the event's own label.
export function sessionEventDetail(
  metadata: Record<string, unknown>,
  t: TFunction,
): string[] {
  const after = metadata.after;
  if (typeof after !== "object" || after === null) {
    return [];
  }
  const fields = after as Record<string, unknown>;
  const parts: string[] = [];
  if (typeof fields.method === "string" && fields.method !== "") {
    parts.push(proofingMethodLabel(fields.method, t));
  }
  if (typeof fields.eidasLevel === "string" && fields.eidasLevel !== "") {
    parts.push(t("customers.flows.eidas", { level: fields.eidasLevel }));
  }
  if (typeof fields.errorCode === "string" && fields.errorCode !== "") {
    parts.push(proofingRejectionReason(fields.errorCode, t));
  }
  if (typeof fields.flowVersion === "number") {
    parts.push(
      t("identityProofingFlows.versionShort", { version: fields.flowVersion }),
    );
  }
  return parts;
}

// What the send form does with its fields: the flow it sends (a pick no longer
// sendable falls back to the default), how (a flow that asks for diplomas or
// the address cannot be mailed, so it runs on screen), whether it checks for one person (a
// flow reading name and date of birth, given a date of birth), and which
// fields still block it.
export function sendFormDecision<
  F extends {
    id: string;
    diplomaMode?: DiplomaMode;
    addressMode?: AddressMode;
    requestedAttributes?: string[];
  },
>(input: {
  sendable: readonly F[];
  initial: F | undefined;
  picked: string;
  channel: ProofingChannel;
  email: string;
  name: string;
  birthDate: string;
}): {
  flowId: string;
  mailable: boolean;
  matchable: boolean;
  expectedBirthDate: string;
  delivery: ProofingChannel;
  emailMissing: boolean;
  nameMissing: boolean;
} {
  const flowId = input.sendable.some((f) => f.id === input.picked)
    ? input.picked
    : (input.initial?.id ?? "");
  const flow = input.sendable.find((f) => f.id === flowId);
  const mailable = sendableByMail(flow);
  const matchable = readsIdentity(flow);
  const expectedBirthDate = matchable ? input.birthDate : "";
  const delivery: ProofingChannel =
    input.channel === "email" && !mailable ? "on_screen" : input.channel;
  // Only a mail needs an address; one given must still be one.
  const emailMissing =
    delivery === "email"
      ? !input.email.includes("@")
      : input.email.trim() !== "" && !input.email.includes("@");
  const nameMissing = expectedBirthDate !== "" && input.name.trim() === "";
  return {
    flowId,
    mailable,
    matchable,
    expectedBirthDate,
    delivery,
    emailMissing,
    nameMissing,
  };
}

// Whether a session's row shows its verified identity to this reader: an
// admin's, for a session with an outcome (one in review shows the reviewer
// who it is), never once it is purged.
export function showsSessionIdentity(
  request: Pick<ProofingRequest, "status" | "purgedAt">,
  isAdmin: boolean,
): boolean {
  return (
    isAdmin &&
    request.purgedAt === undefined &&
    (request.status === "approved" ||
      request.status === "rejected" ||
      request.status === "needs_review")
  );
}

// The outcomes a chip or face check reports.
export const CHECK_OUTCOMES = [
  "valid",
  "invalid",
  "not_performed",
  "passed",
  "failed",
] as const;

export function isCheckOutcome(
  value: string,
): value is (typeof CHECK_OUTCOMES)[number] {
  return (CHECK_OUTCOMES as readonly string[]).includes(value);
}

// An identity image as an <img> source.
export function imageSource(image: ProofingImage): string {
  return `data:${image.mimeType};base64,${image.data}`;
}

// Who a session timeline event names as its actor: the member, the customer's
// API key for a request it created, a non-user actor's label, or the system;
// null when the event's detail already says who.
export function timelineActor(
  event: AuditEvent,
  request: Pick<ProofingRequest, "apiKeyName">,
  t: TFunction,
  detail: readonly string[],
): string | null {
  if (event.actor) {
    return fullName(event.actor);
  }
  if (event.action === "identity_proofing.requested" && request.apiKeyName) {
    return t("customers.sessions.viaApiKey", { name: request.apiKeyName });
  }
  const label = auditActorLabel(event.actorLabel, t);
  if (label) return label;
  return detail.length > 0 ? null : t("auditLog.system");
}
