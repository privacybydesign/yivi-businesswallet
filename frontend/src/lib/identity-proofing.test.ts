import { describe, expect, it } from "vitest";
import { ApiError } from "../api/http";
import i18n from "../i18n";
import type { ProofingFlow } from "../api/identity-proofing";
import {
  attributeAvailable,
  draftFromFlow,
  draftSteps,
  emptyFlowDraft,
  flowDraftError,
  flowSpecFromDraft,
  isProofingLive,
  latestRequestByMember,
  proofingErrorMessage,
  proofingStatusLabel,
  sendableFlows,
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

describe("attributeAvailable", () => {
  it("follows the step that produces the data", () => {
    const chipOnly = draft({ faceVerification: false });
    expect(attributeAvailable(chipOnly, "dg1")).toBe(true);
    expect(attributeAvailable(chipOnly, "chip_checks")).toBe(true);
    expect(attributeAvailable(chipOnly, "selfie")).toBe(false);
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

describe("flowDraftError", () => {
  it("points at the field to fix", () => {
    expect(flowDraftError(draft())).toBeNull();
    expect(flowDraftError(draft({ name: " " }))).toBe("name");
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

describe("latestRequestByMember", () => {
  it("keeps the newest request per member", () => {
    const latest = latestRequestByMember([
      { id: "new", subjectUserId: "u1" },
      { id: "old", subjectUserId: "u1" },
      { id: "legacy" },
    ]);
    expect(latest.get("u1")?.id).toBe("new");
    expect(latest.size).toBe(1);
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

describe("proofing status", () => {
  it("keeps polling only statuses the service may still change", () => {
    expect(isProofingLive("in_progress")).toBe(true);
    expect(isProofingLive("needs_review")).toBe(true);
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

  it("maps known codes to their own copy", () => {
    expect(
      proofingErrorMessage(apiError(422, "flow_not_allowed", "internal"), t),
    ).toBe(t("identityProofing.errors.flowNotAllowed"));
  });

  it("falls back to generic copy for anything else", () => {
    expect(proofingErrorMessage(new Error("boom"), t)).toBe(
      t("identityProofing.errors.generic"),
    );
  });
});
