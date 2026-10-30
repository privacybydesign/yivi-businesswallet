import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import type { AuditEvent } from "../api/organization";
import i18n from "../i18n";
import {
  auditActionLabel,
  auditActorLabel,
  auditDetailFields,
  auditSubject,
  auditTargetLabel,
} from "./audit-event";

// The backend is the source of truth for audit action/target identifiers
// (backend/internal/audit/audit.go). auditActionLabel / auditTargetLabel map
// each one to an i18n string and fall back to the raw dotted key in their
// `default` branch. This test parses the Go constants and asserts every one
// resolves to a real translation, so a new backend action can't silently
// regress to rendering its raw key (e.g. "postguard.file_sent") in the UI.

const auditGoPath = fileURLToPath(
  new URL("../../../backend/internal/audit/audit.go", import.meta.url),
);
const source = readFileSync(auditGoPath, "utf8");

// Grab every `Identifier = "value"` const declaration. Actions carry a dot
// (organization.created); targets do not (org_email_settings).
const constValues = [...source.matchAll(/^\s*[A-Z]\w*\s*=\s*"([^"]+)"/gm)].map(
  (m) => m[1],
);
const actions = constValues.filter((v) => v.includes("."));
const targets = constValues.filter((v) => !v.includes("."));

const t = i18n.getFixedT("en");

// A returned label is "unresolved" if it equals the raw key (missing switch
// case) or still looks like an i18n key path (missing en.ts entry).
function isUnresolved(label: string, key: string): boolean {
  return label === key || label.startsWith("auditLog.");
}

describe("audit-event backend/frontend parity", () => {
  it("extracts the constants from audit.go", () => {
    expect(actions.length).toBeGreaterThan(0);
    expect(targets.length).toBeGreaterThan(0);
  });

  it.each(actions)("translates the action %s", (action) => {
    const label = auditActionLabel(action, t);
    expect(isUnresolved(label, action)).toBe(false);
  });

  it.each(targets)("translates the target %s", (target) => {
    const label = auditTargetLabel(target, t);
    expect(isUnresolved(label, target)).toBe(false);
  });
});

describe("auditSubject", () => {
  const dateFormatter = new Intl.DateTimeFormat("en");

  function event(metadata: Record<string, unknown>): AuditEvent {
    return {
      id: "1",
      occurredAt: "2026-07-22T10:00:00Z",
      action: "attestation.issued",
      targetType: "issued_attestation",
      targetId: "att-1",
      metadata,
      actor: null,
    };
  }

  // The issued-attestation create event carries the recipient, not name/email/
  // role, so the subject must resolve to it — otherwise the row renders blank.
  it("uses the recipient for an issued-attestation event", () => {
    const subject = auditSubject(
      event({
        after: {
          schemaVct: "https://example.test/vct",
          recipient: "alice@example.test",
          recipientKind: "external",
          qualified: false,
        },
      }),
      dateFormatter,
      t,
    );
    expect(subject).toBe("alice@example.test");
  });

  // The sent-encrypted-file create event carries the recipient list, not
  // name/email/role, so the subject must join it — otherwise the row is blank.
  it("joins the recipients for a sent-encrypted-file event", () => {
    const subject = auditSubject(
      event({
        after: {
          fileName: "contract.pdf",
          sizeBytes: 1024,
          recipients: ["alice@example.test", "bob@example.test"],
          cryptifyUuid: "abc",
        },
      }),
      dateFormatter,
      t,
    );
    expect(subject).toBe("alice@example.test, bob@example.test");
  });

  it("prefers name/email over recipient when present", () => {
    expect(
      auditSubject(
        event({ after: { name: "Acme", recipient: "acme-ref" } }),
        dateFormatter,
        t,
      ),
    ).toBe("Acme");
    expect(
      auditSubject(
        event({ after: { email: "a@b.test", recipient: "acme-ref" } }),
        dateFormatter,
        t,
      ),
    ).toBe("a@b.test");
  });

  // A proofing outcome names its subject, labels the fields it adds and drops
  // the empty ones, so it never reads as "— → , — → low".
  it("leads a proofing outcome with its subject and labels added fields", () => {
    expect(
      auditSubject(
        event({
          before: {
            status: "in_progress",
            subjectName: "Anna Jansen",
            subjectEmail: "anna@example.test",
          },
          after: {
            status: "approved",
            subjectName: "Anna Jansen",
            subjectEmail: "anna@example.test",
            assuranceLevel: "substantial",
            eidasLevel: "substantial",
            errorCode: "",
          },
        }),
        dateFormatter,
        t,
      ),
    ).toBe(
      "Anna Jansen: in_progress → approved, Assurance: substantial, eIDAS level: substantial",
    );
  });

  it("gives a proofing rejection its reason in words", () => {
    expect(
      auditSubject(
        event({
          before: { status: "in_progress", subjectEmail: "anna@example.test" },
          after: {
            status: "rejected",
            subjectEmail: "anna@example.test",
            errorCode: "DOCUMENT_TYPE_NOT_ACCEPTED",
          },
        }),
        dateFormatter,
        t,
      ),
    ).toBe(
      "anna@example.test: in_progress → rejected, Reason: document type not accepted by the flow",
    );
  });

  it("falls back to the subject's e-mail when sent without a name", () => {
    expect(
      auditSubject(
        event({
          before: { status: "pending", subjectEmail: "anna@example.test" },
          after: { status: "in_progress", subjectEmail: "anna@example.test" },
        }),
        dateFormatter,
        t,
      ),
    ).toBe("anna@example.test: pending → in_progress");
    expect(
      auditSubject(
        event({ after: { flowId: "f1", subjectEmail: "anna@example.test" } }),
        dateFormatter,
        t,
      ),
    ).toBe("anna@example.test");
  });
});

describe("auditActorLabel", () => {
  it("names an API key by its prefix", () => {
    expect(auditActorLabel("api_key:yp_live_ab12", t)).toBe(
      t("auditLog.apiKeyActor", { prefix: "yp_live_ab12" }),
    );
    expect(auditActorLabel("hosted_link", t)).toBe(
      t("auditLog.hostedLinkActor"),
    );
    // An admin who acted on a member's request, unnamed in their timeline.
    expect(auditActorLabel("admin", t)).toBe(t("auditLog.adminActor"));
    expect(auditActorLabel("app:idem_app", t)).toBe(
      t("identityProofing.methods.idemApp"),
    );
    expect(auditActorLabel(null, t)).toBeNull();
    expect(auditActorLabel(undefined, t)).toBeNull();
  });
});

describe("auditDetailFields", () => {
  const dateFormatter = new Intl.DateTimeFormat("en", { timeZone: "UTC" });

  function event(metadata: Record<string, unknown>): AuditEvent {
    return {
      id: "1",
      occurredAt: "2026-07-22T10:00:00Z",
      action: "identity_proofing.rejected",
      targetType: "identity_proofing_request",
      targetId: "req-1",
      metadata,
      actor: null,
    };
  }

  // An update lists every field: the unchanged ones by their value, the
  // changed ones before and after, and none empty on both sides.
  it("shows an update's changed fields before and after", () => {
    expect(
      auditDetailFields(
        event({
          before: {
            subjectName: "Anna Jansen",
            status: "in_progress",
            errorCode: "",
          },
          after: {
            subjectName: "Anna Jansen",
            status: "rejected",
            errorCode: "",
          },
        }),
        dateFormatter,
        t,
      ),
    ).toEqual([
      { key: "subjectName", label: "subjectName", value: "Anna Jansen" },
      {
        key: "status",
        label: "status",
        value: "rejected",
        before: "in_progress",
      },
    ]);
  });

  // A known field reads by its label and value, an unknown one by its key; a
  // nested value is compact and a timestamp formatted.
  it("labels known fields and formats nested values and dates", () => {
    const fields = auditDetailFields(
      event({
        after: {
          method: "idem_app",
          roles: ["admin", "member"],
          sentAt: "2026-07-22T10:00:00Z",
          note: null,
        },
      }),
      dateFormatter,
      t,
    );
    expect(fields.map((f) => [f.label, f.value])).toEqual([
      [t("auditLog.fields.method"), t("identityProofing.methods.idemApp")],
      ["roles", '["admin","member"]'],
      ["sentAt", dateFormatter.format(new Date("2026-07-22T10:00:00Z"))],
    ]);
  });

  it("adds metadata outside the snapshot", () => {
    expect(
      auditDetailFields(
        event({ before: { name: "Sales" }, via: "api" }),
        dateFormatter,
        t,
      ),
    ).toEqual([
      { key: "name", label: "name", value: "Sales" },
      { key: "via", label: "via", value: "api" },
    ]);
  });

  it("is empty without metadata", () => {
    expect(auditDetailFields(event({}), dateFormatter, t)).toEqual([]);
  });
});
