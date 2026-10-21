import { beforeEach, describe, expect, it, vi } from "vitest";
import type { z } from "zod";
import { request } from "./http";
import {
  getHostedProofing,
  getHostedProofingStatus,
  getProofingFlows,
  getProofingSettings,
} from "./identity-proofing";

// The subject is told the retention the server works out (the flow's, else
// the org's): the pages read it from these two responses, so their schemas
// must carry it. The hosted page refuses an answer without it; the flow list's
// schema leaves it optional, as a single version has none.

vi.mock("./http", () => ({ request: vi.fn(), absoluteApiUrl: vi.fn() }));

const RETENTION_DAYS = 31;

// The schema the last request was made with.
function lastSchema(): z.ZodType {
  const call = vi.mocked(request).mock.lastCall;
  if (call === undefined) throw new Error("no request made");
  return call[1].schema;
}

const hostedPage = {
  status: "pending",
  linkExpiresAt: "2026-10-05T09:30:00Z",
  started: false,
  addressGiven: false,
  sessionId: "ps_b7k2mq4xz6rt3vnw5hc2jd7fae",
  embedOrigins: [],
  locales: [],
  organization: {
    name: "Acme",
    branding: {
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
      supportContact: "",
      privacyUrl: "",
      logoUri: "",
      hidePoweredBy: false,
    },
    dataRetentionDays: 30,
  },
  flow: {
    name: "Onboarding",
    requestedAttributes: [],
    yiviAvailable: true,
    diplomaMode: "off",
    addressMode: "off",
    kind: "identity",
    retentionDays: RETENTION_DAYS,
  },
  diplomas: [],
};

const listedFlow = {
  id: "f-1",
  version: 1,
  active: true,
  name: "Onboarding",
  steps: [],
  createdAt: "2026-09-14T08:12:00Z",
  completable: true,
  allowed: true,
  default: true,
  retentionDays: RETENTION_DAYS,
};

describe("the retention a subject is told", () => {
  beforeEach(() => {
    vi.mocked(request).mockReset();
  });

  it("comes with the hosted page's flow", async () => {
    await getHostedProofing("token");
    const schema = lastSchema();
    expect(schema.parse(hostedPage)).toMatchObject({
      flow: { retentionDays: RETENTION_DAYS },
    });
    const flow: Record<string, unknown> = { ...hostedPage.flow };
    delete flow.retentionDays;
    expect(() => schema.parse({ ...hostedPage, flow })).toThrow();
  });

  it("comes with each of the org's flows", async () => {
    await getProofingFlows("acme");
    const schema = lastSchema();
    expect(schema.parse([listedFlow])).toMatchObject([
      { retentionDays: RETENTION_DAYS },
    ]);
  });
});

// A value the backend adds to a closed set must not fail the whole page.
describe("a value the backend adds", () => {
  beforeEach(() => {
    vi.mocked(request).mockReset();
  });

  it("falls back on the hosted page's flow", async () => {
    await getHostedProofing("token");
    const schema = lastSchema();
    const flow = {
      ...hostedPage.flow,
      diplomaMode: "optional",
      kind: "data_portability",
    };
    expect(schema.parse({ ...hostedPage, flow })).toMatchObject({
      flow: { diplomaMode: "required", kind: "identity" },
    });
  });
});

// What the law makes the org keep is read from the settings, and told to a
// person whose erasure it declined through their link's progress.
describe("the legal retention", () => {
  const branding = hostedPage.organization.branding;
  const settings = {
    sessionTtlSeconds: 600,
    dataRetentionDays: 30,
    erasureHandling: "review",
    legalRetention: null,
    branding,
    allowedRedirectOrigins: [],
  };
  const legal = { months: 60, reasonEn: "The Wwft.", reasonNl: "De Wwft." };

  beforeEach(() => {
    vi.mocked(request).mockReset();
  });

  it("comes with the settings, null without one", async () => {
    await getProofingSettings("acme");
    const schema = lastSchema();
    expect(schema.parse(settings)).toMatchObject({ legalRetention: null });
    expect(schema.parse({ ...settings, legalRetention: legal })).toMatchObject({
      legalRetention: legal,
    });
    const missing: Record<string, unknown> = { ...settings };
    delete missing.legalRetention;
    expect(() => schema.parse(missing)).toThrow();
  });

  it("comes with the link's progress of a declined erasure", async () => {
    await getHostedProofingStatus("token");
    const schema = lastSchema();
    const progress = {
      status: "rejected",
      errorCode: "LEGAL_RETENTION",
      linkExpiresAt: "2026-10-19T12:00:00Z",
      started: true,
      addressGiven: false,
    };
    const notice = {
      until: "2031-10-19T12:00:00Z",
      reason: { en: "The Wwft.", nl: "De Wwft." },
    };
    expect(schema.parse(progress)).not.toHaveProperty("legalRetention");
    expect(schema.parse({ ...progress, legalRetention: notice })).toMatchObject(
      { legalRetention: notice },
    );
  });
});
