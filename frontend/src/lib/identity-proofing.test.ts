import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { ApiError } from "../api/http";
import i18n from "../i18n";
import type { ProofingFlow } from "../api/identity-proofing";
import { DIPLOMA_MODES } from "../api/identity-proofing";
import {
  diplomaRejectionReason,
  diplomaStepDone,
  OUTCOME_ONLY,
  readsIdentity,
  sendableByMail,
  verifyStages,
  progressPollContinues,
  reviewSubmitStep,
  assignedFlows,
  attributeAvailable,
  draftFromFlow,
  draftSteps,
  editedFlowSelection,
  emptyFlowDraft,
  flowDraftError,
  reachesAssuranceLevel,
  flowSpecFromDraft,
  formatDuration,
  secondsUntil,
  isProofingLive,
  isProofingStep,
  isRequestedAttribute,
  proofingErrorMessage,
  proofingStatsBy,
  proofingStatusLabel,
  requestSubject,
  searchCustomers,
  sendableFlows,
  sessionDurationSeconds,
  sessionFilterCounts,
  shortRequestId,
  sumProofingStats,
  verifiedShare,
  customerDisplayStatus,
  DATA_RETENTION_DAY_OPTIONS,
  isHexColor,
  isSuccessStatus,
  proofingMethodLabel,
  yiviAppAvailable,
  sessionEventDetail,
  readableTextOn,
  SESSION_TTL_OPTIONS_SECONDS,
} from "./identity-proofing";
import type { ProofingFlowDraft } from "./identity-proofing";

const t = i18n.getFixedT("en");

function apiError(status: number, code: string, message: string): ApiError {
  return new ApiError(status, "", "/api/v1/x", { error: message, code });
}

function draft(patch: Partial<ProofingFlowDraft> = {}): ProofingFlowDraft {
  return { ...emptyFlowDraft(), name: "  Passport check ", ...patch };
}

describe("flowSpecFromDraft", () => {
  it("sends the document scan and chip scan as a pair with passive auth locked on", () => {
    const spec = flowSpecFromDraft(draft({ faceVerification: false }));
    expect(spec).toMatchObject({
      name: "Passport check",
      steps: ["document_capture", "nfc_read"],
      requiredChecks: ["nfc.passive_auth"],
      requestedAttributes: ["dg1", "dg11", "dg2", "chip_checks"],
    });
    expect(spec.checkThresholds).toBeUndefined();
  });

  it("locks face match on with face verification and keeps the extras optional", () => {
    const spec = flowSpecFromDraft(
      draft({
        chipAuthentication: true,
        liveness: true,
        faceMatchThreshold: "0.8",
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
      draft({ documentAndChip: false, chipAuthentication: true }),
    );
    expect(spec.steps).toEqual(["face_verification"]);
    expect(spec.requiredChecks).toEqual(["face.match"]);
    expect(spec.requestedAttributes).toEqual(["selfie", "biometrics"]);
  });

  it("lets optional data be deselected", () => {
    const spec = flowSpecFromDraft(
      draft({ requestedAttributes: new Set(["dg1", "biometrics"]) }),
    );
    expect(spec.requestedAttributes).toEqual(["dg1", "biometrics"]);
  });

  it("sends only the overrides the admin set", () => {
    const spec = flowSpecFromDraft(
      draft({
        acceptedDocumentTypes: "P, I ,",
        acceptedIssuingCountries: "nld, bel",
        assuranceLevel: "substantial",
        bsnPolicy: "mask",
        blurFace: "false",
        retentionSeconds: "3600",
      }),
    );
    expect(spec).toMatchObject({
      acceptedDocumentTypes: ["P", "I"],
      acceptedIssuingCountries: ["NLD", "BEL"],
      requiredAssuranceLevel: "substantial",
      bsnPolicy: "mask",
      blurFace: false,
      retentionOverrideSeconds: 3600,
    });
    expect(spec.blurBsn).toBeUndefined();
    expect(
      flowSpecFromDraft(draft({ retentionSeconds: "0" }))
        .retentionOverrideSeconds,
    ).toBeUndefined();
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
      checkThresholds: { "face.match": 0.7 },
      requiredAssuranceLevel: "substantial",
      bsnPolicy: "omit",
      blurFace: true,
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
      // Kept as stored, though substantial needs them: the editor refuses to
      // save it until they are turned on, rather than turning them on itself.
      chipAuthentication: false,
      liveness: false,
      faceMatchThreshold: "0.7",
      assuranceLevel: "substantial",
      bsnPolicy: "omit",
      blurFace: "true",
      blurBsn: "",
      retentionSeconds: "86400",
    });
    expect(flowSpecFromDraft(edited)).toMatchObject({
      requestedAttributes: ["dg1", "selfie"],
      assuranceTiers: [{ level: "low", minPercent: 0 }],
      legalBasis: "consent",
    });
  });
});

describe("assurance level", () => {
  it("turns nothing on: a draft that cannot reach its level is refused", () => {
    const chipOnly = draft({ faceVerification: false });
    expect(flowDraftError({ ...chipOnly, assuranceLevel: "low" })).toBeNull();
    const short = { ...chipOnly, assuranceLevel: "substantial" as const };
    expect(reachesAssuranceLevel(short)).toBe(false);
    expect(flowDraftError(short)).toBe("assuranceLevel");
    expect(flowSpecFromDraft(short).steps).toEqual([
      "document_capture",
      "nfc_read",
    ]);
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
    expect(flowSpecFromDraft({ ...full, assuranceLevel: "low" })).toMatchObject(
      {
        requiredChecks: [
          "nfc.passive_auth",
          "nfc.chip_auth",
          "face.match",
          "face.liveness",
        ],
        requiredAssuranceLevel: "low",
      },
    );
  });

  it("needs nothing without a level", () => {
    expect(reachesAssuranceLevel(draft({ documentAndChip: false }))).toBe(true);
  });
});

describe("flowDraftError", () => {
  it("points at the field to fix", () => {
    expect(flowDraftError(draft())).toBeNull();
    expect(flowDraftError(draft({ name: " " }))).toBe("name");
    // Countries are 3-letter codes; whether one exists is the engine's check.
    expect(
      flowDraftError(draft({ acceptedIssuingCountries: "nld, DEU" })),
    ).toBeNull();
    expect(flowDraftError(draft({ acceptedIssuingCountries: "NL" }))).toBe(
      "issuingCountries",
    );
    expect(
      flowDraftError(draft({ acceptedIssuingCountries: "NLD, Nederland" })),
    ).toBe("issuingCountries");
    expect(
      flowDraftError(
        draft({ documentAndChip: false, faceVerification: false }),
      ),
    ).toBe("steps");
    expect(flowDraftError(draft({ faceMatchThreshold: "1.5" }))).toBe(
      "faceMatchThreshold",
    );
    expect(flowDraftError(draft({ retentionSeconds: "-1" }))).toBe(
      "retentionSeconds",
    );
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

describe("assignedFlows", () => {
  it("offers only a customer's assigned flows and starts on its default", () => {
    const { sendable, initial } = assignedFlows([
      { id: "a", assigned: false, default: false },
      { id: "b", assigned: true, default: false },
      { id: "c", assigned: true, default: true },
    ]);
    expect(sendable.map((f) => f.id)).toEqual(["b", "c"]);
    expect(initial?.id).toBe("c");
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

  it("falls the default to the first ticked flow once it is unticked", () => {
    const { selection, dirty } = editedFlowSelection(
      flows,
      new Set(["b", "a"]),
      "c",
      saved,
    );
    expect(selection).toEqual({ flowIds: ["a", "b"], defaultFlowId: "a" });
    expect(dirty).toBe(true);
  });

  it("has no default when nothing is ticked", () => {
    expect(editedFlowSelection(flows, new Set(), "c", saved).selection).toEqual(
      { flowIds: [], defaultFlowId: "" },
    );
  });

  it("is dirty when only the default moved", () => {
    expect(
      editedFlowSelection(flows, new Set(["a", "c"]), "a", saved).dirty,
    ).toBe(true);
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

  it("labels known statuses and passes an unknown one through", () => {
    expect(proofingStatusLabel("needs_review", t)).toBe("Needs review");
    expect(proofingStatusLabel("something_new", t)).toBe("something_new");
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

  it("says a paused customer takes no request", () => {
    expect(
      proofingErrorMessage(apiError(409, "customer_paused", "internal"), t),
    ).toBe(t("identityProofing.errors.customerPaused"));
  });

  it("maps known codes to their own copy", () => {
    expect(
      proofingErrorMessage(apiError(422, "flow_not_allowed", "internal"), t),
    ).toBe(t("identityProofing.errors.flowNotAllowed"));
  });

  it.each([
    [409, "customer_sessions_left", "customerSessionsLeft"],
    [404, "api_key_not_found", "apiKeyNotFound"],
    [404, "session_not_found", "sessionNotFound"],
    [409, "not_under_review", "notUnderReview"],
    [422, "reference_photo_required", "referencePhotoRequired"],
    [404, "organization_not_found", "organizationNotFound"],
    [404, "webhook_not_found", "webhookNotFound"],
    [409, "link_started", "linkStarted"],
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

describe("proofing stats", () => {
  const row = (
    customerId: string,
    flowId: string,
    sessions: number,
    approved: number,
  ): {
    customerId: string;
    flowId: string;
    sessions: number;
    approved: number;
    rejected: number;
    needsReview: number;
    expired: number;
    cancelled: number;
  } => ({
    customerId,
    flowId,
    sessions,
    approved,
    rejected: 1,
    needsReview: 0,
    expired: 1,
    cancelled: 1,
  });
  const rows = [
    row("c1", "f1", 10, 6),
    row("c1", "f2", 4, 2),
    row("c2", "f1", 5, 3),
  ];

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

  it("groups per customer and per flow", () => {
    const perCustomer = proofingStatsBy(rows, (r) => r.customerId);
    expect(perCustomer.get("c1")?.sessions).toBe(14);
    expect(perCustomer.get("c2")?.approved).toBe(3);
    expect(perCustomer.has("c3")).toBe(false);
    const perFlow = proofingStatsBy(
      rows.filter((r) => r.customerId === "c1"),
      (r) => r.flowId,
    );
    expect([...perFlow.keys()]).toEqual(["f1", "f2"]);
  });

  it("has no verified share without sessions", () => {
    expect(verifiedShare(sumProofingStats([]))).toBeUndefined();
    expect(verifiedShare(sumProofingStats([row("c", "f", 4, 3)]))).toBe(0.75);
  });
});

describe("searchCustomers", () => {
  const customers = [{ name: "Veldhuis Verzekeringen" }, { name: "Kliq" }];

  it("matches on the name, ignoring case and space", () => {
    expect(searchCustomers(customers, "  VERZ ")).toEqual([customers[0]]);
    expect(searchCustomers(customers, "")).toEqual(customers);
    expect(searchCustomers(customers, "nope")).toEqual([]);
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

describe("sessions tab", () => {
  const sent = "2026-09-25T10:00:00Z";
  const expires = "2026-09-25T10:10:00Z";

  it("counts each filter", () => {
    const statuses = [
      "approved",
      "approved",
      "rejected",
      "expired",
      "pending",
      "needs_review",
    ];
    expect(sessionFilterCounts(statuses.map((status) => ({ status })))).toEqual(
      {
        all: 6,
        review: 1,
        verified: 2,
        failed: 1,
        expired: 1,
      },
    );
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

  it("offers every data retention and no other", () => {
    const list = /DataRetentionDayOptions = \[\]int\{([^}]*)\}/.exec(source);
    expect(list).not.toBeNull();
    const backend = list![1].split(",").map((e) => Number(e.trim()));
    expect([...DATA_RETENTION_DAY_OPTIONS]).toEqual(backend);
  });
});

describe("customerDisplayStatus", () => {
  it("flags an active customer whose webhook fails", () => {
    expect(
      customerDisplayStatus({
        status: "active",
        webhook: { state: "failing" },
        hasApiKey: true,
      }),
    ).toBe("needs_attention");
    expect(
      customerDisplayStatus({
        status: "paused",
        webhook: { state: "failing" },
        hasApiKey: true,
      }),
    ).toBe("paused");
    expect(
      customerDisplayStatus({
        status: "active",
        webhook: { state: "delivering" },
        hasApiKey: true,
      }),
    ).toBe("active");
  });

  it("asks for a live API key before anything but a pause", () => {
    expect(
      customerDisplayStatus({
        status: "active",
        webhook: { state: "failing" },
        hasApiKey: false,
      }),
    ).toBe("setup_needed");
    expect(
      customerDisplayStatus({
        status: "paused",
        webhook: { state: "delivering" },
        hasApiKey: false,
      }),
    ).toBe("paused");
  });

  it("tells a 2xx from the rest", () => {
    expect(isSuccessStatus(204)).toBe(true);
    expect(isSuccessStatus(503)).toBe(false);
    expect(isSuccessStatus(301)).toBe(false);
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

describe("diploma step", () => {
  it("adds the diploma stage after the session when the flow has it", () => {
    expect(verifyStages({ appChoice: true, diplomas: true })).toEqual([
      "overview",
      "method",
      "session",
      "diplomas",
    ]);
    expect(verifyStages({ appChoice: false, diplomas: false })).toEqual([
      "overview",
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

describe("data request review", () => {
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

  it("names the sessions an erasure deletes", () => {
    const key = "customers.sessions.dataRequest.data_erasure.confirm.title";
    expect(t(key, { count: 1 })).toBe("Erase 1 session?");
    expect(t(key, { count: 3 })).toBe("Erase 3 sessions?");
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
