import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import type { AuditEvent } from "../api/organization";
import i18n from "../i18n";
import {
  auditActionLabel,
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
    );
    expect(subject).toBe("alice@example.test, bob@example.test");
  });

  it("prefers name/email over recipient when present", () => {
    expect(
      auditSubject(
        event({ after: { name: "Acme", recipient: "acme-ref" } }),
        dateFormatter,
      ),
    ).toBe("Acme");
    expect(
      auditSubject(
        event({ after: { email: "a@b.test", recipient: "acme-ref" } }),
        dateFormatter,
      ),
    ).toBe("a@b.test");
  });
});
