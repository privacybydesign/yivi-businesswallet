import { describe, expect, it } from "vitest";
import i18n, { SUPPORTED_LANGUAGES } from "../i18n";
import {
  consentDetailKey,
  consentDocuments,
  consentItems,
  consentLabelKey,
  consentSources,
  consentWording,
  hostedConsentBasis,
  joinSources,
} from "./consent";
import type { ConsentSource } from "./consent";
import type { DocumentKind } from "./identity-proofing";

const CARDS: DocumentKind[] = ["identity_card", "drivers_license"];

const EVERY_DOCUMENT = [
  "passport",
  "identity_card",
  "residence_permit",
  "drivers_license",
];

describe("consentSources", () => {
  it("is the personal data alone for a DigiD flow", () => {
    expect(consentSources(["personal_data"], false)).toEqual(["personal_data"]);
  });

  it("names the personal data first, then the documents in their order", () => {
    expect(
      consentSources(
        ["drivers_license", "personal_data", "identity_card"],
        false,
      ),
    ).toEqual(["personal_data", "identity_card", "drivers_license"]);
  });

  it("is every document when none is named", () => {
    expect(consentSources([], false)).toEqual(EVERY_DOCUMENT);
  });

  it("adds every document to the personal data a photographed document comes with", () => {
    expect(consentSources(["personal_data"], true)).toEqual([
      "personal_data",
      ...EVERY_DOCUMENT,
    ]);
  });

  it("reads the document codes older editors saved", () => {
    expect(consentSources(["P", "ID"], false)).toEqual([
      "passport",
      "identity_card",
    ]);
  });
});

describe("hostedConsentBasis", () => {
  it("rebuilds the sources from the hosted page's flow", () => {
    expect(
      hostedConsentBasis({
        yiviPersonalData: true,
        scanDocuments: [],
        documentPhoto: false,
        bsn: true,
      }),
    ).toEqual({ sources: ["personal_data"], keepsBsn: true });
    expect(
      hostedConsentBasis({
        yiviPersonalData: true,
        scanDocuments: ["identity_card"],
        documentPhoto: false,
        bsn: false,
      }).sources,
    ).toEqual(["personal_data", "identity_card"]);
  });
});

describe("consentWording", () => {
  it.each<[ConsentSource[], string]>([
    [["personal_data"], "registered"],
    [["personal_data", "identity_card"], "mixed"],
    [["personal_data", "drivers_license"], "mixed"],
    [["passport", "identity_card"], "document"],
    [["passport", "drivers_license"], "document"],
    [["drivers_license"], "licence"],
  ])("words %j as %s", (sources, wording) => {
    expect(consentWording(sources)).toBe(wording);
  });
});

describe("consentItems", () => {
  const registered = (keepsBsn: boolean) => ({
    sources: ["personal_data"] as ConsentSource[],
    keepsBsn,
  });

  it("names the BSN of the personal data when the flow keeps it, requested or not", () => {
    expect(consentItems(["dg1"], registered(true))).toEqual(["dg1", "dg11"]);
    expect(consentItems(["dg1", "dg11"], registered(true))).toEqual([
      "dg1",
      "dg11",
    ]);
  });

  it("leaves the BSN out of the personal data when the flow omits it", () => {
    expect(consentItems(["dg1", "dg11"], registered(false))).toEqual(["dg1"]);
  });

  it("leaves out the chip items when no source has a chip", () => {
    expect(
      consentItems(["dg1", "dg2", "chip_checks", "selfie"], registered(false)),
    ).toEqual(["dg1", "selfie"]);
  });

  it("keeps the chip items and adds the BSN for the personal data or a document", () => {
    expect(
      consentItems(["dg1", "dg2"], {
        sources: ["personal_data", "passport"],
        keepsBsn: true,
      }),
    ).toEqual(["dg1", "dg11", "dg2"]);
  });

  it("lists a document flow's items as requested", () => {
    expect(
      consentItems(["dg1", "dg2"], { sources: ["passport"], keepsBsn: true }),
    ).toEqual(["dg1", "dg2"]);
  });
});

describe("consentDetailKey", () => {
  it("words the document data by the sources, without the date of birth under an age check", () => {
    expect(consentDetailKey("dg1", ["dg1"], "mixed", CARDS, true)).toBe(
      "dg1.mixed",
    );
    expect(
      consentDetailKey("dg1", ["dg1", "age_over"], "licence", CARDS, true),
    ).toBe("dg1WithoutBirthDate.licence");
  });

  it("says where a mixed flow's BSN comes from", () => {
    expect(
      consentDetailKey("dg11", ["dg1", "dg11"], "mixed", CARDS, true),
    ).toBe("dg11.mixed");
    expect(consentDetailKey("dg11", ["dg1"], "mixed", CARDS, true)).toBe(
      "dg11.mixedBsnOnly",
    );
    expect(consentDetailKey("dg11", ["dg11"], "licence", CARDS, true)).toBe(
      "dg11.document",
    );
  });

  it("leaves the items that do not depend on the source", () => {
    expect(
      consentDetailKey("selfie", ["selfie"], "registered", CARDS, true),
    ).toBeUndefined();
  });
});

describe("the documents the texts name", () => {
  it("are the flow's documents, or every one beside the personal data alone", () => {
    expect(consentDocuments(["personal_data", "identity_card"])).toEqual([
      "identity_card",
    ]);
    expect(consentDocuments(["personal_data"])).toEqual(EVERY_DOCUMENT);
  });

  it("ask a passport's back, its BSN page, only of a flow keeping the BSN", () => {
    const withPassport: DocumentKind[] = ["passport", "identity_card"];
    expect(
      consentDetailKey("document_image", [], "document", CARDS, true),
    ).toBe("photos.sides");
    expect(
      consentDetailKey("document_image", [], "document", withPassport, true),
    ).toBe("photos.sidesWithPassport");
    expect(
      consentDetailKey("document_image", [], "document", withPassport, false),
    ).toBe("photos.sidesPassportFront");
    expect(
      consentDetailKey("document_image", [], "document", ["passport"], false),
    ).toBe("photos.passportFront");
  });
});

describe("consentLabelKey", () => {
  it("names the data personal data wherever the BRP may give it", () => {
    expect(consentLabelKey("dg1", ["dg1"], "registered")).toBe(
      "labels.registeredData",
    );
    expect(consentLabelKey("dg11", ["dg1"], "registered")).toBe("labels.bsn");
    expect(consentLabelKey("dg1", ["dg1"], "mixed")).toBe(
      "labels.registeredData",
    );
    expect(consentLabelKey("dg1", ["dg1"], "document")).toBeUndefined();
  });

  it("names a mixed flow's BSN alone when the place of birth was not requested", () => {
    expect(consentLabelKey("dg11", ["dg1"], "mixed")).toBe("labels.bsn");
    expect(consentLabelKey("dg11", ["dg1", "dg11"], "mixed")).toBeUndefined();
  });
});

describe("the consent texts", () => {
  it("names every source in every language", () => {
    for (const language of SUPPORTED_LANGUAGES) {
      for (const source of ["personal_data", ...EVERY_DOCUMENT]) {
        const key = `identityProofing.onScreen.consent.sources.${source}`;
        expect(i18n.exists(key, { lng: language }), `${language} ${key}`).toBe(
          true,
        );
      }
    }
  });

  it("joins the sources with or, in the page's language", () => {
    expect(joinSources(["a", "b", "c"], "nl")).toBe("a, b of c");
    expect(joinSources(["a", "b"], "en")).toBe("a or b");
  });
});
