import { beforeEach, describe, expect, it, vi } from "vitest";
import type { z } from "zod";
import { request } from "./http";
import {
  getHostedProofing,
  getProofingCustomerFlows,
} from "./identity-proofing";

// The subject is told the retention the server works out (the flow's, else
// the customer's): the pages read it from these two responses, so their
// schemas must carry it and refuse an answer without it.

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
  sessionId: "ps_b7k2mq4xz6rt3vnw5hc2jd7fae",
  embedOrigins: [],
  locales: [],
  customer: {
    name: "Acme",
    branding: {
      displayName: "",
      primaryColor: "",
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
    kind: "identity",
    retentionDays: RETENTION_DAYS,
  },
  diplomas: [],
};

const customerFlow = {
  id: "f-1",
  version: 1,
  active: true,
  name: "Onboarding",
  steps: [],
  createdAt: "2026-09-14T08:12:00Z",
  completable: true,
  assigned: true,
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

  it("comes with each of a customer's flows", async () => {
    await getProofingCustomerFlows("acme", "c-1");
    const schema = lastSchema();
    expect(schema.parse([customerFlow])).toMatchObject([
      { retentionDays: RETENTION_DAYS },
    ]);
    const flow: Record<string, unknown> = { ...customerFlow };
    delete flow.retentionDays;
    expect(() => schema.parse([flow])).toThrow();
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
