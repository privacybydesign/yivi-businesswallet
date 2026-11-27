import {
  ATTRIBUTE_AGE_OVER,
  ATTRIBUTE_DOCUMENT,
  DOCUMENT_KINDS,
  PERSONAL_DATA,
  STEP_DOCUMENT_PHOTO,
  documentKindOf,
} from "./identity-proofing";
import type { DocumentKind } from "./identity-proofing";

// What a person may confirm their identity with: the registered personal data
// (BRP), which the Yivi app shares, or a document.
export type ConsentSource = typeof PERSONAL_DATA | DocumentKind;

// What each source gives. The consent screen's texts follow from the sources
// a flow accepts, so a new source is one entry here.
interface SourceTraits {
  // The BRP's data rather than a document's: no document number or expiry.
  registered: boolean;
  // A chip with a portrait and checks.
  chip: boolean;
  nationality: boolean;
}

const SOURCE_TRAITS: Record<ConsentSource, SourceTraits> = {
  personal_data: { registered: true, chip: false, nationality: true },
  passport: { registered: false, chip: true, nationality: true },
  identity_card: { registered: false, chip: true, nationality: true },
  residence_permit: { registered: false, chip: true, nationality: true },
  drivers_license: { registered: false, chip: true, nationality: false },
};

// How the consent screen words the data: from the BRP alone, from documents
// alone (a driving licence alone has no nationality), or from either.
export type ConsentWording = "registered" | "document" | "licence" | "mixed";

// What the consent screen is built from: the sources a flow accepts, and
// whether it keeps the BSN, which the personal data shares under the BSN
// policy alone.
export interface ConsentBasis {
  sources: ConsentSource[];
  keepsBsn: boolean;
}

// The sources of a flow's accepted document types. No document named is any
// document, unless the personal data is the only one and no document is
// photographed.
export function consentSources(
  acceptedDocumentTypes: readonly string[],
  documentPhoto: boolean,
): ConsentSource[] {
  const registered = acceptedDocumentTypes.some(
    (type) => type.toLowerCase() === PERSONAL_DATA,
  );
  const named = new Set(
    acceptedDocumentTypes
      .map((type) => documentKindOf(type))
      .filter((kind) => kind !== undefined),
  );
  const anyDocument = named.size === 0 && (!registered || documentPhoto);
  const documents = DOCUMENT_KINDS.filter(
    (kind) => anyDocument || named.has(kind),
  );
  return registered ? [PERSONAL_DATA, ...documents] : documents;
}

// A stored flow's basis (proofing.takesDocumentPhoto, keepsBSN).
export function consentBasisOf(flow: {
  acceptedDocumentTypes?: readonly string[];
  steps?: readonly string[];
  bsnPolicy?: string;
}): ConsentBasis {
  return {
    sources: consentSources(
      flow.acceptedDocumentTypes ?? [],
      (flow.steps ?? []).includes(STEP_DOCUMENT_PHOTO),
    ),
    keepsBsn: keepsBsn(flow.bsnPolicy),
  };
}

// Whether a BSN policy keeps the BSN, in full or masked.
export function keepsBsn(bsnPolicy: string | undefined): boolean {
  return bsnPolicy === "retrieve" || bsnPolicy === "mask";
}

// The hosted page's basis: its flow carries the photographed documents and
// the personal data apart.
export function hostedConsentBasis(flow: {
  yiviPersonalData: boolean;
  scanDocuments?: readonly string[];
  documentPhoto: boolean;
  bsn: boolean;
}): ConsentBasis {
  const accepted = [...(flow.scanDocuments ?? [])];
  if (flow.yiviPersonalData) {
    accepted.push(PERSONAL_DATA);
  }
  return {
    sources: consentSources(accepted, flow.documentPhoto),
    keepsBsn: flow.bsn,
  };
}

export function consentWording(
  sources: readonly ConsentSource[],
): ConsentWording {
  const registered = sources.some((s) => SOURCE_TRAITS[s].registered);
  const documents = sources.filter((s) => !SOURCE_TRAITS[s].registered);
  if (registered) {
    return documents.length === 0 ? "registered" : "mixed";
  }
  return documents.length > 0 &&
    documents.every((s) => !SOURCE_TRAITS[s].nationality)
    ? "licence"
    : "document";
}

// The chip's portrait and checks.
const CHIP_ITEMS: ReadonlySet<string> = new Set(["dg2", "chip_checks"]);

// The item the BSN is named under.
const ATTRIBUTE_BSN = "dg11";

// The items the consent screen lists: no chip items when no source has a
// chip, and from the personal data the BSN exactly when the flow keeps it,
// whether or not it was requested.
export function consentItems(
  requested: readonly string[],
  basis: ConsentBasis,
): string[] {
  const chip = basis.sources.some((s) => SOURCE_TRAITS[s].chip);
  const wording = consentWording(basis.sources);
  const items = requested.filter((value) => chip || !CHIP_ITEMS.has(value));
  if (wording === "registered" && !basis.keepsBsn) {
    return items.filter((value) => value !== ATTRIBUTE_BSN);
  }
  const registered = wording === "registered" || wording === "mixed";
  if (registered && basis.keepsBsn && !items.includes(ATTRIBUTE_BSN)) {
    items.splice(items.indexOf(ATTRIBUTE_DOCUMENT) + 1, 0, ATTRIBUTE_BSN);
  }
  return items;
}

// The keys of the texts that depend on the wording, under
// identityProofing.onScreen.consent.
export type ConsentTextKey =
  | `dg1.${ConsentWording}`
  | `dg1WithoutBirthDate.${ConsentWording}`
  | `dg11.${"registered" | "document" | "mixed" | "mixedBsnOnly"}`
  | `ageOver.${"registered" | "document" | "mixed"}`
  | `photos.${"sides" | "sidesWithPassport"}`;

// The text key of an item whose text depends on the sources; undefined for
// one that says the same whatever the source.
export function consentDetailKey(
  value: string,
  requested: readonly string[],
  wording: ConsentWording,
  documents: readonly DocumentKind[],
): ConsentTextKey | undefined {
  if (value === ATTRIBUTE_DOCUMENT) {
    return requested.includes(ATTRIBUTE_AGE_OVER)
      ? `dg1WithoutBirthDate.${wording}`
      : `dg1.${wording}`;
  }
  if (value === ATTRIBUTE_BSN) {
    return bsnDetailKey(requested, wording);
  }
  if (value === ATTRIBUTE_AGE_OVER) {
    return wording === "licence" ? "ageOver.document" : `ageOver.${wording}`;
  }
  if (value === ATTRIBUTE_DOCUMENT_IMAGE) {
    return documents.includes(PASSPORT)
      ? "photos.sidesWithPassport"
      : "photos.sides";
  }
  return undefined;
}

// The BSN item's text: from the BRP, a document, or either, where a mixed
// flow that did not request the place of birth gets the BSN from the BRP
// alone.
function bsnDetailKey(
  requested: readonly string[],
  wording: ConsentWording,
): ConsentTextKey {
  if (wording === "mixed") {
    return requested.includes(ATTRIBUTE_BSN)
      ? "dg11.mixed"
      : "dg11.mixedBsnOnly";
  }
  return wording === "registered" ? "dg11.registered" : "dg11.document";
}

// The item of the document photos.
const ATTRIBUTE_DOCUMENT_IMAGE = "document_image";

// A passport's back is the page with the BSN, which the photos text says.
const PASSPORT: DocumentKind = "passport";

// The documents among the sources, which the texts name; every document
// when the personal data is the only source.
export function consentDocuments(
  sources: readonly ConsentSource[],
): DocumentKind[] {
  const documents = sources.filter(
    (source): source is DocumentKind => !SOURCE_TRAITS[source].registered,
  );
  return documents.length === 0 ? [...DOCUMENT_KINDS] : documents;
}

// The label key of an item the BRP names its own way; undefined keeps the
// item's usual label. The data is personal data wherever the BRP may give
// it, and the BSN alone, without the place of birth, is the BSN item of the
// personal data, or of a mixed flow that did not request it.
export function consentLabelKey(
  value: string,
  requested: readonly string[],
  wording: ConsentWording,
): "labels.registeredData" | "labels.bsn" | undefined {
  const registered = wording === "registered" || wording === "mixed";
  if (registered && value === ATTRIBUTE_DOCUMENT) {
    return "labels.registeredData";
  }
  const bsnAlone =
    wording === "registered" ||
    (wording === "mixed" && !requested.includes(ATTRIBUTE_BSN));
  return value === ATTRIBUTE_BSN && bsnAlone ? "labels.bsn" : undefined;
}

// Names in words, as "a, b or c" in language.
export function joinSources(
  names: readonly string[],
  language: string,
): string {
  return new Intl.ListFormat(language, { type: "disjunction" }).format(names);
}
