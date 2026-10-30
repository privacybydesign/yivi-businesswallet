import type { TFunction } from "i18next";
import { ApiError } from "../api/http";
import {
  DATA_REQUEST_KINDS,
  EMPLOYMENT_KIND,
  MAX_LISTED_REQUESTS,
} from "../api/identity-proofing";
import type {
  AddressMode,
  AddressVerification,
  AttributeReasons,
  DataRequestKind,
  DiplomaMode,
  EmploymentMode,
  FlowKind,
  ProofingBranding,
  ProofingChannel,
  ProofingEmployment,
  ProofingFlow,
  ProofingFlowBranding,
  ProofingFlowHosted,
  ProofingFlowSelection,
  ProofingFlowSpec,
  ProofingImage,
  ProofingMethod,
  ProofingRequest,
  RegisterAddress,
  WebhookDelivery,
} from "../api/identity-proofing";
import type { AuditEvent } from "../api/organization";
import type { OrgTheme } from "../api/theme";
import { SUPPORTED_LANGUAGES } from "../i18n/language";
import type { Language } from "../i18n/language";
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
const FINAL_POLL_STATUSES: ReadonlySet<number> = new Set([403, 404]);

// Whether a verify page keeps polling its progress: until the first read,
// while the session is live, and while an approved session still owes its
// Verzekeringsbericht past the window, until the backend rejects it for that
// (EMPLOYMENT_NOT_PROVIDED); unless a read was refused for good.
export function progressPollContinues(
  progress: { status: string; employmentUntil?: string } | undefined,
  error: Error | null,
  now: number = Date.now(),
): boolean {
  if (refusedForGood(error)) {
    return false;
  }
  return (
    progress === undefined ||
    isProofingLive(progress.status) ||
    (employmentOwed(progress) &&
      Date.parse(progress.employmentUntil ?? "") <= now)
  );
}

// Whether the backend still waits for an approved session's
// Verzekeringsbericht: it settles once one is kept (approved or to review) or
// the window passed without one (rejected), and only that read may be handed
// back as the outcome.
export function employmentOwed(progress: {
  status: string;
  employmentUntil?: string;
}): boolean {
  return (
    progress.status === "approved" && progress.employmentUntil !== undefined
  );
}

// Whether a verify page keeps re-reading a session in review, slowly: a
// person decides it, or the org's erasure setting seconds after it got
// there, while the subject may still be watching.
export function reviewPollContinues(
  progress: { status: string } | undefined,
  error: Error | null,
): boolean {
  return !refusedForGood(error) && progress?.status === "needs_review";
}

function refusedForGood(error: Error | null): boolean {
  return error instanceof ApiError && FINAL_POLL_STATUSES.has(error.status);
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
//     only for face.match, on a slider from 0 to 1 that Substantial floors.
//   - Requested data: each item is available only when the step that produces
//     it is in the flow, and cleared otherwise; none is forced on.
//   - Assurance (flow.LevelRequirements): low needs nfc.passive_auth;
//     substantial also nfc.chip_auth, face.match (at a threshold of at least
//     SUBSTANTIAL_FACE_MATCH_FLOOR) and face.liveness with Regula.
//     Picking a level ticks what it needs and locks it (withAssuranceLevel);
//     a draft that still cannot reach its level is refused. High is never
//     reachable. The engine explains a refusal.
export const STEP_DOCUMENT_CAPTURE = "document_capture";
export const STEP_NFC_READ = "nfc_read";
export const STEP_DOCUMENT_PHOTO = "document_photo";
export const STEP_FACE_VERIFICATION = "face_verification";
export const CHECK_PASSIVE_AUTH = "nfc.passive_auth";
export const CHECK_CHIP_AUTH = "nfc.chip_auth";
export const CHECK_FACE_MATCH = "face.match";
export const CHECK_LIVENESS = "face.liveness";

// The face match threshold is a similarity from 0 to 1, in steps of 0.01.
export const FACE_MATCH_MIN = 0;
export const FACE_MATCH_MAX = 1;
export const FACE_MATCH_STEP = 0.01;
// Regula's default (regula.DefaultMatchThreshold): what a flow with no
// threshold gets, and the lowest one recommended.
export const FACE_MATCH_RECOMMENDED = 0.75;
// The lowest threshold a flow requiring Substantial may set
// (flow.SubstantialMinFaceMatch).
export const SUBSTANTIAL_FACE_MATCH_FLOOR = 0.75;

// The requested data the service's admin page offers, in the order the editor
// lists them, and the step that produces each: the document scan gives the
// document and holder (dg1), the chip read the age check, its extras, photo and
// check results, the face step its selfie and biometrics.
export const REQUESTED_ATTRIBUTES = [
  { value: "dg1", step: STEP_DOCUMENT_CAPTURE },
  { value: "age_over", step: STEP_NFC_READ },
  { value: "chip_checks", step: STEP_NFC_READ },
  { value: "biometrics", step: STEP_FACE_VERIFICATION },
  { value: "dg11", step: STEP_NFC_READ },
  { value: "dg2", step: STEP_NFC_READ },
  { value: "selfie", step: STEP_FACE_VERIFICATION },
  { value: "document_image", step: STEP_DOCUMENT_PHOTO },
] as const;

// The requested data of a flow that releases nothing but the outcome
// (proofingengine.attrOutcomeOnly): an empty list releases everything the
// steps collect, so a flow with no data ticked sends this instead.
export const OUTCOME_ONLY = "outcome_only";

// The longest reason a flow gives for an item, in each language
// (maxAttributeReasonLength).
export const MAX_ATTRIBUTE_REASON_CHARS = 300;

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

// In the order the editor offers them, the one that keeps least first.
export const BSN_POLICIES = ["omit", "mask", "retrieve"] as const;
type BsnPolicy = (typeof BSN_POLICIES)[number];

// A new flow leaves the BSN out. A stored flow without a policy is shown as
// what the proofing engine does with it (an unset policy leaves the BSN out,
// privacy.DefaultBSNPolicy), so the editor says what the engine does.
const NEW_FLOW_BSN_POLICY: BsnPolicy = "omit";
const UNSET_BSN_POLICY: BsnPolicy = "omit";

// The kinds of document the editor lets a flow accept, saved as the proofing
// engine names them (flow.DocumentKindPassport, DocumentKindIdentityCard,
// DocumentKindResidencePermit and DocumentTypeDrivingLicence;
// identity-proofing.test.ts holds the two together). The engine tells a
// document's kind from its code, issuing state and holder's nationality
// (flow.DocumentKindOf), and the Yivi app's from its credential, so a
// residence permit coded ID is not taken for an identity card, and a
// diplomatic passport (PD) is a passport.
export const DOCUMENT_KINDS = [
  "passport",
  "identity_card",
  "residence_permit",
  "drivers_license",
] as const;
export type DocumentKind = (typeof DOCUMENT_KINDS)[number];

// The ICAO 9303 document codes earlier editors saved for a kind: "P" for a
// passport, "I" and "ID" for an identity card. A stored one reads as its
// kind; any other stored code is kept as it is.
const DOCUMENT_KIND_CODES: Record<DocumentKind, readonly string[]> = {
  passport: ["P"],
  identity_card: ["I", "ID"],
  residence_permit: [],
  drivers_license: [],
};

// The kind a stored document type, or a chip's document code, reads as: a
// kind by its name in any case (the old editor uppercased them) or one of
// DOCUMENT_KIND_CODES; undefined for another code.
export function documentKindOf(type: string): DocumentKind | undefined {
  const upper = type.toUpperCase();
  return DOCUMENT_KINDS.find(
    (kind) =>
      kind.toUpperCase() === upper || DOCUMENT_KIND_CODES[kind].includes(upper),
  );
}

// The kinds a stored list of document types reads as: every kind for an
// empty one, which accepts any document.
function storedDocumentKinds(
  types: readonly string[],
): ReadonlySet<DocumentKind> {
  return new Set(
    DOCUMENT_KINDS.filter(
      (kind) =>
        types.length === 0 ||
        types.some((type) => documentKindOf(type) === kind),
    ),
  );
}

type CountryScope = "any" | "only";

// What verifies the face step; a new flow starts on the first, Regula. The
// wallet runs no face engine of its own, so there is no "engine", and the
// proofing engine verifies faces with Regula only, so Iris is not offered.
export const FACE_PROVIDERS = ["regula"] as const;
export type FaceProvider = (typeof FACE_PROVIDERS)[number];

// The service's steps that capture the face (proofing.faceSteps).
const FACE_STEPS = new Set([
  "face_verification",
  "selfie",
  "liveness",
  "face_match",
]);
// The selfie location that takes the face in the browser
// (proofing.selfieLocationBrowser).
const SELFIE_LOCATION_BROWSER = "browser";

// The eIDAS level a Yivi session reaches (proofing.yiviEIDASLevel).
const YIVI_EIDAS_LEVEL = "low";

// Whether the Yivi app can run a flow (proofing.yiviAppAvailable): unless it
// photographs the document, which only the Idem app does, matches the face
// against the sender's reference photo (proofing.NeedsReferencePhoto: a face
// step without the chip read, outside the browser), where the Yivi app
// matches against its credential's photo instead, or requires a level above
// what a Yivi session reaches, or checks an age the Yivi passport and id-card
// carry no claim for (AGE_OVER_CHOICES).
export function yiviAppAvailable(flow: {
  steps: readonly string[];
  selfieLocation?: string;
  requiredAssuranceLevel?: string;
  requestedAttributes?: readonly string[];
  ageOver?: number;
}): boolean {
  const needsReferencePhoto =
    flow.steps.some((step) => FACE_STEPS.has(step)) &&
    !flow.steps.includes(STEP_NFC_READ) &&
    flow.selfieLocation !== SELFIE_LOCATION_BROWSER;
  const levelMet =
    !flow.requiredAssuranceLevel ||
    flow.requiredAssuranceLevel === YIVI_EIDAS_LEVEL;
  return (
    !flow.steps.includes(STEP_DOCUMENT_PHOTO) &&
    !needsReferencePhoto &&
    (!(flow.requestedAttributes ?? []).includes(ATTRIBUTE_AGE_OVER) ||
      AGE_OVER_CHOICES.includes(flow.ageOver ?? 0)) &&
    levelMet
  );
}

// "" is no requirement.
export type Inherit<T extends string> = "" | T;

// A flow's retention or session lifetime of 0 sets none: the org's applies.
export const FOLLOW_ORG = 0;

export interface ProofingFlowDraft {
  name: string;
  // document_capture and nfc_read, always together.
  documentAndChip: boolean;
  documentPhoto: boolean;
  faceVerification: boolean;
  chipAuthentication: boolean;
  liveness: boolean;
  faceProvider: FaceProvider;
  // The face match threshold, 0..1; null sets none, so the deployment's applies.
  faceMatchThreshold: number | null;
  requestedAttributes: ReadonlySet<string>;
  // Whole years as typed, the age the age check answers for.
  ageOver: string;
  // Why each item is collected. An unticked item keeps what was typed, so
  // ticking it again brings it back; only the collected ones are saved.
  attributeReasons: AttributeReasons;
  // Every kind ticked accepts any document.
  documentKinds: ReadonlySet<DocumentKind>;
  // Document codes of a stored version that no kind covers, kept as stored.
  otherDocumentTypes: readonly string[];
  // The document types of the version being edited, as stored: sent as they
  // are while the kinds ticked are the ones they read as. Null for a new flow.
  storedDocumentTypes: readonly string[] | null;
  countryScope: CountryScope;
  // ICAO codes in the order picked; only sent with the scope "only".
  issuingCountries: readonly string[];
  assuranceLevel: Inherit<AssuranceLevel>;
  bsnPolicy: BsnPolicy;
  blackOutBsn: boolean;
  keepFacePhotos: boolean;
  // Whole seconds the flow keeps its finished sessions; 0 follows the org's.
  retentionSeconds: number;
  // How long a session of the flow runs; 0 follows the org's.
  sessionTtlSeconds: number;
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
export type LevelRequirement = Partial<
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

// The lowest face match threshold a level allows; only Substantial floors it.
export function faceMatchFloor(level: Inherit<AssuranceLevel>): number {
  return level === "substantial"
    ? SUBSTANTIAL_FACE_MATCH_FLOOR
    : FACE_MATCH_MIN;
}

// The threshold the slider shows: the draft's, else Regula's default.
export function faceMatchValue(draft: ProofingFlowDraft): number {
  return draft.faceMatchThreshold ?? FACE_MATCH_RECOMMENDED;
}

const faceMatchFormats = new Map<string, Intl.NumberFormat>();

// A face match threshold as the editor writes it: two decimals in the reader's
// locale, and a bare 0 for the start of the scale.
export function formatFaceMatch(value: number, locale: string): string {
  if (value === FACE_MATCH_MIN) {
    return "0";
  }
  let format = faceMatchFormats.get(locale);
  if (format === undefined) {
    format = new Intl.NumberFormat(locale, {
      minimumFractionDigits: 2,
      maximumFractionDigits: 2,
    });
    faceMatchFormats.set(locale, format);
  }
  return format.format(value);
}

// The draft at a level, with what the level needs ticked. The rest stays as it
// was, so a setting a lower level leaves free keeps its value; a threshold
// under the level's floor is lifted to it.
export function withAssuranceLevel(
  draft: ProofingFlowDraft,
  level: Inherit<AssuranceLevel>,
): ProofingFlowDraft {
  const { faceMatchThreshold } = draft;
  return {
    ...draft,
    ...levelRequirement(level),
    assuranceLevel: level,
    faceMatchThreshold:
      faceMatchThreshold === null
        ? null
        : Math.max(faceMatchThreshold, faceMatchFloor(level)),
  };
}

// Whether the draft's level needs a setting on, which locks it.
export function lockedByLevel(
  draft: ProofingFlowDraft,
  setting: keyof LevelRequirement,
): boolean {
  return levelRequirement(draft.assuranceLevel)[setting] !== undefined;
}

// Whether the draft's own settings can reach its required level. Picking a
// level ticks what it needs (withAssuranceLevel); a draft that still cannot
// reach it is not saved.
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

// The requested data a draft collects: ticked, with its step in the draft.
export function collectedAttributes(draft: ProofingFlowDraft): string[] {
  return REQUESTED_ATTRIBUTES.map((a) => a.value).filter(
    (value) =>
      draft.requestedAttributes.has(value) && attributeAvailable(draft, value),
  );
}

// The reason a draft gives for an item in a language; "" when there is none.
export function draftReason(
  draft: ProofingFlowDraft,
  attribute: string,
  locale: string,
): string {
  return (draft.attributeReasons[attribute]?.[locale] ?? "").trim();
}

// The languages of locales a draft gives an item no reason in: the fields the
// editor marks, each saying which language is missing.
export function missingReasonLocales<L extends string>(
  draft: ProofingFlowDraft,
  attribute: string,
  locales: readonly L[],
): L[] {
  return locales.filter(
    (locale) => draftReason(draft, attribute, locale) === "",
  );
}

// The languages of locales (every supported one when empty) a hosted page
// cannot offer yet: a flow's active version gives an item it collects no
// reason in them (proofing.hostedOffersReasons). None for a version saved
// before reasons existed: it gives none at all and may offer any language.
export function localesWithoutReasons(
  flow: {
    requestedAttributes?: readonly string[];
    attributeReasons?: AttributeReasons;
  },
  locales: readonly string[],
): Language[] {
  const reasons = flow.attributeReasons ?? {};
  if (Object.keys(reasons).length === 0) {
    return [];
  }
  return SUPPORTED_LANGUAGES.filter(
    (locale) =>
      (locales.length === 0 || locales.includes(locale)) &&
      (flow.requestedAttributes ?? []).some(
        (attribute) =>
          attribute !== OUTCOME_ONLY &&
          (reasons[attribute]?.[locale] ?? "").trim() === "",
      ),
  );
}

// A new flow starts with every step the Idem app runs on and the mandatory
// checks on, and receives nothing but the outcome until an item is ticked
// (data minimisation; required diplomas tick Name and document details,
// identityRequired), so it asks no reason yet. The document photo starts off:
// the app cannot take it yet. It accepts any document, leaves the BSN out,
// blacks it out on document photos and keeps no face photos.
export function emptyFlowDraft(): ProofingFlowDraft {
  return {
    name: "",
    documentAndChip: true,
    documentPhoto: false,
    faceVerification: true,
    chipAuthentication: false,
    liveness: false,
    faceProvider: FACE_PROVIDERS[0],
    faceMatchThreshold: null,
    requestedAttributes: new Set(),
    ageOver: String(DEFAULT_AGE_OVER),
    attributeReasons: {},
    documentKinds: new Set(DOCUMENT_KINDS),
    otherDocumentTypes: [],
    storedDocumentTypes: null,
    countryScope: "any",
    issuingCountries: [],
    assuranceLevel: "",
    bsnPolicy: NEW_FLOW_BSN_POLICY,
    blackOutBsn: true,
    keepFacePhotos: false,
    retentionSeconds: FOLLOW_ORG,
    sessionTtlSeconds: FOLLOW_ORG,
    carried: {},
  };
}

function oneOf<T extends string>(
  values: readonly T[],
  value: string | undefined,
): Inherit<T> {
  return values.includes(value as T) ? (value as T) : "";
}

// A flow as any list returns it, without the list's own selection flags: what
// the editor needs to seed a new version.
export type EditableFlow = Omit<ProofingFlow, "allowed" | "default">;

// The editor state for a new version of an existing flow, as stored, with what
// its level needs ticked.
export function draftFromFlow(flow: EditableFlow): ProofingFlowDraft {
  const checks = new Set(flow.requiredChecks ?? []);
  const storedTypes = flow.acceptedDocumentTypes ?? [];
  const storedCountries = flow.acceptedIssuingCountries ?? [];
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
    faceMatchThreshold: flow.checkThresholds?.[CHECK_FACE_MATCH] ?? null,
    requestedAttributes: new Set(flow.requestedAttributes ?? []),
    ageOver: String(flow.ageOver ?? DEFAULT_AGE_OVER),
    attributeReasons: flow.attributeReasons ?? {},
    documentKinds: storedDocumentKinds(storedTypes),
    otherDocumentTypes: storedTypes.filter(
      (type) => documentKindOf(type) === undefined,
    ),
    storedDocumentTypes: storedTypes,
    countryScope: storedCountries.length === 0 ? "any" : "only",
    issuingCountries: storedCountries,
    assuranceLevel: oneOf(ASSURANCE_LEVELS, flow.requiredAssuranceLevel),
    bsnPolicy: oneOf(BSN_POLICIES, flow.bsnPolicy) || UNSET_BSN_POLICY,
    blackOutBsn: flow.blurBsn ?? false,
    keepFacePhotos: flow.keepFacePhotos ?? false,
    retentionSeconds: flow.retentionOverrideSeconds ?? FOLLOW_ORG,
    sessionTtlSeconds: flow.sessionTtlSeconds ?? FOLLOW_ORG,
    carried: {
      legalBasis: flow.legalBasis,
      processingPurpose: flow.processingPurpose,
      assuranceTiers: flow.assuranceTiers,
    },
  };
  return withAssuranceLevel(draft, draft.assuranceLevel);
}

// A draft the editor cannot send, with the field to fix; null when valid. The
// proofing service checks the rest and explains its own refusal.
export type FlowDraftError =
  | "name"
  | "steps"
  | "identity"
  | "assuranceLevel"
  | "documentTypes"
  | "issuingCountries"
  | "faceMatchThreshold"
  | "ageOver"
  | "reasons";

// requiresIdentity is identityRequired for the flow's kind and diplomas.
// locales are the ones the flow's hosted page offers: every item the draft
// collects needs a reason in each. A page that names none offers every
// supported language.
export function flowDraftError(
  draft: ProofingFlowDraft,
  requiresIdentity = false,
  locales: readonly string[] = SUPPORTED_LANGUAGES,
): FlowDraftError | null {
  if (draft.name.trim() === "") {
    return "name";
  }
  if (draftSteps(draft).length === 0) {
    return "steps";
  }
  if (requiresIdentity && !draftReadsIdentity(draft)) {
    return "identity";
  }
  if (!reachesAssuranceLevel(draft)) {
    return "assuranceLevel";
  }
  if (draft.documentKinds.size === 0 && draft.otherDocumentTypes.length === 0) {
    return "documentTypes";
  }
  if (draft.countryScope === "only" && draft.issuingCountries.length === 0) {
    return "issuingCountries";
  }
  if (draft.faceVerification && draft.faceMatchThreshold !== null) {
    const value = draft.faceMatchThreshold;
    if (
      !Number.isFinite(value) ||
      value < faceMatchFloor(draft.assuranceLevel) ||
      value > FACE_MATCH_MAX
    ) {
      return "faceMatchThreshold";
    }
  }
  if (ageOverInvalid(draft)) {
    return "ageOver";
  }

  const unexplained = collectedAttributes(draft).some(
    (attribute) => missingReasonLocales(draft, attribute, locales).length > 0,
  );
  return unexplained ? "reasons" : null;
}

// Whether draft's age check asks an age the service refuses
// (flow.validateAgeOver).
function ageOverInvalid(draft: ProofingFlowDraft): boolean {
  if (!asksAge(draft)) return false;
  const age = Number(draft.ageOver);
  return !Number.isInteger(age) || age < MIN_AGE_OVER || age > MAX_AGE_OVER;
}

// The document types a draft accepts: as stored while the kinds ticked are
// the ones the stored list reads as, so an unchanged save sends what the
// version had; else the kinds ticked and any stored code no kind covers.
// Every kind ticked and no other code is any document, which an empty list
// says: a flow listing all four kinds would refuse a document whose kind the
// engine cannot tell (flow.DocumentKindOf: a foreigner's card coded ID is a
// residence permit in Belgium, an identity card in Estonia), though it is
// one of them. The Yivi app's credentials always have a kind, so it accepts
// either list.
function acceptedDocumentTypes(draft: ProofingFlowDraft): string[] {
  const ticked = DOCUMENT_KINDS.filter((kind) => draft.documentKinds.has(kind));
  const stored = draft.storedDocumentTypes;
  if (stored !== null) {
    const storedKinds = storedDocumentKinds(stored);
    if (
      ticked.length === storedKinds.size &&
      ticked.every((kind) => storedKinds.has(kind))
    ) {
      return [...stored];
    }
  }
  if (
    ticked.length === DOCUMENT_KINDS.length &&
    draft.otherDocumentTypes.length === 0
  ) {
    return [];
  }
  return [...ticked, ...draft.otherDocumentTypes];
}

// The request body for a draft, as the service's editor builds it. Call only
// on a draft flowDraftError accepts.
export function flowSpecFromDraft(draft: ProofingFlowDraft): ProofingFlowSpec {
  const requiredChecks = draftRequiredChecks(draft);
  const requestedAttributes = collectedAttributes(draft);
  const spec: ProofingFlowSpec = {
    ...draft.carried,
    name: draft.name.trim(),
    steps: draftSteps(draft),
    requiredChecks,
    requestedAttributes:
      requestedAttributes.length > 0 ? requestedAttributes : [OUTCOME_ONLY],
    acceptedDocumentTypes: acceptedDocumentTypes(draft),
    acceptedIssuingCountries:
      draft.countryScope === "only"
        ? draft.issuingCountries.map((country) => country.toUpperCase())
        : [],
    bsnPolicy: draft.bsnPolicy,
    blurBsn: draft.blackOutBsn,
    keepFacePhotos: draft.keepFacePhotos,
  };
  applyDraftOverrides(spec, draft, requestedAttributes);
  return spec;
}

// Sets the spec's fields a draft only sends when it sets them.
function applyDraftOverrides(
  spec: ProofingFlowSpec,
  draft: ProofingFlowDraft,
  requestedAttributes: readonly string[],
): void {
  if (draft.faceVerification) {
    spec.faceProvider = draft.faceProvider;
  }
  if (draft.faceVerification && draft.faceMatchThreshold !== null) {
    spec.checkThresholds = { [CHECK_FACE_MATCH]: draft.faceMatchThreshold };
  }

  const reasons = savedReasons(draft, requestedAttributes);
  if (Object.keys(reasons).length > 0) {
    spec.attributeReasons = reasons;
  }

  if (draft.assuranceLevel !== "") {
    spec.requiredAssuranceLevel = draft.assuranceLevel;
  }
  if (draft.retentionSeconds > 0) {
    spec.retentionOverrideSeconds = draft.retentionSeconds;
  }
  if (draft.sessionTtlSeconds > 0) {
    spec.sessionTtlSeconds = draft.sessionTtlSeconds;
  }
  if (asksAge(draft)) {
    spec.ageOver = Number(draft.ageOver);
  }
}

// The checks a draft requires: the chip's passive authentication, and chip
// authentication on top, with the document and chip; the face match, and
// liveness on top, with the face check.
function draftRequiredChecks(draft: ProofingFlowDraft): string[] {
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
  return requiredChecks;
}

// Whether the draft releases the name and date of birth (readsIdentity): the
// document data, without the age check, which withholds the date.
function draftReadsIdentity(draft: ProofingFlowDraft): boolean {
  return (
    draft.requestedAttributes.has(ATTRIBUTE_DOCUMENT) &&
    attributeAvailable(draft, ATTRIBUTE_DOCUMENT) &&
    !asksAge(draft)
  );
}

// The draft of a flow that must read the identity (identityRequired): the
// document data ticked, and the age check, which withholds the date of birth
// that flow needs, unticked.
export function withIdentity(draft: ProofingFlowDraft): ProofingFlowDraft {
  const requestedAttributes = new Set(draft.requestedAttributes);
  requestedAttributes.add(ATTRIBUTE_DOCUMENT);
  requestedAttributes.delete(ATTRIBUTE_AGE_OVER);
  return { ...draft, requestedAttributes };
}

// Whether the draft releases the age check, which needs an age.
export function asksAge(draft: ProofingFlowDraft): boolean {
  return (
    draft.requestedAttributes.has(ATTRIBUTE_AGE_OVER) &&
    attributeAvailable(draft, ATTRIBUTE_AGE_OVER)
  );
}

// The reasons a draft saves: those it gave for the items it collects, in every
// language it holds, trimmed.
function savedReasons(
  draft: ProofingFlowDraft,
  collected: readonly string[],
): AttributeReasons {
  const saved: AttributeReasons = {};
  for (const attribute of collected) {
    const given = Object.keys(draft.attributeReasons[attribute] ?? {}).flatMap(
      (locale) => {
        const text = draftReason(draft, attribute, locale);
        return text === "" ? [] : [[locale, text] as const];
      },
    );
    if (given.length === 0) {
      continue;
    }

    saved[attribute] = Object.fromEntries(given);
  }
  return saved;
}

// The flows a member may send a request on, and the one the form starts on:
// the admin's default, else the first available (a default the proofing
// service no longer lists is simply absent).
export function sendableFlows<T extends { allowed: boolean; default: boolean }>(
  flows: readonly T[],
): { sendable: T[]; initial: T | undefined } {
  return withInitial(flows.filter((flow) => flow.allowed));
}

// The flows an external person can be sent from the app, and the one the form
// starts on: the org's default, else the first. The list holds every flow for
// an admin and only the ones members may use for a member; one that needs the
// sender's own photo is not completable here (the API sends those).
export function externalFlows<
  T extends { completable: boolean; default: boolean },
>(flows: readonly T[]): { sendable: T[]; initial: T | undefined } {
  return withInitial(flows.filter((flow) => flow.completable));
}

function withInitial<T extends { default: boolean }>(
  sendable: T[],
): { sendable: T[]; initial: T | undefined } {
  return {
    sendable,
    initial: sendable.find((flow) => flow.default) ?? sendable[0],
  };
}

// The selection an admin edited: the ticked flows members may use, and the
// default, which is not one of them by necessity. Dirty when it differs from
// the saved one.
export function editedFlowSelection(
  flows: readonly { id: string }[],
  ticked: ReadonlySet<string>,
  defaultId: string,
  saved: ProofingFlowSelection,
): { selection: ProofingFlowSelection; dirty: boolean } {
  const flowIds = flows.filter((f) => ticked.has(f.id)).map((f) => f.id);
  const defaultFlowId = flows.some((f) => f.id === defaultId) ? defaultId : "";
  const dirty =
    saved.flowIds.length !== flowIds.length ||
    saved.flowIds.some((id) => !ticked.has(id)) ||
    saved.defaultFlowId !== defaultFlowId;
  return { selection: { flowIds, defaultFlowId }, dirty };
}

export function requestSubject(request: {
  subjectName: string;
  subjectEmail: string;
  proofedName?: string;
}): { name: string; verifiedAs: string | undefined } {
  const given = request.subjectName.trim();
  const proofed = request.proofedName?.trim() ?? "";
  const name = given || proofed || request.subjectEmail;
  return {
    name,
    verifiedAs: proofed !== "" && proofed !== name ? proofed : undefined,
  };
}

// Who sent the check: the API token that created it, else the member.
export function requestedBy(
  request: { apiKeyName?: string; requestedByName: string },
  t: TFunction,
): string {
  return request.apiKeyName
    ? t("identityProofing.sessions.viaApiKey", { name: request.apiKeyName })
    : request.requestedByName;
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
      return t("identityProofing.sessions.address.verified");
    case "unchecked":
      return t("identityProofing.sessions.address.unchecked");
    case "unsupported":
      return t("identityProofing.sessions.address.unsupported");
    case "registered":
      return t("identityProofing.sessions.address.registered");
    case "registered_mismatch":
      return t("identityProofing.sessions.address.registeredMismatch");
    case undefined:
      return t("identityProofing.sessions.address.declared");
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
// checks, the wallet's own decisions (a lapse, a pause, a legal duty) and what
// a reviewer found wrong. The person reads it too. The test holds it to the
// backend's codes, both ways.
const REJECTION_REASON_KEYS = {
  DOCUMENT_TYPE_NOT_ACCEPTED: "documentTypeNotAccepted",
  DOCUMENT_COUNTRY_NOT_ACCEPTED: "documentCountryNotAccepted",
  FACE_STEP_NOT_COMPLETED: "faceStepNotCompleted",
  FACE_NO_MATCH: "faceNoMatch",
  AGE_NOT_DISCLOSED: "ageNotDisclosed",
  LIVENESS_FAILED: "livenessFailed",
  DOC_TAMPERED: "docTampered",
  CHIP_CLONE_DETECTED: "chipCloneDetected",
  DOC_EXPIRED: "docExpired",
  ASSURANCE_NOT_MET: "assuranceNotMet",
  IDENTITY_MISMATCH: "identityMismatch",
  MANUAL_REVIEW_REJECTED: "manualReviewRejected",
  REVIEW_NOT_HOLDER: "reviewNotHolder",
  REVIEW_ALTERED: "reviewAltered",
  EVIDENCE_NOT_AS_REQUESTED: "evidenceNotAsRequested",
  REVIEW_LAPSED: "reviewLapsed",
  LEGAL_RETENTION: "legalRetention",
  ORG_PAUSED: "orgPaused",
  EMPLOYMENT_NOT_PROVIDED: "employmentNotProvided",
} as const;

export type ProofingRejectionCode = keyof typeof REJECTION_REASON_KEYS;

export const PROOFING_REJECTION_CODES = Object.keys(
  REJECTION_REASON_KEYS,
) as ProofingRejectionCode[];

// Why a data request whose subject did prove who they are was refused: a
// reviewer refused it, nobody answered it, proofing was paused, or the
// org must keep the data. The subject's page then says the request was
// refused, not that they were not verified.
export type DataRequestRefusal = "reviewer" | "lapsed" | "paused" | "law";

const DATA_REQUEST_REFUSALS: Partial<
  Record<ProofingRejectionCode, DataRequestRefusal>
> = {
  MANUAL_REVIEW_REJECTED: "reviewer",
  REVIEW_NOT_HOLDER: "reviewer",
  REVIEW_ALTERED: "reviewer",
  EVIDENCE_NOT_AS_REQUESTED: "reviewer",
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

// The stages a verify page's stepper shows: what is collected, the app, its
// session, then the address, the diploma upload and the Verzekeringsbericht
// (when the flow has those steps).
export type VerifyStage =
  | "overview"
  | "method"
  | "session"
  | "address"
  | "diplomas"
  | "employment";

export function verifyStages({
  address = false,
  diplomas,
  employment = false,
}: {
  address?: boolean;
  diplomas: boolean;
  employment?: boolean;
}): VerifyStage[] {
  return [
    "overview",
    "method",
    "session",
    ...(address ? (["address"] as const) : []),
    ...(diplomas ? (["diplomas"] as const) : []),
    ...(employment ? (["employment"] as const) : []),
  ];
}

// The apps a verify page's app step offers: both when the Yivi app can run
// the flow, else the Idem app alone. Continue always leads to this step, even
// with one app, because tapping an app is what starts the session: a person
// (or an admin checking a link) who only reads the first step uses nothing up.
export function methodChoices(yiviAvailable: boolean): ProofingMethod[] {
  return yiviAvailable ? ["yivi_app", "idem_app"] : ["idem_app"];
}

// The stage a verify page is at: the address, diploma or employment step
// while it is open, else the page's own step.
export function currentStage({
  step,
  inAddress,
  inDiplomas,
  inEmployment,
}: {
  step: "overview" | "method" | "session";
  inAddress: boolean;
  inDiplomas: boolean;
  inEmployment: boolean;
}): VerifyStage {
  if (inAddress) {
    return "address";
  }
  if (inDiplomas) {
    return "diplomas";
  }
  if (inEmployment) {
    return "employment";
  }
  return step;
}

// What a verify page's running session shows: once its identity check
// finished (approved, or in review) and it asks for them, the address step,
// the diploma step and then the employment step until the subject is done with
// each, else the outcome once settled, the expiry once an unstarted session's
// link ran out, or the session itself. The employment step stays while the
// backend still owes it (employmentOwed): the outcome it hands back is only
// ever the one read after the statement.
export type SessionView =
  | "address"
  | "diplomas"
  | "employment"
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
  employmentMode,
  employmentUntil,
  employmentDone,
  employmentOwed = false,
  secondsLeft,
}: {
  status: string;
  addressMode: AddressMode;
  addressUntil: string | undefined;
  addressDone: boolean;
  diplomaMode: DiplomaMode;
  diplomasUntil: string | undefined;
  diplomasDone: boolean;
  employmentMode: EmploymentMode;
  employmentUntil: string | undefined;
  employmentDone: boolean;
  employmentOwed?: boolean;
  secondsLeft: number;
}): SessionView {
  if (!isProofingLive(status)) {
    if (addressMode !== "off" && addressUntil !== undefined && !addressDone) {
      return "address";
    }
    if (diplomaMode !== "off" && diplomasUntil !== undefined && !diplomasDone) {
      return "diplomas";
    }
    return employmentMode !== "off" &&
      employmentUntil !== undefined &&
      (!employmentDone || employmentOwed)
      ? "employment"
      : "outcome";
  }
  return secondsLeft === 0 && status === "pending" ? "expired" : "running";
}

// Whether a request on flow can be mailed (proofing.ErrDiplomasNeedPage,
// ErrAddressNeedsPage, ErrEmploymentNeedsPage): a flow that asks for
// diplomas, the address or the Verzekeringsbericht has them given on the page
// that runs the session. No flow picked yet is mailable.
export function sendableByMail(
  flow:
    | {
        diplomaMode?: DiplomaMode;
        addressMode?: AddressMode;
        employmentMode?: EmploymentMode;
      }
    | undefined,
): boolean {
  return (
    (flow?.diplomaMode ?? "off") === "off" &&
    (flow?.addressMode ?? "off") === "off" &&
    (flow?.employmentMode ?? "off") === "off"
  );
}

// The document data a flow releases: the holder's name and date of birth.
export const ATTRIBUTE_DOCUMENT = "dg1";

// The age check: only whether the holder is the flow's age or older, derived
// from the chip's date of birth (flow.AttributeAgeOver).
export const ATTRIBUTE_AGE_OVER = "age_over";
// The age a new age check answers for, and the lowest and highest the service
// takes (flow.minAgeOver, flow.maxAgeOver).
const DEFAULT_AGE_OVER = 18;
const MIN_AGE_OVER = 1;
const MAX_AGE_OVER = 120;
// The ages the editor offers: the usual legal limits, as ISO mDL and the EUDI
// PID name them (age_over_NN), and the ones the Yivi passport and id-card
// answer (overNN, openid4vpverifier.AgeClaim).
const AGE_OVER_CHOICES = [12, 16, 18, 21, 65];

// The ages to choose from for a draft: AGE_OVER_CHOICES, plus the draft's own
// age when a flow was saved with another, so editing it keeps it.
export function ageOverChoices(draft: ProofingFlowDraft): number[] {
  const current = Number(draft.ageOver);
  if (!Number.isInteger(current) || AGE_OVER_CHOICES.includes(current)) {
    return AGE_OVER_CHOICES;
  }
  return [...AGE_OVER_CHOICES, current].sort((a, b) => a - b);
}

// The age check's answer as a result's identity shows it ("18 or older":
// Yes or No); none for a flow without the age check.
export function ageOverFact(
  identity: { ageOver?: { age: number; over: boolean } } | undefined,
  t: TFunction,
): { label: string; value: string } | undefined {
  const answer = identity?.ageOver;
  if (answer === undefined) {
    return undefined;
  }
  return {
    label: t("identityProofing.sessions.identity.ageOver", { age: answer.age }),
    value: answer.over
      ? t("identityProofing.sessions.identity.ageOverYes")
      : t("identityProofing.sessions.identity.ageOverNo"),
  };
}

// Whether flow's result carries the holder's name and date of birth (the
// document data, dg1, without the age check, which withholds the date), which
// a request for one known person is matched against (proofing.readsIdentity).
// The service hands a flow back listing what it releases.
export function readsIdentity(
  flow: { requestedAttributes?: string[] } | undefined,
): boolean {
  const attributes = flow?.requestedAttributes ?? [];
  return (
    attributes.includes(ATTRIBUTE_DOCUMENT) &&
    !attributes.includes(ATTRIBUTE_AGE_OVER)
  );
}

// Whether a flow with diplomas or employment must read the name and date of
// birth (proofing.identityRequired): a required diploma's or
// Verzekeringsbericht's holder is checked against them.
export function identityRequired(
  diplomas: DiplomaMode,
  employment: EmploymentMode = "off",
): boolean {
  return diplomas !== "off" || employment !== "off";
}

// What the Verify a person form sends: an identity check on a flow, or one of
// the data requests every org offers, on the org's own flow of that kind.
export type SendKind = "identity" | DataRequestKind;

export const SEND_KINDS: readonly SendKind[] = [
  "identity",
  ...DATA_REQUEST_KINDS,
];

// The send form's flows for kind: the identity flows an external person can be
// sent from the app (externalFlows), or the org's own flow of a data request
// kind, none while they load.
export function sendKindFlows<
  T extends { completable: boolean; default: boolean; kind?: string },
>(
  kind: SendKind,
  identityFlows: readonly T[],
  dataRequestFlows: readonly T[] | undefined,
): { sendable: T[]; initial: T | undefined } {
  if (kind === "identity") {
    return externalFlows(identityFlows);
  }
  return withInitial(
    (dataRequestFlows ?? []).filter((flow) => flow.kind === kind),
  );
}

// What a request names: its flow, or a data request's kind.
export function requestTarget(
  kind: SendKind,
  flowId: string,
): { flowId: string } | { flowKind: DataRequestKind } {
  return kind === "identity" ? { flowId } : { flowKind: kind };
}

// The on-screen page's query: the flow it runs, or the data request kind,
// whose flow it reads itself.
export function verifySearch(kind: SendKind, flowId: string): string {
  const params: Record<string, string> =
    kind === "identity" ? { flow: flowId } : { kind };
  return new URLSearchParams(params).toString();
}

// The data request kind a query names, if it names one.
export function dataRequestKindOf(
  value: string | null,
): DataRequestKind | undefined {
  return DATA_REQUEST_KINDS.find((kind) => kind === value);
}

interface SendFields {
  email: string;
  name: string;
  birthDate: string | undefined;
  // Only this person may pass: set by its switch, on a flow that reads identity.
  passing: boolean;
  // Passing is matched on the name too, so it asks for one whatever the name
  // switch says.
  nameShown: boolean;
  missing: { email: boolean; name: boolean; birthDate: boolean };
}

// What the Verify a person form sends, and what it still lacks. A field whose
// switch is off, or whose channel does not use it, sends nothing even if it
// was filled before; only a mail carries an address.
export function sendRequestFields(input: {
  delivery: ProofingChannel;
  matchable: boolean;
  nameOn: boolean;
  passOn: boolean;
  email: string;
  name: string;
  birthDate: string;
}): SendFields {
  const passing = input.passOn && input.matchable;
  const nameShown = input.nameOn || passing;
  const mailed = input.delivery === "email";
  const email = mailed ? input.email.trim() : "";
  const name = nameShown ? input.name.trim() : "";
  const birthDate = passing ? input.birthDate : undefined;

  return {
    email,
    name,
    birthDate,
    passing,
    nameShown,
    missing: {
      email: mailed && !email.includes("@"),
      name: nameShown && name === "",
      birthDate: passing && input.birthDate === "",
    },
  };
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

// Why an uploaded Verzekeringsbericht was not kept, from its verdict's
// reason. An unknown reason is shown as is.
export function employmentRejectionReason(
  reason: string,
  t: TFunction,
): string {
  switch (reason) {
    case "not_a_statement":
      return t("identityProofing.employment.reasons.notAStatement");
    case "signature_invalid":
      return t("identityProofing.employment.reasons.signatureInvalid");
    case "holder_mismatch":
      return t("identityProofing.employment.reasons.holderMismatch");
    case "stale":
      return t("identityProofing.employment.reasons.stale");
    default:
      return reason;
  }
}

// Whether the employment step can end: once a statement is kept, or the time
// to upload one is up.
export function employmentStepDone(
  kept: ProofingEmployment | undefined,
  secondsLeft: number,
): boolean {
  return kept !== undefined || secondsLeft === 0;
}

// Why a kept statement sends the session to review, as the wallet decides it:
// no current job, or a current UWV benefit or other income beside one.
// undefined for none.
export function employmentReview(
  kept: ProofingEmployment,
): "noJob" | "benefit" | "otherIncome" | undefined {
  if (!kept.employed) return "noJob";
  if (kept.relations.some((r) => r.kind === EMPLOYMENT_KIND.benefit))
    return "benefit";
  if (kept.relations.some((r) => r.kind === EMPLOYMENT_KIND.otherIncome)) {
    return "otherIncome";
  }
  return undefined;
}

// The copy an identity proofing API error shows, keyed on its stable code.
const PROOFING_ERROR_KEYS = {
  no_encryption_key: "identityProofing.errors.noEncryptionKey",
  flow_not_found: "identityProofing.errors.flowNotFound",
  flow_not_completable: "identityProofing.errors.flowNotCompletable",
  flow_not_allowed: "identityProofing.errors.flowNotAllowed",
  member_not_found: "identityProofing.errors.memberNotFound",
  no_default_flow: "identityProofing.errors.noDefaultFlow",
  proofing_paused: "identityProofing.errors.proofingPaused",
  api_key_not_found: "identityProofing.errors.apiKeyNotFound",
  session_not_found: "identityProofing.errors.sessionNotFound",
  not_under_review: "identityProofing.errors.notUnderReview",
  address_not_under_review: "identityProofing.errors.addressNotUnderReview",
  own_review: "identityProofing.errors.ownReview",
  reference_photo_required: "identityProofing.errors.referencePhotoRequired",
  organization_not_found: "identityProofing.errors.organizationNotFound",
  webhook_not_found: "identityProofing.errors.webhookNotFound",
  link_started: "identityProofing.errors.linkStarted",
  session_over: "identityProofing.errors.sessionOver",
  device_active: "identityProofing.errors.deviceActive",
  diplomas_need_page: "identityProofing.errors.diplomasNeedPage",
  hosted_disabled: "identityProofing.errors.hostedDisabled",
  redirect_not_allowed: "identityProofing.errors.redirectNotAllowed",
  redirect_origin_in_use: "identityProofing.errors.redirectOriginInUse",
  data_request_flow: "identityProofing.errors.dataRequestFlow",
  diplomas_closed: "identityProofing.errors.diplomasClosed",
  diplomas_not_asked: "identityProofing.errors.diplomasNotAsked",
  address_needs_page: "identityProofing.errors.addressNeedsPage",
  address_closed: "identityProofing.errors.addressClosed",
  address_not_asked: "identityProofing.errors.addressNotAsked",
  address_not_found: "identityProofing.errors.addressNotFound",
  employment_needs_page: "identityProofing.errors.employmentNeedsPage",
  employment_closed: "identityProofing.errors.employmentClosed",
  employment_not_asked: "identityProofing.errors.employmentNotAsked",
  no_employment_checker: "identityProofing.errors.noEmploymentChecker",
  seal_check_unavailable: "identityProofing.errors.sealCheckUnavailable",
  flow_no_identity: "identityProofing.errors.flowNoIdentity",
  data_flow_for_member: "identityProofing.errors.dataFlowForMember",
  export_unavailable: "identityProofing.errors.exportUnavailable",
  reasons_missing: "identityProofing.errors.reasonsMissing",
  // An admin by role whose mandate was withdrawn: the backend refuses every
  // admin call, though the role still shows the admin's tabs.
  mandate_withdrawn: "identityProofing.errors.mandateWithdrawn",
  legal_retention: "identityProofing.errors.legalRetention",
  // too_large is a diploma or Verzekeringsbericht upload's,
  // payload_too_large a branding logo's.
  too_large: "identityProofing.errors.tooLarge",
  payload_too_large: "identityProofing.errors.tooLarge",
} as const;

const SERVER_MESSAGE_CODES = new Set([
  "too_many_files",
  "invalid_input",
  "rejected_by_provider",
]);

export function proofingErrorMessage(error: unknown, t: TFunction): string {
  const code = errorCode(error);
  if (code !== null && Object.hasOwn(PROOFING_ERROR_KEYS, code)) {
    return t(PROOFING_ERROR_KEYS[code as keyof typeof PROOFING_ERROR_KEYS]);
  }
  if (code !== null && SERVER_MESSAGE_CODES.has(code)) {
    return serverMessage(error) ?? t("identityProofing.errors.generic");
  }
  return t("identityProofing.errors.generic");
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
    case "employment_closed":
    case "seal_check_unavailable":
    case "export_unavailable":
    case "too_large":
    case "payload_too_large":
    case "too_many_files":
    case "invalid_input":
    case "rejected_by_provider":
      return proofingErrorMessage(error, t);
    case "session_over":
      return t("proofLink.errors.sessionOver");
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

// How often the org's webhook deliveries are re-read: soon while one waits
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

// Stats rows summed per key: per flow.
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

const PROOFING_STEPS: ReadonlySet<string> = new Set([
  STEP_DOCUMENT_CAPTURE,
  STEP_NFC_READ,
  STEP_DOCUMENT_PHOTO,
  STEP_FACE_VERIFICATION,
]);

export function isProofingStep(value: string): value is ProofingStep {
  return PROOFING_STEPS.has(value);
}

// A flow version's steps in words, in the order the app runs them; a step the
// wallet has no copy for shows as it is.
export function flowStepLabels(
  flow: {
    steps: readonly string[];
    diplomaMode?: DiplomaMode;
    addressMode?: AddressMode;
  },
  t: TFunction,
): string[] {
  const labels = flow.steps.map((step) =>
    isProofingStep(step) ? t(`identityProofingFlows.steps.${step}`) : step,
  );
  // The wallet's own steps, after the app's: given in the browser.
  if (flow.addressMode === "required") {
    labels.push(t("identityProofingFlows.steps.address"));
  }
  if (flow.diplomaMode === "required") {
    labels.push(t("identityProofingFlows.steps.diploma_upload"));
  }
  return labels;
}

// The people lists' filters: every session, or one outcome; "review" is the
// review queue and "in_progress" a session its subject has not finished.
// Cancelled sessions show under "all" only.
export const SESSION_FILTERS = [
  "all",
  "in_progress",
  "review",
  "verified",
  "failed",
  "expired",
] as const;
export type SessionFilter = (typeof SESSION_FILTERS)[number];

const FILTER_STATUSES: Record<
  Exclude<SessionFilter, "all">,
  readonly string[]
> = {
  in_progress: ["pending", "in_progress"],
  review: ["needs_review"],
  verified: ["approved"],
  failed: ["rejected"],
  expired: ["expired"],
};

export function matchesSessionFilter(
  status: string,
  filter: SessionFilter,
): boolean {
  return filter === "all" || FILTER_STATUSES[filter].includes(status);
}

export function sessionFilterCounts(
  requests: readonly { status: string }[],
): Record<SessionFilter, number> {
  const count = (filter: SessionFilter): number =>
    requests.filter((r) => matchesSessionFilter(r.status, filter)).length;
  return {
    all: requests.length,
    in_progress: count("in_progress"),
    review: count("review"),
    verified: count("verified"),
    failed: count("failed"),
    expired: count("expired"),
  };
}

// Whether a request list is as long as one list read returns: it may then have
// been cut off, so a count read from it is only a lower bound.
export function requestListCapped(requests: readonly unknown[]): boolean {
  return requests.length >= MAX_LISTED_REQUESTS;
}

// A count read from a request list, for display: the number, or the number
// with a "+" when the list may have been cut off, as "200+" for all of them.
export function listedCount(
  count: number,
  capped: boolean,
  t: TFunction,
): string {
  return capped
    ? t("identityProofing.requests.atLeast", { count })
    : String(count);
}

// Whose retention a people list states: the org's across every flow, the one
// flow's it is narrowed to, or none for a flow the caller cannot read (a
// member's, on a flow no longer offered to members), rather than the org's,
// which may not be that flow's.
export type RetentionOf<F> =
  | { kind: "org" }
  | { kind: "flow"; flow: F }
  | { kind: "unknown" };

export function peopleRetentionOf<F extends { id: string }>(
  flowId: string,
  flows: readonly F[] | undefined,
): RetentionOf<F> {
  if (flowId === "") return { kind: "org" };
  const flow = flows?.find((f) => f.id === flowId);
  return flow ? { kind: "flow", flow } : { kind: "unknown" };
}

// The flows requests were sent on, by name: what a flow filter offers. The
// requests are newest first and each keeps the name its flow had when it was
// sent, so the first one seen names the flow as it is now.
export function requestFlows(
  requests: readonly { flowId: string; flowName: string }[],
): { id: string; name: string }[] {
  const names = new Map<string, string>();
  for (const r of requests) {
    if (!names.has(r.flowId)) {
      names.set(r.flowId, r.flowName);
    }
  }

  return [...names]
    .map(([id, name]) => ({ id, name }))
    .sort((a, b) => a.name.localeCompare(b.name));
}

const SECONDS_PER_DAY = 86_400;

// How many days after a check ends a flow's personal data is purged: the flow's
// own retention when it sets one, else the org's.
export function flowPurgeDays(
  flow: { retentionOverrideSeconds?: number },
  settings: { dataRetentionDays: number },
): number {
  const override = flow.retentionOverrideSeconds ?? 0;
  return override > 0
    ? Math.ceil(override / SECONDS_PER_DAY)
    : settings.dataRetentionDays;
}

// The days a flow may keep its finished sessions, besides following the org's
// retention (0).
const FLOW_RETENTION_DAYS = [1, 30, 90] as const;

// The retentions a flow's picker offers, in seconds. A flow whose own is none
// of the offered days keeps it as a choice, so opening it changes nothing.
export function retentionChoices(current: number): number[] {
  const offered = FLOW_RETENTION_DAYS.map((days) => days * SECONDS_PER_DAY);
  const own = current > 0 && !offered.includes(current) ? [current] : [];
  return [FOLLOW_ORG, ...[...offered, ...own].sort((a, b) => a - b)];
}

// Seconds as whole days; undefined for a part of a day.
export function wholeDays(seconds: number): number | undefined {
  return seconds % SECONDS_PER_DAY === 0
    ? seconds / SECONDS_PER_DAY
    : undefined;
}

const MS_PER_SECOND = 1000;
const SECONDS_PER_MINUTE = 60;
const SECONDS_PAD = 2;

// How long a finished session ran, in whole seconds: from the send to its
// outcome, or to its expiry for one nobody finished. Undefined while it runs.
export function sessionDurationSeconds(request: {
  status: string;
  createdAt: string;
  completedAt?: string;
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

// The session lifetimes a flow may set, mirroring SessionTTLOptions in
// backend/internal/proofing (identity-proofing.test.ts holds the two
// together).
export const SESSION_TTL_OPTIONS_SECONDS = [120, 300, 600] as const;

const SECONDS_IN_MINUTE = 60;

export function ttlMinutes(seconds: number): number {
  return Math.round(seconds / SECONDS_IN_MINUTE);
}

const MAIL_ADDRESS = /^[^\s@]+@[^\s@]+$/;
const MAILTO_SCHEME = "mailto:";
const WEB_PROTOCOLS: ReadonlySet<string> = new Set(["http:", "https:"]);

interface SupportLink {
  href: string;
  kind: "mail" | "web";
}

// Where a support contact leads: a mail address opens the mail app and a web
// address its page. Anything else (a phone number, opening hours) is text.
export function supportContactLink(contact: string): SupportLink | undefined {
  const value = contact.trim();

  // Not URL.canParse: Safari 16 lacks it.
  let url: URL | undefined;
  try {
    url = new URL(value);
  } catch {
    url = undefined;
  }

  // Before the mail test: a /@handle path or user@host matches MAIL_ADDRESS.
  if (url && WEB_PROTOCOLS.has(url.protocol)) {
    return { href: url.href, kind: "web" };
  }

  const address = value.toLowerCase().startsWith(MAILTO_SCHEME)
    ? value.slice(MAILTO_SCHEME.length)
    : value;

  return MAIL_ADDRESS.test(address)
    ? { href: `${MAILTO_SCHEME}${address}`, kind: "mail" }
    : undefined;
}

// How long a mailed session runs on a flow: its own lifetime, else the
// organisation's, which is unknown until the settings are read.
export function mailLifetimeMinutes(
  flow: Pick<ProofingFlow, "sessionTtlSeconds"> | undefined,
  orgSeconds: number | undefined,
): number | undefined {
  const own = flow?.sessionTtlSeconds ?? FOLLOW_ORG;
  const seconds = own === FOLLOW_ORG ? orgSeconds : own;

  return seconds === undefined ? undefined : ttlMinutes(seconds);
}

// A delivery answered 2xx.
const HTTP_SUCCESS_CLASS = 2;
const HTTP_STATUS_CLASS_DIVISOR = 100;

export function isSuccessStatus(code: number): boolean {
  return Math.floor(code / HTTP_STATUS_CLASS_DIVISOR) === HTTP_SUCCESS_CLASS;
}

// The deliveries to the org's own endpoint. Those to the wallet's default
// endpoint carry no endpointUrl and are the wallet's, not the org's software.
export function ownEndpointDeliveries(
  deliveries: readonly WebhookDelivery[],
): WebhookDelivery[] {
  return deliveries.filter((d) => d.endpointUrl !== undefined);
}

// The branding a flow's mail and hosted page wear: its own fields where it is
// custom and set them, else the org's. "Powered by" stays the org's. Mirrors
// Branding.over in backend/internal/proofing/flow_branding.go.
export function effectiveBranding(
  org: ProofingBranding,
  own: ProofingFlowBranding,
): ProofingBranding {
  if (!own.custom) {
    return org;
  }
  return {
    ...org,
    displayName: own.displayName || org.displayName,
    primaryColor: own.primaryColor || org.primaryColor,
    supportContact: own.supportContact || org.supportContact,
    privacyUrl: own.privacyUrl || org.privacyUrl,
    logoUri: own.logoUri || org.logoUri,
  };
}

// Whether saving a flow's Branding tab deletes branding of its own: it was
// custom and goes back to the organisation's, its fields and logo included.
export function dropsOwnBranding(
  saved: ProofingFlowBranding,
  custom: boolean,
): boolean {
  return saved.custom && !custom;
}

// The fields that give a branding a look of its own in the Idem app: a colour
// or the font. A name, logo, support contact or privacy link alone do not.
const APP_LOOK_FIELDS = [
  "primaryColor",
  "secondaryColor",
  "textColor",
  "surfaceColor",
  "borderColor",
  "linkColor",
  "successColor",
  "warningColor",
  "errorColor",
  "topbarColor",
  "fontFamily",
] as const satisfies readonly (keyof ProofingBranding)[];

// What the Idem app shows a person outside the org of a flow's branding (see
// effectiveBranding): the name its mail signs with, its colour, the app's
// header and the logo; "" keeps the app's own. A branding with a look of its
// own is shown as it is; otherwise the org's theme, with the branding's logo
// when it has one. Mirrors AppBrandingResolver and whiteLabeled in
// backend/internal/proofing/app_branding.go.
export interface AppLook {
  name: string;
  primaryColor: string;
  headerColor: string;
  logoUri: string;
}

export function appLook(
  branding: ProofingBranding,
  theme: Pick<OrgTheme, "primaryColor" | "topbarColor" | "logoUri">,
  orgName: string,
): AppLook {
  const name = branding.displayName || orgName;
  if (APP_LOOK_FIELDS.some((field) => branding[field] !== "")) {
    return {
      name,
      primaryColor: branding.primaryColor,
      headerColor: branding.topbarColor,
      logoUri: branding.logoUri,
    };
  }
  return {
    name,
    primaryColor: theme.primaryColor,
    headerColor: theme.topbarColor,
    logoUri: branding.logoUri || theme.logoUri,
  };
}

// The languages a hosted page form shows ticked: none saved is every one.
export function selectedLocales(saved: readonly string[]): string[] {
  return saved.length === 0 ? [...SUPPORTED_LANGUAGES] : [...saved];
}

// Every language ticked is saved as none, so a language added later is offered
// too.
export function savedLocales(selected: readonly string[]): string[] {
  return selected.length === SUPPORTED_LANGUAGES.length ? [] : [...selected];
}

// The return page a Hosted page form saves: trimmed, and none on a flow that
// always ends on its thank-you page.
export function savedReturnUrl(draft: ProofingFlowHosted): string {
  return draft.completion === "redirect" ? draft.returnUrl.trim() : "";
}

// What a Hosted page form saves.
export function hostedToSave(draft: ProofingFlowHosted): ProofingFlowHosted {
  return {
    ...draft,
    locales: savedLocales(draft.locales),
    returnUrl: savedReturnUrl(draft),
  };
}

// A flow's Hosted page form as saved: Cancel goes back to it.
export function hostedDraft(saved: ProofingFlowHosted): ProofingFlowHosted {
  return { ...saved, locales: selectedLocales(saved.locales) };
}

// Whether the Hosted page form holds a change from what is saved.
export function hostedDraftDirty(
  draft: ProofingFlowHosted,
  saved: ProofingFlowHosted,
): boolean {
  const seed = hostedDraft(saved);
  return (
    draft.enabled !== seed.enabled ||
    draft.completion !== seed.completion ||
    savedReturnUrl(draft) !== seed.returnUrl ||
    draft.locales.join() !== seed.locales.join()
  );
}

// The reason an item is collected, in the language the person reads: "nl-NL"
// reads Dutch. None when the flow gives none in it, so the page says nothing
// rather than something in a language the person may not read.
export function reasonIn(
  reasons: Readonly<Record<string, string>> | undefined,
  language: string,
): string | undefined {
  const text = reasons?.[language.split("-")[0].toLowerCase()]?.trim();
  return text === "" ? undefined : text;
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

// A colour field that holds something and not a #rrggbb; empty falls back.
export function isInvalidColor(value: string): boolean {
  return value !== "" && !isHexColor(value);
}

export function readableTextOn(hex: string): "#ffffff" | "#000000" {
  if (!isHexColor(hex)) {
    return "#ffffff";
  }
  const luminance = LUMINANCE_WEIGHTS.reduce((sum, weight, i) => {
    const start = 1 + i * HEX_PAIR;
    const c =
      Number.parseInt(hex.slice(start, start + HEX_PAIR), HEX_RADIX) /
      CHANNEL_MAX;
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
    parts.push(t("identityProofing.flows.eidas", { level: fields.eidasLevel }));
  }
  if (typeof fields.errorCode === "string" && fields.errorCode !== "") {
    parts.push(proofingRejectionReason(fields.errorCode, t));
  }
  if (typeof fields.flowVersion === "number") {
    parts.push(
      t("identityProofingFlows.versionShort", { version: fields.flowVersion }),
    );
  }
  return [...parts, ...decisionEventDetail(fields, t)];
}

// What a review or address event adds: that an entered address goes to a
// reviewer, what a reviewer decided, and why. The backend leaves the reason
// out for a reader who may not see it.
function decisionEventDetail(
  fields: Record<string, unknown>,
  t: TFunction,
): string[] {
  const parts: string[] = [];
  if (
    typeof fields.verification === "string" &&
    fields.verification !== "registered"
  ) {
    parts.push(t("identityProofing.sessions.address.toReview"));
  }
  if (fields.decision === "approved" || fields.decision === "rejected") {
    parts.push(t(`identityProofing.reviews.outcome.${fields.decision}`));
  }
  if (
    fields.addressReview === "confirmed" ||
    fields.addressReview === "rejected"
  ) {
    parts.push(
      t(`identityProofing.sessions.address.tag.${fields.addressReview}`),
    );
  }
  if (typeof fields.reason === "string" && fields.reason !== "") {
    parts.push(
      t("identityProofing.reviews.decided.reason", { reason: fields.reason }),
    );
  }
  return parts;
}

// What the send form does with the flow and channel it is given: the flow it
// sends (a pick no longer sendable falls back to the default), how (a flow that
// asks for diplomas, the address or employment cannot be mailed, so it runs on
// screen; one whose "Allow sending this flow as a link" switch is off is
// neither mailed nor a link, so it runs on screen too) and whether it can
// check for one person (a
// flow reading name and date of birth). sendRequestFields has what the fields
// send and lack.
export function sendFormDecision<
  F extends {
    id: string;
    diplomaMode?: DiplomaMode;
    addressMode?: AddressMode;
    employmentMode?: EmploymentMode;
    requestedAttributes?: string[];
    hostedEnabled?: boolean;
  },
>(input: {
  sendable: readonly F[];
  initial: F | undefined;
  picked: string;
  channel: ProofingChannel;
}): {
  flowId: string;
  linkable: boolean;
  mailable: boolean;
  matchable: boolean;
  delivery: ProofingChannel;
} {
  const flowId = input.sendable.some((f) => f.id === input.picked)
    ? input.picked
    : (input.initial?.id ?? "");
  const flow = input.sendable.find((f) => f.id === flowId);
  const linkable = flow?.hostedEnabled ?? true;
  const mailable = sendableByMail(flow);
  const matchable = readsIdentity(flow);
  const delivery: ProofingChannel =
    !linkable || (input.channel === "email" && !mailable)
      ? "on_screen"
      : input.channel;
  return { flowId, linkable, mailable, matchable, delivery };
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

// Who a session timeline event names as its actor: the member, the API key for
// a request it created, a non-user actor's label, or the system; null when the
// event's detail already says who.
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
    return t("identityProofing.sessions.viaApiKey", {
      name: request.apiKeyName,
    });
  }
  const label = auditActorLabel(event.actorLabel, t);
  if (label) return label;
  return detail.length > 0 ? null : t("auditLog.system");
}

// The last decision on a session's address in its timeline: who decided, when
// and why (absent for a reader the backend leaves the reason out for).
export interface AddressDecision {
  by: string | null;
  at: string;
  reason?: string;
}

export function latestAddressDecision(
  events: readonly AuditEvent[],
): AddressDecision | undefined {
  const event = events.findLast(
    (e) => e.action === "identity_proofing.address_reviewed",
  );
  if (event === undefined) {
    return undefined;
  }
  const after = event.metadata.after;
  const reason =
    typeof after === "object" &&
    after !== null &&
    "reason" in after &&
    typeof after.reason === "string" &&
    after.reason !== ""
      ? after.reason
      : undefined;
  return {
    by: event.actor ? fullName(event.actor) : null,
    at: event.occurredAt,
    reason,
  };
}
