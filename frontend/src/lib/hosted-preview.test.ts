import { describe, expect, it } from "vitest";
import type { ProofingBranding } from "../api/identity-proofing";
import { hostedPreviewPage, hostedPreviewTheme } from "./hosted-preview";

const branding: ProofingBranding = {
  displayName: "Acme Trips",
  primaryColor: "#22337a",
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
};

const theme = {
  primaryColor: "#036bd2",
  accentColor: "#5412c7",
  textColor: "#101e1e",
  surfaceColor: "",
  borderColor: "",
  linkColor: "",
  successColor: "",
  warningColor: "",
  errorColor: "",
  topbarColor: "#0a3d2e",
  fontFamily: '"Nunito", system-ui, sans-serif',
};

describe("hosted preview", () => {
  it("takes each look field of the branding, the wallet theme's for the rest", () => {
    expect(hostedPreviewTheme(branding, theme)).toMatchObject({
      primaryColor: "#22337a",
      secondaryColor: "#5412c7",
      textColor: "#101e1e",
      headerColor: "#0a3d2e",
      fontFamily: '"Nunito", system-ui, sans-serif',
    });
  });

  it("is a fresh link that asks nothing in particular without a flow", () => {
    const page = hostedPreviewPage({
      name: "Acme Trips",
      branding,
      theme,
      flow: undefined,
      dataRetentionDays: 30,
      language: "en",
      now: 0,
    });
    expect(page.started).toBe(false);
    expect(page.status).toBe("pending");
    expect(page.organization.name).toBe("Acme Trips");
    expect(page.flow.idemAvailable).toBe(true);
    expect(page.flow.retentionDays).toBe(30);
  });
});
