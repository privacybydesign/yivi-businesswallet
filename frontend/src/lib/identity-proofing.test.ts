import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/http";
import i18n, { SUPPORTED_LANGUAGES } from "../i18n";
import type {
  ProofingBranding,
  ProofingFlow,
  ProofingFlowBranding,
  ProofingFlowHosted,
  WebhookDelivery,
} from "../api/identity-proofing";
import {
  DATA_REQUEST_KINDS,
  DIPLOMA_MODES,
  ERASURE_HANDLINGS,
  FLOW_KINDS,
  MAX_LISTED_REQUESTS,
} from "../api/identity-proofing";
import {
  ATTRIBUTE_AGE_OVER,
  DOCUMENT_KINDS,
  REQUESTED_ATTRIBUTES,
  PROOFING_REJECTION_CODES,
  addressLookup,
  addressVerificationNote,
  matchingAddress,
  ageOverChoices,
  ageOverFact,
  dataRequestRefusal,
  deliveriesPollInterval,
  hostedErrorMessage,
  identityRequired,
  proofingRejectionReason,
  effectiveBranding,
  appLook,
  dropsOwnBranding,
  hostedDraft,
  hostedDraftDirty,
  savedLocales,
  selectedLocales,
  diplomaRejectionReason,
  diplomaStepDone,
  OUTCOME_ONLY,
  MAX_ATTRIBUTE_REASON_CHARS,
  collectedAttributes,
  draftReason,
  localesWithoutReasons,
  missingReasonLocales,
  reasonIn,
  mailLifetimeMinutes,
  readsIdentity,
  sendableByMail,
  sessionView,
  sendRequestFields,
  verifyStages,
  methodChoices,
  currentStage,
  progressPollContinues,
  reviewPollContinues,
  reviewSubmitStep,
  matchTickedByDefault,
  externalFlows,
  flowPurgeDays,
  flowStepLabels,
  attributeAvailable,
  draftFromFlow,
  draftSteps,
  lockedByLevel,
  retentionChoices,
  wholeDays,
  withAssuranceLevel,
  withIdentity,
  editedFlowSelection,
  emptyFlowDraft,
  FACE_MATCH_RECOMMENDED,
  faceMatchFloor,
  faceMatchValue,
  flowDraftError,
  formatFaceMatch,
  reachesAssuranceLevel,
  flowSpecFromDraft,
  formatDuration,
  secondsUntil,
  isProofingLive,
  isProofingStep,
  isRequestedAttribute,
  proofingErrorMessage,
  matchesSessionFilter,
  proofingStatsBy,
  proofingStatusLabel,
  listedCount,
  peopleRetentionOf,
  requestFlows,
  requestListCapped,
  requestSubject,
  sendableFlows,
  sessionDurationSeconds,
  sessionFilterCounts,
  shortRequestId,
  sumProofingStats,
  verifiedShare,
  isHexColor,
  isSuccessStatus,
  ownEndpointDeliveries,
  proofingMethodLabel,
  yiviAppAvailable,
  sessionEventDetail,
  readableTextOn,
  SESSION_TTL_OPTIONS_SECONDS,
  SUBSTANTIAL_FACE_MATCH_FLOOR,
  supportContactLink,
} from "./identity-proofing";
import type { ProofingFlowDraft } from "./identity-proofing";
import { REJECT_REASONS } from "./proofing-reviews";

const t = i18n.getFixedT("en");

function apiError(status: number, code: string, message: string): ApiError {
  return new ApiError(status, "", "/api/v1/x", { error: message, code });
}

// A reason for every item in every language, so a test's draft is refused
// only for what the test is about.
const EXPLAINED = Object.fromEntries(
  REQUESTED_ATTRIBUTES.map(({ value }) => [
    value,
    { en: `Why ${value}`, nl: `Waarom ${value}` },
  ]),
);

function draft(patch: Partial<ProofingFlowDraft> = {}): ProofingFlowDraft {
  return {
    ...emptyFlowDraft(),
    name: "  Passport check ",
    attributeReasons: EXPLAINED,
    ...patch,
  };
}

// Every item What you receive offers, ticked, but the age check, which
// withholds the date of birth from the document data next to it.
const EVERY_ATTRIBUTE: ReadonlySet<string> = new Set(
  REQUESTED_ATTRIBUTES.map((a) => a.value).filter(
    (value) => value !== ATTRIBUTE_AGE_OVER,
  ),
);

describe("flowSpecFromDraft", () => {
  it("sends the age check with its age instead of the document data", () => {
    const spec = flowSpecFromDraft(
      draft({
        requestedAttributes: new Set(["age_over", "chip_checks"]),
        ageOver: "16",
      }),
    );
    expect(spec.requestedAttributes).toEqual(["age_over", "chip_checks"]);
    expect(spec.ageOver).toBe(16);
    expect(flowSpecFromDraft(draft()).ageOver).toBeUndefined();
  });

  it("sends the document scan and chip scan as a pair with passive auth locked on", () => {
    const spec = flowSpecFromDraft(
      draft({ faceVerification: false, requestedAttributes: EVERY_ATTRIBUTE }),
    );
    expect(spec).toMatchObject({
      name: "Passport check",
      steps: ["document_capture", "nfc_read"],
      requiredChecks: ["nfc.passive_auth"],
      requestedAttributes: ["dg1", "chip_checks", "dg11", "dg2"],
    });
    expect(spec.checkThresholds).toBeUndefined();
  });

  it("locks face match on with face verification and keeps the extras optional", () => {
    const spec = flowSpecFromDraft(
      draft({
        chipAuthentication: true,
        liveness: true,
        faceMatchThreshold: 0.8,
      }),
    );
    expect(spec.steps).toEqual([
      "document_capture",
      "nfc_read",
      "face_verification",
    ]);
    expect(spec.requiredChecks).toEqual([
      "nfc.passive_auth",
      "nfc.chip_auth",
      "face.match",
      "face.liveness",
    ]);
    expect(spec.checkThresholds).toEqual({ "face.match": 0.8 });
  });

  it("drops data and checks whose step is not in the flow", () => {
    const spec = flowSpecFromDraft(
      draft({
        documentAndChip: false,
        chipAuthentication: true,
        requestedAttributes: EVERY_ATTRIBUTE,
      }),
    );
    expect(spec.steps).toEqual(["face_verification"]);
    expect(spec.requiredChecks).toEqual(["face.match"]);
    expect(spec.requestedAttributes).toEqual(["biometrics", "selfie"]);
  });

  it("lets optional data be deselected", () => {
    const spec = flowSpecFromDraft(
      draft({ requestedAttributes: new Set(["dg1", "biometrics"]) }),
    );
    expect(spec.requestedAttributes).toEqual(["dg1", "biometrics"]);
  });

  it("sends what the admin chose, the BSN and redaction always explicit", () => {
    const spec = flowSpecFromDraft(
      draft({
        documentKinds: new Set(["passport"]),
        countryScope: "only",
        issuingCountries: ["NLD", "bel"],
        assuranceLevel: "substantial",
        bsnPolicy: "mask",
        blackOutBsn: false,
        keepFacePhotos: true,
        retentionSeconds: 3600,
        sessionTtlSeconds: 300,
      }),
    );
    expect(spec).toMatchObject({
      acceptedDocumentTypes: ["passport"],
      acceptedIssuingCountries: ["NLD", "BEL"],
      requiredAssuranceLevel: "substantial",
      bsnPolicy: "mask",
      blurBsn: false,
      keepFacePhotos: true,
      retentionOverrideSeconds: 3600,
      sessionTtlSeconds: 300,
    });
  });

  it("follows the org where the admin chose nothing", () => {
    const spec = flowSpecFromDraft(draft());
    expect(spec.retentionOverrideSeconds).toBeUndefined();
    expect(spec.sessionTtlSeconds).toBeUndefined();
    expect(spec.requiredAssuranceLevel).toBeUndefined();
    expect(spec.acceptedDocumentTypes).toEqual([]);
    expect(spec.acceptedIssuingCountries).toEqual([]);
  });

  it("leaves the BSN out, blacks it out and keeps no face photos in a new flow", () => {
    expect(flowSpecFromDraft(draft())).toMatchObject({
      bsnPolicy: "omit",
      blurBsn: true,
      keepFacePhotos: false,
    });
  });

  it("receives only the outcome in a new flow", () => {
    expect(emptyFlowDraft().requestedAttributes.size).toBe(0);
    expect(flowSpecFromDraft(draft()).requestedAttributes).toEqual([
      OUTCOME_ONLY,
    ]);
  });
});

describe("accepted documents", () => {
  const stored = {
    id: "f",
    version: 1,
    active: true,
    name: "F",
    steps: ["document_capture", "nfc_read"],
    createdAt: "2026-09-28T00:00:00Z",
    completable: true,
  };

  it("accepts any document with every kind ticked, as a new flow does", () => {
    expect(flowSpecFromDraft(draft()).acceptedDocumentTypes).toEqual([]);
  });

  it("names the kinds that are ticked, as the engine names them", () => {
    for (const kind of DOCUMENT_KINDS) {
      expect(
        flowSpecFromDraft(draft({ documentKinds: new Set([kind]) }))
          .acceptedDocumentTypes,
      ).toEqual([kind]);
    }
    expect(
      flowSpecFromDraft(
        draft({ documentKinds: new Set(["identity_card", "passport"]) }),
      ).acceptedDocumentTypes,
    ).toEqual(["passport", "identity_card"]);
  });

  it("refuses a flow that accepts no kind", () => {
    expect(flowDraftError(draft({ documentKinds: new Set() }))).toBe(
      "documentTypes",
    );
  });

  it("reads a stored list back as kinds and keeps a code no kind covers", () => {
    expect(draftFromFlow(stored).documentKinds).toEqual(
      new Set(DOCUMENT_KINDS),
    );
    const codes = draftFromFlow({
      ...stored,
      acceptedDocumentTypes: ["P", "I", "ID"],
    });
    expect(codes.documentKinds).toEqual(new Set(["passport", "identity_card"]));
    expect(codes.otherDocumentTypes).toEqual([]);
    const mixed = draftFromFlow({
      ...stored,
      acceptedDocumentTypes: ["P", "IR"],
    });
    expect(mixed.documentKinds).toEqual(new Set(["passport"]));
    expect(mixed.otherDocumentTypes).toEqual(["IR"]);
    const uppercased = draftFromFlow({
      ...stored,
      acceptedDocumentTypes: ["DRIVERS_LICENSE", "Residence_Permit"],
    });
    expect(uppercased.documentKinds).toEqual(
      new Set(["drivers_license", "residence_permit"]),
    );
  });

  it("sends a stored list unchanged while its kinds are ticked", () => {
    for (const types of [
      [],
      ["P", "I"],
      ["P", "ID", "IR"],
      ["DRIVERS_LICENSE"],
      [...DOCUMENT_KINDS],
    ]) {
      const unchanged = draftFromFlow({
        ...stored,
        acceptedDocumentTypes: types,
      });
      expect(flowSpecFromDraft(unchanged).acceptedDocumentTypes).toEqual(types);
    }
  });

  it("sends kinds once the kinds ticked change, keeping a code no kind covers", () => {
    const codes = draftFromFlow({
      ...stored,
      acceptedDocumentTypes: ["P", "I"],
    });
    const more = {
      ...codes,
      documentKinds: new Set([...codes.documentKinds, "residence_permit"]),
    } satisfies ProofingFlowDraft;
    expect(flowSpecFromDraft(more).acceptedDocumentTypes).toEqual([
      "passport",
      "identity_card",
      "residence_permit",
    ]);
    const mixed = draftFromFlow({
      ...stored,
      acceptedDocumentTypes: ["P", "IR"],
    });
    const withCards = {
      ...mixed,
      documentKinds: new Set([...mixed.documentKinds, "identity_card"]),
    } satisfies ProofingFlowDraft;
    expect(flowSpecFromDraft(withCards).acceptedDocumentTypes).toEqual([
      "passport",
      "identity_card",
      "IR",
    ]);
    // Every kind ticked is any document again.
    const every = { ...codes, documentKinds: new Set(DOCUMENT_KINDS) };
    expect(flowSpecFromDraft(every).acceptedDocumentTypes).toEqual([]);
  });
});

// The kinds the editor saves are the ones the proofing engine reads.
describe("document kinds mirror backend/internal/proofingengine", () => {
  it("saves every kind the engine names and no other", () => {
    const source = readFileSync(
      fileURLToPath(
        new URL(
          "../../../backend/internal/proofingengine/flow/flow.go",
          import.meta.url,
        ),
      ),
      "utf8",
    );
    const constant = (name: string): string | undefined =>
      new RegExp(`\\b${name}\\s*= "([a-z_]+)"`).exec(source)?.[1];
    const engine = [
      "DocumentKindPassport",
      "DocumentKindIdentityCard",
      "DocumentKindResidencePermit",
      "DocumentTypeDrivingLicence",
    ].map(constant);
    expect(new Set(engine)).toEqual(new Set(DOCUMENT_KINDS));
    expect(engine).toHaveLength(DOCUMENT_KINDS.length);
  });
});

describe("issuing countries", () => {
  it("sends none for any country, whatever was picked before", () => {
    const picked = draft({ issuingCountries: ["NLD"], countryScope: "any" });
    expect(flowSpecFromDraft(picked).acceptedIssuingCountries).toEqual([]);
  });

  it("needs a country once only some are accepted", () => {
    expect(
      flowDraftError(draft({ countryScope: "only", issuingCountries: [] })),
    ).toBe("issuingCountries");
    expect(
      flowDraftError(
        draft({ countryScope: "only", issuingCountries: ["NLD"] }),
      ),
    ).toBeNull();
  });

  it("reads a stored list as the chosen countries", () => {
    const flow = {
      id: "f",
      version: 1,
      active: true,
      name: "F",
      steps: ["nfc_read", "document_capture"],
      createdAt: "2026-09-28T00:00:00Z",
      completable: true,
    };
    expect(draftFromFlow(flow)).toMatchObject({
      countryScope: "any",
      issuingCountries: [],
    });
    expect(
      draftFromFlow({ ...flow, acceptedIssuingCountries: ["NLD", "BEL"] }),
    ).toMatchObject({
      countryScope: "only",
      issuingCountries: ["NLD", "BEL"],
    });
  });
});

describe("what a stored flow without a setting shows", () => {
  const flow = {
    id: "f",
    version: 1,
    active: true,
    name: "F",
    steps: ["nfc_read", "document_capture"],
    createdAt: "2026-09-28T00:00:00Z",
    completable: true,
  };

  it("shows what the engine does with it: the BSN left out, the face photos dropped, nothing blacked out", () => {
    expect(draftFromFlow(flow)).toMatchObject({
      bsnPolicy: "omit",
      blackOutBsn: false,
      keepFacePhotos: false,
      retentionSeconds: 0,
      sessionTtlSeconds: 0,
    });
  });

  it("shows what it sets", () => {
    expect(
      draftFromFlow({
        ...flow,
        bsnPolicy: "mask",
        blurBsn: true,
        keepFacePhotos: true,
        retentionOverrideSeconds: 86_400,
        sessionTtlSeconds: 120,
      }),
    ).toMatchObject({
      bsnPolicy: "mask",
      blackOutBsn: true,
      keepFacePhotos: true,
      retentionSeconds: 86_400,
      sessionTtlSeconds: 120,
    });
  });
});

describe("retentionChoices", () => {
  const day = 86_400;

  it("offers the org's retention and 1, 30 and 90 days", () => {
    expect(retentionChoices(0)).toEqual([0, day, 30 * day, 90 * day]);
    expect(retentionChoices(30 * day)).toEqual([0, day, 30 * day, 90 * day]);
  });

  it("keeps a retention of the flow's own as a choice, in order", () => {
    expect(retentionChoices(7 * day)).toEqual([
      0,
      day,
      7 * day,
      30 * day,
      90 * day,
    ]);
    expect(retentionChoices(3600)).toContain(3600);
  });

  it("counts whole days only", () => {
    expect(wholeDays(7 * day)).toBe(7);
    expect(wholeDays(3600)).toBeUndefined();
  });
});

describe("face provider", () => {
  it("sends the provider only with the face step, Regula by default", () => {
    expect(flowSpecFromDraft(draft({})).faceProvider).toBe("regula");
    expect(
      flowSpecFromDraft(
        draft({ faceProvider: "regula", faceVerification: false }),
      ).faceProvider,
    ).toBeUndefined();
  });

  it("round-trips from a stored version, Regula for none or unknown", () => {
    const stored = {
      id: "f",
      version: 1,
      active: true,
      name: "F",
      steps: ["face_verification"],
      createdAt: "2026-09-28T00:00:00Z",
      completable: false,
    };
    expect(
      draftFromFlow({ ...stored, faceProvider: "regula" }).faceProvider,
    ).toBe("regula");
    expect(
      draftFromFlow({ ...stored, faceProvider: "iris" }).faceProvider,
    ).toBe("regula");
    expect(
      draftFromFlow({ ...stored, faceProvider: "engine" }).faceProvider,
    ).toBe("regula");
    expect(draftFromFlow(stored).faceProvider).toBe("regula");
  });
});

describe("yiviAppAvailable", () => {
  it("leaves a face step to either app when the chip is read", () => {
    const face = ["document_capture", "nfc_read", "face_verification"];
    expect(yiviAppAvailable({ steps: face })).toBe(true);
  });

  it("leaves a face match against a reference photo to the Idem app", () => {
    const noChip = ["document_capture", "face_verification"];
    expect(yiviAppAvailable({ steps: noChip })).toBe(false);
    expect(yiviAppAvailable({ steps: noChip, selfieLocation: "browser" })).toBe(
      true,
    );
  });

  it("leaves a flow that photographs the document to the Idem app", () => {
    expect(yiviAppAvailable({ steps: ["nfc_read", "document_photo"] })).toBe(
      false,
    );
  });

  it("offers an age check the Yivi credentials answer", () => {
    const ageCheck = (ageOver: number) =>
      yiviAppAvailable({
        steps: ["document_capture", "nfc_read"],
        requestedAttributes: ["dg1", "age_over"],
        ageOver,
      });
    expect(ageCheck(18)).toBe(true);
    expect(ageCheck(17)).toBe(false);
  });

  it("leaves a flow requiring more than low to the Idem app", () => {
    const face = ["document_capture", "nfc_read", "face_verification"];
    expect(
      yiviAppAvailable({ steps: face, requiredAssuranceLevel: "low" }),
    ).toBe(true);
    for (const level of ["substantial", "high"]) {
      expect(
        yiviAppAvailable({ steps: face, requiredAssuranceLevel: level }),
      ).toBe(false);
    }
  });
});

describe("attributeAvailable", () => {
  it("follows the step that produces the data", () => {
    const chipOnly = draft({ faceVerification: false });
    expect(attributeAvailable(chipOnly, "dg1")).toBe(true);
    expect(attributeAvailable(chipOnly, "chip_checks")).toBe(true);
    expect(attributeAvailable(chipOnly, "selfie")).toBe(false);
    expect(attributeAvailable(chipOnly, "document_image")).toBe(false);
    const withPhoto = draft({ documentPhoto: true });
    expect(attributeAvailable(withPhoto, "document_image")).toBe(true);
    expect(draftSteps(withPhoto)).toEqual([
      "document_capture",
      "document_photo",
      "nfc_read",
      "face_verification",
    ]);
    expect(
      draftSteps(draft({ documentAndChip: false, faceVerification: false })),
    ).toEqual([]);
  });
});

describe("draftFromFlow", () => {
  it("round-trips a stored version and keeps settings the editor does not show", () => {
    const flow: ProofingFlow = {
      id: "f1",
      version: 2,
      active: true,
      name: "Passport + face",
      steps: ["document_capture", "nfc_read", "face_verification"],
      requestedAttributes: ["dg1", "selfie"],
      requiredChecks: ["nfc.passive_auth", "face.match"],
      checkThresholds: { "face.match": 0.8 },
      requiredAssuranceLevel: "substantial",
      bsnPolicy: "omit",
      keepFacePhotos: false,
      retentionOverrideSeconds: 86_400,
      assuranceTiers: [{ level: "low", minPercent: 0 }],
      legalBasis: "consent",
      createdAt: "2026-09-24T10:00:00Z",
      completable: true,
      allowed: true,
      default: true,
    };
    const edited = draftFromFlow(flow);
    expect(edited).toMatchObject({
      documentAndChip: true,
      faceVerification: true,
      // Not stored, but substantial needs them: they are ticked, and locked.
      chipAuthentication: true,
      liveness: true,
      faceMatchThreshold: 0.8,
      assuranceLevel: "substantial",
      bsnPolicy: "omit",
      keepFacePhotos: false,
      blackOutBsn: false,
      retentionSeconds: 86_400,
    });
    expect(flowSpecFromDraft(edited)).toMatchObject({
      requestedAttributes: ["dg1", "selfie"],
      assuranceTiers: [{ level: "low", minPercent: 0 }],
      legalBasis: "consent",
    });
  });
});

describe("assurance level", () => {
  const chipOnly = draft({ faceVerification: false });

  it("ticks what the level needs and leaves the rest as it was", () => {
    const low = withAssuranceLevel(chipOnly, "low");
    expect(low).toMatchObject({
      assuranceLevel: "low",
      documentAndChip: true,
      faceVerification: false,
      chipAuthentication: false,
    });
    const substantial = withAssuranceLevel(low, "substantial");
    expect(substantial).toMatchObject({
      assuranceLevel: "substantial",
      documentAndChip: true,
      chipAuthentication: true,
      faceVerification: true,
      liveness: true,
      faceProvider: "regula",
    });
    expect(reachesAssuranceLevel(substantial)).toBe(true);
    // Back down, what Substantial ticked stays ticked, now free to change.
    const back = withAssuranceLevel(substantial, "low");
    expect(back.faceVerification).toBe(true);
    expect(lockedByLevel(back, "faceVerification")).toBe(false);
  });

  it("locks what the level needs: the chip read for Low, more for Substantial", () => {
    const locked = (level: "" | "low" | "substantial") =>
      (
        [
          "documentAndChip",
          "chipAuthentication",
          "faceVerification",
          "liveness",
        ] as const
      ).filter((setting) =>
        lockedByLevel(withAssuranceLevel(draft(), level), setting),
      );
    expect(locked("")).toEqual([]);
    expect(locked("low")).toEqual(["documentAndChip"]);
    expect(locked("substantial")).toEqual([
      "documentAndChip",
      "chipAuthentication",
      "faceVerification",
      "liveness",
    ]);
  });

  it("sends the level with what it needs", () => {
    expect(
      flowSpecFromDraft(withAssuranceLevel(chipOnly, "substantial")),
    ).toMatchObject({
      steps: ["document_capture", "nfc_read", "face_verification"],
      requiredChecks: [
        "nfc.passive_auth",
        "nfc.chip_auth",
        "face.match",
        "face.liveness",
      ],
      requiredAssuranceLevel: "substantial",
    });
  });

  it("still refuses a draft that cannot reach its level", () => {
    const short = { ...chipOnly, assuranceLevel: "substantial" as const };
    expect(reachesAssuranceLevel(short)).toBe(false);
    expect(flowDraftError(short)).toBe("assuranceLevel");
    expect(
      flowDraftError(draft({ documentAndChip: false, assuranceLevel: "low" })),
    ).toBe("assuranceLevel");
  });

  it("accepts a draft configured for more than its level requires", () => {
    const full = draft({
      chipAuthentication: true,
      liveness: true,
      faceProvider: "regula",
    });
    expect(flowDraftError({ ...full, assuranceLevel: "low" })).toBeNull();
    expect(
      flowDraftError({ ...full, assuranceLevel: "substantial" }),
    ).toBeNull();
  });

  it("needs nothing without a level", () => {
    expect(reachesAssuranceLevel(draft({ documentAndChip: false }))).toBe(true);
  });
});

describe("ageOverFact", () => {
  it("shows the age check's answer, and nothing without one", () => {
    expect(ageOverFact({ ageOver: { age: 18, over: true } }, t)).toEqual({
      label: "18 or older",
      value: "Yes",
    });
    expect(ageOverFact({ ageOver: { age: 21, over: false } }, t)?.value).toBe(
      "No",
    );
    expect(ageOverFact({}, t)).toBeUndefined();
    expect(ageOverFact(undefined, t)).toBeUndefined();
  });
});

describe("ageOverChoices", () => {
  it("offers the usual ages, plus a saved one that is not among them", () => {
    expect(ageOverChoices(draft({ ageOver: "18" }))).toEqual([
      12, 16, 18, 21, 65,
    ]);
    expect(ageOverChoices(draft({ ageOver: "17" }))).toEqual([
      12, 16, 17, 18, 21, 65,
    ]);
  });
});

describe("attribute reasons", () => {
  // A draft with every item ticked, explained in both languages: a new flow
  // starts with none ticked.
  const ticked = (patch: Partial<ProofingFlowDraft> = {}): ProofingFlowDraft =>
    draft({ requestedAttributes: EVERY_ATTRIBUTE, ...patch });
  const chipOnly = ticked({ faceVerification: false });
  const bare = (patch: Partial<ProofingFlowDraft> = {}): ProofingFlowDraft =>
    ticked({ attributeReasons: {}, ...patch });

  it("asks a new flow no reason until it collects an item, then one for each", () => {
    const fresh = { ...emptyFlowDraft(), name: "F" };
    expect(flowDraftError(fresh)).toBeNull();
    expect(
      flowDraftError({ ...fresh, requestedAttributes: EVERY_ATTRIBUTE }),
    ).toBe("reasons");
  });

  it("wants each collected item explained in every language the page offers", () => {
    expect(flowDraftError(bare(), false, ["en", "nl"])).toBe("reasons");
    expect(flowDraftError(ticked(), false, ["en", "nl"])).toBeNull();

    const dutchMissing = ticked({
      attributeReasons: {
        ...EXPLAINED,
        dg1: { en: "To check your age." },
      },
    });
    expect(flowDraftError(dutchMissing, false, ["en", "nl"])).toBe("reasons");
    expect(flowDraftError(dutchMissing, false, ["en"])).toBeNull();
  });

  it("counts a blank reason as none", () => {
    const blank = ticked({
      attributeReasons: {
        ...EXPLAINED,
        dg1: { en: "  ", nl: "Om te controleren." },
      },
    });
    expect(flowDraftError(blank, false, ["en", "nl"])).toBe("reasons");
    expect(draftReason(blank, "dg1", "en")).toBe("");
  });

  it("asks nothing for an item that is not collected", () => {
    const outcomeOnly = bare({ requestedAttributes: new Set() });
    expect(flowDraftError(outcomeOnly, false, ["en", "nl"])).toBeNull();

    // Selfie stays ticked while its step is off, and is not collected.
    const stepOff = ticked({
      faceVerification: false,
      attributeReasons: { ...EXPLAINED, selfie: {}, biometrics: {} },
    });
    expect(flowDraftError(stepOff, false, ["en", "nl"])).toBeNull();
  });

  it("lists the data a draft collects in the editor's order", () => {
    expect(collectedAttributes(chipOnly)).toEqual([
      "dg1",
      "chip_checks",
      "dg11",
      "dg2",
    ]);
    expect(
      collectedAttributes(bare({ requestedAttributes: new Set() })),
    ).toEqual([]);
  });

  it("sends the trimmed reasons of the collected items only", () => {
    const spec = flowSpecFromDraft(
      ticked({
        faceVerification: false,
        attributeReasons: {
          ...EXPLAINED,
          dg1: { en: "  To check your age. ", nl: "Om je leeftijd te zien." },
        },
      }),
    );
    expect(spec.attributeReasons).toEqual({
      dg1: { en: "To check your age.", nl: "Om je leeftijd te zien." },
      chip_checks: EXPLAINED.chip_checks,
      dg11: EXPLAINED.dg11,
      dg2: EXPLAINED.dg2,
    });
  });

  it("asks the age check for its reason too, and shows it to the person", () => {
    const ageCheck = new Set(["dg1", "age_over"]);
    const halfExplained = draft({
      requestedAttributes: ageCheck,
      attributeReasons: {
        ...EXPLAINED,
        age_over: { en: "To check your age." },
      },
    });
    expect(collectedAttributes(halfExplained)).toEqual(["dg1", "age_over"]);
    expect(flowDraftError(halfExplained, false, ["en", "nl"])).toBe("reasons");
    expect(
      missingReasonLocales(halfExplained, "age_over", ["en", "nl"]),
    ).toEqual(["nl"]);

    const explained = draft({ requestedAttributes: ageCheck });
    expect(flowDraftError(explained, false, ["en", "nl"])).toBeNull();
    expect(flowSpecFromDraft(explained).attributeReasons?.age_over).toEqual(
      EXPLAINED.age_over,
    );
    // The overview lists the age check, its "Why" line under it.
    expect(isRequestedAttribute("age_over")).toBe(true);
  });

  it("sends none for a flow that collects nothing", () => {
    const spec = flowSpecFromDraft(ticked({ requestedAttributes: new Set() }));
    expect(spec.requestedAttributes).toEqual([OUTCOME_ONLY]);
    expect(spec).not.toHaveProperty("attributeReasons");
  });

  it("starts from the reasons a flow has, and from none for one saved before", () => {
    const flow = {
      id: "f",
      version: 1,
      active: true,
      name: "F",
      steps: ["nfc_read", "document_capture"],
      requestedAttributes: ["dg1"],
      createdAt: "2026-09-28T00:00:00Z",
      completable: true,
    };
    expect(draftFromFlow(flow).attributeReasons).toEqual({});
    expect(flowDraftError(draftFromFlow(flow), false, ["en", "nl"])).toBe(
      "reasons",
    );

    const explained = { ...flow, attributeReasons: { dg1: EXPLAINED.dg1 } };
    expect(draftFromFlow(explained).attributeReasons).toEqual({
      dg1: EXPLAINED.dg1,
    });
    expect(
      flowDraftError(draftFromFlow(explained), false, ["en", "nl"]),
    ).toBeNull();
  });

  it("names the language a reason is missing in", () => {
    const englishMissing = ticked({
      attributeReasons: {
        ...EXPLAINED,
        dg1: { nl: "Om je leeftijd te zien." },
      },
    });
    expect(missingReasonLocales(englishMissing, "dg1", ["en", "nl"])).toEqual([
      "en",
    ]);
    expect(missingReasonLocales(englishMissing, "dg1", ["nl"])).toEqual([]);
    expect(missingReasonLocales(bare(), "dg1", ["en", "nl"])).toEqual([
      "en",
      "nl",
    ]);
  });

  it("keeps a hosted page from offering a language without reasons", () => {
    const flow = {
      requestedAttributes: ["dg1", "chip_checks"],
      attributeReasons: {
        dg1: { en: "To check your age.", nl: "Om je leeftijd te zien." },
        chip_checks: { en: "To make sure your document is real." },
      },
    };
    expect(localesWithoutReasons(flow, ["en"])).toEqual([]);
    expect(localesWithoutReasons(flow, ["en", "nl"])).toEqual(["nl"]);
    // No languages named is every one.
    expect(localesWithoutReasons(flow, [])).toEqual(["nl"]);
    expect(
      localesWithoutReasons(
        {
          requestedAttributes: [OUTCOME_ONLY],
          attributeReasons: flow.attributeReasons,
        },
        ["en", "nl"],
      ),
    ).toEqual([]);
  });

  // #329's open point: a version saved before reasons gives none, and its
  // page may offer any language (without a "Why" line).
  it("lets a version from before reasons offer any language", () => {
    expect(
      localesWithoutReasons({ requestedAttributes: ["dg1"] }, ["en", "nl"]),
    ).toEqual([]);
    expect(
      localesWithoutReasons(
        { requestedAttributes: ["dg1"], attributeReasons: {} },
        ["en", "nl"],
      ),
    ).toEqual([]);
  });

  it("reads the reason in the page's language and never another", () => {
    const reasons = { en: "To check your age.", nl: "Om je leeftijd te zien." };
    expect(reasonIn(reasons, "nl")).toBe(reasons.nl);
    expect(reasonIn(reasons, "nl-NL")).toBe(reasons.nl);
    expect(reasonIn(reasons, "EN")).toBe(reasons.en);
    expect(reasonIn({ en: "To check your age." }, "nl")).toBeUndefined();
    expect(reasonIn({ nl: "  " }, "nl")).toBeUndefined();
    expect(reasonIn(undefined, "en")).toBeUndefined();
  });
});

// The longest reason the editor takes is the one the backend accepts.
describe("attribute reason bound mirrors backend/internal/proofing", () => {
  it("allows reasons as long as the backend does", () => {
    const source = readFileSync(
      fileURLToPath(
        new URL(
          "../../../backend/internal/proofing/flow_reasons.go",
          import.meta.url,
        ),
      ),
      "utf8",
    );
    const found = /maxAttributeReasonLength\s*=\s*(\d+)/.exec(source);
    expect(found, "maxAttributeReasonLength is declared").not.toBeNull();
    expect(MAX_ATTRIBUTE_REASON_CHARS).toBe(Number(found![1]));
  });
});

describe("flowDraftError", () => {
  it("needs a whole minimum age with the age check", () => {
    const age = (ageOver: string) =>
      draft({ requestedAttributes: new Set(["age_over"]), ageOver });

    expect(flowDraftError(age("18"))).toBeNull();
    for (const bad of ["", "0", "17.5", "121"]) {
      expect(flowDraftError(age(bad))).toBe("ageOver");
    }

    expect(
      flowDraftError(
        draft({ requestedAttributes: new Set(["dg1"]), ageOver: "" }),
      ),
    ).toBeNull();
  });

  it("points at the field to fix", () => {
    expect(flowDraftError(draft())).toBeNull();
    expect(flowDraftError(draft({ name: " " }))).toBe("name");
    expect(
      flowDraftError(draft({ countryScope: "only", issuingCountries: [] })),
    ).toBe("issuingCountries");
    expect(
      flowDraftError(
        draft({ documentAndChip: false, faceVerification: false }),
      ),
    ).toBe("steps");
    expect(flowDraftError(draft({ faceMatchThreshold: 1.5 }))).toBe(
      "faceMatchThreshold",
    );
  });
});

describe("identity for diplomas", () => {
  it("is needed by required diplomas", () => {
    expect(identityRequired("off")).toBe(false);
    expect(identityRequired("required")).toBe(true);
  });

  it("refuses a draft that drops the name and date of birth when needed", () => {
    const noDocumentData = draft({ requestedAttributes: new Set(["dg2"]) });
    expect(flowDraftError(noDocumentData)).toBeNull();
    expect(flowDraftError(noDocumentData, true)).toBe("identity");
    expect(
      flowDraftError(draft({ requestedAttributes: new Set(["dg1"]) }), true),
    ).toBeNull();
    // Without the document scan there is no document data to read.
    expect(
      flowDraftError(
        draft({ documentAndChip: false, faceVerification: true }),
        true,
      ),
    ).toBe("identity");
  });

  it("does not count the document data next to the age check", () => {
    const ageCheck = draft({
      requestedAttributes: new Set(["dg1", "age_over"]),
    });
    expect(flowDraftError(ageCheck)).toBeNull();
    expect(flowDraftError(ageCheck, true)).toBe("identity");
  });

  it("ticks the document data and unticks the age check when needed", () => {
    const needed = withIdentity(
      draft({ requestedAttributes: new Set(["age_over", "chip_checks"]) }),
    );
    expect([...needed.requestedAttributes].sort()).toEqual([
      "chip_checks",
      "dg1",
    ]);
    expect(flowDraftError(needed, true)).toBeNull();
  });
});

describe("face match threshold", () => {
  const withThreshold = (
    level: "" | "low" | "substantial",
    faceMatchThreshold: number | null,
  ) => withAssuranceLevel(draft({ faceMatchThreshold }), level);

  it("floors only Substantial", () => {
    expect(faceMatchFloor("")).toBe(0);
    expect(faceMatchFloor("low")).toBe(0);
    expect(faceMatchFloor("substantial")).toBe(SUBSTANTIAL_FACE_MATCH_FLOOR);
  });

  it("shows the recommended minimum while the flow sets none", () => {
    expect(faceMatchValue(draft())).toBe(FACE_MATCH_RECOMMENDED);
    expect(faceMatchValue(draft({ faceMatchThreshold: 0.4 }))).toBe(0.4);
    expect(faceMatchValue(draft({ faceMatchThreshold: 0 }))).toBe(0);
  });

  it("lifts a threshold under the floor when Substantial is picked", () => {
    expect(withThreshold("substantial", 0.6).faceMatchThreshold).toBe(0.75);
    expect(withThreshold("substantial", 0).faceMatchThreshold).toBe(0.75);
  });

  it("keeps a threshold the level allows, and none", () => {
    expect(withThreshold("substantial", 0.9).faceMatchThreshold).toBe(0.9);
    expect(withThreshold("substantial", 0.75).faceMatchThreshold).toBe(0.75);
    expect(withThreshold("substantial", null).faceMatchThreshold).toBeNull();
    expect(withThreshold("low", 0.6).faceMatchThreshold).toBe(0.6);
    expect(withThreshold("", 0).faceMatchThreshold).toBe(0);
  });

  it("writes a threshold with two decimals in the reader's language", () => {
    expect(formatFaceMatch(0.75, "en")).toBe("0.75");
    expect(formatFaceMatch(0.5, "en")).toBe("0.50");
    expect(formatFaceMatch(1, "en")).toBe("1.00");
    expect(formatFaceMatch(0, "en")).toBe("0");
    expect(formatFaceMatch(0.75, "nl")).toBe("0,75");
  });

  it("keeps a lifted threshold when the level drops", () => {
    const substantial = withThreshold("substantial", 0.6);
    expect(withAssuranceLevel(substantial, "low").faceMatchThreshold).toBe(
      0.75,
    );
  });

  it("refuses a threshold under the level's floor or off the scale", () => {
    const substantial = (faceMatchThreshold: number) =>
      flowDraftError({
        ...withAssuranceLevel(draft(), "substantial"),
        faceMatchThreshold,
      });
    expect(substantial(0.74)).toBe("faceMatchThreshold");
    expect(substantial(0.75)).toBeNull();
    expect(substantial(1)).toBeNull();
    expect(substantial(1.01)).toBe("faceMatchThreshold");
    expect(flowDraftError(draft({ faceMatchThreshold: 0 }))).toBeNull();
    expect(flowDraftError(draft({ faceMatchThreshold: -0.1 }))).toBe(
      "faceMatchThreshold",
    );
    expect(flowDraftError(draft({ faceMatchThreshold: NaN }))).toBe(
      "faceMatchThreshold",
    );
  });

  it("sends the threshold only for a flow that matches a face", () => {
    const sent = (patch: Partial<ProofingFlowDraft>) =>
      flowSpecFromDraft(draft(patch)).checkThresholds;
    expect(sent({ faceMatchThreshold: 0.6 })).toEqual({ "face.match": 0.6 });
    expect(sent({ faceMatchThreshold: 0 })).toEqual({ "face.match": 0 });
    expect(sent({ faceMatchThreshold: null })).toBeUndefined();
    expect(
      sent({ faceMatchThreshold: 0.6, faceVerification: false }),
    ).toBeUndefined();
  });

  it("starts from a stored threshold, lifted to what the level allows", () => {
    const stored = (level: string, threshold: number) =>
      draftFromFlow({
        id: "f1",
        version: 1,
        active: true,
        name: "Flow",
        steps: ["document_capture", "nfc_read", "face_verification"],
        requiredChecks: ["nfc.passive_auth", "face.match"],
        requiredAssuranceLevel: level,
        checkThresholds: { "face.match": threshold },
        createdAt: "2026-09-24T10:00:00Z",
        completable: true,
      }).faceMatchThreshold;
    expect(stored("", 0.6)).toBe(0.6);
    expect(stored("substantial", 0.6)).toBe(0.75);
    expect(stored("substantial", 0.85)).toBe(0.85);
  });
});

describe("sendableFlows", () => {
  const flow = (id: string, allowed: boolean, isDefault = false) => ({
    id,
    allowed,
    default: isDefault,
  });

  it("offers only allowed flows and starts on the admin's default", () => {
    const { sendable, initial } = sendableFlows([
      flow("a", true),
      flow("b", false),
      flow("c", true, true),
    ]);
    expect(sendable.map((f) => f.id)).toEqual(["a", "c"]);
    expect(initial?.id).toBe("c");
  });

  it("falls back to the first allowed flow without a listed default", () => {
    expect(sendableFlows([flow("a", false), flow("b", true)]).initial?.id).toBe(
      "b",
    );
    expect(sendableFlows([flow("a", false)]).initial).toBeUndefined();
  });
});

describe("externalFlows", () => {
  it("offers the flows that can be finished and starts on the org's default", () => {
    const { sendable, initial } = externalFlows([
      { id: "a", completable: false, default: false },
      { id: "b", completable: true, default: false },
      { id: "c", completable: true, default: true },
    ]);
    expect(sendable.map((f) => f.id)).toEqual(["b", "c"]);
    expect(initial?.id).toBe("c");
  });

  it("starts on the first flow when the default cannot be finished", () => {
    const { initial } = externalFlows([
      { id: "a", completable: true, default: false },
      { id: "b", completable: false, default: true },
    ]);
    expect(initial?.id).toBe("a");
  });
});

describe("editedFlowSelection", () => {
  const flows = [{ id: "a" }, { id: "b" }, { id: "c" }];
  const saved = { flowIds: ["a", "c"], defaultFlowId: "c" };

  it("is clean when it matches the saved selection", () => {
    const { selection, dirty } = editedFlowSelection(
      flows,
      new Set(["c", "a"]),
      "c",
      saved,
    );
    expect(selection).toEqual({ flowIds: ["a", "c"], defaultFlowId: "c" });
    expect(dirty).toBe(false);
  });

  it("keeps a default that members may not use", () => {
    const { selection, dirty } = editedFlowSelection(
      flows,
      new Set(["a", "c"]),
      "b",
      saved,
    );
    expect(selection).toEqual({ flowIds: ["a", "c"], defaultFlowId: "b" });
    expect(dirty).toBe(true);
  });

  it("has no default once the default is no longer a listed flow", () => {
    const { selection, dirty } = editedFlowSelection(
      flows,
      new Set(["a"]),
      "gone",
      saved,
    );
    expect(selection).toEqual({ flowIds: ["a"], defaultFlowId: "" });
    expect(dirty).toBe(true);
  });

  it("has no flows when nothing is ticked", () => {
    expect(editedFlowSelection(flows, new Set(), "c", saved).selection).toEqual(
      { flowIds: [], defaultFlowId: "c" },
    );
  });
});

describe("requestSubject", () => {
  const email = "anna@example.org";

  it("names a subject by the given name, then the verified one, then the address", () => {
    expect(
      requestSubject({ subjectName: "Anna", subjectEmail: email }).name,
    ).toBe("Anna");
    expect(
      requestSubject({
        subjectName: " ",
        subjectEmail: email,
        proofedName: "Anna Jansen",
      }),
    ).toEqual({ name: "Anna Jansen", verifiedAs: undefined });
    expect(requestSubject({ subjectName: "", subjectEmail: email }).name).toBe(
      email,
    );
  });

  it("adds the verified name when it differs from the given one", () => {
    expect(
      requestSubject({
        subjectName: "Anna",
        subjectEmail: email,
        proofedName: "Anna Maria Jansen",
      }).verifiedAs,
    ).toBe("Anna Maria Jansen");
  });
});

describe("proofing status", () => {
  it("keeps polling only statuses the service may still change", () => {
    expect(isProofingLive("in_progress")).toBe(true);
    expect(isProofingLive("needs_review")).toBe(false);
    expect(isProofingLive("approved")).toBe(false);
    expect(isProofingLive("expired")).toBe(false);
  });

  it("re-reads a session in review until it is decided or gone", () => {
    expect(reviewPollContinues({ status: "needs_review" }, null)).toBe(true);
    expect(reviewPollContinues({ status: "rejected" }, null)).toBe(false);
    expect(reviewPollContinues(undefined, null)).toBe(false);
    expect(
      reviewPollContinues(
        { status: "needs_review" },
        new ApiError(404, "", "/api/v1/x", null),
      ),
    ).toBe(false);
  });

  it("labels known statuses and passes an unknown one through", () => {
    expect(proofingStatusLabel("needs_review", t)).toBe("Needs review");
    expect(proofingStatusLabel("something_new", t)).toBe("something_new");
  });
});

describe("proofingRejectionReason", () => {
  it("says a declined erasure is kept by law", () => {
    const reason = proofingRejectionReason("LEGAL_RETENTION", t);
    expect(reason).toBe(t("identityProofing.rejectionReasons.legalRetention"));
    expect(reason).not.toBe("LEGAL_RETENTION");
  });

  it("shows a code it does not know with its code", () => {
    expect(proofingRejectionReason("SOMETHING_NEW", t)).toBe(
      "the check did not pass (SOMETHING_NEW)",
    );
  });
});

describe("proofingErrorMessage", () => {
  it("shows the proofing service's own reason for a refused flow", () => {
    const err = apiError(
      422,
      "rejected_by_provider",
      "flow: nfc_read requires nfc.passive_auth",
    );
    expect(proofingErrorMessage(err, t)).toBe(
      "flow: nfc_read requires nfc.passive_auth",
    );
  });

  it("says the Idem app still holds the session", () => {
    expect(
      proofingErrorMessage(apiError(409, "device_active", "internal"), t),
    ).toBe(t("identityProofing.errors.deviceActive"));
  });

  it("maps known codes to their own copy", () => {
    expect(
      proofingErrorMessage(apiError(422, "flow_not_allowed", "internal"), t),
    ).toBe(t("identityProofing.errors.flowNotAllowed"));
  });

  it.each([
    [422, "no_default_flow", "noDefaultFlow"],
    [404, "api_key_not_found", "apiKeyNotFound"],
    [404, "session_not_found", "sessionNotFound"],
    [409, "not_under_review", "notUnderReview"],
    [422, "reference_photo_required", "referencePhotoRequired"],
    [404, "organization_not_found", "organizationNotFound"],
    [404, "webhook_not_found", "webhookNotFound"],
    [409, "link_started", "linkStarted"],
    [422, "address_not_found", "addressNotFound"],
    [409, "legal_retention", "legalRetention"],
    [400, "reasons_missing", "reasonsMissing"],
  ] as const)("maps %i %s to its own copy", (status, code, key) => {
    const copy = t(`identityProofing.errors.${key}`);
    expect(copy).not.toBe(`identityProofing.errors.${key}`);
    expect(proofingErrorMessage(apiError(status, code, "internal"), t)).toBe(
      copy,
    );
  });

  it("falls back to generic copy for anything else", () => {
    expect(proofingErrorMessage(new Error("boom"), t)).toBe(
      t("identityProofing.errors.generic"),
    );
  });
});

describe("addressLookup", () => {
  it.each([
    ["1015 CP", "12", { postalCode: "1015CP", houseNumber: "12" }],
    [" 1015cp ", "12a", { postalCode: "1015CP", houseNumber: "12" }],
    ["1015 C", "12", null],
    ["0123 AB", "12", null],
    ["1000", "16", null],
    ["1015 CP", "", null],
    ["1015 CP", "A", null],
  ] as const)("looks %s %s up as %o", (postalCode, houseNumber, want) => {
    expect(addressLookup(postalCode, houseNumber)).toEqual(want);
  });
});

describe("matchingAddress", () => {
  const at = (houseNumber: string) => ({
    street: "Keizersgracht",
    houseNumber,
    postalCode: "1015 CP",
    city: "Amsterdam",
  });
  const found = [at("12"), at("12A"), at("12-2")];

  it.each([
    ["12", "12"],
    ["12 a", "12A"],
    ["12a", "12A"],
    ["12 2", "12-2"],
  ])("matches %s to %s", (typed, want) => {
    expect(matchingAddress(found, typed)?.houseNumber).toBe(want);
  });

  it("matches no address the number lacks", () => {
    expect(matchingAddress(found, "12B")).toBeUndefined();
  });
});

describe("addressVerificationNote", () => {
  it.each([
    ["verified", "verified"],
    ["unchecked", "unchecked"],
    ["unsupported", "unsupported"],
    [undefined, "declared"],
  ] as const)("says how a %s address was checked", (verification, key) => {
    const copy = t(`identityProofing.sessions.address.${key}`);
    expect(copy).not.toBe(`identityProofing.sessions.address.${key}`);
    expect(addressVerificationNote(verification, t)).toBe(copy);
  });
});

describe("proofing stats", () => {
  const row = (
    flowId: string,
    sessions: number,
    approved: number,
  ): {
    flowId: string;
    sessions: number;
    approved: number;
    rejected: number;
    needsReview: number;
    expired: number;
    cancelled: number;
  } => ({
    flowId,
    sessions,
    approved,
    rejected: 1,
    needsReview: 0,
    expired: 1,
    cancelled: 1,
  });
  const rows = [row("f1", 10, 6), row("f2", 4, 2), row("f1", 5, 3)];

  it("sums every row", () => {
    expect(sumProofingStats(rows)).toEqual({
      sessions: 19,
      approved: 11,
      rejected: 3,
      needsReview: 0,
      expired: 3,
      cancelled: 3,
    });
    expect(sumProofingStats([]).sessions).toBe(0);
  });

  it("groups per flow", () => {
    const perFlow = proofingStatsBy(rows, (r) => r.flowId);
    expect([...perFlow.keys()]).toEqual(["f1", "f2"]);
    expect(perFlow.get("f1")?.sessions).toBe(15);
    expect(perFlow.get("f2")?.approved).toBe(2);
    expect(perFlow.has("f3")).toBe(false);
  });

  it("has no verified share without sessions", () => {
    expect(verifiedShare(sumProofingStats([]))).toBeUndefined();
    expect(verifiedShare(sumProofingStats([row("f", 4, 3)]))).toBe(0.75);
  });
});

describe("requestFlows", () => {
  it("lists each flow once, by name", () => {
    expect(
      requestFlows([
        { flowId: "b", flowName: "Ticket check" },
        { flowId: "a", flowName: "Age check" },
        { flowId: "b", flowName: "Ticket check" },
      ]),
    ).toEqual([
      { id: "a", name: "Age check" },
      { id: "b", name: "Ticket check" },
    ]);
    expect(requestFlows([])).toEqual([]);
  });

  it("names a renamed flow by its newest request", () => {
    expect(
      requestFlows([
        { flowId: "a", flowName: "Age check 2026" },
        { flowId: "a", flowName: "Age check" },
      ]),
    ).toEqual([{ id: "a", name: "Age check 2026" }]);
  });
});

describe("listedCount", () => {
  const full = Array.from({ length: MAX_LISTED_REQUESTS }, () => ({}));

  it("reads a list as long as one read returns as possibly cut off", () => {
    expect(requestListCapped(full)).toBe(true);
    expect(requestListCapped(full.slice(1))).toBe(false);
    expect(requestListCapped([])).toBe(false);
  });

  it("shows a count from a cut-off list as at least that many", () => {
    expect(listedCount(MAX_LISTED_REQUESTS, true, t)).toBe("200+");
    expect(listedCount(12, true, t)).toBe("12+");
    expect(listedCount(12, false, t)).toBe("12");
    expect(listedCount(0, false, t)).toBe("0");
  });

  it("shows it so in Dutch too", () => {
    const nl = i18n.getFixedT("nl");
    expect(listedCount(MAX_LISTED_REQUESTS, true, nl)).toBe("200+");
    expect(listedCount(12, false, nl)).toBe("12");
  });
});

describe("peopleRetentionOf", () => {
  const flows = [{ id: "a" }, { id: "b" }];

  it("states the org's retention across every flow", () => {
    expect(peopleRetentionOf("", flows)).toEqual({ kind: "org" });
    expect(peopleRetentionOf("", undefined)).toEqual({ kind: "org" });
  });

  it("states the picked flow's retention", () => {
    expect(peopleRetentionOf("b", flows)).toEqual({
      kind: "flow",
      flow: { id: "b" },
    });
  });

  it("states none for a picked flow the caller cannot read", () => {
    expect(peopleRetentionOf("c", flows)).toEqual({ kind: "unknown" });
    expect(peopleRetentionOf("a", undefined)).toEqual({ kind: "unknown" });
  });
});

describe("flowPurgeDays", () => {
  const org = { dataRetentionDays: 30 };

  it("purges after the org's retention unless the flow sets its own", () => {
    expect(flowPurgeDays({}, org)).toBe(30);
    expect(flowPurgeDays({ retentionOverrideSeconds: 0 }, org)).toBe(30);
    expect(flowPurgeDays({ retentionOverrideSeconds: 7 * 86_400 }, org)).toBe(
      7,
    );
  });

  it("rounds a part of a day up", () => {
    expect(flowPurgeDays({ retentionOverrideSeconds: 86_401 }, org)).toBe(2);
  });
});

describe("flowStepLabels", () => {
  it("names each step in the order given", () => {
    expect(
      flowStepLabels(
        { steps: ["document_capture", "nfc_read", "face_verification"] },
        t,
      ),
    ).toEqual(["Scan the document", "Read the document's chip", "Face check"]);
  });

  it("adds the wallet's diploma upload after the app's steps", () => {
    expect(
      flowStepLabels(
        { steps: ["face_verification"], diplomaMode: "required" },
        t,
      ),
    ).toEqual(["Face check", "Upload diplomas (DUO)"]);
    expect(
      flowStepLabels({ steps: ["face_verification"], diplomaMode: "off" }, t),
    ).toEqual(["Face check"]);
  });

  it("adds the address entry before the diploma upload", () => {
    expect(
      flowStepLabels(
        {
          steps: ["face_verification"],
          addressMode: "required",
          diplomaMode: "required",
        },
        t,
      ),
    ).toEqual(["Face check", "Enter the address", "Upload diplomas (DUO)"]);
  });

  it("shows a step it has no copy for as it is", () => {
    expect(flowStepLabels({ steps: ["video_call"] }, t)).toEqual([
      "video_call",
    ]);
  });
});

describe("known flow values", () => {
  it("recognises only values the wallet has copy for", () => {
    expect(isRequestedAttribute("dg1")).toBe(true);
    expect(isRequestedAttribute("dg14")).toBe(false);
    expect(isProofingStep("nfc_read")).toBe(true);
    expect(isProofingStep("video_call")).toBe(false);
  });
});

describe("people lists", () => {
  const sent = "2026-09-25T10:00:00Z";
  const expires = "2026-09-25T10:10:00Z";

  it("counts each filter", () => {
    const statuses = [
      "approved",
      "approved",
      "rejected",
      "expired",
      "pending",
      "in_progress",
      "cancelled",
      "needs_review",
    ];
    expect(sessionFilterCounts(statuses.map((status) => ({ status })))).toEqual(
      {
        all: 8,
        in_progress: 2,
        review: 1,
        verified: 2,
        failed: 1,
        expired: 1,
      },
    );
  });

  it("shows a cancelled session under all only", () => {
    expect(matchesSessionFilter("cancelled", "all")).toBe(true);
    expect(matchesSessionFilter("cancelled", "in_progress")).toBe(false);
    expect(matchesSessionFilter("pending", "in_progress")).toBe(true);
    expect(matchesSessionFilter("approved", "in_progress")).toBe(false);
  });

  it("times a session to its outcome or its expiry", () => {
    const base = { createdAt: sent, linkExpiresAt: expires };
    expect(
      sessionDurationSeconds({
        ...base,
        status: "approved",
        completedAt: "2026-09-25T10:00:48Z",
      }),
    ).toBe(48);
    expect(sessionDurationSeconds({ ...base, status: "expired" })).toBe(600);
    expect(
      sessionDurationSeconds({ ...base, status: "in_progress" }),
    ).toBeUndefined();
  });

  it("formats like a stopwatch", () => {
    expect(formatDuration(48)).toBe("0:48");
    expect(formatDuration(151)).toBe("2:31");
    expect(formatDuration(600)).toBe("10:00");
  });

  it("shortens a request id", () => {
    expect(shortRequestId("8f2k1a2b-0000-4000-8000-000000000000")).toBe(
      "8f2k1a2b",
    );
  });
});

// The session settings the Settings tab offers are the ones the backend accepts.
describe("session settings mirror backend/internal/proofing", () => {
  const source = readFileSync(
    fileURLToPath(
      new URL(
        "../../../backend/internal/proofing/proofing.go",
        import.meta.url,
      ),
    ),
    "utf8",
  );
  const minutes = (expr: string): number => {
    const named = /const SessionTTL = (\d+) \* time\.Minute/.exec(source);
    const literal = /^(\d+) \* time\.Minute$/.exec(expr.trim());
    if (expr.trim() === "SessionTTL" && named) return Number(named[1]);
    if (literal) return Number(literal[1]);
    throw new Error(`unparsed session lifetime ${expr}`);
  };

  it("offers every session lifetime and no other", () => {
    const list = /SessionTTLOptions = \[\]time\.Duration\{([^}]*)\}/.exec(
      source,
    );
    expect(list).not.toBeNull();
    const backend = list![1].split(",").map((e) => minutes(e) * 60);
    expect([...SESSION_TTL_OPTIONS_SECONDS]).toEqual(backend);
  });

  it("caps a request list where the backend does", () => {
    const cap = /const maxListedRequests = (\d+)/.exec(source);
    expect(cap).not.toBeNull();
    expect(MAX_LISTED_REQUESTS).toBe(Number(cap![1]));
  });
});

// The face match slider's floor and recommended mark are the engine's.
describe("face match threshold mirrors the backend", () => {
  const read = (path: string): string =>
    readFileSync(fileURLToPath(new URL(path, import.meta.url)), "utf8");
  const constant = (source: string, name: string): number => {
    const found = new RegExp(`const ${name} = ([\\d.]+)`).exec(source);
    expect(found, `${name} is declared`).not.toBeNull();
    return Number(found![1]);
  };

  it("floors Substantial where the engine does", () => {
    const flowGo = read(
      "../../../backend/internal/proofingengine/flow/flow.go",
    );
    expect(SUBSTANTIAL_FACE_MATCH_FLOOR).toBe(
      constant(flowGo, "SubstantialMinFaceMatch"),
    );
  });

  it("recommends Regula's default", () => {
    const regulaGo = read(
      "../../../backend/internal/proofingengine/regula/regula.go",
    );
    expect(FACE_MATCH_RECOMMENDED).toBe(
      constant(regulaGo, "DefaultMatchThreshold"),
    );
  });
});

describe("isSuccessStatus", () => {
  it("tells a 2xx from the rest", () => {
    expect(isSuccessStatus(204)).toBe(true);
    expect(isSuccessStatus(503)).toBe(false);
    expect(isSuccessStatus(301)).toBe(false);
  });
});

describe("ownEndpointDeliveries", () => {
  const delivery = (id: string, endpointUrl?: string): WebhookDelivery => ({
    id,
    event: "session.verified",
    status: "delivered",
    attempts: 1,
    createdAt: "2026-10-16T10:00:00Z",
    endpointUrl,
  });

  it("drops the deliveries to the wallet's default endpoint", () => {
    const own = delivery("own", "https://hooks.example.org/yivi");
    expect(ownEndpointDeliveries([delivery("default"), own])).toEqual([own]);
  });

  it("is empty for an org that never had an endpoint", () => {
    expect(ownEndpointDeliveries([delivery("a"), delivery("b")])).toEqual([]);
  });
});

describe("readableTextOn", () => {
  it("puts white on dark fills and black on light ones", () => {
    expect(readableTextOn("#1F5B4A")).toBe("#ffffff");
    expect(readableTextOn("#1A1A1A")).toBe("#ffffff");
    expect(readableTextOn("#F5D547")).toBe("#000000");
    expect(readableTextOn("not a colour")).toBe("#ffffff");
    expect(isHexColor("#abcdef")).toBe(true);
    expect(isHexColor("abcdef")).toBe(false);
  });
});

describe("session method and timeline", () => {
  it("names the app a subject used", () => {
    expect(proofingMethodLabel("idem_app", t)).toBe(
      t("identityProofing.methods.idemApp"),
    );
    expect(proofingMethodLabel("yivi_app", t)).toBe(
      t("identityProofing.methods.yiviApp"),
    );
    expect(proofingMethodLabel(undefined, t)).toBe("—");
    expect(proofingMethodLabel("carrier_pigeon", t)).toBe("carrier_pigeon");
  });

  it("details an event from its after snapshot", () => {
    expect(
      sessionEventDetail(
        {
          before: { status: "in_progress" },
          after: {
            status: "rejected",
            method: "idem_app",
            errorCode: "DOC_EXPIRED",
          },
        },
        t,
      ),
    ).toEqual([
      t("identityProofing.methods.idemApp"),
      t("identityProofing.rejectionReasons.docExpired"),
    ]);
    expect(
      sessionEventDetail(
        { after: { status: "approved", eidasLevel: "substantial" } },
        t,
      ),
    ).toEqual(["eIDAS substantial"]);
    expect(sessionEventDetail({}, t)).toEqual([]);
  });
});

describe("secondsUntil", () => {
  const now = Date.parse("2026-09-28T10:00:00Z");

  it("rounds a part second up, so 0 means the session is over", () => {
    expect(secondsUntil("2026-09-28T10:10:00Z", now)).toBe(600);
    expect(secondsUntil("2026-09-28T10:00:00.200Z", now)).toBe(1);
  });

  it("never goes below zero, also for an unreadable time", () => {
    expect(secondsUntil("2026-09-28T09:59:00Z", now)).toBe(0);
    expect(secondsUntil("not a time", now)).toBe(0);
  });
});

// Every code a session can be rejected with has copy, and copy is only kept
// for codes the backend still sends: the engine's, the wallet's own, and the
// ones a reviewer picks when rejecting a check (review.go), which the engine
// stores as given.
describe("rejection codes mirror the backend", () => {
  const read = (path: string): string =>
    readFileSync(
      fileURLToPath(
        new URL(`../../../backend/internal/${path}`, import.meta.url),
      ),
      "utf8",
    );
  const codes = (source: string, pattern: RegExp): string[] =>
    [...source.matchAll(pattern)].map((m) => m[1]);
  const engine = ["sessions.go", "chip_verify.go", "yivi.go", "rp.go"].flatMap(
    (file) =>
      codes(
        read(`proofingengine/${file}`),
        /errCode\w+\s*=\s*"([A-Z][A-Z_]+)"/g,
      ),
  );
  const walletCodes = /\b[eE]rror[A-Z]\w*\s*=\s*"([A-Z][A-Z_]+)"/g;
  const wallet = [
    "proofing.go",
    "service.go",
    "data_request.go",
    "review.go",
  ].flatMap((file) => codes(read(`proofing/${file}`), walletCodes));
  const reviewer = codes(read("proofing/review.go"), walletCodes);
  const backend = [...new Set([...engine, ...wallet])];

  it("has copy for every backend code and no other", () => {
    expect(engine.length).toBeGreaterThan(0);
    expect(wallet.length).toBeGreaterThan(0);
    expect([...PROOFING_REJECTION_CODES].sort()).toEqual(backend.sort());
  });

  it("offers a reviewer only the reviewer's own codes", () => {
    expect(reviewer.length).toBeGreaterThan(0);
    for (const { errorCode } of REJECT_REASONS) {
      if (errorCode !== undefined) expect(reviewer).toContain(errorCode);
    }
  });
});

// The diploma modes the flows page offers are the ones the backend accepts,
// both ways.
describe("diploma modes mirror backend/internal/proofing", () => {
  const source = readFileSync(
    fileURLToPath(
      new URL("../../../backend/internal/proofing/diploma.go", import.meta.url),
    ),
    "utf8",
  );
  const backend = [
    ...source.matchAll(/Diplomas\w+\s+DiplomaMode = "(\w+)"/g),
  ].map((m) => m[1]);

  it("has every backend mode and no other", () => {
    expect(backend.length).toBeGreaterThan(0);
    for (const mode of backend) {
      expect(DIPLOMA_MODES).toContain(mode);
    }
    for (const mode of DIPLOMA_MODES) {
      expect(backend).toContain(mode);
    }
  });
});

// The flow kinds, and of them the data requests the send form offers, are the
// backend's, both ways.
describe("flow kinds mirror backend/internal/proofing", () => {
  const source = readFileSync(
    fileURLToPath(
      new URL(
        "../../../backend/internal/proofing/data_request.go",
        import.meta.url,
      ),
    ),
    "utf8",
  );
  const backend = [...source.matchAll(/Flow\w+\s+FlowKind = "(\w+)"/g)].map(
    (m) => m[1],
  );

  it("has every backend kind and no other", () => {
    expect(backend.length).toBeGreaterThan(0);
    expect([...FLOW_KINDS].sort()).toEqual([...backend].sort());
    expect([...DATA_REQUEST_KINDS].sort()).toEqual(
      backend.filter((kind) => kind !== "identity").sort(),
    );
  });
});

describe("erasure handlings mirror backend/internal/proofing", () => {
  const source = readFileSync(
    fileURLToPath(
      new URL(
        "../../../backend/internal/proofing/proofing.go",
        import.meta.url,
      ),
    ),
    "utf8",
  );
  const backend = [
    ...source.matchAll(/Erasure\w+\s+ErasureHandling = "(\w+)"/g),
  ].map((m) => m[1]);

  it("has every backend handling and no other", () => {
    expect(backend.length).toBeGreaterThan(0);
    expect([...ERASURE_HANDLINGS].sort()).toEqual([...backend].sort());
  });
});

describe("sessionView", () => {
  const until = "2026-10-12T12:00:00Z";
  const running = {
    status: "pending",
    addressMode: "off" as const,
    addressUntil: undefined,
    addressDone: false,
    diplomaMode: "off" as const,
    diplomasUntil: undefined,
    diplomasDone: false,
    secondsLeft: 60,
  };

  it("runs the session while it is live and its link lasts", () => {
    expect(sessionView(running)).toBe("running");
    expect(sessionView({ ...running, status: "in_progress" })).toBe("running");
  });

  it("expires an unstarted session once its link ran out", () => {
    expect(sessionView({ ...running, secondsLeft: 0 })).toBe("expired");
  });

  it("keeps a started session running after its link ran out", () => {
    expect(
      sessionView({ ...running, status: "in_progress", secondsLeft: 0 }),
    ).toBe("running");
  });

  it("shows the outcome once settled", () => {
    for (const status of ["approved", "rejected", "needs_review", "expired"]) {
      expect(sessionView({ ...running, status, secondsLeft: 0 })).toBe(
        "outcome",
      );
    }
  });

  it("asks for diplomas after a settled session that has the step", () => {
    expect(
      sessionView({
        ...running,
        status: "approved",
        diplomaMode: "required",
        diplomasUntil: until,
      }),
    ).toBe("diplomas");
  });

  it("shows the outcome once the diplomas are done", () => {
    expect(
      sessionView({
        ...running,
        status: "approved",
        diplomaMode: "required",
        diplomasUntil: until,
        diplomasDone: true,
      }),
    ).toBe("outcome");
  });

  it("skips the diploma step without a deadline or on a flow without it", () => {
    expect(
      sessionView({ ...running, status: "rejected", diplomaMode: "required" }),
    ).toBe("outcome");
    expect(
      sessionView({ ...running, status: "approved", diplomasUntil: until }),
    ).toBe("outcome");
  });

  it("does not ask for diplomas while the session is live", () => {
    expect(
      sessionView({
        ...running,
        diplomaMode: "required",
        diplomasUntil: until,
      }),
    ).toBe("running");
  });

  it("asks for the address before the diplomas", () => {
    const settled = {
      ...running,
      status: "approved",
      addressMode: "required" as const,
      addressUntil: until,
      diplomaMode: "required" as const,
      diplomasUntil: until,
    };
    expect(sessionView(settled)).toBe("address");
    expect(sessionView({ ...settled, addressDone: true })).toBe("diplomas");
    expect(
      sessionView({ ...settled, addressUntil: undefined, diplomasDone: true }),
    ).toBe("outcome");
  });
});

describe("app step", () => {
  it("offers both apps when the Yivi app can run the flow", () => {
    expect(methodChoices(true)).toEqual(["yivi_app", "idem_app"]);
  });

  // Continue never starts a session (#304): a flow with one app still shows
  // the app step, with that one app to tap.
  it("offers the Idem app alone otherwise, still as a step of its own", () => {
    expect(methodChoices(false)).toEqual(["idem_app"]);
    expect(verifyStages({ diplomas: false })).toContain("method");
  });

  it("offers the Yivi app for an age check only at an age it answers", () => {
    const ageCheck = (ageOver: number) =>
      methodChoices(
        yiviAppAvailable({
          steps: ["document_capture", "nfc_read"],
          requestedAttributes: ["dg1", "age_over"],
          ageOver,
        }),
      );
    expect(ageCheck(21)).toEqual(["yivi_app", "idem_app"]);
    expect(ageCheck(20)).toEqual(["idem_app"]);
  });
});

describe("address step", () => {
  it("adds the address stage after the session, before the diplomas", () => {
    expect(verifyStages({ address: true, diplomas: true })).toEqual([
      "overview",
      "method",
      "session",
      "address",
      "diplomas",
    ]);
  });

  it("is at the address or diploma step while it is open", () => {
    const at = {
      step: "session",
      inAddress: false,
      inDiplomas: false,
    } as const;
    expect(currentStage(at)).toBe("session");
    expect(currentStage({ ...at, inAddress: true, inDiplomas: true })).toBe(
      "address",
    );
    expect(currentStage({ ...at, inDiplomas: true })).toBe("diplomas");
  });

  it("mails only a flow that asks for no address", () => {
    expect(sendableByMail({ addressMode: "off" })).toBe(true);
    expect(sendableByMail({ addressMode: "required" })).toBe(false);
    expect(
      sendableByMail({ addressMode: "required", diplomaMode: "off" }),
    ).toBe(false);
  });
});

describe("diploma step", () => {
  it("adds the diploma stage after the session when the flow has it", () => {
    expect(verifyStages({ diplomas: true })).toEqual([
      "overview",
      "method",
      "session",
      "diplomas",
    ]);
    expect(verifyStages({ diplomas: false })).toEqual([
      "overview",
      "method",
      "session",
    ]);
  });

  it("ends the step once an extract is held or the time is up", () => {
    expect(diplomaStepDone(0, 60)).toBe(false);
    expect(diplomaStepDone(1, 60)).toBe(true);
    expect(diplomaStepDone(0, 0)).toBe(true);
  });

  it("mails only a flow that asks for no diplomas", () => {
    expect(sendableByMail(undefined)).toBe(true);
    expect(sendableByMail({})).toBe(true);
    expect(sendableByMail({ diplomaMode: "off" })).toBe(true);
    expect(sendableByMail({ diplomaMode: "required" })).toBe(false);
  });

  // Mirrors proofing.readsIdentity: a request for one person is matched
  // against the document data.
  it("knows which flows read the name and date of birth", () => {
    expect(readsIdentity(undefined)).toBe(false);
    expect(readsIdentity({ requestedAttributes: [OUTCOME_ONLY] })).toBe(false);
    expect(
      readsIdentity({
        requestedAttributes: ["dg1"],
      }),
    ).toBe(true);
    expect(
      readsIdentity({
        requestedAttributes: ["dg2"],
      }),
    ).toBe(false);
    expect(
      readsIdentity({
        requestedAttributes: ["dg1", "age_over"],
      }),
    ).toBe(false);
  });

  it("explains every refusal the backend gives, and shows another as is", () => {
    const t = i18n.getFixedT("en");
    const sources = [
      "../../../backend/internal/diploma/checker.go",
      "../../../backend/internal/proofing/diploma.go",
    ].map((path) =>
      readFileSync(fileURLToPath(new URL(path, import.meta.url)), "utf8"),
    );
    const reasons = sources.flatMap((source) =>
      [...source.matchAll(/Reason\w*\s*= "(\w+)"/g)].map((m) => m[1]),
    );
    expect(reasons.length).toBe(4);
    for (const reason of reasons) {
      expect(diplomaRejectionReason(reason, t)).not.toBe(reason);
    }
    expect(diplomaRejectionReason("something_new", t)).toBe("something_new");
  });
});

describe("sendRequestFields", () => {
  const filled = {
    delivery: "email",
    matchable: true,
    nameOn: false,
    passOn: false,
    email: " anna@example.org ",
    name: " Anna ",
    birthDate: "1990-04-02",
  } as const;

  it("sends only the address while both switches are off", () => {
    const fields = sendRequestFields(filled);

    expect(fields.email).toBe("anna@example.org");
    expect(fields.name).toBe("");
    expect(fields.birthDate).toBeUndefined();
    expect(fields.missing).toEqual({
      email: false,
      name: false,
      birthDate: false,
    });
  });

  it("asks for an address only when the person gets a mail", () => {
    expect(sendRequestFields({ ...filled, email: "" }).missing.email).toBe(
      true,
    );
    expect(sendRequestFields({ ...filled, email: "anna" }).missing.email).toBe(
      true,
    );

    for (const delivery of ["on_screen", "hosted"] as const) {
      const fields = sendRequestFields({ ...filled, delivery, email: "" });

      expect(fields.missing.email).toBe(false);
      expect(sendRequestFields({ ...filled, delivery }).email).toBe("");
    }
  });

  it("sends the name once its switch is on, and asks for it", () => {
    const on = { ...filled, nameOn: true };

    expect(sendRequestFields(on).name).toBe("Anna");
    expect(sendRequestFields(on).nameShown).toBe(true);
    expect(sendRequestFields({ ...on, name: " " }).missing.name).toBe(true);
  });

  it("passes one person on name and date of birth", () => {
    const fields = sendRequestFields({ ...filled, passOn: true });

    expect(fields.passing).toBe(true);
    expect(fields.nameShown).toBe(true);
    expect(fields.name).toBe("Anna");
    expect(fields.birthDate).toBe("1990-04-02");
    expect(
      sendRequestFields({ ...filled, passOn: true, name: "" }).missing.name,
    ).toBe(true);
    expect(
      sendRequestFields({ ...filled, passOn: true, birthDate: "" }).missing
        .birthDate,
    ).toBe(true);
  });

  it("ignores the pass switch on a flow that does not read identity", () => {
    const fields = sendRequestFields({
      ...filled,
      passOn: true,
      matchable: false,
      birthDate: "",
    });

    expect(fields.passing).toBe(false);
    expect(fields.birthDate).toBeUndefined();
    expect(fields.name).toBe("");
    expect(fields.missing).toEqual({
      email: false,
      name: false,
      birthDate: false,
    });
  });
});

describe("mailLifetimeMinutes", () => {
  it("takes the flow's own lifetime over the organisation's", () => {
    expect(mailLifetimeMinutes({ sessionTtlSeconds: 120 }, 600)).toBe(2);
  });

  it("follows the organisation when the flow sets none", () => {
    expect(mailLifetimeMinutes({}, 300)).toBe(5);
    expect(mailLifetimeMinutes({ sessionTtlSeconds: 0 }, 300)).toBe(5);
    expect(mailLifetimeMinutes(undefined, 300)).toBe(5);
  });

  it("is unknown until the organisation's settings are read", () => {
    expect(mailLifetimeMinutes({}, undefined)).toBeUndefined();
    expect(mailLifetimeMinutes({ sessionTtlSeconds: 120 }, undefined)).toBe(2);
  });
});

describe("supportContactLink", () => {
  it.each([
    "help@example.nl",
    "  help@example.nl ",
    "mailto:help@example.nl",
    "MAILTO:help@example.nl",
  ])("opens %j in the mail app, never as a mailto: prefix", (contact) => {
    expect(supportContactLink(contact)).toEqual({
      href: "mailto:help@example.nl",
      kind: "mail",
    });
  });

  it("does not link a mailto: prefix without an address", () => {
    expect(supportContactLink("mailto:help")).toBeUndefined();
  });

  it.each([
    ["https://example.nl/help", "https://example.nl/help"],
    ["http://example.nl", "http://example.nl/"],
    ["https://mastodon.social/@yivi", "https://mastodon.social/@yivi"],
    ["https://medium.com/@yivi/help", "https://medium.com/@yivi/help"],
    ["https://user@example.nl", "https://user@example.nl/"],
  ])("opens %s as a web link, also with an @", (contact, href) => {
    expect(supportContactLink(contact)).toEqual({ href, kind: "web" });
  });

  it("parses without URL.canParse, which Safari 16 lacks", () => {
    const canParse = vi.spyOn(URL, "canParse");
    try {
      supportContactLink("https://example.nl");
      supportContactLink("030 123 4567");
      supportContactLink("");

      expect(canParse).not.toHaveBeenCalled();
    } finally {
      canParse.mockRestore();
    }
  });

  it("leaves a phone number or opening hours as text", () => {
    expect(supportContactLink("030 123 4567")).toBeUndefined();
    expect(supportContactLink("Mon to Fri, 9:00 to 17:00")).toBeUndefined();
    expect(supportContactLink("")).toBeUndefined();
  });

  it("never links a script or a file", () => {
    expect(supportContactLink("javascript:alert(1)")).toBeUndefined();
    expect(supportContactLink("data:text/html,<b>x</b>")).toBeUndefined();
    expect(supportContactLink("file:///etc/passwd")).toBeUndefined();
  });
});

describe("data request review", () => {
  it("starts an erasure with the matches read off a verified chip ticked", () => {
    expect(matchTickedByDefault("data_erasure", "strong")).toBe(true);
    expect(matchTickedByDefault("data_erasure", "probable")).toBe(true);
    expect(matchTickedByDefault("data_access", "strong")).toBe(true);
    expect(matchTickedByDefault("data_access", "probable")).toBe(false);
    for (const level of ["email", "name"]) {
      expect(matchTickedByDefault("data_erasure", level)).toBe(false);
      expect(matchTickedByDefault("data_access", level)).toBe(false);
    }
  });

  it("confirms an erasure before approving it", () => {
    expect(
      reviewSubmitStep({
        decision: "approve",
        kind: "data_erasure",
        confirming: false,
      }),
    ).toBe("confirm");
    // Submitting from the confirmation decides.
    expect(
      reviewSubmitStep({
        decision: "approve",
        kind: "data_erasure",
        confirming: true,
      }),
    ).toBe("decide");
  });

  it("decides a rejection or an access request at once", () => {
    expect(
      reviewSubmitStep({
        decision: "reject",
        kind: "data_erasure",
        confirming: false,
      }),
    ).toBe("decide");
    expect(
      reviewSubmitStep({
        decision: "approve",
        kind: "data_access",
        confirming: false,
      }),
    ).toBe("decide");
  });

  it("names the checks an erasure deletes", () => {
    const key = "identityProofing.reviews.dataRequest.decision.confirmTitle";
    expect(t(key, { count: 1 })).toBe("Delete 1 check?");
    expect(t(key, { count: 3 })).toBe("Delete 3 checks?");
  });
});

describe("progress poll", () => {
  const notAllowed = new ApiError(403, "", "/api/v1/x", null);
  const gone = new ApiError(404, "", "/api/v1/x", null);

  it("polls until the first read and while live", () => {
    expect(progressPollContinues(undefined, null)).toBe(true);
    expect(progressPollContinues({ status: "pending" }, null)).toBe(true);
    expect(progressPollContinues({ status: "approved" }, null)).toBe(false);
  });

  it("stops after a 403 or a 404", () => {
    expect(progressPollContinues(undefined, notAllowed)).toBe(false);
    expect(progressPollContinues({ status: "pending" }, gone)).toBe(false);
  });

  it("keeps polling after a passing failure", () => {
    const unavailable = new ApiError(503, "", "/api/v1/x", null);
    expect(progressPollContinues({ status: "pending" }, unavailable)).toBe(
      true,
    );
    expect(progressPollContinues(undefined, new Error("offline"))).toBe(true);
  });
});

describe("rejection reasons", () => {
  it("names a code the copy does not know rather than showing it bare", () => {
    expect(proofingRejectionReason("SOMETHING_NEW", t)).toBe(
      "the check did not pass (SOMETHING_NEW)",
    );
    expect(proofingRejectionReason("MANUAL_REVIEW_REJECTED", t)).toBe(
      "refused by a reviewer",
    );
  });

  it("tells a refused data request from a failed identity check", () => {
    expect(dataRequestRefusal("MANUAL_REVIEW_REJECTED")).toBe("reviewer");
    for (const code of [
      "REVIEW_NOT_HOLDER",
      "REVIEW_ALTERED",
      "EVIDENCE_NOT_AS_REQUESTED",
    ]) {
      expect(dataRequestRefusal(code)).toBe("reviewer");
    }
    expect(dataRequestRefusal("LEGAL_RETENTION")).toBe("law");
    expect(dataRequestRefusal("FACE_NO_MATCH")).toBeUndefined();
    expect(dataRequestRefusal(undefined)).toBeUndefined();
  });
});

describe("hostedErrorMessage", () => {
  it("tells the subject the link does not work instead of what an admin should do", () => {
    const paused = hostedErrorMessage(
      apiError(409, "customer_paused", "paused"),
      t,
    );
    expect(paused).toBe(t("proofLink.errors.unavailable"));
    expect(paused).not.toBe(
      proofingErrorMessage(apiError(409, "customer_paused", "paused"), t),
    );
  });

  it("keeps the copy the subject can act on, and the generic one offline", () => {
    const started = apiError(409, "link_started", "started");
    expect(hostedErrorMessage(started, t)).toBe(
      proofingErrorMessage(started, t),
    );
    expect(hostedErrorMessage(new Error("offline"), t)).toBe(
      t("identityProofing.errors.generic"),
    );
  });
});

describe("deliveriesPollInterval", () => {
  it("polls soon for a first send, slowly for a retry, and not without one pending", () => {
    expect(deliveriesPollInterval(undefined)).toBe(false);
    expect(deliveriesPollInterval([{ status: "delivered", attempts: 1 }])).toBe(
      false,
    );
    const firstSend = deliveriesPollInterval([
      { status: "pending", attempts: 0 },
      { status: "pending", attempts: 4 },
    ]);
    const retrying = deliveriesPollInterval([
      { status: "pending", attempts: 4 },
    ]);
    expect(typeof firstSend).toBe("number");
    expect(typeof retrying).toBe("number");
    expect(firstSend as number).toBeLessThan(retrying as number);
  });
});

describe("effective branding", () => {
  const org: ProofingBranding = {
    displayName: "Acme",
    primaryColor: "#111111",
    secondaryColor: "#222222",
    textColor: "#333333",
    surfaceColor: "#444444",
    borderColor: "#555555",
    linkColor: "#666666",
    successColor: "#777777",
    warningColor: "#888888",
    errorColor: "#999999",
    topbarColor: "#aaaaaa",
    fontFamily: "Open Sans, sans-serif",
    supportContact: "help@acme.example",
    privacyUrl: "https://acme.example/privacy",
    logoUri: "/org-logo",
    hidePoweredBy: true,
  };
  const none: ProofingFlowBranding = {
    custom: true,
    displayName: "",
    primaryColor: "",
    supportContact: "",
    privacyUrl: "",
    logoUri: "",
  };

  it("is the org's when the flow is not custom, whatever it holds", () => {
    expect(
      effectiveBranding(org, { ...none, custom: false, displayName: "Trips" }),
    ).toEqual(org);
  });

  it("is the org's when a custom flow sets nothing", () => {
    expect(effectiveBranding(org, none)).toEqual(org);
  });

  it("takes each field the flow sets and the org's for the rest", () => {
    expect(
      effectiveBranding(org, {
        ...none,
        displayName: "Acme Trips",
        logoUri: "/flow-logo",
      }),
    ).toEqual({
      ...org,
      displayName: "Acme Trips",
      logoUri: "/flow-logo",
    });
  });

  it("keeps the org's powered-by line", () => {
    expect(
      effectiveBranding({ ...org, hidePoweredBy: false }, none).hidePoweredBy,
    ).toBe(false);
  });

  it("keeps the org's white-label colours and font", () => {
    const own = { ...none, primaryColor: "#abcdef" };
    expect(effectiveBranding(org, own)).toEqual({
      ...org,
      primaryColor: "#abcdef",
    });
  });
});

describe("app look", () => {
  const bare: ProofingBranding = {
    displayName: "",
    primaryColor: "",
    secondaryColor: "",
    textColor: "",
    surfaceColor: "",
    borderColor: "",
    linkColor: "",
    successColor: "",
    warningColor: "",
    errorColor: "",
    topbarColor: "",
    fontFamily: "",
    supportContact: "help@acme.example",
    privacyUrl: "https://acme.example/privacy",
    logoUri: "",
    hidePoweredBy: false,
  };
  const theme = {
    primaryColor: "#036bd2",
    topbarColor: "#101e1e",
    logoUri: "/theme-logo",
  };

  it("is the org's theme under the branding's name without a look of its own", () => {
    expect(
      appLook({ ...bare, displayName: "Acme Trips" }, theme, "Acme"),
    ).toEqual({
      name: "Acme Trips",
      primaryColor: "#036bd2",
      headerColor: "#101e1e",
      logoUri: "/theme-logo",
    });
  });

  it("takes the branding's logo over the theme's", () => {
    expect(appLook({ ...bare, logoUri: "/flow-logo" }, theme, "Acme")).toEqual({
      name: "Acme",
      primaryColor: "#036bd2",
      headerColor: "#101e1e",
      logoUri: "/flow-logo",
    });
  });

  it("is the branding alone once it has a colour or a font, the app's own for the rest", () => {
    expect(
      appLook({ ...bare, primaryColor: "#22337a" }, theme, "Acme"),
    ).toEqual({
      name: "Acme",
      primaryColor: "#22337a",
      headerColor: "",
      logoUri: "",
    });
    expect(
      appLook({ ...bare, fontFamily: "Inter, sans-serif" }, theme, "Acme")
        .primaryColor,
    ).toBe("");
    expect(
      appLook(
        { ...bare, secondaryColor: "#112233", topbarColor: "#0a3d2e" },
        theme,
        "Acme",
      ).headerColor,
    ).toBe("#0a3d2e");
  });

  it("does not count the support contact or privacy link as a look", () => {
    expect(appLook(bare, theme, "Acme").primaryColor).toBe("#036bd2");
  });
});

describe("going back to the organisation's branding", () => {
  const saved: ProofingFlowBranding = {
    custom: true,
    displayName: "Acme Trips",
    primaryColor: "",
    supportContact: "",
    privacyUrl: "",
    logoUri: "/flow-logo",
  };

  it("drops the flow's own branding only when it had one", () => {
    expect(dropsOwnBranding(saved, false)).toBe(true);
    expect(dropsOwnBranding(saved, true)).toBe(false);
    expect(dropsOwnBranding({ ...saved, custom: false }, false)).toBe(false);
    expect(dropsOwnBranding({ ...saved, custom: false }, true)).toBe(false);
  });
});

describe("hosted page form", () => {
  const saved: ProofingFlowHosted = {
    enabled: true,
    locales: [],
    completion: "redirect",
  };

  it("starts from what is saved, every language ticked for none", () => {
    expect(hostedDraft(saved)).toEqual({
      ...saved,
      locales: [...SUPPORTED_LANGUAGES],
    });
    expect(hostedDraftDirty(hostedDraft(saved), saved)).toBe(false);
  });

  it("holds a change until it is reset to what is saved", () => {
    const changed = { ...hostedDraft(saved), enabled: false, locales: ["nl"] };
    expect(hostedDraftDirty(changed, saved)).toBe(true);
    expect(hostedDraftDirty({ ...changed, enabled: true }, saved)).toBe(true);
    expect(hostedDraftDirty(hostedDraft(saved), saved)).toBe(false);
    expect(
      hostedDraftDirty(
        { ...hostedDraft(saved), completion: "done" as const },
        saved,
      ),
    ).toBe(true);
  });
});

describe("hosted page languages", () => {
  it("shows every language ticked when none is saved", () => {
    expect(selectedLocales([])).toEqual([...SUPPORTED_LANGUAGES]);
  });

  it("shows only the saved languages ticked", () => {
    expect(selectedLocales(["nl"])).toEqual(["nl"]);
  });

  it("saves every language ticked as none", () => {
    expect(savedLocales([...SUPPORTED_LANGUAGES])).toEqual([]);
  });

  it("saves a subset as ticked, in the order given", () => {
    expect(savedLocales(["nl"])).toEqual(["nl"]);
  });

  it("round-trips none and a subset", () => {
    expect(savedLocales(selectedLocales([]))).toEqual([]);
    expect(savedLocales(selectedLocales(["en"]))).toEqual(["en"]);
  });
});
