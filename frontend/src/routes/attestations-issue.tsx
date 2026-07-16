import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import QRCode from "qrcode";
import * as React from "react";
import type {
  AttestationTemplate,
  IssueAttestationInput,
  IssueResult,
} from "../api/attestations";
import {
  useIssueAttestationMutation,
  useIssuedAttestationQuery,
} from "../api/attestations.queries";
import { useQerdsContactsQuery } from "../api/qerds.queries";
import { useOrganizationMembersQuery } from "../api/organization.queries";
import { fullName } from "../lib/name";
import { Button, Icon, Modal, Outcome } from "../ui";
import { apiErrorCode, control } from "../lib/attestation-form";
import { Field } from "./attestations-fields";

const QR_SIZE = 220;
const MEMBER_PAGE_SIZE = 200;
const EMAIL_ATTRIBUTE_KEY = "email";
const CLAIMED_STATUS = "claimed";
const RECIPIENT_MEMBER = "member";
const RECIPIENT_EXTERNAL = "external";
const RECIPIENT_ORGANIZATION = "organization";
const SUBJECT_ORGANIZATION = "organization";
// Plausible address check only; the backend is the authority.
const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

type RecipientKind = typeof RECIPIENT_MEMBER | typeof RECIPIENT_EXTERNAL;

// The wizard phases, in order. "review" is a summary before submitting; "offer"
// is the post-submit QR + claim-polling screen.
type Phase = "template" | "recipient" | "attributes" | "review" | "offer";

const STEP_ORDER = ["template", "recipient", "attributes"] as const;
type Step = (typeof STEP_ORDER)[number];

const STEP_LABEL_KEYS = {
  template: "attestations.wizard.steps.template",
  recipient: "attestations.wizard.steps.recipient",
  attributes: "attestations.wizard.steps.attributes",
} as const;

function isStep(phase: Phase): phase is Step {
  return (
    phase === "template" || phase === "recipient" || phase === "attributes"
  );
}

function defaultsFor(template: AttestationTemplate): Record<string, string> {
  const values: Record<string, string> = {};
  for (const attribute of template.attributes) {
    values[attribute.key] = template.defaultAttributes?.[attribute.key] ?? "";
  }
  return values;
}

function inlineError(error: Error, t: TFunction): string {
  const code = apiErrorCode(error);
  if (code === "unknown_attribute") {
    return t("attestations.wizard.unknownAttribute");
  }
  if (code === "missing_attribute") {
    return t("attestations.wizard.missingAttribute");
  }
  return error.message;
}

interface Props {
  slug: string;
  templates: AttestationTemplate[];
  // Template to preselect (e.g. from a template card's "Issue" action).
  initialTemplate?: AttestationTemplate;
  onClose: () => void;
}

export function AttestationIssueWizard({
  slug,
  templates,
  initialTemplate,
  onClose,
}: Props): React.JSX.Element {
  const { t } = useTranslation();
  const issue = useIssueAttestationMutation(slug);

  const members = useOrganizationMembersQuery(
    slug,
    { status: "active", limit: MEMBER_PAGE_SIZE },
    true,
  );
  const contacts = useQerdsContactsQuery(slug);

  const [phase, setPhase] = useState<Phase>(
    initialTemplate ? "recipient" : "template",
  );
  const [templateId, setTemplateId] = useState(initialTemplate?.id ?? "");
  const [recipientKind, setRecipientKind] =
    useState<RecipientKind>(RECIPIENT_MEMBER);
  const [memberUserId, setMemberUserId] = useState("");
  const [externalEmail, setExternalEmail] = useState("");
  const [contactAddress, setContactAddress] = useState("");
  const [values, setValues] = useState<Record<string, string>>(
    initialTemplate ? defaultsFor(initialTemplate) : {},
  );
  const [result, setResult] = useState<IssueResult | null>(null);
  const [qrDataUrl, setQrDataUrl] = useState("");
  const [attempted, setAttempted] = useState(false);

  const template = useMemo(
    () => templates.find((tpl) => tpl.id === templateId),
    [templates, templateId],
  );

  const memberEntries = (members.data?.entries ?? []).filter(
    (entry) => entry.userId !== null,
  );
  const selectedMember = memberEntries.find(
    (entry) => entry.userId === memberUserId,
  );

  // Poll the issued attestation until the recipient claims it.
  const issued = useIssuedAttestationQuery(
    slug,
    result?.id ?? "",
    result !== null,
  );
  const claimed = issued.data?.status === CLAIMED_STATUS;

  // Render the offer link as a QR once issued.
  useEffect(() => {
    if (!result) {
      return;
    }
    let cancelled = false;
    void QRCode.toDataURL(result.offerUri, { margin: 1, width: QR_SIZE })
      .then((url) => {
        if (!cancelled) {
          setQrDataUrl(url);
        }
      })
      .catch(() => {
        // The open-wallet button still works even if QR rendering fails.
      });
    return () => {
      cancelled = true;
    };
  }, [result]);

  function chooseTemplate(id: string): void {
    setTemplateId(id);
    const picked = templates.find((tpl) => tpl.id === id);
    setValues(picked ? defaultsFor(picked) : {});
  }

  function selectMember(userId: string): void {
    setMemberUserId(userId);
    const entry = memberEntries.find((m) => m.userId === userId);
    if (entry && template) {
      setValues((current) => {
        const next = { ...current };
        if (EMAIL_ATTRIBUTE_KEY in next && next[EMAIL_ATTRIBUTE_KEY] === "") {
          next[EMAIL_ATTRIBUTE_KEY] = entry.email;
        }
        return next;
      });
    }
  }

  // Organization-subject templates are delivered over QERDS to a contact in the
  // address book; natural-person templates go to a member or external e-mail.
  const isOrganization = template?.subjectType === SUBJECT_ORGANIZATION;

  const recipientRef = isOrganization
    ? contactAddress
    : recipientKind === RECIPIENT_MEMBER
      ? (selectedMember?.email ?? "")
      : externalEmail.trim();

  const recipientValid = isOrganization
    ? contactAddress !== ""
    : recipientKind === RECIPIENT_MEMBER
      ? memberUserId !== ""
      : EMAIL_PATTERN.test(externalEmail.trim());

  function submit(): void {
    if (!template || issue.isPending) {
      return;
    }
    const attributes: Record<string, string> = {};
    for (const attribute of template.attributes) {
      attributes[attribute.key] = values[attribute.key] ?? "";
    }
    const recipient: IssueAttestationInput["recipient"] = isOrganization
      ? { kind: RECIPIENT_ORGANIZATION, ref: recipientRef }
      : recipientKind === RECIPIENT_MEMBER
        ? { kind: RECIPIENT_MEMBER, userId: memberUserId, ref: recipientRef }
        : { kind: RECIPIENT_EXTERNAL, ref: recipientRef };
    const input: IssueAttestationInput = {
      templateId: template.id,
      recipient,
      attributes,
    };
    issue.mutate(input, {
      onSuccess: (data) => {
        setResult(data);
        setPhase("offer");
      },
    });
  }

  const stepPhase: Step = isStep(phase) ? phase : "attributes";
  const currentStepIndex = STEP_ORDER.indexOf(stepPhase);

  const title =
    phase === "offer"
      ? t("attestations.wizard.offerTitle")
      : t("attestations.wizard.title");

  return (
    <Modal
      title={title}
      closeLabel={t("common.cancel")}
      onClose={onClose}
      wide
      footer={phase === "offer" ? undefined : renderFooter()}
    >
      {phase !== "offer" && (
        <ol className="mb-5 flex items-center gap-2">
          {STEP_ORDER.map((step, index) => {
            const done = index < currentStepIndex;
            const active = index === currentStepIndex;
            return (
              <li key={step} className="flex flex-1 items-center gap-2">
                <span
                  className={[
                    "flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-[12px] font-semibold",
                    active
                      ? "bg-primary text-primary-fg"
                      : done
                        ? "bg-success-bg text-success"
                        : "bg-surface-3 text-ink-soft",
                  ].join(" ")}
                >
                  {done ? <Icon name="valid" size={13} /> : index + 1}
                </span>
                <span
                  className={[
                    "text-[12.5px] font-semibold",
                    active ? "text-ink" : "text-ink-soft",
                  ].join(" ")}
                >
                  {t(STEP_LABEL_KEYS[step])}
                </span>
              </li>
            );
          })}
        </ol>
      )}

      {phase === "template" && (
        <div className="flex flex-col gap-2">
          {templates.length === 0 ? (
            <p className="text-ink-soft text-[13.5px]">
              {t("attestations.wizard.noTemplates")}
            </p>
          ) : (
            templates.map((tpl) => (
              <button
                key={tpl.id}
                type="button"
                onClick={() => chooseTemplate(tpl.id)}
                className={[
                  "rounded-yivi flex flex-col items-start gap-0.5 border p-3 text-left transition-colors",
                  tpl.id === templateId
                    ? "border-primary bg-highlight"
                    : "border-line hover:bg-surface-3",
                ].join(" ")}
              >
                <span className="text-ink font-semibold">{tpl.name}</span>
                <span className="text-ink-soft font-mono text-[12px]">
                  {tpl.vct}
                </span>
              </button>
            ))
          )}
        </div>
      )}

      {phase === "recipient" && isOrganization && (
        <div className="flex flex-col gap-4">
          {contacts.isPending ? (
            <p className="text-ink-soft text-[13.5px]">{t("common.loading")}</p>
          ) : (contacts.data?.length ?? 0) === 0 ? (
            <p className="text-ink-soft text-[13.5px]">
              {t("attestations.wizard.noContacts")}{" "}
              <Link
                to={`/${slug}/qerds/contacts`}
                className="text-link font-semibold hover:underline"
              >
                {t("attestations.wizard.manageContacts")}
              </Link>
            </p>
          ) : (
            <Field
              id="wizard-contact"
              label={t("attestations.wizard.selectContact")}
              required
              error={
                attempted && !recipientValid
                  ? t("attestations.wizard.recipientRequired")
                  : undefined
              }
            >
              <select
                id="wizard-contact"
                className={`${control(attempted && !recipientValid)} h-9`}
                value={contactAddress}
                onChange={(event) => setContactAddress(event.target.value)}
              >
                <option value="">
                  {t("attestations.wizard.selectContact")}
                </option>
                {(contacts.data ?? []).map((contact) => (
                  <option key={contact.id} value={contact.address}>
                    {contact.name} — {contact.address}
                  </option>
                ))}
              </select>
            </Field>
          )}
        </div>
      )}

      {phase === "recipient" && !isOrganization && (
        <div className="flex flex-col gap-4">
          <div className="bg-surface-3 rounded-yivi inline-flex gap-1 self-start p-[3px]">
            {[RECIPIENT_MEMBER, RECIPIENT_EXTERNAL].map((kind) => (
              <button
                key={kind}
                type="button"
                onClick={() => setRecipientKind(kind as RecipientKind)}
                className={[
                  "h-[26px] cursor-pointer rounded-md px-2.5 text-[12.5px] font-semibold transition-colors",
                  recipientKind === kind
                    ? "bg-surface text-ink shadow-sm"
                    : "text-ink-soft hover:text-ink",
                ].join(" ")}
              >
                {kind === RECIPIENT_MEMBER
                  ? t("attestations.wizard.recipientMember")
                  : t("attestations.wizard.recipientExternal")}
              </button>
            ))}
          </div>

          {recipientKind === RECIPIENT_MEMBER ? (
            <Field
              id="wizard-member"
              label={t("attestations.wizard.selectMember")}
              required
              error={
                attempted && !recipientValid
                  ? t("attestations.wizard.recipientRequired")
                  : undefined
              }
            >
              <select
                id="wizard-member"
                className={`${control(attempted && !recipientValid)} h-9`}
                value={memberUserId}
                onChange={(event) => selectMember(event.target.value)}
              >
                <option value="">
                  {t("attestations.wizard.selectMember")}
                </option>
                {memberEntries.map((entry) => (
                  <option key={entry.userId} value={entry.userId ?? ""}>
                    {fullName(entry)} — {entry.email}
                  </option>
                ))}
              </select>
            </Field>
          ) : (
            <Field
              id="wizard-external"
              label={t("attestations.wizard.externalEmail")}
              required
              error={
                attempted && !recipientValid
                  ? t("attestations.wizard.recipientRequired")
                  : undefined
              }
            >
              <input
                id="wizard-external"
                type="email"
                className={`${control(attempted && !recipientValid)} h-9`}
                value={externalEmail}
                onChange={(event) => setExternalEmail(event.target.value)}
                placeholder={t("attestations.wizard.externalPlaceholder")}
              />
            </Field>
          )}
        </div>
      )}

      {phase === "attributes" && template && (
        <div className="flex flex-col gap-3">
          {template.attributes.length === 0 ? (
            <p className="text-ink-soft text-[13.5px]">
              {t("attestations.wizard.noAttributes")}
            </p>
          ) : (
            template.attributes.map((attribute) => (
              <Field
                key={attribute.key}
                id={`wizard-attr-${attribute.key}`}
                label={attribute.label || attribute.key}
                required={attribute.required}
                error={
                  attempted &&
                  attribute.required &&
                  (values[attribute.key] ?? "").trim() === ""
                    ? t("attestations.wizard.attributeRequired")
                    : undefined
                }
              >
                <input
                  id={`wizard-attr-${attribute.key}`}
                  className={`${control(
                    attempted &&
                      attribute.required &&
                      (values[attribute.key] ?? "").trim() === "",
                  )} h-9`}
                  value={values[attribute.key] ?? ""}
                  onChange={(event) =>
                    setValues((current) => ({
                      ...current,
                      [attribute.key]: event.target.value,
                    }))
                  }
                />
              </Field>
            ))
          )}
        </div>
      )}

      {phase === "review" && template && (
        <div className="flex flex-col gap-4">
          <dl className="flex flex-col gap-2 text-[13.5px]">
            <div className="flex justify-between gap-4">
              <dt className="text-ink-soft">
                {t("attestations.wizard.reviewTemplate")}
              </dt>
              <dd className="text-ink font-semibold">{template.name}</dd>
            </div>
            <div className="flex justify-between gap-4">
              <dt className="text-ink-soft">
                {t("attestations.wizard.reviewRecipient")}
              </dt>
              <dd className="text-ink font-semibold">{recipientRef}</dd>
            </div>
          </dl>
          <div className="border-line flex flex-col gap-1.5 border-t pt-3">
            {template.attributes.map((attribute) => (
              <div
                key={attribute.key}
                className="flex justify-between gap-4 text-[13px]"
              >
                <span className="text-ink-soft">
                  {attribute.label || attribute.key}
                </span>
                <span className="text-ink font-mono">
                  {values[attribute.key] || "—"}
                </span>
              </div>
            ))}
          </div>
          {issue.isError && issue.error && (
            <p
              role="alert"
              className="rounded-yivi bg-error-bg text-error px-3 py-2 text-[13px]"
            >
              {t("attestations.wizard.error", {
                message: inlineError(issue.error, t),
              })}
            </p>
          )}
        </div>
      )}

      {phase === "offer" && result && (
        <div className="flex flex-col items-center gap-4">
          {claimed ? (
            <Outcome
              tone="success"
              icon="valid"
              title={t("attestations.wizard.claimedTitle")}
              message={t("attestations.wizard.claimedHint")}
              action={
                <Button onClick={onClose}>
                  {t("attestations.wizard.done")}
                </Button>
              }
            />
          ) : (
            <>
              <p className="text-ink-soft text-center text-[13px]">
                {t("attestations.wizard.offerHint")}
              </p>
              <div
                className="border-line-strong bg-surface rounded-yivi flex items-center justify-center border"
                style={{ width: QR_SIZE, height: QR_SIZE }}
              >
                {qrDataUrl ? (
                  <img
                    src={qrDataUrl}
                    alt=""
                    width={QR_SIZE}
                    height={QR_SIZE}
                    className="rounded-yivi"
                  />
                ) : (
                  <span
                    aria-hidden="true"
                    className="text-muted h-8 w-8 animate-spin rounded-full border-2 border-current border-t-transparent"
                  />
                )}
              </div>
              {result.txCode && (
                <p className="text-ink text-center text-[13px]">
                  {t("attestations.wizard.txCode", { code: result.txCode })}
                </p>
              )}
              <a
                href={result.offerUri}
                className="rounded-yivi font-display bg-primary text-primary-fg hover:bg-primary-hover inline-flex h-11 items-center justify-center px-[18px] text-[15px] font-semibold"
              >
                {t("attestations.wizard.openWallet")}
              </a>
              <p className="text-muted text-center text-[12px]">
                {t("attestations.wizard.waiting")}
              </p>
            </>
          )}
        </div>
      )}
    </Modal>
  );

  function renderFooter(): React.JSX.Element {
    const backTarget: Partial<Record<Phase, Phase>> = {
      recipient: "template",
      attributes: "recipient",
      review: "attributes",
    };
    const back = backTarget[phase];
    return (
      <>
        {back ? (
          <Button variant="secondary" onClick={() => setPhase(back)}>
            {t("attestations.wizard.back")}
          </Button>
        ) : (
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
        )}
        {phase === "review" ? (
          <Button onClick={submit} loading={issue.isPending}>
            {t("attestations.wizard.submit")}
          </Button>
        ) : (
          <Button onClick={next} disabled={!canAdvance()}>
            {t("attestations.wizard.next")}
          </Button>
        )}
      </>
    );
  }

  function canAdvance(): boolean {
    if (phase === "template") {
      return template !== undefined;
    }
    return true;
  }

  function next(): void {
    if (phase === "template") {
      if (template) {
        setPhase("recipient");
      }
      return;
    }
    if (phase === "recipient") {
      setAttempted(true);
      if (recipientValid) {
        setAttempted(false);
        setPhase("attributes");
      }
      return;
    }
    if (phase === "attributes" && template) {
      setAttempted(true);
      const missing = template.attributes.some(
        (attribute) =>
          attribute.required && (values[attribute.key] ?? "").trim() === "",
      );
      if (!missing) {
        setAttempted(false);
        setPhase("review");
      }
    }
  }
}
