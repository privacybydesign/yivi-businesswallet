import type {
  HostedProofing,
  HostedTheme,
  ProofingBranding,
  ProofingFlow,
} from "../api/identity-proofing";
import type { OrgTheme } from "../api/theme";
import {
  APP_IDEM,
  acceptsPersonalData,
  allowsApp,
  yiviAppAvailable,
} from "./identity-proofing";

// The branding settings show the real hosted page in a frame of the wallet's
// own origin, so the page's look applies to that frame's document alone. The
// frame asks for its page once it listens, and the settings send it again on
// every change.
export const HOSTED_PREVIEW_PATH = "/preview/hosted";
export const HOSTED_PREVIEW_READY = "hosted-preview-ready";
export const HOSTED_PREVIEW_PAGE = "hosted-preview-page";

export interface HostedPreviewMessage {
  type: typeof HOSTED_PREVIEW_PAGE;
  page: HostedProofing;
}

// A link the preview shows as not yet expired.
const PREVIEW_LINK_TTL_MS = 10 * 60 * 1000;

export interface HostedPreviewInput {
  // The name the page signs with: the branding's display name, else the
  // org's.
  name: string;
  // The branding the page wears, the flow's own over the org's.
  branding: ProofingBranding;
  // The wallet's theme, which each look field the branding leaves empty is.
  theme: Pick<
    OrgTheme,
    | "primaryColor"
    | "accentColor"
    | "textColor"
    | "surfaceColor"
    | "borderColor"
    | "linkColor"
    | "successColor"
    | "warningColor"
    | "errorColor"
    | "topbarColor"
    | "fontFamily"
  >;
  // The flow the page runs; none shows an identity check that asks nothing.
  flow: ProofingFlow | undefined;
  dataRetentionDays: number;
  language: string;
  now: number;
}

// The look the hosted view carries: each field the branding's when set, else
// the wallet theme's, as AppBrandingResolver resolves it.
export function hostedPreviewTheme(
  branding: ProofingBranding,
  theme: HostedPreviewInput["theme"],
): HostedTheme {
  return {
    primaryColor: branding.primaryColor || theme.primaryColor,
    secondaryColor: branding.secondaryColor || theme.accentColor,
    textColor: branding.textColor || theme.textColor,
    surfaceColor: branding.surfaceColor || theme.surfaceColor,
    borderColor: branding.borderColor || theme.borderColor,
    linkColor: branding.linkColor || theme.linkColor,
    successColor: branding.successColor || theme.successColor,
    warningColor: branding.warningColor || theme.warningColor,
    errorColor: branding.errorColor || theme.errorColor,
    headerColor: branding.topbarColor || theme.topbarColor,
    fontFamily: branding.fontFamily || theme.fontFamily,
  };
}

// The hosted view a fresh link to an external person would get.
export function hostedPreviewPage(input: HostedPreviewInput): HostedProofing {
  const { flow } = input;
  return {
    status: "pending",
    linkExpiresAt: new Date(input.now + PREVIEW_LINK_TTL_MS).toISOString(),
    started: false,
    addressGiven: false,
    employmentGiven: false,
    sessionId: "",
    embedOrigins: [],
    language: input.language,
    locales: [],
    organization: {
      name: input.name,
      branding: input.branding,
      dataRetentionDays: input.dataRetentionDays,
    },
    flow: {
      requestedAttributes: flow?.requestedAttributes ?? [],
      attributeReasons: flow?.attributeReasons ?? {},
      yiviAvailable: flow ? yiviAppAvailable(flow) : false,
      idemAvailable: flow ? allowsApp(flow, APP_IDEM) : true,
      yiviPersonalData: flow ? acceptsPersonalData(flow) : false,
      browserOnly: flow?.browserOnly ?? false,
      diplomaMode: flow?.diplomaMode ?? "off",
      addressMode: flow?.addressMode ?? "off",
      employmentMode: flow?.employmentMode ?? "off",
      kind: flow?.kind ?? "identity",
      retentionDays: flow?.retentionDays ?? input.dataRetentionDays,
    },
    diplomas: [],
    theme: hostedPreviewTheme(input.branding, input.theme),
  };
}

export function isHostedPreviewMessage(
  data: unknown,
): data is HostedPreviewMessage {
  return (
    typeof data === "object" &&
    data !== null &&
    (data as { type?: unknown }).type === HOSTED_PREVIEW_PAGE &&
    typeof (data as { page?: unknown }).page === "object"
  );
}
