import { beforeEach, describe, expect, it, vi } from "vitest";
import { request } from "./http";
import { saveProofingFlowBranding } from "./identity-proofing";

// The Branding tab keeps its draft logo while "Same as the organisation" is
// chosen. That mode deletes the flow's own logo, so the file is not sent: the
// server would otherwise reject a draft it never uses.

vi.mock("./http", () => ({ request: vi.fn(), absoluteApiUrl: vi.fn() }));

const fields = {
  displayName: "Acme Reizen",
  primaryColor: "#112233",
  supportContact: "help@acme.example",
  privacyUrl: "https://acme.example/privacy",
};

// The form the last save sent.
function lastForm(): FormData {
  const call = vi.mocked(request).mock.lastCall;
  if (call === undefined) throw new Error("no request made");
  const body = call[1].body;
  if (!(body instanceof FormData)) throw new Error("body is not a form");
  return body;
}

describe("saveProofingFlowBranding", () => {
  const logo = new File([new Uint8Array([1])], "logo.png", {
    type: "image/png",
  });

  beforeEach(() => {
    vi.mocked(request).mockReset();
  });

  it("sends the draft logo when the flow is custom", async () => {
    await saveProofingFlowBranding("acme", "flow", {
      custom: true,
      ...fields,
      logo,
    });
    expect(lastForm().get("custom")).toBe("true");
    expect(lastForm().get("logo")).toBeInstanceOf(File);
  });

  it("sends the draft logo's removal when the flow is custom", async () => {
    await saveProofingFlowBranding("acme", "flow", {
      custom: true,
      ...fields,
      removeLogo: true,
    });
    expect(lastForm().get("removeLogo")).toBe("true");
  });

  it("sends no logo field when the flow is not custom", async () => {
    await saveProofingFlowBranding("acme", "flow", {
      custom: false,
      ...fields,
      logo,
      removeLogo: true,
    });
    expect(lastForm().get("custom")).toBe("false");
    expect(lastForm().has("logo")).toBe(false);
    expect(lastForm().has("removeLogo")).toBe(false);
  });
});
