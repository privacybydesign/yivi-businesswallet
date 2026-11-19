import { describe, expect, it } from "vitest";
import type { HostedTheme } from "../api/identity-proofing";
import { idemLook, isIdemBranded } from "./idem-look";

const bare: HostedTheme = {
  primaryColor: "",
  secondaryColor: "",
  textColor: "",
  surfaceColor: "",
  borderColor: "",
  linkColor: "",
  successColor: "",
  warningColor: "",
  errorColor: "",
  headerColor: "",
  fontFamily: "",
};

describe("idem look", () => {
  it("takes a primary that reads on white, with the text that reads on it", () => {
    const look = idemLook({ ...bare, primaryColor: "#1E5A47" });
    expect(look.primary).toBe("#1e5a47");
    expect(look.onPrimary).toBe("#ffffff");
  });

  it("keeps the app's own primary for one too light to read on white", () => {
    expect(idemLook({ ...bare, primaryColor: "#ffee00" }).primary).toBe(
      "#ba3354",
    );
  });

  it("uses the design pair without a font, and a bundled font for both", () => {
    expect(idemLook(bare).headingFont).toContain("Montserrat");
    expect(idemLook(bare).bodyFont).toContain("Nunito");
    const look = idemLook({
      ...bare,
      fontFamily: '"Open Sans", system-ui, sans-serif',
    });
    expect(look.headingFont).toContain('"Open Sans"');
    expect(look.bodyFont).toContain('"Open Sans"');
  });

  it("falls back to the platform font for one the app does not have", () => {
    expect(idemLook({ ...bare, fontFamily: "Georgia, serif" }).bodyFont).toBe(
      "system-ui",
    );
  });

  it("is Idem's own look for a session without branding", () => {
    expect(isIdemBranded(bare, "")).toBe(false);
    expect(isIdemBranded(bare, "/logo")).toBe(true);
    const look = idemLook(bare, false);
    expect(look.primary).toBe("#ba3354");
    expect(look.ink).toBe("#484747");
    expect(look.buttonRadius).toBe("8px");
    expect(look.bodyFont).toBe("system-ui");
  });
});
